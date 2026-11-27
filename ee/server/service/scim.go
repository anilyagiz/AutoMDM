package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

func (svc *Service) ScimDetails(ctx context.Context) (automdm.ScimDetails, error) {
	err := svc.authz.Authorize(ctx, &automdm.ScimUser{}, automdm.ActionRead)
	if err != nil {
		return automdm.ScimDetails{}, err
	}

	request, err := svc.ds.ScimLastRequest(ctx)
	if err != nil {
		return automdm.ScimDetails{}, ctxerr.Wrap(ctx, err, "scim details")
	}
	return automdm.ScimDetails{
		LastRequest: request,
	}, nil
}
