package automdm

import "github.com/anilyagiz/automdm/v4/pkg/optjson"

// The profiles, scripts, setup assistant, setup experience script, and bootstrap package are
// applied and read through their own endpoints, so the config and team settings don't have
// them. GitOps and automdmctl apply files list them next to those settings, so the types below
// add them back for parsing those files.

// MacOSSetupSpec is setup_experience as team specs send it. The bootstrap package URL isn't
// saved: it only tells the server a package is coming, for the settings that need one, because
// GitOps uploads the package after applying the team's settings and dry runs never upload it.
type MacOSSetupSpec struct {
	MacOSSetup
	BootstrapPackage optjson.String `json:"macos_bootstrap_package,omitzero" removedkey:"bootstrap_package" renamescope:"setup_experience"`
}

// GitOpsMacOSSetup is setup_experience in GitOps and automdmctl apply files.
type GitOpsMacOSSetup struct {
	MacOSSetupSpec
	MacOSSetupAssistant optjson.String `json:"apple_setup_assistant" removedkey:"macos_setup_assistant" renamescope:"setup_experience"`
	Script              optjson.String `json:"macos_script" removedkey:"script" renamescope:"setup_experience"`
}

// SetDefaultsIfNeeded sets every setting the file leaves out to its default, so the file
// replaces what's there.
func (mos *GitOpsMacOSSetup) SetDefaultsIfNeeded() {
	if mos == nil {
		return
	}
	mos.MacOSSetup.SetDefaultsIfNeeded()
	if !mos.BootstrapPackage.Valid {
		mos.BootstrapPackage = optjson.SetString("")
	}
	if !mos.MacOSSetupAssistant.Valid {
		mos.MacOSSetupAssistant = optjson.SetString("")
	}
	if !mos.Script.Valid {
		mos.Script = optjson.SetString("")
	}
}

func NewGitOpsMacOSSetupWithDefaults() *GitOpsMacOSSetup {
	mos := &GitOpsMacOSSetup{}
	mos.SetDefaultsIfNeeded()
	return mos
}

// GitOpsMacOSSettings is apple_settings in GitOps and automdmctl apply files.
type GitOpsMacOSSettings struct {
	MacOSSettings
	CustomSettings []MDMProfileSpec `json:"configuration_profiles" removedkey:"custom_settings" renamescope:"apple_settings,windows_settings,android_settings"`
}

func (s GitOpsMacOSSettings) GetMDMProfileSpecs() []MDMProfileSpec {
	return s.CustomSettings
}

// GitOpsWindowsSettings is windows_settings in GitOps and automdmctl apply files.
type GitOpsWindowsSettings struct {
	WindowsSettings
	CustomSettings optjson.Slice[MDMProfileSpec] `json:"configuration_profiles" removedkey:"custom_settings" renamescope:"apple_settings,windows_settings,android_settings"`
}

func (s GitOpsWindowsSettings) GetMDMProfileSpecs() []MDMProfileSpec {
	return s.CustomSettings.Value
}

// GitOpsAndroidSettings is android_settings in GitOps and automdmctl apply files.
type GitOpsAndroidSettings struct {
	AndroidSettings
	CustomSettings optjson.Slice[MDMProfileSpec] `json:"configuration_profiles" removedkey:"custom_settings" renamescope:"apple_settings,windows_settings,android_settings"`
}

func (s GitOpsAndroidSettings) GetMDMProfileSpecs() []MDMProfileSpec {
	return s.CustomSettings.Value
}

var (
	_ WithMDMProfileSpecs = GitOpsMacOSSettings{}
	_ WithMDMProfileSpecs = GitOpsWindowsSettings{}
	_ WithMDMProfileSpecs = GitOpsAndroidSettings{}
)

// AppConfigRequestOnly holds the PATCH /config keys the server applies or validates with
// but doesn't save in the config.
type AppConfigRequestOnly struct {
	MDM AppConfigRequestOnlyMDM `json:"mdm"`
}

type AppConfigRequestOnlyMDM struct {
	VolumePurchasingProgram optjson.Slice[MDMAppleVolumePurchasingProgramInfo] `json:"volume_purchasing_program"`
	MacOSSetup              struct {
		BootstrapPackage optjson.String `json:"macos_bootstrap_package"`
	} `json:"setup_experience"`
}
