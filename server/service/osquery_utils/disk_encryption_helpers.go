package osquery_utils

import (
	"context"
	"log/slog"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// IsDiskEncryptionEscrowEnabledForHost reports whether AutoMDM should store a
// recovery key for this host, based on its platform's escrow-relevant setting
// in the host's team, or globally if the host is not assigned to a team.
func IsDiskEncryptionEscrowEnabledForHost(ctx context.Context, logger *slog.Logger, ds automdm.Datastore, host *automdm.Host) bool {
	var cfg automdm.DiskEncryptionConfig

	// team
	if host.TeamID != nil {
		teamMDM, err := ds.TeamMDMConfig(ctx, *host.TeamID)
		if err != nil {
			logger.DebugContext(ctx, "failed to get automdm MDM config for disk encryption check",
				"host_id", host.ID,
				"team_id", *host.TeamID,
				"err", err,
			)
			return false
		}
		if teamMDM == nil {
			return false
		}
		cfg = teamMDM.DiskEncryptionConfig()
	} else {
		// global
		appConfig, err := ds.AppConfig(ctx)
		if err != nil {
			logger.DebugContext(ctx, "failed to get app config for disk encryption check",
				"host_id", host.ID,
				"err", err,
			)
			return false
		}
		cfg = appConfig.MDM.DiskEncryptionConfig()
	}

	switch host.AutoMDMPlatform() {
	case "darwin":
		// enforcement alone escrows nothing: the profile omits the escrow
		// payloads, so no key is ever produced for AutoMDM to store
		return cfg.MacOSEscrowEnabled
	case "windows":
		// BitLocker escrow is not separately settable; enforcement implies it
		return cfg.WindowsEnabled
	case "linux":
		return cfg.LinuxEscrowEnabled
	default:
		return false
	}
}
