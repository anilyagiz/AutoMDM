package update

import (
	"encoding/hex"
	"strings"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/constant"
)

// automdmdInstallerName is the product name of automdmd's MSI, which Installed apps shows until automdmd renames its entry.
const automdmdInstallerName = "AutoMDM osquery"

// isAutoMDMdUninstallEntry returns whether a Windows Installed apps entry is automdmd's. Other apps are called "AutoMDM" too,
// so the name automdmd renames its entry to also needs AutoMDM's publisher. The installer's name is matched alone, as it
// always was.
func isAutoMDMdUninstallEntry(displayName, publisher string) bool {
	switch displayName {
	case automdmdInstallerName:
		return true
	case constant.AgentDisplayName:
		return publisher == constant.AgentPublisher
	default:
		return false
	}
}

// packedProductCode returns a product code such as {AFA95216-3541-4281-BD30-8C16737ACBA1} in the form Windows Installer
// names its registry keys with: the first three groups reversed, then the two digits of each remaining byte swapped.
func packedProductCode(code string) (string, bool) {
	if len(code) != 38 || code[0] != '{' || code[37] != '}' {
		return "", false
	}
	groups := strings.Split(code[1:37], "-")
	if len(groups) != 5 || len(groups[0]) != 8 || len(groups[1]) != 4 || len(groups[2]) != 4 || len(groups[3]) != 4 {
		return "", false
	}
	if _, err := hex.DecodeString(strings.Join(groups, "")); err != nil {
		return "", false
	}
	var b strings.Builder
	for _, g := range groups[:3] {
		for i := len(g) - 1; i >= 0; i-- {
			b.WriteByte(g[i])
		}
	}
	rest := groups[3] + groups[4]
	for i := 0; i < len(rest); i += 2 {
		b.WriteByte(rest[i+1])
		b.WriteByte(rest[i])
	}
	return b.String(), true
}
