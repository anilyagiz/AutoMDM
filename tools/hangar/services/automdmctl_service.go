package services

import "github.com/anilyagiz/automdm/tools/hangar/internal/automdmctl"

// AutoMDMctlService exposes automdmctl binary resolution, config read/write, and
// a capture runner. Mirrors automdmctl.rs.
type AutoMDMctlService struct{}

func (s *AutoMDMctlService) AutoMDMctlResolveBinary(repo, settingsPath string) automdmctl.ResolvedBinary {
	return automdmctl.ResolveBinary(repo, settingsPath)
}
func (s *AutoMDMctlService) AutoMDMctlReadContext() (automdmctl.ContextInfo, error) {
	return automdmctl.ReadContext(automdmctl.DefaultConfigPath())
}
func (s *AutoMDMctlService) AutoMDMctlReadConfigRaw() (automdmctl.RawConfig, error) {
	return automdmctl.ReadConfigRaw(automdmctl.DefaultConfigPath())
}
func (s *AutoMDMctlService) AutoMDMctlSaveConfig(yaml string) error {
	return automdmctl.SaveConfig(automdmctl.DefaultConfigPath(), yaml)
}
func (s *AutoMDMctlService) AutoMDMctlRunCapture(program, cwd string, args []string, env map[string]string, stdinData string, timeoutMS uint64) (automdmctl.CapturedRun, error) {
	return automdmctl.RunCapture(program, cwd, args, env, stdinData, timeoutMS)
}
