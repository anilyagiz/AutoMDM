package service

import (
	"context"
	"fmt"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (mw metricsMiddleware) InviteNewUser(ctx context.Context, payload automdm.InvitePayload) (*automdm.Invite, error) {
	var (
		invite *automdm.Invite
		err    error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "InviteNewUser", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	invite, err = mw.Service.InviteNewUser(ctx, payload)
	return invite, err
}

func (mw metricsMiddleware) DeleteInvite(ctx context.Context, id uint) error {
	var (
		err error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "DeleteInvite", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	err = mw.Service.DeleteInvite(ctx, id)
	return err
}

func (mw metricsMiddleware) ListInvites(ctx context.Context, opt automdm.ListOptions) ([]*automdm.Invite, error) {
	var (
		invites []*automdm.Invite
		err     error
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "Invites", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	invites, err = mw.Service.ListInvites(ctx, opt)
	return invites, err
}

func (mw metricsMiddleware) VerifyInvite(ctx context.Context, token string) (*automdm.Invite, error) {
	var (
		err    error
		invite *automdm.Invite
	)
	defer func(begin time.Time) {
		lvs := []string{"method", "VerifyInvite", "error", fmt.Sprint(err != nil)}
		mw.requestCount.With(lvs...).Add(1)
		mw.requestLatency.With(lvs...).Observe(time.Since(begin).Seconds())
	}(time.Now())
	invite, err = mw.Service.VerifyInvite(ctx, token)
	return invite, err
}
