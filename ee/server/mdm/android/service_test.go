package android

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	licensectx "github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	ds_mock "github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
)

func premiumAdminCtx(t *testing.T) context.Context {
	t.Helper()
	ctx := licensectx.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	return viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
}

func setupEEService(t *testing.T) (*Service, *ds_mock.Store, *android_mock.Client) {
	t.Helper()

	mockDS := &ds_mock.Store{}
	apiClient := &android_mock.Client{}
	apiClient.InitCommonMocks()

	// Default mocks
	mockDS.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{AndroidEnabledAndConfigured: true}}, nil
	}
	mockDS.GetAllMDMConfigAssetsByNameFunc = func(_ context.Context, names []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		result := make(map[automdm.MDMAssetName]automdm.MDMConfigAsset, len(names))
		for _, name := range names {
			result[name] = automdm.MDMConfigAsset{Value: []byte("value")}
		}
		return result, nil
	}

	// Stub core service (returns ErrMissingLicense for GetZeroTouchConfiguration)
	stubCore := &stubCoreService{}

	svc, err := NewService(stubCore, mockDS, mockDS, apiClient, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return svc, mockDS, apiClient
}

// stubCoreService satisfies android.Service. GetZeroTouchConfiguration returns
// ErrMissingLicense (matching the real core stub). Other methods panic if called.
type stubCoreService struct {
	android.Service
	ensurePolicyErr     error
	ensuredPolicyTeamID []uint
}

func (s *stubCoreService) EnsureAutoMDMDefaultPolicy(_ context.Context, enterpriseName string, teamID uint) (string, error) {
	s.ensuredPolicyTeamID = append(s.ensuredPolicyTeamID, teamID)
	if s.ensurePolicyErr != nil {
		return "", s.ensurePolicyErr
	}
	return enterpriseName + "/policies/" + android.AutoMDMDefaultPolicyID(teamID), nil
}

func TestGetZeroTouchConfiguration_PremiumRequired(t *testing.T) {
	svc, _, _ := setupEEService(t)

	// No license in context
	_, err := svc.GetZeroTouchConfiguration(t.Context(), nil)
	require.ErrorIs(t, err, automdm.ErrMissingLicense)
}

func TestGetZeroTouchConfiguration_FreeLicense(t *testing.T) {
	svc, _, _ := setupEEService(t)

	ctx := licensectx.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierFree})
	_, err := svc.GetZeroTouchConfiguration(ctx, nil)
	require.ErrorIs(t, err, automdm.ErrMissingLicense)
}

func TestGetZeroTouchConfiguration_AndroidNotConfigured(t *testing.T) {
	svc, mockDS, _ := setupEEService(t)

	mockDS.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{AndroidEnabledAndConfigured: false}}, nil
	}

	_, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Android MDM is NOT configured")
}

func TestGetZeroTouchConfiguration_ReturnsExistingToken(t *testing.T) {
	svc, mockDS, _ := setupEEService(t)

	mockDS.GetZeroTouchEnrollmentTokenFunc = func(_ context.Context, teamID *uint) (*android.ZeroTouchToken, error) {
		assert.Nil(t, teamID)
		return &android.ZeroTouchToken{
			TokenValue:        "existing-token-value",
			ExpiresAt:         time.Now().Add(100 * 365 * 24 * time.Hour),
			UsesAutoMDMPolicy: true,
		}, nil
	}

	resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), nil)
	require.NoError(t, err)

	var extras dpcExtras
	require.NoError(t, json.Unmarshal(resp.DPCExtras, &extras))
	assert.Equal(t, "existing-token-value", extras.AdminExtrasBundle.EnrollmentToken)

	assert.True(t, mockDS.GetZeroTouchEnrollmentTokenFuncInvoked)
	assert.False(t, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
	assert.False(t, mockDS.ReplaceZeroTouchEnrollmentTokenFuncInvoked)
}

