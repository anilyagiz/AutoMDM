package profiles

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/ptr"
)

/*
This file contains functions to replace profile variables in MDM profiles, that are supported
on multiple platforms, so it can be shared.

AutoMDM variables supported across systems:
- $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>
- $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>

Once more is needed it should be placed here, and the main replacement logic can be taken from the apple_mdm.go
under server/service folder. Inside the `preprocessProfileContents` under the `automdmVarLoop` loop.
*/

// KnownCANames returns the configured CA names for logging. Names extracted
// from profile contents must not be logged: after variable substitution the
// contents can embed secrets (e.g. certificate passwords), so a malformed
// variable name derived from them could leak secret fragments into logs.
func KnownCANames[T any](cas map[string]T) string {
	return strings.Join(slices.Sorted(maps.Keys(cas)), ",")
}

func ReplaceCustomSCEPChallengeVariable(ctx context.Context, logger *slog.Logger, automdmVariable string, customSCEPCAs map[string]*automdm.CustomSCEPProxyCA, profileContents string) (contents string, replacedVariable bool, err error) {
	caName := strings.TrimPrefix(automdmVariable, string(automdm.AutoMDMVarCustomSCEPChallengePrefix))
	ca, ok := customSCEPCAs[caName]
	if !ok {
		logger.ErrorContext(ctx, "Custom SCEP CA not found. This error should never happen since we validated/populated CAs earlier",
			"known_cas", KnownCANames(customSCEPCAs))
		return "", false, nil
	}
	contents, err = ReplaceExactAutoMDMPrefixVariableInXML(string(automdm.AutoMDMVarCustomSCEPChallengePrefix), ca.Name, profileContents, ca.Challenge)
	if err != nil {
		return "", false, ctxerr.Wrap(ctx, err, "replacing AutoMDM variable for SCEP challenge")
	}
	return contents, true, nil
}

func ReplaceCustomSCEPProxyURLVariable(ctx context.Context, logger *slog.Logger, ds automdm.Datastore, appConfig *automdm.AppConfig,
	automdmVar string, customSCEPCAs map[string]*automdm.CustomSCEPProxyCA, profileContents string,
	hostUUID string, profUUID string,
) (contents string, managedCertificate *automdm.MDMManagedCertificate, replacedVariable bool, err error) {
	caName := strings.TrimPrefix(automdmVar, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix))
	ca, ok := customSCEPCAs[caName]
	if !ok {
		logger.ErrorContext(ctx, "Custom SCEP CA not found. This error should never happen since we validated/populated CAs earlier",
			"known_cas", KnownCANames(customSCEPCAs))
		return "", nil, false, nil
	}
	// Generate a new SCEP challenge for the profile
	challenge, err := ds.NewChallenge(ctx, automdm.ChallengeScope{HostUUID: hostUUID, ProfileUUID: profUUID, CAName: caName})
	if err != nil {
		return "", nil, false, ctxerr.Wrap(ctx, err, "generating SCEP challenge")
	}
	// Insert the SCEP URL into the profile contents
	proxyURL := fmt.Sprintf("%s%s%s", appConfig.MDMUrl(), SCEPProxyPath,
		url.PathEscape(fmt.Sprintf("%s,%s,%s,%s", hostUUID, profUUID, caName, challenge)))
	contents, err = ReplaceExactAutoMDMPrefixVariableInXML(string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix), ca.Name, profileContents, proxyURL)
	if err != nil {
		return "", nil, false, ctxerr.Wrap(ctx, err, "replacing AutoMDM variable for SCEP proxy URL")
	}

	managedCertificate = &automdm.MDMManagedCertificate{
		HostUUID:    hostUUID,
		ProfileUUID: profUUID,
		Type:        automdm.CAConfigCustomSCEPProxy,
		CAName:      caName,
	}

	return contents, managedCertificate, true, nil
}

func ReplaceHostEndUserIDPVariables(ctx context.Context, ds automdm.Datastore,
	automdmVar string, profileContents string, hostUUID string,
	hostIDForUUIDCache map[string]uint,
	onError func(errMsg string) error,
) (replacedContents string, replacedVariable bool, err error) {
	value, rx, ok, err := ResolveHostEndUserIDPValue(ctx, ds, automdmVar, hostUUID, hostIDForUUIDCache, onError)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	replacedContents = ReplaceAutoMDMVariableInXML(rx, profileContents, value)
	return replacedContents, true, nil
}

