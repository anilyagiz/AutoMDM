package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	nanodep_client "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/godep"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mdmmock "github.com/anilyagiz/automdm/v4/server/mock/mdm"
	servicemock "github.com/anilyagiz/automdm/v4/server/mock/service"
	"github.com/anilyagiz/automdm/v4/server/test"
)

func TestNewAppleMDMProfileManagerWithoutConfig(t *testing.T) {
	ctx := context.Background()
	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	cmdr := apple_mdm.NewMDMAppleCommander(mdmStorage, nil)
	logger := slog.New(slog.DiscardHandler)

	sch, err := newAppleMDMProfileManagerSchedule(ctx, "foo", ds, cmdr, kv, logger, 0, false)
	require.NotNil(t, sch)
	require.NoError(t, err)
}

func TestNewWindowsMDMProfileManagerWithoutConfig(t *testing.T) {
	ctx := context.Background()
	ds := new(mock.Store)
	logger := slog.New(slog.DiscardHandler)

	sch, err := newWindowsMDMProfileManagerSchedule(ctx, "foo", ds, logger, false)
	require.NotNil(t, sch)
	require.NoError(t, err)
}

func TestMigrateABMTokenDuringDEPCronJob(t *testing.T) {
	ctx := context.Background()
	ds := mysqltest.CreateMySQLDS(t)

	depStorage, err := ds.NewMDMAppleDEPStorage()
	require.NoError(t, err)
	// to avoid issues with syncer, use that constant as org name for now
	const tokenOrgName = "automdm"

	// insert an ABM token as if it had been migrated by the DB migration script
	tok := mysqltest.SetTestABMAssets(t, ds, "")
	// tok, err := ds.InsertABMToken(ctx, &automdm.ABMToken{EncryptedToken: abmToken, RenewAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)})
	// require.NoError(t, err)
	require.Empty(t, tok.OrganizationName)

	// start a server that will mock the Apple DEP API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		encoder := json.NewEncoder(w)
		switch r.URL.Path {
		case "/session":
			_, _ = w.Write([]byte(`{"auth_session_token": "session123"}`))
		case "/account":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"admin_id": "admin123", "org_name": "%s"}`, tokenOrgName)))
		case "/profile":
			err := encoder.Encode(godep.ProfileResponse{ProfileUUID: "profile123"})
			require.NoError(t, err)
		case "/server/devices":
			err := encoder.Encode(godep.DeviceResponse{Devices: nil})
			require.NoError(t, err)
		case "/devices/sync":
			err := encoder.Encode(godep.DeviceResponse{Devices: nil})
			require.NoError(t, err)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	err = depStorage.StoreConfig(ctx, tokenOrgName, &nanodep_client.Config{BaseURL: srv.URL})
	require.NoError(t, err)
	err = depStorage.StoreConfig(ctx, apple_mdm.UnsavedABMTokenOrgName, &nanodep_client.Config{BaseURL: srv.URL})
	require.NoError(t, err)

	logger := slog.New(slog.DiscardHandler)
	syncFn := appleMDMDEPSyncerJob(ds, depStorage, logger)
	err = syncFn(ctx)
	require.NoError(t, err)

	// token has been updated with its org name/apple id
	tok, err = ds.GetABMTokenByOrgName(ctx, tokenOrgName)
	require.NoError(t, err)
	require.Equal(t, tokenOrgName, tok.OrganizationName)
	require.Equal(t, "admin123", tok.AppleID)
	require.Nil(t, tok.MacOSDefaultTeamID)
	require.Nil(t, tok.IOSDefaultTeamID)
	require.Nil(t, tok.IPadOSDefaultTeamID)

	// empty-name token does not exist anymore
	_, err = ds.GetABMTokenByOrgName(ctx, "")
	require.Error(t, err)
	var nfe automdm.NotFoundError
	require.ErrorAs(t, err, &nfe)

	// the default profile was created
	defProf, err := ds.GetMDMAppleEnrollmentProfileByType(ctx, automdm.MDMAppleEnrollmentTypeAutomatic)
	require.NoError(t, err)
	require.NotNil(t, defProf)
	require.NotEmpty(t, defProf.Token)

	// no profile UUID was assigned for no-team (because there are no hosts right now)
	_, _, err = ds.GetMDMAppleDefaultSetupAssistant(ctx, nil, "")
	require.Error(t, err)
	require.ErrorAs(t, err, &nfe)

	// no teams, so no team-specific custom setup assistants
	teams, err := ds.ListTeams(ctx, automdm.TeamFilter{User: test.UserAdmin}, automdm.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, teams)

	// no no-team custom setup assistant
	_, err = ds.GetMDMAppleSetupAssistant(ctx, nil)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// no host got created
	hosts, err := ds.ListHosts(ctx, automdm.TeamFilter{User: test.UserAdmin}, automdm.HostListOptions{})
	require.NoError(t, err)
	require.Empty(t, hosts)
}

func TestCleanupUnusedS3FilesCronJobsSetDeadline(t *testing.T) {
	ds := new(mock.Store)

	const budget = time.Minute
	var deadline time.Time
	var hasDeadline bool
	ds.CleanupUnusedSoftwareInstallersFunc = func(ctx context.Context, softwareInstallStore automdm.SoftwareInstallerStore, removeCreatedBefore time.Time) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	}
	var iconDeadline time.Time
	var iconHasDeadline bool
	ds.CleanupUnusedSoftwareTitleIconsFunc = func(ctx context.Context, softwareTitleIconStore automdm.SoftwareTitleIconStore, removeCreatedBefore time.Time) error {
		iconDeadline, iconHasDeadline = ctx.Deadline()
		return nil
	}
	var bootstrapDeadline time.Time
	var bootstrapHasDeadline bool
	ds.CleanupUnusedBootstrapPackagesFunc = func(ctx context.Context, pkgStore automdm.MDMBootstrapPackageStore, removeCreatedBefore time.Time) error {
		bootstrapDeadline, bootstrapHasDeadline = ctx.Deadline()
		return nil
	}

	// The schedule hands each job a context with no deadline, so the budget has to come from the job.
	err := cleanupUnusedSoftwareInstallersCronJob(context.Background(), ds, nil, budget)
	require.NoError(t, err)
	require.True(t, ds.CleanupUnusedSoftwareInstallersFuncInvoked)
	require.True(t, hasDeadline, "the S3 calls must inherit the job time budget")
	require.Positive(t, time.Until(deadline))
	require.LessOrEqual(t, time.Until(deadline), budget)

	// Run the title icon cleanup with a context that has no deadline, the S3 calls should get the job time budget.
	err = cleanupUnusedSoftwareTitleIconsCronJob(context.Background(), ds, nil, budget)
	require.NoError(t, err)
	require.True(t, ds.CleanupUnusedSoftwareTitleIconsFuncInvoked)
	require.True(t, iconHasDeadline, "the S3 calls must inherit the job time budget")
	require.Positive(t, time.Until(iconDeadline))
	require.LessOrEqual(t, time.Until(iconDeadline), budget)

	// Run the bootstrap package cleanup with a context that has no deadline, the S3 calls should get the job time budget.
	err = cleanupUnusedBootstrapPackagesCronJob(context.Background(), ds, nil, budget)
	require.NoError(t, err)
	require.True(t, ds.CleanupUnusedBootstrapPackagesFuncInvoked)
	require.True(t, bootstrapHasDeadline, "the S3 calls must inherit the job time budget")
	require.Positive(t, time.Until(bootstrapDeadline))
	require.LessOrEqual(t, time.Until(bootstrapDeadline), budget)
}

func TestCleanupStaleOSVVulnerabilities(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	ds := mysqltest.CreateMySQLDS(t)

	// Create test software
	host := test.NewHost(t, ds, "host1", "", "host1key", "host1uuid", time.Now())
	software := []automdm.Software{
		{Name: "pkg1", Version: "1.0", Source: "apps"},
	}
	_, err := ds.UpdateHostSoftware(ctx, host.ID, software)
	require.NoError(t, err)
	require.NoError(t, ds.LoadHostSoftware(ctx, host, false))
	require.Len(t, host.Software, 1)

	// Insert vulnerabilities from both sources
	_, err = ds.InsertSoftwareVulnerability(ctx, automdm.SoftwareVulnerability{
		SoftwareID: host.Software[0].ID,
		CVE:        "CVE-2024-0001",
	}, automdm.UbuntuOSVSource)
	require.NoError(t, err)

	_, err = ds.InsertSoftwareVulnerability(ctx, automdm.SoftwareVulnerability{
		SoftwareID: host.Software[0].ID,
		CVE:        "CVE-2024-0002",
	}, automdm.UbuntuOVALSource)
	require.NoError(t, err)

	t.Run("OSV enabled - does not delete OSV vulnerabilities", func(t *testing.T) {
		cleanupStaleOSVVulnerabilities(ctx, ds, logger, true)

		// Both vulnerabilities should still exist
		osvVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOSVSource)
		require.NoError(t, err)
		require.Len(t, osvVulns[host.ID], 1)

		ovalVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOVALSource)
		require.NoError(t, err)
		require.Len(t, ovalVulns[host.ID], 1)
	})

	t.Run("OSV disabled - deletes only UbuntuOSVSource vulnerabilities", func(t *testing.T) {
		cleanupStaleOSVVulnerabilities(ctx, ds, logger, false)

		// OSV vulnerabilities should be deleted
		osvVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOSVSource)
		require.NoError(t, err)
		require.Empty(t, osvVulns[host.ID])

		// OVAL vulnerability should remain
		ovalVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOVALSource)
		require.NoError(t, err)
		require.Len(t, ovalVulns[host.ID], 1)
		require.Equal(t, "CVE-2024-0002", ovalVulns[host.ID][0].CVE)
	})
}

func TestCleanupStaleOVALVulnerabilities(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	ds := mysqltest.CreateMySQLDS(t)

	// Create test software
	host := test.NewHost(t, ds, "host2", "", "host2key", "host2uuid", time.Now())
	software := []automdm.Software{
		{Name: "pkg2", Version: "2.0", Source: "apps"},
	}
	_, err := ds.UpdateHostSoftware(ctx, host.ID, software)
	require.NoError(t, err)
	require.NoError(t, ds.LoadHostSoftware(ctx, host, false))
	require.Len(t, host.Software, 1)

	// Insert vulnerabilities from multiple sources
	_, err = ds.InsertSoftwareVulnerability(ctx, automdm.SoftwareVulnerability{
		SoftwareID: host.Software[0].ID,
		CVE:        "CVE-2024-0003",
	}, automdm.UbuntuOSVSource)
	require.NoError(t, err)

	_, err = ds.InsertSoftwareVulnerability(ctx, automdm.SoftwareVulnerability{
		SoftwareID: host.Software[0].ID,
		CVE:        "CVE-2024-0004",
	}, automdm.UbuntuOVALSource)
	require.NoError(t, err)

	_, err = ds.InsertSoftwareVulnerability(ctx, automdm.SoftwareVulnerability{
		SoftwareID: host.Software[0].ID,
		CVE:        "CVE-2024-0005",
	}, automdm.RHELOVALSource)
	require.NoError(t, err)

	t.Run("deletes only UbuntuOVALSource vulnerabilities, preserves others", func(t *testing.T) {
		cleanupStaleOVALVulnerabilities(ctx, ds, logger)

		// Ubuntu OVAL vulnerabilities should be deleted
		ovalVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOVALSource)
		require.NoError(t, err)
		require.Empty(t, ovalVulns[host.ID])

		// OSV vulnerabilities should remain
		osvVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.UbuntuOSVSource)
		require.NoError(t, err)
		require.Len(t, osvVulns[host.ID], 1)
		require.Equal(t, "CVE-2024-0003", osvVulns[host.ID][0].CVE)

		// RHEL OVAL vulnerabilities should remain
		rhelVulns, err := ds.ListSoftwareVulnerabilitiesByHostIDsSource(ctx, []uint{host.ID}, automdm.RHELOVALSource)
		require.NoError(t, err)
		require.Len(t, rhelVulns[host.ID], 1)
		require.Equal(t, "CVE-2024-0005", rhelVulns[host.ID][0].CVE)
	})
}

func TestCleanupStaleWindowsMDMEnrollmentsCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("non-positive retention disables the job", func(t *testing.T) {
		for _, retention := range []time.Duration{0, -time.Hour} {
			ds := new(mock.Store)
			require.NoError(t, cleanupStaleWindowsMDMEnrollmentsCronJob(t.Context(), ds, logger, retention))
			require.False(t, ds.CleanupStaleMDMWindowsEnrollmentsFuncInvoked)
		}
	})

	t.Run("passes the cutoff derived from the retention", func(t *testing.T) {
		ds := new(mock.Store)
		var cutoff time.Time
		ds.CleanupStaleMDMWindowsEnrollmentsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			cutoff = olderThan
			return 3, nil
		}
		before := time.Now()
		require.NoError(t, cleanupStaleWindowsMDMEnrollmentsCronJob(t.Context(), ds, logger, 30*24*time.Hour))
		require.True(t, ds.CleanupStaleMDMWindowsEnrollmentsFuncInvoked)
		require.WithinDuration(t, before.Add(-30*24*time.Hour), cutoff, time.Minute)
	})

	t.Run("propagates datastore errors", func(t *testing.T) {
		ds := new(mock.Store)
		ds.CleanupStaleMDMWindowsEnrollmentsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			return 2, errors.New("boom")
		}
		err := cleanupStaleWindowsMDMEnrollmentsCronJob(t.Context(), ds, logger, time.Hour)
		require.ErrorContains(t, err, "boom")
	})
}

func TestCleanupWindowsMDMCommandHistoryCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("non-positive retention disables the job", func(t *testing.T) {
		for _, retention := range []time.Duration{0, -time.Hour} {
			ds := new(mock.Store)
			require.NoError(t, cleanupWindowsMDMCommandHistoryCronJob(t.Context(), ds, logger, retention))
			require.False(t, ds.CleanupMDMWindowsCommandHistoryFuncInvoked)
		}
	})

	t.Run("passes the cutoff derived from the retention", func(t *testing.T) {
		ds := new(mock.Store)
		var cutoff time.Time
		ds.CleanupMDMWindowsCommandHistoryFunc = func(ctx context.Context, olderThan time.Time) (automdm.MDMWindowsCommandHistoryCleanupCounts, error) {
			cutoff = olderThan
			return automdm.MDMWindowsCommandHistoryCleanupCounts{Responses: 1, Results: 2, Commands: 3}, nil
		}
		before := time.Now()
		require.NoError(t, cleanupWindowsMDMCommandHistoryCronJob(t.Context(), ds, logger, 30*24*time.Hour))
		require.True(t, ds.CleanupMDMWindowsCommandHistoryFuncInvoked)
		require.WithinDuration(t, before.Add(-30*24*time.Hour), cutoff, time.Minute)
	})

	t.Run("propagates datastore errors", func(t *testing.T) {
		ds := new(mock.Store)
		ds.CleanupMDMWindowsCommandHistoryFunc = func(ctx context.Context, olderThan time.Time) (automdm.MDMWindowsCommandHistoryCleanupCounts, error) {
			return automdm.MDMWindowsCommandHistoryCleanupCounts{Responses: 1}, errors.New("boom")
		}
		err := cleanupWindowsMDMCommandHistoryCronJob(t.Context(), ds, logger, time.Hour)
		require.ErrorContains(t, err, "boom")
	})
}

func TestCleanupHostSoftwareInstallsCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("non-positive retention disables the job", func(t *testing.T) {
		for _, retention := range []time.Duration{0, -time.Hour} {
			ds := new(mock.Store)
			require.NoError(t, cleanupHostSoftwareInstallsCronJob(t.Context(), ds, logger, retention))
			require.False(t, ds.CleanupHostSoftwareInstallsFuncInvoked)
		}
	})

	t.Run("passes the cutoff derived from the retention", func(t *testing.T) {
		ds := new(mock.Store)
		var cutoff time.Time
		ds.CleanupHostSoftwareInstallsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			cutoff = olderThan
			return 7, nil
		}
		before := time.Now()
		require.NoError(t, cleanupHostSoftwareInstallsCronJob(t.Context(), ds, logger, 30*24*time.Hour))
		require.True(t, ds.CleanupHostSoftwareInstallsFuncInvoked)
		require.WithinDuration(t, before.Add(-30*24*time.Hour), cutoff, time.Minute)
	})

	t.Run("propagates datastore errors", func(t *testing.T) {
		ds := new(mock.Store)
		ds.CleanupHostSoftwareInstallsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			return 1, errors.New("boom")
		}
		err := cleanupHostSoftwareInstallsCronJob(t.Context(), ds, logger, time.Hour)
		require.ErrorContains(t, err, "boom")
	})
}

func TestCleanupHostScriptResultsCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("non-positive retention disables the job", func(t *testing.T) {
		for _, retention := range []time.Duration{0, -time.Hour} {
			ds := new(mock.Store)
			require.NoError(t, cleanupHostScriptResultsCronJob(t.Context(), ds, logger, retention))
			require.False(t, ds.CleanupHostScriptResultsFuncInvoked)
		}
	})

	t.Run("passes the cutoff derived from the retention", func(t *testing.T) {
		ds := new(mock.Store)
		var cutoff time.Time
		ds.CleanupHostScriptResultsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			cutoff = olderThan
			return 3, nil
		}
		before := time.Now()
		require.NoError(t, cleanupHostScriptResultsCronJob(t.Context(), ds, logger, 30*24*time.Hour))
		require.True(t, ds.CleanupHostScriptResultsFuncInvoked)
		require.WithinDuration(t, before.Add(-30*24*time.Hour), cutoff, time.Minute)
	})

	t.Run("propagates datastore errors", func(t *testing.T) {
		ds := new(mock.Store)
		ds.CleanupHostScriptResultsFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
			return 1, errors.New("boom")
		}
		err := cleanupHostScriptResultsCronJob(t.Context(), ds, logger, time.Hour)
		require.ErrorContains(t, err, "boom")
	})
}

func TestUnblockHostsUpcomingActivityQueueCronJob(t *testing.T) {
	const maxHosts = 500

	unblockReturning := func(t *testing.T, unblocked int, retErr error) (*mock.Store, *bytes.Buffer, error) {
		t.Helper()
		var logs bytes.Buffer
		ds := new(mock.Store)
		var gotMaxHosts int
		var gotSkipAutoMDMInitiated bool
		ds.UnblockHostsUpcomingActivityQueueFunc = func(ctx context.Context, m int, skipAutoMDMInitiated bool) (int, error) {
			gotMaxHosts, gotSkipAutoMDMInitiated = m, skipAutoMDMInitiated
			return unblocked, retErr
		}
		err := unblockHostsUpcomingActivityQueueCronJob(
			t.Context(), ds, slog.New(slog.NewTextHandler(&logs, nil)), maxHosts, true,
		)
		require.True(t, ds.UnblockHostsUpcomingActivityQueueFuncInvoked)
		require.Equal(t, maxHosts, gotMaxHosts)
		require.True(t, gotSkipAutoMDMInitiated)
		return ds, &logs, err
	}

	t.Run("logs the count it unblocked", func(t *testing.T) {
		_, logs, err := unblockReturning(t, 7, nil)
		require.NoError(t, err)
		require.Contains(t, logs.String(), "hosts_unblocked=7")
		require.Contains(t, logs.String(), "max_hosts=500")
		require.Contains(t, logs.String(), "cap_reached=false")
	})

	t.Run("flags a run that hit its cap, since more hosts are still blocked", func(t *testing.T) {
		_, logs, err := unblockReturning(t, maxHosts, nil)
		require.NoError(t, err)
		require.Contains(t, logs.String(), "cap_reached=true")
	})

	t.Run("stays quiet when nothing was blocked", func(t *testing.T) {
		_, logs, err := unblockReturning(t, 0, nil)
		require.NoError(t, err)
		require.Empty(t, logs.String())
	})

	t.Run("reports partial progress before returning the error", func(t *testing.T) {
		_, logs, err := unblockReturning(t, 3, errors.New("boom"))
		require.ErrorContains(t, err, "boom")
		require.Contains(t, logs.String(), "hosts_unblocked=3")
	})

	t.Run("propagates datastore errors", func(t *testing.T) {
		_, logs, err := unblockReturning(t, 0, errors.New("boom"))
		require.ErrorContains(t, err, "boom")
		require.Empty(t, logs.String())
	})
}

func TestCleanupHostCertificatesCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	ds := new(mock.Store)
	var cutoff time.Time
	ds.CleanupSoftDeletedHostCertificatesFunc = func(ctx context.Context, olderThan time.Time) (int64, error) {
		cutoff = olderThan
		return 0, nil
	}

	require.NoError(t, cleanupHostCertificatesCronJob(t.Context(), ds, logger, 0))
	require.False(t, ds.CleanupSoftDeletedHostCertificatesFuncInvoked, "zero retention disables the cleanup")

	require.NoError(t, cleanupHostCertificatesCronJob(t.Context(), ds, logger, 30*24*time.Hour))
	require.WithinDuration(t, time.Now().Add(-30*24*time.Hour), cutoff, time.Minute)
}

func TestCleanupExpiredHostsCronJob(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("drains until empty", func(t *testing.T) {
		svc := &servicemock.Service{}
		calls := 0
		svc.CleanupExpiredHostsBatchFunc = func(ctx context.Context, batchSize int) ([]automdm.DeletedHostDetails, error) {
			require.Equal(t, 5, batchSize)
			calls++
			if calls == 1 {
				return []automdm.DeletedHostDetails{{ID: 1}}, nil
			}
			return nil, nil
		}

		err := cleanupExpiredHostsCronJob(context.Background(), svc, logger, time.Minute, 5)
		require.NoError(t, err)
		require.Equal(t, 2, calls)
	})

	t.Run("stops between batches when runtime budget expires", func(t *testing.T) {
		svc := &servicemock.Service{}
		calls := 0
		svc.CleanupExpiredHostsBatchFunc = func(ctx context.Context, batchSize int) ([]automdm.DeletedHostDetails, error) {
			require.Equal(t, 5, batchSize)
			calls++
			// The batch must not be cancelled by the runtime budget: a batch
			// that outlives the budget still completes, and the loop stops
			// before starting the next one.
			_, hasDeadline := ctx.Deadline()
			require.False(t, hasDeadline)
			time.Sleep(60 * time.Millisecond)
			require.NoError(t, ctx.Err())
			return []automdm.DeletedHostDetails{{ID: 1}}, nil
		}

		err := cleanupExpiredHostsCronJob(context.Background(), svc, logger, 50*time.Millisecond, 5)
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("returns error when parent context is cancelled", func(t *testing.T) {
		svc := &servicemock.Service{}
		ctx, cancel := context.WithCancel(context.Background())
		svc.CleanupExpiredHostsBatchFunc = func(ctx context.Context, batchSize int) ([]automdm.DeletedHostDetails, error) {
			require.Equal(t, 5, batchSize)
			cancel()
			return []automdm.DeletedHostDetails{{ID: 1}}, nil
		}

		err := cleanupExpiredHostsCronJob(ctx, svc, logger, time.Minute, 5)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestBuildChartScopeResolver(t *testing.T) {
	historicalData := func(uptime, vulns bool) automdm.HistoricalDataSettings {
		return automdm.HistoricalDataSettings{Uptime: uptime, Vulnerabilities: vulns}
	}
	makeAppCfg := func(uptime, vulns bool) *automdm.AppConfig {
		return &automdm.AppConfig{
			Features: automdm.Features{HistoricalData: historicalData(uptime, vulns)},
		}
	}
	makeTeam := func(id uint, uptime, vulns bool) *automdm.Team {
		return &automdm.Team{
			ID: id,
			Config: automdm.TeamConfig{
				Features: automdm.Features{HistoricalData: historicalData(uptime, vulns)},
			},
		}
	}

	t.Run("global off → skip", func(t *testing.T) {
		scope := buildChartScopeResolver(makeAppCfg(false, true), nil, true, nil)
		skip, disabled := scope("uptime")
		require.True(t, skip)
		require.Nil(t, disabled)
	})

	t.Run("global on, no teams → no scoping", func(t *testing.T) {
		scope := buildChartScopeResolver(makeAppCfg(true, true), nil, true, nil)
		skip, disabled := scope("uptime")
		require.False(t, skip)
		require.Nil(t, disabled)
	})

	t.Run("global on, all teams on → empty disabled list", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, true),
			[]*automdm.Team{makeTeam(1, true, true), makeTeam(2, true, true)},
			true,
			nil,
		)
		skip, disabled := scope("uptime")
		require.False(t, skip)
		require.Empty(t, disabled)
	})

	t.Run("global on, mixed teams → disabled team IDs only", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, true),
			[]*automdm.Team{
				makeTeam(1, true, true),
				makeTeam(2, false, true), // uptime disabled on team 2
				makeTeam(3, true, true),
				makeTeam(4, false, true), // uptime disabled on team 4
			},
			true,
			nil,
		)
		skip, disabled := scope("uptime")
		require.False(t, skip)
		require.ElementsMatch(t, []uint{2, 4}, disabled)
	})

	t.Run("per-dataset isolation: uptime and cve resolve independently", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, true),
			[]*automdm.Team{
				makeTeam(1, true, false), // cve only on team 1
				makeTeam(2, false, true), // uptime only on team 2
				makeTeam(3, true, true),
			},
			true,
			nil,
		)
		skipU, disabledU := scope("uptime")
		require.False(t, skipU)
		require.ElementsMatch(t, []uint{2}, disabledU)

		skipC, disabledC := scope("cve")
		require.False(t, skipC)
		require.ElementsMatch(t, []uint{1}, disabledC)
	})

	t.Run("global cve off, teams ignored", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, false),
			[]*automdm.Team{makeTeam(1, true, true)},
			true,
			nil,
		)
		skip, disabled := scope("cve")
		require.True(t, skip)
		require.Nil(t, disabled)
	})

	t.Run("unknown dataset name falls through to no-scope", func(t *testing.T) {
		scope := buildChartScopeResolver(makeAppCfg(true, true), nil, true, nil)
		skip, disabled := scope("unknown_dataset")
		require.False(t, skip)
		require.Nil(t, disabled)
	})

	t.Run("free tier skips cve even when the config enables it", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, true),
			[]*automdm.Team{makeTeam(1, true, true)},
			false,
			nil,
		)
		skip, disabled := scope("cve")
		require.True(t, skip)
		require.Nil(t, disabled)
	})

	t.Run("free tier still collects uptime", func(t *testing.T) {
		scope := buildChartScopeResolver(
			makeAppCfg(true, true),
			[]*automdm.Team{makeTeam(1, true, true), makeTeam(2, false, true)},
			false,
			nil,
		)
		skip, disabled := scope("uptime")
		require.False(t, skip)
		require.ElementsMatch(t, []uint{2}, disabled, "per-team uptime scoping is unaffected by the license")
	})

	t.Run("premium with cve enabled collects it", func(t *testing.T) {
		scope := buildChartScopeResolver(makeAppCfg(true, true), nil, true, nil)
		skip, disabled := scope("cve")
		require.False(t, skip)
		require.Nil(t, disabled)
	})
}

func TestHostVitalsLabelMembershipCronIDP(t *testing.T) {
	ctx := context.Background()
	ds := mysqltest.CreateMySQLDS(t)

	team1, err := ds.NewTeam(ctx, &automdm.Team{Name: "idp-cron-team1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(ctx, &automdm.Team{Name: "idp-cron-team2"})
	require.NoError(t, err)

	// host0 -> team1, host1 -> team2, host2 -> global (no team).
	hosts := make([]*automdm.Host, 3)
	teamIDs := []*uint{&team1.ID, &team2.ID, nil}
	for i := range 3 {
		h, err := ds.NewHost(ctx, &automdm.Host{
			OsqueryHostID:  new(fmt.Sprintf("idp-cron-%d", i)),
			NodeKey:        new(fmt.Sprintf("idp-cron-%d", i)),
			UUID:           fmt.Sprintf("idp-cron-uuid%d", i),
			Hostname:       fmt.Sprintf("idp-cron-host%d.local", i),
			HardwareSerial: fmt.Sprintf("idp-cron-hwd%d", i),
			Platform:       "darwin",
			TeamID:         teamIDs[i],
		})
		require.NoError(t, err)
		hosts[i] = h
	}

	// All three SCIM users are in the same "Engineering" IdP group.
	scimUserIDs := make([]uint, 3)
	for i := range 3 {
		id, err := ds.CreateScimUser(ctx, &automdm.ScimUser{
			UserName: fmt.Sprintf("idp-cron-user%d", i),
			Active:   new(true),
		})
		require.NoError(t, err)
		scimUserIDs[i] = id
		hostID, scimUserID := hosts[i].ID, id
		mysqltest.ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
			_, err := q.ExecContext(ctx,
				"INSERT INTO host_scim_user (host_id, scim_user_id) VALUES (?, ?)",
				hostID, scimUserID)
			return err
		})
	}
	_, err = ds.CreateScimGroup(ctx, &automdm.ScimGroup{DisplayName: "Engineering", ScimUsers: scimUserIDs})
	require.NoError(t, err)

	criteria, err := json.Marshal(&automdm.HostVitalCriteria{
		Vital: new("end_user_idp_group"),
		Value: new("Engineering"),
	})
	require.NoError(t, err)
	raw := json.RawMessage(criteria)

	// Create a global and a team1-scoped IdP host vitals label.
	globalLabel, err := ds.NewLabel(ctx, &automdm.Label{
		Name:                "idp-cron-global",
		LabelType:           automdm.LabelTypeRegular,
		LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
		HostVitalsCriteria:  &raw,
	})
	require.NoError(t, err)
	team1Label, err := ds.NewLabel(ctx, &automdm.Label{
		Name:                "idp-cron-team1",
		TeamID:              &team1.ID,
		LabelType:           automdm.LabelTypeRegular,
		LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
		HostVitalsCriteria:  &raw,
	})
	require.NoError(t, err)

	// Run the actual cron.
	require.NoError(t, cronHostVitalsLabelMembership(ctx, ds))

	filter := automdm.TeamFilter{User: test.UserAdmin}

	globalHosts, err := ds.ListHostsInLabel(ctx, filter, globalLabel.ID, automdm.HostListOptions{})
	require.NoError(t, err)
	gotGlobal := make([]uint, 0, len(globalHosts))
	for _, h := range globalHosts {
		gotGlobal = append(gotGlobal, h.ID)
	}
	require.ElementsMatch(t, []uint{hosts[0].ID, hosts[1].ID, hosts[2].ID}, gotGlobal)

	team1Hosts, err := ds.ListHostsInLabel(ctx, filter, team1Label.ID, automdm.HostListOptions{})
	require.NoError(t, err)
	gotTeam1 := make([]uint, 0, len(team1Hosts))
	for _, h := range team1Hosts {
		gotTeam1 = append(gotTeam1, h.ID)
	}
	require.ElementsMatch(t, []uint{hosts[0].ID}, gotTeam1)
}

func TestAPNsJobsCapTheirRunTime(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	// both jobs read the app config first, so a deadline observed there covers
	// the whole run; MDM disabled stops each job right after
	cases := []struct {
		name string
		run  func(ctx context.Context, ds automdm.Datastore) error
	}{
		{"apns_push_to_pending_hosts", func(ctx context.Context, ds automdm.Datastore) error {
			return apnsPusherJob(ctx, ds, nil, logger)
		}},
		{"apns_sweep", func(ctx context.Context, ds automdm.Datastore) error {
			return apnsSweepJob(ctx, ds, nil, logger, time.Minute)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			var deadline time.Time
			var hasDeadline bool
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				deadline, hasDeadline = ctx.Deadline()
				return &automdm.AppConfig{}, nil
			}

			before := time.Now()
			require.NoError(t, tc.run(t.Context(), ds))

			require.True(t, ds.AppConfigFuncInvoked)
			require.True(t, hasDeadline, "job context must carry a deadline")
			require.WithinDuration(t, before.Add(apnsMaxRunTime), deadline, time.Minute)
		})
	}
}
