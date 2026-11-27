package mysqlredis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/mock"
	redigo "github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/require"
)

func TestEnforceHostLimit(t *testing.T) {
	const hostLimit = 3

	oldBatchSize := redisSetMembersBatchSize
	redisSetMembersBatchSize = 2
	defer func() { redisSetMembersBatchSize = oldBatchSize }()

	runTest := func(t *testing.T, pool automdm.RedisPool) {
		var hostIDSeq uint
		var expiredHostDetails []automdm.DeletedHostDetails
		var incomingHostsIDs []uint

		ctx := context.Background()
		ds := new(mock.Store)
		ds.EnrollOsqueryFunc = func(_ context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
			config := &automdm.DatastoreEnrollOsqueryConfig{}
			for _, opt := range opts {
				opt(config)
			}
			hostIDSeq++
			return &automdm.Host{
				ID: hostIDSeq, OsqueryHostID: &config.OsqueryHostID, NodeKey: &config.NodeKey,
			}, nil
		}
		ds.NewHostFunc = func(ctx context.Context, host *automdm.Host) (*automdm.Host, error) {
			hostIDSeq++
			host.ID = hostIDSeq
			return host, nil
		}
		ds.DeleteHostFunc = func(ctx context.Context, hid uint) error {
			return nil
		}
		ds.DeleteHostsFunc = func(ctx context.Context, ids []uint) error {
			return nil
		}
		ds.CleanupExpiredHostsBatchFunc = func(ctx context.Context, batchSize int) ([]automdm.DeletedHostDetails, error) {
			return expiredHostDetails, nil
		}
		ds.CleanupIncomingHostsFunc = func(ctx context.Context, now time.Time) ([]uint, error) {
			return incomingHostsIDs, nil
		}

		wrappedDS := New(ds, pool, WithEnforcedHostLimit(hostLimit))

		requireInvokedAndReset := func(flag *bool) {
			require.True(t, *flag)
			*flag = false
		}
		requireCanEnroll := func(ok bool) {
			canEnroll, err := wrappedDS.CanEnrollNewHost(ctx)
			require.NoError(t, err)
			require.Equal(t, ok, canEnroll)
		}

		// create a few hosts within the limit
		h1, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)
		require.NotNil(t, h1)
		requireInvokedAndReset(&ds.NewHostFuncInvoked)
		requireCanEnroll(true)
		h2, err := wrappedDS.EnrollOsquery(ctx,
			automdm.WithEnrollOsqueryHostID("osquery-2"),
			automdm.WithEnrollOsqueryNodeKey("node-2"),
			automdm.WithEnrollOsqueryCooldown(time.Second),
		)
		require.NoError(t, err)
		require.NotNil(t, h2)
		requireInvokedAndReset(&ds.EnrollOsqueryFuncInvoked)
		requireCanEnroll(true)
		h3, err := wrappedDS.EnrollOsquery(ctx,
			automdm.WithEnrollOsqueryHostID("osquery-3"),
			automdm.WithEnrollOsqueryNodeKey("node-3"),
			automdm.WithEnrollOsqueryCooldown(time.Second),
		)
		require.NoError(t, err)
		require.NotNil(t, h3)
		requireInvokedAndReset(&ds.EnrollOsqueryFuncInvoked)
		requireCanEnroll(false)

		// deleting h1 allows h4 to be created
		err = wrappedDS.DeleteHost(ctx, h1.ID)
		require.NoError(t, err)
		requireCanEnroll(true)
		h4, err := wrappedDS.EnrollOsquery(ctx,
			automdm.WithEnrollOsqueryHostID("osquery-4"),
			automdm.WithEnrollOsqueryNodeKey("node-4"),
			automdm.WithEnrollOsqueryCooldown(time.Second),
		)
		require.NoError(t, err)
		require.NotNil(t, h4)
		requireInvokedAndReset(&ds.EnrollOsqueryFuncInvoked)
		requireCanEnroll(false)

		// delete h1-h2-h3 (even if h1 is already deleted) should allow 2 more
		err = wrappedDS.DeleteHosts(ctx, []uint{h1.ID, h2.ID, h3.ID})
		require.NoError(t, err)
		requireCanEnroll(true)
		h5, err := wrappedDS.EnrollOsquery(ctx,
			automdm.WithEnrollOsqueryHostID("osquery-5"),
			automdm.WithEnrollOsqueryNodeKey("node-5"),
			automdm.WithEnrollOsqueryCooldown(time.Second),
		)
		require.NoError(t, err)
		require.NotNil(t, h5)
		requireInvokedAndReset(&ds.EnrollOsqueryFuncInvoked)
		requireCanEnroll(true)
		h6, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)
		require.NotNil(t, h6)
		requireInvokedAndReset(&ds.NewHostFuncInvoked)
		requireCanEnroll(false)

		// cleanup expired removes h4
		expiredHostDetails = []automdm.DeletedHostDetails{{ID: h4.ID}}
		_, err = wrappedDS.CleanupExpiredHostsBatch(ctx, 100)
		require.NoError(t, err)
		requireCanEnroll(true)
		// cleanup incoming removes h4, h5
		incomingHostsIDs = []uint{h4.ID, h5.ID}
		_, err = wrappedDS.CleanupIncomingHosts(ctx, time.Now())
		require.NoError(t, err)
		requireCanEnroll(true)

		// can now create 2 more
		h7, err := wrappedDS.EnrollOsquery(ctx,
			automdm.WithEnrollOsqueryHostID("osquery-7"),
			automdm.WithEnrollOsqueryNodeKey("node-7"),
			automdm.WithEnrollOsqueryCooldown(time.Second),
		)
		require.NoError(t, err)
		require.NotNil(t, h7)
		requireInvokedAndReset(&ds.EnrollOsqueryFuncInvoked)
		requireCanEnroll(true)
		h8, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)
		require.NotNil(t, h8)
		requireInvokedAndReset(&ds.NewHostFuncInvoked)
		requireCanEnroll(false)
	}

	t.Run("standalone", func(t *testing.T) {
		pool := redistest.SetupRedis(t, enrolledHostsSetKey, false, false, false)
		runTest(t, pool)
	})

	t.Run("cluster", func(t *testing.T) {
		pool := redistest.SetupRedis(t, enrolledHostsSetKey, true, true, false)
		runTest(t, pool)
	})
}

