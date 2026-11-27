package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/anilyagiz/automdm/v4/pkg/mdm/profilevalidation"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	microsoft_mdm "github.com/anilyagiz/automdm/v4/server/mdm/microsoft"
	"github.com/anilyagiz/automdm/v4/server/mdm/microsoft/syncml"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
	"github.com/anilyagiz/automdm/v4/server/variables"
)

func (svc *Service) NewMDMWindowsConfigProfile(ctx context.Context, teamID uint, profileName string, data []byte, labelsInclude []string, labelsMembershipMode automdm.MDMLabelsMode, labelsExcludeAny []string, description string, selfService, hidden bool) (*automdm.MDMWindowsConfigProfile, error) {
	if err := svc.authz.Authorize(ctx, &automdm.MDMConfigProfileAuthz{TeamID: &teamID}, automdm.ActionWrite); err != nil {
		return nil, ctxerr.Wrap(ctx, err)
	}

	if err := automdm.ValidateMDMProfileDescription(description); err != nil {
		return nil, ctxerr.Wrap(ctx, err)
	}
	if err := validateProfileDeployFlags(ctx, selfService, hidden, false, "Couldn't add. "); err != nil {
		return nil, ctxerr.Wrap(ctx, err)
	}
	validation, err := svc.lenientProfileValidationMode(ctx)
	if err != nil {
		return nil, err
	}
	cp, usesAutoMDMVars, teamName, err := svc.parseAndValidateWindowsConfigProfile(ctx, teamID, profileName, data, labelsInclude, labelsMembershipMode, labelsExcludeAny, "Couldn't add. ", validation)
	if err != nil {
		return nil, profileValidationUserError(err, false)
	}
	cp.Description = description
	cp.Hidden = hidden

	newCP, err := svc.ds.NewMDMWindowsConfigProfile(ctx, *cp, usesAutoMDMVars)
	if err != nil {
		if _, ok := errors.AsType[endpointer.ExistsErrorInterface](err); ok {
			err = automdm.NewInvalidArgumentError("profile", SameProfileNameUploadErrorMsg).
				WithStatus(http.StatusConflict)
		}
		return nil, ctxerr.Wrap(ctx, err)
	}

	var (
		actTeamID   *uint
		actTeamName *string
	)
	if teamID > 0 {
		actTeamID = &teamID
		actTeamName = &teamName
	}
	if err := svc.NewActivity(
		ctx, authz.UserFromContext(ctx), &automdm.ActivityTypeCreatedWindowsProfile{
			TeamID:      actTeamID,
			TeamName:    actTeamName,
			ProfileName: newCP.Name,
		},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "logging activity for create mdm windows config profile")
	}

	return newCP, nil
}