// A token created against the global default policy is replaced the next time it's viewed, so admins who
// copy the DPC extras again get the passcode during setup.
func TestGetZeroTouchConfiguration_ReplacesLegacyToken(t *testing.T) {
	teamID := uint(7)
	legacy := &android.ZeroTouchToken{
		TeamID:     &teamID,
		TokenName:  "enterprises/LC00test/enrollmentTokens/legacy",
		TokenValue: "legacy-token-value",
		ExpiresAt:  time.Now().Add(100 * 365 * 24 * time.Hour),
	}

	setup := func(t *testing.T) (*Service, *ds_mock.Store, *android_mock.Client, *stubCoreService) {
		svc, mockDS, apiClient := setupEEService(t)
		mockDS.TeamExistsFunc = func(_ context.Context, id uint) (bool, error) { return true, nil }
		mockDS.GetZeroTouchEnrollmentTokenFunc = func(_ context.Context, _ *uint) (*android.ZeroTouchToken, error) {
			return legacy, nil
		}
		mockDS.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{EnterpriseID: "LC00test"}, nil
		}
		apiClient.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, _ string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
			assert.Equal(t, "enterprises/LC00test/policies/automdm7", token.PolicyName)
			return &androidmanagement.EnrollmentToken{
				Name:                "enterprises/LC00test/enrollmentTokens/new",
				Value:               "new-token-value",
				ExpirationTimestamp: time.Now().Add(100 * 365 * 24 * time.Hour).Format(time.RFC3339),
			}, nil
		}
		return svc, mockDS, apiClient, svc.Service.(*stubCoreService)
	}
	enrollmentToken := func(t *testing.T, resp *android.ZeroTouchConfigurationResponse) string {
		var extras dpcExtras
		require.NoError(t, json.Unmarshal(resp.DPCExtras, &extras))
		return extras.AdminExtrasBundle.EnrollmentToken
	}

	t.Run("replaced", func(t *testing.T) {
		svc, mockDS, _, core := setup(t)
		mockDS.ReplaceZeroTouchEnrollmentTokenFunc = func(_ context.Context, oldTokenName string, token *android.ZeroTouchToken) (*android.ZeroTouchToken, error) {
			assert.Equal(t, legacy.TokenName, oldTokenName)
			assert.Equal(t, &teamID, token.TeamID)
			assert.True(t, token.UsesAutoMDMPolicy)
			return token, nil
		}

		resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), &teamID)
		require.NoError(t, err)
		assert.Equal(t, "new-token-value", enrollmentToken(t, resp))
		assert.Equal(t, []uint{teamID}, core.ensuredPolicyTeamID)
		assert.False(t, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
	})

	t.Run("policy failure keeps the old token", func(t *testing.T) {
		svc, mockDS, apiClient, core := setup(t)
		var logs bytes.Buffer
		svc.logger = slog.New(slog.NewTextHandler(&logs, nil))
		core.ensurePolicyErr = errors.New("quota")

		resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), &teamID)
		require.NoError(t, err)
		assert.Equal(t, "legacy-token-value", enrollmentToken(t, resp))
		assert.False(t, apiClient.EnterprisesEnrollmentTokensCreateFuncInvoked)
		assert.False(t, mockDS.ReplaceZeroTouchEnrollmentTokenFuncInvoked)
		assert.Contains(t, logs.String(), "team_id=7 ", "the team ID is logged, not its pointer")
	})

	t.Run("save failure keeps the old token", func(t *testing.T) {
		svc, mockDS, _, _ := setup(t)
		var logs bytes.Buffer
		svc.logger = slog.New(slog.NewTextHandler(&logs, nil))
		mockDS.ReplaceZeroTouchEnrollmentTokenFunc = func(_ context.Context, _ string, _ *android.ZeroTouchToken) (*android.ZeroTouchToken, error) {
			return nil, errors.New("db down")
		}

		resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), &teamID)
		require.NoError(t, err)
		assert.Equal(t, "legacy-token-value", enrollmentToken(t, resp))
		assert.Contains(t, logs.String(), "team_id=7 ", "the team ID is logged, not its pointer")
	})
}

