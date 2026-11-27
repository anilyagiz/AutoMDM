package sso

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallbackURL(t *testing.T) {
	const callbackPath = "/api/v1/automdm/sso/callback"

	testCases := []struct {
		name      string
		baseURL   string
		urlPrefix string
		want      string
	}{
		{
			name:      "no prefix configured",
			baseURL:   "https://automdm.example.com",
			urlPrefix: "",
			want:      "https://automdm.example.com/api/v1/automdm/sso/callback",
		},
		{
			name:      "root prefix is treated as no prefix",
			baseURL:   "https://automdm.example.com",
			urlPrefix: "/",
			want:      "https://automdm.example.com/api/v1/automdm/sso/callback",
		},
		{
			name:      "prefix set and base url already includes it",
			baseURL:   "https://automdm.example.com/apps/automdm",
			urlPrefix: "/apps/automdm",
			want:      "https://automdm.example.com/apps/automdm/api/v1/automdm/sso/callback",
		},
		{
			name:      "prefix set and base url omits it",
			baseURL:   "https://automdm.example.com",
			urlPrefix: "/apps/automdm",
			want:      "https://automdm.example.com/apps/automdm/api/v1/automdm/sso/callback",
		},
		{
			name:      "base url includes prefix with trailing slash",
			baseURL:   "https://automdm.example.com/apps/automdm/",
			urlPrefix: "/apps/automdm",
			want:      "https://automdm.example.com/apps/automdm/api/v1/automdm/sso/callback",
		},
		{
			name:      "prefix configured with trailing slash",
			baseURL:   "https://automdm.example.com/apps/automdm",
			urlPrefix: "/apps/automdm/",
			want:      "https://automdm.example.com/apps/automdm/api/v1/automdm/sso/callback",
		},
		{
			name:      "proxy mounts team under an additional outer segment",
			baseURL:   "https://automdm.example.com/gateway/apps/automdm",
			urlPrefix: "/apps/automdm",
			want:      "https://automdm.example.com/gateway/apps/automdm/api/v1/automdm/sso/callback",
		},
		{
			name:      "outer segment that is not a full path segment still gets the prefix",
			baseURL:   "https://automdm.example.com/myapps/automdm",
			urlPrefix: "/apps/automdm",
			want:      "https://automdm.example.com/myapps/automdm/apps/automdm/api/v1/automdm/sso/callback",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := url.Parse(tc.baseURL)
			require.NoError(t, err)
			got := CallbackURL(base, tc.urlPrefix, callbackPath)
			require.Equal(t, tc.want, got.String())
			// The base URL must not be mutated, so callers can still use it for
			// other purposes (e.g. the expected SAML audience).
			require.Equal(t, tc.baseURL, base.String())
		})
	}
}