// parseAndValidateWindowsConfigProfile runs the validation shared by the
// create and update paths. It returns the constructed profile (with labels
// set), the AutoMDM variable names it uses, and the team's name (empty string
// for no team).
func (svc *Service) parseAndValidateWindowsConfigProfile(ctx context.Context, teamID uint, profileName string, data []byte, labelsInclude []string, labelsMembershipMode automdm.MDMLabelsMode, labelsExcludeAny []string, errPrefix string, validation *profilevalidation.Mode) (*automdm.MDMWindowsConfigProfile, []automdm.AutoMDMVarName, string, error) {
	// check that Windows MDM is enabled - the middleware of that endpoint checks
	// only that any MDM is enabled, maybe it's just macOS
	if err := svc.VerifyMDMWindowsConfigured(ctx); err != nil {
		err := automdm.NewInvalidArgumentError("profile", automdm.WindowsMDMNotConfiguredMessage).WithStatus(http.StatusBadRequest)
		return nil, nil, "", ctxerr.Wrap(ctx, err, "check windows MDM enabled")
	}

	lic, err := svc.License(ctx)
	if err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err, "checking license")
	}

	var teamName string
	if teamID > 0 {
		if lic == nil || !lic.IsPremium() {
			return nil, nil, "", ctxerr.Wrap(ctx, automdm.ErrMissingLicense)
		}
		tm, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, &teamID, nil)
		if err != nil {
			return nil, nil, "", ctxerr.Wrap(ctx, err)
		}
		teamName = tm.Name
	}

	if len(labelsInclude) > 0 || len(labelsExcludeAny) > 0 {
		if lic == nil || !lic.IsPremium() {
			return nil, nil, "", ctxerr.Wrap(ctx, automdm.NewLicenseErrorWithCause(automdm.ConfigProfileLabelScopingPremiumCauseMsg), "checking license for profile label scoping")
		}
	}

	cp := automdm.MDMWindowsConfigProfile{
		TeamID: &teamID,
		Name:   profileName,
		SyncML: data,
	}
	if err := cp.ValidateUserProvided(svc.config.MDM.IsCustomDiskEncryptionEnabled()); err != nil {
		msg := err.Error()
		if strings.Contains(msg, syncml.DiskEncryptionProfileRestrictionErrMsg) {
			return nil, nil, "", ctxerr.Wrap(ctx,
				&automdm.BadRequestError{Message: msg + " To control these settings use disk encryption endpoint."})
		}

		// this is not great, but since the validations are shared between the CLI
		// and the API, we must make some changes to error message here.
		if ix := strings.Index(msg, "To control these settings,"); ix >= 0 {
			msg = strings.TrimSpace(msg[:ix])
		}
		err := &automdm.BadRequestError{Message: errPrefix + msg}
		return nil, nil, "", ctxerr.Wrap(ctx, err, "validate profile")
	}
	// After ValidateUserProvided, which keeps its own 400s for reserved and
	// empty names; this adds the limits shared with the other profile types.
	if err := automdm.ValidateMDMProfileName(cp.Name); err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err)
	}

	if overlap := automdm.LabelOverlap(labelsInclude, labelsExcludeAny); overlap != "" {
		return nil, nil, "", ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("labels", fmt.Sprintf("label %q cannot appear in both include and exclude lists", overlap)))
	}
	includeLabels, excludeLabels, err := svc.validateProfileLabelSets(ctx, &teamID, labelsInclude, labelsExcludeAny)
	if err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err, "validating labels")
	}
	switch labelsMembershipMode {
	case automdm.LabelsIncludeAny:
		cp.LabelsIncludeAny = includeLabels
	default:
		// default include all
		cp.LabelsIncludeAll = includeLabels
	}
	cp.LabelsExcludeAny = excludeLabels

	if err := automdm.ValidateEmbeddedSecretsAndCustomHostVitals(ctx, svc.ds, []string{string(cp.SyncML)}); err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("profile", err.Error()))
	}

	groupedCAs, err := svc.ds.GetGroupedCertificateAuthorities(ctx, true)
	if err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err, "getting grouped certificate authorities")
	}

	// Validated expanded, like the batch path, so a secret's value is checked (a SCEP challenge's characters, a
	// schema's types); the expanded copy stays local and cp.SyncML is stored unexpanded.
	expanded := string(cp.SyncML)
	if len(automdm.ContainsPrefixVars(expanded, automdm.ServerSecretPrefix)) > 0 {
		expanded, err = svc.ds.ExpandEmbeddedSecrets(ctx, expanded)
		if err != nil {
			return nil, nil, "", ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("profile", err.Error()))
		}
	}

	foundVars, err := validateWindowsProfileAutoMDMVariables(expanded, lic, groupedCAs)
	if err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err)
	}

	// Collect AutoMDM variables used in the profile
	var usesAutoMDMVars []automdm.AutoMDMVarName
	for _, varName := range foundVars {
		usesAutoMDMVars = append(usesAutoMDMVars, automdm.AutoMDMVarName(varName))
	}

	if validation != nil {
		if err := svc.validateProfileSchema(ctx, profileSchemaWindowsProfile, []byte(expanded), validation, cp.Name, teamID); err != nil {
			return nil, nil, "", err
		}
	}

	if err := svc.handleWindowsProfileSoftwareUpdate(ctx, cp.SyncML, teamID); err != nil {
		return nil, nil, "", ctxerr.Wrap(ctx, err, "handling windows profile software update")
	}

	return &cp, usesAutoMDMVars, teamName, nil
}