// A team without a token still gets one when its default policy can't be created; it points at the global
// policy and is marked as such, so the next view replaces it.
func TestGetZeroTouchConfiguration_NewTokenFallsBackToGlobalPolicy(t *testing.T) {
	svc, mockDS, apiClient := setupEEService(t)
	svc.Service.(*stubCoreService).ensurePolicyErr = errors.New("quota")

	mockDS.GetZeroTouchEnrollmentTokenFunc = func(_ context.Context, _ *uint) (*android.ZeroTouchToken, error) {
		return nil, &notFoundError{}
	}
	mockDS.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC00test"}, nil
	}
	apiClient.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, _ string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
		assert.Equal(t, "enterprises/LC00test/policies/1", token.PolicyName)
		return &androidmanagement.EnrollmentToken{
			Name:                "enterprises/LC00test/enrollmentTokens/global",
			Value:               "global-token-value",
			ExpirationTimestamp: time.Now().Add(100 * 365 * 24 * time.Hour).Format(time.RFC3339),
		}, nil
	}
	mockDS.CreateZeroTouchEnrollmentTokenFunc = func(_ context.Context, token *android.ZeroTouchToken) (*android.ZeroTouchToken, error) {
		assert.False(t, token.UsesAutoMDMPolicy)
		return token, nil
	}

	resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), nil)
	require.NoError(t, err)
	var extras dpcExtras
	require.NoError(t, json.Unmarshal(resp.DPCExtras, &extras))
	assert.Equal(t, "global-token-value", extras.AdminExtrasBundle.EnrollmentToken)
	assert.True(t, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
}

func TestGetZeroTouchConfiguration_CreatesTokenWhenNoneExists(t *testing.T) {
	svc, mockDS, apiClient := setupEEService(t)

	mockDS.GetZeroTouchEnrollmentTokenFunc = func(_ context.Context, _ *uint) (*android.ZeroTouchToken, error) {
		return nil, &notFoundError{}
	}
	mockDS.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC00test"}, nil
	}

	apiClient.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, enterpriseName string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
		assert.Equal(t, "enterprises/LC00test", enterpriseName)
		assert.Equal(t, zeroTouchTokenDuration, token.Duration)
		assert.False(t, token.OneTimeOnly)
		assert.Equal(t, "PERSONAL_USAGE_DISALLOWED", token.AllowPersonalUsage)
		assert.Contains(t, token.AdditionalData, `"team_id"`)
		assert.Equal(t, "enterprises/LC00test/policies/automdm0", token.PolicyName)
		return &androidmanagement.EnrollmentToken{
			Name:                "enterprises/LC00test/enrollmentTokens/abc123",
			Value:               "new-token-value",
			ExpirationTimestamp: time.Now().Add(100 * 365 * 24 * time.Hour).Format(time.RFC3339),
		}, nil
	}

	mockDS.CreateZeroTouchEnrollmentTokenFunc = func(_ context.Context, token *android.ZeroTouchToken) (*android.ZeroTouchToken, error) {
		assert.Equal(t, "enterprises/LC00test/enrollmentTokens/abc123", token.TokenName)
		assert.Equal(t, "new-token-value", token.TokenValue)
		assert.Nil(t, token.TeamID)
		assert.True(t, token.UsesAutoMDMPolicy)
		token.ID = 1
		return token, nil
	}

	resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), nil)
	require.NoError(t, err)

	var extras dpcExtras
	require.NoError(t, json.Unmarshal(resp.DPCExtras, &extras))
	assert.Equal(t, "new-token-value", extras.AdminExtrasBundle.EnrollmentToken)

	assert.True(t, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
	assert.True(t, apiClient.EnterprisesEnrollmentTokensCreateFuncInvoked)
}

type notFoundError struct{}

func (e *notFoundError) Error() string    { return "not found" }
func (e *notFoundError) IsNotFound() bool { return true }

