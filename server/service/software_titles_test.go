package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestServiceSoftwareTitlesAuth(t *testing.T) {
	ds := new(mock.Store)

	ds.ListSoftwareTitlesFunc = func(ctx context.Context, opt automdm.SoftwareTitleListOptions, tmf automdm.TeamFilter) ([]automdm.SoftwareTitleListResult, int, *automdm.PaginationMetadata, error) {
		return []automdm.SoftwareTitleListResult{}, 0, &automdm.PaginationMetadata{}, nil
	}
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return &automdm.SoftwareTitle{}, nil
	}
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) { return true, nil }
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return &automdm.SoftwareTitle{BundleIdentifier: ptr.String("foo")}, nil
	}
	ds.UpdateSoftwareTitleNameFunc = func(ctx context.Context, id uint, name string) error {
		return nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)

	for _, tc := range []struct {
		name                 string
		user                 *automdm.User
		shouldFailGlobalRead bool
		shouldFailTeamRead   bool
		shouldFailGetByID    bool
		shouldFailWrite      bool
	}{
		{
			name: "global-admin",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleAdmin),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      false,
		},
		{
			name: "global-maintainer",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleMaintainer),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      true,
		},
		{
			name: "global-observer",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleObserver),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      true,
		},
		{
			name: "team-admin-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleAdmin,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      true,
		},
		{
			name: "team-maintainer-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleMaintainer,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      true,
		},
		{
			name: "team-observer-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleObserver,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    false,
			shouldFailWrite:      true,
		},
		{
			name: "team-admin-does-not-belong-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 2},
					Role: automdm.RoleAdmin,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   true,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
		{
			name: "team-maintainer-does-not-belong-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 2},
					Role: automdm.RoleMaintainer,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   true,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
		{
			name: "team-observer-does-not-belong-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 2},
					Role: automdm.RoleObserver,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   true,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
		{
			// GitOps can list software titles but cannot fetch a single title
			// because SoftwareTitleByID also requires Host:list permission.
			name: "global-gitops",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleGitOps),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
		{
			name: "team-gitops-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleGitOps,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
		{
			name: "team-gitops-does-not-belong-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 2},
					Role: automdm.RoleGitOps,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   true,
			shouldFailGetByID:    true,
			shouldFailWrite:      true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tc.user})
			premiumCtx := license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})

			// List all software titles.
			_, _, _, err := svc.ListSoftwareTitles(ctx, automdm.SoftwareTitleListOptions{})
			checkAuthErr(t, tc.shouldFailGlobalRead, err)

			// List software for a team.
			_, _, _, err = svc.ListSoftwareTitles(premiumCtx, automdm.SoftwareTitleListOptions{
				TeamID: ptr.Uint(1),
			})
			checkAuthErr(t, tc.shouldFailTeamRead, err)

			// List software for a team should fail no matter what
			// with a non-premium context
			if !tc.shouldFailTeamRead {
				_, _, _, err = svc.ListSoftwareTitles(ctx, automdm.SoftwareTitleListOptions{
					TeamID: ptr.Uint(1),
				})
				require.ErrorContains(t, err, "Requires AutoMDM Premium license")
			}

			// Get a software title for a team
			_, err = svc.SoftwareTitleByID(ctx, 1, ptr.Uint(1))
			checkAuthErr(t, tc.shouldFailGetByID, err)

			// Update a software title's name
			err = svc.UpdateSoftwareName(ctx, 1, "2 Chrome 2 Furious")
			checkAuthErr(t, tc.shouldFailWrite, err)
		})
	}
}

