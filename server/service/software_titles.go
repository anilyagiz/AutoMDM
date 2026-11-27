package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

/////////////////////////////////////////////////////////////////////////////////
// List Software Titles
/////////////////////////////////////////////////////////////////////////////////

type listSoftwareTitlesRequest struct {
	automdm.SoftwareTitleListOptions
}

type listSoftwareTitlesResponse struct {
	Meta            *automdm.PaginationMetadata       `json:"meta"`
	Count           int                               `json:"count"`
	CountsUpdatedAt *time.Time                        `json:"counts_updated_at"`
	SoftwareTitles  []automdm.SoftwareTitleListResult `json:"software_titles"`
	Err             error                             `json:"error,omitempty"`
}

func (r listSoftwareTitlesResponse) Error() error { return r.Err }

func listSoftwareTitlesEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listSoftwareTitlesRequest)
	titles, count, meta, err := svc.ListSoftwareTitles(ctx, req.SoftwareTitleListOptions)
	if err != nil {
		return listSoftwareTitlesResponse{Err: err}, nil
	}

	var latest time.Time
	for _, sw := range titles {
		if sw.CountsUpdatedAt != nil && !sw.CountsUpdatedAt.IsZero() && sw.CountsUpdatedAt.After(latest) {
			latest = *sw.CountsUpdatedAt
		}
	}
	if len(titles) == 0 {
		titles = []automdm.SoftwareTitleListResult{}
	}
	listResp := listSoftwareTitlesResponse{
		SoftwareTitles: titles,
		Count:          count,
		Meta:           meta,
	}
	if !latest.IsZero() {
		listResp.CountsUpdatedAt = &latest
	}

	return listResp, nil
}

func (svc *Service) ListSoftwareTitles(
	ctx context.Context,
	opt automdm.SoftwareTitleListOptions,
) ([]automdm.SoftwareTitleListResult, int, *automdm.PaginationMetadata, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AuthzSoftwareInventory{
		TeamID: opt.TeamID,
	}, automdm.ActionRead); err != nil {
		return nil, 0, nil, err
	}

	lic, err := svc.License(ctx)
	if err != nil {
		return nil, 0, nil, ctxerr.Wrap(ctx, err, "get license")
	}

	if opt.TeamID != nil && *opt.TeamID != 0 && !lic.IsPremium() {
		return nil, 0, nil, automdm.ErrMissingLicense
	}

	if !lic.IsPremium() && (opt.MaximumCVSS > 0 || opt.MinimumCVSS > 0 || opt.KnownExploit || opt.AITool) {
		return nil, 0, nil, automdm.ErrMissingLicense
	}

	opt.TypeFilter, err = automdm.ParseSoftwareTypeFilter(opt.Source, opt.ExtensionFor)
	if err != nil {
		return nil, 0, nil, err
	}

	// always include metadata for software titles
	opt.ListOptions.IncludeMetadata = true
	// cursor-based pagination is not supported for software titles
	opt.ListOptions.After = ""

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, 0, nil, automdm.ErrNoContext
	}

	titles, count, meta, err := svc.ds.ListSoftwareTitles(ctx, opt, automdm.TeamFilter{
		User:            vc.User,
		IncludeObserver: true,
		TeamID:          opt.TeamID,
	})
	if err != nil {
		return nil, 0, nil, err
	}
	if !lic.IsPremium() {
		for i := range titles {
			titles[i].AITool = nil
		}
	}
	// Clear the deprecated software_package object from every response except setup experience
	for i := range titles {
		titles[i].SoftwarePackage = nil
	}

	return titles, count, meta, nil
}

/////////////////////////////////////////////////////////////////////////////////
// Get a Software Title
/////////////////////////////////////////////////////////////////////////////////

type getSoftwareTitleRequest struct {
	ID     uint  `url:"id"`
	TeamID *uint `query:"team_id,optional"`
}

type getSoftwareTitleResponse struct {
	SoftwareTitle *automdm.SoftwareTitle `json:"software_title,omitempty"`
	Err           error                  `json:"error,omitempty"`
}

func (r getSoftwareTitleResponse) Error() error { return r.Err }

func getSoftwareTitleEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getSoftwareTitleRequest)

	software, err := svc.SoftwareTitleByID(ctx, req.ID, req.TeamID)
	if err != nil {
		return getSoftwareTitleResponse{Err: err}, nil
	}

	return getSoftwareTitleResponse{SoftwareTitle: software}, nil
}

func (svc *Service) SoftwareTitleByID(ctx context.Context, id uint, teamID *uint) (*automdm.SoftwareTitle, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Host{TeamID: teamID}, automdm.ActionList); err != nil {
		return nil, err
	}

	if teamID != nil {
		// Verify the caller has permission for the requested scope (team or global).
		if err := svc.authz.Authorize(ctx, &automdm.AuthzSoftwareInventory{TeamID: teamID}, automdm.ActionRead); err != nil {
			return nil, err
		}
		if *teamID != 0 {
			exists, err := svc.ds.TeamExists(ctx, *teamID)
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "checking if team exists")
			} else if !exists {
				return nil, automdm.NewInvalidArgumentError("team_id", fmt.Sprintf("team %d does not exist", *teamID)).
					WithStatus(http.StatusNotFound)
			}
		}
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, automdm.ErrNoContext
	}

	// get software by id including team_id data from software_title_host_counts
	software, err := svc.ds.SoftwareTitleByID(ctx, id, teamID, automdm.TeamFilter{
		User:            vc.User,
		IncludeObserver: true,
	})
	if err != nil {
		// A title that exists only on a team outside the caller's visibility
		// must return the same NotFound as a title that doesn't exist at
		// all, so existence elsewhere can't be inferred from the response.
		return nil, ctxerr.Wrap(ctx, err, "getting software title by id")
	}

	license, err := svc.License(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "get license")
	}
	// Skip installer data when no team is given, packages, App Store apps and in-house apps are set per team
	if license.IsPremium() && teamID != nil {
		// add software installer data if needed
		if software.SoftwareInstallersCount > 0 {
			pkgs, err := svc.ds.GetSoftwarePackagesByTeamAndTitleID(ctx, teamID, id)
			if err != nil && !automdm.IsNotFound(err) {
				return nil, ctxerr.Wrap(ctx, err, "get software packages")
			}
			if len(pkgs) > 0 {
				// Display name and icon are title-level; fetch once from the first-added package.
				titleMeta, err := svc.ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, teamID, id, true)
				if err != nil && !automdm.IsNotFound(err) {
					return nil, ctxerr.Wrap(ctx, err, "get software installer metadata")
				}

				// Categories are per-package.
				installerIDs := make([]uint, len(pkgs))
				for i, pkg := range pkgs {
					installerIDs[i] = pkg.InstallerID
				}
				categoriesByInstaller, err := svc.ds.GetCategoriesForSoftwareInstallers(ctx, installerIDs)
				if err != nil {
					return nil, ctxerr.Wrap(ctx, err, "get categories for software packages")
				}

				// Key policies by installer_id so each package on a multi-package
				// title only surfaces the ones actually bound to it. VPP-backed
				// policies have nil InstallerID and carry a VPPAppsTeamsID instead.
				policiesByInstaller := make(map[uint][]automdm.AutomaticInstallPolicy)
				if titleMeta != nil {
					for _, p := range titleMeta.AutomaticInstallPolicies {
						if p.InstallerID == nil {
							continue
						}
						policiesByInstaller[*p.InstallerID] = append(policiesByInstaller[*p.InstallerID], p)
					}
				}

				for _, pkg := range pkgs {
					summary, err := svc.ds.GetSummaryHostSoftwareInstalls(ctx, pkg.InstallerID)
					if err != nil {
						return nil, ctxerr.Wrap(ctx, err, "get software installer status summary")
					}
					pkg.Status = summary
					pkg.Categories = categoriesByInstaller[pkg.InstallerID]

					if titleMeta != nil {
						pkg.DisplayName = titleMeta.DisplayName
						pkg.IconUrl = titleMeta.IconUrl
					}
					pkg.AutomaticInstallPolicies = policiesByInstaller[pkg.InstallerID]

					// Populate AutoMDMMaintainedVersions/pin/patch policy for FMA titles.
					// An FMA title has a single active package, so this runs on it.
					if pkg.AutoMDMMaintainedAppID != nil {
						fmaVersions, err := svc.ds.GetAutoMDMMaintainedVersionsByTitleID(ctx, teamID, id)
						if err != nil {
							return nil, ctxerr.Wrap(ctx, err, "get automdm maintained versions")
						}
						pkg.AutoMDMMaintainedVersions = fmaVersions

						// No pin row means the title tracks "Latest" (nil pinned_version); any other error is real.
						pinnedVersion, err := svc.ds.GetPinnedVersion(ctx, teamID, id)
						if err != nil && !errors.Is(err, sql.ErrNoRows) {
							return nil, ctxerr.Wrap(ctx, err, "get pinned version")
						}
						pkg.PinnedVersion = pinnedVersion

						patchPolicy, err := svc.ds.GetPatchPolicy(ctx, teamID, id)
						if err != nil && !automdm.IsNotFound(err) {
							return nil, ctxerr.Wrap(ctx, err, "get patch policy")
						}
						pkg.PatchPolicy = patchPolicy

						// While patch_when_closed, notify_before_patching, or patch_deadline is on, the
						// pre-install query is AutoMDM's managed app open query, shown read-only.
						if patchPolicy != nil && (patchPolicy.PatchWhenClosed || patchPolicy.NotifyBeforePatching || patchPolicy.PatchDeadline) {
							pkg.PreInstallQuery = pkg.AppOpenQuery
						}
					}
				}

				software.Packages = make([]automdm.SoftwareInstaller, len(pkgs))
				for i, pkg := range pkgs {
					software.Packages[i] = *pkg
				}
			}
		}

		// add VPP app data if needed
		if software.VPPAppsCount > 0 {
			versions, err := svc.ds.GetAppStoreAppVersionsByTeamAndTitleID(ctx, *teamID, id)
			if err != nil && !automdm.IsNotFound(err) {
				return nil, ctxerr.Wrap(ctx, err, "get VPP app metadata")
			}
			summaries, err := svc.ds.GetSummaryHostVPPAppInstalls(ctx, versions)
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "get VPP app status summary")
			}
			vppTokensByID := make(map[uint]*automdm.VPPTokenSummary)
			for _, meta := range versions {
				meta.Status = summaries[meta.VPPAppsTeamsID]

				if meta.VPPTokenID != nil {
					if _, ok := vppTokensByID[*meta.VPPTokenID]; !ok {
						var tok *automdm.VPPTokenDB
						tok, err = svc.ds.GetVPPToken(ctx, *meta.VPPTokenID)
						if err != nil && !automdm.IsNotFound(err) {
							return nil, ctxerr.Wrap(ctx, err, "get vpp token for app store app")
						}
						if tok != nil {
							vppTokensByID[*meta.VPPTokenID] = &automdm.VPPTokenSummary{ID: tok.ID, OrgName: tok.OrgName, Location: tok.Location, CountryCode: tok.CountryCode}
						}
					}
					meta.VPPToken = vppTokensByID[*meta.VPPTokenID]
				}

				// Wrap iOS / iPadOS plist as a JSON string for the response.
				if len(meta.Configuration) > 0 {
					switch meta.Platform {
					case automdm.IOSPlatform, automdm.IPadOSPlatform:
						wrapped, err := json.Marshal(string(meta.Configuration))
						if err != nil {
							return nil, ctxerr.Wrap(ctx, err, "wrapping VPP configuration for response")
						}
						meta.Configuration = wrapped
					}
				}
			}
			software.AppStoreApps = versions
		}

		// add in house app data if needed
		if software.InHouseAppCount > 0 {
			meta, err := svc.ds.GetInHouseAppMetadataByTeamAndTitleID(ctx, teamID, id)
			if err != nil && !automdm.IsNotFound(err) {
				return nil, ctxerr.Wrap(ctx, err, "get in house app metadata")
			}
			if meta != nil {
				summary, err := svc.ds.GetSummaryHostInHouseAppInstalls(ctx, teamID, meta.InstallerID)
				if err != nil {
					return nil, ctxerr.Wrap(ctx, err, "get in house app status summary")
				}
				meta.Status = &automdm.SoftwareInstallerStatusSummary{
					Installed:      summary.Installed,
					PendingInstall: summary.Pending,
					FailedInstall:  summary.Failed,
				}

				// Wrap iOS / iPadOS plist as a JSON string for the response.
				if len(meta.Configuration) > 0 {
					wrapped, err := json.Marshal(string(meta.Configuration))
					if err != nil {
						return nil, ctxerr.Wrap(ctx, err, "wrapping in-house app configuration for response")
					}
					meta.Configuration = wrapped
				}
				software.Packages = []automdm.SoftwareInstaller{*meta}
			}
		}
	}

	svc.filterInstallerDetailsForUser(ctx, teamID, software)
	if !license.IsPremium() {
		software.AITool = nil
	}

	return software, nil
}

