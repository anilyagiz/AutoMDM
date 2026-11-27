package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mock"
	svcmock "github.com/anilyagiz/automdm/v4/server/mock/service"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
)

func TestBatchAssociateVPPApps(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc := newTestService(t, ds)

	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	t.Run("Fails if missing VPP token when payloads to associate", func(t *testing.T) {
		ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
			return nil, nil
		}
		ds.GetAssignedVPPAppsFunc = func(ctx context.Context, teamID *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
			return nil, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		t.Run("dry run", func(t *testing.T) {
			_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
				{
					AppStoreID:       "my-fake-app",
					LabelsExcludeAny: []string{},
					LabelsIncludeAny: []string{},
					LabelsIncludeAll: []string{},
					Categories:       []string{},
					Platform:         automdm.MacOSPlatform,
				},
			}, true)
			require.ErrorContains(t, err, "No available VPP Token")
		})
		t.Run("not dry run", func(t *testing.T) {
			_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
				{
					AppStoreID:       "my-fake-app",
					LabelsExcludeAny: []string{},
					LabelsIncludeAny: []string{},
					LabelsIncludeAll: []string{},
					Categories:       []string{},
					Platform:         automdm.MacOSPlatform,
				},
			}, false)
			require.ErrorContains(t, err, "No available VPP Token")
		})
	})

	t.Run("Rejects malformed custom host vital reference in Android app configuration", func(t *testing.T) {
		ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
			return nil, nil
		}
		_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
			{
				AppStoreID:       "com.example.app",
				LabelsExcludeAny: []string{},
				LabelsIncludeAny: []string{},
				LabelsIncludeAll: []string{},
				Categories:       []string{},
				Platform:         automdm.AndroidPlatform,
				Configuration:    json.RawMessage(`{"managedConfiguration": {"assetTag": "$AUTOMDM_HOST_VITAL_asset_tag"}}`),
			},
		}, true)
		var badReqErr *automdm.BadRequestError
		require.ErrorAs(t, err, &badReqErr)
		require.ErrorContains(t, err, "Invalid custom host vital reference")
	})

	t.Run("Rejects Android app configuration referencing an unknown custom host vital", func(t *testing.T) {
		ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
			return nil, nil
		}
		ds.ValidateReferencedCustomHostVitalsFunc = func(ctx context.Context, documents []string) error {
			return &automdm.MissingCustomHostVitalsError{MissingIDs: []uint{9}}
		}
		_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
			{
				AppStoreID:       "com.example.app",
				LabelsExcludeAny: []string{},
				LabelsIncludeAny: []string{},
				LabelsIncludeAll: []string{},
				Categories:       []string{},
				Platform:         automdm.AndroidPlatform,
				Configuration:    json.RawMessage(`{"managedConfiguration": {"assetTag": "$AUTOMDM_HOST_VITAL_9"}}`),
			},
		}, true)
		var invalidArgErr *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &invalidArgErr)
		require.ErrorContains(t, err, "is not defined")
	})

	t.Run("Android app configuration: infrastructure failure propagates instead of being reported as invalid input", func(t *testing.T) {
		ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
			return nil, nil
		}
		ds.ValidateReferencedCustomHostVitalsFunc = func(ctx context.Context, documents []string) error {
			return ctxerr.Wrap(ctx, errors.New("connection refused"), "validating custom host vitals")
		}
		_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
			{
				AppStoreID:       "com.example.app",
				LabelsExcludeAny: []string{},
				LabelsIncludeAny: []string{},
				LabelsIncludeAll: []string{},
				Categories:       []string{},
				Platform:         automdm.AndroidPlatform,
				Configuration:    json.RawMessage(`{"managedConfiguration": {"assetTag": "$AUTOMDM_HOST_VITAL_9"}}`),
			},
		}, true)
		require.Error(t, err)
		require.ErrorContains(t, err, "connection refused")
		var invalidArgErr2 *automdm.InvalidArgumentError
		require.NotErrorAs(t, err, &invalidArgErr2, "an infrastructure failure must not be reported as invalid input (422)")
	})

	t.Run("Fails for AutoMDM Agent Android apps via GitOps", func(t *testing.T) {
		ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
			return nil, nil
		}

		automdmAgentPackages := []string{
			"com.fleetdm.agent",
			"com.fleetdm.agent.pingali",
			"com.fleetdm.agent.private.testuser",
		}

		for _, pkg := range automdmAgentPackages {
			t.Run(pkg+" dry run", func(t *testing.T) {
				_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
					{
						AppStoreID:       pkg,
						LabelsExcludeAny: []string{},
						LabelsIncludeAny: []string{},
						LabelsIncludeAll: []string{},
						Categories:       []string{},
						Platform:         automdm.AndroidPlatform,
					},
				}, true)
				require.ErrorContains(t, err, "The AutoMDM agent cannot be added manually")
			})
			t.Run(pkg+" not dry run", func(t *testing.T) {
				_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
					{
						AppStoreID:       pkg,
						LabelsExcludeAny: []string{},
						LabelsIncludeAny: []string{},
						LabelsIncludeAll: []string{},
						Categories:       []string{},
						Platform:         automdm.AndroidPlatform,
					},
				}, false)
				require.ErrorContains(t, err, "The AutoMDM agent cannot be added manually")
			})
		}
	})
}

// TestGetAnchoredVPPAppsMetadataSkipsReAnchorOnEmptyMetadata guards against
// the row mismatch where reAnchors holds an entry for a (adamID, platform)
// whose metadata fetch was skipped because Apple returned blanks. Before the
// fix the trailing UpdateVPPAppCountryCode in BatchAssociateVPPApps would
// rewrite the row's country without a matching metadata insert, leaving the
// row internally inconsistent until the next refresh.
func TestGetAnchoredVPPAppsMetadataSkipsReAnchorOnEmptyMetadata(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	// Fake Apple metadata endpoint that returns the requested adamID with a
	// blank Name, the documented transiently-degraded path that the second
	// loop's empty-metadata guard skips.
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type plat struct {
			BundleID         string            `json:"bundleId"`
			Artwork          map[string]any    `json:"artwork"`
			LatestVersionRaw map[string]string `json:"latestVersionInfo"`
		}
		type attrs struct {
			Name           string          `json:"name"`
			DeviceFamilies []string        `json:"deviceFamilies"`
			Platforms      map[string]plat `json:"platformAttributes"`
		}
		type meta struct {
			ID         string `json:"id"`
			Attributes attrs  `json:"attributes"`
		}
		type resp struct {
			Data []meta `json:"data"`
		}
		out := resp{Data: []meta{{
			ID: "100",
			Attributes: attrs{
				Name:           "",
				DeviceFamilies: []string{"mac"},
				Platforms: map[string]plat{
					"osx": {
						BundleID:         "com.example.100",
						Artwork:          map[string]any{"url": "https://example.test/icon.png"},
						LatestVersionRaw: map[string]string{"versionDisplay": "1.0"},
					},
				},
			},
		}}}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(metaSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_STOKEN_AUTHENTICATED_APPS_URL", metaSrv.URL, t)

	ds := new(mock.Store)
	// Existing row anchored to "us". The DE team adding it has no owning
	// token in the anchored country, so resolveAddAnchor returns
	// reAnchor=true with anchorCountry="de".
	ds.GetVPPAppByAdamIDPlatformFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform) (*automdm.VPPApp, error) {
		return &automdm.VPPApp{
			VPPAppTeam:    automdm.VPPAppTeam{VPPAppID: automdm.VPPAppID{AdamID: adamID, Platform: platform}},
			CountryCode:   "us",
			Name:          "Todoist US",
			LatestVersion: "0.1",
		}, nil
	}
	ds.GetVPPTokenOwningAppInCountryFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform, country string) (*automdm.VPPTokenDB, error) {
		return nil, &batchNotFoundError{}
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{
		authz:  authorizer,
		ds:     ds,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Non-empty AppleConnectJWT so getVPPConfig's authenticator
		// short-circuits to the JWT instead of querying the datastore.
		config: config.AutoMDMConfig{MDM: config.MDMConfig{AppleConnectJWT: "test-jwt"}},
	}

	apps, reAnchors, err := svc.getAnchoredVPPAppsMetadata(t.Context(),
		[]automdm.VPPAppTeam{{AdamID: "100", Platform: automdm.MacOSPlatform, VPPTokenID: new(uint(7))}},
		map[uint]vppTokenInfo{7: {Secret: "de-secret", Country: "de"}},
	)
	require.NoError(t, err)
	require.Empty(t, apps, "row with empty Apple metadata must not be inserted")
	require.Empty(t, reAnchors, "reAnchors must not contain entries for skipped rows")
}

// batchNotFoundError satisfies automdm.IsNotFound for the GetVPPTokenOwningAppInCountry mock.
type batchNotFoundError struct{}

func (batchNotFoundError) Error() string    { return "not found" }
func (batchNotFoundError) IsNotFound() bool { return true }

// TestGetAppStoreAppsDoesNotWriteMetadata guards the picker against writing
// the team's current-storefront metadata onto rows whose stored country
// is anchored elsewhere.
func TestGetAppStoreAppsDoesNotWriteMetadata(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, incompatible with t.Parallel.

	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type plat struct {
			BundleID         string            `json:"bundleId"`
			Artwork          map[string]any    `json:"artwork"`
			LatestVersionRaw map[string]string `json:"latestVersionInfo"`
		}
		type attrs struct {
			Name           string          `json:"name"`
			DeviceFamilies []string        `json:"deviceFamilies"`
			Platforms      map[string]plat `json:"platformAttributes"`
		}
		type meta struct {
			ID         string `json:"id"`
			Attributes attrs  `json:"attributes"`
		}
		type resp struct {
			Data []meta `json:"data"`
		}
		out := resp{Data: []meta{{
			ID: "100",
			Attributes: attrs{
				Name:           "Todoist DE",
				DeviceFamilies: []string{"mac"},
				Platforms: map[string]plat{
					"osx": {
						BundleID:         "com.example.100",
						Artwork:          map[string]any{"url": "https://example.test/de-icon.png"},
						LatestVersionRaw: map[string]string{"versionDisplay": "9.9"},
					},
				},
			},
		}}}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(metaSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_STOKEN_AUTHENTICATED_APPS_URL", metaSrv.URL, t)

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[{"adamId":"100","pricingParam":"STDQ"}]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	teamID := uint(1)
	ds := new(mock.Store)
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{{
			ID:          1,
			OrgName:     "de-org",
			Token:       "de-secret",
			RenewDate:   time.Now().Add(24 * time.Hour),
			CountryCode: "de",
			Teams:       []automdm.TeamTuple{{ID: teamID}},
		}}, nil
	}
	// Existing row anchored to "us" while the team's current token is "de".
	ds.GetAssignedVPPAppsFunc = func(ctx context.Context, _ *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
		return map[automdm.VPPAppID]automdm.VPPAppTeam{
			{AdamID: "100", Platform: automdm.MacOSPlatform}: {
				VPPAppID: automdm.VPPAppID{AdamID: "100", Platform: automdm.MacOSPlatform},
			},
		}, nil
	}
	batchInsertCalled := false
	ds.BatchInsertVPPAppsFunc = func(ctx context.Context, _ []*automdm.VPPApp) error {
		batchInsertCalled = true
		return nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{
		Service: &svcmock.Service{VerifyMDMAppleConfiguredFunc: func(context.Context) error { return nil }},
		authz:   authorizer,
		ds:      ds,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		config:  config.AutoMDMConfig{MDM: config.MDMConfig{AppleConnectJWT: "test-jwt"}},
	}
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	apps, err := svc.GetAppStoreApps(ctx, &teamID, nil)
	require.NoError(t, err)
	require.Empty(t, apps, "already-assigned apps must be filtered out of the picker list")
	require.False(t, batchInsertCalled, "picker must not write metadata back")
}

// A no-platform numeric Adam ID expands to multiple (AdamID, platform)
// rows; the missing-asset error must surface each AdamID only once.
func TestBatchAssociateVPPAppsDedupsMissingAssetsError(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{{
			ID:          1,
			OrgName:     "us-org",
			Location:    "Acme US",
			Token:       "us-secret",
			RenewDate:   time.Now().Add(24 * time.Hour),
			CountryCode: "us",
			Teams:       []automdm.TeamTuple{},
		}}, nil
	}
	ds.GetAssignedVPPAppsFunc = func(ctx context.Context, teamID *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
		return nil, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, _ uint, _ []string) (map[string]uint, error) {
		return nil, nil
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, values []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}

	svc := newTestService(t, ds)

	// ValidateSoftwareLabels inside the loop requires a present authz context
	// for Authorize to mark it checked.
	ctx := authz_ctx.NewContext(t.Context(), &authz_ctx.AuthorizationContext{})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	const adamID = "1107542306"
	_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
		{
			AppStoreID:       adamID,
			LabelsExcludeAny: []string{},
			LabelsIncludeAny: []string{},
			LabelsIncludeAll: []string{},
			Categories:       []string{},
			// Empty Platform triggers the auto-expansion to multiple
			// (AdamID, platform) rows — the multiplier this test guards.
		},
	}, false)

	require.Error(t, err)
	require.ErrorContains(t, err, `Couldn't apply. App "`+adamID+`" isn't available in the "Acme US" VPP token.`)
	require.Equal(t, 1, strings.Count(err.Error(), adamID),
		"missing-asset error must dedup by AdamID, got: %s", err.Error())
}

// A dry run must report an app that is missing from the VPP location the
// same way a real apply does. Before this test the dry run returned early,
// so an unlicensed or mistyped Adam ID passed `automdmctl gitops --dry-run`
// and only failed during the real apply.
func TestBatchAssociateVPPAppsDryRunReportsMissingAssets(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[{"adamId":"497799835"}]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{{
			ID:          1,
			OrgName:     "us-org",
			Location:    "Acme US",
			Token:       "us-secret",
			RenewDate:   time.Now().Add(24 * time.Hour),
			CountryCode: "us",
			Teams:       []automdm.TeamTuple{},
		}}, nil
	}
	ds.GetAssignedVPPAppsFunc = func(ctx context.Context, teamID *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
		return nil, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, _ uint, _ []string) (map[string]uint, error) {
		return nil, nil
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}

	svc := newTestService(t, ds)

	ctx := authz_ctx.NewContext(t.Context(), &authz_ctx.AuthorizationContext{})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	payload := func(adamID string) []automdm.VPPBatchPayload {
		return []automdm.VPPBatchPayload{{
			AppStoreID:       adamID,
			Platform:         automdm.IOSPlatform,
			LabelsExcludeAny: []string{},
			LabelsIncludeAny: []string{},
			LabelsIncludeAll: []string{},
			Categories:       []string{},
		}}
	}

	// Licensed app: the dry run still succeeds without writing anything.
	_, _, err := svc.BatchAssociateVPPApps(ctx, "", payload("497799835"), true)
	require.NoError(t, err)
	require.False(t, ds.BatchInsertVPPAppsFuncInvoked)
	require.False(t, ds.SetTeamVPPAppsFuncInvoked)

	// Unlicensed app: the dry run fails with the same error as the real apply.
	const missing = "1107542306"
	_, _, err = svc.BatchAssociateVPPApps(ctx, "", payload(missing), true)
	require.Error(t, err)
	require.ErrorContains(t, err, `Couldn't apply. App "`+missing+`" isn't available in the "Acme US" VPP token.`)
	require.False(t, ds.BatchInsertVPPAppsFuncInvoked)
	require.False(t, ds.SetTeamVPPAppsFuncInvoked)
}

