package automdm_test

import (
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	chartapi "github.com/anilyagiz/automdm/v4/server/chart/api"
	"github.com/stretchr/testify/require"
)

// TestMobileOnlineWindowMatchesChart guards against drift between
// automdm.MobileOnlineWindow (used by Host.mobileStatus and the mysql predicates)
// and chartapi.MobileOnlineWindowSeconds (used by the chart bounded context's
// FindOnlineHostIDs). The two live in different packages because server/chart
// can't import server/automdm; this test is the only automated sync check.
func TestMobileOnlineWindowMatchesChart(t *testing.T) {
	require.Equal(t,
		chartapi.MobileOnlineWindowSeconds,
		int(automdm.MobileOnlineWindow/time.Second),
		"automdm.MobileOnlineWindow and chartapi.MobileOnlineWindowSeconds drifted; edit both together",
	)
}
