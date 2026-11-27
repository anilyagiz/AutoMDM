package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type listHostAISessionsRequest struct {
	Identifier  string              `url:"id"`
	ListOptions automdm.ListOptions `url:"list_options"`
	// OrderDirection is order_direction as sent, since the direction defaults to descending and ListOptions can't
	// tell an unset direction from an explicit "asc".
	OrderDirection string `query:"order_direction,optional"`
	Agent          string `query:"agent,optional"`
	Category       string `query:"category,optional"`
}

type listHostAISessionsResponse struct {
	AISessions []*automdm.HostAISession    `json:"ai_sessions"`
	Count      uint                        `json:"count"`
	Meta       *automdm.PaginationMetadata `json:"meta,omitempty"`
	Err        error                       `json:"error,omitempty"`
}

func (r listHostAISessionsResponse) Error() error { return r.Err }

func listHostAISessionsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listHostAISessionsRequest)
	opts := req.ListOptions
	if req.OrderDirection == "" {
		opts.OrderDirection = automdm.OrderDescending
	}
	var meta *automdm.PaginationMetadata
	sessions, _, err := hostByIDOrIdentifier(ctx, svc, req.Identifier, func(hostID uint) (sessions []*automdm.HostAISession, err error) {
		sessions, meta, err = svc.ListHostAISessions(ctx, hostID, automdm.HostAISessionListOptions{
			ListOptions: opts,
			Agent:       req.Agent,
			Category:    automdm.AISessionCategory(req.Category),
		})
		return sessions, err
	})
	if err != nil {
		return listHostAISessionsResponse{Err: err}, nil
	}
	resp := listHostAISessionsResponse{AISessions: sessions, Meta: meta}
	if meta != nil {
		resp.Count = meta.TotalResults
	}
	return resp, nil
}

func (svc *Service) ListHostAISessions(ctx context.Context, hostID uint, opts automdm.HostAISessionListOptions) ([]*automdm.HostAISession, *automdm.PaginationMetadata, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, nil, automdm.ErrMissingLicense
}

type getHostAISessionRequest struct {
	Identifier  string `url:"id"`
	AISessionID uint   `url:"ai_session_id"`
}

type getHostAISessionResponse struct {
	AISession *automdm.HostAISession `json:"ai_session"`
	Err       error                  `json:"error,omitempty"`
}

func (r getHostAISessionResponse) Error() error { return r.Err }

func getHostAISessionEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getHostAISessionRequest)
	session, _, err := hostByIDOrIdentifier(ctx, svc, req.Identifier, func(hostID uint) (*automdm.HostAISession, error) {
		return svc.GetHostAISession(ctx, hostID, req.AISessionID)
	})
	if err != nil {
		return getHostAISessionResponse{Err: err}, nil
	}
	return getHostAISessionResponse{AISession: session}, nil
}

func (svc *Service) GetHostAISession(ctx context.Context, hostID, aiSessionID uint) (*automdm.HostAISession, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

type listHostAISessionEventsRequest struct {
	Identifier  string              `url:"id"`
	AISessionID uint                `url:"ai_session_id"`
	ListOptions automdm.ListOptions `url:"list_options"`
	Category    string              `query:"category,optional"`
}

type listHostAISessionEventsResponse struct {
	Events []*automdm.HostAISessionEvent `json:"events"`
	Count  uint                          `json:"count"`
	Meta   *automdm.PaginationMetadata   `json:"meta,omitempty"`
	Err    error                         `json:"error,omitempty"`
}

func (r listHostAISessionEventsResponse) Error() error { return r.Err }

func listHostAISessionEventsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listHostAISessionEventsRequest)
	var meta *automdm.PaginationMetadata
	events, _, err := hostByIDOrIdentifier(ctx, svc, req.Identifier, func(hostID uint) (events []*automdm.HostAISessionEvent, err error) {
		events, meta, err = svc.ListHostAISessionEvents(ctx, hostID, req.AISessionID, automdm.HostAISessionEventListOptions{
			ListOptions: req.ListOptions,
			Category:    automdm.AISessionCategory(req.Category),
		})
		return events, err
	})
	if err != nil {
		return listHostAISessionEventsResponse{Err: err}, nil
	}
	resp := listHostAISessionEventsResponse{Events: events, Meta: meta}
	if meta != nil {
		resp.Count = meta.TotalResults
	}
	return resp, nil
}

func (svc *Service) ListHostAISessionEvents(ctx context.Context, hostID, aiSessionID uint, opts automdm.HostAISessionEventListOptions) ([]*automdm.HostAISessionEvent, *automdm.PaginationMetadata, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, nil, automdm.ErrMissingLicense
}
