package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReapStuckMDMInstalls(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	const (
		hostID      = uint(7)
		hostUUID    = "host-uuid-7"
		commandUUID = "install-cmd-uuid"
	)

	setupMockDS := func() *mock.DataStore {
		ds := new(mock.DataStore)
		ds.MaybeUpdateSetupExperienceVPPStatusFunc = func(_ context.Context, _, _ string, _ automdm.SetupExperienceStatusResultStatus) (bool, error) {
			return false, nil
		}
		return ds
	}

	appStoreReaped := func() []automdm.ReapedMDMInstall {
		return []automdm.ReapedMDMInstall{{
			HostID:           hostID,
			HostUUID:         hostUUID,
			CommandUUID:      commandUUID,
			User:             &automdm.User{ID: 3},
			AppStoreActivity: &automdm.ActivityInstalledAppStoreApp{HostID: hostID, CommandUUID: commandUUID},
		}}
	}

	t.Run("emits the failed install activity", func(t *testing.T) {
		ds := setupMockDS()
		ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, olderThan time.Duration, maxHosts int) ([]automdm.ReapedMDMInstall, error) {
			assert.Equal(t, 24*time.Hour, olderThan)
			assert.Equal(t, 500, maxHosts)
			return appStoreReaped(), nil
		}

		var gotUser *automdm.User
		var gotActivity automdm.ActivityDetails
		newActivityFn := func(_ context.Context, user *automdm.User, act automdm.ActivityDetails) error {
			gotUser, gotActivity = user, act
			return nil
		}

		require.NoError(t, ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, 24*time.Hour, 500))
		require.NotNil(t, gotActivity)
		require.NotNil(t, gotUser)
		assert.Equal(t, uint(3), gotUser.ID)
		act, ok := gotActivity.(*automdm.ActivityInstalledAppStoreApp)
		require.True(t, ok)
		assert.Equal(t, commandUUID, act.CommandUUID)
		assert.False(t, act.FromSetupExperience)
	})

	t.Run("fails the setup experience step and flags the activity", func(t *testing.T) {
		ds := setupMockDS()
		ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, _ time.Duration, _ int) ([]automdm.ReapedMDMInstall, error) {
			return appStoreReaped(), nil
		}
		// Without this the step stays running for good and, on macOS, the rest of the setup
		// experience is never cancelled.
		var gotHostUUID, gotCmdUUID string
		var gotStatus automdm.SetupExperienceStatusResultStatus
		ds.MaybeUpdateSetupExperienceVPPStatusFunc = func(_ context.Context, hUUID, cmdUUID string,
			status automdm.SetupExperienceStatusResultStatus,
		) (bool, error) {
			gotHostUUID, gotCmdUUID, gotStatus = hUUID, cmdUUID, status
			return true, nil
		}
		mockHostBySetupExperienceUUID(ds, func(_ context.Context, _ string) (*automdm.Host, error) {
			return &automdm.Host{ID: hostID, UUID: hostUUID, Platform: "ios"}, nil
		})
		ds.ListSetupExperienceResultsByHostUUIDFunc = func(_ context.Context, _ string, _ uint) ([]*automdm.SetupExperienceStatusResult, error) {
			return nil, nil
		}

		var gotActivity automdm.ActivityDetails
		newActivityFn := func(_ context.Context, _ *automdm.User, act automdm.ActivityDetails) error {
			gotActivity = act
			return nil
		}

		require.NoError(t, ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, 24*time.Hour, 500))
		assert.Equal(t, hostUUID, gotHostUUID)
		assert.Equal(t, commandUUID, gotCmdUUID)
		assert.Equal(t, automdm.SetupExperienceStatusFailure, gotStatus)

		act, ok := gotActivity.(*automdm.ActivityInstalledAppStoreApp)
		require.True(t, ok)
		assert.True(t, act.FromSetupExperience)
	})

	t.Run("in-house installs fail the setup experience step and flag the activity", func(t *testing.T) {
		ds := setupMockDS()
		ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, _ time.Duration, _ int) ([]automdm.ReapedMDMInstall, error) {
			return []automdm.ReapedMDMInstall{{
				HostID:          hostID,
				HostUUID:        hostUUID,
				CommandUUID:     commandUUID,
				InHouseActivity: &automdm.ActivityTypeInstalledSoftware{HostID: hostID, CommandUUID: commandUUID},
			}}, nil
		}
		// In-house apps install during iOS/iPadOS setup experience; a reaped one must fail its
		// step or the row stays running for good (the queue row is gone, so no command result
		// will ever flip it).
		var gotHostUUID, gotCmdUUID string
		var gotStatus automdm.SetupExperienceStatusResultStatus
		ds.MaybeUpdateSetupExperienceVPPStatusFunc = func(_ context.Context, hUUID, cmdUUID string,
			status automdm.SetupExperienceStatusResultStatus,
		) (bool, error) {
			gotHostUUID, gotCmdUUID, gotStatus = hUUID, cmdUUID, status
			return true, nil
		}
		mockHostBySetupExperienceUUID(ds, func(_ context.Context, _ string) (*automdm.Host, error) {
			return &automdm.Host{ID: hostID, UUID: hostUUID, Platform: "ios"}, nil
		})
		ds.ListSetupExperienceResultsByHostUUIDFunc = func(_ context.Context, _ string, _ uint) ([]*automdm.SetupExperienceStatusResult, error) {
			return nil, nil
		}

		var gotActivity automdm.ActivityDetails
		newActivityFn := func(_ context.Context, _ *automdm.User, act automdm.ActivityDetails) error {
			gotActivity = act
			return nil
		}

		require.NoError(t, ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, 24*time.Hour, 500))
		assert.Equal(t, hostUUID, gotHostUUID)
		assert.Equal(t, commandUUID, gotCmdUUID)
		assert.Equal(t, automdm.SetupExperienceStatusFailure, gotStatus)

		act, ok := gotActivity.(*automdm.ActivityTypeInstalledSoftware)
		require.True(t, ok)
		assert.True(t, act.FromSetupExperience)
	})

	t.Run("records the installs a partially failed run did reap", func(t *testing.T) {
		ds := setupMockDS()
		// A host that errors must not cost the hosts that succeeded their activities: the
		// datastore has already failed those installs.
		ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, _ time.Duration, _ int) ([]automdm.ReapedMDMInstall, error) {
			return appStoreReaped(), errors.New("one host blew up")
		}

		var activities int
		newActivityFn := func(_ context.Context, _ *automdm.User, _ automdm.ActivityDetails) error {
			activities++
			return nil
		}

		err := ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, 24*time.Hour, 500)
		require.Error(t, err)
		require.ErrorContains(t, err, "one host blew up")
		assert.Equal(t, 1, activities)
	})

	t.Run("a non-positive timeout reaps nothing at all", func(t *testing.T) {
		// Reaching the query with 0 would make every activated install on the team older than
		// the threshold, so this must not call the datastore at all.
		for _, olderThan := range []time.Duration{0, -1 * time.Hour} {
			ds := setupMockDS()
			ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, _ time.Duration, _ int) ([]automdm.ReapedMDMInstall, error) {
				t.Fatalf("the datastore must not be reached with olderThan=%s", olderThan)
				return nil, nil
			}
			newActivityFn := func(_ context.Context, _ *automdm.User, _ automdm.ActivityDetails) error {
				t.Fatal("no activity should be emitted")
				return nil
			}
			require.NoError(t, ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, olderThan, 500))
			require.False(t, ds.ReapStuckActivatedMDMInstallsFuncInvoked)
		}
	})

	t.Run("nothing to reap does nothing", func(t *testing.T) {
		ds := setupMockDS()
		ds.ReapStuckActivatedMDMInstallsFunc = func(_ context.Context, _ time.Duration, _ int) ([]automdm.ReapedMDMInstall, error) {
			return nil, nil
		}
		newActivityFn := func(_ context.Context, _ *automdm.User, _ automdm.ActivityDetails) error {
			t.Fatal("no activity should be emitted")
			return nil
		}
		require.NoError(t, ReapStuckMDMInstalls(ctx, ds, logger, newActivityFn, 24*time.Hour, 500))
	})
}