// A dry run for a team that does not exist yet used to return before any
// check. It must still refuse an Apple app that no VPP token can supply, since
// a new team's first real apply is where such an app otherwise fails, part-way
// through automdmctl's VPP re-apply.
func TestBatchAssociateVPPAppsDryRunNewTeamReportsMissingAssets(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[{"adamId":"497799835"}]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, common_mysql.NotFound("Team")
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{{
			ID:          1,
			OrgName:     "us-org",
			Token:       "us-secret",
			RenewDate:   time.Now().Add(24 * time.Hour),
			CountryCode: "us",
		}}, nil
	}
	ds.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC01"}, nil
	}

	svc := newTestService(t, ds)
	svc.androidModule = &playStoreLookupAndroidModule{availableApplicationIDs: []string{"com.example.android"}}
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	payload := func(adamID string, platform automdm.InstallableDevicePlatform) []automdm.VPPBatchPayload {
		return []automdm.VPPBatchPayload{{
			AppStoreID:       adamID,
			Platform:         platform,
			LabelsExcludeAny: []string{},
			LabelsIncludeAny: []string{},
			LabelsIncludeAll: []string{},
			Categories:       []string{},
		}}
	}

	// Licensed on a token: the dry run passes and nothing is written.
	_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", payload("497799835", automdm.IOSPlatform), true)
	require.NoError(t, err)

	// Licensed nowhere: the same error a real apply returns.
	const missing = "1107542306"
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload(missing, automdm.IPadOSPlatform), true)
	require.Error(t, err)
	require.ErrorContains(t, err, fmt.Sprintf(`Couldn't apply. App %q isn't available in any VPP token. Please purchase a license in Apple Business and try again.`, missing))

	// Play Store apps are not VPP assets and are not checked against VPP tokens.
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload("com.example.android", automdm.AndroidPlatform), true)
	require.NoError(t, err)

	require.False(t, ds.BatchInsertVPPAppsFuncInvoked)
	require.False(t, ds.SetTeamVPPAppsFuncInvoked)

	// A real apply for a missing team is unchanged: still a not-found error.
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload(missing, automdm.IPadOSPlatform), false)
	require.Error(t, err)
	require.True(t, automdm.IsNotFound(err))
}

type playStoreLookupAndroidModule struct {
	android.Service
	availableApplicationIDs []string
	titleByApplicationID    map[string]string
	lookedUpApplicationIDs  []string
}

func (m *playStoreLookupAndroidModule) EnterprisesApplications(ctx context.Context, enterpriseName string, applicationID string) (*androidmanagement.Application, error) {
	m.lookedUpApplicationIDs = append(m.lookedUpApplicationIDs, applicationID)
	if !slices.Contains(m.availableApplicationIDs, applicationID) {
		return nil, batchNotFoundError{}
	}
	title := applicationID
	if m.titleByApplicationID[applicationID] != "" {
		title = m.titleByApplicationID[applicationID]
	}
	return &androidmanagement.Application{Name: enterpriseName + "/applications/" + applicationID, Title: title}, nil
}

func TestBatchAssociateVPPAppsDryRunReportsMissingAndroidApps(t *testing.T) {
	t.Parallel()

	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		if name == "Existing team" {
			return &automdm.Team{ID: 1, Name: name}, nil
		}
		return nil, common_mysql.NotFound("Team")
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, _ uint, _ []string) (map[string]uint, error) {
		return nil, nil
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	ds.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC01"}, nil
	}

	const firstWebApp = automdm.AndroidWebAppPrefix + ".first"
	const secondWebApp = automdm.AndroidWebAppPrefix + ".second"
	androidModule := &playStoreLookupAndroidModule{
		availableApplicationIDs: []string{"com.android.chrome", firstWebApp, secondWebApp},
		titleByApplicationID:    map[string]string{firstWebApp: "Intranet", secondWebApp: "intranet"},
	}
	svc := newTestService(t, ds)
	svc.androidModule = androidModule

	ctx := authz_ctx.NewContext(t.Context(), &authz_ctx.AuthorizationContext{})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	const missing = "com.example.notinplaystore"
	missingErr := `Couldn't add software. The application ID "` + missing + `" isn't available in Play Store.`
	chromeVersions := []automdm.VPPBatchPayload{
		{AppStoreID: "com.android.chrome", Platform: automdm.AndroidPlatform, VersionName: "Work"},
		{AppStoreID: "com.android.chrome", Platform: automdm.AndroidPlatform, VersionName: "Personal"},
	}
	missingApp := []automdm.VPPBatchPayload{{AppStoreID: missing, Platform: automdm.AndroidPlatform}}

	// Dry run two versions of an available app on an existing team, the app should be looked up once.
	_, _, err := svc.BatchAssociateVPPApps(ctx, "Existing team", chromeVersions, true)
	require.NoError(t, err)
	require.Equal(t, []string{"com.android.chrome"}, androidModule.lookedUpApplicationIDs)

	// Dry run an app missing from Play Store on an existing team, the dry run should fail like the apply.
	androidModule.lookedUpApplicationIDs = nil
	_, _, err = svc.BatchAssociateVPPApps(ctx, "Existing team", missingApp, true)
	require.ErrorContains(t, err, missingErr)
	require.Equal(t, []string{missing}, androidModule.lookedUpApplicationIDs)

	// Dry run an app missing from Play Store on Unassigned, the dry run should fail like the apply.
	androidModule.lookedUpApplicationIDs = nil
	_, _, err = svc.BatchAssociateVPPApps(ctx, "", missingApp, true)
	require.ErrorContains(t, err, missingErr)
	require.Equal(t, []string{missing}, androidModule.lookedUpApplicationIDs)

	// Dry run two versions of an available app and a missing app on a new team, each app should be
	// looked up once and the dry run should fail on the missing app.
	androidModule.lookedUpApplicationIDs = nil
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", slices.Concat(chromeVersions, missingApp), true)
	require.ErrorContains(t, err, missingErr)
	require.Equal(t, []string{"com.android.chrome", missing}, androidModule.lookedUpApplicationIDs)

	// Dry run two web apps with the same title on a new team, the dry run should fail like the apply.
	androidModule.lookedUpApplicationIDs = nil
	sameTitleWebApps := []automdm.VPPBatchPayload{
		{AppStoreID: firstWebApp, Platform: automdm.AndroidPlatform},
		{AppStoreID: secondWebApp, Platform: automdm.AndroidPlatform},
	}
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", sameTitleWebApps, true)
	require.ErrorContains(t, err, `Couldn't add. Web app with this name ("intranet") already exists in this team.`)
	require.Equal(t, []string{firstWebApp, secondWebApp}, androidModule.lookedUpApplicationIDs)

	require.False(t, ds.BatchInsertVPPAppsFuncInvoked)
	require.False(t, ds.SetTeamVPPAppsFuncInvoked)
}

