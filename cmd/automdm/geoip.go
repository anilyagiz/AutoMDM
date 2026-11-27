package main

import (
	"context"
	"log/slog"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
)

// initGeoIP returns the GeoIP provider for the server. GeoIP is best-effort:
// when no database path is configured, or the MaxMind database fails to load,
// it falls back to a no-op provider and logs the problem rather than aborting
// startup.
func initGeoIP(ctx context.Context, cfg config.AutoMDMConfig, logger *slog.Logger) automdm.GeoIP {
	if cfg.GeoIP.DatabasePath == "" {
		return &automdm.NoOpGeoIP{}
	}

	maxmind, err := automdm.NewMaxMindGeoIP(logger, cfg.GeoIP.DatabasePath)
	if err != nil {
		logger.ErrorContext(ctx, "failed to initialize maxmind geoip, check database path", "database_path",
			cfg.GeoIP.DatabasePath, "error", err)
		return &automdm.NoOpGeoIP{}
	}
	return maxmind
}