// updateMDMWindowsConfigProfile implements the Windows branch of
// UpdateMDMConfigProfile. A Windows .xml carries no identifier, so the profile is
// keyed by UUID here. An empty profileName keeps the stored name; a nil
// description keeps the stored description. The rename is in place, unlike
// GitOps, which matches on name and so treats a rename as delete-then-insert.
func (svc *Service) updateMDMWindowsConfigProfile(ctx context.Context, profileUUID string, profileName string, profile []byte, labelsInclude []string, labelsMembershipMode automdm.MDMLabelsMode, labelsExcludeAny []string, description *string, selfService, hidden *bool) error {
	// first we perform a basic authz check
	if err := svc.authz.Authorize(ctx, &automdm.Team{}, automdm.ActionRead); err != nil {
		return ctxerr.Wrap(ctx, err)
	}

	if err := svc.VerifyMDMWindowsConfigured(ctx); err != nil {
		return err
	}

	existing, err := svc.ds.GetMDMWindowsConfigProfile(ctx, profileUUID)
	if err != nil {
		return ctxerr.Wrap(ctx, err)
	}

	teamID, teamName, err := svc.resolveProfileTeam(ctx, existing.TeamID)
	if err != nil {
		return err
	}

	// now we can do a specific authz check based on team id of profile before we update it
	if err := svc.authz.Authorize(ctx, &automdm.MDMConfigProfileAuthz{TeamID: existing.TeamID}, automdm.ActionWrite); err != nil {
		return ctxerr.Wrap(ctx, err)
	}

	// prevent editing profiles that are managed by AutoMDM
	automdmNames := mdm.AutoMDMReservedProfileNames()
	if _, ok := automdmNames[existing.Name]; ok {
		return &automdm.BadRequestError{
			Message:     "profiles managed by AutoMDM can't be edited using this endpoint.",
			InternalErr: fmt.Errorf("editing profile %s for team %s not allowed because it's managed by AutoMDM", existing.Name, teamName),
		}
	}

	name := existing.Name
	if profileName != "" {
		name = profileName
	}
	newDescription := existing.Description
	if description != nil {
		if err := automdm.ValidateMDMProfileDescription(*description); err != nil {
			return ctxerr.Wrap(ctx, err)
		}
		newDescription = *description
	}
	_, newHidden, err := resolveProfileDeployFlags(ctx, false, existing.Hidden, selfService, hidden, false, "Couldn't edit. ")
	if err != nil {
		return err
	}

	var cp *automdm.MDMWindowsConfigProfile
	var usesAutoMDMVars []automdm.AutoMDMVarName
	if len(profile) > 0 {
		var validation *profilevalidation.Mode
		if !bytes.Equal(profile, existing.SyncML) {
			if validation, err = svc.lenientProfileValidationMode(ctx); err != nil {
				return err
			}
		}
		cp, usesAutoMDMVars, _, err = svc.parseAndValidateWindowsConfigProfile(ctx, teamID, name, profile, labelsInclude, labelsMembershipMode, labelsExcludeAny, "Couldn't edit. ", validation)
		if err != nil {
			return profileValidationUserError(err, true)
		}
	} else {
		// no new content -- only labels are being changed.
		if err := svc.checkLabelsOnlyProfileUpdate(ctx, labelsInclude, labelsExcludeAny); err != nil {
			return err
		}
		includeLabels, excludeLabels, err := svc.validateProfileLabelSets(ctx, &teamID, labelsInclude, labelsExcludeAny)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "validating labels")
		}
		if name != existing.Name {
			if err := automdm.ValidateMDMProfileName(name); err != nil {
				return ctxerr.Wrap(ctx, err)
			}
		}
		cp = &automdm.MDMWindowsConfigProfile{
			Name:   name,
			TeamID: existing.TeamID,
		}
		switch labelsMembershipMode {
		case automdm.LabelsIncludeAll:
			cp.LabelsIncludeAll = includeLabels
		case automdm.LabelsIncludeAny:
			cp.LabelsIncludeAny = includeLabels
		}
		cp.LabelsExcludeAny = excludeLabels
	}
	cp.ProfileUUID = profileUUID
	cp.Description = newDescription
	cp.Hidden = newHidden

	if _, err := svc.ds.UpdateMDMWindowsConfigProfile(ctx, *cp, usesAutoMDMVars); err != nil {
		if _, ok := errors.AsType[endpointer.ExistsErrorInterface](err); ok {
			err = automdm.NewInvalidArgumentError("profile", SameProfileNameEditErrorMsg).WithStatus(http.StatusConflict)
		}
		return ctxerr.Wrap(ctx, err)
	}

	var (
		actTeamID   *uint
		actTeamName *string
	)
	if teamID > 0 {
		actTeamID = &teamID
		actTeamName = &teamName
	}
	if err := svc.NewActivity(
		ctx, authz.UserFromContext(ctx), &automdm.ActivityTypeEditedWindowsProfile{
			TeamID:      actTeamID,
			TeamName:    actTeamName,
			ProfileName: cp.Name,
		},
	); err != nil {
		return ctxerr.Wrap(ctx, err, "logging activity for edit mdm windows config profile")
	}

	return nil
}

