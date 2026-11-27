package service

import (
	"context"
	"log/slog"
	"net/http"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/vpp"
	notifications_api "github.com/anilyagiz/automdm/v4/server/notifications/api"
)

func (svc *Service) GetActivitiesWebhookSettings(ctx context.Context) (automdm.ActivitiesWebhookSettings, error) {
	appConfig, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return automdm.ActivitiesWebhookSettings{}, ctxerr.Wrap(ctx, err, "get app config for activities webhook")
	}
	return appConfig.WebhookSettings.ActivitiesWebhook, nil
}

// GetHostActivitiesWebhookSettings returns the enabled host-activities webhook
// settings of the teams the given hosts belong to, deduplicated by team and
// destination URL. Like GetActivitiesWebhookSettings, it reads settings
// without an authz check because it is an internal provider hook for the
// activity bounded context, not an endpoint.
//
// Perf note: this runs for every host-linked activity on Premium, enabled or
// not. DefaultTeamConfig is served from the datastore cache but TeamLite is
// not, so named-automdm hosts cost one lite host read plus one team read per
// activity.
func (svc *Service) GetHostActivitiesWebhookSettings(ctx context.Context, hostIDs []uint) ([]automdm.HostActivitiesWebhookDelivery, error) {
	if !license.IsPremium(ctx) {
		return nil, nil
	}
	settings, err := automdm.ResolveHostActivitiesWebhooks(ctx, svc.ds, hostIDs)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "resolve host activities webhooks")
	}
	return settings, nil
}

func (svc *Service) ActivateNextUpcomingActivityForHost(ctx context.Context, hostID uint, fromCompletedExecID string) error {
	return svc.ds.ActivateNextUpcomingActivityForHost(ctx, hostID, fromCompletedExecID)
}

func (svc *Service) NewActivity(ctx context.Context, user *automdm.User, activity activity_api.ActivityDetails) error {
	var apiUser *activity_api.User
	if user != nil {
		apiUser = &activity_api.User{
			ID:      user.ID,
			Name:    user.Name,
			Email:   user.Email,
			Deleted: user.Deleted,
		}
	}
	return svc.activitySvc.NewActivity(ctx, apiUser, activity)
}

// NewActivities creates one activity per item of activities, with the cost of a few queries for the whole batch.
func (svc *Service) NewActivities(ctx context.Context, user *automdm.User, activities []activity_api.ActivityDetails) error {
	var apiUser *activity_api.User
	if user != nil {
		apiUser = &activity_api.User{
			ID:      user.ID,
			Name:    user.Name,
			Email:   user.Email,
			Deleted: user.Deleted,
		}
	}
	return svc.activitySvc.NewActivities(ctx, apiUser, activities)
}

////////////////////////////////////////////////////////////////////////////////
// List host upcoming activities
////////////////////////////////////////////////////////////////////////////////

type listHostUpcomingActivitiesRequest struct {
	Identifier  string              `url:"id"`
	ListOptions automdm.ListOptions `url:"list_options"`
}

type listHostUpcomingActivitiesResponse struct {
	Meta       *automdm.PaginationMetadata `json:"meta"`
	Activities []*automdm.UpcomingActivity `json:"activities"`
	Count      uint                        `json:"count"`
	Err        error                       `json:"error,omitempty"`
}

func (r listHostUpcomingActivitiesResponse) Error() error { return r.Err }

func listHostUpcomingActivitiesEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listHostUpcomingActivitiesRequest)
	var meta *automdm.PaginationMetadata
	acts, _, err := hostByIDOrIdentifier(ctx, svc, req.Identifier, func(hostID uint) (acts []*automdm.UpcomingActivity, err error) {
		acts, meta, err = svc.ListHostUpcomingActivities(ctx, hostID, req.ListOptions)
		return acts, err
	})
	if err != nil {
		return listHostUpcomingActivitiesResponse{Err: err}, nil
	}

	return listHostUpcomingActivitiesResponse{Meta: meta, Activities: acts, Count: meta.TotalResults}, nil
}

