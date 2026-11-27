package microsoft_mdm

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/hashicorp/go-multierror"
)

// SendManagedLocalAccountRotationRequests is the Windows counterpart of apple_mdm.SendManagedLocalAccountRotationCommands.
// There is no command to enqueue: recording the request is the whole of the work, and the host's next orbit config
// check-in asks it to re-provision the account. Benign races (eligibility changed since the SELECT, host unenrolled)
// are debug-logged and skipped.
func SendManagedLocalAccountRotationRequests(
	ctx context.Context,
	ds automdm.Datastore,
	logger *slog.Logger,
	newActivityFn automdm.NewActivityFunc,
) error {
	hosts, err := ds.GetWindowsManagedLocalAccountsForAutoRotation(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get windows managed local accounts for auto rotation")
	}
	if len(hosts) == 0 {
		logger.DebugContext(ctx, "no windows managed local accounts due for rotation")
		return nil
	}
	logger.InfoContext(ctx, "requesting windows managed local account password rotations", "count", len(hosts))

	var result *multierror.Error
	for _, host := range hosts {
		if err := ds.InitiateWindowsManagedLocalAccountAutoRotation(ctx, host.HostUUID); err != nil {
			if automdm.IsNotFound(err) ||
				errors.Is(err, automdm.ErrManagedLocalAccountRotationPending) ||
				errors.Is(err, automdm.ErrManagedLocalAccountNotEligible) {
				logger.DebugContext(ctx, "windows managed local account no longer eligible for rotation",
					"host_uuid", host.HostUUID, "err", err)
				continue
			}
			logger.ErrorContext(ctx, "windows managed local account rotation",
				"host_uuid", host.HostUUID, "err", err)
			result = multierror.Append(result, err)
			continue
		}

		logManagedLocalAccountRotationActivity(ctx, logger, newActivityFn, host)
		logger.DebugContext(ctx, "requested windows managed local account rotation", "host_uuid", host.HostUUID)
	}

	return result.ErrorOrNil()
}

// logManagedLocalAccountRotationActivity logs only view-driven rows (initiated_by_team=1); a manual request was
// already logged with the user as actor.
func logManagedLocalAccountRotationActivity(
	ctx context.Context,
	logger *slog.Logger,
	newActivityFn automdm.NewActivityFunc,
	host automdm.HostManagedLocalAccountWindowsRotationInfo,
) {
	if !host.InitiatedByTeam || newActivityFn == nil {
		return
	}
	if err := newActivityFn(ctx, nil, automdm.ActivityTypeRotatedManagedLocalAccountPassword{
		HostID:           host.HostID,
		HostDisplayName:  host.DisplayName,
		AutoMDMInitiated: true,
	}); err != nil {
		logger.WarnContext(ctx, "windows managed local account rotation: failed to create activity",
			"host_uuid", host.HostUUID, "err", err)
	}
}
