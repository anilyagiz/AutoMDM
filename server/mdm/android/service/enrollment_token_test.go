package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
	"google.golang.org/api/googleapi"
)

// An unauthenticated caller with an invalid enroll secret must get the same
// error whether or not Android MDM is configured, so the endpoint can't be
// used to probe configuration state.
func TestCreateEnrollmentTokenUniformInvalidSecretError(t *testing.T) {
	androidAPIClient := android_mock.Client{}
	androidAPIClient.InitCommonMocks()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	automdmDS := InitCommonDSMocks()
	automdmDS.Store.VerifyEnrollSecretFunc = func(ctx context.Context, secret string) (*automdm.EnrollSecret, error) {
		return nil, &notFoundError{}
	}
	svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, noopNewActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)

	badSecretErr := func(configured bool, idpSessionID string) error {
		automdmDS.Store.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
			appCfg := &automdm.AppConfig{}
			appCfg.MDM.AndroidEnabledAndConfigured = configured
			return appCfg, nil
		}
		_, err := svc.CreateEnrollmentToken(t.Context(), "bogus-secret", idpSessionID, false)
		require.Error(t, err)
		return err
	}
	errNotConfigured := badSecretErr(false, "")
	errConfigured := badSecretErr(true, "")
	// The service has no idP session store, so this only returns the uniform
	// auth error if the secret is verified before idP session validation.
	errIdPSession := badSecretErr(true, "bogus-session")

	var authFailed *automdm.AuthFailedError
	require.ErrorAs(t, errNotConfigured, &authFailed)
	require.ErrorAs(t, errConfigured, &authFailed)
	require.ErrorAs(t, errIdPSession, &authFailed)
	require.Equal(t, errConfigured.Error(), errNotConfigured.Error())
	require.Equal(t, errConfigured.Error(), errIdPSession.Error())
}

// Enrollment tokens point at the enrolling team's default policy, so its passcode is set during setup.
func TestCreateEnrollmentTokenUsesAutoMDMDefaultPolicy(t *testing.T) {
	const enterpriseName = "enterprises/LC00test"
	teamID := uint(4)

	cases := []struct {
		name           string
		secretTeamID   *uint
		checksums      map[uint][]byte
		patchErr       error
		rejectPasscode bool
		wantPatched    []string
		wantPolicyName string
	}{
		{
			name:           "unassigned",
			wantPatched:    []string{enterpriseName + "/policies/automdm0"},
			wantPolicyName: enterpriseName + "/policies/automdm0",
		},
		{
			name:           "automdm",
			secretTeamID:   &teamID,
			wantPatched:    []string{enterpriseName + "/policies/automdm4"},
			wantPolicyName: enterpriseName + "/policies/automdm4",
		},
		{
			name:           "team policy already sent",
			secretTeamID:   &teamID,
			checksums:      map[uint][]byte{teamID: []byte("checksum")},
			wantPolicyName: enterpriseName + "/policies/automdm4",
		},
		{
			name:           "rejected passcode settings fall back to the base settings in the team policy",
			secretTeamID:   &teamID,
			rejectPasscode: true,
			wantPatched:    []string{enterpriseName + "/policies/automdm4", enterpriseName + "/policies/automdm4"},
			wantPolicyName: enterpriseName + "/policies/automdm4",
		},
		{
			name:           "falls back to the global policy when the patch fails",
			secretTeamID:   &teamID,
			patchErr:       errors.New("quota"),
			wantPatched:    []string{enterpriseName + "/policies/automdm4"},
			wantPolicyName: enterpriseName + "/policies/1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			androidAPIClient := android_mock.Client{}
			androidAPIClient.InitCommonMocks()
			var patched []string
			androidAPIClient.EnterprisesPoliciesPatchFunc = func(_ context.Context, policyName string, policy *androidmanagement.Policy, opts androidmgmt.PoliciesPatchOpts) (*androidmanagement.Policy, error) {
				patched = append(patched, policyName)
				require.True(t, opts.ExcludeApps)
				require.NotNil(t, policy.StatusReportingSettings)
				if c.rejectPasscode && policy.PasswordPolicies != nil {
					return nil, &googleapi.Error{Code: http.StatusBadRequest}
				}
				return policy, c.patchErr
			}
			var tokenPolicyName string
			androidAPIClient.EnterprisesEnrollmentTokensCreateFunc = func(_ context.Context, _ string, token *androidmanagement.EnrollmentToken) (*androidmanagement.EnrollmentToken, error) {
				tokenPolicyName = token.PolicyName
				return &androidmanagement.EnrollmentToken{Value: "token"}, nil
			}

			automdmDS := InitCommonDSMocks()
			automdmDS.Store.VerifyEnrollSecretFunc = func(_ context.Context, _ string) (*automdm.EnrollSecret, error) {
				return &automdm.EnrollSecret{Secret: "secret", TeamID: c.secretTeamID}, nil
			}
			automdmDS.Store.TeamLiteFunc = func(_ context.Context, id uint) (*automdm.TeamLite, error) {
				return &automdm.TeamLite{ID: id}, nil
			}
			automdmDS.Store.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
				appCfg := &automdm.AppConfig{}
				appCfg.MDM.AndroidEnabledAndConfigured = true
				return appCfg, nil
			}
			automdmDS.Store.GetEnterpriseFunc = func(_ context.Context) (*android.Enterprise, error) {
				return &android.Enterprise{EnterpriseID: "LC00test"}, nil
			}
			automdmDS.Store.GetAllMDMConfigAssetsByNameFunc = func(_ context.Context, _ []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
				return map[automdm.MDMAssetName]automdm.MDMConfigAsset{automdm.MDMAssetAndroidAutoMDMServerSecret: {Value: []byte("secret")}}, nil
			}
			automdmDS.Store.GetAndroidAutoMDMDefaultPolicyChecksumsFunc = func(_ context.Context) (map[uint][]byte, error) {
				return c.checksums, nil
			}
			automdmDS.Store.ListAndroidAutoMDMDefaultPolicyProfilesFunc = func(_ context.Context, gotTeamID *uint) ([]*automdm.MDMAndroidConfigProfile, error) {
				require.NotNil(t, gotTeamID)
				return []*automdm.MDMAndroidConfigProfile{{Name: "passcode", TeamID: gotTeamID, RawJSON: []byte(`{"passwordPolicies": [{"passwordMinimumLength": 6}]}`)}}, nil
			}
			var savedTeamIDs []uint
			automdmDS.Store.MarkAndroidAutoMDMDefaultPolicyUnverifiedFunc = func(_ context.Context, gotTeamID uint) error {
				savedTeamIDs = append(savedTeamIDs, gotTeamID)
				return nil
			}
			svc, err := NewServiceWithClient(slog.New(slog.DiscardHandler), automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, noopNewActivity, config.AndroidAgentConfig{})
			require.NoError(t, err)

			_, err = svc.CreateEnrollmentToken(t.Context(), "secret", "", true)
			require.NoError(t, err)
			require.Equal(t, c.wantPolicyName, tokenPolicyName)
			require.Equal(t, c.wantPatched, patched)
			if len(c.wantPatched) > 0 && c.patchErr == nil {
				var wantTeamID uint
				if c.secretTeamID != nil {
					wantTeamID = *c.secretTeamID
				}
				require.Equal(t, []uint{wantTeamID}, savedTeamIDs)
			} else {
				require.Empty(t, savedTeamIDs)
			}
		})
	}
}
