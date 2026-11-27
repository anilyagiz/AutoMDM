package apple_mdm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anilyagiz/automdm/v4/ee/server/service/digicert"
	"github.com/anilyagiz/automdm/v4/ee/server/service/ejbca"
	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mdm/profiles"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/variables"
	"github.com/google/uuid"
)

// EnqueueResult holds the results of profile enqueue operations.
type EnqueueResult struct {
	// FailedCmdUUIDs maps command UUIDs that failed to enqueue to their errors.
	FailedCmdUUIDs map[string]error
	// SucceededCmdUUIDs contains the command UUIDs that were enqueued successfully.
	SucceededCmdUUIDs []string
}

func ProcessAndEnqueueProfiles(ctx context.Context,
	ds automdm.Datastore,
	logger *slog.Logger,
	appConfig *automdm.AppConfig,
	commander *MDMAppleCommander,
	installTargets, removeTargets map[string]*automdm.CmdTarget,
	hostProfilesToInstallMap map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload,
	userEnrollmentsToHostUUIDsMap map[string]string,
	profileContents map[string]mobileconfig.Mobileconfig,
) (*EnqueueResult, error) {
	// Grab the contents of all the profiles we need to install, if not already provided.
	if profileContents == nil {
		profileUUIDs := make([]string, 0, len(installTargets))
		for pUUID := range installTargets {
			profileUUIDs = append(profileUUIDs, pUUID)
		}

		var err error
		profileContents, err = ds.GetMDMAppleProfilesContents(ctx, profileUUIDs)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "get profile contents")
		}
	}

	groupedCAs, err := ds.GetGroupedCertificateAuthorities(ctx, true)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting grouped certificate authorities")
	}

	// Insert variables into profile contents of install targets. Variables may be host-specific.
	err = preprocessProfileContents(ctx, appConfig, ds,
		scep.NewSCEPConfigService(logger, nil),
		digicert.NewService(digicert.WithLogger(logger)),
		ejbca.NewService(ejbca.WithLogger(logger)),
		logger, installTargets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	if err != nil {
		return nil, err
	}

	// Find the profiles containing secret variables.
	profilesWithSecrets, err := automdm.FindProfilesWithSecrets(ctx, logger, installTargets, profileContents)
	if err != nil {
		return nil, err
	}

	type remoteResult struct {
		Err     error
		CmdUUID string
	}

	// Send the install/remove commands for each profile.
	var wgProd, wgCons sync.WaitGroup
	ch := make(chan remoteResult)

	execCmd := func(profUUID string, target *automdm.CmdTarget, op automdm.MDMOperationType) {
		defer wgProd.Done()

		var err error
		switch op {
		case automdm.MDMOperationTypeInstall:
			if _, ok := profilesWithSecrets[profUUID]; ok {
				err = commander.EnqueueCommandInstallProfileWithSecrets(ctx, target.EnrollmentIDs, profileContents[profUUID], target.CmdUUID, target.ProfileName)
			} else {
				err = commander.InstallProfile(ctx, target.EnrollmentIDs, profileContents[profUUID], target.CmdUUID, target.ProfileName)
			}
		case automdm.MDMOperationTypeRemove:
			err = commander.RemoveProfile(ctx, target.EnrollmentIDs, target.ProfileIdentifier, target.CmdUUID, target.ProfileName)
		}

		// Determine whether the command was enqueued (even if push notification failed).
		var e *APNSDeliveryError
		switch {
		case errors.As(err, &e):
			logger.DebugContext(ctx, "failed sending push notifications, profiles still enqueued", "details", err)
			ch <- remoteResult{nil, target.CmdUUID}
			// this is fine to pass as success here, since we have sent the command but just didn't notify the client, but when the client checks back in it will process this profile.
		case err != nil:
			logger.ErrorContext(ctx, fmt.Sprintf("enqueue command to %s profiles", op), "details", err)
			ch <- remoteResult{err, target.CmdUUID}
		default:
			ch <- remoteResult{nil, target.CmdUUID}
		}

	}
	for profUUID, target := range installTargets {
		wgProd.Add(1)
		go execCmd(profUUID, target, automdm.MDMOperationTypeInstall)
	}
	for profUUID, target := range removeTargets {
		wgProd.Add(1)
		go execCmd(profUUID, target, automdm.MDMOperationTypeRemove)
	}

	result := &EnqueueResult{
		FailedCmdUUIDs:    make(map[string]error),
		SucceededCmdUUIDs: []string{},
	}

	wgCons.Go(func() {
		for resp := range ch {
			if resp.Err == nil {
				result.SucceededCmdUUIDs = append(result.SucceededCmdUUIDs, resp.CmdUUID)
			} else {
				result.FailedCmdUUIDs[resp.CmdUUID] = resp.Err
			}
		}
	})

	wgProd.Wait()
	close(ch) // done sending at this point, this triggers end of for loop in consumer
	wgCons.Wait()
	return result, nil
}

