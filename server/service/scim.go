package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service/contract"
)

func getScimDetailsEndpoint(ctx context.Context, _ interface{}, svc automdm.Service) (automdm.Errorer, error) {
	details, err := svc.ScimDetails(ctx)
	if err != nil {
		return contract.ScimDetailsResponse{Err: err}, nil
	}
	return contract.ScimDetailsResponse{
		ScimDetails: details,
	}, nil
}

func (svc *Service) ScimDetails(ctx context.Context) (automdm.ScimDetails, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return automdm.ScimDetails{}, automdm.ErrMissingLicense
}
