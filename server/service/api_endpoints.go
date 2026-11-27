package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

//////////////////////////////////////////////////////////////////////////////////
// List API endpoints
//////////////////////////////////////////////////////////////////////////////////

type listAPIEndpointsRequest struct{}

type listAPIEndpointsResponse struct {
	APIEndpoints []automdm.APIEndpoint `json:"api_endpoints"`
	Err          error                 `json:"error,omitempty"`
}

func (r listAPIEndpointsResponse) Error() error { return r.Err }

func listAPIEndpointsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	endpoints, err := svc.ListAPIEndpoints(ctx)
	return listAPIEndpointsResponse{
		APIEndpoints: endpoints,
		Err:          err,
	}, nil
}

func (svc *Service) ListAPIEndpoints(ctx context.Context) ([]automdm.APIEndpoint, error) {
	// skipauth: No authorization check, this is a premium feature only
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}