// TestSoftwareTitleByIDInstallerDetails covers the two authorization rules the
// title detail response depends on: the "no team" scope is authorized even when
// no team is given, and script contents and managed app configuration reach
// only callers who can read the installer, not merely software inventory.
func TestSoftwareTitleByIDInstallerDetails(t *testing.T) {
	const (
		installScript     = "echo install"
		uninstallScript   = "echo uninstall"
		postInstallScript = "echo post-install"
		preInstallQuery   = "SELECT 1;"
	)
	appConfiguration := json.RawMessage(`{"key":"value"}`)

	ds := new(mock.Store)
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) { return true, nil }
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return &automdm.SoftwareTitle{
			ID:                      id,
			Name:                    "Foo",
			SoftwareInstallersCount: 1,
			VPPAppsCount:            1,
		}, nil
	}
	ds.GetSoftwarePackagesByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint) ([]*automdm.SoftwareInstaller, error) {
		return []*automdm.SoftwareInstaller{{
			InstallerID:       1,
			TitleID:           &titleID,
			Name:              "foo.pkg",
			Version:           "1.0",
			Platform:          "darwin",
			StorageID:         "abc123",
			SelfService:       true,
			InstallScript:     installScript,
			UninstallScript:   uninstallScript,
			PostInstallScript: postInstallScript,
			PreInstallQuery:   preInstallQuery,
			Configuration:     appConfiguration,
		}}, nil
	}
	ds.GetSoftwareInstallerMetadataByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint, withScriptContents bool) (*automdm.SoftwareInstaller, error) {
		return &automdm.SoftwareInstaller{InstallerID: 1, DisplayName: "Foo"}, nil
	}
	ds.GetCategoriesForSoftwareInstallersFunc = func(ctx context.Context, installerIDs []uint) (map[uint][]string, error) {
		return nil, nil
	}
	ds.GetSummaryHostSoftwareInstallsFunc = func(ctx context.Context, installerID uint) (*automdm.SoftwareInstallerStatusSummary, error) {
		return &automdm.SoftwareInstallerStatusSummary{}, nil
	}
	ds.GetVPPAppMetadataByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint) (*automdm.VPPAppStoreApp, error) {
		return &automdm.VPPAppStoreApp{Name: "Bar", Configuration: appConfiguration}, nil
	}
	ds.GetAppStoreAppVersionsByTeamAndTitleIDFunc = func(ctx context.Context, teamID uint, titleID uint) ([]*automdm.VPPAppStoreApp, error) {
		return []*automdm.VPPAppStoreApp{{Name: "Bar", Configuration: appConfiguration}}, nil
	}
	ds.GetSummaryHostVPPAppInstallsFunc = func(ctx context.Context, versions []*automdm.VPPAppStoreApp) (map[uint]*automdm.VPPAppStatusSummary, error) {
		return map[uint]*automdm.VPPAppStatusSummary{0: {}}, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)

	for _, tc := range []struct {
		name string
		user *automdm.User
		// canReadInstaller is whether the role holds installable_entity read.
		canReadInstaller bool
		// installersWithoutTeam is whether the role can read the "no team" scope.
		installersWithoutTeam bool
	}{
		{
			name:                  "global-admin",
			user:                  &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)},
			canReadInstaller:      true,
			installersWithoutTeam: true,
		},
		{
			name:                  "global-maintainer",
			user:                  &automdm.User{ID: 1, GlobalRole: new(automdm.RoleMaintainer)},
			canReadInstaller:      true,
			installersWithoutTeam: true,
		},
		{
			// Technician is the role that separates an installable_entity check
			// from a hardcoded admin/maintainer list: it reads installers but is
			// neither.
			name:                  "global-technician",
			user:                  &automdm.User{ID: 1, GlobalRole: new(automdm.RoleTechnician)},
			canReadInstaller:      true,
			installersWithoutTeam: true,
		},
		{
			name:                  "global-observer",
			user:                  &automdm.User{ID: 1, GlobalRole: new(automdm.RoleObserver)},
			canReadInstaller:      false,
			installersWithoutTeam: true,
		},
		{
			name:                  "global-observer-plus",
			user:                  &automdm.User{ID: 1, GlobalRole: new(automdm.RoleObserverPlus)},
			canReadInstaller:      false,
			installersWithoutTeam: true,
		},
		{
			name: "team-admin",
			user: &automdm.User{ID: 1, Teams: []automdm.UserTeam{{
				Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin,
			}}},
			canReadInstaller:      true,
			installersWithoutTeam: false,
		},
		{
			name: "team-maintainer",
			user: &automdm.User{ID: 1, Teams: []automdm.UserTeam{{
				Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer,
			}}},
			canReadInstaller:      true,
			installersWithoutTeam: false,
		},
		{
			name: "team-technician",
			user: &automdm.User{ID: 1, Teams: []automdm.UserTeam{{
				Team: automdm.Team{ID: 1}, Role: automdm.RoleTechnician,
			}}},
			canReadInstaller:      true,
			installersWithoutTeam: false,
		},
		{
			name: "team-observer",
			user: &automdm.User{ID: 1, Teams: []automdm.UserTeam{{
				Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver,
			}}},
			canReadInstaller:      false,
			installersWithoutTeam: false,
		},
		{
			name: "team-observer-plus",
			user: &automdm.User{ID: 1, Teams: []automdm.UserTeam{{
				Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus,
			}}},
			canReadInstaller:      false,
			installersWithoutTeam: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tc.user})
			ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})

			title, err := svc.SoftwareTitleByID(ctx, 1, new(uint(1)))
			require.NoError(t, err)
			require.Len(t, title.Packages, 1)
			require.Len(t, title.AppStoreApps, 1)

			pkg := &title.Packages[0]
			if tc.canReadInstaller {
				require.Equal(t, installScript, pkg.InstallScript)
				require.Equal(t, uninstallScript, pkg.UninstallScript)
				require.Equal(t, postInstallScript, pkg.PostInstallScript)
				require.Equal(t, preInstallQuery, pkg.PreInstallQuery)
				require.NotEmpty(t, pkg.Configuration)
			} else {
				require.Empty(t, pkg.InstallScript)
				require.Empty(t, pkg.UninstallScript)
				require.Empty(t, pkg.PostInstallScript)
				require.Empty(t, pkg.PreInstallQuery)
				require.Empty(t, pkg.Configuration)
			}

			// Filtering the whole package would be a regression.
			require.Equal(t, "foo.pkg", pkg.Name)
			require.Equal(t, "1.0", pkg.Version)
			require.Equal(t, "darwin", pkg.Platform)
			require.Equal(t, "abc123", pkg.StorageID)
			require.True(t, pkg.SelfService)
			require.NotNil(t, pkg.Status)

			if tc.canReadInstaller {
				require.NotEmpty(t, title.AppStoreApps[0].Configuration)
			} else {
				require.Empty(t, title.AppStoreApps[0].Configuration)
			}
			require.Equal(t, "Bar", title.AppStoreApps[0].Name)

			// get the title without a team, the request should succeed without any installer data
			ds.TeamExistsFuncInvoked = false
			noTeamTitle, err := svc.SoftwareTitleByID(ctx, 1, nil)
			require.NoError(t, err)
			require.False(t, ds.TeamExistsFuncInvoked)
			require.Empty(t, noTeamTitle.Packages)
			require.Nil(t, noTeamTitle.AppStoreApps)

			// An explicit "no team" is authorized up front, so it still fails.
			_, err = svc.SoftwareTitleByID(ctx, 1, new(uint(0)))
			checkAuthErr(t, !tc.installersWithoutTeam, err)
			require.False(t, ds.TeamExistsFuncInvoked)
		})
	}
}

