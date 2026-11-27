package profiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/variables"
	"google.golang.org/api/androidmanagement/v1"
	"google.golang.org/api/googleapi"
)

// ErrUnresolvableAndroidAppConfigVar signals that one of the $AUTOMDM_VAR_*
// tokens referenced in an Android managed app configuration could not be
// resolved for the target host.
var ErrUnresolvableAndroidAppConfigVar = errors.New("android: unresolvable AutoMDM variable in managed app configuration")

// UnresolvableAndroidAppConfigVarError carries the unresolved variable name
// and a user-facing detail message.
type UnresolvableAndroidAppConfigVarError struct {
	AutoMDMVar string
	Detail     string
}

func (e *UnresolvableAndroidAppConfigVarError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("android: unresolvable AutoMDM variable $AUTOMDM_VAR_%s", e.AutoMDMVar)
}

func (e *UnresolvableAndroidAppConfigVarError) Is(target error) bool {
	return target == ErrUnresolvableAndroidAppConfigVar
}

// AndroidAppConfigSubstitutionHost carries the host context needed to
// substitute host-scoped $AUTOMDM_VAR_* tokens and $AUTOMDM_HOST_VITAL_<id>
// custom host vitals in Android managed app configuration.
type AndroidAppConfigSubstitutionHost struct {
	HostID         uint
	UUID           string
	HardwareSerial string
	Platform       string
}

// SubstituteAutoMDMVarsAndVitalsInAndroidAppConfig replaces every supported
// $AUTOMDM_VAR_* token and $AUTOMDM_HOST_VITAL_<id> custom host vital reference in
// config with the resolved value for the given host, returning the substituted
// bytes. End-user IDP fields are looked up via ds.
// Returns ErrUnresolvableAndroidAppConfigVar (wrapped) if the host can't supply
// a referenced variable, or an *automdm.MissingCustomHostVitalValueError if a
// referenced vital has no value set for this host.
func SubstituteAutoMDMVarsAndVitalsInAndroidAppConfig(
	ctx context.Context,
	ds automdm.Datastore,
	config []byte,
	host AndroidAppConfigSubstitutionHost,
) ([]byte, error) {
	if len(config) == 0 {
		return config, nil
	}
	contents := string(config)
	used := variables.Find(contents)
	hasHostVitals := len(automdm.FindCustomHostVitalIDs(contents)) > 0
	if len(used) == 0 && !hasHostVitals {
		return config, nil
	}

	idpUUIDCache := map[string]uint{}

	for _, name := range used {
		switch automdm.AutoMDMVarName(name) {
		case automdm.AutoMDMVarHostUUID:
			contents = replaceJSONSafe(contents, name, host.UUID)

		case automdm.AutoMDMVarHostHardwareSerial:
			if host.HardwareSerial == "" {
				return nil, &UnresolvableAndroidAppConfigVarError{
					AutoMDMVar: name,
					Detail:     fmt.Sprintf("There is no serial number for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", name),
				}
			}
			contents = replaceJSONSafe(contents, name, host.HardwareSerial)

		case automdm.AutoMDMVarHostPlatform:
			contents = replaceJSONSafe(contents, name, host.Platform)

		case automdm.AutoMDMVarHostEndUserIDPUsername,
			automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart,
			automdm.AutoMDMVarHostEndUserIDPGroups,
			automdm.AutoMDMVarHostEndUserIDPDepartment,
			automdm.AutoMDMVarHostEndUserIDPFullname,
			automdm.AutoMDMVarHostEndUserIDPExternalID:
			var detail string
			value, _, ok, err := ResolveHostEndUserIDPValue(
				ctx, ds, name, host.UUID, idpUUIDCache,
				func(d string) error { detail = d; return nil },
			)
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "resolve host idp variable for android app config")
			}
			if !ok {
				return nil, &UnresolvableAndroidAppConfigVarError{AutoMDMVar: name, Detail: detail}
			}
			contents = replaceJSONSafe(contents, name, value)

		default:
			// A profile or configuration saved before the upgrade can still use a variable AutoMDM 5 removed.
			var detail string
			if err := automdm.FindRemovedAutoMDMVariable(name); err != nil {
				detail = err.Error()
			}
			return nil, &UnresolvableAndroidAppConfigVarError{AutoMDMVar: name, Detail: detail}
		}
	}

	if hasHostVitals {
		expanded, err := ds.ExpandCustomHostVitals(ctx, host.HostID, contents)
		if err != nil {
			return nil, err
		}
		contents = expanded
	}

	return []byte(contents), nil
}

