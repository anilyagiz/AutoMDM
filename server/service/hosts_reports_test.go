package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListHostReports(t *testing.T) {
	ds := new(mock.Store)

	now := time.Now().UTC().Truncate(time.Second)

	// global admin user
	admin := &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}

	// Host with no team
	hostNoTeam := &automdm.Host{ID: 10, TeamID: nil}
	// Host with team
	teamID := uint(42)
	hostWithTeam := &automdm.Host{ID: 20, TeamID: &teamID}

	sampleReports := []*automdm.HostReport{
		{
			ReportID:     1,
			Name:         "Query Alpha",
			Description:  "desc alpha",
			LastFetched:  &now,
			FirstResult:  map[string]string{"col1": "val1"},
			NHostResults: 3,
		},
		{
			ReportID:     2,
			Name:         "Query Beta",
			Description:  "desc beta",
			LastFetched:  nil,
			FirstResult:  nil,
			NHostResults: 0,
		},
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		if id == hostNoTeam.ID {
			return hostNoTeam, nil
		}
		if id == hostWithTeam.ID {
			return hostWithTeam, nil
		}
		return nil, errors.New("host not found")
	}

	var capturedTeamID *uint
	ds.ListHostReportsFunc = func(ctx context.Context, hostID uint, tID *uint, hostPlatform string, opts automdm.ListHostReportsOptions) ([]*automdm.HostReport, int, *automdm.PaginationMetadata, error) {
		capturedTeamID = tID
		return sampleReports, len(sampleReports), nil, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)

	t.Run("admin can list reports for host with no team", func(t *testing.T) {
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: admin})
		opts := automdm.ListHostReportsOptions{}
		reports, count, _, err := svc.ListHostReports(viewerCtx, hostNoTeam.ID, opts)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
		assert.Len(t, reports, 2)
		assert.Equal(t, "Query Alpha", reports[0].Name)
		assert.Equal(t, "Query Beta", reports[1].Name)
		assert.Equal(t, &now, reports[0].LastFetched)
		assert.Nil(t, reports[1].LastFetched)
		assert.Equal(t, 3, reports[0].NHostResults)
		assert.Equal(t, 0, reports[1].NHostResults)
		assert.Equal(t, map[string]string{"col1": "val1"}, reports[0].FirstResult)
		assert.Nil(t, reports[1].FirstResult)
		// host has no team, so nil teamID must be forwarded to the datastore.
		assert.Nil(t, capturedTeamID)
	})

	t.Run("admin can list reports for host with team", func(t *testing.T) {
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: admin})
		opts := automdm.ListHostReportsOptions{}
		reports, count, _, err := svc.ListHostReports(viewerCtx, hostWithTeam.ID, opts)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
		assert.Len(t, reports, 2)
		// teamID must be forwarded to the datastore so it can scope queries correctly.
		assert.Equal(t, &teamID, capturedTeamID)
	})

	t.Run("observer can list reports", func(t *testing.T) {
		observer := &automdm.User{
			ID:         2,
			GlobalRole: ptr.String(automdm.RoleObserver),
		}
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: observer})
		opts := automdm.ListHostReportsOptions{}
		reports, count, _, err := svc.ListHostReports(viewerCtx, hostNoTeam.ID, opts)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
		assert.Len(t, reports, 2)
	})

	t.Run("invalid order_key returns bad request", func(t *testing.T) {
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: admin})
		_, _, _, err := svc.ListHostReports(viewerCtx, hostNoTeam.ID, automdm.ListHostReportsOptions{
			ListOptions: automdm.ListOptions{OrderKey: "invalid_key"},
		})
		require.Error(t, err)
		var invalidArgErr *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &invalidArgErr)
	})

	t.Run("unauthenticated gets error", func(t *testing.T) {
		_, _, _, err := svc.ListHostReports(ctx, hostNoTeam.ID, automdm.ListHostReportsOptions{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "forbidden")
	})

	t.Run("team observer cannot read host belonging to a different team", func(t *testing.T) {
		otherTeamID := uint(99)
		teamObserver := &automdm.User{
			ID: 3,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: otherTeamID}, Role: automdm.RoleObserver},
			},
		}
		// hostWithTeam belongs to teamID=42; teamObserver only has access to teamID=99.
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: teamObserver})
		_, _, _, err := svc.ListHostReports(viewerCtx, hostWithTeam.ID, automdm.ListHostReportsOptions{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "forbidden")
	})
}

