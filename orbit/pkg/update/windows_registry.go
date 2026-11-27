//go:build windows

package update

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const (
	// registry paths, absolute and relative to the HKEY_LOCAL_MACHINE root key - see
	// https://pkg.go.dev/golang.org/x/sys/windows/registry#LOCAL_MACHINE and
	// https://learn.microsoft.com/en-us/troubleshoot/windows-server/performance/windows-registry-advanced-users
	HKEY_LOCAL_MACHINE_PATH = `Computer\HKEY_LOCAL_MACHINE`
	REG_UNINSTALL_REL_PATH  = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	REG_UNINSTALL_ABS_PATH  = HKEY_LOCAL_MACHINE_PATH + `\` + REG_UNINSTALL_REL_PATH
)

func updateUninstallAutoMDMdRegistryVersion(newVersion string) error {
	// Since automdmd doesn't know its GUID key in the registry, iterate through all of them until we find
	// the appropriate key

	// path from the HKEY_LOCAL_MACHINE registry root key ("Computer\HKEY_LOCAL_MACHINE") to the key
	// for the uninstall AutoMDMd registry entry. That is, REG_UNINSTALL_REL_PATH + `\` + (GUID of
	// AutoMDMd entry ). This format is for compatibility with the registry.OpenKey function signature
	uninstallAutoMDMdRegRelPath, err := findUninstallAutoMDMdRegKeyRelPath()
	if err != nil {
		return fmt.Errorf(`couldn't find the uninstall automdmd registry key in '%v': %w`, REG_UNINSTALL_ABS_PATH, err)
	}

	setKey, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallAutoMDMdRegRelPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf(`couldn't open 'SET_VALUE' key handle for '%v\%v": %w`, HKEY_LOCAL_MACHINE_PATH, uninstallAutoMDMdRegRelPath, err)
	}
	defer setKey.Close()

	if err := setKey.SetStringValue("DisplayVersion", newVersion); err != nil {
		return fmt.Errorf(`couldn't set value 'DisplayVersion' for '%v\%v: %w`, HKEY_LOCAL_MACHINE_PATH, uninstallAutoMDMdRegRelPath, err)
	}
	return nil
}

// renameUninstallAutoMDMdRegistryEntry sets the name Installed apps shows for automdmd.
func renameUninstallAutoMDMdRegistryEntry(name string) error {
	uninstallAutoMDMdRegRelPath, err := findUninstallAutoMDMdRegKeyRelPath()
	if err != nil {
		return fmt.Errorf(`couldn't find the uninstall automdmd registry key in '%v': %w`, REG_UNINSTALL_ABS_PATH, err)
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallAutoMDMdRegRelPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf(`couldn't open key handle for '%v\%v': %w`, HKEY_LOCAL_MACHINE_PATH, uninstallAutoMDMdRegRelPath, err)
	}
	defer key.Close()
	if current, _, err := key.GetStringValue("DisplayName"); err == nil && current == name {
		return nil
	}
	if err := key.SetStringValue("DisplayName", name); err != nil {
		return fmt.Errorf(`couldn't set value 'DisplayName' for '%v\%v': %w`, HKEY_LOCAL_MACHINE_PATH, uninstallAutoMDMdRegRelPath, err)
	}
	return nil
}

func findUninstallAutoMDMdRegKeyRelPath() (string, error) {
	// get the existing keys in the Uninstall registry directory
	enumerateKeyHandle, err := registry.OpenKey(registry.LOCAL_MACHINE, REG_UNINSTALL_REL_PATH, registry.READ)
	if err != nil {
		return "", fmt.Errorf(`couldn't open registry key '%v': %w`, REG_UNINSTALL_ABS_PATH, err)
	}
	defer enumerateKeyHandle.Close()

	stat, err := enumerateKeyHandle.Stat()
	if err != nil {
		return "", fmt.Errorf(`couldn't get stat from registry key handle for '%v': %w`, REG_UNINSTALL_ABS_PATH, err)
	}
	subKeyCount := stat.SubKeyCount

	keys, err := enumerateKeyHandle.ReadSubKeyNames(int(subKeyCount))
	if err != nil {
		return "", fmt.Errorf(`couldn't read subkeys of registry key handle for '%v': %w`, REG_UNINSTALL_ABS_PATH, err)
	}

	// find the AutoMDMd entry in the existing keys
	var automdmdKey string
	for _, key := range keys {
		displayName, publisher, err := readUninstallEntryName(REG_UNINSTALL_REL_PATH + `\` + key)
		if err != nil {
			// Another app's unreadable or half-removed entry mustn't hide automdmd's.
			continue
		}
		if !isAutoMDMdUninstallEntry(displayName, publisher) {
			continue
		}
		// An old uninstall script can leave a renamed entry behind without the product's Windows Installer
		// registration, so the installed product's entry wins over the first match.
		if isInstalledProduct(key) {
			automdmdKey = key
			break
		}
		if automdmdKey == "" {
			automdmdKey = key
		}
	}

	if automdmdKey == "" {
		return "", errors.New(`couldn't find a corresponding registry value for automdmd in 'SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`)
	}

	return REG_UNINSTALL_REL_PATH + `\` + automdmdKey, nil
}

// isInstalledProduct returns whether an Installed apps entry's key is the product code of a product that Windows Installer
// has registered per machine, as automdmd's MSI installs.
func isInstalledProduct(key string) bool {
	packed, ok := packedProductCode(key)
	if !ok {
		return false
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Classes\Installer\Products\`+packed, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// readUninstallEntryName returns an Installed apps entry's DisplayName and Publisher.
func readUninstallEntryName(relPath string) (displayName, publisher string, err error) {
	keyHandle, err := registry.OpenKey(registry.LOCAL_MACHINE, relPath, registry.READ)
	if err != nil {
		return "", "", err
	}
	defer keyHandle.Close()
	displayName, _, err = keyHandle.GetStringValue("DisplayName")
	if err != nil {
		return "", "", err
	}
	// A missing or unreadable Publisher only rules out the renamed entry, so it isn't an error.
	publisher, _, _ = keyHandle.GetStringValue("Publisher")
	return displayName, publisher, nil
}