// filterInstallerDetailsForUser clears the installer fields governed by
// installable_entity read. This response embeds the full installer, so without
// it a caller holding only software_inventory read would see script bodies and
// managed app configuration that every other installer route withholds.
func (svc *Service) filterInstallerDetailsForUser(ctx context.Context, teamID *uint, title *automdm.SoftwareTitle) {
	if title == nil {
		return
	}
	if err := svc.authz.Authorize(ctx, &automdm.SoftwareInstaller{TeamID: teamID}, automdm.ActionRead); err == nil {
		return
	}

	filterInstaller := func(installer *automdm.SoftwareInstaller) {
		installer.InstallScript = ""
		installer.UninstallScript = ""
		installer.PostInstallScript = ""
		installer.PreInstallQuery = ""
		installer.Configuration = nil
	}

	for i := range title.Packages {
		filterInstaller(&title.Packages[i])
	}
	for i := range title.AppStoreApps {
		title.AppStoreApps[i].Configuration = nil
	}
}

func (svc *Service) SoftwareTitleNameForHostFilter(ctx context.Context, id uint, teamID *uint) (name, displayName string, err error) {
	// Intentionally skip team-scoped inventory auth: only minimal title name.
	if err := svc.authz.Authorize(ctx, &automdm.Host{TeamID: teamID}, automdm.ActionList); err != nil {
		return "", "", err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return "", "", automdm.ErrNoContext
	}

	return svc.ds.SoftwareTitleNameForHostFilter(ctx, id, teamID, automdm.TeamFilter{
		User:            vc.User,
		IncludeObserver: true,
	})
}

