package service

import (
	"context"

	apiendpoints "github.com/anilyagiz/automdm/v4/server/api_endpoints"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

func (svc *Service) ListAPIEndpoints(ctx context.Context) ([]automdm.APIEndpoint, error) {
	if err := svc.authz.Authorize(ctx, &automdm.APIEndpoint{}, automdm.ActionRead); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "authorize list API endpoints")
	}
	return apiendpoints.GetAPIEndpoints(), nil
}
