package test

import (
	"context"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func NewQueryWithSchedule(t *testing.T, ds automdm.Datastore, teamID *uint, name, q string, authorID uint, saved bool, interval uint, automationsEnabled bool) *automdm.Query {
	authorPtr := &authorID
	if authorID == 0 {
		authorPtr = nil
	}
	query, err := ds.NewQuery(context.Background(), &automdm.Query{
		Name:               name,
		Query:              q,
		AuthorID:           authorPtr,
		Saved:              saved,
		TeamID:             teamID,
		Interval:           interval,
		AutomationsEnabled: automationsEnabled,
		Logging:            automdm.LoggingSnapshot,
	})
	require.NoError(t, err)

	// Loading gives us the timestamps
	query, err = ds.Query(context.Background(), query.ID)
	require.NoError(t, err)

	return query
}

func NewQuery(t *testing.T, ds automdm.Datastore, teamID *uint, name, q string, authorID uint, saved bool) *automdm.Query {
	return NewQueryWithSchedule(t, ds, teamID, name, q, authorID, saved, 0, false)
}

func NewCampaign(t *testing.T, ds automdm.Datastore, queryID uint, status automdm.DistributedQueryStatus, now time.Time) *automdm.DistributedQueryCampaign {
	campaign, err := ds.NewDistributedQueryCampaign(context.Background(), &automdm.DistributedQueryCampaign{
		UpdateCreateTimestamps: automdm.UpdateCreateTimestamps{
			CreateTimestamp: automdm.CreateTimestamp{
				CreatedAt: now,
			},
		},
		QueryID: queryID,
		Status:  status,
	})
	require.NoError(t, err)

	// Loading gives us the timestamps
	campaign, err = ds.DistributedQueryCampaign(context.Background(), campaign.ID)
	require.NoError(t, err)

	return campaign
}

func AddHostToCampaign(t *testing.T, ds automdm.Datastore, campaignID, hostID uint) {
	_, err := ds.NewDistributedQueryCampaignTarget(
		context.Background(),
		&automdm.DistributedQueryCampaignTarget{
			Type:                       automdm.TargetHost,
			TargetID:                   hostID,
			DistributedQueryCampaignID: campaignID,
		})
	require.NoError(t, err)
}

func AddLabelToCampaign(t *testing.T, ds automdm.Datastore, campaignID, labelID uint) {
	_, err := ds.NewDistributedQueryCampaignTarget(
		context.Background(),
		&automdm.DistributedQueryCampaignTarget{
			Type:                       automdm.TargetLabel,
			TargetID:                   labelID,
			DistributedQueryCampaignID: campaignID,
		})
	require.NoError(t, err)
}

func AddAllHostsLabel(t *testing.T, ds automdm.Datastore) {
	_, err := ds.NewLabel(
		context.Background(),
		&automdm.Label{
			Name:                "All Hosts",
			Query:               "select 1",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeManual,
		},
	)
	require.NoError(t, err)
}

func BuiltinLabels() []*automdm.Label {
	return []*automdm.Label{
		{
			Name:                "All Hosts",
			Query:               "select 1",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "macOS",
			Query:               "select 1 from os_version where platform = 'darwin';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "Ubuntu Linux",
			Query:               "select 1 from os_version where platform = 'ubuntu' or platform_like like '%ubuntu%';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "CentOS Linux",
			Query:               "select 1 from os_version where platform = 'centos' or name like '%centos%';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "MS Windows",
			Query:               "select 1 from os_version where platform = 'windows';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "Red Hat Linux",
			Query:               "SELECT 1 FROM os_version WHERE name LIKE '%red hat%'",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "All Linux",
			Query:               "SELECT 1 FROM osquery_info WHERE build_platform LIKE '%ubuntu%' OR build_distro LIKE '%centos%';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "chrome",
			Query:               "select 1 from os_version where platform = 'chrome';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                automdm.BuiltinLabelMacOS14Plus,
			Query:               "select 1 from os_version where platform = 'darwin' and major >= 14;",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "iOS",
			Platform:            "ios",
			Query:               "",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeManual,
		},
		{
			Name:                "iPadOS",
			Platform:            "ipados",
			Query:               "",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeManual,
		},
		{
			Name:                "Fedora Linux",
			Platform:            "rhel",
			Query:               "select 1 from os_version where name like '%fedora%';",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
		{
			Name:                "Android",
			Platform:            "android",
			Query:               "",
			LabelType:           automdm.LabelTypeBuiltIn,
			LabelMembershipType: automdm.LabelMembershipTypeManual,
		},
	}
}

func AddBuiltinLabels(t *testing.T, ds automdm.Datastore) {
	builtins := BuiltinLabels()

	names := automdm.ReservedLabelNames()
	require.Equal(t, len(builtins), len(names))
	storedByName := map[string]*automdm.Label{}
	for _, b := range builtins {
		stored, err := ds.NewLabel(context.Background(), b)
		require.NoError(t, err)
		storedByName[stored.Name] = stored
	}
	require.Len(t, storedByName, len(builtins))

	for name := range names {
		_, ok := storedByName[name]
		require.True(t, ok, "expected label %s to be created", name)
	}
}

// NewHostOption is an Option for the NewHost function.
type NewHostOption func(*automdm.Host)

// WithComputerName sets the ComputerName in NewHost.
func WithComputerName(s string) NewHostOption {
	return func(h *automdm.Host) {
		h.ComputerName = s
	}
}

func WithPlatform(s string) NewHostOption {
	return func(h *automdm.Host) {
		h.Platform = s
	}
}

func WithOSVersion(s string) NewHostOption {
	return func(h *automdm.Host) {
		h.OSVersion = s
	}
}

func WithTeamID(teamID uint) NewHostOption {
	return func(h *automdm.Host) {
		h.TeamID = &teamID
	}
}

// WithHardwareSerial sets the HardwareSerial in NewHost.
func WithHardwareSerial(s string) NewHostOption {
	return func(h *automdm.Host) {
		h.HardwareSerial = s
	}
}

func NewHost(tb testing.TB, ds automdm.Datastore, name, ip, key, uuid string, now time.Time, options ...NewHostOption) *automdm.Host {
	osqueryHostID, _ := server.GenerateRandomText(10)
	h := &automdm.Host{
		Hostname:        name,
		NodeKey:         &key,
		UUID:            uuid,
		DetailUpdatedAt: now,
		LabelUpdatedAt:  now,
		PolicyUpdatedAt: now,
		SeenTime:        now,
		OsqueryHostID:   &osqueryHostID,
		Platform:        "darwin",
		PublicIP:        ip,
		PrimaryIP:       ip,
	}
	for _, o := range options {
		o(h)
	}
	h, err := ds.NewHost(context.Background(), h)
	require.NoError(tb, err)
	require.NotZero(tb, h.ID)
	require.NoError(tb, ds.MarkHostsSeen(context.Background(), []uint{h.ID}, now))

	return h
}

func NewUser(t *testing.T, ds automdm.Datastore, name, email string, admin bool) *automdm.User {
	role := automdm.RoleObserver
	if admin {
		role = automdm.RoleAdmin
	}
	u, err := ds.NewUser(context.Background(), &automdm.User{
		Password:   []byte("garbage"),
		Salt:       "garbage",
		Name:       name,
		Email:      email,
		GlobalRole: &role,
	})

	require.NoError(t, err)
	require.NotZero(t, u.ID)

	return u
}
