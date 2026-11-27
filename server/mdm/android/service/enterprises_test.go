package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	ds_mock "github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
	"google.golang.org/api/googleapi"
)

func TestEnterprisesAuth(t *testing.T) {
	androidAPIClient := android_mock.Client{}
	androidAPIClient.InitCommonMocks()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	automdmDS := InitCommonDSMocks()
	newActivity := noopNewActivity // This test does not verify activity creation.
	svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)

	testCases := []struct {
		name            string
		user            *automdm.User
		shouldFailWrite bool
		shouldFailRead  bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			true,
			true,
		},
		{
			"global gitops",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			true,
			true,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			true,
		},
		{
			"global observer+",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
			true,
			true,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
			true,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
			true,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			true,
		},
		{
			"team observer+",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			true,
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: tt.user})

			_, err := svc.GetEnterprise(ctx)
			checkAuthErr(t, tt.shouldFailRead, err)

			err = svc.DeleteEnterprise(ctx)
			checkAuthErr(t, tt.shouldFailWrite, err)

			_, err = svc.EnterpriseSignup(ctx)
			checkAuthErr(t, tt.shouldFailWrite, err)

			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			_, err = svc.EnterpriseSignupSSE(ctx)
			checkAuthErr(t, tt.shouldFailRead, err)
		})
	}

	t.Run("unauthorized", func(t *testing.T) {
		err := svc.EnterpriseSignupCallback(context.Background(), "signup_token", "token")
		checkAuthErr(t, false, err)
		err = svc.EnterpriseSignupCallback(context.Background(), "bad_token", "token")
		checkAuthErr(t, true, err)
	})
}

func TestEnterpriseSignupMissingPrivateKey(t *testing.T) {
	androidAPIClient := android_mock.Client{}
	androidAPIClient.InitCommonMocks()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	automdmDS := InitCommonDSMocks()
	newActivity := noopNewActivity // This test does not verify activity creation.

	svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)

	user := &automdm.User{ID: 1, GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: user})

	testSetEmptyPrivateKey = true
	t.Cleanup(func() { testSetEmptyPrivateKey = false })

	_, err = svc.EnterpriseSignup(ctx)
	require.Error(t, err)

	require.Contains(t, err.Error(), "missing required private key")
	require.Contains(t, err.Error(), "https://automdm.com/learn-more-about/automdm-server-private-key")
}

func checkAuthErr(t *testing.T, shouldFail bool, err error) {
	t.Helper()
	if shouldFail {
		require.Error(t, err)
		var forbiddenError *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenError)
	} else {
		require.NoError(t, err)
	}
}

