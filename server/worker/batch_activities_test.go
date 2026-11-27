package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestBatchActivitiesRun(t *testing.T) {
	ctx := context.Background()
	ds := new(mock.Store)

	var canceled bool
	var status automdm.ScheduledBatchExecutionStatus = automdm.ScheduledBatchExecutionScheduled

	ds.GetBatchActivityFunc = func(ctx context.Context, executionID string) (*automdm.BatchActivity, error) {
		return &automdm.BatchActivity{
			BatchExecutionID: executionID,
			Status:           status,
			Canceled:         canceled,
		}, nil
	}

	var recvExecID string
	ds.RunScheduledBatchActivityFunc = func(ctx context.Context, executionID string) error {
		recvExecID = executionID
		return nil
	}

	batchWorker := &BatchScripts{
		Datastore: ds,
	}

	err := batchWorker.Run(ctx, json.RawMessage(`{"execution_id": "abc"}`))
	require.NoError(t, err)
	require.Equal(t, "abc", recvExecID)
	require.True(t, ds.GetBatchActivityFuncInvoked)
	require.True(t, ds.RunScheduledBatchActivityFuncInvoked)

	// Job is already started
	ds.GetBatchActivityFuncInvoked = false
	ds.RunScheduledBatchActivityFuncInvoked = false
	status = automdm.ScheduledBatchExecutionStarted

	err = batchWorker.Run(ctx, json.RawMessage(`{"execution_id": "abc"}`))
	require.NoError(t, err)
	require.True(t, ds.GetBatchActivityFuncInvoked)
	require.False(t, ds.RunScheduledBatchActivityFuncInvoked)

	// Job was canceled
	ds.GetBatchActivityFuncInvoked = false
	ds.RunScheduledBatchActivityFuncInvoked = false
	status = automdm.ScheduledBatchExecutionScheduled
	canceled = true

	err = batchWorker.Run(ctx, json.RawMessage(`{"execution_id": "abc"}`))
	require.NoError(t, err)
	require.True(t, ds.GetBatchActivityFuncInvoked)
	require.False(t, ds.RunScheduledBatchActivityFuncInvoked)
}
