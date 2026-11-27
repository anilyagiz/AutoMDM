package service

import (
	"context"
	"fmt"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (mw metricsMiddleware) Login(ctx context.Context, email string, password string, supportsEmailVerification bool) (*automdm.User, *automdm.Session, error) {
	var (
		user    *automdm.User
		session *automdm.Session
		err     error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "Login", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	user, session, err = mw.Service.Login(ctx, email, password, supportsEmailVerification)
	return user, session, err
}

func (mw metricsMiddleware) GetSessionDuration(ctx context.Context) time.Duration {
	return mw.Service.GetSessionDuration(ctx)
}

func (mw metricsMiddleware) Logout(ctx context.Context) error {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "Logout", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	err = mw.Service.Logout(ctx)
	return err
}

func (mw metricsMiddleware) DestroySession(ctx context.Context) error {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "DestroySession", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	err = mw.Service.DestroySession(ctx)
	return err
}

func (mw metricsMiddleware) GetInfoAboutSessionsForUser(ctx context.Context, id uint) ([]*automdm.Session, error) {
	var (
		sessions []*automdm.Session
		err      error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "GetInfoAboutSessionsForUser", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	sessions, err = mw.Service.GetInfoAboutSessionsForUser(ctx, id)
	return sessions, err
}

func (mw metricsMiddleware) DeleteSessionsForUser(ctx context.Context, id uint) error {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "DeleteSessionsForUser", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	err = mw.Service.DeleteSessionsForUser(ctx, id)
	return err
}

func (mw metricsMiddleware) GetInfoAboutSession(ctx context.Context, id uint) (*automdm.Session, error) {
	var (
		session *automdm.Session
		err     error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "GetInfoAboutSession", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	session, err = mw.Service.GetInfoAboutSession(ctx, id)
	return session, err
}

func (mw metricsMiddleware) DeleteSession(ctx context.Context, id uint) error {
	var err error
	defer func(begin time.Time) {
		lvs := []string{"method", "DeleteSession", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	err = mw.Service.DeleteSession(ctx, id)
	return err
}