func preprocessProfileContents(
	ctx context.Context,
	appConfig *automdm.AppConfig,
	ds automdm.Datastore,
	scepConfig automdm.SCEPConfigService,
	digiCertService automdm.DigiCertService,
	ejbcaService automdm.EJBCAService,
	logger *slog.Logger,
	targets map[string]*automdm.CmdTarget,
	profileContents map[string]mobileconfig.Mobileconfig,
	hostProfilesToInstallMap map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload,
	userEnrollmentsToHostUUIDsMap map[string]string,
	groupedCAs *automdm.GroupedCertificateAuthorities,
) error {
	// This method replaces AutoMDM variables ($AUTOMDM_VAR_<NAME>) in the profile
	// contents, generating a unique profile for each host. For a 2KB profile and
	// 30K hosts, this method may generate ~60MB of profile data in memory.

	var (
		// Copy of NDES SCEP config which will contain unencrypted password, if needed
		ndesConfig    *automdm.NDESSCEPProxyCA
		pkcs12CAs     = make(map[string]pkcs12CA) // the CAs of the profile that deliver a PKCS#12, by pkcs12CAVariable.cacheKey
		customSCEPCAs map[string]*automdm.CustomSCEPProxyCA
		smallstepCAs  map[string]*automdm.SmallstepSCEPProxyCA
	)

	pkcs12CATypes := newPKCS12CATypes(digiCertService, ejbcaService)

	// this is used to cache the host ID corresponding to the UUID, so we don't
	// need to look it up more than once per host.
	hostIDForUUIDCache := make(map[string]uint)

	var addedTargets map[string]*automdm.CmdTarget
	for profUUID, target := range targets {
		contents, ok := profileContents[profUUID]
		if !ok {
			// This should never happen
			continue
		}

		// Check if AutoMDM variables or custom host vitals are present.
		contentsStr := string(contents)
		automdmVars := variables.Find(contentsStr)
		hasHostVitals := len(automdm.FindCustomHostVitalIDs(contentsStr)) > 0
		if len(automdmVars) == 0 && !hasHostVitals {
			continue
		}

		// The PSSO device registration token is host-independent: it resolves to
		// the same $AUTOMDM_HOST_SECRET_ placeholder for every host, which is then
		// expanded into a per-host, AutoMDM-signed JWT at command-delivery time
		// (see ExpandHostSecrets). Substitute it once on the shared contents. If
		// it is the only AutoMDM variable, the contents are identical for every
		// host, so we skip the per-host profile fan-out and send a single shared
		// command — the per-host token is injected at delivery time.
		if slices.Contains(automdmVars, string(automdm.AutoMDMVarPSSODeviceRegistrationToken)) {
			contentsStr = profiles.ReplaceAutoMDMVariableInXML(
				automdm.AutoMDMVarPSSODeviceRegistrationTokenRegexp, contentsStr,
				"$"+automdm.HostSecretPrefix+automdm.HostSecretPSSODeviceRegistrationToken)
			automdmVars = slices.DeleteFunc(automdmVars, func(v string) bool {
				return v == string(automdm.AutoMDMVarPSSODeviceRegistrationToken)
			})
			if len(automdmVars) == 0 {
				profileContents[profUUID] = mobileconfig.Mobileconfig(contentsStr)
				continue
			}
		}

		// The conditional access SCEP URL and challenge placeholder are the same for every host, so they're substituted
		// once on the shared contents. The challenge is created for the host when the command is sent (see
		// ExpandHostSecrets), so the stored command never holds one.
		isConditionalAccessVar := func(v string) bool {
			return v == string(automdm.AutoMDMVarConditionalAccessSCEPURL) || v == string(automdm.AutoMDMVarConditionalAccessSCEPChallenge)
		}
		if slices.ContainsFunc(automdmVars, isConditionalAccessVar) {
			scepURL, err := url.JoinPath(appConfig.ServerSettings.ServerURL, automdm.ConditionalAccessSCEPPath)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "building conditional access SCEP URL")
			}
			contentsStr = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarConditionalAccessSCEPURLRegexp, contentsStr, scepURL)
			contentsStr = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarConditionalAccessSCEPChallengeRegexp, contentsStr,
				automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge))
			automdmVars = slices.DeleteFunc(automdmVars, isConditionalAccessVar)
		}

		var variablesUpdatedAt *time.Time

		// Do common validation that applies to all hosts in the target
		valid := true
		// Check if there are any CA variables first so that if a non-CA variable causes
		// preprocessing to fail, we still set the variablesUpdatedAt timestamp so that
		// validation works as expected
		// In the future we should expand variablesUpdatedAt logic to include non-CA variables as
		// well
		for _, automdmVar := range automdmVars {
			_, isPKCS12CA := findPKCS12CAVariable(pkcs12CATypes, automdmVar)
			if isPKCS12CA || automdmVar == string(automdm.AutoMDMVarSCEPRenewalID) || automdmVar == string(automdm.AutoMDMVarCertificateRenewalID) ||
				automdmVar == string(automdm.AutoMDMVarNDESSCEPChallenge) || automdmVar == string(automdm.AutoMDMVarNDESSCEPProxyURL) || automdmVar == string(automdm.AutoMDMVarHostUUID) ||
				strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix)) || strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix)) ||
				strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)) || strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)) {
				// Give a few minutes leeway to account for clock skew
				variablesUpdatedAt = ptr.Time(time.Now().UTC().Add(-3 * time.Minute))
				break
			}
		}

	initialAutoMDMVarLoop:
		for _, automdmVar := range automdmVars {
			pkcs12Var, isPKCS12CA := findPKCS12CAVariable(pkcs12CATypes, automdmVar)
			switch {
			case automdmVar == string(automdm.AutoMDMVarNDESSCEPChallenge) || automdmVar == string(automdm.AutoMDMVarNDESSCEPProxyURL):
				configured, err := isNDESSCEPConfigured(ctx, logger, groupedCAs, ds, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID, target)
				if err != nil {
					return ctxerr.Wrap(ctx, err, "checking NDES SCEP configuration")
				}
				if !configured {
					valid = false
					break initialAutoMDMVarLoop
				}

			case automdmVar == string(automdm.AutoMDMVarHostHardwareSerial) || automdmVar == string(automdm.AutoMDMVarHostPlatform) ||
				automdmVar == string(automdm.AutoMDMVarHostEndUserIDPUsername) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart) ||
				automdmVar == string(automdm.AutoMDMVarHostEndUserIDPGroups) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPDepartment) ||
				automdmVar == string(automdm.AutoMDMVarSCEPRenewalID) || automdmVar == string(automdm.AutoMDMVarCertificateRenewalID) ||
				automdmVar == string(automdm.AutoMDMVarHostEndUserIDPFullname) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPExternalID) ||
				automdmVar == string(automdm.AutoMDMVarHostUUID):
				// No extra validation needed for these variables

			case isPKCS12CA:
				configured, err := isPKCS12CAConfigured(ctx, logger, groupedCAs, ds, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, pkcs12CAs,
					profUUID, target, pkcs12Var, automdmVar)
				if err != nil {
					return ctxerr.Wrapf(ctx, err, "checking %s configuration", pkcs12Var.caType.vendor)
				}
				if !configured {
					valid = false
					break initialAutoMDMVarLoop
				}

			case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)) || strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)):
				caName, found := strings.CutPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix))
				if !found {
					caName, _ = strings.CutPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix))
				}

				if customSCEPCAs == nil {
					customSCEPCAs = make(map[string]*automdm.CustomSCEPProxyCA)
					if groupedCAs != nil {
						for _, ca := range groupedCAs.CustomScepProxy {
							customSCEPCAs[ca.Name] = &ca
						}
					}
				}
				err := profiles.IsCustomSCEPConfigured(ctx, customSCEPCAs, caName, automdmVar, func(errMsg string) error {
					_, err := automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID, errMsg, ptr.Time(time.Now().UTC()))
					return err
				})
				if err != nil {
					valid = false
					break initialAutoMDMVarLoop
				}

			case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix)) || strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix)):
				if smallstepCAs == nil {
					smallstepCAs = make(map[string]*automdm.SmallstepSCEPProxyCA)
				}
				caName, found := strings.CutPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix))
				if !found {
					caName, _ = strings.CutPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix))
				}

				configured, err := isSmallstepSCEPConfigured(ctx, logger, groupedCAs, ds, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, smallstepCAs, profUUID, target, caName,
					automdmVar)
				if err != nil {
					return ctxerr.Wrap(ctx, err, "checking Smallstep SCEP configuration")
				}
				if !configured {
					valid = false
					break initialAutoMDMVarLoop
				}

			default:
				// Otherwise, error out since this variable is unknown
				detail := fmt.Sprintf("Unknown AutoMDM variable $AUTOMDM_VAR_%s found in profile. Please update or remove.",
					automdmVar)
				// A profile saved before the upgrade can still use a variable AutoMDM 5 removed.
				if err := automdm.FindRemovedAutoMDMVariable(automdmVar); err != nil {
					detail = err.Error()
				}
				_, err := automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID, detail, variablesUpdatedAt)
				if err != nil {
					return err
				}
				valid = false
			}
		}
		if !valid {
			// We marked the profile as failed, so we will not do any additional processing on it
			delete(targets, profUUID)
			continue
		}

		// Currently, all supported AutoMDM variables are unique per host, so we split the profile into multiple profiles.
		// We generate a new temporary profileUUID which is currently only used to install the profile.
		// The profileUUID in host_mdm_apple_profiles is still the original profileUUID.
		// We also generate a new commandUUID which is used to install the profile via nano_commands table.
		if addedTargets == nil {
			addedTargets = make(map[string]*automdm.CmdTarget, 1)
		}
		// We store the timestamp when the challenge was retrieved to know if it has expired.
		var managedCertificatePayloads []*automdm.MDMManagedCertificate
		// We need to update the profiles of each host with the new command UUID
		profilesToUpdate := make([]*automdm.MDMAppleBulkUpsertHostProfilePayload, 0, len(target.EnrollmentIDs))
		for _, enrollmentID := range target.EnrollmentIDs {
			tempProfUUID := uuid.NewString()
			// Use the same UUID for command UUID, which will be the primary key for nano_commands
			tempCmdUUID := tempProfUUID
			profile, ok := getHostProfileToInstallByEnrollmentID(hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, enrollmentID, profUUID)
			if !ok || profile == nil { // Should never happen
				continue
			}
			// Fetch the host UUID, which may not be the same as the Enrollment ID, from the profile
			hostUUID := profile.HostUUID

			// some variables need more information about the host; build a skeleton host and hydrate if we need more info
			hostLite := automdm.Host{UUID: hostUUID}
			onMismatchedHostCount := func(hostCount int) error {
				return ctxerr.Wrap(ctx, ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
					CommandUUID:        target.CmdUUID,
					HostUUID:           hostLite.UUID,
					Status:             &automdm.MDMDeliveryFailed,
					Detail:             fmt.Sprintf("Unexpected number of hosts (%d) for UUID %s.", hostCount, hostLite.UUID),
					OperationType:      automdm.MDMOperationTypeInstall,
					VariablesUpdatedAt: variablesUpdatedAt,
				}), "could not retrieve host by UUID for profile variable substitution")
			}

			profile.CommandUUID = tempCmdUUID
			profile.VariablesUpdatedAt = variablesUpdatedAt

			hostContents := contentsStr
			failed := false
			// Holds the records of the certificates issued for this host while its profile is built. They are saved only if the whole
			// profile is sent: after a later failure the host keeps its old certificates, and a new record would give a wrong expiry.
			// This only matters for a profile that gets more than one certificate, which is rare and not possible with some CA types (like SCEP).
			var pkcs12ManagedCertificates []*automdm.MDMManagedCertificate

		automdmVarLoop:
			for _, automdmVar := range automdmVars {
				pkcs12Var, isPKCS12CA := findPKCS12CAVariable(pkcs12CATypes, automdmVar)
				var err error
				switch {
				case automdmVar == string(automdm.AutoMDMVarNDESSCEPChallenge):
					if ndesConfig == nil {
						if groupedCAs == nil || groupedCAs.NDESSCEP == nil {
							logger.ErrorContext(ctx, "missing NDES CA configuration for profile with NDES variables", "host_uuid", hostUUID, "profile_uuid", profUUID)
							continue
						}
						ndesConfig = groupedCAs.NDESSCEP
					}
					logger.DebugContext(ctx, "fetching NDES challenge", "host_uuid", hostUUID, "profile_uuid", profUUID)
					// Insert the SCEP challenge into the profile contents
					challenge, err := scepConfig.GetNDESSCEPChallenge(ctx, *ndesConfig)
					if err != nil {
						if !scep.IsTerminalNDESChallengeError(err) {
							// Nothing was delivered, and the condition clears on its own, so leave the profile queued and let a later tick preprocess it.
							logger.WarnContext(ctx, "could not reach NDES for a SCEP challenge; leaving profile queued for a later tick",
								"host_uuid", hostUUID, "profile_uuid", profUUID, "err", err)
							failed = true
							break automdmVarLoop
						}
						detail := scep.NDESChallengeErrorToDetail(err)
						err := ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
							CommandUUID:        target.CmdUUID,
							HostUUID:           hostUUID,
							Status:             &automdm.MDMDeliveryFailed,
							Detail:             detail,
							OperationType:      automdm.MDMOperationTypeInstall,
							VariablesUpdatedAt: variablesUpdatedAt,
						})
						if err != nil {
							return ctxerr.Wrap(ctx, err, "updating host MDM Apple profile for NDES SCEP challenge")
						}
						failed = true
						break automdmVarLoop
					}
					payload := &automdm.MDMManagedCertificate{
						HostUUID:             hostUUID,
						ProfileUUID:          profUUID,
						ChallengeRetrievedAt: ptr.Time(time.Now()),
						Type:                 automdm.CAConfigNDES,
						CAName:               "NDES",
					}
					managedCertificatePayloads = append(managedCertificatePayloads, payload)

					hostContents = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarNDESSCEPChallengeRegexp, hostContents, challenge)

				case automdmVar == string(automdm.AutoMDMVarNDESSCEPProxyURL):
					// Insert the SCEP URL into the profile contents
					hostContents = profiles.ReplaceNDESSCEPProxyURLVariable(appConfig.MDMUrl(), hostUUID, profUUID, hostContents)

				case automdmVar == string(automdm.AutoMDMVarSCEPRenewalID), automdmVar == string(automdm.AutoMDMVarCertificateRenewalID):
					// Insert the renewal ID into the SCEP/ACME Payload CN or OU.
					// Both legacy SCEP_RENEWAL_ID and the preferred
					// CERTIFICATE_RENEWAL_ID substitute to the same value.
					automdmRenewalID := "automdm-" + profUUID
					hostContents = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarRenewalIDRegexp, hostContents, automdmRenewalID)

				case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)):
					replacedContents, replacedVariable, err := profiles.ReplaceCustomSCEPChallengeVariable(ctx, logger, automdmVar, customSCEPCAs, hostContents)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "replacing custom SCEP challenge variable")
					}
					if !replacedVariable {
						continue
					}
					hostContents = replacedContents

				case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)):
					replacedContents, managedCertificate, replacedVariable, err := profiles.ReplaceCustomSCEPProxyURLVariable(ctx, logger, ds, appConfig, automdmVar, customSCEPCAs, hostContents, hostUUID, profUUID)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "replacing custom SCEP proxy URL variable")
					}
					if !replacedVariable {
						continue
					}
					hostContents = replacedContents
					managedCertificatePayloads = append(managedCertificatePayloads, managedCertificate)

				case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix)):
					caName := strings.TrimPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix))
					ca, ok := smallstepCAs[caName]
					if !ok {
						logger.ErrorContext(ctx, "Smallstep SCEP CA not found. "+
							"This error should never happen since we validated/populated CAs earlier", "known_cas", profiles.KnownCANames(smallstepCAs))
						continue
					}
					logger.DebugContext(ctx, "fetching Smallstep SCEP challenge", "host_uuid", hostUUID, "profile_uuid", profUUID)
					challenge, err := scepConfig.GetSmallstepSCEPChallenge(ctx, *ca)
					if err != nil {
						detail := fmt.Sprintf("AutoMDM couldn't populate $AUTOMDM_VAR_%s. %s", automdm.AutoMDMVarSmallstepSCEPChallengePrefix, err.Error())
						err := ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
							CommandUUID:        target.CmdUUID,
							HostUUID:           hostUUID,
							Status:             &automdm.MDMDeliveryFailed,
							Detail:             detail,
							OperationType:      automdm.MDMOperationTypeInstall,
							VariablesUpdatedAt: variablesUpdatedAt,
						})
						if err != nil {
							return ctxerr.Wrap(ctx, err, "updating host MDM Apple profile for Smallstep SCEP challenge")
						}
						failed = true
						break automdmVarLoop
					}
					logger.InfoContext(ctx, "retrieved SCEP challenge from Smallstep", "host_uuid", hostUUID, "profile_uuid", profUUID)

					payload := &automdm.MDMManagedCertificate{
						HostUUID:             hostUUID,
						ProfileUUID:          profUUID,
						ChallengeRetrievedAt: ptr.Time(time.Now()),
						Type:                 automdm.CAConfigSmallstep,
						CAName:               caName,
					}
					managedCertificatePayloads = append(managedCertificatePayloads, payload)
					hostContents, err = profiles.ReplaceExactAutoMDMPrefixVariableInXML(string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix), ca.Name, hostContents, challenge)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "replacing Smallstep SCEP challenge variable")
					}

				case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix)):
					// Insert the SCEP URL into the profile contents
					caName := strings.TrimPrefix(automdmVar, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix))
					proxyURL := fmt.Sprintf("%s%s%s", appConfig.MDMUrl(), SCEPProxyPath,
						url.PathEscape(fmt.Sprintf("%s,%s,%s", hostUUID, profUUID, caName)))
					hostContents, err = profiles.ReplaceExactAutoMDMPrefixVariableInXML(string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix), caName, hostContents, proxyURL)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "replacing Smallstep SCEP URL variable")
					}

				case automdmVar == string(automdm.AutoMDMVarHostHardwareSerial):
					hostLite, ok, err = profiles.HydrateHost(ctx, ds, hostLite, onMismatchedHostCount)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "getting host hardware serial")
					}
					if !ok {
						failed = true
						break automdmVarLoop
					}
					hostContents = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostHardwareSerialRegexp, hostContents, hostLite.HardwareSerial)
				case automdmVar == string(automdm.AutoMDMVarHostPlatform):
					hostLite, ok, err = profiles.HydrateHost(ctx, ds, hostLite, onMismatchedHostCount)
					if err != nil {
						return ctxerr.Wrap(ctx, err, "getting host platform")
					}
					if !ok {
						failed = true
						break automdmVarLoop
					}
					platform := hostLite.Platform
					if platform == "darwin" {
						platform = "macos"
					}

					hostContents = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostPlatformRegexp, hostContents, platform)
				case automdmVar == string(automdm.AutoMDMVarHostEndUserIDPUsername) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart) ||
					automdmVar == string(automdm.AutoMDMVarHostEndUserIDPGroups) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPDepartment) ||
					automdmVar == string(automdm.AutoMDMVarHostEndUserIDPFullname) || automdmVar == string(automdm.AutoMDMVarHostEndUserIDPExternalID):
					replacedContents, replacedVariable, err := profiles.ReplaceHostEndUserIDPVariables(ctx, ds, automdmVar, hostContents, hostUUID, hostIDForUUIDCache, func(errMsg string) error {
						err := ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
							CommandUUID:        target.CmdUUID,
							HostUUID:           hostUUID,
							Status:             &automdm.MDMDeliveryFailed,
							Detail:             errMsg,
							OperationType:      automdm.MDMOperationTypeInstall,
							VariablesUpdatedAt: variablesUpdatedAt,
						})
						return err
					})
					if err != nil {
						return ctxerr.Wrap(ctx, err, "replacing host end user IDP variables")
					}
					if !replacedVariable {
						failed = true
						break automdmVarLoop
					}

					hostContents = replacedContents

				case isPKCS12CA && !pkcs12Var.isData:
					// We will replace the password when we populate the certificate data

				case automdmVar == string(automdm.AutoMDMVarHostUUID):
					hostContents = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostUUIDRegexp, hostContents, hostUUID)

				case isPKCS12CA:
					ca, ok := pkcs12CAs[pkcs12Var.cacheKey()]
					if !ok {
						logger.ErrorContext(ctx, "Custom "+pkcs12Var.caType.vendor+" CA not found. "+
							"This error should never happen since we validated/populated CAs earlier")
						continue
					}
					updatedContents, managed, ok, err := issuePKCS12ForHost(ctx, ds, logger, ca, pkcs12Var, target, hostLite, profUUID, hostContents,
						hostIDForUUIDCache, variablesUpdatedAt, onMismatchedHostCount)
					if err != nil {
						return err
					}
					if !ok {
						failed = true
						break automdmVarLoop
					}
					hostContents = updatedContents
					pkcs12ManagedCertificates = append(pkcs12ManagedCertificates, managed)

				default:
					// This was handled in the above switch statement, so we should never reach this case
				}
			}

			// Expand per-host custom host vitals ($AUTOMDM_HOST_VITAL_<id>). This is a
			// top-level prefix not handled by the AUTOMDM_VAR_ loop above. On a
			// missing/empty value for this host, mark the profile failed with a
			// detail rather than shipping a blank substitution.
			if !failed && hasHostVitals {
				hostForVitals, ok, err := profiles.HydrateHost(ctx, ds, hostLite, onMismatchedHostCount)
				if err != nil {
					return ctxerr.Wrap(ctx, err, "hydrating host for custom host vitals")
				}
				if !ok {
					// onMismatchedHostCount already marked the profile failed.
					failed = true
				} else {
					hostLite = hostForVitals
					expanded, err := ds.ExpandCustomHostVitals(ctx, hostLite.ID, hostContents)
					if err != nil {
						var missing *automdm.MissingCustomHostVitalValueError
						if !errors.As(err, &missing) {
							return ctxerr.Wrap(ctx, err, "expanding custom host vitals")
						}
						if updErr := ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
							CommandUUID:        target.CmdUUID,
							HostUUID:           hostUUID,
							Status:             &automdm.MDMDeliveryFailed,
							Detail:             missing.Error(),
							OperationType:      automdm.MDMOperationTypeInstall,
							VariablesUpdatedAt: variablesUpdatedAt,
						}); updErr != nil {
							return ctxerr.Wrap(ctx, updErr, "marking profile failed for missing custom host vital")
						}
						failed = true
					} else {
						hostContents = expanded
					}
				}
			}

			if !failed {
				managedCertificatePayloads = append(managedCertificatePayloads, pkcs12ManagedCertificates...)
				addedTargets[tempProfUUID] = &automdm.CmdTarget{
					CmdUUID:           tempCmdUUID,
					ProfileIdentifier: target.ProfileIdentifier,
					ProfileName:       target.ProfileName,
					EnrollmentIDs:     []string{enrollmentID},
				}
				profileContents[tempProfUUID] = mobileconfig.Mobileconfig(hostContents)
				profilesToUpdate = append(profilesToUpdate, profile)
			}
		}
		// Update profiles with the new command UUID
		if len(profilesToUpdate) > 0 {
			if err := ds.BulkUpsertMDMAppleHostProfiles(ctx, profilesToUpdate); err != nil {
				return ctxerr.Wrap(ctx, err, "updating host profiles")
			}
		}
		if len(managedCertificatePayloads) != 0 {
			// TODO: We could filter out failed profiles, but at the moment we don't, see Windows impl. for how it's done there.
			err := ds.BulkUpsertMDMManagedCertificates(ctx, managedCertificatePayloads)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "updating managed certificates")
			}
		}
		// Remove the parent target, since we will use host-specific targets
		delete(targets, profUUID)
	}
	if len(addedTargets) > 0 {
		// Add the new host-specific targets to the original targets map
		maps.Copy(targets, addedTargets)
	}
	return nil
}