// A dry run for a team that does not exist yet reads every unexpired token. A
// token Apple rejects must not fail the dry run while another token answers,
// since the team may never be assigned to it. Only when no token answers does
// the dry run fail, and when every token is expired it fails without calling Apple.
func TestBatchAssociateVPPAppsDryRunNewTeamToleratesUnreadableToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	var calls atomic.Int32
	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		if r.Header.Get("Authorization") == "Bearer good" {
			_, _ = w.Write([]byte(`{"assets":[{"adamId":"497799835"}]}`))
			return
		}
		// Apple answers a revoked token with 200 and an error body.
		_, _ = w.Write([]byte(`{"errorNumber":9622,"errorMessage":"Invalid authentication token"}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	token := func(secret string, renew time.Time) *automdm.VPPTokenDB {
		return &automdm.VPPTokenDB{OrgName: secret + "-org", Token: secret, RenewDate: renew, CountryCode: "us"}
	}
	valid := time.Now().Add(24 * time.Hour)
	expired := time.Now().Add(-24 * time.Hour)

	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, common_mysql.NotFound("Team")
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	var tokens []*automdm.VPPTokenDB
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return tokens, nil
	}
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	payload := []automdm.VPPBatchPayload{{AppStoreID: "497799835", Platform: automdm.IOSPlatform}}

	// One revoked token and one that holds the app: the dry run passes.
	tokens = []*automdm.VPPTokenDB{token("bad", valid), token("good", valid)}
	_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", payload, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())

	// Only revoked tokens: the dry run fails with the fetch error.
	tokens = []*automdm.VPPTokenDB{token("bad", valid), token("worse", valid)}
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload, true)
	require.ErrorContains(t, err, "unable to retrieve assets")
	require.ErrorContains(t, err, "Invalid authentication token")

	// Only expired tokens: the dry run fails before calling Apple.
	calls.Store(0)
	tokens = []*automdm.VPPTokenDB{token("good", expired)}
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload, true)
	require.ErrorContains(t, err, "Couldn't apply. All VPP tokens are expired. Please renew one and try again.")
	require.Zero(t, calls.Load())

	// An expired token next to a valid one is ignored.
	tokens = []*automdm.VPPTokenDB{token("bad", expired), token("good", valid)}
	_, _, err = svc.BatchAssociateVPPApps(ctx, "New team", payload, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())

	require.False(t, ds.BatchInsertVPPAppsFuncInvoked)
	require.False(t, ds.SetTeamVPPAppsFuncInvoked)
}

// A dry run for a team that does not exist yet must reject the same payloads
// the existing-team path rejects, instead of skipping them because no VPP
// lookup applies. Each case fails before any token is read.
func TestBatchAssociateVPPAppsDryRunNewTeamValidatesPayloads(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, common_mysql.NotFound("Team")
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	ios := func(adamID, versionName string) automdm.VPPBatchPayload {
		return automdm.VPPBatchPayload{AppStoreID: adamID, Platform: automdm.IOSPlatform, VersionName: versionName}
	}
	tests := []struct {
		name string
		// duplicates is what the collation lookup reports, indexes into the
		// expanded version list.
		duplicates []automdm.DuplicateStringGroup
		payloads   []automdm.VPPBatchPayload
		wantErr    string
	}{
		{
			name:     "unsupported platform",
			payloads: []automdm.VPPBatchPayload{{AppStoreID: "not-an-adam-id", Platform: automdm.InstallableDevicePlatform("windows")}},
			wantErr:  "platform must be one of",
		},
		{
			name:     "automdm agent on android",
			payloads: []automdm.VPPBatchPayload{{AppStoreID: automdmAgentPackagePrefix + ".foo", Platform: automdm.AndroidPlatform}},
			wantErr:  "The AutoMDM agent cannot be added manually",
		},
		{
			name:     "empty category",
			payloads: []automdm.VPPBatchPayload{{AppStoreID: "497799835", Platform: automdm.IOSPlatform, Categories: []string{" "}}},
			wantErr:  "name is required",
		},
		{
			name:     "version name too long",
			payloads: []automdm.VPPBatchPayload{ios("497799835", strings.Repeat("v", automdm.MaxAppStoreAppVersionNameLength+1))},
			wantErr:  "The version name can't be longer than",
		},
		{
			name:     "entry with and without versions",
			payloads: []automdm.VPPBatchPayload{ios("497799835", ""), ios("497799835", "Stable")},
			wantErr:  "The app has an entry without versions and an entry with versions",
		},
		{
			name: "auto-update window without times",
			payloads: []automdm.VPPBatchPayload{{
				AppStoreID: "497799835", Platform: automdm.IOSPlatform, AutoUpdateEnabled: new(true),
			}},
			wantErr: "Start and end time must both be set",
		},
		{
			// No platform expands to macOS, iOS, and iPadOS, so the iOS window check applies.
			name: "auto-update window on an app with no platform",
			payloads: []automdm.VPPBatchPayload{{
				AppStoreID: "497799835", AutoUpdateStartTime: new("09:00"), AutoUpdateEndTime: new("09:30"),
			}},
			wantErr: "The update window must be at least one hour long",
		},
		{
			name:       "version names equal under collation",
			duplicates: []automdm.DuplicateStringGroup{{Indices: []int{0, 1}}},
			payloads:   []automdm.VPPBatchPayload{ios("497799835", "Stable"), ios("497799835", "stable")},
			wantErr:    "More than one version is named",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds.ListVPPTokensFuncInvoked = false
			ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
				return tt.duplicates, nil
			}
			_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", tt.payloads, true)
			require.ErrorContains(t, err, tt.wantErr)
			require.False(t, ds.ListVPPTokensFuncInvoked)
		})
	}
}

// A dry run for a team that does not exist yet reads the assets of every VPP
// token, so it must require global software write access first. Anyone who can
// read teams could otherwise probe which App Store IDs the org has licensed.
func TestBatchAssociateVPPAppsDryRunNewTeamRequiresWriteAccess(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, common_mysql.NotFound("Team")
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, _ []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}
	svc := newTestService(t, ds)

	payloads := []automdm.VPPBatchPayload{{
		AppStoreID:       "497799835",
		Platform:         automdm.IOSPlatform,
		LabelsExcludeAny: []string{},
		LabelsIncludeAny: []string{},
		LabelsIncludeAll: []string{},
		Categories:       []string{},
	}}

	team1 := func(role string) *automdm.User {
		return &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: role}}}
	}
	tests := []struct {
		name      string
		user      *automdm.User
		forbidden bool
	}{
		{"global admin", &automdm.User{GlobalRole: new(automdm.RoleAdmin)}, false},
		{"global maintainer", &automdm.User{GlobalRole: new(automdm.RoleMaintainer)}, false},
		{"global gitops", &automdm.User{GlobalRole: new(automdm.RoleGitOps)}, false},
		{"global technician", &automdm.User{GlobalRole: new(automdm.RoleTechnician)}, true},
		{"global observer", &automdm.User{GlobalRole: new(automdm.RoleObserver)}, true},
		{"team admin", team1(automdm.RoleAdmin), true},
		{"team gitops", team1(automdm.RoleGitOps), true},
		{"team observer", team1(automdm.RoleObserver), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds.ListVPPTokensFuncInvoked = false
			ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: tt.user})
			_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", payloads, true)
			if tt.forbidden {
				var forbidden *authz.Forbidden
				require.ErrorAs(t, err, &forbidden)
				require.False(t, ds.ListVPPTokensFuncInvoked, "VPP tokens must not be read before authorization")
				return
			}
			require.NoError(t, err)
			require.True(t, ds.ListVPPTokensFuncInvoked)
		})
	}
}

// TestGetVPPTokensScoping verifies that GetVPPTokens returns every token to
// global readers but scopes the list to a team-scoped user's readable teams
// (plus "All teams" tokens), without leaking tokens from teams the user can't
// read. See #46057.
func TestGetVPPTokensScoping(t *testing.T) {
	ds := new(mock.Store)
	// Tokens: team 1, team 2, "All teams" (non-nil empty Teams), and an
	// unassigned token (nil Teams).
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{
			{ID: 1, OrgName: "team1", Teams: []automdm.TeamTuple{{ID: 1, Name: "Workstations"}}},
			{ID: 2, OrgName: "team2", Teams: []automdm.TeamTuple{{ID: 2, Name: "Servers"}}},
			{ID: 3, OrgName: "allteams", Teams: []automdm.TeamTuple{}},
			{ID: 4, OrgName: "unassigned", Teams: nil},
		}, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{
		authz:  authorizer,
		ds:     ds,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	globalMaintainer := &automdm.User{GlobalRole: new(automdm.RoleMaintainer)}
	// Technician can read installable entities but not write them, so it must be
	// able to read the token list to use the picker (#46057 names this role).
	globalTechnician := &automdm.User{GlobalRole: new(automdm.RoleTechnician)}
	teamMaintainer1 := &automdm.User{Teams: []automdm.UserTeam{
		{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer},
	}}
	teamTechnician1 := &automdm.User{Teams: []automdm.UserTeam{
		{Team: automdm.Team{ID: 1}, Role: automdm.RoleTechnician},
	}}
	// Observer on the first team, maintainer on the second: must still be
	// authorized (via team 2) and scoped to team 2, never team 1.
	observerThenMaintainer := &automdm.User{Teams: []automdm.UserTeam{
		{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver},
		{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer},
	}}
	teamObserver1 := &automdm.User{Teams: []automdm.UserTeam{
		{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver},
	}}

	tests := []struct {
		name    string
		user    *automdm.User
		wantErr bool
		wantIDs []uint
	}{
		{"global admin sees all", &automdm.User{GlobalRole: new(automdm.RoleAdmin)}, false, []uint{1, 2, 3, 4}},
		{"global maintainer sees all", globalMaintainer, false, []uint{1, 2, 3, 4}},
		{"global technician sees all", globalTechnician, false, []uint{1, 2, 3, 4}},
		{"team maintainer scoped to team + all-teams", teamMaintainer1, false, []uint{1, 3}},
		{"team technician scoped to team + all-teams", teamTechnician1, false, []uint{1, 3}},
		{"observer-then-maintainer scoped to second team", observerThenMaintainer, false, []uint{2, 3}},
		{"team observer forbidden", teamObserver1, true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: tt.user})
			got, err := svc.GetVPPTokens(ctx)
			if tt.wantErr {
				require.Error(t, err)
				require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
				return
			}
			require.NoError(t, err)
			gotIDs := make([]uint, 0, len(got))
			for _, tok := range got {
				gotIDs = append(gotIDs, tok.ID)
			}
			require.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestAppStoreAppsRequireAppleMDM(t *testing.T) {
	t.Parallel()
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	setup := func(t *testing.T) (*Service, *mock.Store, *svcmock.Service) {
		ds := new(mock.Store)
		svc, baseSvc := newTestServiceWithMock(t, ds)
		baseSvc.VerifyMDMAppleConfiguredFunc = func(context.Context) error { return automdm.ErrMDMNotConfigured }
		ds.GetVPPTokenByTeamIDFunc = func(context.Context, *uint) (*automdm.VPPTokenDB, error) {
			return &automdm.VPPTokenDB{ID: 1}, nil
		}
		ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
			return []*automdm.VPPTokenDB{{ID: 1, Teams: []automdm.TeamTuple{}}}, nil
		}
		ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) { return &automdm.AppConfig{}, nil }
		ds.TeamByNameFunc = func(context.Context, string) (*automdm.Team, error) {
			return nil, common_mysql.NotFound("Team")
		}
		ds.GetDuplicateStringGroupsUnderCollationFunc = func(context.Context, []string) ([]automdm.DuplicateStringGroup, error) {
			return nil, nil
		}
		ds.GetEnterpriseFunc = func(context.Context) (*android.Enterprise, error) {
			return &android.Enterprise{EnterpriseID: "LC01"}, nil
		}
		svc.androidModule = &playStoreLookupAndroidModule{availableApplicationIDs: []string{"com.example.app"}}
		return svc, ds, baseSvc
	}

	t.Run("listing VPP apps", func(t *testing.T) {
		svc, ds, _ := setup(t)
		_, err := svc.GetAppStoreApps(ctx, nil, nil)
		require.ErrorIs(t, err, automdm.ErrMDMNotConfigured)
		require.False(t, ds.ListVPPTokensFuncInvoked, "the VPP token must not be used")
	})

	t.Run("adding an Apple app", func(t *testing.T) {
		svc, _, _ := setup(t)
		_, err := svc.AddAppStoreApp(ctx, nil, automdm.VPPAppTeam{AdamID: "497799835", Platform: automdm.MacOSPlatform})
		require.ErrorIs(t, err, automdm.ErrMDMNotConfigured)
	})

	t.Run("adding an Android app doesn't need Apple MDM", func(t *testing.T) {
		svc, _, baseSvc := setup(t)
		// Label validation runs after the Apple MDM check and fails in this test's context, so reaching it proves
		// the add got past the check.
		_, err := svc.AddAppStoreApp(ctx, nil, automdm.VPPAppTeam{AdamID: "com.example.app", Platform: automdm.AndroidPlatform})
		require.ErrorContains(t, err, "validating software labels")
		require.False(t, baseSvc.VerifyMDMAppleConfiguredFuncInvoked)
	})

	t.Run("GitOps apply with an Apple app writes nothing", func(t *testing.T) {
		svc, ds, _ := setup(t)
		ds.ListSoftwareCategoriesFunc = func(context.Context, uint) ([]automdm.SoftwareCategory, error) { return nil, nil }
		_, _, err := svc.BatchAssociateVPPApps(ctx, "", []automdm.VPPBatchPayload{
			{AppStoreID: "497799835", Platform: automdm.IOSPlatform, Categories: []string{"New category"}},
		}, false)
		require.ErrorIs(t, err, automdm.ErrMDMNotConfigured)
		require.False(t, ds.ListSoftwareCategoriesFuncInvoked, "categories must not be written for a rejected batch")
	})

	t.Run("GitOps dry run for a new team with an Apple app", func(t *testing.T) {
		svc, _, _ := setup(t)
		_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", []automdm.VPPBatchPayload{{AppStoreID: "497799835", Platform: automdm.IOSPlatform}}, true)
		require.ErrorIs(t, err, automdm.ErrMDMNotConfigured)
	})

	t.Run("GitOps dry run for a new team with only Android apps", func(t *testing.T) {
		svc, _, baseSvc := setup(t)
		_, _, err := svc.BatchAssociateVPPApps(ctx, "New team", []automdm.VPPBatchPayload{{AppStoreID: "com.example.app", Platform: automdm.AndroidPlatform}}, true)
		require.NoError(t, err)
		require.False(t, baseSvc.VerifyMDMAppleConfiguredFuncInvoked)
	})
}

func vppErrStatus(err error) int {
	var withStatus interface{ Status() int }
	if errors.As(err, &withStatus) {
		return withStatus.Status()
	}
	if withCode, ok := errors.AsType[automdm.ErrWithStatusCode](err); ok {
		return withCode.StatusCode()
	}
	if _, ok := errors.AsType[*automdm.InvalidArgumentError](err); ok {
		return http.StatusUnprocessableEntity
	}
	return 0
}

func TestResolveAppStoreAppToken(t *testing.T) {
	t.Parallel()

	valid := time.Now().Add(24 * time.Hour)
	us := &automdm.VPPTokenDB{ID: 1, Location: "Acme US", Token: "us-secret", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 1}}}
	jp := &automdm.VPPTokenDB{ID: 2, Location: "Acme Japan", Token: "jp-secret", RenewDate: valid, CountryCode: "jp", Teams: []automdm.TeamTuple{{ID: 1}}}
	jpExpired := &automdm.VPPTokenDB{ID: 2, Location: "Acme Japan", Token: "jp-secret", RenewDate: time.Now().Add(-time.Hour), CountryCode: "jp", Teams: []automdm.TeamTuple{{ID: 1}}}
	unassigned := &automdm.VPPTokenDB{ID: 3, Location: "Unassigned", Token: "x", RenewDate: valid, CountryCode: "us"}
	otherAutoMDM := &automdm.VPPTokenDB{ID: 4, Location: "Other team", Token: "x", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 2}}}
	allAutoMDMs := &automdm.VPPTokenDB{ID: 5, Location: "All teams", Token: "all-secret", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{}}
	noAutoMDM := &automdm.VPPTokenDB{ID: 6, Location: "No team", Token: "no-secret", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 0}}}

	tokenID := func(id uint) *uint { return &id }
	automdmID := func(id uint) *uint { return &id }

	maintainerOf := func(ids ...uint) *automdm.User {
		u := &automdm.User{}
		for _, id := range ids {
			u.Teams = append(u.Teams, automdm.UserTeam{ID: id, Role: automdm.RoleMaintainer})
		}
		return u
	}

	tests := []struct {
		name       string
		user       *automdm.User // global admin when nil
		tokens     []*automdm.VPPTokenDB
		teamID     *uint
		vppTokenID *uint
		wantID     uint
		wantErr    string
		wantStatus int
	}{
		{name: "single token without id", tokens: []*automdm.VPPTokenDB{us, otherAutoMDM}, teamID: automdmID(1), wantID: 1},
		{name: "single token with its id", tokens: []*automdm.VPPTokenDB{us, otherAutoMDM}, teamID: automdmID(1), vppTokenID: tokenID(1), wantID: 1},
		{name: "requested token of two", tokens: []*automdm.VPPTokenDB{us, jp}, teamID: automdmID(1), vppTokenID: tokenID(2), wantID: 2},
		{name: "all teams token by id", tokens: []*automdm.VPPTokenDB{allAutoMDMs}, teamID: automdmID(7), vppTokenID: tokenID(5), wantID: 5},
		{name: "nil team is no team", tokens: []*automdm.VPPTokenDB{noAutoMDM, us}, wantID: 6},
		{
			name: "missing token", tokens: []*automdm.VPPTokenDB{us}, teamID: automdmID(1), vppTokenID: tokenID(99),
			wantErr: "Couldn't list apps. VPP token with ID 99 doesn't exist.", wantStatus: http.StatusNotFound,
		},
		{
			name: "unassigned token", tokens: []*automdm.VPPTokenDB{us, unassigned}, teamID: automdmID(1), vppTokenID: tokenID(3),
			wantErr: `Couldn't list apps. VPP token "Unassigned" isn't assigned to this team.`, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "token assigned to another team", tokens: []*automdm.VPPTokenDB{us, otherAutoMDM}, teamID: automdmID(1), vppTokenID: tokenID(4),
			wantErr: `Couldn't list apps. VPP token "Other team" isn't assigned to this team.`, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "token of another team hidden from team maintainer", user: maintainerOf(1), tokens: []*automdm.VPPTokenDB{us, otherAutoMDM}, teamID: automdmID(1), vppTokenID: tokenID(4),
			wantErr: "Couldn't list apps. VPP token with ID 4 doesn't exist.", wantStatus: http.StatusNotFound,
		},
		{
			name: "token of another team visible to maintainer of both", user: maintainerOf(1, 2), tokens: []*automdm.VPPTokenDB{us, otherAutoMDM}, teamID: automdmID(1), vppTokenID: tokenID(4),
			wantErr: `Couldn't list apps. VPP token "Other team" isn't assigned to this team.`, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "unassigned token hidden from team maintainer", user: maintainerOf(1), tokens: []*automdm.VPPTokenDB{us, unassigned}, teamID: automdmID(1), vppTokenID: tokenID(3),
			wantErr: "Couldn't list apps. VPP token with ID 3 doesn't exist.", wantStatus: http.StatusNotFound,
		},
		{
			name: "expired requested token", tokens: []*automdm.VPPTokenDB{us, jpExpired}, teamID: automdmID(1), vppTokenID: tokenID(2),
			wantErr: `Couldn't list apps. VPP token "Acme Japan" is expired. Please renew it and try again.`, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "expired only token", tokens: []*automdm.VPPTokenDB{jpExpired}, teamID: automdmID(1),
			wantErr: `Couldn't list apps. VPP token "Acme Japan" is expired. Please renew it and try again.`, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "two tokens without id", tokens: []*automdm.VPPTokenDB{us, jp}, teamID: automdmID(1),
			wantErr:    "Couldn't list apps. This team has more than one VPP token. Please specify vpp_token_id.",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "all teams plus automdm token", tokens: []*automdm.VPPTokenDB{us, allAutoMDMs}, teamID: automdmID(1),
			wantErr:    "Couldn't list apps. This team has more than one VPP token. Please specify vpp_token_id.",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "all teams plus no automdm token", tokens: []*automdm.VPPTokenDB{noAutoMDM, allAutoMDMs}, teamID: automdmID(0),
			wantErr:    "Couldn't list apps. This team has more than one VPP token. Please specify vpp_token_id.",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "no token for the team", tokens: []*automdm.VPPTokenDB{otherAutoMDM, unassigned}, teamID: automdmID(1),
			wantErr: "No available VPP Token", wantStatus: http.StatusUnprocessableEntity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) { return tt.tokens, nil }
			svc := newTestService(t, ds)

			user := tt.user
			if user == nil {
				user = &automdm.User{GlobalRole: new(automdm.RoleAdmin)}
			}
			ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: user})

			tok, err := svc.resolveAppStoreAppToken(ctx, tt.teamID, tt.vppTokenID, "Couldn't list apps.")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Equal(t, tt.wantStatus, vppErrStatus(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantID, tok.ID)
		})
	}

	t.Run("add copy", func(t *testing.T) {
		ds := new(mock.Store)
		ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) { return []*automdm.VPPTokenDB{us, jp}, nil }
		svc := newTestService(t, ds)
		ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

		_, err := svc.resolveAppStoreAppToken(ctx, automdmID(1), nil, "Couldn't add software.")
		require.ErrorContains(t, err, "Couldn't add software. This team has more than one VPP token. Please specify vpp_token_id.")
		_, err = svc.resolveAppStoreAppToken(ctx, automdmID(1), tokenID(99), "Couldn't add software.")
		require.ErrorContains(t, err, "Couldn't add software. VPP token with ID 99 doesn't exist.")
	})
}

func TestGetAppStoreAppsUsesRequestedToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, incompatible with t.Parallel.
	var seenAuth []string
	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	valid := time.Now().Add(24 * time.Hour)
	ds := new(mock.Store)
	ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{
			{ID: 1, Location: "Acme US", Token: "us-secret", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 1}}},
			{ID: 2, Location: "Acme Japan", Token: "jp-secret", RenewDate: valid, CountryCode: "jp", Teams: []automdm.TeamTuple{{ID: 1}}},
		}, nil
	}
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	teamID := uint(1)
	_, err := svc.GetAppStoreApps(ctx, &teamID, nil)
	require.ErrorContains(t, err, "Couldn't list apps. This team has more than one VPP token. Please specify vpp_token_id.")
	require.Empty(t, seenAuth, "Apple must not be called without a token choice")

	_, err = svc.GetAppStoreApps(ctx, &teamID, new(uint(2)))
	require.NoError(t, err)
	require.Equal(t, []string{"Bearer jp-secret"}, seenAuth)
}

type addAppStoreAppFixture struct {
	svc        *Service
	ds         *mock.Store
	ctx        context.Context
	assets     string
	seenAuth   []string
	inserted   *automdm.VPPApp
	activities []automdm.ActivityDetails
}

