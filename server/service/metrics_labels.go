package service

import (
	"context"
	"fmt"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (mw metricsMiddleware) ModifyLabel(ctx context.Context, id uint, p automdm.ModifyLabelPayload) (*automdm.LabelWithTeamName, []uint, error) {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "ModifyLabel", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	return mw.Service.ModifyLabel(ctx, id, p)
}