// digiCertVarHint tells admins that a failing IdP variable lives in the DigiCert CA fields, not in the profile.
const digiCertVarHint = " The variable is used in the DigiCert certificate authority, not in the profile."

// ejbcaVarHint tells admins that a failing IdP variable lives in the EJBCA CA fields, not in the profile.
const ejbcaVarHint = " The variable is used in the EJBCA certificate authority, not in the profile."

// replaceAutoMDMVarInCAItem expands the AutoMDM variables of one field of a certificate authority for the host. varHint is added to the profile
// detail of a failure, to tell the admin the variable is in the certificate authority.
func replaceAutoMDMVarInCAItem(ctx context.Context, ds automdm.Datastore, target *automdm.CmdTarget, hostLite automdm.Host, caVarsCache map[string]string, item *string, hostIDForUUIDCache map[string]uint, variablesUpdatedAt *time.Time, onMismatchedHostCount func(int) error, varHint string) (bool, error) {
	caAutoMDMVars := variables.Find(*item)
	for _, caVar := range caAutoMDMVars {
		switch {
		case caVar == string(automdm.AutoMDMVarHostHardwareSerial):
			hardwareSerial, ok := caVarsCache[string(automdm.AutoMDMVarHostHardwareSerial)]
			if !ok {
				var err error
				hostLite, ok, err = profiles.HydrateHost(ctx, ds, hostLite, onMismatchedHostCount)
				if err != nil {
					return false, ctxerr.Wrap(ctx, err, "getting host hardware serial")
				}
				if !ok {
					return false, nil
				}
				hardwareSerial = hostLite.HardwareSerial
				caVarsCache[string(automdm.AutoMDMVarHostHardwareSerial)] = hostLite.HardwareSerial
			}
			*item = variables.Replace(*item, caVar, hardwareSerial)
		case caVar == string(automdm.AutoMDMVarHostPlatform):
			platform, ok := caVarsCache[string(automdm.AutoMDMVarHostPlatform)]
			if !ok {
				var err error
				hostLite, ok, err = profiles.HydrateHost(ctx, ds, hostLite, onMismatchedHostCount)
				if err != nil {
					return false, ctxerr.Wrap(ctx, err, "getting host hardware serial")
				}
				if !ok {
					return false, nil
				}
				platform = hostLite.Platform
				if platform == "darwin" {
					platform = "macos"
				}

				caVarsCache[string(automdm.AutoMDMVarHostPlatform)] = platform
			}
			*item = variables.Replace(*item, caVar, platform)
		case caVar == string(automdm.AutoMDMVarHostUUID):
			*item = variables.Replace(*item, caVar, hostLite.UUID)
		case slices.Contains(automdm.IDPAutoMDMVariables, automdm.AutoMDMVarName(caVar)):
			value, ok := caVarsCache[caVar]
			if !ok {
				var err error
				value, _, ok, err = profiles.ResolveHostEndUserIDPValue(ctx, ds, caVar, hostLite.UUID, hostIDForUUIDCache, func(errMsg string) error {
					return ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
						CommandUUID:        target.CmdUUID,
						HostUUID:           hostLite.UUID,
						Status:             &automdm.MDMDeliveryFailed,
						Detail:             strings.TrimSpace(errMsg) + varHint,
						OperationType:      automdm.MDMOperationTypeInstall,
						VariablesUpdatedAt: variablesUpdatedAt,
					})
				})
				if err != nil {
					return false, ctxerr.Wrap(ctx, err, "resolving host end user IDP variable")
				}
				if !ok {
					return false, nil
				}
				caVarsCache[caVar] = value
			}
			*item = variables.Replace(*item, caVar, value)
		default:
			// Validated when the CA was saved, so this is a CA saved before the upgrade that still uses a variable AutoMDM 5
			// removed. Fail rather than send the variable to the CA as literal text.
			detail := fmt.Sprintf("Unknown AutoMDM variable $AUTOMDM_VAR_%s. Please update or remove.", caVar)
			if err := automdm.FindRemovedAutoMDMVariable(caVar); err != nil {
				detail = err.Error()
			}
			err := ds.UpdateOrDeleteHostMDMAppleProfile(ctx, &automdm.HostMDMAppleProfile{
				CommandUUID:        target.CmdUUID,
				HostUUID:           hostLite.UUID,
				Status:             &automdm.MDMDeliveryFailed,
				Detail:             detail + varHint,
				OperationType:      automdm.MDMOperationTypeInstall,
				VariablesUpdatedAt: variablesUpdatedAt,
			})
			if err != nil {
				return false, ctxerr.Wrap(ctx, err, "updating host MDM Apple profile for unknown CA variable")
			}
			return false, nil
		}
	}
	return true, nil
}