// newAddAppStoreAppFixture wires AddAppStoreApp for an iOS app (adam ID "100")
// on the No team team, with fake Apple VPP and metadata servers.
func newAddAppStoreAppFixture(t *testing.T, tokens ...*automdm.VPPTokenDB) *addAppStoreAppFixture {
	t.Helper()
	fx := &addAppStoreAppFixture{assets: `{"assets":[{"adamId":"100","pricingParam":"STDQ"}]}`}

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.seenAuth = append(fx.seenAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fx.assets))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"100","attributes":{"name":"Canva","deviceFamilies":["iphone"],` +
			`"platformAttributes":{"ios":{"bundleId":"com.example.canva","artwork":{"url":"https://example.test/icon.png"},` +
			`"latestVersionInfo":{"versionDisplay":"1.0"}}}}}]}`))
	}))
	t.Cleanup(metaSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_STOKEN_AUTHENTICATED_APPS_URL", metaSrv.URL, t)

	ds := new(mock.Store)
	svc, baseSvc := newTestServiceWithMock(t, ds)
	svc.config = config.AutoMDMConfig{MDM: config.MDMConfig{AppleConnectJWT: "test-jwt"}}
	baseSvc.VerifyMDMAppleConfiguredFunc = func(context.Context) error { return nil }
	baseSvc.NewActivityFunc = func(_ context.Context, _ *automdm.User, a automdm.ActivityDetails) error {
		fx.activities = append(fx.activities, a)
		return nil
	}

	ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) { return tokens, nil }
	ds.GetVPPTokenFunc = func(_ context.Context, id uint) (*automdm.VPPTokenDB, error) {
		for _, tok := range tokens {
			if tok.ID == id {
				return tok, nil
			}
		}
		return nil, common_mysql.NotFound("VPPToken")
	}
	ds.GetAppStoreAppVersionCountFunc = func(context.Context, *uint, automdm.VPPAppID, string) (uint, bool, error) {
		return 0, false, nil
	}
	ds.GetVPPAppByAdamIDPlatformFunc = func(context.Context, string, automdm.InstallableDevicePlatform) (*automdm.VPPApp, error) {
		return nil, common_mysql.NotFound("VPPApp")
	}
	ds.CheckConflictingInHouseAppExistsFunc = func(context.Context, *uint, string, string) (bool, error) { return false, nil }
	ds.InsertVPPAppWithTeamFunc = func(_ context.Context, app *automdm.VPPApp, _ *uint, _ *uint) (*automdm.VPPApp, error) {
		fx.inserted = app
		added := *app
		added.TitleID = 5
		added.VersionName = automdm.DefaultAppStoreAppVersionName
		return &added, nil
	}

	fx.svc = svc
	fx.ds = ds
	ctx := authz_ctx.NewContext(t.Context(), &authz_ctx.AuthorizationContext{})
	fx.ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	return fx
}

func TestAddAppStoreAppToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, incompatible with t.Parallel.
	valid := time.Now().Add(24 * time.Hour)
	// Tokens on the No team team (nil teamID).
	us := func() *automdm.VPPTokenDB {
		return &automdm.VPPTokenDB{ID: 1, Location: "Acme US", Token: "us-secret", RenewDate: valid, CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 0}}}
	}
	jp := func() *automdm.VPPTokenDB {
		return &automdm.VPPTokenDB{ID: 2, Location: "Acme Japan", Token: "jp-secret", RenewDate: valid, CountryCode: "jp", Teams: []automdm.TeamTuple{{ID: 0}}}
	}
	iosApp := func(tokenID *uint) automdm.VPPAppTeam {
		return automdm.VPPAppTeam{AdamID: "100", Platform: automdm.IOSPlatform, VPPTokenID: tokenID}
	}

	t.Run("binds the requested token and records it on the activity", func(t *testing.T) {
		fx := newAddAppStoreAppFixture(t, us(), jp())
		_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(new(uint(2))))
		require.NoError(t, err)
		require.Equal(t, []string{"Bearer jp-secret"}, fx.seenAuth)
		require.NotNil(t, fx.inserted.VPPTokenID)
		require.Equal(t, uint(2), *fx.inserted.VPPTokenID)
		require.Equal(t, "jp", fx.inserted.CountryCode)

		require.Len(t, fx.activities, 1)
		act, ok := fx.activities[0].(automdm.ActivityAddedAppStoreApp)
		require.True(t, ok)
		require.Equal(t, &automdm.ActivityVPPToken{ID: 2, Location: "Acme Japan", CountryCode: "jp"}, act.VPPToken)
	})

	t.Run("single token binds without a token id", func(t *testing.T) {
		fx := newAddAppStoreAppFixture(t, us())
		_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(nil))
		require.NoError(t, err)
		require.Equal(t, uint(1), *fx.inserted.VPPTokenID)
		require.Equal(t, uint(1), fx.activities[0].(automdm.ActivityAddedAppStoreApp).VPPToken.ID)
	})

	t.Run("two tokens without a token id", func(t *testing.T) {
		fx := newAddAppStoreAppFixture(t, us(), jp())
		_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(nil))
		require.ErrorContains(t, err, "Couldn't add software. This team has more than one VPP token. Please specify vpp_token_id.")
		require.Equal(t, http.StatusUnprocessableEntity, vppErrStatus(err))
		require.False(t, fx.ds.InsertVPPAppWithTeamFuncInvoked)
	})

	t.Run("app missing from the token", func(t *testing.T) {
		fx := newAddAppStoreAppFixture(t, us(), jp())
		fx.assets = `{"assets":[]}`
		_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(new(uint(2))))
		require.ErrorContains(t, err, `Couldn't add software. "100" isn't available in the "Acme Japan" VPP token. Please purchase a license in Apple Business and try again.`)
		require.Equal(t, http.StatusUnprocessableEntity, vppErrStatus(err))
		require.False(t, fx.ds.InsertVPPAppWithTeamFuncInvoked)
	})

	t.Run("Android rejects a token id", func(t *testing.T) {
		fx := newAddAppStoreAppFixture(t, us(), jp())
		_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, automdm.VPPAppTeam{
			AdamID:     "com.example.app",
			Platform:   automdm.AndroidPlatform,
			VPPTokenID: new(uint(1)),
		})
		require.ErrorContains(t, err, "Couldn't add software. vpp_token_id isn't supported for Android apps.")
		require.Equal(t, http.StatusUnprocessableEntity, vppErrStatus(err))
		require.False(t, fx.ds.InsertVPPAppWithTeamFuncInvoked)
	})

	t.Run("second version", func(t *testing.T) {
		withExisting := func(t *testing.T, existingTokenID *uint) *addAppStoreAppFixture {
			fx := newAddAppStoreAppFixture(t, us(), jp())
			fx.ds.GetAppStoreAppVersionCountFunc = func(context.Context, *uint, automdm.VPPAppID, string) (uint, bool, error) {
				return 1, false, nil
			}
			fx.ds.GetVPPAppMetadataByAdamIDPlatformTeamIDFunc = func(context.Context, string, automdm.InstallableDevicePlatform, *uint) (*automdm.VPPApp, error) {
				return &automdm.VPPApp{Name: "Canva", VPPTokenID: existingTokenID}, nil
			}
			return fx
		}

		t.Run("inherits the first version's token", func(t *testing.T) {
			fx := withExisting(t, new(uint(2)))
			_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(nil))
			require.NoError(t, err)
			require.Equal(t, []string{"Bearer jp-secret"}, fx.seenAuth)
			require.Equal(t, uint(2), *fx.inserted.VPPTokenID)
		})

		t.Run("same token is accepted", func(t *testing.T) {
			fx := withExisting(t, new(uint(2)))
			_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(new(uint(2))))
			require.NoError(t, err)
			require.Equal(t, uint(2), *fx.inserted.VPPTokenID)
		})

		for _, name := range []string{"", "US copy"} {
			t.Run("another token conflicts/name="+name, func(t *testing.T) {
				fx := withExisting(t, new(uint(2)))
				fx.ds.GetAppStoreAppVersionCountFunc = func(context.Context, *uint, automdm.VPPAppID, string) (uint, bool, error) {
					return 1, name == "", nil
				}
				app := iosApp(new(uint(1)))
				app.VersionName = name
				_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, app)
				require.ErrorContains(t, err, `Couldn't add software. Canva (iOS) is already added to this team from the "Acme Japan" VPP token.`)
				require.Equal(t, http.StatusConflict, vppErrStatus(err))
				require.Empty(t, fx.seenAuth)
				require.False(t, fx.ds.InsertVPPAppWithTeamFuncInvoked)
			})
		}

		t.Run("same token with an existing name conflicts", func(t *testing.T) {
			fx := withExisting(t, new(uint(2)))
			fx.ds.GetAppStoreAppVersionCountFunc = func(context.Context, *uint, automdm.VPPAppID, string) (uint, bool, error) {
				return 1, true, nil
			}
			_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(new(uint(2))))
			require.ErrorContains(t, err, `A version named "Default version" already exists`)
			require.Equal(t, http.StatusConflict, vppErrStatus(err))
			require.Empty(t, fx.seenAuth)
			require.False(t, fx.ds.InsertVPPAppWithTeamFuncInvoked)
		})

		t.Run("a first version without a token falls through to resolution", func(t *testing.T) {
			fx := withExisting(t, nil)
			_, err := fx.svc.AddAppStoreApp(fx.ctx, nil, iosApp(new(uint(1))))
			require.NoError(t, err)
			require.Equal(t, uint(1), *fx.inserted.VPPTokenID)
		})
	})
}

func TestUpdateVPPTokenTeamsValidation(t *testing.T) {
	t.Parallel()
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	setup := func(t *testing.T) (*Service, *mock.Store) {
		ds := new(mock.Store)
		ds.GetVPPTokenFunc = func(ctx context.Context, id uint) (*automdm.VPPTokenDB, error) {
			require.True(t, ctxdb.IsPrimaryRequired(ctx), "a just-uploaded token may not be on the replica yet")
			if id != 1 {
				return nil, common_mysql.NotFound("VPPToken")
			}
			return &automdm.VPPTokenDB{ID: 1}, nil
		}
		ds.TeamsSummaryFunc = func(context.Context) ([]*automdm.TeamSummary, error) {
			return []*automdm.TeamSummary{{ID: 1}, {ID: 2}}, nil
		}
		ds.UpdateVPPTokenTeamsFunc = func(_ context.Context, id uint, _ []uint) (*automdm.VPPTokenDB, error) {
			return &automdm.VPPTokenDB{ID: id}, nil
		}
		return newTestService(t, ds), ds
	}

	t.Run("missing token with teams", func(t *testing.T) {
		svc, ds := setup(t)
		_, err := svc.UpdateVPPTokenTeams(ctx, 99, []uint{1})
		require.ErrorContains(t, err, "Couldn't edit. VPP token with ID 99 doesn't exist.")
		require.Equal(t, http.StatusNotFound, vppErrStatus(err))
		require.False(t, ds.UpdateVPPTokenTeamsFuncInvoked)
	})

	t.Run("missing token with null teams", func(t *testing.T) {
		svc, ds := setup(t)
		_, err := svc.UpdateVPPTokenTeams(ctx, 99, nil)
		require.ErrorContains(t, err, "Couldn't edit. VPP token with ID 99 doesn't exist.")
		require.Equal(t, http.StatusNotFound, vppErrStatus(err))
		require.False(t, ds.UpdateVPPTokenTeamsFuncInvoked)
	})

	t.Run("missing team", func(t *testing.T) {
		svc, ds := setup(t)
		_, err := svc.UpdateVPPTokenTeams(ctx, 1, []uint{1, 7})
		require.ErrorContains(t, err, "Couldn't edit. AutoMDM with ID 7 doesn't exist.")
		require.Equal(t, http.StatusNotFound, vppErrStatus(err))
		require.False(t, ds.UpdateVPPTokenTeamsFuncInvoked)
	})

	t.Run("no team is always valid", func(t *testing.T) {
		svc, ds := setup(t)
		tok, err := svc.UpdateVPPTokenTeams(ctx, 1, []uint{0})
		require.NoError(t, err)
		require.Equal(t, uint(1), tok.ID)
		require.True(t, ds.UpdateVPPTokenTeamsFuncInvoked)
		require.False(t, ds.TeamsSummaryFuncInvoked, "teams are only loaded to check non-zero ids")
	})

	t.Run("existing teams and all teams", func(t *testing.T) {
		svc, _ := setup(t)
		_, err := svc.UpdateVPPTokenTeams(ctx, 1, []uint{1, 2})
		require.NoError(t, err)
		_, err = svc.UpdateVPPTokenTeams(ctx, 1, []uint{})
		require.NoError(t, err)
	})
}

func TestUpdateAppStoreAppActivityToken(t *testing.T) {
	t.Parallel()
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	run := func(t *testing.T, tokenID *uint, getToken func(context.Context, uint) (*automdm.VPPTokenDB, error)) (*mock.Store, *automdm.ActivityEditedAppStoreApp, error) {
		ds := new(mock.Store)
		svc := newTestService(t, ds)
		ds.GetAppStoreAppVersionsByTeamAndTitleIDFunc = func(context.Context, uint, uint) ([]*automdm.VPPAppStoreApp, error) {
			return []*automdm.VPPAppStoreApp{{
				AdamID:         "100",
				Platform:       automdm.IOSPlatform,
				Name:           "Canva",
				VPPAppsTeamsID: 1,
				VPPTokenID:     tokenID,
			}}, nil
		}
		ds.InsertVPPAppWithTeamFunc = func(_ context.Context, app *automdm.VPPApp, _ *uint, _ *uint) (*automdm.VPPApp, error) {
			return &automdm.VPPApp{AppTeamID: 1}, nil
		}
		ds.GetVPPTokenFunc = getToken

		_, act, err := svc.UpdateAppStoreApp(ctx, 5, nil, automdm.AppStoreAppUpdatePayload{SelfService: new(true)})
		return ds, act, err
	}
	setup := func(t *testing.T, tokenID *uint, getToken func(context.Context, uint) (*automdm.VPPTokenDB, error)) *automdm.ActivityEditedAppStoreApp {
		_, act, err := run(t, tokenID, getToken)
		require.NoError(t, err)
		return act
	}

	t.Run("carries the app's token", func(t *testing.T) {
		act := setup(t, new(uint(2)), func(_ context.Context, id uint) (*automdm.VPPTokenDB, error) {
			return &automdm.VPPTokenDB{ID: id, Location: "Acme Japan", CountryCode: "jp"}, nil
		})
		require.Equal(t, &automdm.ActivityVPPToken{ID: 2, Location: "Acme Japan", CountryCode: "jp"}, act.VPPToken)
	})

	t.Run("app without a token", func(t *testing.T) {
		act := setup(t, nil, func(context.Context, uint) (*automdm.VPPTokenDB, error) {
			t.Fatal("no token to look up")
			return nil, nil
		})
		require.Nil(t, act.VPPToken)
	})

	t.Run("deleted token", func(t *testing.T) {
		act := setup(t, new(uint(2)), func(context.Context, uint) (*automdm.VPPTokenDB, error) {
			return nil, common_mysql.NotFound("VPPToken")
		})
		require.Nil(t, act.VPPToken)
	})

	t.Run("token lookup failure doesn't write the edit", func(t *testing.T) {
		ds, _, err := run(t, new(uint(2)), func(context.Context, uint) (*automdm.VPPTokenDB, error) {
			return nil, errors.New("db down")
		})
		require.ErrorContains(t, err, "db down")
		require.False(t, ds.InsertVPPAppWithTeamFuncInvoked)
	})
}

func TestUpdateAppStoreAppActivityKeepsStoredSettings(t *testing.T) {
	t.Parallel()
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	ds := new(mock.Store)
	svc := newTestService(t, ds)
	calls := 0
	ds.GetAppStoreAppVersionsByTeamAndTitleIDFunc = func(context.Context, uint, uint) ([]*automdm.VPPAppStoreApp, error) {
		calls++
		app := &automdm.VPPAppStoreApp{AdamID: "100", Platform: automdm.IOSPlatform, Name: "Canva", VPPAppsTeamsID: 1}
		if calls == 1 {
			app.Configuration = json.RawMessage(`<dict></dict>`)
			app.LabelsIncludeAny = []automdm.SoftwareScopeLabel{{LabelID: 3, LabelName: "Staging"}}
		}
		// later reads simulate a lagging replica that still shows no labels or configuration
		return []*automdm.VPPAppStoreApp{app}, nil
	}
	ds.InsertVPPAppWithTeamFunc = func(_ context.Context, app *automdm.VPPApp, _ *uint, _ *uint) (*automdm.VPPApp, error) {
		return &automdm.VPPApp{AppTeamID: 1}, nil
	}
	ds.GetVPPTokenFunc = func(context.Context, uint) (*automdm.VPPTokenDB, error) { return nil, nil }

	// A partial edit (no labels, no configuration) still reports the stored ones, taken from the pre-update read.
	_, act, err := svc.UpdateAppStoreApp(ctx, 5, nil, automdm.AppStoreAppUpdatePayload{SelfService: new(true)})
	require.NoError(t, err)
	require.Equal(t, []automdm.ActivitySoftwareLabel{{ID: 3, Name: "Staging"}}, act.LabelsIncludeAny)
	require.JSONEq(t, `"<dict></dict>"`, string(act.Configuration))
}

const batchVPPTeamName = "📱 Mobile"

// batchVPPFixture wires BatchAssociateVPPApps for team 7 with fake Apple VPP
// and metadata servers. Assets are per token secret.
type batchVPPFixture struct {
	svc *Service
	ds  *mock.Store
	ctx context.Context

	// assets maps a token secret to the adam IDs it owns.
	assets map[string][]string
	// assetCalls holds the secret of every assets request, in order.
	assetCalls []string
	// assigned and existing are the team's current rows and their vpp_apps rows.
	assigned map[automdm.VPPAppID]automdm.VPPAppTeam
	existing map[automdm.VPPAppID]*automdm.VPPApp

	setApps  []automdm.VPPAppTeam
	titleIDs map[string]uint
	inserted []*automdm.VPPApp
}

