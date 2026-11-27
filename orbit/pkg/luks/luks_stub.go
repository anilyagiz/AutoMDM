//go:build !linux
// +build !linux

package luks

import (
	"context"
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// Run is a placeholder method for non-Linux builds.
func (lr *LuksRunner) Run(oc *automdm.OrbitConfig) error {
	return nil
}

// GetLuksDump is a placeholder method for non-Linux builds.
func GetLuksDump(ctx context.Context, devicePath string) (*LuksDump, error) {
	return nil, nil
}
