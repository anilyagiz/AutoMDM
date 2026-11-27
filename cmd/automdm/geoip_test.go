package main

import (
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/stretchr/testify/assert"
)

func TestInitGeoIP(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("no database path returns no-op provider", func(t *testing.T) {
		got := initGeoIP(t.Context(), config.AutoMDMConfig{}, logger)
		assert.IsType(t, &automdm.NoOpGeoIP{}, got)
	})

	t.Run("invalid database path falls back to no-op, not fatal", func(t *testing.T) {
		cfg := config.AutoMDMConfig{}
		cfg.GeoIP.DatabasePath = "/nonexistent/geoip.mmdb"
		got := initGeoIP(t.Context(), cfg, logger)
		assert.IsType(t, &automdm.NoOpGeoIP{}, got)
	})
}