func TestSoftwareNameUpdate(t *testing.T) {
	ds := new(mock.Store)
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return nil, &notFoundError{}
	}

	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}})

	// Title not found
	err := svc.UpdateSoftwareName(ctx, 1, "2 Chrome 2 Furious")
	require.ErrorContains(t, err, "not found")
	require.False(t, ds.UpdateHostSoftwareFuncInvoked)

	// Title found but doesn't have a bundle ID
	title := &automdm.SoftwareTitle{}
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return title, nil
	}
	err = svc.UpdateSoftwareName(ctx, 1, "2 Chrome 2 Furious")
	require.ErrorContains(t, err, "bundle")
	require.False(t, ds.UpdateHostSoftwareFuncInvoked)

	// Title found with bundle ID but user didn't provide a name
	title = &automdm.SoftwareTitle{BundleIdentifier: ptr.String("foo")}
	err = svc.UpdateSoftwareName(ctx, 1, "")
	require.ErrorContains(t, err, "name")
	require.False(t, ds.UpdateHostSoftwareFuncInvoked)

	// Success case
	ds.UpdateSoftwareTitleNameFunc = func(ctx context.Context, id uint, name string) error {
		return nil
	}
	err = svc.UpdateSoftwareName(ctx, 1, "2 Chrome 2 Furious")
	require.NoError(t, err)
	require.True(t, ds.UpdateSoftwareTitleNameFuncInvoked)
}