func TestSyncEnrolledHostIDs(t *testing.T) {
	runTest := func(t *testing.T, pool automdm.RedisPool) {
		var hostIDSeq uint
		var enrolledHostCount int
		var enrolledHostIDs []uint
		ctx := context.Background()

		ds := new(mock.Store)
		ds.NewHostFunc = func(ctx context.Context, host *automdm.Host) (*automdm.Host, error) {
			hostIDSeq++
			host.ID = hostIDSeq
			return host, nil
		}
		ds.CountAllHostsFunc = func(ctx context.Context) (int, error) {
			return enrolledHostCount, nil
		}
		ds.EnrolledHostIDsFunc = func(ctx context.Context) ([]uint, error) {
			return enrolledHostIDs, nil
		}

		requireInvokedAndReset := func(flag *bool) {
			require.True(t, *flag)
			*flag = false
		}

		wrappedDS := New(ds, pool, WithEnforcedHostLimit(10)) // limit is irrelevant for this test

		// create a few hosts kept in sync
		h1, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)
		h2, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)
		h3, err := wrappedDS.NewHost(ctx, &automdm.Host{})
		require.NoError(t, err)

		conn := pool.Get()
		defer conn.Close()

		redisIDs, err := redigo.Strings(conn.Do("SMEMBERS", enrolledHostsSetKey))
		require.NoError(t, err)
		require.ElementsMatch(t, []string{fmt.Sprint(h1.ID), fmt.Sprint(h2.ID), fmt.Sprint(h3.ID)}, redisIDs)

		// syncing with the correct count does not trigger a sync
		enrolledHostCount = 3
		err = wrappedDS.SyncEnrolledHostIDs(ctx)
		require.NoError(t, err)
		requireInvokedAndReset(&ds.CountAllHostsFuncInvoked)
		require.False(t, ds.EnrolledHostIDsFuncInvoked)

		// syncing with a non-matching count triggers a sync
		enrolledHostCount = 2
		enrolledHostIDs = []uint{h1.ID, h3.ID} // will set the redis key to those values
		err = wrappedDS.SyncEnrolledHostIDs(ctx)
		require.NoError(t, err)
		requireInvokedAndReset(&ds.CountAllHostsFuncInvoked)
		requireInvokedAndReset(&ds.EnrolledHostIDsFuncInvoked)

		redisIDs, err = redigo.Strings(conn.Do("SMEMBERS", enrolledHostsSetKey))
		require.NoError(t, err)
		require.ElementsMatch(t, []string{fmt.Sprint(h1.ID), fmt.Sprint(h3.ID)}, redisIDs)

		// syncing when enforcing the limit is disabled removes the set key
		wrappedDS = New(ds, pool) // no limit enforced
		err = wrappedDS.SyncEnrolledHostIDs(ctx)
		require.NoError(t, err)
		exists, err := redigo.Bool(conn.Do("EXISTS", enrolledHostsSetKey))
		require.NoError(t, err)
		require.False(t, exists)
	}

	t.Run("standalone", func(t *testing.T) {
		pool := redistest.SetupRedis(t, enrolledHostsSetKey, false, false, false)
		runTest(t, pool)
	})

	t.Run("cluster", func(t *testing.T) {
		pool := redistest.SetupRedis(t, enrolledHostsSetKey, true, true, false)
		runTest(t, pool)
	})
}