// handleWindowsProfileSoftwareUpdate validates the preconditions for an OS-update
// (software update) profile: premium license and OS updates not already configured
// via settings. The "already exists" check and tracking-table insert happen
// atomically in ds.NewMDMWindowsConfigProfile.
func (svc *Service) handleWindowsProfileSoftwareUpdate(
	ctx context.Context,
	syncML []byte,
	teamID uint,
) error {
	if !automdm.ProfileTargetsReservedLocURI(syncML, syncml.AutoMDMOSUpdateTargetLocURI) {
		return nil
	}

	lic, _ := license.FromContext(ctx)
	if lic == nil || !lic.IsPremium() {
		return automdm.ErrMissingLicense
	}

	osUpdatesConfigured, err := isWindowsOSUpdatesConfigured(ctx, teamID, svc)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "checking if Windows OS updates are configured")
	}
	if osUpdatesConfigured {
		return &automdm.BadRequestError{
			Message: automdm.OSUpdatesAlreadyConfiguredErrorMessage,
		}
	}

	return nil
}

func isWindowsOSUpdatesConfigured(ctx context.Context, teamID uint, svc *Service) (bool, error) {
	var windowsOSUpdates automdm.WindowsUpdates
	if teamID > 0 {
		teamConfig, err := svc.ds.TeamMDMConfig(ctx, teamID)
		if err != nil {
			return false, ctxerr.Wrap(ctx, err, "getting team config")
		}
		windowsOSUpdates = teamConfig.WindowsUpdates
	} else {
		appConfig, err := svc.ds.AppConfig(ctx)
		if err != nil {
			return false, ctxerr.Wrap(ctx, err, "getting app config")
		}
		windowsOSUpdates = appConfig.MDM.WindowsUpdates
	}

	if windowsOSUpdates.Configured() {
		return true, nil
	}
	return false, nil
}

// automdmVarsSupportedInWindowsProfiles lists the AutoMDM variables that are
// supported in Windows configuration profiles.
// except prefix variables
var automdmVarsSupportedInWindowsProfiles = []automdm.AutoMDMVarName{
	automdm.AutoMDMVarHostUUID,
	automdm.AutoMDMVarHostHardwareSerial,
	automdm.AutoMDMVarSCEPWindowsCertificateID,
	automdm.AutoMDMVarSCEPRenewalID,
	automdm.AutoMDMVarCertificateRenewalID,
	automdm.AutoMDMVarHostEndUserIDPUsername,
	automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart,
	automdm.AutoMDMVarHostEndUserIDPFullname,
	automdm.AutoMDMVarHostEndUserIDPDepartment,
	automdm.AutoMDMVarHostEndUserIDPGroups,
	automdm.AutoMDMVarHostEndUserIDPExternalID,
	automdm.AutoMDMVarHostPlatform,
	automdm.AutoMDMVarNDESSCEPChallenge,
	automdm.AutoMDMVarNDESSCEPProxyURL,
	automdm.AutoMDMVarConditionalAccessSCEPURL,
	automdm.AutoMDMVarConditionalAccessSCEPChallenge,
}

var (
	subjectNameMissingRenewalIDMsg = "SubjectName item must contain the " + automdm.AutoMDMVarCertificateRenewalID.WithPrefix() +
		" variable in the OU field. In some use cases, it can go in the CN field instead."
	subjectNameRenewalIDInOneOfManyCNsMsg = "SubjectName item can only have one CN field when the CN contains the " +
		automdm.AutoMDMVarCertificateRenewalID.WithPrefix() + " variable."
	subjectNameEntityQuotedVariableMsg = `SubjectName values with AutoMDM variables can't use escaped quotes (&quot;). ` +
		`Remove them, or use a literal " instead.`
)

