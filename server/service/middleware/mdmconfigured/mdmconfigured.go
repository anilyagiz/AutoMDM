// Package mdmconfigured implements middleware functions for the supported platform-specific MDM
// solutions to ensure MDM is configured and fail fast before reaching the handler if that is not the case.
package mdmconfigured

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	hostctx "github.com/anilyagiz/automdm/v4/server/contexts/host"
	"github.com/go-kit/kit/endpoint"
)

type Middleware struct {
	svc automdm.Service
}

func NewMDMConfigMiddleware(svc automdm.Service) *Middleware {
	return &Middleware{svc: svc}
}

func (m *Middleware) VerifyAppleMDM() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if err := m.svc.VerifyMDMAppleConfigured(ctx); err != nil {
				return nil, err
			}

			return next(ctx, req)
		}
	}
}

// VerifyAppleMDMOnMacOSHosts verifies that MDM is enabled and configured when it's an Apple host making the request.
// This is used on API endpoints that are reused on Linux hosts (which don't require Apple MDM to be configured).
func (m *Middleware) VerifyAppleMDMOnMacOSHosts() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			host, ok := hostctx.FromContext(ctx)
			if !ok {
				return nil, ctxerr.Wrap(ctx, automdm.NewAuthRequiredError("internal error: missing host from request context"))
			}
			if automdm.IsApplePlatform(host.Platform) {
				if err := m.svc.VerifyMDMAppleConfigured(ctx); err != nil {
					return nil, err
				}
			}

			return next(ctx, req)
		}
	}
}

func (m *Middleware) VerifyWindowsMDM() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if err := m.svc.VerifyMDMWindowsConfigured(ctx); err != nil {
				return nil, err
			}

			return next(ctx, req)
		}
	}
}

func (m *Middleware) VerifyAndroidMDM() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if err := m.svc.VerifyMDMAndroidConfigured(ctx); err != nil {
				return nil, err
			}

			return next(ctx, req)
		}
	}
}

func (m *Middleware) VerifyAnyMDM() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if err := m.svc.VerifyAnyMDMConfigured(ctx); err != nil {
				return nil, err
			}

			return next(ctx, req)
		}
	}
}