func newBatchVPPFixture(t *testing.T, tokens ...*automdm.VPPTokenDB) *batchVPPFixture {
	t.Helper()
	fx := &batchVPPFixture{
		assets:   map[string][]string{"us-secret": {"1", "2"}, "jp-secret": {"3"}},
		assigned: map[automdm.VPPAppID]automdm.VPPAppTeam{},
		existing: map[automdm.VPPAppID]*automdm.VPPApp{},
	}

	vppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		fx.assetCalls = append(fx.assetCalls, secret)
		var assets []string
		for _, id := range fx.assets[secret] {
			assets = append(assets, `{"adamId":"`+id+`","pricingParam":"STDQ"}`)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assets":[` + strings.Join(assets, ",") + `]}`))
	}))
	t.Cleanup(vppSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", vppSrv.URL, t)

	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var data []string
		for _, id := range []string{"1", "2", "3"} {
			data = append(data, `{"id":"`+id+`","attributes":{"name":"App `+id+`","deviceFamilies":["iphone","ipad"],`+
				`"platformAttributes":{"ios":{"bundleId":"com.example.`+id+`","artwork":{"url":"https://example.test/icon.png"},`+
				`"latestVersionInfo":{"versionDisplay":"1.0"}}}}}`)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[` + strings.Join(data, ",") + `]}`))
	}))
	t.Cleanup(metaSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_STOKEN_AUTHENTICATED_APPS_URL", metaSrv.URL, t)

	ds := new(mock.Store)
	svc := newTestService(t, ds)
	svc.config = config.AutoMDMConfig{MDM: config.MDMConfig{AppleConnectJWT: "test-jwt"}}

	ds.TeamByNameFunc = func(context.Context, string) (*automdm.Team, error) {
		return &automdm.Team{ID: 7, Name: batchVPPTeamName}, nil
	}
	ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) { return tokens, nil }
	ds.GetAssignedVPPAppsFunc = func(context.Context, *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
		return fx.assigned, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(context.Context, uint, []string) (map[string]uint, error) { return nil, nil }
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(context.Context, []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	ds.GetVPPAppByAdamIDPlatformFunc = func(_ context.Context, adamID string, platform automdm.InstallableDevicePlatform) (*automdm.VPPApp, error) {
		if app, ok := fx.existing[automdm.VPPAppID{AdamID: adamID, Platform: platform}]; ok {
			return app, nil
		}
		return nil, common_mysql.NotFound("VPPApp")
	}
	ds.GetVPPTokenOwningAppInCountryFunc = func(_ context.Context, _ string, _ automdm.InstallableDevicePlatform, country string) (*automdm.VPPTokenDB, error) {
		for _, tok := range tokens {
			if tok.CountryCode == country {
				return tok, nil
			}
		}
		return nil, common_mysql.NotFound("VPPToken")
	}
	ds.BatchInsertVPPAppsFunc = func(_ context.Context, apps []*automdm.VPPApp) error {
		for i, app := range apps {
			app.TitleID = uint(100 + i)
		}
		fx.inserted = apps
		return nil
	}
	ds.SetTeamVPPAppsFunc = func(_ context.Context, _ *uint, apps []automdm.VPPAppTeam, titleIDs map[string]uint) (bool, error) {
		fx.setApps = apps
		fx.titleIDs = titleIDs
		return false, nil
	}
	ds.DeleteIconsAssociatedWithTitlesWithoutInstallersFunc = func(context.Context, uint) error { return nil }
	ds.GetVPPAppsFunc = func(context.Context, *uint) ([]automdm.VPPAppResponse, error) { return nil, nil }
	ds.GetEnterpriseFunc = func(context.Context) (*android.Enterprise, error) { return nil, common_mysql.NotFound("Enterprise") }

	fx.svc = svc
	fx.ds = ds
	ctx := authz_ctx.NewContext(t.Context(), &authz_ctx.AuthorizationContext{})
	fx.ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	return fx
}

func (fx *batchVPPFixture) apply(dryRun bool, payloads ...automdm.VPPBatchPayload) error {
	_, _, err := fx.svc.BatchAssociateVPPApps(fx.ctx, batchVPPTeamName, payloads, dryRun)
	return err
}

// boundTokens returns the token each app was passed to SetTeamVPPApps with.
func (fx *batchVPPFixture) boundTokens() map[automdm.VPPAppID]uint {
	bound := make(map[automdm.VPPAppID]uint, len(fx.setApps))
	for _, app := range fx.setApps {
		bound[app.VPPAppID] = *app.VPPTokenID
	}
	return bound
}

func batchUSToken() *automdm.VPPTokenDB {
	return &automdm.VPPTokenDB{ID: 1, Location: "Acme US", Token: "us-secret", RenewDate: time.Now().Add(24 * time.Hour), CountryCode: "us", Teams: []automdm.TeamTuple{{ID: 7}}}
}

func batchJPToken() *automdm.VPPTokenDB {
	return &automdm.VPPTokenDB{ID: 2, Location: "Acme Japan", Token: "jp-secret", RenewDate: time.Now().Add(24 * time.Hour), CountryCode: "jp", Teams: []automdm.TeamTuple{{ID: 7}}}
}

func batchIOS(adamID, location string) automdm.VPPBatchPayload {
	return automdm.VPPBatchPayload{
		AppStoreID:       adamID,
		Platform:         automdm.IOSPlatform,
		Location:         location,
		LabelsExcludeAny: []string{},
		LabelsIncludeAny: []string{},
		LabelsIncludeAll: []string{},
		Categories:       []string{},
	}
}

func batchAppID(adamID string) automdm.VPPAppID {
	return automdm.VPPAppID{AdamID: adamID, Platform: automdm.IOSPlatform}
}

func TestBatchAssociateVPPAppsTokenResolution(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	t.Run("binds the first-added token, or the located one", func(t *testing.T) {
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken())
		require.NoError(t, fx.apply(false, batchIOS("1", ""), batchIOS("3", "Acme Japan")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("1"): 1, batchAppID("3"): 2}, fx.boundTokens())
		require.ElementsMatch(t, []string{"us-secret", "jp-secret"}, fx.assetCalls, "one assets call per token")
		require.Len(t, fx.inserted, 2)
	})

	t.Run("an existing app keeps its token without a location", func(t *testing.T) {
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken())
		fx.assigned[batchAppID("3")] = automdm.VPPAppTeam{VPPTokenID: new(uint(2))}
		fx.existing[batchAppID("3")] = &automdm.VPPApp{Name: "App 3", TitleID: 33, CountryCode: "jp"}
		require.NoError(t, fx.apply(false, batchIOS("3", "")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("3"): 2}, fx.boundTokens())
		require.Equal(t, []string{"jp-secret"}, fx.assetCalls)
	})

	t.Run("an existing app accepts its own location", func(t *testing.T) {
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken())
		fx.assigned[batchAppID("3")] = automdm.VPPAppTeam{VPPTokenID: new(uint(2))}
		fx.existing[batchAppID("3")] = &automdm.VPPApp{Name: "App 3", TitleID: 33, CountryCode: "jp"}
		require.NoError(t, fx.apply(false, batchIOS("3", "Acme Japan")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("3"): 2}, fx.boundTokens())
	})

	t.Run("a new version lands on the token of the app", func(t *testing.T) {
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken())
		fx.assigned[batchAppID("3")] = automdm.VPPAppTeam{VPPTokenID: new(uint(2))}
		fx.existing[batchAppID("3")] = &automdm.VPPApp{Name: "App 3", TitleID: 33, CountryCode: "jp"}
		v1, v2 := batchIOS("3", ""), batchIOS("3", "")
		v1.VersionName, v2.VersionName = "Stable", "Beta"
		require.NoError(t, fx.apply(false, v1, v2))
		require.Len(t, fx.setApps, 2)
		for _, app := range fx.setApps {
			require.Equal(t, uint(2), *app.VPPTokenID)
		}
	})

	t.Run("a third token assigned to the team leaves apps on their tokens", func(t *testing.T) {
		third := &automdm.VPPTokenDB{ID: 3, Location: "Acme EU", Token: "eu-secret", RenewDate: time.Now().Add(24 * time.Hour), CountryCode: "de", Teams: []automdm.TeamTuple{{ID: 7}}}
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken(), third)
		fx.assigned[batchAppID("1")] = automdm.VPPAppTeam{VPPTokenID: new(uint(1))}
		fx.assigned[batchAppID("3")] = automdm.VPPAppTeam{VPPTokenID: new(uint(2))}
		fx.existing[batchAppID("1")] = &automdm.VPPApp{Name: "App 1", TitleID: 11, CountryCode: "us"}
		fx.existing[batchAppID("3")] = &automdm.VPPApp{Name: "App 3", TitleID: 33, CountryCode: "jp"}
		require.NoError(t, fx.apply(false, batchIOS("1", ""), batchIOS("3", "")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("1"): 1, batchAppID("3"): 2}, fx.boundTokens())
	})

	t.Run("first-added is the lowest id among the team's and All teams tokens", func(t *testing.T) {
		token := func(id uint, teams []automdm.TeamTuple) *automdm.VPPTokenDB {
			secret := "token-" + strings.Repeat("x", int(id))
			return &automdm.VPPTokenDB{ID: id, Location: secret, Token: secret, RenewDate: time.Now().Add(24 * time.Hour), CountryCode: "us", Teams: teams}
		}
		for _, tt := range []struct {
			name   string
			tokens []*automdm.VPPTokenDB
			want   uint
		}{
			{
				name:   "All teams token below the automdm token",
				tokens: []*automdm.VPPTokenDB{token(1, []automdm.TeamTuple{{ID: 9}}), token(2, []automdm.TeamTuple{}), token(3, []automdm.TeamTuple{{ID: 7}})},
				want:   2,
			},
			{
				name:   "moving the lowest token to All teams keeps it first",
				tokens: []*automdm.VPPTokenDB{token(1, []automdm.TeamTuple{}), token(2, []automdm.TeamTuple{{ID: 9}}), token(3, []automdm.TeamTuple{{ID: 7}})},
				want:   1,
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				fx := newBatchVPPFixture(t, tt.tokens...)
				fx.assets = map[string][]string{}
				for _, tok := range tt.tokens {
					fx.assets[tok.Token] = []string{"1"}
				}
				require.NoError(t, fx.apply(false, batchIOS("1", "")))
				require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("1"): tt.want}, fx.boundTokens())
			})
		}
	})
}

func TestBatchAssociateVPPAppsTokenErrors(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	expired := time.Now().Add(-24 * time.Hour)
	missingLine := func(adamID, location string) string {
		return `Couldn't apply. App "` + adamID + `" isn't available in the "` + location + `" VPP token. Please purchase a license in Apple Business and try again.`
	}
	androidPayload := automdm.VPPBatchPayload{AppStoreID: "com.example.app", Platform: automdm.AndroidPlatform, Location: "Acme Japan"}

	tests := []struct {
		name     string
		tokens   func() (us, jp *automdm.VPPTokenDB)
		assigned map[automdm.VPPAppID]automdm.VPPAppTeam
		payloads []automdm.VPPBatchPayload
		want     string
		status   int
		// dryRunPasses is set for the one error a dry run skips.
		dryRunPasses bool
	}{
		{
			name:     "location doesn't match a token",
			payloads: []automdm.VPPBatchPayload{batchIOS("1", "Nowhere")},
			want:     `Couldn't apply. VPP token "Nowhere" doesn't exist.`,
			status:   http.StatusUnprocessableEntity,
		},
		{
			name: "location token isn't assigned to the team",
			tokens: func() (*automdm.VPPTokenDB, *automdm.VPPTokenDB) {
				jp := batchJPToken()
				jp.Teams = []automdm.TeamTuple{{ID: 9}}
				return batchUSToken(), jp
			},
			payloads:     []automdm.VPPBatchPayload{batchIOS("3", "Acme Japan")},
			want:         `Couldn't apply. VPP token "Acme Japan" isn't assigned to team "📱 Mobile". Add it under "org_settings.mdm.volume_purchasing_program".`,
			status:       http.StatusUnprocessableEntity,
			dryRunPasses: true,
		},
		{
			name:     "no location and the app isn't in the first-added token",
			payloads: []automdm.VPPBatchPayload{batchIOS("3", "")},
			want:     missingLine("3", "Acme US"),
			status:   http.StatusUnprocessableEntity,
		},
		{
			name:     "location and the app isn't in that token",
			payloads: []automdm.VPPBatchPayload{batchIOS("1", "Acme Japan")},
			want:     missingLine("1", "Acme Japan"),
			status:   http.StatusUnprocessableEntity,
		},
		{
			name: "one line per missing app, sorted, once per app across platforms",
			payloads: []automdm.VPPBatchPayload{
				{AppStoreID: "9", Categories: []string{}},
				batchIOS("3", ""),
				batchIOS("10", ""),
			},
			want:   strings.Join([]string{missingLine("10", "Acme US"), missingLine("3", "Acme US"), missingLine("9", "Acme US")}, "\n"),
			status: http.StatusUnprocessableEntity,
		},
		{
			name: "first-added token is expired for a new app",
			tokens: func() (*automdm.VPPTokenDB, *automdm.VPPTokenDB) {
				us := batchUSToken()
				us.RenewDate = expired
				return us, batchJPToken()
			},
			payloads: []automdm.VPPBatchPayload{batchIOS("1", "")},
			want:     `Couldn't apply. VPP token "Acme US" is expired. Please renew it and try again.`,
			status:   http.StatusUnprocessableEntity,
		},
		{
			name: "location token is expired for a new app",
			tokens: func() (*automdm.VPPTokenDB, *automdm.VPPTokenDB) {
				jp := batchJPToken()
				jp.RenewDate = expired
				return batchUSToken(), jp
			},
			payloads: []automdm.VPPBatchPayload{batchIOS("3", "Acme Japan")},
			want:     `Couldn't apply. VPP token "Acme Japan" is expired. Please renew it and try again.`,
			status:   http.StatusUnprocessableEntity,
		},
		{
			name:     "location differs from the token of an app that's already added",
			assigned: map[automdm.VPPAppID]automdm.VPPAppTeam{batchAppID("1"): {VPPTokenID: new(uint(1))}},
			payloads: []automdm.VPPBatchPayload{batchIOS("1", "Acme Japan")},
			want:     `Couldn't apply. Canva is already added from the "Acme US" VPP token. To add it from "Acme Japan", remove it, run GitOps, and then add it back with the new location.`,
			status:   http.StatusConflict,
		},
		{
			name: "no token available to the team",
			tokens: func() (*automdm.VPPTokenDB, *automdm.VPPTokenDB) {
				us, jp := batchUSToken(), batchJPToken()
				us.Teams, jp.Teams = []automdm.TeamTuple{{ID: 9}}, nil
				return us, jp
			},
			payloads: []automdm.VPPBatchPayload{batchIOS("1", "")},
			want:     "No available VPP Token",
			status:   http.StatusUnprocessableEntity,
		},
		{
			name:     "same app and platform with different locations",
			payloads: []automdm.VPPBatchPayload{batchIOS("3", "Acme Japan"), batchIOS("3", "")},
			want:     `Couldn't apply. App "3" is specified more than once with different location values.`,
			status:   http.StatusUnprocessableEntity,
		},
		{
			name:     "location on an Android app",
			payloads: []automdm.VPPBatchPayload{androidPayload},
			want:     `Couldn't apply. location isn't supported for Android apps.`,
			status:   http.StatusUnprocessableEntity,
		},
	}
	for _, tt := range tests {
		for _, dryRun := range []bool{false, true} {
			name := tt.name + " real run"
			if dryRun {
				name = tt.name + " dry run"
			}
			t.Run(name, func(t *testing.T) {
				tokens := func() (*automdm.VPPTokenDB, *automdm.VPPTokenDB) { return batchUSToken(), batchJPToken() }
				if tt.tokens != nil {
					tokens = tt.tokens
				}
				us, jp := tokens()
				fx := newBatchVPPFixture(t, us, jp)
				maps.Copy(fx.assigned, tt.assigned)
				for id := range tt.assigned {
					fx.existing[id] = &automdm.VPPApp{Name: "Canva", TitleID: 11, CountryCode: "us"}
				}

				err := fx.apply(dryRun, tt.payloads...)
				if dryRun && tt.dryRunPasses {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.want)
					require.Equal(t, tt.status, vppErrStatus(err))
				}
				require.False(t, fx.ds.SetTeamVPPAppsFuncInvoked)
			})
		}
	}

	t.Run("each missing app is reported once", func(t *testing.T) {
		fx := newBatchVPPFixture(t, batchUSToken(), batchJPToken())
		err := fx.apply(false, automdm.VPPBatchPayload{AppStoreID: "9", Categories: []string{}})
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(err.Error(), `App "9"`))
	})
}

