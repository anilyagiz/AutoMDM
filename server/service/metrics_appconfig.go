package service

import (
	"context"
	"fmt"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (mw metricsMiddleware) NewAppConfig(ctx context.Context, p automdm.AppConfig) (*automdm.AppConfig, error) {
	var (
		info *automdm.AppConfig
		err  error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "NewOrgInfo", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	info, err = mw.Service.NewAppConfig(ctx, p)
	return info, err
}

func (mw metricsMiddleware) AppConfigObfuscated(ctx context.Context) (*automdm.AppConfig, error) {
	var (
		info *automdm.AppConfig
		err  error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "OrgInfo", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	info, err = mw.Service.AppConfigObfuscated(ctx)
	return info, err
}

func (mw metricsMiddleware) ModifyAppConfig(ctx context.Context, p []byte, applyOpts automdm.ApplySpecOptions) (*automdm.AppConfig, error) {
	var (
		info *automdm.AppConfig
		err  error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "ModifyOrgInfo", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	info, err = mw.Service.ModifyAppConfig(ctx, p, applyOpts)
	return info, err
}
