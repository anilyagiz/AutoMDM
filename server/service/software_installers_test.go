package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	eeservice "github.com/anilyagiz/automdm/v4/ee/server/service"
	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/installersize"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/filesystem"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestUploadSoftwareInstallerDecodeTitleID(t *testing.T) {
	testCases := []struct {
		name      string
		value     *string
		want      *uint
		wantError string
	}{
		{name: "omitted"},
		{name: "valid", value: new("42"), want: new(uint(42))},
		{name: "invalid", value: new("invalid"), wantError: "Invalid software_title_id: invalid"},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			file, err := writer.CreateFormFile("software", "test.sh")
			require.NoError(t, err)
			_, err = file.Write([]byte("#!/bin/sh\n"))
			require.NoError(t, err)
			if tt.value != nil {
				require.NoError(t, writer.WriteField("software_title_id", *tt.value))
			}
			require.NoError(t, writer.Close())

			request := httptest.NewRequest(http.MethodPost, "/api/latest/automdm/software/package", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			ctx := installersize.NewContext(t.Context(), int64(body.Len()))
			decoded, err := (uploadSoftwareInstallerRequest{}).DecodeRequest(ctx, request)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, decoded.(*uploadSoftwareInstallerRequest).TitleID)
		})
	}
}

func TestSoftwareInstallersAuth(t *testing.T) {
	ds := new(mock.Store)

	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license})

	testCases := []struct {
		name            string
		user            *automdm.User
		teamID          *uint
		shouldFailRead  bool
		shouldFailWrite bool
	}{
		{"no role no team", test.UserNoRoles, nil, true, true},
		{"no role team 0", test.UserNoRoles, ptr.Uint(0), true, true},
		{"no role team", test.UserNoRoles, ptr.Uint(1), true, true},
		{"global admin no team", test.UserAdmin, nil, false, false},
		{"global admin team 0", test.UserAdmin, ptr.Uint(0), false, false},
		{"global admin team", test.UserAdmin, ptr.Uint(1), false, false},
		{"global maintainer no team", test.UserMaintainer, nil, false, false},
		{"global mainteiner team 0", test.UserMaintainer, ptr.Uint(0), false, false},
		{"global maintainer team", test.UserMaintainer, ptr.Uint(1), false, false},
		{"global observer no team", test.UserObserver, nil, true, true},
		{"global observer team 0", test.UserObserver, ptr.Uint(0), true, true},
		{"global observer team", test.UserObserver, ptr.Uint(1), true, true},
		{"global observer+ no team", test.UserObserverPlus, nil, true, true},
		{"global observer+ team 0", test.UserObserverPlus, ptr.Uint(0), true, true},
		{"global observer+ team", test.UserObserverPlus, ptr.Uint(1), true, true},
		{"global gitops no team", test.UserGitOps, nil, true, false},
		{"global gitops team 0", test.UserGitOps, ptr.Uint(0), false, false},
		{"global gitops team", test.UserGitOps, ptr.Uint(1), false, false},
		{"team admin no team", test.UserTeamAdminTeam1, nil, true, true},
		{"team admin team 0", test.UserTeamAdminTeam1, ptr.Uint(0), true, true},
		{"team admin team", test.UserTeamAdminTeam1, ptr.Uint(1), false, false},
		{"team admin other team", test.UserTeamAdminTeam2, ptr.Uint(1), true, true},
		{"team maintainer no team", test.UserTeamMaintainerTeam1, nil, true, true},
		{"team maintainer team 0", test.UserTeamMaintainerTeam1, ptr.Uint(0), true, true},
		{"team maintainer team", test.UserTeamMaintainerTeam1, ptr.Uint(1), false, false},
		{"team maintainer other team", test.UserTeamMaintainerTeam2, ptr.Uint(1), true, true},
		{"team observer no team", test.UserTeamObserverTeam1, nil, true, true},
		{"team observer team 0", test.UserTeamObserverTeam1, ptr.Uint(0), true, true},
		{"team observer team", test.UserTeamObserverTeam1, ptr.Uint(1), true, true},
		{"team observer other team", test.UserTeamObserverTeam2, ptr.Uint(1), true, true},
		{"team observer+ no team", test.UserTeamObserverPlusTeam1, nil, true, true},
		{"team observer+ team 0", test.UserTeamObserverPlusTeam1, ptr.Uint(0), true, true},
		{"team observer+ team", test.UserTeamObserverPlusTeam1, ptr.Uint(1), true, true},
		{"team observer+ other team", test.UserTeamObserverPlusTeam2, ptr.Uint(1), true, true},
		{"team gitops no team", test.UserTeamGitOpsTeam1, nil, true, true},
		{"team gitops team 0", test.UserTeamGitOpsTeam1, ptr.Uint(0), true, true},
		{"team gitops team", test.UserTeamGitOpsTeam1, ptr.Uint(1), false, false},
		{"team gitops other team", test.UserTeamGitOpsTeam2, ptr.Uint(1), true, true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			ds.GetSoftwareInstallerMetadataByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint, withScripts bool) (*automdm.SoftwareInstaller, error) {
				return &automdm.SoftwareInstaller{TeamID: tt.teamID}, nil
			}
			ds.GetVPPAppMetadataByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint) (*automdm.VPPAppStoreApp, error) {
				if tt.teamID == nil {
					return &automdm.VPPAppStoreApp{VPPAppsTeamsID: 0}, nil
				}
				return &automdm.VPPAppStoreApp{VPPAppsTeamsID: *tt.teamID}, nil
			}
			ds.GetAppStoreAppVersionsByTeamAndTitleIDFunc = func(ctx context.Context, teamID uint, titleID uint) ([]*automdm.VPPAppStoreApp, error) {
				return []*automdm.VPPAppStoreApp{{VPPAppsTeamsID: teamID}}, nil
			}
			ds.GetInHouseAppMetadataByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint) (*automdm.SoftwareInstaller, error) {
				return &automdm.SoftwareInstaller{TeamID: tt.teamID}, nil
			}
			ds.GetSoftwarePackagesByTeamAndTitleIDFunc = func(ctx context.Context, teamID *uint, titleID uint) ([]*automdm.SoftwareInstaller, error) {
				return []*automdm.SoftwareInstaller{{TeamID: tt.teamID, InstallerID: 1}}, nil
			}

			ds.DeleteSoftwareInstallerFunc = func(ctx context.Context, installerID uint) error {
				return nil
			}

			tokenExpiration := time.Now().Add(24 * time.Hour)
			token, err := test.CreateVPPTokenEncoded(tokenExpiration, "automdm", "ca")
			require.NoError(t, err)
			ds.GetVPPTokenByTeamIDFunc = func(ctx context.Context, teamID *uint) (*automdm.VPPTokenDB, error) {
				return &automdm.VPPTokenDB{
					ID:        1,
					OrgName:   "AutoMDM",
					Location:  "Earth",
					RenewDate: tokenExpiration,
					Token:     string(token),
					Teams:     nil,
				}, nil
			}

			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{}, nil
			}
			ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
				if tt.teamID != nil {
					return &automdm.TeamLite{ID: *tt.teamID}, nil
				}

				return nil, nil
			}

			ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) {
				return false, nil
			}

			ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
				_ sqlx.QueryerContext,
			) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
				return map[automdm.MDMAssetName]automdm.MDMConfigAsset{}, nil
			}

			_, err = svc.DownloadSoftwareInstaller(ctx, false, "media", 1, tt.teamID, nil)
			if tt.teamID == nil {
				require.Error(t, err)
			} else {
				checkAuthErr(t, tt.shouldFailRead, err)
			}

			err = svc.DeleteSoftwareInstaller(ctx, 1, tt.teamID, nil, nil)
			if tt.teamID == nil {
				require.Error(t, err)
			} else {
				checkAuthErr(t, tt.shouldFailWrite, err)
			}

			// Note: these calls always return an error because they're attempting to unmarshal a
			// non-existent VPP token.
			_, err = svc.GetAppStoreApps(ctx, tt.teamID, nil)
			if tt.teamID == nil {
				require.Error(t, err)
			} else if tt.shouldFailRead {
				checkAuthErr(t, true, err)
			}

			_, err = svc.AddAppStoreApp(ctx, tt.teamID, automdm.VPPAppTeam{AdamID: "123", Platform: automdm.IOSPlatform})
			if tt.teamID == nil {
				require.Error(t, err)
			} else if tt.shouldFailWrite {
				checkAuthErr(t, true, err)
			}

			// TODO: configure test with mock software installer store and add tests to check upload auth
		})
	}
}