// ResolveHostEndUserIDPValue resolves the raw string value for the given IDP automdm variable and host.
func ResolveHostEndUserIDPValue(ctx context.Context, ds automdm.Datastore,
	automdmVar string, hostUUID string,
	hostIDForUUIDCache map[string]uint,
	onError func(errMsg string) error,
) (value string, rx *regexp.Regexp, ok bool, err error) {
	user, userOK, err := getHostEndUserIDPUser(ctx, ds, hostUUID, automdmVar, hostIDForUUIDCache, onError)
	if err != nil {
		return "", nil, false, err
	}
	if !userOK {
		return "", nil, false, nil
	}

	switch automdmVar {
	case string(automdm.AutoMDMVarHostEndUserIDPUsername):
		rx = automdm.AutoMDMVarHostEndUserIDPUsernameRegexp
		value = user.IdpUserName
	case string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart):
		rx = automdm.AutoMDMVarHostEndUserIDPUsernameLocalPartRegexp
		value = automdm.EmailLocalPart(user.IdpUserName)
	case string(automdm.AutoMDMVarHostEndUserIDPGroups):
		rx = automdm.AutoMDMVarHostEndUserIDPGroupsRegexp
		value = strings.Join(user.IdpGroups, ",")
	case string(automdm.AutoMDMVarHostEndUserIDPDepartment):
		rx = automdm.AutoMDMVarHostEndUserIDPDepartmentRegexp
		value = user.Department
	case string(automdm.AutoMDMVarHostEndUserIDPFullname):
		rx = automdm.AutoMDMVarHostEndUserIDPFullnameRegexp
		value = strings.TrimSpace(user.IdpFullName)
	case string(automdm.AutoMDMVarHostEndUserIDPExternalID):
		rx = automdm.AutoMDMVarHostEndUserIDPExternalIDRegexp
		value = user.IdpID
	default:
		return "", nil, false, nil
	}
	return value, rx, true, nil
}

