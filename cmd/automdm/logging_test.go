package main

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/stretchr/testify/assert"
)

func TestShouldEnableAuditLog(t *testing.T) {
	premium := &automdm.LicenseInfo{Tier: automdm.TierPremium}
	free := &automdm.LicenseInfo{Tier: automdm.TierFree}

	for _, tc := range []struct {
		name      string
		license   *automdm.LicenseInfo
		enabled   bool
		wantAudit bool
	}{
		{name: "premium and enabled", license: premium, enabled: true, wantAudit: true},
		{name: "premium but disabled", license: premium, enabled: false, wantAudit: false},
		{name: "free but enabled is not allowed", license: free, enabled: true, wantAudit: false},
		{name: "free and disabled", license: free, enabled: false, wantAudit: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.AutoMDMConfig{}
			cfg.Activity.EnableAuditLog = tc.enabled
			assert.Equal(t, tc.wantAudit, shouldEnableAuditLog(tc.license, cfg))
		})
	}
}

func TestBuildLoggingConfigMapsConfig(t *testing.T) {
	cfg := config.AutoMDMConfig{}
	cfg.Firehose.Region = "us-east-1"
	cfg.PubSub.Project = "automdm-project"
	cfg.Nats.Server = "nats://localhost:4222"

	got := buildLoggingConfig(cfg)

	assert.Equal(t, "us-east-1", got.Firehose.Region)
	assert.Equal(t, "automdm-project", got.PubSub.Project)
	assert.Equal(t, "nats://localhost:4222", got.Nats.Server)
}
