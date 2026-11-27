package govulndb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

// testArtifact covers the shapes seen in the real database: open-ended, fixed, unfixed and
// CVE-less module advisories, the multi-range stdlib shape, and a toolchain advisory with a fixed
// and an unfixed range.
func testArtifact() *Artifact {
	return &Artifact{
		SchemaVersion: "1",
		Modules: map[string][]Advisory{
			"github.com/air-verse/air": {
				{
					ID:     "GO-2024-1000",
					CVEs:   []string{"CVE-2024-1000"},
					Ranges: []VersionRange{{Introduced: "0", Fixed: "1.49.0"}},
				},
				{
					ID:     "GO-2024-1001",
					CVEs:   []string{"CVE-2024-1001"},
					Ranges: []VersionRange{{Introduced: "1.20.0", Fixed: "1.40.0"}},
				},
				{
					ID:     "GO-2026-1002",
					CVEs:   []string{"CVE-2026-1002"},
					Ranges: []VersionRange{{Introduced: "1.48.0"}},
				},
				{
					ID:     "GO-2026-1003",
					Ranges: []VersionRange{{Introduced: "0"}},
				},
			},
			StdlibModule: {
				{
					ID:   "GO-2024-2963",
					CVEs: []string{"CVE-2024-24791"},
					Ranges: []VersionRange{
						{Introduced: "0", Fixed: "1.21.12"},
						{Introduced: "1.22.0-0", Fixed: "1.22.5"},
					},
				},
			},
			ToolchainModule: {
				{
					ID:     "GO-2024-3000",
					CVEs:   []string{"CVE-2024-3000"},
					Ranges: []VersionRange{{Introduced: "0", Fixed: "1.22.3"}, {Introduced: "1.27.0"}},
				},
			},
		},
	}
}

func cves(vulns []automdm.SoftwareVulnerability) []string {
	out := make([]string, 0, len(vulns))
	for _, v := range vulns {
		out = append(out, v.CVE)
	}
	return out
}

func TestMatchBinary(t *testing.T) {
	artifact := testArtifact()

	for _, tc := range []struct {
		name     string
		software automdm.Software
		wantCVEs []string
	}{
		{
			name:     "module advisory below the fix",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.48.0", ExtensionID: "github.com/air-verse/air"},
			wantCVEs: []string{"CVE-2024-1000", "CVE-2026-1002"},
		},
		{
			name:     "module advisory at the fix",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.49.0", ExtensionID: "github.com/air-verse/air"},
			wantCVEs: []string{"CVE-2026-1002"},
		},
		{
			name:     "version below the introduced bound",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.10.0", ExtensionID: "github.com/air-verse/air"},
			wantCVEs: []string{"CVE-2024-1000"},
		},
		{
			name:     "version inside a closed range",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.30.0", ExtensionID: "github.com/air-verse/air"},
			wantCVEs: []string{"CVE-2024-1000", "CVE-2024-1001"},
		},
		{
			name:     "unknown module",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.0.0", ExtensionID: "github.com/other/air"},
			wantCVEs: nil,
		},
		{
			name:     "no module path and no toolchain",
			software: automdm.Software{ID: 1, Name: "air", Version: "v1.48.0"},
			wantCVEs: nil,
		},
		{
			name:     "devel version skips only the module lookup",
			software: automdm.Software{ID: 1, Name: "air", Version: "(devel)", ExtensionID: "github.com/air-verse/air", Release: "go1.21.11"},
			wantCVEs: []string{"CVE-2024-24791"},
		},
		{
			// `go install` on an untagged module or a commit records v0.0.0-<pseudo>, which
			// sorts below "0"'s v0.0.0.
			name:     "pseudo-version of an untagged module",
			software: automdm.Software{ID: 1, Name: "air", Version: "v0.0.0-20240101000000-abcdef123456", ExtensionID: "github.com/air-verse/air"},
			wantCVEs: []string{"CVE-2024-1000"},
		},
		{
			name:     "stdlib advisory below the fix",
			software: automdm.Software{ID: 2, Name: "tool", Version: "v1.0.0", Release: "go1.21.11"},
			wantCVEs: []string{"CVE-2024-24791"},
		},
		{
			name:     "stdlib advisory at the fix",
			software: automdm.Software{ID: 2, Name: "tool", Version: "v1.0.0", Release: "go1.21.12"},
			wantCVEs: nil,
		},
		{
			name:     "stdlib advisory in the next minor's range",
			software: automdm.Software{ID: 2, Name: "tool", Version: "v1.0.0", Release: "go1.22.4"},
			wantCVEs: []string{"CVE-2024-24791"},
		},
		{
			name:     "stdlib advisory past the next minor's fix",
			software: automdm.Software{ID: 2, Name: "tool", Version: "v1.0.0", Release: "go1.22.5"},
			wantCVEs: nil,
		},
		{
			name:     "release candidate toolchain inside the next minor's range",
			software: automdm.Software{ID: 2, Name: "tool", Version: "v1.0.0", Release: "go1.22rc1"},
			wantCVEs: []string{"CVE-2024-24791"},
		},
		{
			name: "module and stdlib advisories together",
			software: automdm.Software{
				ID: 3, Name: "air", Version: "v1.48.0",
				ExtensionID: "github.com/air-verse/air", Release: "go1.21.11",
			},
			wantCVEs: []string{"CVE-2024-1000", "CVE-2026-1002", "CVE-2024-24791"},
		},
		{
			name:     "toolchain advisories never match",
			software: automdm.Software{ID: 4, Name: "go", Version: "v1.21.11", ExtensionID: "toolchain", Release: "go1.21.11"},
			wantCVEs: []string{"CVE-2024-24791"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := matchBinary(&tc.software, artifact)
			require.ElementsMatch(t, tc.wantCVEs, cves(got))
			for _, v := range got {
				require.Equal(t, tc.software.ID, v.SoftwareID)
			}
		})
	}
}

func TestModuleVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    string
		ok      bool
	}{
		{version: "v1.48.0", want: "v1.48.0", ok: true},
		{version: "1.48.0", want: "v1.48.0", ok: true},
		{version: "v0.0.0-20240101000000-abcdef123456", want: "v0.0.0-20240101000000-abcdef123456", ok: true},
		{version: "(devel)"},
		{version: "not-a-version"},
		{version: ""},
	} {
		t.Run(tc.version, func(t *testing.T) {
			got, ok := moduleVersion(tc.version)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestToolchainVersion(t *testing.T) {
	for _, tc := range []struct {
		release string
		want    string
		ok      bool
	}{
		{release: "go1.21.12", want: "v1.21.12", ok: true},
		{release: "go1.21", want: "v1.21", ok: true},
		{release: "go1.22rc1", want: "v1.22.0-rc.1", ok: true},
		{release: "go1.22beta1", want: "v1.22.0-beta.1", ok: true},
		// Binaries built with a GOEXPERIMENT report it after the version.
		{release: "go1.21.11 X:boringcrypto", want: "v1.21.11", ok: true},
		{release: "devel go1.27-abcdef1234 Mon Jan 1 00:00:00 2026 +0000"},
		{release: ""},
	} {
		t.Run(tc.release, func(t *testing.T) {
			got, ok := toolchainVersion(tc.release)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestIsToolchainInstall(t *testing.T) {
	for _, tc := range []struct {
		name     string
		software automdm.Software
		want     bool
	}{
		{name: "homebrew go", software: automdm.Software{Name: "go", Source: "homebrew_packages"}, want: true},
		{name: "homebrew versioned go", software: automdm.Software{Name: "go@1.24", Source: "homebrew_packages"}, want: true},
		{name: "homebrew formula starting with go", software: automdm.Software{Name: "gotop", Source: "homebrew_packages"}},
		{name: "homebrew formula with go in its name", software: automdm.Software{Name: "go-task", Source: "homebrew_packages"}},
		{name: "arch go", software: automdm.Software{Name: "go", Source: "pacman_packages"}, want: true},
		{name: "arch go tools", software: automdm.Software{Name: "go-tools", Source: "pacman_packages"}},
		{name: "nixos go", software: automdm.Software{Name: "go", Source: "nix_packages"}, want: true},
		{name: "nixos go tools", software: automdm.Software{Name: "gotools", Source: "nix_packages"}},
		{name: "gentoo go", software: automdm.Software{Name: "dev-lang/go", Source: "portage_packages"}, want: true},
		{
			// Only used to build dev-lang/go, and pinned to an old release.
			name:     "gentoo go bootstrap",
			software: automdm.Software{Name: "dev-lang/go-bootstrap", Source: "portage_packages"},
		},
		{name: "gentoo package named go in another category", software: automdm.Software{Name: "app-misc/go", Source: "portage_packages"}},
		{
			name:     "windows go installer",
			software: automdm.Software{Name: "Go Programming Language amd64 go1.27.4", Source: "programs"},
			want:     true,
		},
		{name: "windows program starting with Go", software: automdm.Software{Name: "GoLand 2025.3.3", Source: "programs"}},
		{
			// Distributions backport fixes without changing the upstream version, so these stay
			// on OVAL.
			name:     "debian golang package",
			software: automdm.Software{Name: "golang-go", Source: "deb_packages"},
		},
		{name: "go binary named go", software: automdm.Software{Name: "go", Source: "go_binaries"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := publishedRules(t)
			require.Equal(t, tc.want, rules.IsToolchainInstall(&tc.software))

			// NVD skips every toolchain install, so if the SQL prefilter dropped one it would get
			// no CVEs at all. (?i) because MySQL's REGEXP ignores case under the column's collation.
			if tc.want {
				opts, ok := rules.iterOptions()
				require.True(t, ok)
				require.Contains(t, opts.IncludedSources, tc.software.Source)
				require.Regexp(t, "(?i)"+opts.NameMatch, tc.software.Name)
			}
		})
	}
}

func TestMatchToolchainInstall(t *testing.T) {
	artifact := testArtifact()

	type vuln struct {
		cve        string
		resolvedIn string
	}

	for _, tc := range []struct {
		name     string
		software automdm.Software
		want     []vuln
	}{
		{
			name:     "stdlib and toolchain advisories below their fixes",
			software: automdm.Software{ID: 1, Name: "go", Source: "homebrew_packages", Version: "1.21.11"},
			want:     []vuln{{"CVE-2024-24791", "1.21.12"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name:     "fix taken from the range the version falls in",
			software: automdm.Software{ID: 1, Name: "go", Source: "homebrew_packages", Version: "1.22.4"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}},
		},
		{
			name:     "patched toolchain",
			software: automdm.Software{ID: 1, Name: "go", Source: "homebrew_packages", Version: "1.22.5"},
		},
		{
			name:     "homebrew revision suffix",
			software: automdm.Software{ID: 1, Name: "go@1.22", Source: "homebrew_packages", Version: "1.22.4_1"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}},
		},
		{
			name:     "arch",
			software: automdm.Software{ID: 1, Name: "go", Source: "pacman_packages", Version: "1.22.2", Release: "1"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name:     "arch row stored with epoch and package release",
			software: automdm.Software{ID: 1, Name: "go", Source: "pacman_packages", Version: "2:1.22.2-1"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name:     "nixos",
			software: automdm.Software{ID: 1, Name: "go", Source: "nix_packages", Version: "1.22.2"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name:     "gentoo",
			software: automdm.Software{ID: 1, Name: "dev-lang/go", Source: "portage_packages", Version: "1.22.4"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}},
		},
		{
			name:     "gentoo ebuild revision",
			software: automdm.Software{ID: 1, Name: "dev-lang/go", Source: "portage_packages", Version: "1.22.4-r1"},
			want:     []vuln{{"CVE-2024-24791", "1.22.5"}},
		},
		{
			name:     "advisory with no fix leaves the fix unset",
			software: automdm.Software{ID: 1, Name: "go", Source: "homebrew_packages", Version: "1.27.1"},
			want:     []vuln{{"CVE-2024-3000", ""}},
		},
		{
			name: "windows installer",
			software: automdm.Software{
				ID: 1, Name: "Go Programming Language amd64 go1.22.2", Source: "programs", Version: "1.22.2",
			},
			want: []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name: "windows installer release candidate, named after the Go tag",
			software: automdm.Software{
				ID: 1, Name: "Go Programming Language amd64 go1.22rc1", Source: "programs",
			},
			want: []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name: "windows installer name without a Go tag falls back to the version",
			software: automdm.Software{
				ID: 1, Name: "Go Programming Language amd64", Source: "programs", Version: "1.22.2",
			},
			want: []vuln{{"CVE-2024-24791", "1.22.5"}, {"CVE-2024-3000", "1.22.3"}},
		},
		{
			name:     "unparseable version",
			software: automdm.Software{ID: 1, Name: "go", Source: "homebrew_packages", Version: "HEAD-abcdef1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []vuln
			for _, v := range publishedRules(t).match(&tc.software, artifact) {
				require.Equal(t, tc.software.ID, v.SoftwareID)
				// "" in want means unset: a missing fix is never stored as an empty string.
				if v.ResolvedInVersion != nil {
					require.NotEmpty(t, *v.ResolvedInVersion)
				}
				got = append(got, vuln{v.CVE, ptr.ValOrZero(v.ResolvedInVersion)})
			}
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

type fakeSoftwareIterator struct {
	index     int
	softwares []*automdm.Software
}

func (f *fakeSoftwareIterator) Next() bool { return f.index < len(f.softwares) }

func (f *fakeSoftwareIterator) Value() (*automdm.Software, error) {
	s := f.softwares[f.index]
	f.index++
	return s, nil
}

func (f *fakeSoftwareIterator) Err() error   { return nil }
func (f *fakeSoftwareIterator) Close() error { return nil }

// analyzeRecorder captures what the analyzer wrote to the mock datastore.
type analyzeRecorder struct {
	inserted         []automdm.SoftwareVulnerability
	deletedOlderThan *time.Time
}

// analyzeTestStore returns a mock datastore that serves the given Go binaries and Go toolchain
// installs, and records what the analyzer writes.
func analyzeTestStore(binaries, toolchains []*automdm.Software) (*mock.Store, *analyzeRecorder) {
	ds := new(mock.Store)
	rec := new(analyzeRecorder)

	ds.AllSoftwareIteratorFunc = func(ctx context.Context, q automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
		if slices.Equal(q.IncludedSources, []string{goBinariesSource}) {
			return &fakeSoftwareIterator{softwares: binaries}, nil
		}
		return &fakeSoftwareIterator{softwares: toolchains}, nil
	}
	ds.InsertSoftwareVulnerabilitiesFunc = func(
		ctx context.Context, vulns []automdm.SoftwareVulnerability, source automdm.VulnerabilitySource,
	) ([]automdm.SoftwareVulnerability, error) {
		if source != automdm.GoVulnDBSource {
			return nil, fmt.Errorf("unexpected source %d", source)
		}
		rec.inserted = append(rec.inserted, vulns...)
		return vulns, nil
	}
	ds.DeleteOutOfDateVulnerabilitiesFunc = func(
		ctx context.Context, source automdm.VulnerabilitySource, olderThan time.Time,
	) error {
		if source != automdm.GoVulnDBSource {
			return fmt.Errorf("unexpected source %d", source)
		}
		rec.deletedOlderThan = &olderThan
		return nil
	}

	return ds, rec
}

func TestAnalyze(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("no artifact is not an error", func(t *testing.T) {
		ds, _ := analyzeTestStore(nil, nil)

		vulns, err := Analyze(t.Context(), ds, nil, publishedRules(t), true, time.Now(), logger)
		require.NoError(t, err)
		require.Empty(t, vulns)
		require.False(t, ds.AllSoftwareIteratorFuncInvoked)
	})

	t.Run("matches Go binaries and toolchain installs and removes stale rows", func(t *testing.T) {

		startTime := time.Now().Add(-time.Hour)
		ds, rec := analyzeTestStore([]*automdm.Software{
			{ID: 1, Name: "air", Version: "v1.48.0", ExtensionID: "github.com/air-verse/air", Release: "go1.26.1"},
			{ID: 2, Name: "air", Version: "v1.49.0", ExtensionID: "github.com/air-verse/air", Release: "go1.21.11"},
			{ID: 3, Name: "devtool", Version: "(devel)", ExtensionID: "github.com/anilyagiz/automdm/v4", Release: "go1.26.1"},
		}, []*automdm.Software{
			{ID: 10, Name: "go", Source: "homebrew_packages", Version: "1.22.4"},
		})

		vulns, err := Analyze(t.Context(), ds, testArtifact(), publishedRules(t), true, startTime, logger)
		require.NoError(t, err)

		require.ElementsMatch(t, []automdm.SoftwareVulnerability{
			{SoftwareID: 1, CVE: "CVE-2024-1000", ResolvedInVersion: new("v1.49.0")},
			{SoftwareID: 1, CVE: "CVE-2026-1002"},
			{SoftwareID: 2, CVE: "CVE-2026-1002"},
			{SoftwareID: 2, CVE: "CVE-2024-24791"},
			{SoftwareID: 10, CVE: "CVE-2024-24791", ResolvedInVersion: new("1.22.5")},
		}, rec.inserted)
		require.ElementsMatch(t, rec.inserted, vulns)

		require.NotNil(t, rec.deletedOlderThan)
		require.Equal(t, startTime, *rec.deletedOlderThan)
	})

	t.Run("collectVulns off returns nothing but still writes", func(t *testing.T) {

		ds, rec := analyzeTestStore([]*automdm.Software{
			{ID: 1, Name: "air", Version: "v1.48.0", ExtensionID: "github.com/air-verse/air"},
		}, nil)

		vulns, err := Analyze(t.Context(), ds, testArtifact(), publishedRules(t), false, time.Now(), logger)
		require.NoError(t, err)
		require.Empty(t, vulns)
		require.Len(t, rec.inserted, 2)
	})

	t.Run("stale rows are kept when the insert fails", func(t *testing.T) {

		ds, _ := analyzeTestStore([]*automdm.Software{
			{ID: 1, Name: "air", Version: "v1.48.0", ExtensionID: "github.com/air-verse/air"},
		}, nil)
		ds.InsertSoftwareVulnerabilitiesFunc = func(
			ctx context.Context, vulns []automdm.SoftwareVulnerability, source automdm.VulnerabilitySource,
		) ([]automdm.SoftwareVulnerability, error) {
			return nil, errors.New("insert failed")
		}

		_, err := Analyze(t.Context(), ds, testArtifact(), publishedRules(t), true, time.Now(), logger)
		require.ErrorContains(t, err, "insert failed")
		require.False(t, ds.DeleteOutOfDateVulnerabilitiesFuncInvoked)
	})

	// Inserted rows are already known to the next run, so dropping them here would suppress
	// their automations forever.
	t.Run("a later failure still returns what was inserted", func(t *testing.T) {

		binaries := make([]*automdm.Software, vulnBatchSize)
		for i := range binaries {
			binaries[i] = &automdm.Software{ID: uint(i + 1), Name: "tool", Version: "v1.0.0", Release: "go1.21.11"}
		}
		ds, rec := analyzeTestStore(binaries, nil)
		ds.AllSoftwareIteratorFunc = func(ctx context.Context, q automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
			if slices.Equal(q.IncludedSources, []string{goBinariesSource}) {
				return &fakeSoftwareIterator{softwares: binaries}, nil
			}
			return nil, errors.New("toolchain query failed")
		}

		vulns, err := Analyze(t.Context(), ds, testArtifact(), publishedRules(t), true, time.Now(), logger)
		require.ErrorContains(t, err, "toolchain query failed")
		require.Len(t, rec.inserted, vulnBatchSize)
		require.ElementsMatch(t, rec.inserted, vulns)
		require.False(t, ds.DeleteOutOfDateVulnerabilitiesFuncInvoked)
	})

	t.Run("no matches still removes stale rows", func(t *testing.T) {

		ds, rec := analyzeTestStore([]*automdm.Software{
			{ID: 1, Name: "gopls", Version: "v0.20.0", ExtensionID: "golang.org/x/tools/gopls", Release: "go1.26.1"},
		}, []*automdm.Software{
			{ID: 10, Name: "go", Source: "homebrew_packages", Version: "1.26.1"},
		})

		vulns, err := Analyze(t.Context(), ds, testArtifact(), publishedRules(t), true, time.Now(), logger)
		require.NoError(t, err)
		require.Empty(t, vulns)
		require.Empty(t, rec.inserted)
		require.NotNil(t, rec.deletedOlderThan)
	})

	t.Run("without rules toolchain installs are left alone", func(t *testing.T) {
		empty, err := NewToolchainRules(&ToolchainInstalls{Version: ToolchainInstallsVersion})
		require.NoError(t, err)

		for name, rules := range map[string]*ToolchainRules{"nil": nil, "empty": empty} {
			t.Run(name, func(t *testing.T) {

				ds, rec := analyzeTestStore(nil, []*automdm.Software{
					{ID: 10, Name: "go", Source: "homebrew_packages", Version: "1.21.11"},
				})
				var queries int
				iterator := ds.AllSoftwareIteratorFunc
				ds.AllSoftwareIteratorFunc = func(ctx context.Context, q automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
					queries++
					return iterator(ctx, q)
				}

				_, err := Analyze(t.Context(), ds, testArtifact(), rules, true, time.Now(), logger)
				require.NoError(t, err)
				require.Empty(t, rec.inserted)
				// No sources to include would otherwise mean iterating all software.
				require.Equal(t, 1, queries, "want only the Go binaries query")
				require.NotNil(t, rec.deletedOlderThan)
			})
		}
	})
}