func TestUpgradeCodeMigration(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	_, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license})

	dir := t.TempDir()
	softwareInstallStore, err := filesystem.NewSoftwareInstallerStore(dir)
	require.NoError(t, err)
	tfr, err := automdm.NewKeepFileReader(filepath.Join("testdata", "software-installers", "automdm-osquery.msi"))
	require.NoError(t, err)
	defer tfr.Close()
	require.NoError(t, softwareInstallStore.Put(ctx, "deadbeef", tfr))

	ds.GetMSIInstallersWithoutUpgradeCodeFunc = func(ctx context.Context) (map[uint]string, error) {
		return map[uint]string{uint(1): "deadbeef", uint(2): "deadbeef", uint(3): "noexist", uint(4): "noexist"}, nil
	}

	updatedInstallerIDs := map[uint]struct{}{}
	ds.UpdateInstallerUpgradeCodeFunc = func(ctx context.Context, installerID uint, upgradeCode string) error {
		updatedInstallerIDs[installerID] = struct{}{}
		require.Equal(t, "{B681CB20-107E-428A-9B14-2D3C1AFED244}", upgradeCode)
		return nil
	}

	require.NoError(t, eeservice.UpgradeCodeMigration(ctx, ds, softwareInstallStore, slog.New(slog.DiscardHandler)))
	require.True(t, ds.UpdateInstallerUpgradeCodeFuncInvoked)
	require.Len(t, updatedInstallerIDs, 2)
}

