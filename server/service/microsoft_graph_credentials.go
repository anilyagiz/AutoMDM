package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

/////////////////////////////////////////////////////////////////////////////////
// GET /microsoft_graph_credentials
/////////////////////////////////////////////////////////////////////////////////

type listMicrosoftGraphCredentialsResponse struct {
	MicrosoftGraphCredentials []*automdm.MicrosoftGraphCredentialMetadata `json:"microsoft_graph_credentials"`
	Err                       error                                       `json:"error,omitempty"`
}

func (r listMicrosoftGraphCredentialsResponse) Error() error { return r.Err }

func listMicrosoftGraphCredentialsEndpoint(ctx context.Context, _ any, svc automdm.Service) (automdm.Errorer, error) {
	creds, err := svc.ListMicrosoftGraphCredentials(ctx)
	if err != nil {
		return listMicrosoftGraphCredentialsResponse{Err: err}, nil
	}
	return listMicrosoftGraphCredentialsResponse{MicrosoftGraphCredentials: creds}, nil
}

func (svc *Service) ListMicrosoftGraphCredentials(ctx context.Context) ([]*automdm.MicrosoftGraphCredentialMetadata, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}

/////////////////////////////////////////////////////////////////////////////////
// PUT /microsoft_graph_credentials
/////////////////////////////////////////////////////////////////////////////////

type applyMicrosoftGraphCredentialsRequest struct {
	MicrosoftGraphCredentials []automdm.MicrosoftGraphCredential `json:"microsoft_graph_credentials"`
	DryRun                    bool                               `json:"dry_run"`
}

type applyMicrosoftGraphCredentialsResponse struct {
	Err error `json:"error,omitempty"`
}

func (r applyMicrosoftGraphCredentialsResponse) Error() error { return r.Err }

func applyMicrosoftGraphCredentialsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*applyMicrosoftGraphCredentialsRequest)
	if err := svc.ApplyMicrosoftGraphCredentials(ctx, req.MicrosoftGraphCredentials, req.DryRun); err != nil {
		return applyMicrosoftGraphCredentialsResponse{Err: err}, nil
	}
	return applyMicrosoftGraphCredentialsResponse{}, nil
}

func (svc *Service) ApplyMicrosoftGraphCredentials(ctx context.Context, _ []automdm.MicrosoftGraphCredential, _ bool) error {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return automdm.ErrMissingLicense
}