/////////////////////////////////////////////////////////////////////////////////
// Update a software title's name
/////////////////////////////////////////////////////////////////////////////////

type updateSoftwareNameRequest struct {
	ID   uint   `url:"id"`
	Name string `json:"name"`
}

type updateSoftwareNameResponse struct {
	Err error `json:"error,omitempty"`
}

func (r updateSoftwareNameResponse) Error() error { return r.Err }
func (r updateSoftwareNameResponse) Status() int  { return http.StatusResetContent }

func updateSoftwareNameEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*updateSoftwareNameRequest)
	return updateSoftwareNameResponse{Err: svc.UpdateSoftwareName(ctx, req.ID, req.Name)}, nil
}

func (svc *Service) UpdateSoftwareName(ctx context.Context, titleID uint, name string) error {
	if err := svc.authz.Authorize(ctx, &automdm.AuthzSoftwareInventory{}, automdm.ActionWrite); err != nil {
		return err
	}
	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return automdm.ErrNoContext
	}

	// get software by id including team_id data from software_title_host_counts
	software, err := svc.ds.SoftwareTitleByID(ctx, titleID, nil, automdm.TeamFilter{
		User: vc.User,
	})
	if err != nil {
		return ctxerr.Wrap(ctx, err, "getting software title by id")
	}
	if software.BundleIdentifier == nil || *software.BundleIdentifier == "" {
		return automdm.NewInvalidArgumentError("id", "only titles with a bundle ID can have their name modified")
	}
	if name == "" {
		return automdm.NewInvalidArgumentError("name", "cannot be empty")
	}

	return svc.ds.UpdateSoftwareTitleName(ctx, titleID, name)
}