// validateSubjectNameRenewalID checks that a SubjectName <Data> has the renewal-ID variable in an OU or CN attribute, the
// two fields host certificate ingestion searches for the marker. OU is the norm, but some CAs (Okta) drop the OU from
// issued certificates. The legacy SCEP_RENEWAL_ID name is accepted for back-compat. The error carries the message for
// the admin.
func validateSubjectNameRenewalID(rawData string) error {
	if microsoft_mdm.HasEntityQuotedSubstitution(rawData) {
		return errors.New(subjectNameEntityQuotedVariableMsg)
	}
	var data strings.Builder
	// Only character data is read, from a profile the validators above already parsed; encoding/xml resolves no external entities.
	dec := xml.NewDecoder(strings.NewReader(rawData)) //nolint:gosec // G709: see above
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New(subjectNameMissingRenewalIDMsg)
		}
		if text, ok := token.(xml.CharData); ok {
			data.Write(text)
		}
	}
	var commonNames int
	var inOU, inCN bool
	for _, attribute := range microsoft_mdm.SubjectNameAttributes(data.String()) {
		key, value, ok := strings.Cut(attribute, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "OU":
			inOU = inOU || automdm.AutoMDMVarRenewalIDRegexp.MatchString(value)
		case "CN":
			commonNames++
			inCN = inCN || automdm.AutoMDMVarRenewalIDRegexp.MatchString(value)
		}
	}
	switch {
	case inOU:
		return nil
	case !inCN:
		return errors.New(subjectNameMissingRenewalIDMsg)
	case commonNames > 1:
		// Ingestion keeps only one CN per certificate, so the one with the marker might not be it.
		return errors.New(subjectNameRenewalIDInOneOfManyCNsMsg)
	}
	return nil
}

func validateWindowsProfileAutoMDMVariables(contents string, lic *automdm.LicenseInfo, groupedCAs *automdm.GroupedCertificateAuthorities) ([]string, error) {
	if err := automdm.ValidateNoHostSecretVariables(contents); err != nil {
		return nil, err
	}
	foundVars := variables.Find(contents)
	if len(foundVars) == 0 {
		return nil, nil
	}

	// Check for premium license if the profile contains AutoMDM variables
	if lic == nil || !lic.IsPremium() {
		return nil, automdm.ErrMissingLicense
	}

	// Check if all found variables are supported
	for _, varName := range foundVars {
		if !slices.Contains(automdmVarsSupportedInWindowsProfiles, automdm.AutoMDMVarName(varName)) &&
			!strings.HasPrefix(varName, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)) &&
			!strings.HasPrefix(varName, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)) {
			return nil, automdm.NewInvalidArgumentError("profile", fmt.Sprintf("AutoMDM variable $AUTOMDM_VAR_%s is not supported in Windows profiles.", varName))
		}
	}

	err := validateProfileCertificateAuthorityVariables(contents, lic, groupedCAs, caVarValidators{
		customSCEP:        additionalCustomSCEPValidationForWindowsProfiles,
		ndes:              additionalNDESValidationForWindowsProfiles,
		conditionalAccess: additionalConditionalAccessValidationForWindowsProfiles,
	})
	if err != nil {
		return nil, err
	}

	// Do additional validation that both custom SCEP URL and challenge vars are provided and not using different CA names etc.

	return foundVars, nil
}

// collectAllSyncMLItems returns all CmdItems from a SyncMLCmd, including items from nested
// commands within Atomic blocks (ReplaceCommands, AddCommands, ExecCommands).
func collectAllSyncMLItems(cmd *automdm.SyncMLCmd) []automdm.CmdItem {
	items := cmd.Items
	for _, nested := range cmd.ReplaceCommands {
		items = append(items, nested.Items...)
	}
	for _, nested := range cmd.AddCommands {
		items = append(items, nested.Items...)
	}
	for _, nested := range cmd.ExecCommands {
		items = append(items, nested.Items...)
	}
	return items
}

// containsAutoMDMVar checks if s contains the given AutoMDM variable in either $AUTOMDM_VAR_ or ${AUTOMDM_VAR_} form.
func containsAutoMDMVar(s string, v automdm.AutoMDMVarName) bool {
	return strings.Contains(s, v.WithPrefix()) || strings.Contains(s, v.WithBraces())
}

// isAutoMDMVar checks if s is exactly the given AutoMDM variable in either $AUTOMDM_VAR_ or ${AUTOMDM_VAR_} form.
func isAutoMDMVar(s string, v automdm.AutoMDMVarName) bool {
	return s == v.WithPrefix() || s == v.WithBraces()
}

