package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ma "github.com/anilyagiz/automdm/v4/ee/maintained-apps"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mocksoftware "github.com/anilyagiz/automdm/v4/server/mock/software"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestListMaintainedAppsAuth(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListAvailableAutoMDMMaintainedAppsFunc = func(ctx context.Context, teamID *uint, opt automdm.MaintainedAppListOptions) ([]automdm.MaintainedApp, *automdm.PaginationMetadata, error) {
		return []automdm.MaintainedApp{}, &automdm.PaginationMetadata{}, nil
	}
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{authz: authorizer, ds: ds}

	testCases := []struct {
		name                        string
		user                        *automdm.User
		shouldFailWithNoTeam        bool
		shouldFailWithMatchingTeam  bool
		shouldFailWithDifferentTeam bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			false,
			false,
			false,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			true,
			true,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			false,
			false,
			true,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			false,
			false,
			true,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			true,
			true,
		},
		{
			"global gitops",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			false,
			false,
			false,
		},
		{
			"team gitops",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			false,
			false,
			true,
		},
	}

	var forbiddenError *authz.Forbidden
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: tt.user})

			_, _, err := svc.ListAutoMDMMaintainedApps(ctx, nil, automdm.MaintainedAppListOptions{})
			if tt.shouldFailWithNoTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}

			_, _, err = svc.ListAutoMDMMaintainedApps(ctx, new(uint(1)), automdm.MaintainedAppListOptions{})
			if tt.shouldFailWithMatchingTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}

			_, _, err = svc.ListAutoMDMMaintainedApps(ctx, new(uint(2)), automdm.MaintainedAppListOptions{})
			if tt.shouldFailWithDifferentTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetMaintainedAppAuth(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{Slug: "1password/darwin"}, nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, ".json"), "/")
		var manifest ma.FMAManifestFile
		switch slug {
		case "fail":
			w.WriteHeader(http.StatusInternalServerError)
			return

		case "notfound":
			w.WriteHeader(http.StatusNotFound)
			return

		case "1password/darwin":
			var versions []*ma.FMAManifestApp
			versions = append(versions, &ma.FMAManifestApp{
				Version: "1",
				Queries: ma.FMAQueries{
					Exists: "SELECT 1 FROM osquery_info;",
				},
				InstallerURL:       "https://google.com",
				InstallScriptRef:   "foobaz",
				UninstallScriptRef: "foobaz",
				SHA256:             "deadbeef",
			})

			manifest = ma.FMAManifestFile{
				Versions: versions,
				Refs: map[string]string{
					"foobaz": "Hello World!",
				},
			}

		default:
			w.WriteHeader(http.StatusBadRequest)
			t.Fatalf("unexpected app token %s", slug)
		}

		err := json.NewEncoder(w).Encode(manifest)
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{authz: authorizer, ds: ds}

	testCases := []struct {
		name                        string
		user                        *automdm.User
		shouldFailWithNoTeam        bool
		shouldFailWithMatchingTeam  bool
		shouldFailWithDifferentTeam bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			false,
			false,
			false,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			true,
			true,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			false,
			false,
			true,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			false,
			false,
			true,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			true,
			true,
		},
		{
			"global gitops",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			false,
			false,
			false,
		},
		{
			"team gitops",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			false,
			false,
			true,
		},
	}

	var forbiddenError *authz.Forbidden
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", srv.URL, t)
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: tt.user})
			_, err := svc.GetAutoMDMMaintainedApp(ctx, 123, nil)

			if tt.shouldFailWithNoTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}

			_, err = svc.GetAutoMDMMaintainedApp(ctx, 1, ptr.Uint(1))
			if tt.shouldFailWithMatchingTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}

			_, err = svc.GetAutoMDMMaintainedApp(ctx, 1, ptr.Uint(2))
			if tt.shouldFailWithDifferentTeam {
				require.Error(t, err)
				require.ErrorAs(t, err, &forbiddenError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetAutoMDMMaintainedAppManifestUnreachable(t *testing.T) {
	ds := new(mock.Store)
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{ID: appID, Name: "Rectangle", Slug: "rectangle/darwin"}, nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", srv.URL, t)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_FALLBACK_BASE_URL", srv.URL, t)

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{authz: authorizer, ds: ds}
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	expectedMessage := "AutoMDM couldn't fetch the manifest for Rectangle. Please try again."

	// get the app for a team while the manifest returns errors, a 502 with the message should be returned
	_, err = svc.GetAutoMDMMaintainedApp(ctx, 1, new(uint(1)))
	var gatewayErr *automdm.GatewayError
	require.ErrorAs(t, err, &gatewayErr)
	require.Equal(t, http.StatusBadGateway, gatewayErr.StatusCode())
	require.Equal(t, expectedMessage, gatewayErr.Error())

	// get the app for Unassigned while the manifest returns errors, a 502 with the message should be returned
	_, err = svc.GetAutoMDMMaintainedApp(ctx, 1, new(uint(0)))
	gatewayErr = nil
	require.ErrorAs(t, err, &gatewayErr)
	require.Equal(t, http.StatusBadGateway, gatewayErr.StatusCode())
	require.Equal(t, expectedMessage, gatewayErr.Error())
}

func TestAddAutoMDMMaintainedApp(t *testing.T) {
	installerBytes := []byte("abc")

	// this is the hash we expect to get in the DB
	h := sha256.New()
	_, err := h.Write(installerBytes)
	require.NoError(t, err)
	spoofedSHA := hex.EncodeToString(h.Sum(nil))

	ds := new(mock.Store)
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{
			ID:               1,
			Name:             "Internet Exploder",
			Slug:             "iexplode/windows",
			Platform:         "windows",
			TitleID:          nil,
			UniqueIdentifier: "Internet Exploder",
		}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	// Mock server to serve the "installer"
	installerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(installerBytes)
	}))
	defer installerServer.Close()
	ds.MatchOrCreateSoftwareInstallerFunc = func(ctx context.Context, payload *automdm.UploadSoftwareInstallerPayload) (uint, uint, error) {
		require.Equal(t, spoofedSHA, payload.StorageID)
		require.Empty(t, payload.BundleIdentifier)
		require.Equal(t, "Internet Exploder", payload.Title)
		require.Equal(t, "programs", payload.Source)
		require.Equal(t, "Hello World!", payload.InstallScript)
		require.Equal(t, "Hello World!", payload.UninstallScript)
		require.Equal(t, installerServer.URL+"/IEXPLODE-SETUP.EXE", payload.URL)
		require.Equal(t, "IEXPLODE-SETUP.EXE", payload.Filename, "filename keeps the original casing")
		require.Equal(t, "exe", payload.Extension)

		// Can't easily inject a proper automdm.service so we bail early before NewActivity gets called and panics
		return 0, 0, errors.New("forced error to short-circuit storage and activity creation")
	}

	// Mock server to serve the manifest
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var versions []*ma.FMAManifestApp
		versions = append(versions, &ma.FMAManifestApp{
			Version: "6.0",
			Queries: ma.FMAQueries{
				Exists: "SELECT 1 FROM osquery_info;",
			},
			InstallerURL:       installerServer.URL + "/IEXPLODE-SETUP.EXE",
			InstallScriptRef:   "foobaz",
			UninstallScriptRef: "foobaz",
			SHA256:             noCheckHash,
		})

		manifest := ma.FMAManifestFile{
			Versions: versions,
			Refs: map[string]string{
				"foobaz": "Hello World!",
			},
		}

		err := json.NewEncoder(w).Encode(manifest)
		require.NoError(t, err)
	}))

	t.Cleanup(manifestServer.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", manifestServer.URL, t)

	svc := newTestService(t, ds)

	authCtx := authz_ctx.AuthorizationContext{}
	ctx := authz_ctx.NewContext(context.Background(), &authCtx)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
	_, err = svc.AddAutoMDMMaintainedApp(ctx, nil, 1, "", "", "", "", false, false, nil, nil, nil)
	require.ErrorContains(t, err, "forced error to short-circuit storage and activity creation")

	require.True(t, ds.MatchOrCreateSoftwareInstallerFuncInvoked)
}

