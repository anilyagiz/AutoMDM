package update

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestSwiftDialogUpdatesDisabled(t *testing.T) {
	cfg := &automdm.OrbitConfig{}
	cfg.Notifications.NeedsMDMMigration = true
	cfg.Notifications.RenewEnrollmentProfile = true
	r := ApplySwiftDialogDownloaderMiddleware(nil)

	// we used to get a panic if updates were disabled (see #11980)
	err := r.Run(cfg)
	require.NoError(t, err)
}