// ListHostUpcomingActivities returns a slice of upcoming activities for the
// specified host.
func (svc *Service) ListHostUpcomingActivities(ctx context.Context, hostID uint, opt automdm.ListOptions) ([]*automdm.UpcomingActivity, *automdm.PaginationMetadata, error) {
	// First ensure the user has access to list hosts, then check the specific
	// host once team_id is loaded.
	if err := svc.authz.Authorize(ctx, &automdm.Host{}, automdm.ActionList); err != nil {
		return nil, nil, err
	}
	host, err := svc.ds.HostLite(ctx, hostID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "get host")
	}
	// Authorize again with team loaded now that we have team_id
	if err := svc.authz.Authorize(ctx, host, automdm.ActionRead); err != nil {
		return nil, nil, err
	}

	// cursor-based pagination is not supported for upcoming activities
	opt.After = ""
	// custom ordering is not supported, always by upcoming queue order
	// (acual order is in the query, not set via ListOptions)
	opt.OrderKey = ""
	opt.OrderDirection = automdm.OrderAscending
	// no matching query support
	opt.MatchQuery = ""
	// always include metadata
	opt.IncludeMetadata = true

	return svc.ds.ListHostUpcomingActivities(ctx, hostID, opt)
}

////////////////////////////////////////////////////////////////////////////////
// Cancel host upcoming activity
////////////////////////////////////////////////////////////////////////////////

type cancelHostUpcomingActivityRequest struct {
	HostID     uint   `url:"id"`
	ActivityID string `url:"activity_id"`
}

type cancelHostUpcomingActivityResponse struct {
	Err error `json:"error,omitempty"`
}

func (r cancelHostUpcomingActivityResponse) Error() error { return r.Err }
func (r cancelHostUpcomingActivityResponse) Status() int  { return http.StatusNoContent }

func cancelHostUpcomingActivityEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*cancelHostUpcomingActivityRequest)
	err := svc.CancelHostUpcomingActivity(ctx, req.HostID, req.ActivityID)
	if err != nil {
		return cancelHostUpcomingActivityResponse{Err: err}, nil
	}
	return cancelHostUpcomingActivityResponse{}, nil
}

func (svc *Service) CancelHostUpcomingActivity(ctx context.Context, hostID uint, executionID string) error {
	// First ensure the user has access to list hosts, then check the specific
	// host once team_id is loaded.
	if err := svc.authz.Authorize(ctx, &automdm.Host{}, automdm.ActionList); err != nil {
		return err
	}
	host, err := svc.ds.HostLite(ctx, hostID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get host")
	}
	// Authorize again with team loaded now that we have team_id
	if err := svc.authz.Authorize(ctx, host, automdm.ActionCancelHostActivity); err != nil {
		return err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return automdm.ErrNoContext
	}

	// prevent cancellation of lock/wipe that are already activated
	actMeta, err := svc.ds.GetHostUpcomingActivityMeta(ctx, hostID, executionID)
	if err != nil {
		return err
	}
	if actMeta.ActivatedAt != nil &&
		(actMeta.WellKnownAction == automdm.WellKnownActionLock || actMeta.WellKnownAction == automdm.WellKnownActionWipe) {
		return &automdm.BadRequestError{
			Message: "Couldn't cancel activity. Lock and wipe can't be canceled if they're about to run to prevent you from losing access to the host.",
		}
	}

	// Capture VPP release info BEFORE ds.CancelHostUpcomingActivity runs. For
	// VPP installs that haven't yet been activated, the reservation info lives
	// in upcoming_activities.payload + vpp_app_upcoming_activities, and the
	// cancel transaction unconditionally deletes the upcoming row — so a
	// post-cancel lookup would return notFound and we'd silently leak the seat.
	// Best-effort: any lookup failure here is logged when the release path
	// runs and doesn't block the cancel response.
	releaseInfo, releaseLookupErr := svc.ds.GetVPPInstallReleaseInfoForCancel(ctx, host.ID, executionID)

	pastAct, err := svc.ds.CancelHostUpcomingActivity(ctx, hostID, executionID)
	if err != nil {
		return err
	}

	// fail the notification too, the notify script is the only thing that reports its outcome
	notificationUUID, notificationErr := svc.notificationsSvc.NotificationUUIDForExecution(ctx, executionID)
	if notificationErr != nil && !automdm.IsNotFound(notificationErr) {
		svc.logger.ErrorContext(ctx, "failed to look up the end user notification a canceled script belongs to",
			"err", notificationErr, "host_id", host.ID, "execution_id", executionID)
	}
	if notificationErr == nil {
		err = svc.notificationsSvc.SetNotificationStatus(ctx, notificationUUID,
			notifications_api.EndUserNotificationFailed,
			new(notifications_api.EndUserNotificationReasonCanceled),
			[]string{notifications_api.EndUserNotificationDispatched})
		if err != nil {
			svc.logger.ErrorContext(ctx, "failed to fail the end user notification whose script was canceled",
				"err", err, "host_id", host.ID, "execution_id", executionID, "notification_uuid", notificationUUID)
		}
		// drop the canceled activity, a notification's script never appears in the feed
		pastAct = nil
	}

	if pastAct != nil {
		// If a VPP install was canceled, release the reserved license seat
		// (if any). Best-effort: failures shouldn't block the cancel response.
		if _, isVPPCancel := pastAct.(automdm.ActivityTypeCanceledInstallAppStoreApp); isVPPCancel {
			if rErr := svc.releaseVPPSeat(ctx, host, releaseInfo, releaseLookupErr); rErr != nil {
				svc.logger.ErrorContext(ctx, "failed to release reserved VPP license on cancel",
					"err", rErr, "host_id", host.ID, "execution_id", executionID)
			}
		}
		if err := svc.NewActivity(ctx, vc.User, pastAct); err != nil {
			return ctxerr.Wrap(ctx, err, "create activity for cancelation")
		}
	}
	return nil
}

