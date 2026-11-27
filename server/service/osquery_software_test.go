package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/service/osquery_utils"
	"github.com/stretchr/testify/require"
)

func TestPreservedSoftwareSources(t *testing.T) {
	aiSources := []string{"ai_clis", "mcp_servers", "ai_skills"}
	sentToMacOS := &hostDetailQueryConfig{detailQueries: map[string]osquery_utils.DetailQuery{
		"software_ai_tools": {Platforms: []string{"darwin"}},
	}}
	notSent := &hostDetailQueryConfig{detailQueries: map[string]osquery_utils.DetailQuery{}}
	sentToLinux := &hostDetailQueryConfig{detailQueries: map[string]osquery_utils.DetailQuery{
		"software_linux_automdmd_pacman": {Platforms: []string{"arch", "nixos"}},
		"software_go_binaries":           {Platforms: []string{"arch", "nixos"}},
		"software_adobe_plugins":         {Platforms: []string{"darwin", "windows"}},
		"software_ai_tools":              {Platforms: []string{"arch", "nixos"}},
		"software_linux_automdmd_nix":    {Platforms: []string{"nixos"}},
	}}
	allOrbitQueries := []string{
		"software_linux_automdmd_pacman", "software_go_binaries", "software_adobe_plugins", "software_ai_tools", "software_linux_automdmd_nix",
	}

	for _, tc := range []struct {
		name          string
		cfg           *hostDetailQueryConfig
		platform      string
		skipped       []string
		orbitInfo     *automdm.HostOrbitInfo
		orbitInfoErr  error
		expected      []string
		expectLookup  bool
		expectLogging bool
	}{
		{
			name:     "nothing skipped",
			cfg:      sentToMacOS,
			platform: "darwin",
			skipped:  nil,
		},
		{
			name:     "a skipped query no automdmd table backs",
			cfg:      sentToMacOS,
			platform: "darwin",
			skipped:  []string{"software_vscode_extensions"},
		},
		{
			name:     "the query was not sent to the host",
			cfg:      notSent,
			platform: "darwin",
			skipped:  []string{"software_ai_tools"},
		},
		{
			name:     "the query does not run on the host's platform",
			cfg:      sentToMacOS,
			platform: "chrome",
			skipped:  []string{"software_ai_tools"},
		},
		{
			name:         "host without automdmd",
			cfg:          sentToMacOS,
			platform:     "darwin",
			skipped:      []string{"software_ai_tools"},
			orbitInfoErr: newNotFoundError(),
			expectLookup: true,
		},
		{
			name:          "orbit info cannot be read: rows are kept rather than risked",
			cfg:           sentToMacOS,
			platform:      "darwin",
			skipped:       []string{"software_vscode_extensions", "software_ai_tools"},
			orbitInfoErr:  errors.New("replica unavailable"),
			expected:      aiSources,
			expectLookup:  true,
			expectLogging: true,
		},
		{
			name:         "automdmd version cannot be parsed",
			cfg:          sentToMacOS,
			platform:     "darwin",
			skipped:      []string{"software_ai_tools"},
			orbitInfo:    &automdm.HostOrbitInfo{Version: "dev"},
			expectLookup: true,
		},
		{
			name:         "automdmd older than the table",
			cfg:          sentToMacOS,
			platform:     "darwin",
			skipped:      []string{"software_ai_tools"},
			orbitInfo:    &automdm.HostOrbitInfo{Version: "1.58.2"},
			expectLookup: true,
		},
		{
			name:          "automdmd that shipped the table",
			cfg:           sentToMacOS,
			platform:      "darwin",
			skipped:       []string{"software_vscode_extensions", "software_ai_tools"},
			orbitInfo:     &automdm.HostOrbitInfo{Version: "1.59.0"},
			expected:      aiSources,
			expectLookup:  true,
			expectLogging: true,
		},
		{
			name:          "newer automdmd",
			cfg:           sentToMacOS,
			platform:      "darwin",
			skipped:       []string{"software_ai_tools"},
			orbitInfo:     &automdm.HostOrbitInfo{Version: "1.62.1"},
			expected:      aiSources,
			expectLookup:  true,
			expectLogging: true,
		},
		{
			name:          "every orbit query skipped: each is gated by its own platform and automdmd version",
			cfg:           sentToLinux,
			platform:      "arch",
			skipped:       allOrbitQueries,
			orbitInfo:     &automdm.HostOrbitInfo{Version: "1.56.2"},
			expected:      []string{"pacman_packages", "go_binaries"},
			expectLookup:  true,
			expectLogging: true,
		},
		{
			name:          "every orbit query skipped on a NixOS host with the newest automdmd",
			cfg:           sentToLinux,
			platform:      "nixos",
			skipped:       allOrbitQueries,
			orbitInfo:     &automdm.HostOrbitInfo{Version: "1.63.0"},
			expected:      []string{"pacman_packages", "go_binaries", "ai_clis", "mcp_servers", "ai_skills", "nix_packages"},
			expectLookup:  true,
			expectLogging: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.GetHostOrbitInfoFunc = func(ctx context.Context, hostID uint) (*automdm.HostOrbitInfo, error) {
				return tc.orbitInfo, tc.orbitInfoErr
			}
			var logs bytes.Buffer
			svc := &Service{ds: ds, logger: slog.New(slog.NewTextHandler(&logs, nil))}
			host := &automdm.Host{ID: 1, Platform: tc.platform}

			got := svc.preservedSoftwareSources(t.Context(), host, tc.cfg, tc.skipped)
			require.Equal(t, tc.expected, got)
			require.Equal(t, tc.expectLookup, ds.GetHostOrbitInfoFuncInvoked)
			if tc.expectLogging {
				require.Contains(t, logs.String(), "keeping previously reported rows")
			} else {
				require.NotContains(t, logs.String(), "keeping previously reported rows")
			}
		})
	}
}