func isNDESSCEPConfigured(ctx context.Context, logger *slog.Logger, groupedCAs *automdm.GroupedCertificateAuthorities, ds automdm.Datastore,
	hostProfilesToInstallMap map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload, userEnrollmentsToHostUUIDsMap map[string]string, profUUID string, target *automdm.CmdTarget,
) (bool, error) {
	if !license.IsPremium(ctx) {
		return automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID, "NDES SCEP Proxy requires an AutoMDM Premium license.", ptr.Time(time.Now().UTC()))
	}
	if groupedCAs == nil || groupedCAs.NDESSCEP == nil {
		return automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID,
			"NDES SCEP Proxy is not configured. Please configure in Settings > Integrations > Certificates.", ptr.Time(time.Now().UTC()))
	}
	return true, nil
}

func isSmallstepSCEPConfigured(ctx context.Context, logger *slog.Logger, groupedCAs *automdm.GroupedCertificateAuthorities, ds automdm.Datastore,
	hostProfilesToInstallMap map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload,
	userEnrollmentsToHostUUIDsMap map[string]string,
	existingSmallstepSCEPCAs map[string]*automdm.SmallstepSCEPProxyCA, profUUID string, target *automdm.CmdTarget, caName string, automdmVar string,
) (bool, error) {
	if !license.IsPremium(ctx) {
		return automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID, "Smallstep SCEP integration requires an AutoMDM Premium license.", ptr.Time(time.Now().UTC()))
	}
	if _, ok := existingSmallstepSCEPCAs[caName]; ok {
		return true, nil
	}
	configured := false
	var scepCA *automdm.SmallstepSCEPProxyCA
	if groupedCAs != nil && len(groupedCAs.Smallstep) > 0 {
		for _, ca := range groupedCAs.Smallstep {
			if ca.Name == caName {
				scepCA = &ca
				configured = true
				break
			}
		}
	}
	if !configured || scepCA == nil {
		return automdm.MarkProfilesFailed(ctx, ds, logger, target, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, profUUID,
			fmt.Sprintf("AutoMDM couldn't populate $%s because %s certificate authority doesn't exist.", automdmVar, caName), ptr.Time(time.Now().UTC()))
	}

	existingSmallstepSCEPCAs[caName] = scepCA
	return true, nil
}

func getHostProfileToInstallByEnrollmentID(hostProfilesToInstallMap map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload,
	userEnrollmentsToHostUUIDsMap map[string]string,
	enrollmentID,
	profUUID string,
) (*automdm.MDMAppleBulkUpsertHostProfilePayload, bool) {
	profile, ok := hostProfilesToInstallMap[automdm.HostProfileUUID{HostUUID: enrollmentID, ProfileUUID: profUUID}]
	if !ok {
		var hostUUID string
		// If sending to the user channel the enrollmentID will have to be mapped back to the host UUID.
		hostUUID, ok = userEnrollmentsToHostUUIDsMap[enrollmentID]
		if ok {
			profile, ok = hostProfilesToInstallMap[automdm.HostProfileUUID{HostUUID: hostUUID, ProfileUUID: profUUID}]
		}
	}
	return profile, ok
}