// TestListHostReportsDatastorePassthrough verifies the options are forwarded
// to the datastore correctly.
func TestListHostReportsDatastorePassthrough(t *testing.T) {
	ds := new(mock.Store)

	teamID := uint(5)
	// Platform is "ubuntu" so PlatformFromHost maps it to "linux" — a non-trivial
	// conversion that would be missed if the service passed host.Platform raw.
	host := &automdm.Host{ID: 7, TeamID: &teamID, Platform: "ubuntu"}

	admin := &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return host, nil
	}

	capturedHostID := uint(0)
	capturedTeamID := (*uint)(nil)
	capturedPlatform := ""
	capturedOpts := automdm.ListHostReportsOptions{}

	ds.ListHostReportsFunc = func(ctx context.Context, hostID uint, tID *uint, hostPlatform string, opts automdm.ListHostReportsOptions) ([]*automdm.HostReport, int, *automdm.PaginationMetadata, error) {
		capturedHostID = hostID
		capturedTeamID = tID
		capturedPlatform = hostPlatform
		capturedOpts = opts
		return nil, 0, nil, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil)
	viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: admin})

	opts := automdm.ListHostReportsOptions{
		IncludeReportsDontStoreResults: true,
		ListOptions: automdm.ListOptions{
			Page:           1,
			PerPage:        10,
			OrderKey:       "name",
			OrderDirection: automdm.OrderAscending,
			MatchQuery:     "Alpha",
		},
	}

	_, _, _, err := svc.ListHostReports(viewerCtx, host.ID, opts)
	require.NoError(t, err)

	assert.Equal(t, host.ID, capturedHostID)
	assert.Equal(t, &teamID, capturedTeamID)
	assert.Equal(t, automdm.PlatformFromHost(host.Platform), capturedPlatform)
	assert.True(t, capturedOpts.IncludeReportsDontStoreResults)
	assert.Equal(t, uint(1), capturedOpts.ListOptions.Page)
	assert.Equal(t, uint(10), capturedOpts.ListOptions.PerPage)
	assert.Equal(t, "name", capturedOpts.ListOptions.OrderKey)
	assert.Equal(t, "Alpha", capturedOpts.ListOptions.MatchQuery)
}

// TestHostReportJSONRoundTrip verifies that HostReport serializes and
// deserializes correctly, including the FirstResult and LastFetched fields.
func TestHostReportJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// This test exercises the HostReport struct's FirstResult field to ensure
	// the data mapping from query_results.data JSON is correct.
	report := &automdm.HostReport{
		ReportID:     1,
		Name:         "USB Devices",
		Description:  "List USB devices",
		LastFetched:  &now,
		FirstResult:  map[string]string{"model": "USB Keyboard", "vendor": "Apple Inc."},
		NHostResults: 0,
	}

	// Verify JSON serialization round-trip
	b, err := json.Marshal(report)
	require.NoError(t, err)

	var decoded automdm.HostReport
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)

	assert.Equal(t, report.ReportID, decoded.ReportID)
	assert.Equal(t, report.Name, decoded.Name)
	assert.Equal(t, report.Description, decoded.Description)
	assert.Equal(t, report.FirstResult, decoded.FirstResult)
	assert.Equal(t, report.NHostResults, decoded.NHostResults)
	assert.NotNil(t, decoded.LastFetched)
}