func InitCommonDSMocks() *AndroidMockDS {
	ds := AndroidMockDS{}
	// Set up basic Android datastore mocks directly on the AutoMDM datastore mock
	ds.Store.CreateEnterpriseFunc = func(ctx context.Context, _ uint) (uint, error) {
		return 1, nil
	}
	ds.Store.UpdateEnterpriseFunc = func(ctx context.Context, enterprise *android.EnterpriseDetails) error {
		return nil
	}
	ds.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{}, nil
	}
	ds.Store.GetEnterpriseByIDFunc = func(ctx context.Context, ID uint) (*android.EnterpriseDetails, error) {
		return &android.EnterpriseDetails{}, nil
	}
	ds.Store.GetEnterpriseBySignupTokenFunc = func(ctx context.Context, signupToken string) (*android.EnterpriseDetails, error) {
		if signupToken == "signup_token" {
			return &android.EnterpriseDetails{}, nil
		}
		return nil, &notFoundError{}
	}
	ds.Store.DeleteAllEnterprisesFunc = func(ctx context.Context) error {
		return nil
	}
	ds.Store.DeleteOtherEnterprisesFunc = func(ctx context.Context, ID uint) error {
		return nil
	}
	ds.Store.DeleteZeroTouchEnrollmentTokensFunc = func(_ context.Context) error {
		return nil
	}
	ds.DataStore.SetHostAndroidSoftwareInstallsVerifiedFunc = func(ctx context.Context, hostID uint, appliedPolicyVersion int64) error {
		return nil
	}
	ds.DataStore.ReplaceHostAndroidSoftwareInstallsFunc = func(ctx context.Context, hostID uint, installs []automdm.HostAndroidSoftwareInstall) error {
		return nil
	}
	ds.DataStore.NewAndroidPolicyRequestFunc = func(ctx context.Context, req *android.MDMAndroidPolicyRequest) error {
		return nil
	}
	ds.DataStore.AndroidDeviceExistsByDeviceIDFunc = func(ctx context.Context, deviceID string) (bool, error) {
		return false, nil
	}

	ds.Store.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.Store.ListHostMDMAndroidVPPAppsLatestFailedInstallFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostAndroidVPPSoftwareInstall, error) {
		return nil, nil
	}
	ds.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
		return nil
	}
	ds.Store.UserOrDeletedUserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
		return &automdm.User{ID: id}, nil
	}
	ds.Store.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		queryerContext sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		result := make(map[automdm.MDMAssetName]automdm.MDMConfigAsset, len(assetNames))
		for _, name := range assetNames {
			result[name] = automdm.MDMConfigAsset{Value: []byte("value")}
		}
		return result, nil
	}
	ds.Store.InsertOrReplaceMDMConfigAssetFunc = func(ctx context.Context, asset automdm.MDMConfigAsset) error {
		return nil
	}
	ds.Store.DeleteMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) error {
		return nil
	}
	ds.Store.BulkSetAndroidHostsUnenrolledFunc = func(ctx context.Context) error {
		return nil
	}
	ds.Store.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
		return &automdm.Job{}, nil
	}
	ds.Store.MarkAllPendingAndroidVPPInstallsAsFailedFunc = func(ctx context.Context) error {
		return nil
	}
	ds.Store.CreatePendingCertificateTemplatesForNewHostFunc = func(ctx context.Context, hostUUID string, teamID uint) (int64, error) {
		return 0, nil
	}
	ds.Store.GetAndroidDeviceLastTeamIDFunc = func(ctx context.Context, enterpriseSpecificID string) (*uint, bool, error) {
		return nil, false, nil
	}
	ds.Store.UpdateHostOperatingSystemFunc = func(ctx context.Context, hostID uint, hostOS automdm.OperatingSystem) error {
		return nil
	}
	ds.Store.GetMDMIdPAccountByUUIDFunc = func(ctx context.Context, uuid string) (*automdm.MDMIdPAccount, error) {
		return nil, &notFoundError{}
	}
	ds.Store.GetMDMIdPAccountByHostUUIDFunc = func(ctx context.Context, hostUUID string) (*automdm.MDMIdPAccount, error) {
		return nil, &notFoundError{}
	}
	ds.Store.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.Store.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
		return nil
	}
	ds.Store.GetAndroidPubSubDedupStateFunc = func(ctx context.Context, hostID uint) (string, *time.Time, error) {
		return "", nil, nil
	}
	ds.Store.SetAndroidPubSubDedupStateFunc = func(ctx context.Context, hostID uint, messageID string, eventTime *time.Time) error {
		return nil
	}
	ds.Store.SetAndroidHostEnrolledFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.Store.AndroidResetOnReenrollmentFunc = func(ctx context.Context, hostID uint, hostUUID string, preserveHostActivities bool) ([]*automdm.User, []automdm.ActivityDetails, error) {
		return nil, nil, nil
	}
	ds.Store.SetOrUpdateHostMDMAndroidDeviceVitalsFunc = func(ctx context.Context, hostUUID string, vitals automdm.MDMAndroidDeviceVitals) error {
		return nil
	}
	return &ds
}

type AndroidMockDS struct {
	ds_mock.Store
}

type notFoundError struct{}

func (e *notFoundError) Error() string    { return "not found" }
func (e *notFoundError) IsNotFound() bool { return true }

// noopNewActivity is a no-op activity creation function for tests that don't verify activity creation.
func noopNewActivity(_ context.Context, _ *automdm.User, _ automdm.ActivityDetails) error {
	return nil
}

func TestGetEnterprise(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	user := &automdm.User{ID: 1, GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: user})

	t.Run("normal enterprise retrieval works", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		automdmDS := InitCommonDSMocks()
		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		enterprise, err := svc.GetEnterprise(ctx)
		require.NoError(t, err)
		require.NotNil(t, enterprise)
		assert.False(t, androidAPIClient.EnterprisesListFuncInvoked, "Should not call android API in get to avoid hitting quotas")
	})

	t.Run("enterprise not found in database", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return not found
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return nil, &notFoundError{}
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		enterprise, err := svc.GetEnterprise(ctx)
		require.Error(t, err)
		require.Nil(t, enterprise)

		// Should not call Google API if enterprise not found in database
		assert.False(t, androidAPIClient.EnterprisesListFuncInvoked)
		assert.Contains(t, err.Error(), "No enterprise found")
	})
}