func additionalNDESValidationForWindowsProfiles(contents string, ndesVars *NDESVarsFound) error {
	if ndesVars == nil {
		return nil
	}
	return validateWindowsSCEPURLAndChallengePlacement(contents, "NDES", automdm.AutoMDMVarNDESSCEPProxyURL, automdm.AutoMDMVarNDESSCEPChallenge)
}

// conditionalAccessChallengeInvalidMsg is the upload error when the challenge of a conditional access SCEP profile is
// missing, empty, or a literal with characters Windows can't encode as a PrintableString.
const conditionalAccessChallengeInvalidMsg = `The SCEP certificate's "Challenge" field must contain $AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE, or a global enroll secret made of letters, numbers, spaces, and ' ( ) + , - . / : = ?`

// additionalConditionalAccessValidationForWindowsProfiles checks that the conditional access SCEP URL and challenge
// variables are in, and only in, the SCEP node's ServerURL and Challenge, and that the SubjectName carries the renewal
// ID. A literal challenge, which profiles written before the challenge variable carry (secret variables are expanded
// before validation), must be one Windows can send. The variable scan that gets us here reads raw XML, comments
// included, so the ServerURL item must be seen in the parsed items too.
func additionalConditionalAccessValidationForWindowsProfiles(contents string, conditionalAccessVars *ConditionalAccessVarsFound) error {
	if conditionalAccessVars == nil {
		return nil
	}
	urlVar := automdm.AutoMDMVarConditionalAccessSCEPURL
	challengeVar := automdm.AutoMDMVarConditionalAccessSCEPChallenge

	var cmdMsg *automdm.SyncMLCmd
	// An admin's upload, already parsed as SyncML by the profile validators; encoding/xml resolves no external entities.
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimSpace([]byte(contents)))) //nolint:gosec // G709: see above
	urlMissingErr := &automdm.BadRequestError{
		Message: fmt.Sprintf("Variable %q must be in the SCEP certificate's \"ServerURL\" field.", urlVar.WithPrefix()),
	}
	challengeFound, challengeIsVar, serverURLFound := false, false, false
	for {
		if err := dec.Decode(&cmdMsg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("The payload isn't valid XML: %w", err)
		}
		if cmdMsg == nil {
			break
		}

		for _, cmd := range collectAllSyncMLItems(cmdMsg) {
			if cmd.Target == nil {
				continue
			}
			target := strings.TrimSpace(*cmd.Target)
			dataContent := ""
			if cmd.Data != nil {
				dataContent = cmd.Data.Content
			}

			if containsAutoMDMVar(target, urlVar) || containsAutoMDMVar(target, challengeVar) {
				return &automdm.BadRequestError{Message: "Conditional access AutoMDM variables must not appear in LocURI target paths."}
			}
			isServerURL := strings.HasSuffix(target, "/Install/ServerURL")
			isChallenge := strings.HasSuffix(target, "/Install/Challenge")
			if !isServerURL && containsAutoMDMVar(dataContent, urlVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf("Variable %q must only be in the SCEP certificate's \"ServerURL\" field.", urlVar.WithPrefix()),
				}
			}
			if !isChallenge && containsAutoMDMVar(dataContent, challengeVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf("Variable %q must only be in the SCEP certificate's \"Challenge\" field.", challengeVar.WithPrefix()),
				}
			}
			if isServerURL {
				if !isAutoMDMVar(dataContent, urlVar) {
					return urlMissingErr
				}
				serverURLFound = true
			}
			if isChallenge {
				challengeFound = true
				challengeIsVar = isAutoMDMVar(dataContent, challengeVar)
				validLiteral := dataContent != "" && microsoft_mdm.IsValidWindowsSCEPChallenge(dataContent)
				if !challengeIsVar && !validLiteral {
					return &automdm.BadRequestError{Message: conditionalAccessChallengeInvalidMsg}
				}
			}
			if strings.HasSuffix(target, automdm.WindowsSCEPSubjectNameSuffix) {
				if err := validateSubjectNameRenewalID(dataContent); err != nil {
					return &automdm.BadRequestError{Message: err.Error()}
				}
			}
		}
	}
	if !serverURLFound {
		return urlMissingErr
	}
	if !challengeFound {
		return &automdm.BadRequestError{Message: conditionalAccessChallengeInvalidMsg}
	}
	// The variable appears at most once, so requiring it as the challenge also keeps it out of the parts of the profile
	// the item checks don't read, such as Meta.
	if conditionalAccessVars.ChallengeFound() && !challengeIsVar {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("Variable %q must be in the SCEP certificate's \"Challenge\" field.", challengeVar.WithPrefix()),
		}
	}
	return nil
}