// TestValidateSoftwareLabels tests logic for validating labels associated with software (VPP apps,
// FMAs, and custom packages)
func TestValidateSoftwareLabels(t *testing.T) {
	ds := new(mock.Store)

	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license})

	t.Run("validate no update", func(t *testing.T) {
		t.Run("no auth context", func(t *testing.T) {
			_, err := eeservice.ValidateSoftwareLabels(context.Background(), svc, nil, nil, nil, nil)
			require.ErrorContains(t, err, "Authentication required")
		})

		authCtx := authz_ctx.AuthorizationContext{}
		ctx = authz_ctx.NewContext(ctx, &authCtx)

		t.Run("no auth checked", func(t *testing.T) {
			_, err := eeservice.ValidateSoftwareLabels(ctx, svc, nil, nil, nil, nil)
			require.ErrorContains(t, err, "Authentication required")
		})

		// validator requires that an authz check has been performed upstream so we'll set it now for
		// the rest of the tests
		authCtx.SetChecked()
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

		mockLabels := map[string]uint{
			"foo": 1,
			"bar": 2,
			"baz": 3,
		}

		ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
			res := make(map[string]uint)
			if names == nil {
				return res, nil
			}
			for _, name := range names {
				if id, ok := mockLabels[name]; ok {
					res[name] = id
				}
			}
			return res, nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			res := make(map[string]*automdm.Label)
			if names == nil {
				return res, nil
			}
			for _, name := range names {
				if id, ok := mockLabels[name]; ok {
					res[name] = &automdm.Label{
						ID:   id,
						Name: name,
					}
				}
			}
			return res, nil
		}

		testCases := []struct {
			name              string
			payloadIncludeAny []string
			payloadExcludeAny []string
			payloadIncludeAll []string
			expectLabels      map[string]automdm.LabelIdent
			expectScope       automdm.LabelScope
			expectError       string
		}{
			{
				"no labels",
				nil,
				nil,
				nil,
				nil,
				"",
				"",
			},
			{
				"include labels",
				[]string{"foo", "bar"},
				nil,
				nil,
				map[string]automdm.LabelIdent{
					"foo": {LabelID: 1, LabelName: "foo"},
					"bar": {LabelID: 2, LabelName: "bar"},
				},
				automdm.LabelScopeIncludeAny,
				"",
			},
			{
				"exclude labels",
				nil,
				[]string{"bar", "baz"},
				nil,
				map[string]automdm.LabelIdent{
					"bar": {LabelID: 2, LabelName: "bar"},
					"baz": {LabelID: 3, LabelName: "baz"},
				},
				automdm.LabelScopeExcludeAny,
				"",
			},
			{
				"include and exclude labels",
				[]string{"foo"},
				[]string{"bar"},
				nil,
				nil,
				"",
				`Only one of "labels_include_all", "labels_include_any" or "labels_exclude_any" can be included.`,
			},
			{
				"non-existent label",
				[]string{"foo", "qux"},
				nil,
				nil,
				nil,
				"",
				`Couldn't update. Label "qux" doesn't exist. Please remove the label from the software`,
			},
			{
				"duplicate label",
				[]string{"foo", "foo"},
				nil,
				nil,
				map[string]automdm.LabelIdent{
					"foo": {LabelID: 1, LabelName: "foo"},
				},
				automdm.LabelScopeIncludeAny,
				"",
			},
			{
				"empty slice",
				nil,
				[]string{},
				nil,
				nil,
				"",
				"",
			},
			{
				"empty string",
				nil,
				[]string{""},
				nil,
				nil,
				"",
				`Couldn't update. Label "" doesn't exist. Please remove the label from the software`,
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				got, err := eeservice.ValidateSoftwareLabels(ctx, svc, nil, tt.payloadIncludeAny, tt.payloadExcludeAny, tt.payloadIncludeAll)
				if tt.expectError != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectError)
				} else {
					require.NoError(t, err)
					require.NotNil(t, got)
					require.Equal(t, tt.expectScope, got.LabelScope)
					require.Equal(t, tt.expectLabels, got.ByName)
				}
			})
		}
	})

	t.Run("validate update", func(t *testing.T) {
		t.Run("no auth context", func(t *testing.T) {
			_, _, err := eeservice.ValidateSoftwareLabelsForUpdate(context.Background(), svc, nil, nil, nil, nil)
			require.ErrorContains(t, err, "Authentication required")
		})

		authCtx := authz_ctx.AuthorizationContext{}
		ctx = authz_ctx.NewContext(ctx, &authCtx)

		t.Run("no auth checked", func(t *testing.T) {
			_, _, err := eeservice.ValidateSoftwareLabelsForUpdate(ctx, svc, nil, nil, nil, nil)
			require.ErrorContains(t, err, "Authentication required")
		})

		// validator requires that an authz check has been performed upstream so we'll set it now for
		// the rest of the tests
		authCtx.SetChecked()

		mockLabels := map[string]uint{
			"foo": 1,
			"bar": 2,
			"baz": 3,
		}

		ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
			res := make(map[string]uint)
			if names == nil {
				return res, nil
			}
			for _, name := range names {
				if id, ok := mockLabels[name]; ok {
					res[name] = id
				}
			}
			return res, nil
		}

		testCases := []struct {
			name              string
			existingInstaller *automdm.SoftwareInstaller
			payloadIncludeAny []string
			payloadExcludeAny []string
			payloadIncludeAll []string
			shouldUpdate      bool
			expectLabels      map[string]automdm.LabelIdent
			expectScope       automdm.LabelScope
			expectError       string
		}{
			{
				"no installer",
				nil,
				nil,
				[]string{"foo"},
				nil,
				false,
				nil,
				"",
				"existing installer must be provided",
			},
			{
				"no labels",
				&automdm.SoftwareInstaller{},
				nil,
				nil,
				nil,
				false,
				nil,
				"",
				"",
			},
			{
				"add label",
				&automdm.SoftwareInstaller{
					LabelsIncludeAny: []automdm.SoftwareScopeLabel{{LabelID: 1, LabelName: "foo"}},
					LabelsExcludeAny: []automdm.SoftwareScopeLabel{},
				},
				[]string{"foo", "bar"},
				nil,
				nil,
				true,
				map[string]automdm.LabelIdent{
					"foo": {LabelID: 1, LabelName: "foo"},
					"bar": {LabelID: 2, LabelName: "bar"},
				},
				automdm.LabelScopeIncludeAny,
				"",
			},
			{
				"change scope",
				&automdm.SoftwareInstaller{
					LabelsIncludeAny: []automdm.SoftwareScopeLabel{{LabelID: 1, LabelName: "foo"}},
					LabelsExcludeAny: []automdm.SoftwareScopeLabel{},
				},
				nil,
				[]string{"foo"},
				nil,
				true,
				map[string]automdm.LabelIdent{
					"foo": {LabelID: 1, LabelName: "foo"},
				},
				automdm.LabelScopeExcludeAny,
				"",
			},
			{
				"remove label",
				&automdm.SoftwareInstaller{
					LabelsIncludeAny: []automdm.SoftwareScopeLabel{{LabelID: 1, LabelName: "foo"}},
					LabelsExcludeAny: []automdm.SoftwareScopeLabel{},
				},
				[]string{},
				nil,
				nil,
				true,
				nil,
				"",
				"",
			},
			{
				"no change",
				&automdm.SoftwareInstaller{
					LabelsIncludeAny: []automdm.SoftwareScopeLabel{{LabelID: 1, LabelName: "foo"}},
					LabelsExcludeAny: []automdm.SoftwareScopeLabel{},
				},
				[]string{"foo"},
				nil,
				nil,
				false,
				nil,
				"",
				"",
			},
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				shouldUpate, got, err := eeservice.ValidateSoftwareLabelsForUpdate(ctx, svc, tt.existingInstaller, tt.payloadIncludeAny, tt.payloadExcludeAny, tt.payloadIncludeAll)
				if tt.expectError != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectError)
				} else {
					require.NoError(t, err)
					if tt.shouldUpdate {
						require.True(t, shouldUpate)
						require.NotNil(t, got)
						require.Equal(t, tt.expectScope, got.LabelScope)
						require.Equal(t, tt.expectLabels, got.ByName)
					} else {
						require.False(t, shouldUpate)
						require.Nil(t, got)
					}
				}
			})
		}
	})
}