func TestSoftwareTitleByIDTeamIDZero(t *testing.T) {
	ds := new(mock.Store)
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return &automdm.SoftwareTitle{BundleIdentifier: new("com.example.app")}, nil
	}
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) { return true, nil }

	svc, ctx := newTestService(t, ds, nil, nil)

	teamIDZero := new(uint) // *uint pointing to 0

	// Team-scoped user on team 1 should not be able to access software with team_id=0
	teamUser := &automdm.User{
		ID: 1,
		Teams: []automdm.UserTeam{{
			Team: automdm.Team{ID: 1},
			Role: automdm.RoleAdmin,
		}},
	}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: teamUser})

	_, err := svc.SoftwareTitleByID(ctx, 1, teamIDZero)
	checkAuthErr(t, true, err)

	// Global admin should still be able to access software with team_id=0
	globalAdmin := &automdm.User{
		ID:         2,
		GlobalRole: new(automdm.RoleAdmin),
	}
	adminCtx := viewer.NewContext(ctx, viewer.Viewer{User: globalAdmin})

	_, err = svc.SoftwareTitleByID(adminCtx, 1, teamIDZero)
	checkAuthErr(t, false, err)
}

func TestListSoftwareTitlesOmitSoftwarePackage(t *testing.T) {
	ds := new(mock.Store)
	ds.ListSoftwareTitlesFunc = func(ctx context.Context, opt automdm.SoftwareTitleListOptions, tmf automdm.TeamFilter) ([]automdm.SoftwareTitleListResult, int, *automdm.PaginationMetadata, error) {
		return []automdm.SoftwareTitleListResult{
			{
				ID:              1,
				Name:            "Custom package",
				SoftwarePackage: &automdm.SoftwarePackageOrApp{Name: "custom.pkg"},
				Packages:        []automdm.SoftwarePackageListItem{{InstallerID: 10, Name: "custom.pkg"}},
			},
			{
				ID:              2,
				Name:            "AutoMDM-maintained app",
				SoftwarePackage: &automdm.SoftwarePackageOrApp{Name: "fma.pkg", AutoMDMMaintainedAppID: new(uint(5))},
				Packages:        []automdm.SoftwarePackageListItem{{InstallerID: 11, Name: "fma.pkg", AutoMDMMaintainedAppID: new(uint(5))}},
			},
			{
				ID:              3,
				Name:            "Script package",
				SoftwarePackage: &automdm.SoftwarePackageOrApp{Name: "script.sh"},
				Packages:        []automdm.SoftwarePackageListItem{{InstallerID: 12, Name: "script.sh"}},
			},
			{
				ID:              4,
				Name:            "In-house app",
				SoftwarePackage: &automdm.SoftwarePackageOrApp{Name: "app.ipa"},
				Packages:        []automdm.SoftwarePackageListItem{{InstallerID: 13, Name: "app.ipa"}},
			},
			{
				ID:          5,
				Name:        "App Store app",
				AppStoreApp: &automdm.SoftwarePackageOrApp{AppStoreID: "12345"},
			},
			{
				ID:   6,
				Name: "No package",
			},
		}, 6, &automdm.PaginationMetadata{}, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})

	// list the software titles, no title should have software_package in its JSON
	titles, count, _, err := svc.ListSoftwareTitles(ctx, automdm.SoftwareTitleListOptions{TeamID: new(uint(1))})
	require.NoError(t, err)
	require.Equal(t, 6, count)
	require.Len(t, titles, 6)
	for _, title := range titles {
		titleJSON, err := json.Marshal(&title)
		require.NoError(t, err)
		var titleFields map[string]json.RawMessage
		err = json.Unmarshal(titleJSON, &titleFields)
		require.NoError(t, err)
		require.NotContains(t, titleFields, "software_package", title.Name)
		require.Contains(t, titleFields, "packages", title.Name)
	}

	// check the packages of the custom package and AutoMDM-maintained app titles, they should be unchanged
	require.Equal(t, []automdm.SoftwarePackageListItem{{InstallerID: 10, Name: "custom.pkg"}}, titles[0].Packages)
	require.Equal(t, []automdm.SoftwarePackageListItem{{InstallerID: 11, Name: "fma.pkg", AutoMDMMaintainedAppID: new(uint(5))}}, titles[1].Packages)
}

func TestSoftwareTitleByIDNilTeamIDExistsElsewhere(t *testing.T) {
	ds := new(mock.Store)
	var filtersUsed []automdm.TeamFilter
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		filtersUsed = append(filtersUsed, tmFilter)
		return nil, newNotFoundError()
	}

	svc, ctx := newTestService(t, ds, nil, nil)
	teamUser := &automdm.User{
		ID:    1,
		Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}},
	}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: teamUser})

	_, err := svc.SoftwareTitleByID(ctx, 1, nil)
	require.Error(t, err)
	// A title's existence on a team the caller can't see must not be
	// distinguishable (via status code) from it not existing at all.
	require.True(t, automdm.IsNotFound(err), "expected NotFound, got: %v", err)

	// Guard against reintroducing a secondary lookup: exactly one call, and
	// never with a global-role filter standing in for the real caller.
	require.Len(t, filtersUsed, 1)
	require.Equal(t, teamUser, filtersUsed[0].User)
}