func TestBatchAssociateVPPAppsExistingAppOnExpiredToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	setup := func(t *testing.T) *batchVPPFixture {
		jp := batchJPToken()
		jp.RenewDate = time.Now().Add(-24 * time.Hour)
		fx := newBatchVPPFixture(t, batchUSToken(), jp)
		fx.assigned[batchAppID("3")] = automdm.VPPAppTeam{VPPTokenID: new(uint(2))}
		fx.existing[batchAppID("3")] = &automdm.VPPApp{Name: "App 3", TitleID: 33, CountryCode: "jp"}
		return fx
	}

	t.Run("survives a run next to a new app", func(t *testing.T) {
		fx := setup(t)
		require.NoError(t, fx.apply(false, batchIOS("1", ""), batchIOS("3", "")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("1"): 1, batchAppID("3"): 2}, fx.boundTokens(),
			"SetTeamVPPApps removes any app that isn't passed to it")
		require.Equal(t, []string{"us-secret"}, fx.assetCalls, "the expired token isn't read")
		require.Len(t, fx.inserted, 1, "metadata of the expired app is kept")
		require.Equal(t, uint(33), fx.titleIDs[batchAppID("3").String()])
		require.Equal(t, uint(100), fx.titleIDs[batchAppID("1").String()])
	})

	t.Run("survives a run with no other app", func(t *testing.T) {
		fx := setup(t)
		require.NoError(t, fx.apply(false, batchIOS("3", "")))
		require.Equal(t, map[automdm.VPPAppID]uint{batchAppID("3"): 2}, fx.boundTokens())
		require.False(t, fx.ds.BatchInsertVPPAppsFuncInvoked)
		require.Empty(t, fx.assetCalls)
	})

	t.Run("dry run doesn't fail or read the expired token", func(t *testing.T) {
		fx := setup(t)
		require.NoError(t, fx.apply(true, batchIOS("1", ""), batchIOS("3", "")))
		require.Equal(t, []string{"us-secret"}, fx.assetCalls)
	})

	t.Run("an app moved to the expired token's location still fails", func(t *testing.T) {
		fx := setup(t)
		err := fx.apply(false, batchIOS("1", "Acme Japan"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme Japan" is expired. Please renew it and try again.`)
	})
}

func TestBatchAssociateVPPAppsExpandedPlatformsOnExpiredToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	setup := func(t *testing.T) *batchVPPFixture {
		us := batchUSToken()
		us.RenewDate = time.Now().Add(-24 * time.Hour)
		fx := newBatchVPPFixture(t, us, batchJPToken())
		// An iOS-only app has no macOS row.
		for _, platform := range []automdm.InstallableDevicePlatform{automdm.IOSPlatform, automdm.IPadOSPlatform} {
			id := automdm.VPPAppID{AdamID: "1", Platform: platform}
			fx.assigned[id] = automdm.VPPAppTeam{VPPTokenID: new(uint(1))}
			fx.existing[id] = &automdm.VPPApp{Name: "App 1", TitleID: 11, CountryCode: "us"}
		}
		return fx
	}
	noPlatform := func(adamID string) automdm.VPPBatchPayload {
		return automdm.VPPBatchPayload{AppStoreID: adamID, Categories: []string{}}
	}

	t.Run("an app with no platform keeps its rows", func(t *testing.T) {
		fx := setup(t)
		require.NoError(t, fx.apply(false, noPlatform("1")))
		require.Equal(t, map[automdm.VPPAppID]uint{
			{AdamID: "1", Platform: automdm.IOSPlatform}:    1,
			{AdamID: "1", Platform: automdm.IPadOSPlatform}: 1,
		}, fx.boundTokens())
	})

	t.Run("an app with no platform and a location keeps its rows", func(t *testing.T) {
		fx := setup(t)
		located := noPlatform("1")
		located.Location = "Acme US"
		require.NoError(t, fx.apply(false, located))
		require.Equal(t, map[automdm.VPPAppID]uint{
			{AdamID: "1", Platform: automdm.IOSPlatform}:    1,
			{AdamID: "1", Platform: automdm.IPadOSPlatform}: 1,
		}, fx.boundTokens())
	})

	t.Run("a new app on the expired token still fails", func(t *testing.T) {
		fx := setup(t)
		err := fx.apply(false, noPlatform("2"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme US" is expired. Please renew it and try again.`)
	})

	t.Run("an explicit platform still fails", func(t *testing.T) {
		fx := setup(t)
		macOS := noPlatform("1")
		macOS.Platform = automdm.MacOSPlatform
		err := fx.apply(false, macOS)
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme US" is expired. Please renew it and try again.`)
	})
}

func TestBatchAssociateVPPAppsExpandedPlatformsInheritToken(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	// App 3 is iOS-only and sits on the Japan token, which isn't the first-added one.
	setup := func(t *testing.T, jp *automdm.VPPTokenDB) *batchVPPFixture {
		fx := newBatchVPPFixture(t, batchUSToken(), jp)
		for i, platform := range []automdm.InstallableDevicePlatform{automdm.IOSPlatform, automdm.IPadOSPlatform} {
			id := automdm.VPPAppID{AdamID: "3", Platform: platform}
			fx.assigned[id] = automdm.VPPAppTeam{AppTeamID: uint(5 + i), VPPTokenID: new(uint(2))}
			fx.existing[id] = &automdm.VPPApp{Name: "App 3", TitleID: 13, CountryCode: "jp"}
		}
		return fx
	}
	noPlatform := func(adamID string) automdm.VPPBatchPayload {
		return automdm.VPPBatchPayload{AppStoreID: adamID, Categories: []string{}}
	}
	want := map[automdm.VPPAppID]uint{
		{AdamID: "3", Platform: automdm.IOSPlatform}:    2,
		{AdamID: "3", Platform: automdm.IPadOSPlatform}: 2,
	}

	t.Run("an app with no platform and no location stays on its token", func(t *testing.T) {
		fx := setup(t, batchJPToken())
		require.NoError(t, fx.apply(false, noPlatform("3")))
		require.Equal(t, want, fx.boundTokens())
		require.Equal(t, []string{"jp-secret"}, fx.assetCalls)
	})

	t.Run("the token is expired", func(t *testing.T) {
		jp := batchJPToken()
		jp.RenewDate = time.Now().Add(-24 * time.Hour)
		fx := setup(t, jp)
		require.NoError(t, fx.apply(false, noPlatform("3")))
		require.Equal(t, want, fx.boundTokens())
		require.Empty(t, fx.assetCalls)
	})

	t.Run("a new app uses the first-added token", func(t *testing.T) {
		fx := setup(t, batchJPToken())
		require.NoError(t, fx.apply(false, noPlatform("2")))
		require.Equal(t, uint(1), fx.boundTokens()[automdm.VPPAppID{AdamID: "2", Platform: automdm.IOSPlatform}])
	})

	t.Run("a new app fails when the first-added token is expired", func(t *testing.T) {
		us := batchUSToken()
		us.RenewDate = time.Now().Add(-24 * time.Hour)
		fx := newBatchVPPFixture(t, us, batchJPToken())
		err := fx.apply(false, noPlatform("2"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme US" is expired. Please renew it and try again.`)
	})
}

func TestBatchAssociateVPPAppsDryRunNewTeamLocation(t *testing.T) {
	// dev_mode.SetOverride uses t.Setenv, which is incompatible with t.Parallel.

	setup := func(t *testing.T, tokens ...*automdm.VPPTokenDB) *batchVPPFixture {
		fx := newBatchVPPFixture(t, tokens...)
		fx.ds.TeamByNameFunc = func(context.Context, string) (*automdm.Team, error) { return nil, common_mysql.NotFound("Team") }
		return fx
	}

	t.Run("a located app is checked against that token only", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		require.NoError(t, fx.apply(true, batchIOS("3", "Acme Japan")))
		require.Equal(t, []string{"jp-secret"}, fx.assetCalls)
	})

	t.Run("a located app missing from its token", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		// The US token has app 1, but the location names Japan.
		err := fx.apply(true, batchIOS("1", "Acme Japan"))
		require.ErrorContains(t, err, `Couldn't apply. App "1" isn't available in the "Acme Japan" VPP token. Please purchase a license in Apple Business and try again.`)
		require.Equal(t, http.StatusUnprocessableEntity, vppErrStatus(err))
		require.Equal(t, []string{"jp-secret"}, fx.assetCalls)
	})

	t.Run("a location that doesn't exist", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		err := fx.apply(true, batchIOS("1", "Nowhere"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Nowhere" doesn't exist.`)
		require.Empty(t, fx.assetCalls)
	})

	t.Run("a location with no tokens uploaded", func(t *testing.T) {
		fx := setup(t)
		err := fx.apply(true, batchIOS("1", "Acme Japan"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme Japan" doesn't exist.`)
	})

	t.Run("an expired location token", func(t *testing.T) {
		jp := batchJPToken()
		jp.RenewDate = time.Now().Add(-24 * time.Hour)
		fx := setup(t, batchUSToken(), jp)
		err := fx.apply(true, batchIOS("3", "Acme Japan"))
		require.ErrorContains(t, err, `Couldn't apply. VPP token "Acme Japan" is expired. Please renew it and try again.`)
		require.Empty(t, fx.assetCalls)
	})

	t.Run("apps without a location keep the union check", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		// Each app is on some token, so the union passes. App 3 is only on the Japan token.
		require.NoError(t, fx.apply(true, batchIOS("2", ""), batchIOS("3", "")))

		err := fx.apply(true, batchIOS("9", ""))
		require.ErrorContains(t, err, `Couldn't apply. App "9" isn't available in any VPP token. Please purchase a license in Apple Business and try again.`)
	})

	t.Run("located and unlocated apps are both checked", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		err := fx.apply(true, batchIOS("3", "Acme Japan"), batchIOS("9", ""))
		require.ErrorContains(t, err, `Couldn't apply. App "9" isn't available in any VPP token. Please purchase a license in Apple Business and try again.`)
		require.ElementsMatch(t, []string{"jp-secret", "us-secret"}, fx.assetCalls, "one assets call per token")
	})

	t.Run("a location on an Android app", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		err := fx.apply(true, automdm.VPPBatchPayload{AppStoreID: "com.example.app", Platform: automdm.AndroidPlatform, Location: "Acme Japan"})
		require.ErrorContains(t, err, "Couldn't apply. location isn't supported for Android apps.")
		require.False(t, fx.ds.ListVPPTokensFuncInvoked)
	})

	t.Run("one app with different locations", func(t *testing.T) {
		fx := setup(t, batchUSToken(), batchJPToken())
		err := fx.apply(true, batchIOS("3", "Acme Japan"), batchIOS("3", ""))
		require.ErrorContains(t, err, `Couldn't apply. App "3" is specified more than once with different location values.`)
		require.False(t, fx.ds.ListVPPTokensFuncInvoked)
	})
}

func TestResolveBatchVPPTokenStepOrder(t *testing.T) {
	t.Parallel()
	us, jp := batchUSToken(), batchJPToken()
	expiredUS := batchUSToken()
	expiredUS.RenewDate = time.Now().Add(-24 * time.Hour)
	all := []*automdm.VPPTokenDB{us, jp}

	tests := []struct {
		name       string
		teamTokens []*automdm.VPPTokenDB
		allTokens  []*automdm.VPPTokenDB
		existing   *uint
		location   string
		dryRun     bool
		wantID     uint
		wantErr    string
	}{
		{name: "unknown location beats an existing app", teamTokens: all, allTokens: all, existing: new(uint(1)), location: "Nowhere", wantErr: "doesn't exist"},
		{name: "unassigned beats the already-added error", teamTokens: []*automdm.VPPTokenDB{us}, allTokens: all, existing: new(uint(1)), location: "Acme Japan", wantErr: "isn't assigned to team"},
		{name: "a dry run skips the unassigned check", teamTokens: []*automdm.VPPTokenDB{us}, allTokens: all, location: "Acme Japan", dryRun: true, wantID: 2},
		{name: "a dry run still reaches the already-added error", teamTokens: []*automdm.VPPTokenDB{us}, allTokens: all, existing: new(uint(1)), location: "Acme Japan", dryRun: true, wantErr: "already added"},
		{name: "already added beats expiry", teamTokens: []*automdm.VPPTokenDB{expiredUS, jp}, allTokens: []*automdm.VPPTokenDB{expiredUS, jp}, existing: new(uint(1)), location: "Acme Japan", wantErr: "already added"},
		{name: "an existing app on an expired token passes", teamTokens: []*automdm.VPPTokenDB{expiredUS}, allTokens: []*automdm.VPPTokenDB{expiredUS}, existing: new(uint(1)), wantID: 1},
		{name: "an existing app outside the team's tokens keeps its token", teamTokens: []*automdm.VPPTokenDB{us}, allTokens: all, existing: new(uint(2)), wantID: 2},
		{name: "a new app on an expired token fails", teamTokens: []*automdm.VPPTokenDB{expiredUS}, allTokens: []*automdm.VPPTokenDB{expiredUS}, wantErr: "is expired"},
		{name: "a new app takes the first token", teamTokens: all, allTokens: all, wantID: 1},
		{name: "the location is compared after NFC normalization", teamTokens: all, allTokens: all, location: "Acme Japan", wantID: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := resolveBatchVPPToken(tt.teamTokens, tt.allTokens, tt.existing, tt.location, batchVPPTeamName, tt.dryRun)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantID, tok.ID)
		})
	}
}

// testAppStoreAppDatastore keeps, in memory, the state the App Store app configuration reconciler reads and the installs it
// writes. Tests change the state the way AutoMDM does, run the cron and check which version each host was sent.
type testAppStoreAppDatastore struct {
	ds  *mock.Store
	now time.Time

	hosts            []*automdm.Host
	mdmOffHostIDs    map[uint]struct{}
	inventoryHostIDs map[uint]struct{}
	labelIDsByHostID map[uint]map[uint]struct{}
	versionsByTeamID map[uint][]*automdm.VPPAppStoreApp
	tokenByTeamID    map[uint]*automdm.VPPTokenDB
	cursor           uint

	installs              []automdm.HostVPPSoftwareInstall
	failedInstalls        []automdm.HostVPPSoftwareInstall
	queuedVersionByHostID map[uint]uint
	queueInstalls         bool
	rejectedHostIDs       map[uint]struct{}
	sentVersionByHostID   map[uint]uint

	retriesByHostAndVersion map[[2]uint]int
}

func (f *testAppStoreAppDatastore) RecordAttempt(ctx context.Context, hostID uint, versionID uint, expireIn time.Duration) (int, error) {
	f.retriesByHostAndVersion[[2]uint{hostID, versionID}]++
	return f.retriesByHostAndVersion[[2]uint{hostID, versionID}], nil
}

func (f *testAppStoreAppDatastore) CountAttempts(ctx context.Context, hostID uint, versionID uint) (int, error) {
	return f.retriesByHostAndVersion[[2]uint{hostID, versionID}], nil
}

func (f *testAppStoreAppDatastore) ResetAttempts(ctx context.Context, hostID uint, versionID uint) error {
	delete(f.retriesByHostAndVersion, [2]uint{hostID, versionID})
	return nil
}