func TestGetZeroTouchConfiguration_AutoMDMSelection(t *testing.T) {
	const existingAutoMDMID uint = 3

	cases := []struct {
		name               string
		automdmID          *uint
		hasExistingToken   bool
		wantTeamID         *uint
		wantAdditionalData string
		wantNotFound       bool
		wantTeamExistsCall bool
		wantAMAPICall      bool
	}{
		{
			name:               "no team creates an Unassigned token",
			automdmID:          nil,
			wantTeamID:         nil,
			wantAdditionalData: `{"team_id":null}`,
			wantAMAPICall:      true,
		},
		{
			name:               "team 0 is treated as Unassigned",
			automdmID:          new(uint(0)),
			wantTeamID:         nil,
			wantAdditionalData: `{"team_id":null}`,
			wantAMAPICall:      true,
		},
		{
			name:               "existing team creates a token for that team",
			automdmID:          new(existingAutoMDMID),
			wantTeamID:         new(existingAutoMDMID),
			wantAdditionalData: `{"team_id":3}`,
			wantTeamExistsCall: true,
			wantAMAPICall:      true,
		},
		{
			name:               "existing team with a token reuses it",
			automdmID:          new(existingAutoMDMID),
			hasExistingToken:   true,
			wantTeamID:         new(existingAutoMDMID),
			wantTeamExistsCall: true,
		},
		{
			name:               "missing team returns not found",
			automdmID:          new(uint(999)),
			wantNotFound:       true,
			wantTeamExistsCall: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, mockDS, apiClient := setupEEService(t)

			mockDS.TeamExistsFunc = func(_ context.Context, id uint) (bool, error) {
				return id == existingAutoMDMID, nil
			}
			mockDS.GetZeroTouchEnrollmentTokenFunc = func(_ context.Context, teamID *uint) (*android.ZeroTouchToken, error) {
				assert.Equal(t, c.wantTeamID, teamID)
				if c.hasExistingToken {
					return &android.ZeroTouchToken{TeamID: teamID, TokenValue: "existing-token-value", UsesAutoMDMPolicy: true}, nil
				}
				return nil, &notFoundError{}
			}
			mockDS.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
				return &android.Enterprise{EnterpriseID: "LC00test"}, nil
			}
			apiClient.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, _ string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
				assert.JSONEq(t, c.wantAdditionalData, token.AdditionalData)
				return &androidmanagement.EnrollmentToken{
					Name:                "enterprises/LC00test/enrollmentTokens/abc123",
					Value:               "new-token-value",
					ExpirationTimestamp: time.Now().Add(100 * 365 * 24 * time.Hour).Format(time.RFC3339),
				}, nil
			}
			mockDS.CreateZeroTouchEnrollmentTokenFunc = func(_ context.Context, token *android.ZeroTouchToken) (*android.ZeroTouchToken, error) {
				assert.Equal(t, c.wantTeamID, token.TeamID)
				token.ID = 1
				return token, nil
			}

			resp, err := svc.GetZeroTouchConfiguration(premiumAdminCtx(t), c.automdmID)
			assert.Equal(t, c.wantTeamExistsCall, mockDS.TeamExistsFuncInvoked)
			assert.Equal(t, c.wantAMAPICall, apiClient.EnterprisesEnrollmentTokensCreateFuncInvoked)

			if c.wantNotFound {
				require.Error(t, err)
				var statusErr interface{ Status() int }
				require.ErrorAs(t, err, &statusErr)
				assert.Equal(t, http.StatusNotFound, statusErr.Status())
				assert.False(t, mockDS.GetZeroTouchEnrollmentTokenFuncInvoked)
				assert.False(t, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
				return
			}

			require.NoError(t, err)
			var extras dpcExtras
			require.NoError(t, json.Unmarshal(resp.DPCExtras, &extras))
			wantToken := "new-token-value"
			if c.hasExistingToken {
				wantToken = "existing-token-value"
			}
			assert.Equal(t, wantToken, extras.AdminExtrasBundle.EnrollmentToken)
			assert.Equal(t, !c.hasExistingToken, mockDS.CreateZeroTouchEnrollmentTokenFuncInvoked)
		})
	}
}

func TestGetZeroTouchConfiguration_AutoMDMCheckedAfterLicense(t *testing.T) {
	svc, mockDS, _ := setupEEService(t)

	ctx := licensectx.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierFree})
	_, err := svc.GetZeroTouchConfiguration(ctx, new(uint(1)))
	require.ErrorIs(t, err, automdm.ErrMissingLicense)
	assert.False(t, mockDS.TeamExistsFuncInvoked)
}

func TestGetZeroTouchConfiguration_NonAdminRejectedBeforeAutoMDMLookup(t *testing.T) {
	for _, role := range []string{automdm.RoleMaintainer, automdm.RoleObserver} {
		t.Run(role, func(t *testing.T) {
			svc, mockDS, _ := setupEEService(t)

			ctx := licensectx.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(role)}})

			_, err := svc.GetZeroTouchConfiguration(ctx, new(uint(999)))
			var authzErr *authz.Forbidden
			require.ErrorAs(t, err, &authzErr)
			assert.False(t, mockDS.TeamExistsFuncInvoked, "team existence must not leak to non-admins")
		})
	}
}