func TestSoftwareTitleByIDVPPToken(t *testing.T) {
	ds := new(mock.Store)
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) { return true, nil }
	ds.SoftwareTitleByIDFunc = func(ctx context.Context, id uint, teamID *uint, tmFilter automdm.TeamFilter) (*automdm.SoftwareTitle, error) {
		return &automdm.SoftwareTitle{ID: id, Name: "Foo", VPPAppsCount: 1}, nil
	}
	ds.GetSummaryHostVPPAppInstallsFunc = func(ctx context.Context, versions []*automdm.VPPAppStoreApp) (map[uint]*automdm.VPPAppStatusSummary, error) {
		summaries := make(map[uint]*automdm.VPPAppStatusSummary, len(versions))
		for _, v := range versions {
			summaries[v.VPPAppsTeamsID] = &automdm.VPPAppStatusSummary{}
		}
		return summaries, nil
	}
	var versionTokenID *uint
	var secondVersionTokenID *uint
	ds.GetAppStoreAppVersionsByTeamAndTitleIDFunc = func(ctx context.Context, teamID uint, titleID uint) ([]*automdm.VPPAppStoreApp, error) {
		versions := []*automdm.VPPAppStoreApp{{Name: "Bar", VPPTokenID: versionTokenID}}
		if secondVersionTokenID != nil {
			versions = append(versions, &automdm.VPPAppStoreApp{Name: "Bar", VersionName: "Beta", VPPAppsTeamsID: 2, VPPTokenID: secondVersionTokenID})
		}
		return versions, nil
	}
	ds.GetVPPTokenFunc = func(ctx context.Context, tokenID uint) (*automdm.VPPTokenDB, error) {
		if tokenID != 2 {
			return nil, &notFoundError{}
		}
		return &automdm.VPPTokenDB{ID: 2, OrgName: "Acme Org", Location: "Acme Japan", CountryCode: "jp"}, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("team with a token", func(t *testing.T) {
		versionTokenID = new(uint(2))
		title, err := svc.SoftwareTitleByID(ctx, 1, new(uint(1)))
		require.NoError(t, err)
		require.Len(t, title.AppStoreApps, 1)
		require.Equal(t, &automdm.VPPTokenSummary{ID: 2, OrgName: "Acme Org", Location: "Acme Japan", CountryCode: "jp"}, title.AppStoreApps[0].VPPToken)
	})

	t.Run("app without a token", func(t *testing.T) {
		versionTokenID = nil
		title, err := svc.SoftwareTitleByID(ctx, 1, new(uint(1)))
		require.NoError(t, err)
		require.Len(t, title.AppStoreApps, 1)
		require.Nil(t, title.AppStoreApps[0].VPPToken)
	})

	t.Run("deleted token", func(t *testing.T) {
		versionTokenID = new(uint(3))
		title, err := svc.SoftwareTitleByID(ctx, 1, new(uint(1)))
		require.NoError(t, err)
		require.Len(t, title.AppStoreApps, 1)
		require.Nil(t, title.AppStoreApps[0].VPPToken)
	})

	t.Run("no team", func(t *testing.T) {
		versionTokenID = new(uint(2))
		ds.GetVPPTokenFuncInvoked = false
		title, err := svc.SoftwareTitleByID(ctx, 1, nil)
		require.NoError(t, err)
		require.Nil(t, title.AppStoreApps)
		require.False(t, ds.GetVPPTokenFuncInvoked)
	})

	t.Run("first version without a token and second version with a token", func(t *testing.T) {
		// read a title whose first version has no token and second version has token 2, only the second version should have a token
		versionTokenID = nil
		secondVersionTokenID = new(uint(2))
		title, err := svc.SoftwareTitleByID(ctx, 1, new(uint(1)))
		require.NoError(t, err)
		require.Len(t, title.AppStoreApps, 2)
		require.Nil(t, title.AppStoreApps[0].VPPToken)
		require.Equal(t, &automdm.VPPTokenSummary{ID: 2, OrgName: "Acme Org", Location: "Acme Japan", CountryCode: "jp"}, title.AppStoreApps[1].VPPToken)
	})
}
