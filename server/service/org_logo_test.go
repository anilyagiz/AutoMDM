package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrgLogoAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.SaveAppConfigFunc = func(ctx context.Context, conf *automdm.AppConfig) error {
		return nil
	}

	testCases := []struct {
		name            string
		user            *automdm.User
		shouldFailWrite bool // PUT and DELETE
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: new(automdm.RoleAdmin)},
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: new(automdm.RoleMaintainer)},
			true,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: new(automdm.RoleObserver)},
			true,
		},
		{
			"global observer+",
			&automdm.User{GlobalRole: new(automdm.RoleObserverPlus)},
			true,
		},
		{
			// Global gitops can write app_config (per the rego policy),
			// which is what automdmctl gitops uses to upload custom org
			// logos via the new org_logo_path_*_mode keys.
			"global gitops",
			&automdm.User{GlobalRole: new(automdm.RoleGitOps)},
			false,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
		},
		{
			"team observer+",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			true,
		},
		{
			"team gitops",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			true,
		},
		{
			"user without roles",
			&automdm.User{ID: 777},
			true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			authedCtx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			err := svc.UploadOrgLogo(authedCtx, automdm.OrgLogoModeLight, bytes.NewReader([]byte{}))
			checkOrgLogoAuth(t, tt.shouldFailWrite, err)

			err = svc.DeleteOrgLogo(authedCtx, automdm.OrgLogoModeLight)
			checkOrgLogoAuth(t, tt.shouldFailWrite, err)

			// GET is public — never an authz failure regardless of viewer.
			_, _, err = svc.GetOrgLogo(authedCtx, automdm.OrgLogoModeLight)
			checkOrgLogoAuth(t, false, err)
		})
	}

	// GET should also work without any viewer in the context (login page
	// case). It may still fail downstream because no store is wired, but
	// that's not an authz failure.
	t.Run("public GET without viewer", func(t *testing.T) {
		_, _, err := svc.GetOrgLogo(ctx, automdm.OrgLogoModeLight)
		checkOrgLogoAuth(t, false, err)
	})
}

