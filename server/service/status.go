package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

////////////////////////////////////////////////////////////////////////////////
// Status Result Store
////////////////////////////////////////////////////////////////////////////////

type statusResponse struct {
	Err error `json:"error,omitempty"`
}

func (m statusResponse) Error() error { return m.Err }

func statusResultStoreEndpoint(ctx context.Context, req interface{}, svc automdm.Service) (automdm.Errorer, error) {
	var resp statusResponse
	if err := svc.StatusResultStore(ctx); err != nil {
		resp.Err = err
	}
	return resp, nil
}

func (svc *Service) StatusResultStore(ctx context.Context) error {
	if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
		return err
	}

	return svc.resultStore.HealthCheck()
}

////////////////////////////////////////////////////////////////////////////////
// Status Live Query
////////////////////////////////////////////////////////////////////////////////

func statusLiveQueryEndpoint(ctx context.Context, req interface{}, svc automdm.Service) (automdm.Errorer, error) {
	var resp statusResponse
	if err := svc.StatusLiveQuery(ctx); err != nil {
		resp.Err = err
	}
	return resp, nil
}

func (svc *Service) StatusLiveQuery(ctx context.Context) error {
	if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
		return err
	}

	cfg, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "retrieve app config")
	}

	if cfg.ServerSettings.LiveQueryDisabled {
		return ctxerr.Wrap(ctx, automdm.NewPermissionError("disabled by administrator"))
	}

	return svc.StatusResultStore(ctx)
}
