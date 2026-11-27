package automdmctl

import (
	"context"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostTransferFlagChecks(t *testing.T) {
	testing_utils.RunServerWithMockedDS(t)

	runAppCheckErr(t,
		[]string{"hosts", "transfer", "--team", "team1", "--hosts", "host1", "--label", "AAA"},
		"--hosts cannot be used along side any other flag",
	)
	runAppCheckErr(t,
		[]string{"hosts", "transfer", "--team", "team1"},
		"You need to define either --hosts, or one or more of --label, --status, --search_query",
	)
	runAppCheckErr(t,
		[]string{"hosts", "transfer", "--team", "team1", "--hosts", "host1", "--label-automdm", "team2"},
		"--hosts cannot be used along side any other flag",
	)
	runAppCheckErr(t,
		[]string{"hosts", "transfer", "--team", "team1", "--status", "online", "--label-automdm", "team2"},
		"--label-automdm can only be used with --label",
	)
}

func TestHostsTransferByHosts(t *testing.T) {
	opts := &service.TestServerOpts{}
	_, ds := testing_utils.RunServerWithMockedDS(t, opts)

	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
		require.Equal(t, "host1", identifier)
		return 42, nil
	}

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		require.Equal(t, "team1", name)
		return &automdm.Team{ID: 99, Name: "team1"}, nil
	}

	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		require.NotNil(t, params.TeamID)
		require.Equal(t, uint(99), *params.TeamID)
		require.Equal(t, []uint{42}, params.HostIDs)
		return nil
	}

	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hostIDs, teamIDs []uint, profileUUIDs, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hostIDs []uint) ([]string, error) {
		return nil, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team1"}, nil
	}

	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		require.IsType(t, automdm.ActivityTypeTransferredHostsToTeam{}, activity)
		return nil
	}

	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 42}}, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "team1", "--hosts", "host1"}))
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
	assert.True(t, opts.ActivityMock.NewActivityFuncInvoked)

	// Now, transfer out of the team.
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		assert.Nil(t, params.TeamID)
		assert.Equal(t, []uint{42}, params.HostIDs)
		return nil
	}
	opts.ActivityMock.NewActivityFuncInvoked = false
	ds.AddHostsToTeamFuncInvoked = false
	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "", "--hosts", "host1"}))
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
	assert.True(t, opts.ActivityMock.NewActivityFuncInvoked)
}

func TestHostsTransferByLabel(t *testing.T) {
	opts := &service.TestServerOpts{}
	_, ds := testing_utils.RunServerWithMockedDS(t, opts)

	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
		require.Equal(t, "host1", identifier)
		return 42, nil
	}

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		require.Equal(t, "team1", name)
		return &automdm.Team{ID: 99, Name: "team1"}, nil
	}

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		require.Equal(t, []string{"label1"}, names)
		return map[string][]uint{"label1": {11}}, nil
	}

	ds.ListHostsInLabelFunc = func(ctx context.Context, filter automdm.TeamFilter, lid uint, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		require.Equal(t, automdm.HostStatus(""), opt.StatusFilter)
		require.Equal(t, uint(11), lid)
		return []*automdm.Host{{ID: 32}, {ID: 12}}, nil
	}

	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		require.NotNil(t, params.TeamID)
		require.Equal(t, uint(99), *params.TeamID)
		require.Equal(t, []uint{32, 12}, params.HostIDs)
		return nil
	}

	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hostIDs, teamIDs []uint, profileUUIDs, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hostIDs []uint) ([]string, error) {
		return nil, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team1"}, nil
	}

	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		require.IsType(t, automdm.ActivityTypeTransferredHostsToTeam{}, activity)
		return nil
	}

	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "team1", "--label", "label1"}))
	require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
	assert.True(t, ds.AddHostsToTeamFuncInvoked)

	// Now, transfer out of the team.
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		assert.Nil(t, params.TeamID)
		require.Equal(t, []uint{32, 12}, params.HostIDs)
		return nil
	}
	opts.ActivityMock.NewActivityFuncInvoked = false
	ds.AddHostsToTeamFuncInvoked = false
	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "", "--label", "label1"}))
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
	assert.True(t, opts.ActivityMock.NewActivityFuncInvoked)

	// GitOps users get the transfer message, and no host moves: from the server with --label, from automdmctl with --hosts.
	for _, user := range []*automdm.User{
		{GlobalRole: new(automdm.RoleGitOps)},
		{Teams: []automdm.UserTeam{{ID: 99, Role: automdm.RoleGitOps}}},
	} {
		ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			user.ID = id
			return user, nil
		}
		for _, flags := range [][]string{{"--label", "label1"}, {"--hosts", "host1"}} {
			ds.AddHostsToTeamFuncInvoked = false
			_, err := runAppNoChecks(append([]string{"hosts", "transfer", "--team", "team1"}, flags...))
			require.ErrorContains(t, err, automdm.GitOpsTransferHostErrorMessage, flags)
			assert.False(t, ds.AddHostsToTeamFuncInvoked)
		}
	}
}

