package android

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
)

const zeroTouchConfigPath = "/api/v1/automdm/android_enterprise/zero_touch_configuration"

func TestAndroidZeroTouchPremium(t *testing.T) {
	s := SetUpSuite(t, "integrationtest.AndroidZeroTouchPremium", WithEEAndroidService(automdm.TierPremium))

	cases := []struct {
		name string
		fn   func(t *testing.T, s *Suite)
	}{
		{"AndroidNotConfigured", testZeroTouchAndroidNotConfigured},
		{"DestinationTeams", testZeroTouchDestinationTeams},
		{"NonAdminsRejected", testZeroTouchNonAdminsRejected},
		{"DeletedTeam", testZeroTouchDeletedTeam},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Truncating everything would also drop the users and sessions the suite logs in with.
			defer mysqltest.TruncateTables(t, s.DS, "android_zero_touch_tokens", "android_enterprises", "teams")
			c.fn(t, s)
		})
	}
}

func TestAndroidZeroTouchFree(t *testing.T) {
	s := SetUpSuite(t, "integrationtest.AndroidZeroTouchFree", WithEEAndroidService(automdm.TierFree))
	amapiCalls := setUpZeroTouch(t, s)

	automdmA, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-free"})
	require.NoError(t, err)

	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusPaymentRequired)
	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusPaymentRequired, "team_id", fmt.Sprint(automdmA.ID))
	assert.Empty(t, *amapiCalls)
}

// setUpZeroTouch turns on Android MDM, creates an enterprise, and records the
// additionalData of every AMAPI enrollment token AutoMDM creates.
func setUpZeroTouch(t *testing.T, s *Suite) *[]string {
	t.Helper()

	appCfg, err := s.DS.AppConfig(t.Context())
	require.NoError(t, err)
	appCfg.MDM.AndroidEnabledAndConfigured = true
	require.NoError(t, s.DS.SaveAppConfig(t.Context(), appCfg))
	t.Cleanup(func() {
		appCfg.MDM.AndroidEnabledAndConfigured = false
		// t.Context() is already canceled when cleanups run.
		require.NoError(t, s.DS.SaveAppConfig(context.Background(), appCfg))
	})

	enterpriseID, err := s.DS.CreateEnterprise(t.Context(), s.Users["admin1"].ID)
	require.NoError(t, err)
	require.NoError(t, s.DS.UpdateEnterprise(t.Context(), &android.EnterpriseDetails{
		ID:           enterpriseID,
		EnterpriseID: "LC00zerotouch",
	}))

	var additionalData []string
	s.AndroidProxy.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, enterpriseName string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
		assert.Equal(t, "enterprises/LC00zerotouch", enterpriseName)
		additionalData = append(additionalData, token.AdditionalData)
		n := len(additionalData)
		return &androidmanagement.EnrollmentToken{
			Name:                fmt.Sprintf("enterprises/LC00zerotouch/enrollmentTokens/%d", n),
			Value:               fmt.Sprintf("zero-touch-token-%d", n),
			ExpirationTimestamp: time.Now().Add(100 * 365 * 24 * time.Hour).Format(time.RFC3339),
		}, nil
	}
	return &additionalData
}

func getZeroTouchToken(t *testing.T, s *Suite, queryParams ...string) string {
	t.Helper()
	var resp map[string]map[string]string
	s.DoJSON(t, "GET", zeroTouchConfigPath, nil, http.StatusOK, &resp, queryParams...)
	bundle, ok := resp["android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE"]
	require.True(t, ok, "response is missing the admin extras bundle: %v", resp)
	return bundle["com.google.android.apps.work.clouddpc.EXTRA_ENROLLMENT_TOKEN"]
}

func countZeroTouchTokens(t *testing.T, s *Suite) int {
	t.Helper()
	var count int
	mysqltest.ExecAdhocSQL(t, s.DS, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(t.Context(), q, &count, `SELECT COUNT(*) FROM android_zero_touch_tokens`)
	})
	return count
}

func testZeroTouchAndroidNotConfigured(t *testing.T, s *Suite) {
	automdmA, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-no-android"})
	require.NoError(t, err)

	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusNotFound)
	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusNotFound, "team_id", fmt.Sprint(automdmA.ID))
}

