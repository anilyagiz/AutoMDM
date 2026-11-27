package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service/schedule"
	"github.com/anilyagiz/automdm/v4/server/service/schedule/scheduletest"
	"github.com/stretchr/testify/require"
)

func TestTriggerCronScheduleAuth(t *testing.T) {
	ds := new(mock.Store)

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{StartCronSchedules: []TestNewScheduleFunc{
		func(ctx context.Context, ds automdm.Datastore) automdm.NewCronScheduleFunc {
			return func() (automdm.CronSchedule, error) {
				s := schedule.New(
					ctx, "test_sched", "id", 1*time.Second, scheduletest.NopLocker{}, scheduletest.NopStatsStore{},
					schedule.WithJob("test_job", func(ctx context.Context) error {
						return nil
					}),
				)
				return s, nil
			}
		},
	}})

	testCases := []struct {
		name       string
		user       *automdm.User
		shouldFail bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			true,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
		},
		{
			"user",
			&automdm.User{ID: 777},
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			err := svc.TriggerCronSchedule(ctx, "test_sched")
			if tt.shouldFail {
				require.Error(t, err)
				require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCronSchedulesService(t *testing.T) {
	ds := new(mock.Store)
	locker := scheduletest.SetupMockLocker("test_sched", "id", time.Now().Add(-1*time.Hour))
	statsStore := scheduletest.SetUpMockStatsStore("test_sched", automdm.CronStats{
		ID:        1,
		StatsType: automdm.CronStatsTypeScheduled,
		Name:      "test_sched",
		Instance:  "id",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		Status:    automdm.CronStatsStatusCompleted,
	})
	jobsDone := uint32(0)
	startCh := make(chan struct{}, 1)

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{StartCronSchedules: []TestNewScheduleFunc{
		func(ctx context.Context, ds automdm.Datastore) automdm.NewCronScheduleFunc {
			return func() (automdm.CronSchedule, error) {
				s := schedule.New(
					ctx, "test_sched", "id", 3*time.Second, locker, statsStore,
					schedule.WithJob("test_jobb", func(ctx context.Context) error {
						time.Sleep(100 * time.Millisecond)
						atomic.AddUint32(&jobsDone, 1)
						return nil
					}),
				)
				startCh <- struct{}{}
				return s, nil
			}
		},
	}})
	<-startCh
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, svc.TriggerCronSchedule(ctx, "test_sched")) // first trigger sent ok and will run successfully

	time.Sleep(10 * time.Millisecond)
	require.ErrorContains(t, svc.TriggerCronSchedule(ctx, "test_sched"), "conflicts with current status of test_sched") // error because first job is pending

	require.ErrorContains(t, svc.TriggerCronSchedule(ctx, "test_sched"), "conflicts with current status of test_sched") // error because first job is pending

	<-ticker.C
	require.Error(t, svc.TriggerCronSchedule(ctx, "test_sched_2")) // error because unrecognized name

	<-ticker.C
	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, uint32(3), atomic.LoadUint32(&jobsDone)) // 2 regularly scheduled (at 3s and 6s) plus 1 triggered
}
