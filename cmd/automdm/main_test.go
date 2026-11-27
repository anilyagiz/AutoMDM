package main

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/stretchr/testify/assert"
)

func TestApplyDevFlags_SkipS3Config(t *testing.T) {
	dev_mode.SetOverride("AUTOMDM_DEV_SKIP_S3_CONFIG", "1", t)

	cfg := &config.AutoMDMConfig{}
	applyDevFlags(cfg)

	assert.Empty(t, cfg.S3.CarvesBucket)
	assert.Empty(t, cfg.S3.SoftwareInstallersBucket)
}

func TestApplyDevFlags_DefaultsS3Config(t *testing.T) {
	dev_mode.SetOverride("AUTOMDM_DEV_SKIP_S3_CONFIG", "0", t)

	cfg := &config.AutoMDMConfig{}
	applyDevFlags(cfg)

	assert.Equal(t, "carves-dev", cfg.S3.CarvesBucket)
	assert.Equal(t, "software-installers-dev", cfg.S3.SoftwareInstallersBucket)
}
