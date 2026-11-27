package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestAutoUpdateAutoMDMMaintainedApps(t *testing.T) {
	// Cached versions for the title, most recently downloaded first (as the real
	// datastore returns them).
	versions := []automdm.AutoMDMMaintainedVersion{
		{ID: 12, Version: "149.0.2"},
		{ID: 11, Version: "147.0.9"},
		{ID: 9, Version: "148.0.1"},
		{ID: 8, Version: "147.0.5"},
	}

	teamID := uint(1)
	candidate := func(activeID uint, activeVer string) automdm.FMAAutoUpdateCandidate {
		return automdm.FMAAutoUpdateCandidate{
			TeamID:      &teamID,
			TitleID:     1,
			InstallerID: activeID,
			Version:     activeVer,
			Slug:        "chrome/darwin",
		}
	}

	cases := []struct {
		name string
		// pin: nil => no pin row (Latest); otherwise the stored expression.
		pin          *string
		active       automdm.FMAAutoUpdateCandidate
		cached       []automdm.AutoMDMMaintainedVersion
		wantFlip     bool
		wantActiveID uint // installer ID bound active (when wantFlip)
	}{
		{
			name:         "Latest advances to newest cached",
			pin:          nil,
			active:       candidate(9, "148.0.1"),
			cached:       versions,
			wantFlip:     true,
			wantActiveID: 12,
		},
		{
			name:     "Latest already on newest is a no-op",
			pin:      nil,
			active:   candidate(12, "149.0.2"),
			cached:   versions,
			wantFlip: false,
		},
		{
			name:         "caret advances within major",
			pin:          new("^147"),
			active:       candidate(8, "147.0.5"),
			cached:       versions,
			wantFlip:     true,
			wantActiveID: 11, // 147.0.9, newest within major 147
		},
		{
			name:     "caret never crosses major when no in-major version is cached",
			pin:      new("^147"),
			active:   candidate(12, "149.0.2"),
			cached:   []automdm.AutoMDMMaintainedVersion{{ID: 12, Version: "149.0.2"}},
			wantFlip: false,
		},
		{
			name:     "literal pin never advances",
			pin:      new("148.0.1"),
			active:   candidate(9, "148.0.1"),
			cached:   versions,
			wantFlip: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.SyncPatchPolicyQueriesFunc = func(ctx context.Context) error { return nil }

			ds.ListAutoMDMMaintainedAppActiveInstallersFunc = func(ctx context.Context) ([]automdm.FMAAutoUpdateCandidate, error) {
				return []automdm.FMAAutoUpdateCandidate{tc.active}, nil
			}
			ds.GetPinnedVersionFunc = func(ctx context.Context, tmID *uint, titleID uint) (*string, error) {
				if tc.pin == nil {
					return nil, sql.ErrNoRows
				}
				return tc.pin, nil
			}
			ds.GetAutoMDMMaintainedVersionsByTitleIDFunc = func(ctx context.Context, tmID *uint, titleID uint) ([]automdm.AutoMDMMaintainedVersion, error) {
				return tc.cached, nil
			}

			var gotActiveID uint
			ds.SetAutoMDMMaintainedAppActiveInstallerFunc = func(ctx context.Context, payload *automdm.UpdateSoftwareInstallerPayload, activeInstallerID uint) error {
				gotActiveID = activeInstallerID
				// The cron must never write pin state, or it could clobber a
				// concurrent admin pin change.
				require.Nil(t, payload.PinnedVersion, "cron must not write the pin row")
				return nil
			}

			// nil store: promote-only mode (no upstream download), exercising
			// advancement among already-cached versions.
			err := AutoUpdateAutoMDMMaintainedApps(context.Background(), ds, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, err)

			require.Equal(t, tc.wantFlip, ds.SetAutoMDMMaintainedAppActiveInstallerFuncInvoked)
			if tc.wantFlip {
				require.Equal(t, tc.wantActiveID, gotActiveID)
			}
		})
	}
}

