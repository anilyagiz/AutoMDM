package settings

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anilyagiz/automdm/tools/hangar/internal/paths"
)

// RepoProbe is the result of validating a candidate AutoMDM clone.
type RepoProbe struct {
	Path   string  `json:"path"`
	Valid  bool    `json:"valid"`
	Reason *string `json:"reason"`
}

// devRoots are the well-known dev-root parent dirs we scan for clones. Each
// is searched at depth 2 as well (catches <parent>/<org>/automdm layouts like
// GitHub Desktop's ~/Documents/GitHub/automdm/automdm).
var devRoots = []string{
	"~/repositories", "~/repos", "~/code", "~/Code", "~/src",
	"~/Developer", "~/Documents/GitHub", "~/Projects", "~/projects",
	"~/work", "~/dev", "~/github", "~/git",
}

func invalid(path, reason string) RepoProbe {
	return RepoProbe{Path: path, Valid: false, Reason: &reason}
}

// probe validates a single path (after tilde expansion) as an AutoMDM clone:
// it must exist, contain a go.mod, and that go.mod's module must be
// github.com/anilyagiz/automdm.
func probe(path string) RepoProbe {
	p := paths.Expand(path)
	if _, err := os.Stat(p); err != nil {
		return invalid(p, "path does not exist")
	}
	goMod := filepath.Join(p, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		return invalid(p, "no go.mod found")
	}
	contents, err := os.ReadFile(goMod)
	if err != nil {
		return invalid(p, "could not read go.mod: "+err.Error())
	}
	if !strings.Contains(string(contents), "github.com/anilyagiz/automdm") {
		return invalid(p, "go.mod module is not github.com/anilyagiz/automdm")
	}
	return RepoProbe{Path: paths.OnDisk(p), Valid: true}
}

// ProbeOne validates the given path as an AutoMDM repo.
func ProbeOne(path string) RepoProbe { return probe(path) }

// DiscoverAutoMDMRepos walks the well-known dev-root parents (plus ~/automdm)
// and returns every directory that looks like an AutoMDM clone, deduped by
// identity so a clone reached via a symlink, or via both ~/code and ~/Code
// on a case-insensitive volume, appears once.
func DiscoverAutoMDMRepos() []RepoProbe {
	home, _ := os.UserHomeDir()
	roots := make([]string, len(devRoots))
	for i, r := range devRoots {
		roots[i] = paths.Expand(r)
	}
	return discoverAutoMDMRepos(home, roots)
}

func discoverAutoMDMRepos(home string, roots []string) []RepoProbe {
	var results []RepoProbe
	var seen []os.FileInfo
	add := func(pr RepoProbe) {
		fi, err := os.Stat(pr.Path)
		if err != nil {
			return
		}
		for _, s := range seen {
			if os.SameFile(s, fi) {
				return
			}
		}
		seen = append(seen, fi)
		results = append(results, pr)
	}
	maybeAdd := func(path string) {
		if pr := probe(path); pr.Valid {
			add(pr)
		}
	}

	// ~/automdm as a one-off (we don't scan all of ~ — too noisy).
	if home != "" {
		maybeAdd(filepath.Join(home, "automdm"))
	}

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			child := filepath.Join(root, entry.Name())
			// Depth-1 hit?
			if pr := probe(child); pr.Valid {
				add(pr)
				continue // don't descend into a known repo
			}
			// Not itself a repo (no go.mod) → treat as a potential org
			// folder and scan one level deeper.
			if _, err := os.Stat(filepath.Join(child, "go.mod")); err != nil {
				grand, err := os.ReadDir(child)
				if err != nil {
					continue
				}
				for _, g := range grand {
					if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
						continue
					}
					maybeAdd(filepath.Join(child, g.Name()))
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results
}

// DetectAutoMDMConfig returns the relative name (automdm.yml / automdm.yaml) of a
// serve config in the repo root, or "" if none exists. Relative because
// serve always runs with the repo as cwd.
func DetectAutoMDMConfig(repo string) string {
	root := paths.Expand(repo)
	for _, name := range []string{"automdm.yml", "automdm.yaml"} {
		if fi, err := os.Stat(filepath.Join(root, name)); err == nil && fi.Mode().IsRegular() {
			return name
		}
	}
	return ""
}
