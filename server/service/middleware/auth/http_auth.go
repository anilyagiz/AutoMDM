package auth

import (
	"context"
	"net/http"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/logging"
	"github.com/anilyagiz/automdm/v4/server/contexts/token"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	kithttp "github.com/go-kit/kit/transport/http"
)

// SetRequestsContexts updates the request with necessary context values for a request
func SetRequestsContexts(svc automdm.Service) kithttp.RequestFunc {
	return func(ctx context.Context, r *http.Request) context.Context {
		bearer := token.FromHTTPRequest(r)
		ctx = token.NewContext(ctx, bearer)
		if bearer != "" {
			v, err := AuthViewer(ctx, string(bearer), svc)
			if err == nil {
				ctx = viewer.NewContext(ctx, *v)
				// Register viewer as error context provider for ctxerr enrichment
				ctx = ctxerr.AddErrorContextProvider(ctx, v)
				// Register viewer as user emailer for logging
				ctx = logging.WithUserEmailer(ctx, v)
			}
		}

		ctx = logging.NewContext(ctx, &logging.LoggingContext{})
		ctx = logging.WithStartTime(ctx)
		return ctx
	}
}

func SetRequestsContextMiddleware(svc automdm.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := kithttp.PopulateRequestContext(r.Context(), r)
		ctx = SetRequestsContexts(svc)(ctx, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
