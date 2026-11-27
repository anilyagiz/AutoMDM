//go:build !windows

package update

func updateUninstallAutoMDMdRegistryVersion(newVersion string) error {
	return nil
}
