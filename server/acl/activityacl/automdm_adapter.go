// Package activityacl provides the anti-corruption layer between the activity
// bounded context and legacy AutoMDM code.
//
// This package is the ONLY place that imports both activity types and team types.
// It translates between them, allowing the activity context to remain decoupled
// from legacy code.
package activityacl

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/activity"
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// UsersByIDsLookup is the minimal interface needed to look up user summaries by ID
// without going through the service layer's authz. The activity bounded context
// performs its own authorization on the host before reaching the user-enrichment
// step, so a second authz check (which would reject team-scoped viewers since
// Service.UsersByIDs authorizes against an empty *automdm.User) is both incorrect
// and the cause of issue #46009 for hosts with user-initiated past activities.
type UsersByIDsLookup interface {
	UsersByIDs(ctx context.Context, ids []uint) ([]*automdm.UserSummary, error)
}

// AutoMDMServiceAdapter provides access to AutoMDM service methods
// for data that the activity bounded context doesn't own.
type AutoMDMServiceAdapter struct {
	svc          automdm.ActivityLookupService
	usersByIDsDS UsersByIDsLookup
}

// NewAutoMDMServiceAdapter creates a new adapter for the AutoMDM service.
// usersByIDsDS is a datastore-direct lookup used for activity user enrichment;
// it must bypass service-layer authz because the host authz check has already
// gated access to the activity list.
func NewAutoMDMServiceAdapter(svc automdm.ActivityLookupService, usersByIDsDS UsersByIDsLookup) *AutoMDMServiceAdapter {
	return &AutoMDMServiceAdapter{svc: svc, usersByIDsDS: usersByIDsDS}
}

// Ensure AutoMDMServiceAdapter implements the required interfaces
var (
	_ activity.UserProvider              = (*AutoMDMServiceAdapter)(nil)
	_ activity.HostProvider              = (*AutoMDMServiceAdapter)(nil)
	_ activity.AppConfigProvider         = (*AutoMDMServiceAdapter)(nil)
	_ activity.UpcomingActivityActivator = (*AutoMDMServiceAdapter)(nil)
)

// UsersByIDs fetches users by their IDs from the datastore directly,
// bypassing service-layer authz. The activity service has already authorized
// the caller against the host before this is called for enrichment.
func (a *AutoMDMServiceAdapter) UsersByIDs(ctx context.Context, ids []uint) ([]*activity.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Fetch only the requested users by their IDs
	users, err := a.usersByIDsDS.UsersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Convert to activity.User
	result := make([]*activity.User, 0, len(users))
	for _, u := range users {
		result = append(result, convertUser(u))
	}
	return result, nil
}

// FindUserIDs searches for users by name/email prefix and returns their IDs.
func (a *AutoMDMServiceAdapter) FindUserIDs(ctx context.Context, query string) ([]uint, error) {
	if query == "" {
		return nil, nil
	}

	// Search users via AutoMDM service with the query
	users, err := a.svc.ListUsers(ctx, automdm.UserListOptions{
		ListOptions: automdm.ListOptions{
			MatchQuery: query,
		},
	})
	if err != nil {
		return nil, err
	}

	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	return ids, nil
}

// HostIDByIDOrIdentifier resolves the host ID or identifier of a host endpoint's :id path parameter.
func (a *AutoMDMServiceAdapter) HostIDByIDOrIdentifier(ctx context.Context, identifier string) (uint, error) {
	return a.svc.HostIDByIDOrIdentifier(ctx, identifier)
}

// GetHostLite fetches minimal host information for authorization.
func (a *AutoMDMServiceAdapter) GetHostLite(ctx context.Context, hostID uint) (*activity.Host, error) {
	host, err := a.svc.GetHostLite(ctx, hostID)
	if err != nil {
		return nil, err
	}
	return &activity.Host{
		ID:     host.ID,
		TeamID: host.TeamID,
	}, nil
}

func convertUser(u *automdm.UserSummary) *activity.User {
	return &activity.User{
		ID:       u.ID,
		Name:     u.Name,
		Email:    u.Email,
		Gravatar: u.GravatarURL,
		APIOnly:  u.APIOnly,
	}
}

// GetActivitiesWebhookConfig returns the webhook configuration for activities.
func (a *AutoMDMServiceAdapter) GetActivitiesWebhookConfig(ctx context.Context) (*activity.ActivitiesWebhookSettings, error) {
	settings, err := a.svc.GetActivitiesWebhookSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &activity.ActivitiesWebhookSettings{
		Enable:         settings.Enable,
		DestinationURL: settings.DestinationURL,
	}, nil
}

// GetHostActivitiesWebhooks returns the enabled host-activities webhook destinations of the teams the given hosts belong to.
func (a *AutoMDMServiceAdapter) GetHostActivitiesWebhooks(ctx context.Context, hostIDs []uint) ([]activity.HostActivitiesWebhook, error) {
	settings, err := a.svc.GetHostActivitiesWebhookSettings(ctx, hostIDs)
	if err != nil {
		return nil, err
	}
	hooks := make([]activity.HostActivitiesWebhook, 0, len(settings))
	for _, s := range settings {
		hooks = append(hooks, activity.HostActivitiesWebhook{
			DestinationURL: s.DestinationURL,
			HostIDs:        s.HostIDs,
		})
	}
	return hooks, nil
}

// ActivateNextUpcomingActivity activates the next upcoming activity in the queue.
func (a *AutoMDMServiceAdapter) ActivateNextUpcomingActivity(ctx context.Context, hostID uint, fromCompletedExecID string) error {
	return a.svc.ActivateNextUpcomingActivityForHost(ctx, hostID, fromCompletedExecID)
}
