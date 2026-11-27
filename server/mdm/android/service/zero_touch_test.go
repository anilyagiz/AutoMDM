package service

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupZeroTouchService(t *testing.T) (*Service, *AndroidMockDS, *android_mock.Client) {
	t.Helper()

	automdmDS := InitCommonDSMocks()
	androidAPIClient := android_mock.Client{}
	androidAPIClient.InitCommonMocks()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, noopNewActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)

	return svc.(*Service), automdmDS, &androidAPIClient
}

func adminCtx(t *testing.T) context.Context {
	t.Helper()
	return viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
}

func TestZeroTouchStubReturnsErrMissingLicense(t *testing.T) {
	svc, _, _ := setupZeroTouchService(t)
	_, err := svc.GetZeroTouchConfiguration(adminCtx(t), nil)
	require.ErrorIs(t, err, automdm.ErrMissingLicense)
}

func TestZeroTouchTokenDeletedOnEnterpriseDelete(t *testing.T) {
	svc, automdmDS, apiClient := setupZeroTouchService(t)

	var tokenDeleted bool
	automdmDS.Store.DeleteZeroTouchEnrollmentTokensFunc = func(_ context.Context) error {
		tokenDeleted = true
		return nil
	}

	apiClient.EnterpriseDeleteFunc = func(_ context.Context, _ string) error {
		return nil
	}

	automdmDS.Store.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC00test"}, nil
	}

	err := svc.DeleteEnterprise(adminCtx(t))
	require.NoError(t, err)
	assert.True(t, tokenDeleted, "zero-touch tokens should be deleted when enterprise is deleted")
}

func TestResolveTeamFromEnrollmentData(t *testing.T) {
	svc, automdmDS, _ := setupZeroTouchService(t)

	t.Run("zero-touch with null team_id goes to unassigned", func(t *testing.T) {
		teamID, idpUUID, err := svc.resolveTeamFromEnrollmentData(t.Context(), `{"team_id": null}`)
		require.NoError(t, err)
		assert.Nil(t, teamID)
		assert.Empty(t, idpUUID)
	})

	t.Run("zero-touch with existing team_id goes to that team", func(t *testing.T) {
		automdmDS.Store.TeamExistsFunc = func(_ context.Context, id uint) (bool, error) {
			assert.Equal(t, uint(3), id)
			return true, nil
		}
		teamID, idpUUID, err := svc.resolveTeamFromEnrollmentData(t.Context(), `{"team_id": 3}`)
		require.NoError(t, err)
		require.NotNil(t, teamID)
		assert.Equal(t, uint(3), *teamID)
		assert.Empty(t, idpUUID)
	})

	t.Run("zero-touch with non-existent team_id falls back to unassigned", func(t *testing.T) {
		automdmDS.Store.TeamExistsFunc = func(_ context.Context, id uint) (bool, error) {
			return false, nil
		}
		teamID, idpUUID, err := svc.resolveTeamFromEnrollmentData(t.Context(), `{"team_id": 99}`)
		require.NoError(t, err)
		assert.Nil(t, teamID, "non-existent team should fall back to unassigned")
		assert.Empty(t, idpUUID)
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		_, _, err := svc.resolveTeamFromEnrollmentData(t.Context(), `not valid json`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshalling enrollment token data")
	})

	t.Run("QR enrollment with enroll secret falls back to existing flow", func(t *testing.T) {
		expectedTeamID := uint(5)
		automdmDS.Store.VerifyEnrollSecretFunc = func(_ context.Context, secret string) (*automdm.EnrollSecret, error) {
			assert.Equal(t, "test-secret", secret)
			return &automdm.EnrollSecret{Secret: secret, TeamID: &expectedTeamID}, nil
		}
		teamID, idpUUID, err := svc.resolveTeamFromEnrollmentData(t.Context(), `{"EnrollSecret": "test-secret", "IdpUUID": "some-uuid"}`)
		require.NoError(t, err)
		require.NotNil(t, teamID)
		assert.Equal(t, uint(5), *teamID)
		assert.Equal(t, "some-uuid", idpUUID)
	})
}

func TestZeroTouchConfigurationRequestDecode(t *testing.T) {
	decode := makeDecoder(zeroTouchConfigurationRequest{}, -1)

	cases := []struct {
		name       string
		query      string
		wantTeamID *uint
		wantErr    bool
	}{
		{name: "no team", query: "", wantTeamID: nil},
		{name: "team_id", query: "?team_id=5", wantTeamID: new(uint(5))},
		{name: "team_id 0", query: "?team_id=0", wantTeamID: new(uint(0))},
		{name: "not a number", query: "?team_id=abc", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/automdm/android_enterprise/zero_touch_configuration"+c.query, nil)
			got, err := decode(t.Context(), r)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantTeamID, got.(*zeroTouchConfigurationRequest).TeamID)
		})
	}
}
