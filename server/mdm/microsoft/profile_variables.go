package microsoft_mdm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/mdm/profiles"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/variables"
)

// PreprocessWindowsProfileContentsForDeployment processes Windows configuration profiles to replace AutoMDM variables
// with their actual values for each host during profile deployment.
func PreprocessWindowsProfileContentsForDeployment(deps ProfilePreprocessDependencies, params ProfilePreprocessParams, profileContents string) (string, error) {
	return preprocessWindowsProfileContents(deps, params, profileContents)
}

// windowsSCEPChallengeRegexp matches challenges made up entirely of characters valid in an ASN.1 PrintableString: letters,
// digits, space, and ' ( ) + , - . / : = ?. Windows encodes the SCEP challenge password as a PrintableString, so a challenge with
// any other character (most commonly "_") makes enrollment fail on-device with "The string contains a non-printable character."
// The space is allowed anywhere, including leading and trailing, and was verified to enroll fine on Windows 11.
var windowsSCEPChallengeRegexp = regexp.MustCompile(`^[A-Za-z0-9 '()+,./:=?-]*$`)

// IsValidWindowsSCEPChallenge reports whether Windows can send challenge as a SCEP challenge password.
func IsValidWindowsSCEPChallenge(challenge string) bool {
	return windowsSCEPChallengeRegexp.MatchString(challenge)
}

// scepChallengeInvalidCharsDetail is the host profile failure detail shown on the Host details page when a custom SCEP proxy
// challenge contains characters Windows can't encode as a PrintableString.
const scepChallengeInvalidCharsDetail = `Couldn't install certificate. The "%s" certificate authority challenge includes characters Windows doesn't support. Allowed: letters, numbers, spaces, and ' ( ) + , - . / : = ?`

// MicrosoftProfileProcessingError is used to indicate errors during Microsoft profile processing, such as variable replacement failures.
// It should not break the entire deployment flow, but rather be handled gracefully at the profile level, setting it to failed and detail = Error()
type MicrosoftProfileProcessingError struct {
	message string
}

func (e *MicrosoftProfileProcessingError) Error() string {
	return e.message
}

// MicrosoftProfileTransientError indicates that preprocessing (substituting this host's AutoMDM variables into the
// profile) could not complete for a reason that clears on its own.
type MicrosoftProfileTransientError struct {
	message string
}

func (e *MicrosoftProfileTransientError) Error() string {
	return e.message
}

type ProfilePreprocessDependencies struct {
	Context                    context.Context
	Logger                     *slog.Logger
	DataStore                  automdm.Datastore
	HostIDForUUIDCache         map[string]uint
	AppConfig                  *automdm.AppConfig
	CustomSCEPCAs              map[string]*automdm.CustomSCEPProxyCA
	ManagedCertificatePayloads *[]*automdm.MDMManagedCertificate
	NDESConfig                 *automdm.NDESSCEPProxyCA
	GetNDESSCEPChallenge       func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error)
	NDESChallengeErrorToDetail func(err error) string
	// NDESChallengeErrorIsTerminal reports whether a challenge-fetch failure needs someone to act before AutoMDM could
	// succeed. Required whenever the profile can carry an NDES challenge. Injected rather than called directly to keep
	// the ee SCEP package out of this one's imports.
	NDESChallengeErrorIsTerminal func(err error) bool
}

type ProfilePreprocessParams struct {
	HostUUID    string
	ProfileUUID string
}