func (f *testAppStoreAppDatastore) ResetInstallerAttempts(ctx context.Context, versionIDs []uint) error {
	return nil
}

func (f *testAppStoreAppDatastore) CheckCanInstallVPPApps(ctx context.Context, appleDevice bool, host *automdm.Host) error {
	return nil
}

func (f *testAppStoreAppDatastore) InstallVPPAppPostValidation(ctx context.Context, host *automdm.Host, vppApp *automdm.VPPApp, opts automdm.HostSoftwareInstallOptions) (string, error) {
	if host == nil || vppApp == nil {
		return "", errors.New("missing host or app")
	}
	if _, rejected := f.rejectedHostIDs[host.ID]; rejected {
		return "", errors.New("Apple rejected the install")
	}
	f.sentVersionByHostID[host.ID] = vppApp.AppTeamID
	if f.queueInstalls {
		f.queuedVersionByHostID[host.ID] = vppApp.AppTeamID
		return "command-uuid", nil
	}
	f.installs = append(f.installs, automdm.HostVPPSoftwareInstall{HostID: host.ID, VPPAppTeamID: new(vppApp.AppTeamID), CreatedAt: f.now})
	return "command-uuid", nil
}

func TestReconcileAppStoreAppConfigurations(t *testing.T) {
	ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	nothingSent := map[uint]uint{}

	// tick moves the test datastore's clock forward a second, so each change and install gets its own whole second like in MySQL
	tick := func(f *testAppStoreAppDatastore) {
		f.now = f.now.Add(time.Second)
	}
	hostByID := func(f *testAppStoreAppDatastore, hostID uint) *automdm.Host {
		for _, host := range f.hosts {
			if host.ID == hostID {
				return host
			}
		}
		return &automdm.Host{}
	}
	versionByID := func(f *testAppStoreAppDatastore, versionID uint) *automdm.VPPAppStoreApp {
		for _, versions := range f.versionsByTeamID {
			for _, version := range versions {
				if version.VPPAppsTeamsID == versionID {
					return version
				}
			}
		}
		return nil
	}
	inScope := func(f *testAppStoreAppDatastore, hostID uint, version *automdm.VPPAppStoreApp) bool {
		if version == nil {
			return false
		}
		labelIDs := f.labelIDsByHostID[hostID]
		for _, label := range version.LabelsExcludeAny {
			if _, member := labelIDs[label.LabelID]; member {
				return false
			}
		}
		for _, label := range version.LabelsIncludeAll {
			if _, member := labelIDs[label.LabelID]; !member {
				return false
			}
		}
		if len(version.LabelsIncludeAny) == 0 {
			return true
		}
		for _, label := range version.LabelsIncludeAny {
			if _, member := labelIDs[label.LabelID]; member {
				return true
			}
		}
		return false
	}

	newTestAppStoreAppDatastore := func() *testAppStoreAppDatastore {
		f := &testAppStoreAppDatastore{
			ds:                    new(mock.Store),
			now:                   time.Now().Add(-24 * time.Hour).Truncate(time.Second),
			mdmOffHostIDs:         map[uint]struct{}{},
			inventoryHostIDs:      map[uint]struct{}{},
			labelIDsByHostID:      map[uint]map[uint]struct{}{},
			versionsByTeamID:      map[uint][]*automdm.VPPAppStoreApp{},
			tokenByTeamID:         map[uint]*automdm.VPPTokenDB{},
			queuedVersionByHostID: map[uint]uint{},
			rejectedHostIDs:       map[uint]struct{}{},

			retriesByHostAndVersion: map[[2]uint]int{},
		}
		f.ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		f.ds.GetAppStoreAppConfigurationReconcileCursorFunc = func(ctx context.Context) (uint, error) {
			return f.cursor, nil
		}
		f.ds.SetAppStoreAppConfigurationReconcileCursorFunc = func(ctx context.Context, cursor uint) error {
			f.cursor = cursor
			return nil
		}
		f.ds.ListIOSAndIPadOSMDMHostsAfterIDFunc = func(ctx context.Context, afterHostID uint, limit int) ([]*automdm.Host, error) {
			var page []*automdm.Host
			for _, host := range f.hosts {
				if _, mdmOff := f.mdmOffHostIDs[host.ID]; mdmOff || host.ID <= afterHostID || len(page) == limit {
					continue
				}
				page = append(page, host)
			}
			return page, nil
		}
		f.ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
			var tokens []*automdm.VPPTokenDB
			for _, token := range f.tokenByTeamID {
				tokens = append(tokens, token)
			}
			return tokens, nil
		}
		f.ds.GetAppStoreAppVersionsByTeamAndTitleFunc = func(ctx context.Context, platforms []automdm.InstallableDevicePlatform) (automdm.AppStoreAppVersionsByTeamAndTitleID, error) {
			versionsByTitleIDByTeamID := make(automdm.AppStoreAppVersionsByTeamAndTitleID)
			for automdmID, versions := range f.versionsByTeamID {
				for _, version := range versions {
					// A version is added from its team's token unless a test picked another one
					if token, ok := f.tokenByTeamID[automdmID]; ok && version.VPPTokenID == nil {
						version.VPPTokenID = new(token.ID)
					}
					version.HasLabels = len(version.LabelsIncludeAny) > 0 || len(version.LabelsExcludeAny) > 0 || len(version.LabelsIncludeAll) > 0
					if versionsByTitleIDByTeamID[automdmID] == nil {
						versionsByTitleIDByTeamID[automdmID] = make(map[uint][]*automdm.VPPAppStoreApp)
					}
					versionsByTitleIDByTeamID[automdmID][1] = append(versionsByTitleIDByTeamID[automdmID][1], version)
				}
			}
			return versionsByTitleIDByTeamID, nil
		}
		f.ds.ListHostAppStoreAppInstallVersionsFunc = func(ctx context.Context, automdmID uint, hostIDs []uint, platforms []automdm.InstallableDevicePlatform) (map[uint]map[uint]automdm.HostVPPInstallWithUpcoming, error) {
			installByHostID := make(map[uint]automdm.HostVPPInstallWithUpcoming)
			for _, hostID := range hostIDs {
				if _, inInventory := f.inventoryHostIDs[hostID]; !inInventory || ptr.ValOrZero(hostByID(f, hostID).TeamID) != automdmID {
					continue
				}
				for _, install := range f.installs {
					if install.HostID == hostID {
						installByHostID[hostID] = automdm.HostVPPInstallWithUpcoming{HostVPPSoftwareInstall: install}
					}
				}
				install, installed := installByHostID[hostID]
				queuedVersionID, queued := f.queuedVersionByHostID[hostID]
				if installed && queued {
					install.UpcomingVPPAppTeamID = &queuedVersionID
					installByHostID[hostID] = install
				}
			}
			return map[uint]map[uint]automdm.HostVPPInstallWithUpcoming{1: installByHostID}, nil
		}
		f.ds.GetIncludedHostIDMapForVPPAppHostsFunc = func(ctx context.Context, vppAppTeamID uint, hostIDs []uint) (map[uint]struct{}, error) {
			version := versionByID(f, vppAppTeamID)
			included := make(map[uint]struct{})
			for _, hostID := range hostIDs {
				if inScope(f, hostID, version) {
					included[hostID] = struct{}{}
				}
			}
			return included, nil
		}
		f.ds.RecordFailedVPPAppInstallFunc = func(ctx context.Context, hostID uint, appID automdm.VPPAppID, commandUUID string, failureReason string, opts automdm.HostSoftwareInstallOptions) (*automdm.User, *automdm.ActivityInstalledAppStoreApp, error) {
			f.failedInstalls = append(f.failedInstalls, automdm.HostVPPSoftwareInstall{HostID: hostID, VPPAppTeamID: new(opts.VPPAppTeamID), CreatedAt: f.now})
			return nil, nil, nil
		}
		f.ds.GetHostIDsWithRecentFailedVPPAppInstallFunc = func(ctx context.Context, vppAppTeamID uint, since time.Time, hostIDs []uint) (map[uint]struct{}, error) {
			failed := make(map[uint]struct{})
			for _, install := range f.failedInstalls {
				if ptr.ValOrZero(install.VPPAppTeamID) == vppAppTeamID && !install.CreatedAt.Before(since) && slices.Contains(hostIDs, install.HostID) {
					failed[install.HostID] = struct{}{}
				}
			}
			return failed, nil
		}
		f.ds.GetVPPAppByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint, vppAppTeamID uint) (*automdm.VPPApp, error) {
			version := versionByID(f, vppAppTeamID)
			if version == nil {
				return nil, &notFoundError{}
			}
			return &automdm.VPPApp{VPPAppID: version.VPPAppID, AppTeamID: vppAppTeamID}, nil
		}
		return f
	}

	addAutoMDM := func(f *testAppStoreAppDatastore, automdmID uint) {
		f.tokenByTeamID[automdmID] = &automdm.VPPTokenDB{ID: 100 + automdmID, Token: "token", RenewDate: time.Now().Add(24 * time.Hour)}
	}
	addHost := func(f *testAppStoreAppDatastore, hostID uint, automdmID uint, labelIDs ...uint) *automdm.Host {
		host := &automdm.Host{ID: hostID, UUID: fmt.Sprintf("host-%d", hostID), Platform: "ios", TeamID: ptr.UintOrNilIfZero(automdmID)}
		f.hosts = append(f.hosts, host)
		f.inventoryHostIDs[hostID] = struct{}{}
		f.labelIDsByHostID[hostID] = map[uint]struct{}{}
		for _, labelID := range labelIDs {
			f.labelIDsByHostID[hostID][labelID] = struct{}{}
		}
		return host
	}
	addVersion := func(f *testAppStoreAppDatastore, automdmID uint, versionID uint, configuration string, includeAnyLabelIDs ...uint) *automdm.VPPAppStoreApp {
		tick(f)
		version := &automdm.VPPAppStoreApp{
			AdamID:         "1",
			Platform:       automdm.IOSPlatform,
			VPPAppsTeamsID: versionID,
		}
		if configuration != "" {
			version.Configuration = json.RawMessage(configuration)
			version.ConfigurationUpdatedAt = new(f.now)
		}
		for _, labelID := range includeAnyLabelIDs {
			version.LabelsIncludeAny = append(version.LabelsIncludeAny, automdm.SoftwareScopeLabel{LabelID: labelID})
		}
		f.versionsByTeamID[automdmID] = append(f.versionsByTeamID[automdmID], version)
		return version
	}
	// setConfiguration sets the version's configuration like the API and GitOps do, an empty configuration clears it
	setConfiguration := func(f *testAppStoreAppDatastore, version *automdm.VPPAppStoreApp, configuration string) {
		tick(f)
		version.Configuration = nil
		if configuration != "" {
			version.Configuration = json.RawMessage(configuration)
		}
		version.ConfigurationUpdatedAt = new(f.now)
	}
	// deleteVersion removes the version, and clears it from the installs of it like the version_id foreign key does
	deleteVersion := func(f *testAppStoreAppDatastore, automdmID uint, versionID uint) {
		f.versionsByTeamID[automdmID] = slices.DeleteFunc(f.versionsByTeamID[automdmID], func(version *automdm.VPPAppStoreApp) bool {
			return version.VPPAppsTeamsID == versionID
		})
		for i := range f.installs {
			if ptr.ValOrZero(f.installs[i].VPPAppTeamID) == versionID {
				f.installs[i].VPPAppTeamID = nil
			}
		}
	}
	// install records an install of the version that isn't from the cron, like a manual or setup experience install
	install := func(f *testAppStoreAppDatastore, hostID uint, versionID uint) {
		tick(f)
		f.installs = append(f.installs, automdm.HostVPPSoftwareInstall{HostID: hostID, VPPAppTeamID: new(versionID), CreatedAt: f.now})
	}
	// failInstall records a failed install of the version, like an MDM error or a failed verification
	failInstall := func(f *testAppStoreAppDatastore, hostID uint, versionID uint) {
		tick(f)
		f.failedInstalls = append(f.failedInstalls, automdm.HostVPPSoftwareInstall{HostID: hostID, VPPAppTeamID: new(versionID), CreatedAt: f.now})
	}
	// flagLatestInstall sets the re-send flag on the host's latest install like a change to an AutoMDM variable the configuration uses
	flagLatestInstall := func(f *testAppStoreAppDatastore, hostID uint) {
		for i := len(f.installs) - 1; i >= 0; i-- {
			if f.installs[i].HostID == hostID {
				f.installs[i].NeedsResend = true
				return
			}
		}
	}
	setLabel := func(f *testAppStoreAppDatastore, hostID uint, labelID uint, member bool) {
		if member {
			if f.labelIDsByHostID[hostID] == nil {
				f.labelIDsByHostID[hostID] = map[uint]struct{}{}
			}
			f.labelIDsByHostID[hostID][labelID] = struct{}{}
			return
		}
		delete(f.labelIDsByHostID[hostID], labelID)
	}
	// reconcile runs the cron and returns the version sent to each host in that run
	reconcile := func(f *testAppStoreAppDatastore, ctx context.Context) (map[uint]uint, error) {
		tick(f)
		f.sentVersionByHostID = map[uint]uint{}
		err := ReconcileAppStoreAppConfigurations(ctx, f.ds, f, f, slog.New(slog.DiscardHandler))
		return f.sentVersionByHostID, err
	}

	t.Run("a host whose version and configuration didn't change gets nothing", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// run the cron twice without changes, the host should get nothing
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("editing or clearing the configuration of a host's version sends that version to the host once", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// edit the configuration, the host should get version 10 once
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// clear the configuration, the host should get version 10 again
		setConfiguration(f, version, "")
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("an install in the same second as a configuration change is sent again", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// change the configuration half a second after the install's second, the install should count as before the change and the host should get version 10
		version.ConfigurationUpdatedAt = new(f.now.Add(500 * time.Millisecond))
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("changing an AutoMDM variable a host's configuration uses sends the host's version", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>user</key><string>$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME</string></dict>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// flag the host's latest install like a SCIM user, group or link change does, the host should get version 10 once
		flagLatestInstall(f, 1)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("adding and removing a host's labels sends the version the host is in scope for", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>v</key><string>a</string></dict>`, 100)
		addVersion(f, 0, 11, `<dict><key>v</key><string>b</string></dict>`)
		addHost(f, 1, 0)
		install(f, 1, 11)

		// add label 100 to the host, as adding it manually, a label spec or a host vitals label cron does, the host should get version 10
		setLabel(f, 1, 100, true)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)

		// remove label 100 from the host, as removing it, replacing or clearing the label's hosts, or re-enrolling does, the host should get version 11
		setLabel(f, 1, 100, false)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
	})

	t.Run("a host in scope for two versions gets the first-added one, and the label scope query isn't run for a version without labels", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>v</key><string>a</string></dict>`, 100)
		addVersion(f, 0, 11, `<dict><key>v</key><string>b</string></dict>`, 100)
		addVersion(f, 0, 12, `<dict><key>v</key><string>c</string></dict>`)
		addHost(f, 1, 0, 100)
		addHost(f, 2, 0)
		install(f, 1, 12)
		install(f, 2, 11)
		var scopedVersionIDs []uint
		inScope := f.ds.GetIncludedHostIDMapForVPPAppHostsFunc
		f.ds.GetIncludedHostIDMapForVPPAppHostsFunc = func(ctx context.Context, vppAppTeamID uint, hostIDs []uint) (map[uint]struct{}, error) {
			scopedVersionIDs = append(scopedVersionIDs, vppAppTeamID)
			return inScope(ctx, vppAppTeamID, hostIDs)
		}

		// run the cron, the host in label 100 should get version 10 and the other host version 12, without a scope query for version 12
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10, 2: 12}, sent)
		require.Equal(t, []uint{10, 11}, scopedVersionIDs)
	})

	t.Run("excluding a host's label from its version sends the host's next version", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict><key>v</key><string>a</string></dict>`)
		addVersion(f, 0, 11, `<dict><key>v</key><string>b</string></dict>`)
		addHost(f, 1, 0, 200)
		install(f, 1, 10)

		// exclude label 200 from version 10, as a dynamic exclude any label does once the host refetches, the host should get version 11
		version.LabelsExcludeAny = []automdm.SoftwareScopeLabel{{LabelID: 200}}
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
	})

	t.Run("adding a version a host is in scope for sends it", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`, 100)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// add a version without labels after the host left version 10's label, the host should get version 11
		addVersion(f, 0, 11, `<dict><key>v</key><string>new</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
	})

	t.Run("deleting a host's version sends the host's next version, with or without a configuration", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`, 100)
		addVersion(f, 0, 11, `<dict/>`, 100)
		addVersion(f, 0, 12, "")
		addHost(f, 1, 0, 100)
		install(f, 1, 10)

		// delete version 10, the host should get version 11
		deleteVersion(f, 0, 10)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)

		// delete version 11, the host should get version 12 without a configuration
		deleteVersion(f, 0, 11)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 12}, sent)
	})

	t.Run("deleting every version of an app or removing it from GitOps sends nothing", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// delete the only version, the host should get nothing since iOS apps aren't removed from hosts
		deleteVersion(f, 0, 10)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("moving a host between versions sends an install only when either version has a configuration", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`, 100)
		addVersion(f, 0, 11, "", 200)
		versionC := addVersion(f, 0, 12, `<dict/>`)
		addHost(f, 1, 0, 100)
		install(f, 1, 10)

		// move the host from version 10 with a configuration to version 11 without one, the host should get version 11
		setLabel(f, 1, 100, false)
		setLabel(f, 1, 200, true)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)

		// clear version 12's configuration after the host's install of version 11, then move the host to it, the host should get nothing
		setConfiguration(f, versionC, "")
		setLabel(f, 1, 200, false)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("moving a host between versions with the same configuration sends nothing, and moving it to a version with another configuration sends that version", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>v</key><string>same</string></dict>`, 100)
		addVersion(f, 0, 11, `<dict><key>v</key><string>same</string></dict>`, 200)
		addVersion(f, 0, 12, `<dict><key>v</key><string>other</string></dict>`)
		addHost(f, 1, 0, 100)
		install(f, 1, 10)

		// move the host from version 10 to version 11 with the same configuration, the host should get nothing
		setLabel(f, 1, 100, false)
		setLabel(f, 1, 200, true)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// move the host to version 12 with another configuration, the host should get version 12
		setLabel(f, 1, 200, false)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 12}, sent)
	})

	t.Run("moving a host between versions with the same configuration sends the new version when an AutoMDM variable the configuration uses changed", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>user</key><string>$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME</string></dict>`, 100)
		addVersion(f, 0, 11, `<dict><key>user</key><string>$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME</string></dict>`)
		addHost(f, 1, 0, 100)
		install(f, 1, 10)

		// move the host from version 10 to version 11 with the same configuration and flag its latest install like a SCIM user change does, the host should get version 11 once
		setLabel(f, 1, 100, false)
		flagLatestInstall(f, 1)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("moving a host off a version whose configuration was cleared after the host's install sends the next version", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		versionA := addVersion(f, 0, 10, `<dict/>`, 100)
		addVersion(f, 0, 11, "")
		addHost(f, 1, 0, 100)
		install(f, 1, 10)

		// clear version 10's configuration and move the host to version 11 before the cron runs, the host should get version 11 since it still has version 10's configuration
		setConfiguration(f, versionA, "")
		setLabel(f, 1, 100, false)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
	})

	t.Run("moving a host between a team and Unassigned sends the new team's version", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addAutoMDM(f, 5)
		addVersion(f, 0, 10, `<dict><key>v</key><string>unassigned</string></dict>`)
		addVersion(f, 5, 20, `<dict><key>v</key><string>team</string></dict>`)
		host := addHost(f, 1, 0)
		install(f, 1, 10)

		// move the host to team 5, the host should get version 20
		host.TeamID = new(uint(5))
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 20}, sent)

		// move the host back to Unassigned, as deleting its team does, the host should get version 10
		host.TeamID = nil
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("moving a host to another team where neither version has a configuration sends nothing, until the installed version's configuration changes", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addAutoMDM(f, 5)
		installedVersion := addVersion(f, 0, 10, "")
		addVersion(f, 5, 20, "")
		host := addHost(f, 1, 0)
		install(f, 1, 10)

		// move the host to team 5, the host should get nothing since neither version has a configuration
		host.TeamID = new(uint(5))
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// set a configuration on team 0's version 10 that the host has installed, the host should get team 5's version 20 since the configurations differ now
		setConfiguration(f, installedVersion, `<dict><key>v</key><string>a</string></dict>`)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 20}, sent)
	})

	t.Run("a host whose latest install is of a version it isn't in scope for gets the version it's in scope for", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict><key>v</key><string>a</string></dict>`, 100)
		addVersion(f, 0, 11, `<dict><key>v</key><string>b</string></dict>`)
		addHost(f, 1, 0)

		// install version 10 on a host outside its label, as the setup experience or a queued install of a deleted version does, the host should get version 11
		install(f, 1, 10)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 11}, sent)
	})

	t.Run("a host re-created after being deleted gets nothing until AutoMDM installs the app on it", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 2, 0)

		// run the cron for a host without an install from AutoMDM, the host should get nothing
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("a host without the app in its inventory gets nothing, and gets the latest configuration if the app is back in its inventory", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// remove the app from the host's inventory and edit the configuration, the host should get nothing
		delete(f.inventoryHostIDs, 1)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// reinstall the app outside AutoMDM, the host should get version 10
		f.inventoryHostIDs[1] = struct{}{}
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("a host with MDM off gets the configuration once MDM is back on", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// turn MDM off for the host and edit the configuration, the host should get nothing
		f.mdmOffHostIDs[1] = struct{}{}
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// turn MDM back on, the host should get version 10
		delete(f.mdmOffHostIDs, 1)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("an install Apple rejects while the app is out of Apple Business is sent once the app is back", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)

		// remove the app from Apple Business and edit the configuration, the install should be rejected and nothing sent
		f.rejectedHostIDs[1] = struct{}{}
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// add the app back to Apple Business, the host should get version 10
		delete(f.rejectedHostIDs, 1)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("an install Apple rejects is retried an hour after the rejection", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		f.now = time.Now().Truncate(time.Second)

		// remove the app from Apple Business and run the cron, the install should be rejected and recorded as a failed install of version 10
		f.rejectedHostIDs[1] = struct{}{}
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		require.Len(t, f.failedInstalls, 1)
		require.Equal(t, new(uint(10)), f.failedInstalls[0].VPPAppTeamID)

		// add the app back to Apple Business and run the cron, the host should get nothing since the rejection was less than an hour ago
		delete(f.rejectedHostIDs, 1)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// move the rejection two hours back and run the cron, the host should get version 10
		f.failedInstalls[0].CreatedAt = time.Now().Add(-2 * time.Hour)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("an install that keeps failing is retried twice, then waits until its retry count expires", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		f.now = time.Now().Truncate(time.Second)
		f.rejectedHostIDs[1] = struct{}{}

		// run the cron with the app out of Apple Business, the install should be rejected and not counted as a retry
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		require.Zero(t, f.retriesByHostAndVersion[[2]uint{1, 10}])

		// move the failed installs two hours back and run the cron twice, each install should be rejected and counted as a retry
		for i := range f.failedInstalls {
			f.failedInstalls[i].CreatedAt = time.Now().Add(-2 * time.Hour)
		}
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		for i := range f.failedInstalls {
			f.failedInstalls[i].CreatedAt = time.Now().Add(-2 * time.Hour)
		}
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		require.Equal(t, 2, f.retriesByHostAndVersion[[2]uint{1, 10}])

		// add the app back to Apple Business, move the failed installs two hours back and run the cron, the host should get nothing since it was retried twice
		delete(f.rejectedHostIDs, 1)
		for i := range f.failedInstalls {
			f.failedInstalls[i].CreatedAt = time.Now().Add(-2 * time.Hour)
		}
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// expire the retry count and run the cron, the host should get version 10
		delete(f.retriesByHostAndVersion, [2]uint{1, 10})
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("a host with a queued install of its version gets nothing more until the install runs", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		f.queueInstalls = true

		// edit the configuration, the host should get version 10 queued once
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("a host whose install failed within the last hour gets nothing, unless the configuration changes after the failure", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)

		// fail the host's install of the new configuration, as a variable without a value does, the host should get nothing since the install failed less than an hour ago
		failInstall(f, 1, 10)
		f.now = time.Now().Truncate(time.Second)
		f.failedInstalls[0].CreatedAt = f.now
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// edit the configuration again, the host should get version 10 right away since the new configuration might not fail
		setConfiguration(f, version, `<dict><key>a</key><string>c</string></dict>`)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("a host whose install failed more than an hour ago gets its version again", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)

		// fail the host's install two hours ago and run the cron, the host should get version 10 again since the install failed more than an hour ago
		failInstall(f, 1, 10)
		f.failedInstalls[0].CreatedAt = time.Now().Add(-2 * time.Hour)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("a rejected install on one host doesn't stop the next host's install", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		addHost(f, 2, 0)
		install(f, 1, 10)
		install(f, 2, 10)
		f.rejectedHostIDs[1] = struct{}{}

		// edit the configuration with the first host's install rejected, the second host should still get version 10
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{2: 10}, sent)
	})

	t.Run("removing or expiring a team's VPP token sends nothing to its hosts", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)

		// expire the team's VPP token, the host should get nothing
		f.tokenByTeamID[0].RenewDate = time.Now().Add(-time.Hour)
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)

		// remove the team's VPP token, the host should get nothing
		delete(f.tokenByTeamID, 0)
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
	})

	t.Run("a version on an expired token waits while a version on the team's other token is sent", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		expired := &automdm.VPPTokenDB{ID: 200, Token: "expired", RenewDate: time.Now().Add(-time.Hour)}
		f.tokenByTeamID[99] = expired // listed by ListVPPTokens, assigned to no host's team
		onExpired := addVersion(f, 0, 10, `<dict/>`, 1)
		onExpired.VPPTokenID = new(expired.ID)
		onHealthy := addVersion(f, 0, 20, `<dict/>`)
		addHost(f, 1, 0)
		addHost(f, 2, 0, 1)
		install(f, 1, 20)
		install(f, 2, 10)
		setConfiguration(f, onExpired, `<dict><key>a</key><string>b</string></dict>`)
		setConfiguration(f, onHealthy, `<dict><key>a</key><string>b</string></dict>`)

		// host 1 gets the healthy token's version; host 2 is first in scope for the expired token's version, so it gets
		// nothing instead of falling through to the healthy version
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 20}, sent)
	})

	t.Run("a run that can't list VPP tokens returns the error without saving the cursor", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		f.ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
			return nil, errors.New("token can't be decrypted")
		}

		_, err := reconcile(f, ctx)
		require.ErrorContains(t, err, "list vpp tokens")
		require.False(t, f.ds.SetAppStoreAppConfigurationReconcileCursorFuncInvoked)
	})

	t.Run("an error on one App Store app is logged and the next app is still reconciled", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		versions := f.ds.GetAppStoreAppVersionsByTeamAndTitleFunc
		f.ds.GetAppStoreAppVersionsByTeamAndTitleFunc = func(ctx context.Context, platforms []automdm.InstallableDevicePlatform) (automdm.AppStoreAppVersionsByTeamAndTitleID, error) {
			versionsByTitleIDByTeamID, err := versions(ctx, platforms)
			versionsByTitleIDByTeamID[0][2] = []*automdm.VPPAppStoreApp{{VPPAppsTeamsID: 20, HasLabels: true}}
			return versionsByTitleIDByTeamID, err
		}
		installs := f.ds.ListHostAppStoreAppInstallVersionsFunc
		f.ds.ListHostAppStoreAppInstallVersionsFunc = func(ctx context.Context, automdmID uint, hostIDs []uint, platforms []automdm.InstallableDevicePlatform) (map[uint]map[uint]automdm.HostVPPInstallWithUpcoming, error) {
			installByHostIDByTitleID, err := installs(ctx, automdmID, hostIDs, platforms)
			installByHostIDByTitleID[2] = installByHostIDByTitleID[1]
			return installByHostIDByTitleID, err
		}
		includedHostIDs := f.ds.GetIncludedHostIDMapForVPPAppHostsFunc
		f.ds.GetIncludedHostIDMapForVPPAppHostsFunc = func(ctx context.Context, vppAppTeamID uint, hostIDs []uint) (map[uint]struct{}, error) {
			if vppAppTeamID == 20 {
				return nil, errors.New("read failed")
			}
			return includedHostIDs(ctx, vppAppTeamID, hostIDs)
		}

		// fail reading the label scope of another app's version, the host should still get version 10
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
	})

	t.Run("hosts are checked in batches that continue from the cursor and start over after the last batch", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		lastHostID := uint(appStoreAppConfigurationReconcileBatchSize + 1)
		for hostID := uint(1); hostID <= lastHostID; hostID++ {
			addHost(f, hostID, 0)
			install(f, hostID, 10)
		}
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)

		// run the cron, the first batch of hosts should get version 10 and the cursor should be set to its last host
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Len(t, sent, appStoreAppConfigurationReconcileBatchSize)
		require.NotContains(t, sent, lastHostID)
		require.Equal(t, lastHostID-1, f.cursor)

		// run the cron again, only the last host should get version 10 and the cursor should be set back to the first host
		sent, err = reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{lastHostID: 10}, sent)
		require.Zero(t, f.cursor)
	})

	t.Run("a team whose App Store app installs can't be read doesn't stop other teams or the cursor", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addAutoMDM(f, 5)
		addAutoMDM(f, 6)
		version := addVersion(f, 0, 10, `<dict/>`)
		addVersion(f, 5, 20, `<dict/>`)
		addVersion(f, 6, 30, `<dict/>`)
		addHost(f, 1, 0)
		addHost(f, 2, 5)
		addHost(f, 3, 6)
		install(f, 1, 10)
		install(f, 2, 20)
		install(f, 3, 30)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)
		installs := f.ds.ListHostAppStoreAppInstallVersionsFunc
		f.ds.ListHostAppStoreAppInstallVersionsFunc = func(ctx context.Context, automdmID uint, hostIDs []uint, platforms []automdm.InstallableDevicePlatform) (map[uint]map[uint]automdm.HostVPPInstallWithUpcoming, error) {
			if automdmID == 6 {
				return nil, errors.New("read failed")
			}
			return installs(ctx, automdmID, hostIDs, platforms)
		}

		// fail reading team 6's App Store app installs, the Unassigned host should still get version 10 and the cursor should be saved
		sent, err := reconcile(f, ctx)
		require.NoError(t, err)
		require.Equal(t, map[uint]uint{1: 10}, sent)
		require.True(t, f.ds.SetAppStoreAppConfigurationReconcileCursorFuncInvoked)
	})

	t.Run("a run whose context ended returns the error without saving the cursor", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		endedCtx, cancel := context.WithCancel(ctx)
		cancel()

		// run the cron with a context that already ended, as when the run times out, the run should fail and the cursor shouldn't be saved
		_, err := reconcile(f, endedCtx)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, f.ds.SetAppStoreAppConfigurationReconcileCursorFuncInvoked)
	})

	t.Run("an AutoMDM Free instance reconciles nothing", func(t *testing.T) {
		f := newTestAppStoreAppDatastore()
		addAutoMDM(f, 0)
		version := addVersion(f, 0, 10, `<dict/>`)
		addHost(f, 1, 0)
		install(f, 1, 10)
		setConfiguration(f, version, `<dict><key>a</key><string>b</string></dict>`)

		// run the cron on AutoMDM Free, the host should get nothing and no hosts should be read
		sent, err := reconcile(f, license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierFree}))
		require.NoError(t, err)
		require.Equal(t, nothingSent, sent)
		require.False(t, f.ds.ListIOSAndIPadOSMDMHostsAfterIDFuncInvoked)
	})
}