func testZeroTouchDestinationTeams(t *testing.T, s *Suite) {
	amapiCalls := setUpZeroTouch(t, s)

	automdmA, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-a"})
	require.NoError(t, err)
	automdmB, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-b"})
	require.NoError(t, err)
	automdmAID := strconv.FormatUint(uint64(automdmA.ID), 10)
	automdmBID := strconv.FormatUint(uint64(automdmB.ID), 10)

	// Unassigned, with no param and with team_id=0, shares one token.
	unassignedToken := getZeroTouchToken(t, s)
	require.Len(t, *amapiCalls, 1)
	assert.JSONEq(t, `{"team_id":null}`, (*amapiCalls)[0])
	assert.Equal(t, unassignedToken, getZeroTouchToken(t, s, "team_id", "0"))

	automdmAToken := getZeroTouchToken(t, s, "team_id", automdmAID)
	require.Len(t, *amapiCalls, 2)
	assert.JSONEq(t, fmt.Sprintf(`{"team_id":%d}`, automdmA.ID), (*amapiCalls)[1])
	assert.NotEqual(t, unassignedToken, automdmAToken)

	// Repeat requests reuse the stored token.
	assert.Equal(t, automdmAToken, getZeroTouchToken(t, s, "team_id", automdmAID))
	require.Len(t, *amapiCalls, 2)

	automdmBToken := getZeroTouchToken(t, s, "team_id", automdmBID)
	require.Len(t, *amapiCalls, 3)
	assert.JSONEq(t, fmt.Sprintf(`{"team_id":%d}`, automdmB.ID), (*amapiCalls)[2])
	assert.NotEqual(t, automdmAToken, automdmBToken)

	assert.Equal(t, 3, countZeroTouchTokens(t, s))

	stored, err := s.DS.GetZeroTouchEnrollmentToken(t.Context(), &automdmA.ID)
	require.NoError(t, err)
	assert.Equal(t, automdmAToken, stored.TokenValue)

	// A team that doesn't exist is a 404 and never reaches AMAPI.
	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusNotFound, "team_id", "999999")
	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusBadRequest, "team_id", "abc")
	assert.Len(t, *amapiCalls, 3)
	assert.Equal(t, 3, countZeroTouchTokens(t, s))
}

func testZeroTouchNonAdminsRejected(t *testing.T, s *Suite) {
	amapiCalls := setUpZeroTouch(t, s)

	automdmA, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-authz"})
	require.NoError(t, err)

	adminToken := s.Token
	t.Cleanup(func() { s.Token = adminToken })

	for _, email := range []string{service.TestMaintainerUserEmail, service.TestObserverUserEmail} {
		t.Run(email, func(t *testing.T) {
			s.Token = s.GetTestToken(t, email, test.GoodPassword)
			s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusForbidden)
			s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusForbidden, "team_id", fmt.Sprint(automdmA.ID))
			// A missing team is still 403, so non-admins can't probe which teams exist.
			s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusForbidden, "team_id", "999999")
		})
	}

	assert.Empty(t, *amapiCalls)
	assert.Zero(t, countZeroTouchTokens(t, s))
}

func testZeroTouchDeletedTeam(t *testing.T, s *Suite) {
	amapiCalls := setUpZeroTouch(t, s)

	deletedAutoMDM, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-deleted"})
	require.NoError(t, err)
	keptAutoMDM, err := s.DS.NewTeam(t.Context(), &automdm.Team{Name: "zero-touch-kept"})
	require.NoError(t, err)

	getZeroTouchToken(t, s, "team_id", fmt.Sprint(deletedAutoMDM.ID))
	keptToken := getZeroTouchToken(t, s, "team_id", fmt.Sprint(keptAutoMDM.ID))
	require.Equal(t, 2, countZeroTouchTokens(t, s))

	require.NoError(t, s.DS.DeleteTeam(t.Context(), deletedAutoMDM.ID))

	assert.Equal(t, 1, countZeroTouchTokens(t, s))
	_, err = s.DS.GetZeroTouchEnrollmentToken(t.Context(), &deletedAutoMDM.ID)
	assert.True(t, automdm.IsNotFound(err))

	s.Do(t, "GET", zeroTouchConfigPath, nil, http.StatusNotFound, "team_id", fmt.Sprint(deletedAutoMDM.ID))
	assert.Equal(t, keptToken, getZeroTouchToken(t, s, "team_id", fmt.Sprint(keptAutoMDM.ID)))
	assert.Len(t, *amapiCalls, 2)
}
