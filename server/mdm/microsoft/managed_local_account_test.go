package microsoft_mdm

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendManagedLocalAccountRotationRequests(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	setup := func(rows []automdm.HostManagedLocalAccountWindowsRotationInfo) *mock.Store {
		ds := new(mock.Store)
		ds.GetWindowsManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountWindowsRotationInfo, error) {
			return rows, nil
		}
		ds.InitiateWindowsManagedLocalAccountAutoRotationFunc = func(ctx context.Context, hostUUID string) error {
			return nil
		}
		return ds
	}

	t.Run("no rows due is not an error", func(t *testing.T) {
		ds := setup(nil)
		require.NoError(t, SendManagedLocalAccountRotationRequests(t.Context(), ds, logger, nil))
		assert.False(t, ds.InitiateWindowsManagedLocalAccountAutoRotationFuncInvoked)
	})

	t.Run("requests a rotation per row and logs only view-driven ones", func(t *testing.T) {
		ds := setup([]automdm.HostManagedLocalAccountWindowsRotationInfo{
			{HostUUID: "uuid-1", HostID: 1, DisplayName: "WIN-1", InitiatedByTeam: true},
			// A deferred manual request: the EE service already logged this one with the user as actor.
			{HostUUID: "uuid-2", HostID: 2, DisplayName: "WIN-2", InitiatedByTeam: false},
		})

		var asked []string
		ds.InitiateWindowsManagedLocalAccountAutoRotationFunc = func(ctx context.Context, hostUUID string) error {
			asked = append(asked, hostUUID)
			return nil
		}
		var logged []uint
		newActivity := func(ctx context.Context, user *automdm.User, act automdm.ActivityDetails) error {
			rotated, ok := act.(automdm.ActivityTypeRotatedManagedLocalAccountPassword)
			require.True(t, ok)
			assert.True(t, rotated.AutoMDMInitiated, "the cron acts as AutoMDM, not as a user")
			assert.Nil(t, user)
			logged = append(logged, rotated.HostID)
			return nil
		}

		require.NoError(t, SendManagedLocalAccountRotationRequests(t.Context(), ds, logger, newActivity))

		assert.Equal(t, []string{"uuid-1", "uuid-2"}, asked, "both rows are asked to rotate")
		assert.Equal(t, []uint{1}, logged, "only the view-driven row gets an activity")
	})

	t.Run("a row that lost eligibility is skipped, not failed", func(t *testing.T) {
		ds := setup([]automdm.HostManagedLocalAccountWindowsRotationInfo{
			{HostUUID: "uuid-pending", HostID: 1, DisplayName: "WIN-1", InitiatedByTeam: true},
			{HostUUID: "uuid-ok", HostID: 2, DisplayName: "WIN-2", InitiatedByTeam: true},
		})
		ds.InitiateWindowsManagedLocalAccountAutoRotationFunc = func(ctx context.Context, hostUUID string) error {
			if hostUUID == "uuid-pending" {
				// Raced with a manual request between the SELECT and here.
				return automdm.ErrManagedLocalAccountRotationPending
			}
			return nil
		}
		var logged []uint
		newActivity := func(ctx context.Context, user *automdm.User, act automdm.ActivityDetails) error {
			logged = append(logged, act.(automdm.ActivityTypeRotatedManagedLocalAccountPassword).HostID)
			return nil
		}

		require.NoError(t, SendManagedLocalAccountRotationRequests(t.Context(), ds, logger, newActivity),
			"a benign race must not fail the cron iteration")
		assert.Equal(t, []uint{2}, logged, "the skipped row gets no activity")
	})

	t.Run("an unexpected error is reported but does not stop the run", func(t *testing.T) {
		ds := setup([]automdm.HostManagedLocalAccountWindowsRotationInfo{
			{HostUUID: "uuid-broken", HostID: 1, DisplayName: "WIN-1", InitiatedByTeam: true},
			{HostUUID: "uuid-ok", HostID: 2, DisplayName: "WIN-2", InitiatedByTeam: true},
		})
		boom := errors.New("boom")
		ds.InitiateWindowsManagedLocalAccountAutoRotationFunc = func(ctx context.Context, hostUUID string) error {
			if hostUUID == "uuid-broken" {
				return boom
			}
			return nil
		}
		var logged []uint
		newActivity := func(ctx context.Context, user *automdm.User, act automdm.ActivityDetails) error {
			logged = append(logged, act.(automdm.ActivityTypeRotatedManagedLocalAccountPassword).HostID)
			return nil
		}

		err := SendManagedLocalAccountRotationRequests(t.Context(), ds, logger, newActivity)
		require.Error(t, err)
		require.ErrorIs(t, err, boom)
		assert.Equal(t, []uint{2}, logged, "the healthy row is still processed")
	})

	t.Run("a failing activity does not fail the rotation", func(t *testing.T) {
		ds := setup([]automdm.HostManagedLocalAccountWindowsRotationInfo{
			{HostUUID: "uuid-1", HostID: 1, DisplayName: "WIN-1", InitiatedByTeam: true},
		})
		newActivity := func(ctx context.Context, user *automdm.User, act automdm.ActivityDetails) error {
			return errors.New("activity store down")
		}

		require.NoError(t, SendManagedLocalAccountRotationRequests(t.Context(), ds, logger, newActivity))
		assert.True(t, ds.InitiateWindowsManagedLocalAccountAutoRotationFuncInvoked)
	})
}