func TestVerifyExistingEnterpriseIfAny(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	user := &automdm.User{ID: 1, GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: user})

	t.Run("enterprise actually deleted - enterprise NOT in LIST", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return an enterprise that will be checked
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{
				EnterpriseID: "enterprises/deleted-enterprise-id",
			}, nil
		}

		// Track calls to SetAndroidEnabledAndConfigured
		setAndroidCalled := false
		automdmDS.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
			setAndroidCalled = true
			assert.False(t, configured, "Should turn off Android configuration when enterprise is deleted")
			return nil
		}

		// Override EnterprisesListFunc to return empty list (enterprise deleted)
		androidAPIClient.EnterprisesListFunc = func(_ context.Context, _ string) ([]*androidmanagement.Enterprise, error) {
			return []*androidmanagement.Enterprise{
				{Name: "enterprises/some-other-enterprise"},
			}, nil
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		err = svc.VerifyExistingEnterpriseIfAny(ctx)
		require.Error(t, err)

		// Verify LIST API was called
		assert.True(t, androidAPIClient.EnterprisesListFuncInvoked)

		// Verify SetAndroidEnabledAndConfigured was called with false
		assert.True(t, setAndroidCalled)

		// Check that it's an AutoMDM error with proper message and 404 status
		assert.Contains(t, err.Error(), "Android Enterprise has been deleted")
		if statusErr, ok := err.(interface{ StatusCode() int }); ok {
			assert.Equal(t, http.StatusNotFound, statusErr.StatusCode())
		} else if statusErr, ok := err.(interface{ Status() int }); ok {
			assert.Equal(t, http.StatusNotFound, statusErr.Status())
		}
	})

	t.Run("enterprise exists - enterprise IS in LIST", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return an enterprise that will be checked
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{
				EnterpriseID: "enterprises/existing-enterprise-id",
			}, nil
		}

		// Track calls to SetAndroidEnabledAndConfigured - should NOT be called
		setAndroidCalled := false
		automdmDS.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
			setAndroidCalled = true
			return nil
		}

		// Override EnterprisesListFunc to return the enterprise
		androidAPIClient.EnterprisesListFunc = func(_ context.Context, _ string) ([]*androidmanagement.Enterprise, error) {
			return []*androidmanagement.Enterprise{
				{Name: "enterprises/existing-enterprise-id"},
				{Name: "enterprises/some-other-enterprise"},
			}, nil
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		err = svc.VerifyExistingEnterpriseIfAny(ctx)
		require.NoError(t, err)

		// Verify LIST API was called
		assert.True(t, androidAPIClient.EnterprisesListFuncInvoked)

		// Verify SetAndroidEnabledAndConfigured was NOT called (no deletion detected)
		assert.False(t, setAndroidCalled)
	})

	t.Run("LIST API fails - return original 403 error", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return an enterprise
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{
				EnterpriseID: "enterprises/test-enterprise-id",
			}, nil
		}

		// Track calls to SetAndroidEnabledAndConfigured - should NOT be called
		setAndroidCalled := false
		automdmDS.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
			setAndroidCalled = true
			return nil
		}

		// Override EnterprisesListFunc to fail
		androidAPIClient.EnterprisesListFunc = func(_ context.Context, _ string) ([]*androidmanagement.Enterprise, error) {
			return nil, &googleapi.Error{
				Code:    http.StatusUnauthorized,
				Message: "Invalid credentials for list operation.",
			}
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		err = svc.VerifyExistingEnterpriseIfAny(ctx)
		require.Error(t, err)

		// Verify LIST API was called
		assert.True(t, androidAPIClient.EnterprisesListFuncInvoked)

		// Verify SetAndroidEnabledAndConfigured was NOT called
		assert.False(t, setAndroidCalled)

		// Check that it returns the LIST error
		assert.Contains(t, err.Error(), "verifying enterprise with Google")
		assert.Contains(t, err.Error(), "authentication error")
	})

	t.Run("SetAndroidEnabledAndConfigured fails - still return 404", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return an enterprise that will be checked
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{
				EnterpriseID: "enterprises/deleted-enterprise-id",
			}, nil
		}

		// Override SetAndroidEnabledAndConfigured to fail
		automdmDS.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
			assert.False(t, configured, "Should try to turn off Android configuration")
			return errors.New("database error")
		}

		// Override EnterprisesListFunc to return empty list (enterprise deleted)
		androidAPIClient.EnterprisesListFunc = func(_ context.Context, _ string) ([]*androidmanagement.Enterprise, error) {
			return []*androidmanagement.Enterprise{}, nil
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		err = svc.VerifyExistingEnterpriseIfAny(ctx)
		require.Error(t, err)

		// Should still return 404 for deleted enterprise even if SetAndroidEnabledAndConfigured fails
		assert.Contains(t, err.Error(), "Android Enterprise has been deleted")
		if statusErr, ok := err.(interface{ StatusCode() int }); ok {
			assert.Equal(t, http.StatusNotFound, statusErr.StatusCode())
		} else if statusErr, ok := err.(interface{ Status() int }); ok {
			assert.Equal(t, http.StatusNotFound, statusErr.Status())
		}
	})

	t.Run("enterprise ID parsing - handles different formats", func(t *testing.T) {
		testCases := []struct {
			name                 string
			enterpriseIDInDB     string
			enterpriseNameInList string
			shouldFindInList     bool
		}{
			{
				name:                 "standard format with prefix",
				enterpriseIDInDB:     "enterprises/test-id",
				enterpriseNameInList: "enterprises/test-id",
				shouldFindInList:     true,
			},
			{
				name:                 "ID without prefix in DB",
				enterpriseIDInDB:     "test-id",
				enterpriseNameInList: "enterprises/test-id",
				shouldFindInList:     true,
			},
			{
				name:                 "different enterprise ID",
				enterpriseIDInDB:     "enterprises/test-id",
				enterpriseNameInList: "enterprises/different-id",
				shouldFindInList:     false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				androidAPIClient := android_mock.Client{}
				androidAPIClient.InitCommonMocks()

				automdmDS := InitCommonDSMocks()
				automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
					return &android.Enterprise{
						EnterpriseID: tc.enterpriseIDInDB,
					}, nil
				}

				setAndroidCalled := false
				automdmDS.Store.SetAndroidEnabledAndConfiguredFunc = func(_ context.Context, configured bool) error {
					setAndroidCalled = true
					return nil
				}

				androidAPIClient.EnterprisesListFunc = func(_ context.Context, _ string) ([]*androidmanagement.Enterprise, error) {
					return []*androidmanagement.Enterprise{
						{Name: tc.enterpriseNameInList},
					}, nil
				}

				newActivity := noopNewActivity // This test does not verify activity creation.
				svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
				require.NoError(t, err)

				err = svc.VerifyExistingEnterpriseIfAny(ctx)

				if tc.shouldFindInList {
					// Enterprise found in list
					require.NoError(t, err)
					assert.False(t, setAndroidCalled, "Should not turn off Android when enterprise exists")
				} else {
					// Enterprise not found in list - should be deletion
					require.Error(t, err)
					assert.True(t, setAndroidCalled, "Should turn off Android when enterprise deleted")
					assert.Contains(t, err.Error(), "Android Enterprise has been deleted")
				}
			})
		}
	})

	t.Run("enterprise not found in database", func(t *testing.T) {
		androidAPIClient := android_mock.Client{}
		androidAPIClient.InitCommonMocks()

		// Override GetEnterpriseFunc to return not found
		automdmDS := InitCommonDSMocks()
		automdmDS.Store.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
			return nil, &notFoundError{}
		}

		newActivity := noopNewActivity // This test does not verify activity creation.
		svc, err := NewServiceWithClient(logger, automdmDS, &androidAPIClient, "test-private-key", &automdmDS.DataStore, newActivity, config.AndroidAgentConfig{})
		require.NoError(t, err)

		err = svc.VerifyExistingEnterpriseIfAny(ctx)
		require.Nil(t, err)

		// Should not call Google API if enterprise not found in database
		assert.False(t, androidAPIClient.EnterprisesListFuncInvoked)
	})
}
