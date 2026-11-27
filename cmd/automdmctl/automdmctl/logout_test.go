package automdmctl

import (
	"context"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/assert"
)

func TestLogout(t *testing.T) {
	_, ds := testing_utils.RunServerWithMockedDS(t)

	ds.SessionByIDFunc = func(ctx context.Context, id uint) (*automdm.Session, error) {
		return &automdm.Session{
			ID:         333,
			AccessedAt: time.Now(),
			UserID:     123,
			Key:        "12344321",
		}, nil
	}
	ds.DestroySessionFunc = func(ctx context.Context, session *automdm.Session) error {
		return nil
	}

	assert.Empty(t, runAppForTest(t, []string{"logout"}))
	assert.True(t, ds.DestroySessionFuncInvoked)
}