func getHostEndUserIDPUser(ctx context.Context, ds automdm.Datastore,
	hostUUID, automdmVar string, hostIDForUUIDCache map[string]uint,
	onError func(errMsg string) error,
) (*automdm.HostEndUser, bool, error) {
	hostID, ok := hostIDForUUIDCache[hostUUID]
	if !ok {
		filter := automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}}
		ids, err := ds.HostIDsByIdentifier(ctx, filter, []string{hostUUID})
		if err != nil {
			return nil, false, ctxerr.Wrap(ctx, err, "get host id from uuid")
		}

		if len(ids) != 1 {
			// Something went wrong. Maybe host was deleted, or we have multiple
			// hosts with the same UUID.
			return nil, false, onError(fmt.Sprintf("Unexpected number of hosts (%d) for UUID %s. ", len(ids), hostUUID))
		}
		hostID = ids[0]
		// TODO figure out whether we should be passing a ref around here, as this looks like the cache doesn't persist
		hostIDForUUIDCache[hostUUID] = hostID
	}

	users, err := automdm.GetEndUsers(ctx, ds, hostID)
	if err != nil {
		return nil, false, ctxerr.Wrap(ctx, err, "get end users for host")
	}

	noGroupsErr := fmt.Sprintf("There are no IdP groups for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdm.AutoMDMVarHostEndUserIDPGroups)
	noDepartmentErr := fmt.Sprintf("There is no IdP department for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdm.AutoMDMVarHostEndUserIDPDepartment)
	noFullnameErr := fmt.Sprintf("There is no IdP full name for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdm.AutoMDMVarHostEndUserIDPFullname)
	noExternalIDErr := fmt.Sprintf("There is no IdP external ID for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdm.AutoMDMVarHostEndUserIDPExternalID)
	if len(users) > 0 && users[0].IdpUserName != "" {
		idpUser := users[0]

		if automdmVar == string(automdm.AutoMDMVarHostEndUserIDPGroups) && len(idpUser.IdpGroups) == 0 {
			return nil, false, onError(noGroupsErr)
		}
		if automdmVar == string(automdm.AutoMDMVarHostEndUserIDPDepartment) && idpUser.Department == "" {
			return nil, false, onError(noDepartmentErr)
		}
		if automdmVar == string(automdm.AutoMDMVarHostEndUserIDPFullname) && strings.TrimSpace(idpUser.IdpFullName) == "" {
			return nil, false, onError(noFullnameErr)
		}
		if automdmVar == string(automdm.AutoMDMVarHostEndUserIDPExternalID) && idpUser.IdpID == "" {
			return nil, false, onError(noExternalIDErr)
		}

		return &idpUser, true, nil
	}

	// otherwise there's no IdP user, mark the profile as failed with the
	// appropriate detail message.
	var detail string
	switch automdmVar {
	case string(automdm.AutoMDMVarHostEndUserIDPUsername), string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart):
		detail = fmt.Sprintf("There is no IdP username for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdmVar)
	case string(automdm.AutoMDMVarHostEndUserIDPGroups):
		detail = noGroupsErr
	case string(automdm.AutoMDMVarHostEndUserIDPDepartment):
		detail = noDepartmentErr
	case string(automdm.AutoMDMVarHostEndUserIDPFullname):
		detail = noFullnameErr
	case string(automdm.AutoMDMVarHostEndUserIDPExternalID):
		detail = noExternalIDErr
	}
	return nil, false, onError(detail)
}

func ReplaceExactAutoMDMPrefixVariableInXML(prefix string, suffix string, contents string, replacement string) (string, error) {
	// Escape XML characters since this replacement is intended for XML profile.
	b := make([]byte, 0, len(replacement))
	buf := bytes.NewBuffer(b)
	// error is always nil for Buffer.Write method, so we ignore it
	_ = xml.EscapeText(buf, []byte(replacement))

	// We are replacing an exact variable, which should be present in XML like: <something>$AUTOMDM_VAR_OUR_VAR</something>
	// We strip the leading/trailing whitespace since we don't want them to remain in XML
	// Our plist parser ignores spaces in <data> type. We don't catch this issue at profile validation, so we handle it here.
	automdmVar := "AUTOMDM_VAR_" + prefix + suffix
	re, err := regexp.Compile(fmt.Sprintf(`>\s*((\$%s)|(\${%s}))\s*<`, automdmVar, automdmVar))
	if err != nil {
		return "", err
	}
	return re.ReplaceAllLiteralString(contents, fmt.Sprintf(`>%s<`, buf.String())), nil
}

func ReplaceAutoMDMVariableInXML(regExp *regexp.Regexp, contents string, replacement string) string {
	// Escape XML characters since this replacement is intended for XML profile.
	b := make([]byte, 0, len(replacement))
	buf := bytes.NewBuffer(b)
	// error is always nil for Buffer.Write method, so we ignore it
	_ = xml.EscapeText(buf, []byte(replacement))
	return regExp.ReplaceAllLiteralString(contents, buf.String())
}

func IsCustomSCEPConfigured(ctx context.Context,
	customSCEPCAs map[string]*automdm.CustomSCEPProxyCA, caName string, automdmVar string,
	onError func(string) error, // A function that allows the caller to run some code on errors, if an error is returned it will be returned by IsCustomSCEPConfigured
) error {
	if !license.IsPremium(ctx) {
		return onError("Custom SCEP integration requires an AutoMDM Premium license.")
	}
	if _, ok := customSCEPCAs[caName]; !ok {
		return onError(fmt.Sprintf("AutoMDM couldn't populate $%s because %s certificate authority doesn't exist.", automdmVar, caName))
	}

	return nil
}

// SCEPProxyPath is the HTTP path that serves the SCEP proxy service. The path is followed by an identifier.
const SCEPProxyPath = "/mdm/scep/proxy/"

// BuildNDESSCEPProxyURL constructs the NDES SCEP proxy URL for a given host and profile.
func BuildNDESSCEPProxyURL(mdmURL string, hostUUID string, profileUUID string) string {
	return fmt.Sprintf("%s%s%s", mdmURL, SCEPProxyPath,
		url.PathEscape(fmt.Sprintf("%s,%s,NDES", hostUUID, profileUUID)))
}

// ReplaceNDESSCEPProxyURLVariable replaces the NDES SCEP proxy URL variable in profile contents.
func ReplaceNDESSCEPProxyURLVariable(mdmURL string, hostUUID string, profileUUID string, profileContents string) string {
	proxyURL := BuildNDESSCEPProxyURL(mdmURL, hostUUID, profileUUID)
	return ReplaceAutoMDMVariableInXML(automdm.AutoMDMVarNDESSCEPProxyURLRegexp, profileContents, proxyURL)
}

func HydrateHost(ctx context.Context, ds automdm.Datastore, hostLite automdm.Host, onHostCountMismatch func(int) error) (automdm.Host, bool, error) {
	if hostLite.ID != 0 { // already hydrated; return as-is
		return hostLite, true, nil
	}

	hosts, err := ds.ListHostsLiteByUUIDs(ctx, automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}}, []string{hostLite.UUID})
	if err != nil {
		return hostLite, false, ctxerr.Wrap(ctx, err, "listing hosts")
	}
	if len(hosts) != 1 {
		return hostLite, false, onHostCountMismatch(len(hosts))
	}
	return *hosts[0], true, nil
}