func getPathRelative(relativePath string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get runtime caller info")
	}
	sourceDir := filepath.Dir(currentFile)
	return filepath.Join(sourceDir, relativePath)
}

type mockInstallerStore struct {
	putCount atomic.Int32
	onPut    func()
}

func (m *mockInstallerStore) Get(ctx context.Context, iconID string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("mock installer store get")
}

func (m *mockInstallerStore) Put(ctx context.Context, iconID string, content io.ReadSeeker) error {
	m.putCount.Add(1)
	if m.putCount.Load() == automdm.BatchUploadMaxRetries {
		m.onPut()
	}

	return errors.New("mock store put")
}

func (m *mockInstallerStore) Exists(ctx context.Context, iconID string) (bool, error) {
	return false, nil
}

func (m *mockInstallerStore) Cleanup(ctx context.Context, usedIconIDs []string, removeCreatedBefore time.Time) (int, error) {
	return 0, nil
}

func (m *mockInstallerStore) Sign(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", errors.New("mock store sign")
}

func TestSoftwareInstallerUploadRetries(t *testing.T) {
	dev_mode.SetOverride("AUTOMDM_DEV_BATCH_RETRY_INTERVAL", "1s")
	defer dev_mode.ClearOverride("AUTOMDM_DEV_BATCH_RETRY_INTERVAL")

	ds := new(mock.Store)
	lic := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	kvStore := &mock.KVStore{}
	kvStore.SetFunc = func(ctx context.Context, key string, value string, expireTime time.Duration) error {
		return nil
	}
	var statusMu sync.Mutex
	status := automdm.BatchSetSoftwareInstallersStatusProcessing
	kvStore.GetFunc = func(ctx context.Context, key string) (*string, error) {
		statusMu.Lock()
		defer statusMu.Unlock()
		// Only the batch status key holds a value here. The sibling keys for deleted
		// packages, categories and download progress are all empty.
		if strings.Contains(key, ":") {
			return nil, nil
		}
		return ptr.String(status), nil
	}

	installerStore := new(mockInstallerStore)
	installerStore.onPut = func() {
		statusMu.Lock()
		defer statusMu.Unlock()
		status = automdm.BatchSetSoftwareInstallersStatusFailed + ":"
	}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: lic, SoftwareInstallStore: installerStore, KeyValueStore: kvStore})

	authCtx := authz_ctx.AuthorizationContext{}
	ctx = authz_ctx.NewContext(ctx, &authCtx)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: test.UserAdmin})

	actx, _ := authz_ctx.FromContext(ctx)
	actx.SetChecked()

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return &automdm.Team{ID: 1, Name: "foo"}, nil
	}

	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: 1, Name: "foo"}, nil
	}

	ds.BatchSetSoftwareInstallersFunc = func(ctx context.Context, tmID *uint, installers []*automdm.UploadSoftwareInstallerPayload) ([]uint, error) {
		return nil, nil
	}

	ds.BatchSetInHouseAppsInstallersFunc = func(ctx context.Context, tmID *uint, installers []*automdm.UploadSoftwareInstallerPayload) error {
		return nil
	}

	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	ds.GetTeamsWithInstallerByHashFunc = func(ctx context.Context, sha256 string, url string) (map[uint][]*automdm.ExistingSoftwareInstaller, error) {
		return map[uint][]*automdm.ExistingSoftwareInstaller{}, nil
	}

	ds.GetInstallerByTeamAndURLFunc = func(ctx context.Context, teamID *uint, url string) (*automdm.ExistingSoftwareInstaller, error) {
		return nil, nil
	}

	ds.GetSoftwareInstallersFunc = func(ctx context.Context, tmID uint) ([]automdm.SoftwarePackageResponse, error) {
		return []automdm.SoftwarePackageResponse{}, nil
	}

	ds.GetSoftwareInstallersPendingDeletionFunc = func(ctx context.Context, tmID *uint, incoming []automdm.SoftwareTitleIdentifier) ([]automdm.DeletedSoftwarePackage, error) {
		return nil, nil
	}

	baseDir := getPathRelative("./testdata/software-installers/")

	// start the web server that will serve the installer
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filepath.Join(baseDir, filepath.Base(r.URL.Path)))

			},
		),
	)
	t.Cleanup(srv.Close)

	_, err := svc.BatchSetSoftwareInstallers(ctx, "foo", []*automdm.SoftwareInstallerPayload{{
		URL:               srv.URL + "/dummy_installer.pkg",
		InstallScript:     "install",
		UninstallScript:   "uninstall",
		SelfService:       false,
		AutoMDMMaintained: false,
		Filename:          "foo",
		ValidatedLabels:   &automdm.LabelIdentsWithScope{},
		Categories:        optjson.SetSlice([]string{}),
		DisplayName:       "foo",
	}}, false)
	require.NoError(t, err)

	timeout := time.After(30 * time.Second)
	for {
		result, err := svc.GetBatchSetSoftwareInstallersResult(ctx, "foo", "requestuuid", false)
		require.NoError(t, err)
		// The status will be failed IFF
		// the mock installer store's Put method was called automdm.BatchUploadMaxRetries times.
		if result.Status == automdm.BatchSetSoftwareInstallersStatusFailed {
			require.Empty(t, result.Packages)
			break
		}
		select {
		case <-timeout:
			// If the max number of retries isn't hit, then the test will timeout.
			t.Fatalf("get batch set software installers result timeout")
		case <-time.After(500 * time.Millisecond):
			// OK, continue
		}
	}

}