// TestAddAutoMDMMaintainedAppReconcilesWindowsTitles covers the wiring, not the merge
// itself: adding a maintained app must kick the Windows title reconcile so software
// already inventoried under a versioned title picks up the installer straight away
// rather than waiting for the periodic pass.
func TestAddAutoMDMMaintainedAppReconcilesWindowsTitles(t *testing.T) {
	installerBytes := []byte("abc")

	ds := new(mock.Store)
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error { return nil }
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{
			ID: 1, Name: "Internet Exploder", Slug: "iexplode/windows",
			Platform: "windows", UniqueIdentifier: "Internet Exploder",
		}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}
	ds.MatchOrCreateSoftwareInstallerFunc = func(ctx context.Context, payload *automdm.UploadSoftwareInstallerPayload) (uint, uint, error) {
		return 1, 42, nil
	}
	ds.ReconcileWindowsMaintainedAppSoftwareTitlesFunc = func(ctx context.Context) error { return nil }

	installerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(installerBytes)
	}))
	defer installerServer.Close()

	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := ma.FMAManifestFile{
			Versions: []*ma.FMAManifestApp{{
				Version:            "6.0",
				Queries:            ma.FMAQueries{Exists: "SELECT 1 FROM osquery_info;"},
				InstallerURL:       installerServer.URL + "/iexplode.exe",
				InstallScriptRef:   "foobaz",
				UninstallScriptRef: "foobaz",
				SHA256:             noCheckHash,
			}},
			Refs: map[string]string{"foobaz": "Hello World!"},
		}
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	t.Cleanup(manifestServer.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", manifestServer.URL, t)

	svc := newTestService(t, ds)
	// Fail at the storage step, which runs just after the reconcile, so the call returns
	// before NewActivity needs a fully wired service.
	svc.softwareInstallStore = &mocksoftware.SoftwareInstallerStore{
		ExistsFunc: func(context.Context, string) (bool, error) {
			return false, errors.New("forced error to short-circuit storage and activity creation")
		},
	}

	ctx := authz_ctx.NewContext(context.Background(), &authz_ctx.AuthorizationContext{})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	_, err := svc.AddAutoMDMMaintainedApp(ctx, nil, 1, "", "", "", "", false, false, nil, nil, nil)
	require.ErrorContains(t, err, "forced error to short-circuit storage and activity creation")

	require.True(t, ds.MatchOrCreateSoftwareInstallerFuncInvoked)
	require.True(t, ds.ReconcileWindowsMaintainedAppSoftwareTitlesFuncInvoked,
		"adding a Windows maintained app must reconcile Windows software titles")
}