// preprocessWindowsProfileContents processes Windows configuration profiles to replace AutoMDM variables
// with their actual values for each host. This function is used both during profile deployment
// and during profile verification to ensure consistency.
//
// The function handles XML escaping to prevent injection attacks.
//
// Currently supported variables:
//   - $AUTOMDM_VAR_HOST_UUID or ${AUTOMDM_VAR_HOST_UUID}: Replaced with the host's UUID
//   - $AUTOMDM_VAR_SCEP_WINDOWS_CERTIFICATE_ID or ${AUTOMDM_VAR_SCEP_WINDOWS_CERTIFICATE_ID}: Replaced with the profile UUID for SCEP certificate
//   - $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME> or ${AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>}: Replaced with the challenge for the specified custom SCEP CA
//   - $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME> or ${AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>}: Replaced with the proxy URL for the specified custom SCEP CA
//   - $AUTOMDM_VAR_NDES_SCEP_CHALLENGE or ${AUTOMDM_VAR_NDES_SCEP_CHALLENGE}: Replaced with the one-time NDES challenge password
//   - $AUTOMDM_VAR_NDES_SCEP_PROXY_URL or ${AUTOMDM_VAR_NDES_SCEP_PROXY_URL}: Replaced with the AutoMDM SCEP proxy URL for NDES
//   - $AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL or ${AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL}: Replaced with AutoMDM's conditional access SCEP URL
//   - $AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE or ${AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE}: Replaced with a host secret placeholder, which becomes a one-time challenge for the host when the command is sent
//
// Why we don't use Go templates here:
//  1. Error handling: Go templates don't provide fine-grained error handling for individual variable
//     replacements. We need to handle failures per-host and per-variable gracefully.
//  2. Variable dependencies: Some variables may be related or have dependencies on each other. With
//     manual processing, we can control the order of variable replacement precisely.
//  3. Performance: Templates must be compiled every time they're used, adding overhead when processing
//     thousands of host profiles. Direct string replacement is more efficient for our use case.
//  4. XML escaping: We need XML-specific escaping for values, which is simpler to control with direct
//     string replacement rather than template functions.
//
// If you need another dependency that should be reused across profiles, add it to a ProfilePreprocessDependencies
// implementation and to the interface if it's required for both verification and deployment. For new dependencies that
// vary profile-to-profile, add them to ProfilePreprocessParams.
func preprocessWindowsProfileContents(deps ProfilePreprocessDependencies, params ProfilePreprocessParams, profileContents string) (string, error) {
	// Check if AutoMDM variables or custom host vitals are present.
	automdmVars := variables.Find(profileContents)
	hasHostVitals := len(automdm.FindCustomHostVitalIDs(profileContents)) > 0
	if len(automdmVars) == 0 && !hasHostVitals {
		// No variables to replace, return original content
		return profileContents, nil
	}

	// Quote the SCEP SubjectName values about to be substituted, while the attribute boundaries are
	// still the admin's own. The pass after substitution doubles any quote that arrived inside them.
	result := transformSCEPSubjectNameData(profileContents, quoteAttributeValue)
	for _, automdmVar := range automdmVars {
		switch {
		case automdmVar == string(automdm.AutoMDMVarHostUUID):
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostUUIDRegexp, result, params.HostUUID)
		case automdmVar == string(automdm.AutoMDMVarHostPlatform):
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostPlatformRegexp, result, "windows")
		case automdmVar == string(automdm.AutoMDMVarHostHardwareSerial):
			hostLite, _, err := profiles.HydrateHost(deps.Context, deps.DataStore, automdm.Host{UUID: params.HostUUID}, func(hostCount int) error {
				return &MicrosoftProfileProcessingError{message: fmt.Sprintf("Found %d hosts with UUID %s. Profile variable substitution for %s requires exactly one host", hostCount, params.HostUUID, automdm.AutoMDMVarHostHardwareSerial.WithPrefix())}
			})
			if err != nil {
				return profileContents, err
			}
			if hostLite.HardwareSerial == "" {
				return profileContents, &MicrosoftProfileProcessingError{message: fmt.Sprintf("There is no serial number for this host. AutoMDM couldn't populate %s.", automdm.AutoMDMVarHostHardwareSerial.WithPrefix())}
			}

			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarHostHardwareSerialRegexp, result, hostLite.HardwareSerial)
		case slices.Contains(automdm.IDPAutoMDMVariables, automdm.AutoMDMVarName(automdmVar)):
			replacedContents, replacedVariable, err := profiles.ReplaceHostEndUserIDPVariables(deps.Context, deps.DataStore, automdmVar, result, params.HostUUID, deps.HostIDForUUIDCache, func(errMsg string) error {
				return &MicrosoftProfileProcessingError{message: errMsg}
			})
			if err != nil {
				return profileContents, err
			}
			if !replacedVariable {
				return profileContents, ctxerr.Wrap(deps.Context, err, "host end user IDP variable replacement failed for variable")
			}
			result = replacedContents
		}

		switch {
		case automdmVar == string(automdm.AutoMDMVarSCEPWindowsCertificateID):
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarSCEPWindowsCertificateIDRegexp, result, params.ProfileUUID)
		case automdmVar == string(automdm.AutoMDMVarSCEPRenewalID), automdmVar == string(automdm.AutoMDMVarCertificateRenewalID):
			// Both legacy SCEP_RENEWAL_ID and the preferred CERTIFICATE_RENEWAL_ID
			// substitute to the same value.
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarRenewalIDRegexp, result, "automdm-"+params.ProfileUUID)
		case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)):
			caName := strings.TrimPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPChallengePrefix))
			err := profiles.IsCustomSCEPConfigured(deps.Context, deps.CustomSCEPCAs, caName, automdmVar, func(errMsg string) error {
				return &MicrosoftProfileProcessingError{message: errMsg}
			})
			if err != nil {
				return profileContents, err
			}
			if ca := deps.CustomSCEPCAs[caName]; ca != nil && !windowsSCEPChallengeRegexp.MatchString(ca.Challenge) {
				return profileContents, &MicrosoftProfileProcessingError{
					message: fmt.Sprintf(scepChallengeInvalidCharsDetail, caName),
				}
			}
			replacedContents, replacedVariable, err := profiles.ReplaceCustomSCEPChallengeVariable(deps.Context, deps.Logger, automdmVar, deps.CustomSCEPCAs, result)
			if err != nil {
				return profileContents, ctxerr.Wrap(deps.Context, err, "replacing custom SCEP challenge variable")
			}
			if !replacedVariable {
				return profileContents, &MicrosoftProfileProcessingError{message: fmt.Sprintf("Custom SCEP challenge variable replacement failed for variable %s", automdmVar)}
			}
			result = replacedContents
		case strings.HasPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)):
			caName := strings.TrimPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix))
			err := profiles.IsCustomSCEPConfigured(deps.Context, deps.CustomSCEPCAs, caName, automdmVar, func(errMsg string) error {
				return &MicrosoftProfileProcessingError{message: errMsg}
			})
			if err != nil {
				return profileContents, err
			}
			replacedContents, managedCertificate, replacedVariable, err := profiles.ReplaceCustomSCEPProxyURLVariable(deps.Context, deps.Logger, deps.DataStore, deps.AppConfig, automdmVar, deps.CustomSCEPCAs, result, params.HostUUID, params.ProfileUUID)
			if err != nil {
				return profileContents, ctxerr.Wrap(deps.Context, err, "replacing custom SCEP challenge variable")
			}
			if !replacedVariable {
				return profileContents, &MicrosoftProfileProcessingError{message: fmt.Sprintf("Custom SCEP challenge variable replacement failed for variable %s", automdmVar)}
			}
			result = replacedContents

			*deps.ManagedCertificatePayloads = append(*deps.ManagedCertificatePayloads, managedCertificate)

		case automdmVar == string(automdm.AutoMDMVarNDESSCEPChallenge):
			if deps.NDESConfig == nil {
				return profileContents, &MicrosoftProfileProcessingError{message: automdm.NDESNotConfiguredMsg}
			}
			deps.Logger.DebugContext(deps.Context, "fetching NDES challenge", "host_uuid", params.HostUUID, "profile_uuid", params.ProfileUUID)
			challenge, err := deps.GetNDESSCEPChallenge(deps.Context, *deps.NDESConfig)
			if err != nil {
				if !deps.NDESChallengeErrorIsTerminal(err) {
					return profileContents, &MicrosoftProfileTransientError{message: deps.NDESChallengeErrorToDetail(err)}
				}
				return profileContents, &MicrosoftProfileProcessingError{message: deps.NDESChallengeErrorToDetail(err)}
			}
			payload := &automdm.MDMManagedCertificate{
				HostUUID:             params.HostUUID,
				ProfileUUID:          params.ProfileUUID,
				ChallengeRetrievedAt: ptr.Time(time.Now()),
				Type:                 automdm.CAConfigNDES,
				CAName:               "NDES",
			}
			*deps.ManagedCertificatePayloads = append(*deps.ManagedCertificatePayloads, payload)
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarNDESSCEPChallengeRegexp, result, challenge)

		case automdmVar == string(automdm.AutoMDMVarNDESSCEPProxyURL):
			result = profiles.ReplaceNDESSCEPProxyURLVariable(deps.AppConfig.MDMUrl(), params.HostUUID, params.ProfileUUID, result)

		case automdmVar == string(automdm.AutoMDMVarConditionalAccessSCEPURL):
			scepURL, err := url.JoinPath(deps.AppConfig.ServerSettings.ServerURL, automdm.ConditionalAccessSCEPPath)
			if err != nil {
				return profileContents, ctxerr.Wrap(deps.Context, err, "building conditional access SCEP URL")
			}
			// The URL variable is what marks the certificate as AutoMDM-tracked, whichever challenge the profile carries.
			*deps.ManagedCertificatePayloads = append(*deps.ManagedCertificatePayloads, &automdm.MDMManagedCertificate{
				HostUUID:             params.HostUUID,
				ProfileUUID:          params.ProfileUUID,
				ChallengeRetrievedAt: new(time.Now()),
				Type:                 automdm.CAConfigConditionalAccess,
				CAName:               automdm.ConditionalAccessManagedCertificateCAName,
			})
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarConditionalAccessSCEPURLRegexp, result, scepURL)

		case automdmVar == string(automdm.AutoMDMVarConditionalAccessSCEPChallenge):
			// The challenge is created when the command is sent, so the stored command never holds one and a host that
			// picks it up late still gets a valid one.
			result = profiles.ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarConditionalAccessSCEPChallengeRegexp, result,
				automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge))
		}
	}

	// Expand per-host custom host vitals. On a missing/empty value the datastore
	// returns a MissingCustomHostVitalValueError, which we surface as a
	// MicrosoftProfileProcessingError so the caller marks the profile failed with
	// this detail rather than shipping a blank substitution.
	if hasHostVitals {
		hostLite, _, err := profiles.HydrateHost(deps.Context, deps.DataStore, automdm.Host{UUID: params.HostUUID}, func(hostCount int) error {
			return &MicrosoftProfileProcessingError{message: fmt.Sprintf("Found %d hosts with UUID %s. Custom host vital substitution requires exactly one host.", hostCount, params.HostUUID)}
		})
		if err != nil {
			return profileContents, err
		}
		expanded, err := deps.DataStore.ExpandCustomHostVitals(deps.Context, hostLite.ID, result)
		if err != nil {
			if missing, ok := errors.AsType[*automdm.MissingCustomHostVitalValueError](err); ok {
				return profileContents, &MicrosoftProfileProcessingError{message: missing.Error()}
			}
			return profileContents, err
		}
		result = expanded
	}

	// Last, so every value that landed inside the quotes added above has been substituted by now.
	result = transformSCEPSubjectNameData(result, doubleAttributeQuotes)

	return result, nil
}
