package jarvis

import "testing"

func TestRepoFromURL(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://github.com/fleetdm/confidential/issues/17176", "fleetdm/confidential"},
		{"https://github.com/fleetdm/fleet/issues/123", "automdm/automdm"},
		{"https://github.com/anilyagiz/automdm", "automdm/automdm"},
		{"https://example.com/nope", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := repoFromURL(c.url); got != c.want {
			t.Errorf("repoFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
	if got := repoOr("https://github.com/fleetdm/confidential/issues/1", "automdm/automdm"); got != "fleetdm/confidential" {
		t.Errorf("repoOr should prefer the URL's repo, got %q", got)
	}
	if got := repoOr("not-a-url", "automdm/automdm"); got != "automdm/automdm" {
		t.Errorf("repoOr fallback: got %q", got)
	}
}
