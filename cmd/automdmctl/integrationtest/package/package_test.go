package _package

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/automdmctltest"
	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/orbit/pkg/packaging"
	"github.com/anilyagiz/automdm/v4/orbit/pkg/update"
	"github.com/anilyagiz/automdm/v4/pkg/nettest"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/require"
)

func TestPackage(t *testing.T) {
	nettest.Run(t)

	// automdmctl package reads ~/.automdm/config (USERPROFILE on Windows) for the team name: never the developer's own.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CONFIG", "") // empty: only an explicit --config gives the lookup a config, never the developer's own

	updateOpt := update.DefaultOptions
	updateOpt.RootDirectory = t.TempDir()
	_, err := packaging.InitializeUpdates(updateOpt)
	require.NoError(t, err)

	// --type is required
	automdmctltest.RunAppCheckErr(t, []string{"package", "deb"}, "Required flag \"type\" not set")

	// if you provide -automdm-url & --enroll-secret are required together
	automdmctltest.RunAppCheckErr(t, []string{"package", "--type=deb", "--automdm-url=https://localhost:8080"},
		"--enroll-secret and --automdm-url must be provided together")
	automdmctltest.RunAppCheckErr(t, []string{"package", "--type=deb", "--enroll-secret=foobar"}, "--enroll-secret and --automdm-url must be provided together")

	// --insecure and --automdm-certificate are mutually exclusive
	automdmctltest.RunAppCheckErr(t, []string{"package", "--type=deb", "--insecure", "--automdm-certificate=test123"},
		"--insecure and --automdm-certificate may not be provided together")

	// Test invalid PEM file provided in --automdm-certificate.
	certDir := t.TempDir()
	automdmCertificate := filepath.Join(certDir, "automdm.pem")
	err = os.WriteFile(automdmCertificate, []byte("undefined"), os.FileMode(0o644))
	require.NoError(t, err)
	automdmctltest.RunAppCheckErr(t, []string{"package", "--type=deb", fmt.Sprintf("--automdm-certificate=%s", automdmCertificate)},
		fmt.Sprintf("failed to read automdm server certificate %q: invalid PEM file", automdmCertificate))

	if runtime.GOOS != "linux" {
		automdmctltest.RunAppCheckErr(t, []string{"package", "--type=msi", "--native-tooling"}, "native tooling is only available in Linux")
	}

	t.Run("deb", func(t *testing.T) {
		t.Cleanup(func() { os.Remove("automdm-linux-amd64.deb") })
		automdmctltest.RunAppForTest(t, []string{"package", "--type=deb", "--insecure", "--disable-open-folder"})
		info, err := os.Stat("automdm-linux-amd64.deb")
		require.NoError(t, err)
		require.Positive(t, info.Size()) // TODO verify contents
	})

	t.Run("deb with the team name", func(t *testing.T) {
		server, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)},
		})
		ds.ListTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions) ([]*automdm.Team, error) {
			return []*automdm.Team{
				{ID: 1, Name: "Workstations (Canary)", Secrets: []*automdm.EnrollSecret{{Secret: "canary-secret"}}},
				// Cleans up to the same name, so the team ID tells the two packages apart.
				{ID: 2, Name: "workstations canary", Secrets: []*automdm.EnrollSecret{{Secret: "other-secret"}}},
			}, nil
		}
		config := fmt.Sprintf("contexts:\n  default:\n    address: %s\n    token: admin-token\n    tls-skip-verify: true\n", server.URL)
		require.NoError(t, os.MkdirAll(filepath.Join(home, ".team"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, ".team", "config"), []byte(config), 0o600))

		t.Cleanup(func() { os.Remove("automdm-linux-arm64-workstations-canary-1.deb") })
		automdmctltest.RunAppForTest(t, []string{
			"package", "--type=deb", "--arch=arm64", "--insecure", "--disable-open-folder",
			"--automdm-url=" + server.URL, "--enroll-secret=canary-secret", "--config=" + filepath.Join(home, ".team", "config"),
		})
		info, err := os.Stat("automdm-linux-arm64-workstations-canary-1.deb")
		require.NoError(t, err)
		require.Positive(t, info.Size())
	})

	t.Run("--use-sytem-configuration can't be used on installers that aren't pkg", func(t *testing.T) {
		for _, p := range []string{"deb", "msi", "rpm", ""} {
			automdmctltest.RunAppCheckErr(
				t,
				[]string{"package", fmt.Sprintf("--type=%s", p), "--use-system-configuration"},
				"--use-system-configuration is only available for pkg installers",
			)
		}
	})

	t.Run("--cpu-quota is only available for Linux packages", func(t *testing.T) {
		for _, p := range []string{"pkg", "msi"} {
			automdmctltest.RunAppCheckErr(
				t,
				[]string{"package", fmt.Sprintf("--type=%s", p), "--cpu-quota=1"},
				"--cpu-quota is only supported for deb/rpm/pkg.tar.zst packages",
			)
		}
		automdmctltest.RunAppCheckErr(t, []string{"package", "--type=deb", "--cpu-quota=0"}, "--cpu-quota must be greater than 0")
	})

	// automdm-windows-amd64.msi
	// runAppForTest(t, []string{"package", "--type=msi", "--insecure"}) TODO: this is currently failing on Github runners due to permission issues
	// info, err = os.Stat("orbit-osquery_0.0.3.msi")
	// require.NoError(t, err)
	// require.Greater(t, info.Size(), int64(0))

	// runAppForTest(t, []string{"package", "--type=pkg", "--insecure"}) TODO: had a hard time getting xar installed on Ubuntu
}
