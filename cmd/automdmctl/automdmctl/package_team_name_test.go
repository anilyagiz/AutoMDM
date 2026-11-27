package automdmctl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/orbit/pkg/packaging"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamNameForPackage(t *testing.T) {
	premium, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
		License: &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)},
	})
	ds.ListTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions) ([]*automdm.Team, error) {
		return []*automdm.Team{
			{ID: 1, Name: "Servers", Secrets: []*automdm.EnrollSecret{{Secret: "servers-secret"}}},
			{ID: 2, Name: "\U0001F4BB Workstations", Secrets: []*automdm.EnrollSecret{{Secret: "old-secret"}, {Secret: "workstations-secret"}}},
			{ID: 3, Name: "Workstations", Secrets: []*automdm.EnrollSecret{{Secret: "other-workstations-secret"}}},
		}, nil
	}
	// Tokens for an observer, whose view of enroll secrets AutoMDM masks, and for an expired session.
	observer := &automdm.User{ID: 9999, Name: "observer", Email: "observer@example.com", GlobalRole: new(automdm.RoleObserver)}
	adminSession, adminUser := ds.SessionByKeyFunc, ds.UserByIDFunc
	ds.SessionByKeyFunc = func(ctx context.Context, key string) (*automdm.Session, error) {
		switch key {
		case "expired-token":
			return nil, &notFoundError{}
		case "observer-token":
			return &automdm.Session{CreatedAt: time.Now(), AccessedAt: time.Now(), UserID: observer.ID, Key: key}, nil
		}
		return adminSession(ctx, key)
	}
	ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
		if id == observer.ID {
			return observer, nil
		}
		return adminUser(ctx, id)
	}

	free, _ := testing_utils.RunServerWithMockedDS(t)

	// A server that never answers, as when --automdm-url points to the wrong host.
	stuck := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-stuck }))
	t.Cleanup(func() { close(stuck); hung.Close() })

	servers := packageAutoMDM{Name: "Servers", ID: 1, Others: []packaging.AutoMDM{{ID: 2, Name: "\U0001F4BB Workstations"}, {ID: 3, Name: "Workstations"}}}
	type automdmctlContext struct{ name, address, token string }
	for _, tc := range []struct {
		name       string
		contexts   []automdmctlContext
		automdmURL string
		secret     string
		want       packageAutoMDM
		wantNote   bool
	}{
		{
			name:       "automdm secret",
			contexts:   []automdmctlContext{{"default", premium.URL, "admin-token"}},
			automdmURL: premium.URL, secret: "workstations-secret",
			want: packageAutoMDM{Name: "\U0001F4BB Workstations", ID: 2, Others: []packaging.AutoMDM{{ID: 1, Name: "Servers"}, {ID: 3, Name: "Workstations"}}},
		},
		{
			name:       "URL spelled differently from the context address",
			contexts:   []automdmctlContext{{"default", premium.URL, "admin-token"}},
			automdmURL: strings.ToUpper(premium.URL) + "/", secret: "servers-secret", want: servers,
		},
		{
			name:       "global secret",
			contexts:   []automdmctlContext{{"default", premium.URL, "admin-token"}},
			automdmURL: premium.URL, secret: "global-secret",
		},
		{
			name:       "role that can't read enroll secrets",
			contexts:   []automdmctlContext{{"default", premium.URL, "observer-token"}},
			automdmURL: premium.URL, secret: "servers-secret",
		},
		{
			name:       "masked secret doesn't match a team",
			contexts:   []automdmctlContext{{"default", premium.URL, "observer-token"}},
			automdmURL: premium.URL, secret: automdm.MaskedPassword,
		},
		{
			name:       "expired token prints a note",
			contexts:   []automdmctlContext{{"default", premium.URL, "expired-token"}},
			automdmURL: premium.URL, secret: "servers-secret", wantNote: true,
		},
		{
			name:       "role that can't read secrets in one context, admin in another",
			contexts:   []automdmctlContext{{"default", premium.URL, "observer-token"}, {"other", premium.URL, "admin-token"}},
			automdmURL: premium.URL, secret: "servers-secret", want: servers,
		},
		{
			name:       "expired token in one context, valid in another",
			contexts:   []automdmctlContext{{"default", premium.URL, "expired-token"}, {"other", premium.URL, "admin-token"}},
			automdmURL: premium.URL, secret: "servers-secret", want: servers,
		},
		{
			name:       "context for another server is not used",
			contexts:   []automdmctlContext{{"default", free.URL, "admin-token"}, {"prod", premium.URL, "admin-token"}},
			automdmURL: premium.URL, secret: "servers-secret", want: servers,
		},
		{
			name:       "no context for the server",
			contexts:   []automdmctlContext{{"default", premium.URL, "admin-token"}},
			automdmURL: "https://automdm.example.com", secret: "servers-secret",
		},
		{
			name:       "context without a token",
			contexts:   []automdmctlContext{{"default", premium.URL, ""}},
			automdmURL: premium.URL, secret: "servers-secret",
		},
		{
			name:       "AutoMDM Free",
			contexts:   []automdmctlContext{{"default", free.URL, "admin-token"}},
			automdmURL: free.URL, secret: "servers-secret",
		},
		{
			name:       "not a URL",
			contexts:   []automdmctlContext{{"default", premium.URL, "admin-token"}},
			automdmURL: "dummy", secret: "dummy",
		},
		{
			name:       "server that never answers",
			contexts:   []automdmctlContext{{"default", hung.URL, "admin-token"}},
			automdmURL: hung.URL, secret: "servers-secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config strings.Builder
			config.WriteString("contexts:\n")
			for _, c := range tc.contexts {
				fmt.Fprintf(&config, "  %s:\n    address: %s\n    token: %q\n    tls-skip-verify: true\n", c.name, c.address, c.token)
			}
			configPath := filepath.Join(t.TempDir(), "config")
			require.NoError(t, os.WriteFile(configPath, []byte(config.String()), configFilePerms))

			oldTimeout := teamNameLookupTimeout
			teamNameLookupTimeout = 500 * time.Millisecond
			t.Cleanup(func() { teamNameLookupTimeout = oldTimeout })

			var errOut bytes.Buffer
			start := time.Now()
			require.Equal(t, tc.want, automdmForPackage(configPath, tc.automdmURL, tc.secret, &errOut))
			assert.Less(t, time.Since(start), 5*time.Second)
			if tc.wantNote {
				assert.Equal(t, "Note: the team's name was left out of the package's filename because the API token for "+tc.automdmURL+
					" is invalid or expired. To add it, log in with: automdmctl login\n", errOut.String())
			} else {
				assert.Empty(t, errOut.String())
			}
		})
	}

	t.Run("missing config file", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config")
		require.Zero(t, automdmForPackage(configPath, premium.URL, "servers-secret", &bytes.Buffer{}))
		require.NoFileExists(t, configPath)
	})
}

func TestNormalizeServerURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"https://automdm.example.com", "https://automdm.example.com"},
		{"https://AutoMDM.Example.com/", "https://automdm.example.com"},
		{"HTTPS://automdm.example.com:443", "https://automdm.example.com"},
		{"automdm.example.com", "https://automdm.example.com"},
		{"automdm.example.com:8080", "https://automdm.example.com:8080"},
		{"http://localhost:80/", "http://localhost"},
		{"https://example.com/automdm/", "https://example.com/automdm"},
		{"https://[::1]:8080", "https://[::1]:8080"},
		{"https://[::1]", "https://[::1]"},
		{"  https://automdm.example.com  ", "https://automdm.example.com"},
	} {
		got, ok := normalizeServerURL(tc.in)
		require.True(t, ok, tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}
	for _, in := range []string{"", "   ", "https://", "://"} {
		_, ok := normalizeServerURL(in)
		require.False(t, ok, in)
	}

	require.Equal(t, "https://example.com/automdm", joinURLPrefix("https://example.com/", "/automdm/"))
	require.Equal(t, "https://example.com", joinURLPrefix("https://example.com", ""))
}