// releaseVPPSeat disassociates the VPP license seat reserved by the canceled
// install, when applicable. The release info is captured by the caller before
// ds.CancelHostUpcomingActivity runs (the pre-activation case relies on data
// the cancel transaction deletes). The seat is released only when the canceled
// install was the one that originally reserved it (associated_event_id is
// non-empty) AND no other still-active install for the same (host, adam_id)
// needs the seat. Personal-enrollment hosts are disassociated by clientUserId
// (matching how they were assigned); device-enrolled hosts by serial number.
func (svc *Service) releaseVPPSeat(ctx context.Context, host *automdm.Host, info *automdm.VPPInstallReleaseInfo, lookupErr error) error {
	if lookupErr != nil {
		if automdm.IsNotFound(lookupErr) {
			return nil
		}
		return ctxerr.Wrap(ctx, lookupErr, "get vpp install release info")
	}
	if info == nil || info.AssociatedEventID == "" || info.HasOtherActiveInstall {
		return nil
	}

	if info.VPPTokenID == nil {
		svc.logger.DebugContext(ctx, "skipping vpp seat release, install has no recorded token", "host_id", host.ID, "adam_id", info.AdamID)
		return nil
	}
	tokenDB, err := svc.ds.GetVPPToken(ctx, *info.VPPTokenID)
	if err != nil {
		if automdm.IsNotFound(err) {
			return nil
		}
		return ctxerr.Wrap(ctx, err, "get vpp token for seat release")
	}

	hostMDM, err := svc.ds.GetHostMDM(ctx, host.ID)
	if err != nil && !automdm.IsNotFound(err) {
		return ctxerr.Wrap(ctx, err, "get host mdm for seat release")
	}
	isPersonal := hostMDM != nil && hostMDM.IsPersonalEnrollment

	// PricingParam: STDQ is the standard tier used by AutoMDM's install path.
	// If a customer uses PLUS we'd need to look up the asset here — defer until
	// observed in the wild.
	req := &vpp.DisassociateAssetsRequest{
		Assets: []vpp.Asset{{AdamID: info.AdamID, PricingParam: "STDQ"}},
	}
	if isPersonal {
		managedAppleID, err := svc.ds.GetHostManagedAppleID(ctx, host.ID)
		if err != nil || managedAppleID == "" {
			if err != nil && !automdm.IsNotFound(err) {
				return ctxerr.Wrap(ctx, err, "get managed apple id for seat release")
			}
			return nil
		}
		clientUser, err := svc.ds.GetVPPClientUser(ctx, tokenDB.ID, managedAppleID)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil
			}
			return ctxerr.Wrap(ctx, err, "get vpp client user for seat release")
		}
		req.ClientUserIds = []string{clientUser.ClientUserID}
	} else {
		req.SerialNumbers = []string{host.HardwareSerial}
	}

	if _, err := vpp.DisassociateAssets(tokenDB.Token, req); err != nil {
		return ctxerr.Wrap(ctx, err, "disassociate vpp assets on cancel")
	}
	return nil
}

func cancelActivitiesAndNotificationsForHost(ctx context.Context, ds automdm.Datastore, notificationsSvc automdm.NotificationsWriteService, logger *slog.Logger, hostID uint) error {
	_, err := ds.BatchCancelAllHostUpcomingActivities(ctx, hostID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "cancel upcoming activities")
	}

	err = notificationsSvc.FailNotificationsForHost(ctx, hostID, notifications_api.EndUserNotificationReasonCanceled)
	if err != nil {
		logger.ErrorContext(ctx, "failed to fail end user notifications for host", "host_id", hostID, "err", err)
	}
	return nil
}