// ContainsAutoMDMVarOrCustomHostVital reports whether content has a $AUTOMDM_VAR_*
// token or a $AUTOMDM_HOST_VITAL_<id> token. Checks bytes for the vital prefix
// before falling back to automdm.FindCustomHostVitalIDs, which needs a string, to
// avoid that conversion's allocation in the common case where content has neither.
func ContainsAutoMDMVarOrCustomHostVital(content []byte) bool {
	if variables.ContainsBytes(content) {
		return true
	}
	if !bytes.Contains(content, []byte(automdm.CustomHostVitalPrefix)) {
		return false
	}
	return len(automdm.FindCustomHostVitalIDs(string(content))) > 0
}

// replaceJSONSafe replaces an AutoMDM variable in contents with a JSON-safe value.
func replaceJSONSafe(contents, variableName, value string) string {
	return variables.Replace(contents, variableName, jsonEscapeString(value))
}

// jsonEscapeString returns value with JSON special characters escaped,
// suitable for embedding inside a JSON string literal.
func jsonEscapeString(s string) string {
	b, _ := json.Marshal(s) // json.Marshal for strings never errors
	return strings.TrimSuffix(strings.TrimPrefix(string(b), `"`), `"`)
}

func BuildApplicationPoliciesWithConfig(ctx context.Context, appIDs []string,
	configsByAppID map[string][]byte, installType string,
) ([]*androidmanagement.ApplicationPolicy, error) {
	appPolicies := make([]*androidmanagement.ApplicationPolicy, 0, len(appIDs))
	for _, appID := range appIDs {
		appPolicy := &androidmanagement.ApplicationPolicy{}
		if config := configsByAppID[appID]; config != nil {
			if err := json.Unmarshal(config, appPolicy); err != nil {
				// should never happen, as it is stored as json in the db and is pre-validated
				return nil, ctxerr.Wrap(ctx, err, "unmarshal android app configuration")
			}
		} else {
			// if there is no config for this app, we must make sure we clear any previously-applied
			// config.
			appPolicy.ManagedConfiguration = googleapi.RawMessage{}
			appPolicy.WorkProfileWidgets = "WORK_PROFILE_WIDGETS_UNSPECIFIED"
			appPolicy.CredentialProviderPolicy = "CREDENTIAL_PROVIDER_POLICY_UNSPECIFIED"
		}
		// Set after unmarshalling so a stored config can never override them.
		appPolicy.PackageName = appID
		appPolicy.InstallType = installType
		// Google rejects installConstraint on AVAILABLE apps, which fails the whole request.
		// It only applies to installs Google starts, like the setup experience's.
		if installType == android.AppStatusAvailable {
			appPolicy.InstallConstraint = nil
		}
		appPolicies = append(appPolicies, appPolicy)
	}
	return appPolicies, nil
}

func SubstituteAutoMDMVarsInAndroidAppConfigs(
	ctx context.Context,
	ds automdm.Datastore,
	configsByAppID map[string][]byte,
	host AndroidAppConfigSubstitutionHost,
) (map[string][]byte, error) {
	if len(configsByAppID) == 0 {
		return configsByAppID, nil
	}

	hasVars := false
	for _, cfg := range configsByAppID {
		if ContainsAutoMDMVarOrCustomHostVital(cfg) {
			hasVars = true
			break
		}
	}
	if !hasVars {
		return configsByAppID, nil
	}

	result := make(map[string][]byte, len(configsByAppID))
	for appID, cfg := range configsByAppID {
		substituted, err := SubstituteAutoMDMVarsAndVitalsInAndroidAppConfig(ctx, ds, cfg, host)
		if err != nil {
			return nil, ctxerr.Wrapf(ctx, err, "substitute automdm vars in android app config for app %s", appID)
		}
		result[appID] = substituted
	}
	return result, nil
}

// IsUnresolvableAndroidAppConfigForHost separates "this host can't supply a
// referenced value" — a custom host vital with no value set for this host, or
// an unresolvable $AUTOMDM_VAR_* — from a real failure. The former is host state
// no retry can fix, so it's a per-host skip rather than a batch failure.
func IsUnresolvableAndroidAppConfigForHost(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*automdm.MissingCustomHostVitalValueError](err); ok {
		return true
	}
	return errors.Is(err, ErrUnresolvableAndroidAppConfigVar)
}