func TestGetBatchSetSoftwareInstallersResultAuth(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	kvStore := &mock.KVStore{}
	kvStore.GetFunc = func(ctx context.Context, key string) (*string, error) {
		// Completed is the only status that authorizes against the team.
		if strings.Contains(key, ":") {
			return nil, nil
		}
		return new(automdm.BatchSetSoftwareInstallersStatusCompleted), nil
	}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, KeyValueStore: kvStore})

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return &automdm.Team{ID: 1, Name: name}, nil
	}
	ds.GetSoftwareInstallersFunc = func(ctx context.Context, teamID uint) ([]automdm.SoftwarePackageResponse, error) {
		return nil, nil
	}

	// Reading a batch result takes the same read as the team's installers, so observers are
	// out even though they can read the team's software titles.
	testCases := []struct {
		name       string
		user       *automdm.User
		teamName   string
		shouldFail bool
	}{
		{"global admin", test.UserAdmin, "team1", false},
		{"global maintainer", test.UserMaintainer, "team1", false},
		{"global technician", test.UserTechnician, "team1", false},
		{"global gitops", test.UserGitOps, "team1", false},
		{"global observer", test.UserObserver, "team1", true},
		{"global observer+", test.UserObserverPlus, "team1", true},
		{"no role", test.UserNoRoles, "team1", true},
		{"team admin", test.UserTeamAdminTeam1, "team1", false},
		{"team technician", test.UserTeamTechnicianTeam1, "team1", false},
		{"team gitops", test.UserTeamGitOpsTeam1, "team1", false},
		{"team observer", test.UserTeamObserverTeam1, "team1", true},
		{"team observer+", test.UserTeamObserverPlusTeam1, "team1", true},
		{"team admin other team", test.UserTeamAdminTeam2, "team1", true},
		{"team technician other team", test.UserTeamTechnicianTeam2, "team1", true},
		{"global admin unassigned", test.UserAdmin, "", false},
		{"global observer unassigned", test.UserObserver, "", true},
		{"team admin unassigned", test.UserTeamAdminTeam1, "", true},
		{"no role unassigned", test.UserNoRoles, "", true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.GetBatchSetSoftwareInstallersResult(ctx, tt.teamName, "request-uuid", false)
			checkAuthErr(t, tt.shouldFail, err)
		})
	}

	// A running batch reports only progress, so anyone logged in can poll it.
	t.Run("polling a running batch only takes a logged in user", func(t *testing.T) {
		processingKVStore := &mock.KVStore{}
		processingKVStore.GetFunc = func(ctx context.Context, key string) (*string, error) {
			if strings.Contains(key, ":") {
				return nil, nil
			}
			return new(automdm.BatchSetSoftwareInstallersStatusProcessing), nil
		}
		processingSvc, processingCtx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, KeyValueStore: processingKVStore})

		ctx := viewer.NewContext(processingCtx, viewer.Viewer{User: test.UserTeamObserverTeam1})
		result, err := processingSvc.GetBatchSetSoftwareInstallersResult(ctx, "team1", "request-uuid", false)
		require.NoError(t, err)
		require.Equal(t, automdm.BatchSetSoftwareInstallersStatusProcessing, result.Status)
	})
}

