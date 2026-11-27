package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	apiendpoints "github.com/anilyagiz/automdm/v4/server/api_endpoints"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/automdmmcp"
	"github.com/anilyagiz/automdm/v4/server/config"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/logging"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyMCPAvailable(t *testing.T) {
	ds := new(mock.Store)
	premium, premiumCtx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{
		License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true,
	})
	free, freeCtx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{SkipCreateTestUsers: true})
	testErr := errors.New("test err")

	for _, tc := range []struct {
		name    string
		svc     automdm.Service
		ctx     context.Context
		cfg     *automdm.AppConfig
		cfgErr  error
		wantErr func(*testing.T, error)
	}{
		{"available", premium, premiumCtx, &automdm.AppConfig{}, nil, func(t *testing.T, err error) { require.NoError(t, err) }},
		{"app config error", premium, premiumCtx, nil, testErr, func(t *testing.T, err error) { require.ErrorIs(t, err, testErr) }},
		{"turned off", premium, premiumCtx, &automdm.AppConfig{ServerSettings: automdm.ServerSettings{MCPDisabled: true}}, nil, func(t *testing.T, err error) {
			var badRequest *automdm.BadRequestError
			require.ErrorAs(t, err, &badRequest)
			assert.Contains(t, badRequest.Message, "server_settings.mcp_disabled")
		}},
		{"AutoMDM Free", free, freeCtx, &automdm.AppConfig{}, nil, func(t *testing.T, err error) { require.ErrorIs(t, err, automdm.ErrMissingLicense) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) { return tc.cfg, tc.cfgErr }
			authzCtx := &authz_ctx.AuthorizationContext{}
			err := tc.svc.VerifyMCPAvailable(authz_ctx.NewContext(tc.ctx, authzCtx))
			tc.wantErr(t, err)
			// On success the request still has to authorize as usual.
			assert.Equal(t, err != nil, authzCtx.Checked())
		})
	}
}

func TestSameOrigin(t *testing.T) {
	const serverURL = "https://automdm.example.com/"
	for origin, want := range map[string]bool{
		"https://automdm.example.com":      true,
		"https://AUTOMDM.example.com:443":  true,
		"https://automdm.example.com:8443": false,
		"http://automdm.example.com":       false,
		"https://evil.example.com":         false,
		"null":                             false,
	} {
		assert.Equal(t, want, sameOrigin(origin, serverURL), origin)
	}
	assert.False(t, sameOrigin("https://automdm.example.com", ""), "no server URL means no Origin is trusted")
	assert.True(t, sameOrigin("http://localhost:8080", "http://localhost:8080/automdm"))
}

// The internal listener serves the root handler, which only answers under the
// URL prefix, and the waits must come from this server's live query period.
func TestNewEmbeddedAutoMDMClient(t *testing.T) {
	t.Setenv("AUTOMDM_LIVE_QUERY_REST_PERIOD", "7s")

	var gotPath string
	api := http.NewServeMux()
	api.HandleFunc("/api/v1/automdm/hosts/count", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]int{"count": 2})
	})
	root := http.NewServeMux()
	root.Handle("/automdm/", http.StripPrefix("/automdm", api))

	conn := NewMCPConn()
	conn.Start(t.Context(), root)
	t.Cleanup(func() { _ = conn.Shutdown(context.Background()) })

	fc, err := newEmbeddedAutoMDMClient(conn, "/automdm", slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	campaignWait, singleHostTimeout := fc.LiveQueryWaits()
	assert.Equal(t, 7*time.Second, campaignWait)
	assert.Equal(t, 37*time.Second, singleHostTimeout)

	n, err := fc.GetHostCount(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "/api/v1/automdm/hosts/count", gotPath)
}

func TestMCPUserLogHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(mcpUserLogHandler{slog.NewTextHandler(&buf, nil)})
	v := &viewer.Viewer{User: &automdm.User{Email: "mcp@example.com"}}

	logger.InfoContext(logging.WithUserEmailer(t.Context(), v), "tool invoked", "tool", "get_host")
	assert.Contains(t, buf.String(), "user=mcp@example.com")
}

// An admin can only grant catalogued endpoints, so a tool calling any other
// route would be hidden from every restricted user.
func TestMCPToolRoutesAreInCatalog(t *testing.T) {
	for tool, routes := range automdmmcp.ToolRoutes {
		for _, r := range routes {
			assert.True(t, apiendpoints.IsInCatalog(automdm.NewAPIEndpointFromTpl(r.Method, r.Path).Fingerprint()), "%s: %s", tool, r)
		}
	}
}

func TestGrantedMCPRoutes(t *testing.T) {
	assert.Nil(t, grantedMCPRoutes(&automdm.User{}))
	assert.Nil(t, grantedMCPRoutes(&automdm.User{APIOnly: true}))

	granted := grantedMCPRoutes(&automdm.User{APIOnly: true, APIEndpoints: []automdm.APIEndpointRef{
		{Method: "POST", Path: "/api/v1/automdm/mcp"},
		{Method: "GET", Path: "/api/v1/automdm/hosts/:id"},
	}})
	require.NotNil(t, granted)
	assert.True(t, granted(automdmmcp.Route{Method: "GET", Path: "/api/v1/automdm/hosts/:host_id"}), "placeholder names don't matter")
	assert.False(t, granted(automdmmcp.Route{Method: "POST", Path: "/api/v1/automdm/hosts/:id"}))
	assert.False(t, granted(automdmmcp.Route{Method: "GET", Path: "/api/v1/automdm/hosts"}))
}