func TestAddAutoMDMMaintainedAppAutoMDMVariables(t *testing.T) {
	ds := new(mock.Store)
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{
			ID:               1,
			Name:             "Internet Exploder",
			Slug:             "iexplode/windows",
			Platform:         "windows",
			TitleID:          nil,
			UniqueIdentifier: "Internet Exploder",
		}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	installerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abc"))
	}))
	defer installerServer.Close()

	// manifest whose default scripts reference an unsupported AutoMDM variable
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := ma.FMAManifestFile{
			Versions: []*ma.FMAManifestApp{{
				Version: "6.0",
				Queries: ma.FMAQueries{
					Exists: "SELECT 1 FROM osquery_info;",
				},
				InstallerURL:       installerServer.URL + "/iexplode.exe",
				InstallScriptRef:   "foobaz",
				UninstallScriptRef: "foobaz",
				SHA256:             noCheckHash,
			}},
			Refs: map[string]string{
				"foobaz": "echo $AUTOMDM_VAR_NONEXISTENT",
			},
		}
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	t.Cleanup(manifestServer.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", manifestServer.URL, t)

	svc := newTestService(t, ds)

	authCtx := authz_ctx.AuthorizationContext{}
	ctx := authz_ctx.NewContext(context.Background(), &authCtx)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})

	// empty script inputs default to the manifest scripts, which are validated
	// after that resolution
	_, err := svc.AddAutoMDMMaintainedApp(ctx, nil, 1, "", "", "", "", false, false, nil, nil, nil)
	require.ErrorContains(t, err, "AutoMDM variable $AUTOMDM_VAR_NONEXISTENT is not supported in scripts.")

	// caller-provided scripts are validated the same way
	_, err = svc.AddAutoMDMMaintainedApp(ctx, nil, 1, "echo ok", "", "echo $AUTOMDM_VAR_NONEXISTENT", "echo ok", false, false, nil, nil, nil)
	require.ErrorContains(t, err, "AutoMDM variable $AUTOMDM_VAR_NONEXISTENT is not supported in scripts.")
}

