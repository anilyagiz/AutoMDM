package automdm

import (
	"fmt"
	"slices"

	"github.com/anilyagiz/automdm/v4/server/variables"
)

// AutoMDMVarsSupportedInScripts is the allow-list of built-in variables that can
// be used in script contents. They are resolved per host when automdmd fetches
// the script.
var AutoMDMVarsSupportedInScripts = []AutoMDMVarName{
	AutoMDMVarHostEndUserIDPUsername,
	AutoMDMVarHostEndUserIDPUsernameLocalPart,
	AutoMDMVarHostEndUserIDPFullname,
	AutoMDMVarHostEndUserIDPGroups,
	AutoMDMVarHostEndUserIDPDepartment,
	AutoMDMVarHostEndUserIDPExternalID,
	AutoMDMVarHostHardwareSerial,
	AutoMDMVarHostUUID,
	AutoMDMVarHostPlatform,
	AutoMDMVarNDESSCEPChallenge,
}

// FindUnsupportedScriptAutoMDMVar returns the name of a $AUTOMDM_VAR_* reference,
// from the names returned by variables.Find, that is not supported in
// scripts, or "" if all are supported.
func FindUnsupportedScriptAutoMDMVar(automdmVars []string) string {
	for _, v := range automdmVars {
		if !slices.Contains(AutoMDMVarsSupportedInScripts, AutoMDMVarName(v)) {
			return v
		}
	}
	return ""
}

// ValidateAutoMDMVariablesInScript returns an error if the script contents
// reference an AutoMDM variable that is not supported in scripts, or if variables
// are used without a premium license.
func ValidateAutoMDMVariablesInScript(contents string, isPremium bool) error {
	automdmVars := variables.Find(contents)
	if len(automdmVars) == 0 {
		return nil
	}
	if !isPremium {
		return ErrMissingLicense
	}
	if v := FindUnsupportedScriptAutoMDMVar(automdmVars); v != "" {
		return NewInvalidArgumentError("script",
			fmt.Sprintf("AutoMDM variable $AUTOMDM_VAR_%s is not supported in scripts.", v))
	}
	return nil
}
