package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	kithttp "github.com/go-kit/kit/transport/http"

	"github.com/anilyagiz/automdm/v4/ee/server/service/hostidentity/httpsig"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/logging"
	"github.com/anilyagiz/automdm/v4/server/contexts/token"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/service/middleware/log"
	"github.com/go-kit/kit/endpoint"
)

// AuthViewer creates an authenticated viewer by validating the session key.
func AuthViewer(ctx context.Context, sessionKey string, svc automdm.Service) (*viewer.Viewer, error) {
	session, err := svc.GetSessionByKey(ctx, sessionKey)
	if err != nil {
		return nil, authViewerError(ctx, err)
	}
	user, err := svc.UserUnauthorized(ctx, session.UserID)
	if err != nil {
		return nil, authViewerError(ctx, err)
	}
	return &viewer.Viewer{User: user, Session: session}, nil
}

func authViewerError(ctx context.Context, err error) error {
	if authErr, ok := errors.AsType[*automdm.AuthRequiredError](err); ok {
		return authErr
	}
	// A client that disconnected mid-lookup is not a server failure.
	if automdm.IsNotFound(err) || errors.Is(err, context.Canceled) {
		return automdm.NewAuthRequiredError(err.Error())
	}
	return ctxerr.Wrap(ctx, automdm.NewAuthUnavailableError(err.Error()), "authenticate session")
}

// AuthenticatedUser wraps an endpoint, requires that the AutoMDM user is
// authenticated, and populates the context with a Viewer struct for that user.
//
// If auth fails or the user must reset their password, an error is returned.
func AuthenticatedUser(svc automdm.Service, next endpoint.Endpoint) endpoint.Endpoint {
	authUserFunc := func(ctx context.Context, request interface{}) (interface{}, error) {
		// first check if already successfully set
		if v, ok := viewer.FromContext(ctx); ok {
			if v.User.IsAdminForcedPasswordReset() {
				return nil, automdm.ErrPasswordResetRequired
			}

			return next(ctx, request)
		}

		requestPath, _ := ctx.Value(kithttp.ContextKeyRequestPath).(string)

		httpSig, sigOk := httpsig.FromContext(ctx)
		if sigOk && httpsig.IsSigAuthEndpoint(requestPath) {
			if time.Now().After(httpSig.NotValidAfter) {
				return nil, automdm.NewAuthFailedError("host identity certificate expired")
			}
			if httpSig.HostID == nil {
				return nil, automdm.NewAuthFailedError("identity certificate is not linked to a specific host")
			}
			if ac, ok := authz.FromContext(ctx); ok {
				ac.SetAuthnMethod(authz.AuthnHTTPMessageSignature)
			}
			return next(ctx, request)
		}

		// if not successful, try again this time with errors
		sessionKey, ok := token.FromContext(ctx)
		if !ok {
			return nil, automdm.NewAuthHeaderRequiredError("no auth token")
		}

		v, err := AuthViewer(ctx, string(sessionKey), svc)
		if err != nil {
			return nil, err
		}

		if v.User.IsAdminForcedPasswordReset() {
			return nil, automdm.ErrPasswordResetRequired
		}

		ctx = viewer.NewContext(ctx, *v)
		// Register viewer as error context provider for ctxerr enrichment
		ctx = ctxerr.AddErrorContextProvider(ctx, v)
		// Register viewer as user emailer for logging
		ctx = logging.WithUserEmailer(ctx, v)
		if ac, ok := authz.FromContext(ctx); ok {
			ac.SetAuthnMethod(authz.AuthnUserToken)
		}
		return next(ctx, request)
	}

	return log.Logged(authUserFunc)
}

func UnauthenticatedRequest(_ automdm.Service, next endpoint.Endpoint) endpoint.Endpoint {
	return log.Logged(next)
}

// errorHandler has the same signature as http.Error
type errorHandler func(w http.ResponseWriter, detail string, status int)

func AuthenticatedUserMiddleware(svc automdm.Service, errHandler errorHandler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// first check if already successfully set
		if v, ok := viewer.FromContext(r.Context()); ok {
			if v.User.IsAdminForcedPasswordReset() {
				errHandler(w, automdm.ErrPasswordResetRequired.Error(), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// if not successful, try again this time with errors
		sessionKey, ok := token.FromContext(r.Context())
		if !ok {
			errHandler(w, automdm.NewAuthHeaderRequiredError("no auth token").Error(), http.StatusUnauthorized)
			return
		}

		v, err := AuthViewer(r.Context(), string(sessionKey), svc)
		if err != nil {
			if unavailable, ok := errors.AsType[*automdm.AuthUnavailableError](err); ok {
				logging.WithErr(r.Context(), err)
				errHandler(w, unavailable.Error(), unavailable.StatusCode())
				return
			}
			errHandler(w, err.Error(), http.StatusUnauthorized)
			return
		}

		if v.User.IsAdminForcedPasswordReset() {
			errHandler(w, automdm.ErrPasswordResetRequired.Error(), http.StatusUnauthorized)
			return
		}

		ctx := viewer.NewContext(r.Context(), *v)
		// Register viewer as error context provider for ctxerr enrichment
		ctx = ctxerr.AddErrorContextProvider(ctx, v)
		// Register viewer as user emailer for logging
		ctx = logging.WithUserEmailer(ctx, v)
		if ac, ok := authz.FromContext(r.Context()); ok {
			ac.SetAuthnMethod(authz.AuthnUserToken)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
