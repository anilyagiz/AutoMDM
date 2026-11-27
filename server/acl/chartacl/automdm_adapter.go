// Package chartacl provides the anti-corruption layer between the chart
// bounded context and legacy AutoMDM code.
//
// This package is the ONLY place that imports both chart types and team /
// viewer-context types. It translates between them, letting the chart
// context stay free of direct server/automdm or server/contexts/viewer imports
// (which the arch_test enforces).
package chartacl

import (
	"context"
	"errors"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/chart/api"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

// AutoMDMViewerAdapter resolves the current authenticated viewer into the
// minimal information the chart service needs to decide team scope. It has
// no state and no AutoMDM-service dependency — the viewer context is the only
// input it needs.
type AutoMDMViewerAdapter struct{}

// NewTeamViewerAdapter returns an adapter suitable for passing to
// chart/bootstrap.New.
func NewTeamViewerAdapter() *AutoMDMViewerAdapter {
	return &AutoMDMViewerAdapter{}
}

// Ensure AutoMDMViewerAdapter implements api.ViewerProvider.
var _ api.ViewerProvider = (*AutoMDMViewerAdapter)(nil)

// ViewerScope reads the user from the viewer context and reports whether they
// have global access (isGlobal) or, otherwise, the list of team IDs they
// belong to. Returns an error when no viewer is in context — chart endpoints
// sit behind authenticated middleware, so the absence of a viewer means the
// request never went through auth and we refuse to serve data.
func (a *AutoMDMViewerAdapter) ViewerScope(ctx context.Context) (bool, []uint, error) {
	vc, ok := viewer.FromContext(ctx)
	if !ok || vc.User == nil {
		return false, nil, errors.New("chart: no authenticated viewer in context")
	}
	u := vc.User
	if u.HasAnyGlobalRole() {
		return true, nil, nil
	}
	ids := make([]uint, 0, len(u.Teams))
	for _, t := range u.Teams {
		ids = append(ids, t.ID)
	}
	return false, ids, nil
}

// Ensure ExpandPlatform satisfies api.PlatformExpanderFn.
var _ api.PlatformExpanderFn = ExpandPlatform

// ExpandPlatform adapts automdm.ExpandPlatform for the chart service so that
// a "linux" platform filter matches the distro names stored in hosts.platform.
func ExpandPlatform(platform string) []string {
	return automdm.ExpandPlatform(platform)
}
