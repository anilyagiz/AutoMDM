package service

import (
	"context"
	"errors"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

// TriggerCronSchedule attempts to trigger an ad-hoc run of the named cron schedule.
func (svc *Service) TriggerCronSchedule(ctx context.Context, name string) error {
	if err := svc.authz.Authorize(ctx, &automdm.CronSchedules{}, automdm.ActionWrite); err != nil {
		return err
	}
	return svc.cronSchedulesService.TriggerCronSchedule(ctx, name)
}

// TriggerPolicyMembershipCleanup starts the cleanup of the policy results that a policy change invalidated. The change
// already requested the cleanup, which the schedule's periodic run does if this fails. A run already in progress picks
// up the change itself, so that conflict isn't an error.
func (svc *Service) TriggerPolicyMembershipCleanup(ctx context.Context) {
	err := svc.cronSchedulesService.TriggerCronSchedule(ctx, string(automdm.CronPolicyMembershipCleanup))
	var conflict interface{ IsConflict() bool }
	if err == nil || errors.As(err, &conflict) {
		return
	}
	err = ctxerr.Wrap(ctx, err, "trigger policy membership cleanup")
	svc.logger.ErrorContext(ctx, "trigger policy membership cleanup", "err", err)
	ctxerr.Handle(ctx, err)
}