func TestSoftwareBatchProgressWriteFailure(t *testing.T) {
	// Progress is only ever printed for the user, so losing it must not turn a batch that
	// would have succeeded into a failed one.
	ds := new(mock.Store)
	lic := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	var kvMu sync.Mutex
	batchStatus := automdm.BatchSetSoftwareInstallersStatusProcessing

	kvStore := &mock.KVStore{}
	kvStore.SetFunc = func(ctx context.Context, key string, value string, expireTime time.Duration) error {
		kvMu.Lock()
		defer kvMu.Unlock()
		switch {
		case strings.HasSuffix(key, ":downloaded"):
			return errors.New("progress write failed")
		case !strings.Contains(key, ":"):
			batchStatus = value
		}
		return nil
	}
	kvStore.GetFunc = func(ctx context.Context, key string) (*string, error) {
		kvMu.Lock()
		defer kvMu.Unlock()
		if strings.Contains(key, ":") {
			return nil, nil
		}
		return new(batchStatus), nil
	}

	softwareInstallStore, err := filesystem.NewSoftwareInstallerStore(t.TempDir())
	require.NoError(t, err)

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
		License:              lic,
		SoftwareInstallStore: softwareInstallStore,
		KeyValueStore:        kvStore,
	})

	authCtx := authz_ctx.AuthorizationContext{}
	ctx = authz_ctx.NewContext(ctx, &authCtx)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: test.UserAdmin})
	actx, _ := authz_ctx.FromContext(ctx)
	actx.SetChecked()

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return &automdm.Team{ID: 1, Name: "foo"}, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: 1, Name: "foo"}, nil
	}
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.ValidateReferencedCustomHostVitalsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return map[string]uint{}, nil
	}
	ds.GetTeamsWithInstallerByHashFunc = func(ctx context.Context, sha256 string, url string) (map[uint][]*automdm.ExistingSoftwareInstaller, error) {
		return map[uint][]*automdm.ExistingSoftwareInstaller{}, nil
	}
	ds.GetInstallerByTeamAndURLFunc = func(ctx context.Context, teamID *uint, url string) (*automdm.ExistingSoftwareInstaller, error) {
		return nil, nil
	}
	ds.BatchSetSoftwareInstallersFunc = func(ctx context.Context, tmID *uint, installers []*automdm.UploadSoftwareInstallerPayload) ([]uint, error) {
		return nil, nil
	}
	ds.BatchSetInHouseAppsInstallersFunc = func(ctx context.Context, tmID *uint, installers []*automdm.UploadSoftwareInstallerPayload) error {
		return nil
	}
	ds.GetSoftwareInstallersPendingDeletionFunc = func(ctx context.Context, tmID *uint, incoming []automdm.SoftwareTitleIdentifier) ([]automdm.DeletedSoftwarePackage, error) {
		return nil, nil
	}
	ds.GetSoftwareInstallersFunc = func(ctx context.Context, tmID uint) ([]automdm.SoftwarePackageResponse, error) {
		return []automdm.SoftwarePackageResponse{}, nil
	}

	baseDir := getPathRelative("./testdata/software-installers/")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(baseDir, filepath.Base(r.URL.Path)))
	}))
	t.Cleanup(srv.Close)

	requestUUID, err := svc.BatchSetSoftwareInstallers(ctx, "foo", []*automdm.SoftwareInstallerPayload{{
		URL:             srv.URL + "/dummy_installer.pkg",
		InstallScript:   "install",
		UninstallScript: "uninstall",
		ValidatedLabels: &automdm.LabelIdentsWithScope{},
		Categories:      optjson.SetSlice([]string{}),
	}}, false)
	require.NoError(t, err)

	timeout := time.After(10 * time.Second)
	for {
		result, err := svc.GetBatchSetSoftwareInstallersResult(ctx, "foo", requestUUID, false)
		require.NoError(t, err)
		if result.Status == automdm.BatchSetSoftwareInstallersStatusCompleted {
			break
		}
		require.NotEqual(t, automdm.BatchSetSoftwareInstallersStatusFailed, result.Status, result.Message)
		select {
		case <-timeout:
			t.Fatal("batch never completed")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