// validateWindowsSCEPURLAndChallengePlacement checks that a certificate authority's SCEP URL and challenge variables
// are each in, and only in, their own SCEP node, and that the SubjectName carries the renewal ID in its OU or CN.
func validateWindowsSCEPURLAndChallengePlacement(contents, groupName string, urlVar, challengeVar automdm.AutoMDMVarName) error {
	var cmdMsg *automdm.SyncMLCmd
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimSpace([]byte(contents))))
	for {
		if err := dec.Decode(&cmdMsg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("The payload isn't valid XML: %w", err)
		}
		if cmdMsg == nil {
			break
		}

		for _, cmd := range collectAllSyncMLItems(cmdMsg) {
			if cmd.Target == nil {
				continue
			}

			target := strings.TrimSpace(*cmd.Target)

			dataContent := ""
			if cmd.Data != nil {
				dataContent = cmd.Data.Content
			}

			isChallenge := strings.HasSuffix(target, "/Install/Challenge")
			isServerURL := strings.HasSuffix(target, "/Install/ServerURL")
			isSubjectName := strings.HasSuffix(target, automdm.WindowsSCEPSubjectNameSuffix)

			// Verify that each variable appears ONLY in its expected field.
			// This prevents the challenge or URL from being placed in an unexpected field
			// where it could be exfiltrated, since variable replacement is a global string substitution.
			if !isChallenge && containsAutoMDMVar(dataContent, challengeVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf(
						"Variable %q must only be in the SCEP certificate's \"Challenge\" field.", challengeVar.WithPrefix(),
					),
				}
			}
			if !isServerURL && containsAutoMDMVar(dataContent, urlVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf(
						"Variable %q must only be in the SCEP certificate's \"ServerURL\" field.", urlVar.WithPrefix(),
					),
				}
			}

			// Variables must not appear in LocURI target paths.
			if containsAutoMDMVar(target, challengeVar) || containsAutoMDMVar(target, urlVar) {
				return &automdm.BadRequestError{
					Message: groupName + " AutoMDM variables must not appear in LocURI target paths.",
				}
			}

			// Verify the expected fields contain the correct variables.
			if isChallenge && !isAutoMDMVar(dataContent, challengeVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf(
						"Variable %q must be in the SCEP certificate's \"Challenge\" field.", challengeVar.WithPrefix(),
					),
				}
			}
			if isServerURL && !isAutoMDMVar(dataContent, urlVar) {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf(
						"Variable %q must be in the SCEP certificate's \"ServerURL\" field.", urlVar.WithPrefix(),
					),
				}
			}
			if isSubjectName {
				if err := validateSubjectNameRenewalID(dataContent); err != nil {
					return &automdm.BadRequestError{Message: err.Error()}
				}
			}
		}
	}

	return nil
}

func additionalCustomSCEPValidationForWindowsProfiles(contents string, customSCEPVars *CustomSCEPVarsFound) error {
	if customSCEPVars == nil {
		return nil
	}

	var cmdMsg *automdm.SyncMLCmd
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimSpace([]byte(contents))))
	for {
		if err := dec.Decode(&cmdMsg); err != nil { // EOF is fine in this case
			if errors.Is(err, io.EOF) {
				break
			}
			return &automdm.BadRequestError{Message: fmt.Sprintf("The payload isn't valid XML: %s", err)}
		}
		if cmdMsg == nil {
			break
		}

		for _, cmd := range collectAllSyncMLItems(cmdMsg) {
			if cmd.Target == nil {
				continue
			}

			target := strings.TrimSpace(*cmd.Target)

			if strings.HasSuffix(target, automdm.WindowsSCEPSubjectNameSuffix) {
				// SubjectName item found, check that it contains the expected renewal ID variable
				if cmd.Data == nil {
					return &automdm.BadRequestError{Message: "SubjectName item is missing data"}
				}

				if err := validateSubjectNameRenewalID(cmd.Data.Content); err != nil {
					return &automdm.BadRequestError{Message: err.Error()}
				}
			}
		}
	}

	return nil
}
