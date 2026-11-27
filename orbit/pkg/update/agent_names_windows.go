//go:build windows

package update

import "github.com/anilyagiz/automdm/v4/orbit/pkg/constant"

// applyAgentDisplayNames renames automdmd's Installed apps entry. The installer keeps its "AutoMDM osquery" product name, so
// an installer repair brings that name back until automdmd's next start.
func applyAgentDisplayNames() error {
	return renameUninstallAutoMDMdRegistryEntry(constant.AgentDisplayName)
}