func TestHostsTransferByLabelSharedByTeams(t *testing.T) {
	opts := &service.TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}}
	_, ds := testing_utils.RunServerWithMockedDS(t, opts)

	const (
		destinationTeamID   = uint(99)
		labelTeamID         = uint(77)
		labelAutoMDMLabelID = uint(11)
		otherTeamLabelID    = uint(12)
	)
	automdmIDsByName := map[string]uint{"team1": destinationTeamID, "team2": labelTeamID}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		automdmID, ok := automdmIDsByName[name]
		require.True(t, ok, "unexpected team name %q", name)
		return &automdm.Team{ID: automdmID, Name: name}, nil
	}

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		require.Equal(t, []string{"label1"}, names)
		return map[string][]uint{"label1": {labelAutoMDMLabelID, otherTeamLabelID}}, nil
	}

	ds.GetLabelSpecFunc = func(ctx context.Context, filter automdm.TeamFilter, name string) (*automdm.LabelSpec, error) {
		require.Equal(t, "label1", name)
		require.NotNil(t, filter.TeamID)
		require.Equal(t, labelTeamID, *filter.TeamID)
		return &automdm.LabelSpec{ID: labelAutoMDMLabelID, Name: name, TeamID: filter.TeamID}, nil
	}

	ds.ListHostsInLabelFunc = func(ctx context.Context, filter automdm.TeamFilter, lid uint, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		require.Equal(t, labelAutoMDMLabelID, lid)
		return []*automdm.Host{{ID: 32, TeamID: new(labelTeamID)}, {ID: 12, TeamID: new(labelTeamID)}}, nil
	}

	var transferredToTeamID *uint
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		transferredToTeamID = params.TeamID
		require.Equal(t, []uint{32, 12}, params.HostIDs)
		return nil
	}

	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hostIDs, teamIDs []uint, profileUUIDs, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hostIDs []uint) ([]string, error) {
		return nil, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team1"}, nil
	}

	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		require.IsType(t, automdm.ActivityTypeTransferredHostsToTeam{}, activity)
		return nil
	}

	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	_, err := runAppNoChecks([]string{"hosts", "transfer", "--team", "team1", "--label", "label1"})
	require.ErrorContains(t, err, `More than one team has a label named "label1".`)
	require.ErrorContains(t, err, "add --label-automdm <team name>")
	assert.False(t, ds.AddHostsToTeamFuncInvoked)

	ds.AllLabelIDsByNameFuncInvoked = false
	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "team1", "--label", "label1", "--label-automdm", "team2"}))
	assert.False(t, ds.AllLabelIDsByNameFuncInvoked)
	assert.True(t, ds.GetLabelSpecFuncInvoked)
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
	assert.True(t, opts.ActivityMock.NewActivityFuncInvoked)
	require.NotNil(t, transferredToTeamID)
	assert.Equal(t, destinationTeamID, *transferredToTeamID)

	ds.AddHostsToTeamFuncInvoked = false
	assert.Empty(t, runAppForTest(t, []string{"hosts", "transfer", "--team", "", "--label", "label1", "--label-automdm", "team2"}))
	assert.False(t, ds.AllLabelIDsByNameFuncInvoked)
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
	assert.Nil(t, transferredToTeamID)
}

func TestHostsTransferByStatus(t *testing.T) {
	opts := &service.TestServerOpts{}
	_, ds := testing_utils.RunServerWithMockedDS(t, opts)

	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
		require.Equal(t, "host1", identifier)
		return 42, nil
	}

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		require.Equal(t, "team1", name)
		return &automdm.Team{ID: 99, Name: "team1"}, nil
	}

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		require.Equal(t, []string{"label1"}, names)
		return map[string][]uint{"label1": {11}}, nil
	}

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		require.Equal(t, automdm.StatusOnline, opt.StatusFilter)
		return []*automdm.Host{{ID: 32}, {ID: 12}}, nil
	}

	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		require.NotNil(t, params.TeamID)
		require.Equal(t, uint(99), *params.TeamID)
		require.Equal(t, []uint{32, 12}, params.HostIDs)
		return nil
	}

	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hostIDs, teamIDs []uint, profileUUIDs, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hostIDs []uint) ([]string, error) {
		return nil, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team1"}, nil
	}

	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		require.IsType(t, automdm.ActivityTypeTransferredHostsToTeam{}, activity)
		return nil
	}

	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	assert.Empty(t, runAppForTest(t,
		[]string{"hosts", "transfer", "--team", "team1", "--status", "online"}))
	require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
}

func TestHostsTransferByStatusAndSearchQuery(t *testing.T) {
	opts := &service.TestServerOpts{}
	_, ds := testing_utils.RunServerWithMockedDS(t, opts)

	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
		require.Equal(t, "host1", identifier)
		return 42, nil
	}

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		require.Equal(t, "team1", name)
		return &automdm.Team{ID: 99, Name: "team1"}, nil
	}

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		require.Equal(t, []string{"label1"}, names)
		return map[string][]uint{"label1": {11}}, nil
	}

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		require.Equal(t, automdm.StatusOnline, opt.StatusFilter)
		require.Equal(t, "somequery", opt.MatchQuery)
		return []*automdm.Host{{ID: 32}, {ID: 12}}, nil
	}

	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		require.NotNil(t, params.TeamID)
		require.Equal(t, uint(99), *params.TeamID)
		require.Equal(t, []uint{32, 12}, params.HostIDs)
		return nil
	}

	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hostIDs, teamIDs []uint, profileUUIDs, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hostIDs []uint) ([]string, error) {
		return nil, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team1"}, nil
	}

	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		require.IsType(t, automdm.ActivityTypeTransferredHostsToTeam{}, activity)
		return nil
	}

	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	assert.Empty(t, runAppForTest(t,
		[]string{"hosts", "transfer", "--team", "team1", "--status", "online", "--search_query", "somequery"}))
	require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
}