func TestAutoUpdateAutoMDMMaintainedAppsContinuesPastError(t *testing.T) {
	ds := new(mock.Store)
	ds.SyncPatchPolicyQueriesFunc = func(ctx context.Context) error { return errors.New("sync boom") }
	teamID := uint(1)
	ds.ListAutoMDMMaintainedAppActiveInstallersFunc = func(ctx context.Context) ([]automdm.FMAAutoUpdateCandidate, error) {
		return []automdm.FMAAutoUpdateCandidate{
			{TeamID: &teamID, TitleID: 1, InstallerID: 9, Slug: "bad/darwin"},
			{TeamID: &teamID, TitleID: 2, InstallerID: 20, Slug: "good/darwin"},
		}, nil
	}
	ds.GetPinnedVersionFunc = func(ctx context.Context, tmID *uint, titleID uint) (*string, error) {
		if titleID == 1 {
			return nil, errors.New("boom")
		}
		return nil, sql.ErrNoRows
	}
	ds.GetAutoMDMMaintainedVersionsByTitleIDFunc = func(ctx context.Context, tmID *uint, titleID uint) ([]automdm.AutoMDMMaintainedVersion, error) {
		return []automdm.AutoMDMMaintainedVersion{{ID: 21, Version: "2.0.0"}}, nil
	}
	var flippedTitle uint
	ds.SetAutoMDMMaintainedAppActiveInstallerFunc = func(ctx context.Context, payload *automdm.UpdateSoftwareInstallerPayload, activeInstallerID uint) error {
		flippedTitle = payload.TitleID
		return nil
	}
	ds.ProcessInstallerUpdateSideEffectsFunc = func(ctx context.Context, installerID uint, wasMetadataUpdated, wasPackageUpdated bool) error {
		return nil
	}

	// The patch policy sync and the first candidate error; the run must still process the second.
	err := AutoUpdateAutoMDMMaintainedApps(context.Background(), ds, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	require.True(t, ds.SyncPatchPolicyQueriesFuncInvoked)
	require.True(t, ds.SetAutoMDMMaintainedAppActiveInstallerFuncInvoked)
	require.Equal(t, uint(2), flippedTitle)
}

func TestAutoUpdateAutoMDMMaintainedAppsBoundsPatchPolicySync(t *testing.T) {
	ds := new(mock.Store)
	ds.SyncPatchPolicyQueriesFunc = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok, "patch policy sync must have its own deadline")
		require.WithinDuration(t, time.Now().Add(patchPolicySyncMaxRunTime), deadline, time.Minute)
		return context.DeadlineExceeded
	}
	teamID := uint(1)
	ds.ListAutoMDMMaintainedAppActiveInstallersFunc = func(ctx context.Context) ([]automdm.FMAAutoUpdateCandidate, error) {
		require.NoError(t, ctx.Err(), "the sync's budget running out must not cancel installer updates")
		return []automdm.FMAAutoUpdateCandidate{{TeamID: &teamID, TitleID: 1, InstallerID: 9, Slug: "only/darwin"}}, nil
	}
	ds.GetPinnedVersionFunc = func(ctx context.Context, tmID *uint, titleID uint) (*string, error) {
		return nil, sql.ErrNoRows
	}
	ds.GetAutoMDMMaintainedVersionsByTitleIDFunc = func(ctx context.Context, tmID *uint, titleID uint) ([]automdm.AutoMDMMaintainedVersion, error) {
		return []automdm.AutoMDMMaintainedVersion{{ID: 9, Version: "1.0"}}, nil
	}

	err := AutoUpdateAutoMDMMaintainedApps(t.Context(), ds, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	require.True(t, ds.GetAutoMDMMaintainedVersionsByTitleIDFuncInvoked)
}

func TestAutoUpdateAutoMDMMaintainedAppsReportsCancelDuringLastApp(t *testing.T) {
	ds := new(mock.Store)
	ds.SyncPatchPolicyQueriesFunc = func(ctx context.Context) error { return nil }
	teamID := uint(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ds.ListAutoMDMMaintainedAppActiveInstallersFunc = func(ctx context.Context) ([]automdm.FMAAutoUpdateCandidate, error) {
		return []automdm.FMAAutoUpdateCandidate{
			{TeamID: &teamID, TitleID: 1, InstallerID: 9, Slug: "only/darwin"},
		}, nil
	}
	// The budget runs out while the last app is in flight, so no later pass through the loop sees it.
	ds.GetPinnedVersionFunc = func(ctx context.Context, tmID *uint, titleID uint) (*string, error) {
		cancel()
		return nil, sql.ErrNoRows
	}
	// Already on the newest cached version, so the app itself is a no-op and only the budget is left to report.
	ds.GetAutoMDMMaintainedVersionsByTitleIDFunc = func(ctx context.Context, tmID *uint, titleID uint) ([]automdm.AutoMDMMaintainedVersion, error) {
		return []automdm.AutoMDMMaintainedVersion{{ID: 9, Version: "1.0"}}, nil
	}

	err := AutoUpdateAutoMDMMaintainedApps(ctx, ds, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ds.SetAutoMDMMaintainedAppActiveInstallerFuncInvoked)
}