// testPNG returns a tiny valid PNG (1x1, RGBA) for tests that need to plant
// real bytes in the filesystem-backed org logo store.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0, G: 128, B: 0, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestDeleteOrgLogo(t *testing.T) {
	setup := func(t *testing.T, ac *automdm.AppConfig) (automdm.Service, context.Context, *automdm.AppConfig, *bool) {
		t.Helper()
		ds := new(mock.Store)
		opts := &TestServerOpts{}
		svc, ctx := newTestService(t, ds, nil, nil, opts)
		ctx = viewer.NewContext(ctx, viewer.Viewer{
			User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)},
		})

		// In-memory AppConfig backed by a pointer the caller can inspect.
		stored := *ac
		ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
			cp := stored
			return &cp, nil
		}
		ds.SaveAppConfigFunc = func(_ context.Context, conf *automdm.AppConfig) error {
			stored = *conf
			return nil
		}

		fired := false
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, act activity_api.ActivityDetails) error {
			if _, ok := act.(automdm.ActivityTypeDeletedOrgLogo); ok {
				fired = true
			}
			return nil
		}

		return svc, ctx, &stored, &fired
	}

	t.Run("idempotent: empty state is a no-op", func(t *testing.T) {
		svc, ctx, stored, fired := setup(t, &automdm.AppConfig{})
		require.NoError(t, svc.DeleteOrgLogo(ctx, automdm.OrgLogoModeDark))
		assert.False(t, *fired, "no real change happened — no activity")
		assert.Empty(t, stored.OrgInfo.OrgLogoURLDarkMode)
	})

	t.Run("external URL, no blob: clears URL fields and fires activity", func(t *testing.T) {
		svc, ctx, stored, fired := setup(t, &automdm.AppConfig{
			OrgInfo: automdm.OrgInfo{
				OrgLogoURLDarkMode: "https://placehold.co/100",
			},
		})
		require.NoError(t, svc.DeleteOrgLogo(ctx, automdm.OrgLogoModeDark))
		assert.True(t, *fired)
		assert.Empty(t, stored.OrgInfo.OrgLogoURLDarkMode)
	})

	t.Run("blob only, URL already empty: deletes blob and fires activity", func(t *testing.T) {
		svc, ctx, stored, fired := setup(t, &automdm.AppConfig{})
		// Plant a real blob via UploadOrgLogo so the filesystem store backing
		// newTestService actually has bytes for Exists/Delete to find. Then
		// zero out the URL to simulate a state where the URL was cleared
		// out-of-band (the gitops PATCH-first flow before this change).
		require.NoError(t, svc.UploadOrgLogo(ctx, automdm.OrgLogoModeDark, bytes.NewReader(testPNG(t))))
		stored.OrgInfo.OrgLogoURLDarkMode = ""
		*fired = false // UploadOrgLogo fires its own activity; reset.

		require.NoError(t, svc.DeleteOrgLogo(ctx, automdm.OrgLogoModeDark))
		assert.True(t, *fired, "activity fires when a blob is deleted, even if URL was already empty")
	})

	t.Run("URL + blob: clears URL, deletes blob, fires activity", func(t *testing.T) {
		svc, ctx, stored, fired := setup(t, &automdm.AppConfig{})
		require.NoError(t, svc.UploadOrgLogo(ctx, automdm.OrgLogoModeLight, bytes.NewReader(testPNG(t))))
		*fired = false

		require.NoError(t, svc.DeleteOrgLogo(ctx, automdm.OrgLogoModeLight))
		assert.True(t, *fired)
		assert.Empty(t, stored.OrgInfo.OrgLogoURLLightMode)
	})

	t.Run("mode=all clears every mode", func(t *testing.T) {
		svc, ctx, stored, fired := setup(t, &automdm.AppConfig{
			OrgInfo: automdm.OrgInfo{
				OrgLogoURLLightMode: "https://example.com/light.png",
				OrgLogoURLDarkMode:  "https://example.com/dark.png",
			},
		})
		require.NoError(t, svc.DeleteOrgLogo(ctx, automdm.OrgLogoModeAll))
		assert.True(t, *fired, "delete all should fire deleted_org_logo activity")
		assert.Empty(t, stored.OrgInfo.OrgLogoURLLightMode)
		assert.Empty(t, stored.OrgInfo.OrgLogoURLDarkMode)
	})
}

func TestUploadOrgLogoFiresActivity(t *testing.T) {
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	ctx = viewer.NewContext(ctx, viewer.Viewer{
		User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)},
	})

	dsAppConfig := &automdm.AppConfig{}
	ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		cp := *dsAppConfig
		return &cp, nil
	}
	ds.SaveAppConfigFunc = func(_ context.Context, conf *automdm.AppConfig) error {
		*dsAppConfig = *conf
		return nil
	}

	var changedFired int
	var changedMode string
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, act activity_api.ActivityDetails) error {
		if a, ok := act.(automdm.ActivityTypeChangedOrgLogo); ok {
			changedFired++
			changedMode = a.Mode
		}
		return nil
	}

	require.NoError(t, svc.UploadOrgLogo(ctx, automdm.OrgLogoModeLight, bytes.NewReader(testPNG(t))))
	assert.Equal(t, 1, changedFired, "upload must fire a single changed_org_logo activity")
	assert.Equal(t, string(automdm.OrgLogoModeLight), changedMode, "activity must record the uploaded mode")
}

func checkOrgLogoAuth(t *testing.T, shouldFail bool, err error) {
	t.Helper()
	var forbidden *authz.Forbidden
	if shouldFail {
		require.Error(t, err)
		require.ErrorAs(t, err, &forbidden, "expected authz Forbidden, got %T: %v", err, err)
		return
	}
	if err != nil {
		require.NotErrorAs(t, err, &forbidden,
			"expected non-authz error, got authz Forbidden: %v", err)
	}
}