func TestExtractMaintainedAppVersionWhenLatest(t *testing.T) {
	installerBytes, err := os.ReadFile(filepath.Join("testdata", "dummy_installer.pkg"))
	require.NoError(t, err)

	// this is the hash we expect to get in the DB
	h := sha256.New()
	_, err = h.Write(installerBytes)
	require.NoError(t, err)
	spoofedSHA := hex.EncodeToString(h.Sum(nil))

	ds := new(mock.Store)
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.GetMaintainedAppByIDFunc = func(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
		return &automdm.MaintainedApp{
			ID:               1,
			Name:             "Dummy",
			Slug:             "dummy/darwin",
			Platform:         "darwin",
			TitleID:          nil,
			UniqueIdentifier: "com.example.dummy",
		}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	// Mock server to serve the dummy package
	installerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(installerBytes)
	}))
	defer installerServer.Close()
	ds.MatchOrCreateSoftwareInstallerFunc = func(ctx context.Context, payload *automdm.UploadSoftwareInstallerPayload) (uint, uint, error) {
		require.Equal(t, spoofedSHA, payload.StorageID)
		require.Equal(t, "1.0.0", payload.Version)

		// Can't easily inject a proper automdm.service so we bail early before NewActivity gets called and panics
		return 0, 0, errors.New("forced error to short-circuit storage and activity creation")
	}

	// Mock server to serve the manifest
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var versions []*ma.FMAManifestApp
		versions = append(versions, &ma.FMAManifestApp{
			Version: "latest",
			Queries: ma.FMAQueries{
				Exists: "SELECT 1 FROM osquery_info;",
			},
			InstallerURL:       installerServer.URL + "/dummy.pkg",
			InstallScriptRef:   "foobaz",
			UninstallScriptRef: "foobaz",
			SHA256:             noCheckHash,
		})

		manifest := ma.FMAManifestFile{
			Versions: versions,
			Refs: map[string]string{
				"foobaz": "Hello World!",
			},
		}

		err := json.NewEncoder(w).Encode(manifest)
		require.NoError(t, err)
	}))

	t.Cleanup(manifestServer.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", manifestServer.URL, t)

	svc := newTestService(t, ds)

	authCtx := authz_ctx.AuthorizationContext{}
	ctx := authz_ctx.NewContext(context.Background(), &authCtx)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
	_, err = svc.AddAutoMDMMaintainedApp(ctx, nil, 1, "", "", "", "", false, false, nil, nil, nil)
	require.ErrorContains(t, err, "forced error to short-circuit storage and activity creation")

	require.True(t, ds.MatchOrCreateSoftwareInstallerFuncInvoked)
}
