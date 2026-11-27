package test

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	hostctx "github.com/anilyagiz/automdm/v4/server/contexts/host"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

// UserContext returns a new context with the provided user as the viewer.
func UserContext(ctx context.Context, user *automdm.User) context.Context {
	return viewer.NewContext(ctx, viewer.Viewer{User: user})
}

// HostContext returns a new context with the provided host as the
// device-authenticated host.
func HostContext(ctx context.Context, host *automdm.Host) context.Context {
	authzCtx := &authz_ctx.AuthorizationContext{}
	ctx = authz_ctx.NewContext(ctx, authzCtx)
	ctx = hostctx.NewContext(ctx, host)
	if ac, ok := authz_ctx.FromContext(ctx); ok {
		ac.SetAuthnMethod(authz_ctx.AuthnDeviceToken)
	}
	return ctx
}
