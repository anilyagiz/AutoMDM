package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestTeams(t *testing.T) {
	ds := CreateMySQLDS(t)

	cases := []struct {
		name string
		fn   func(t *testing.T, ds *Datastore)
	}{
		{"GetSetDelete", testTeamsGetSetDelete},
		{"Users", testTeamsUsers},
		{"UpdateUsers", testTeamsUpdateUsers},
		{"List", testTeamsList},
		{"Summary", testTeamsSummary},
		{"Search", testTeamsSearch},
		{"EnrollSecrets", testTeamsEnrollSecrets},
		{"TeamAgentOptions", testTeamsAgentOptions},
		{"DeleteIntegrationsFromTeams", testTeamsDeleteIntegrationsFromTeams},
		{"TeamsFeatures", testTeamsFeatures},
		{"TeamsMDMConfig", testTeamsMDMConfig},
		{"TestTeamsNameUnicode", testTeamsNameUnicode},
		{"TestTeamsNameEmoji", testTeamsNameEmoji},
		{"TestTeamConflictsWithName", testTeamConflictsWithName},
		{"TestTeamsNameSort", testTeamsNameSort},
		{"TeamIDsWithSetupExperienceIdPEnabled", testTeamIDsWithSetupExperienceIdPEnabled},
		{"DefaultTeamConfig", testDefaultTeamConfig},
		{"TeamLitesByIDs", testTeamLitesByIDs},
		{"DeleteTeamCancelsSoftwareInstalls", testDeleteTeamCancelsSoftwareInstalls},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer TruncateTables(t, ds)
			c.fn(t, ds)
		})
	}
}

func testTeamsGetSetDelete(t *testing.T, ds *Datastore) {
	user, err := ds.NewUser(t.Context(), &automdm.User{
		Name:       "Foobar",
		Email:      "foobar@example.com",
		Password:   []byte("insecure"),
		GlobalRole: ptr.String(automdm.RoleAdmin),
	})
	require.NoError(t, err)

	test.CreateInsertGlobalVPPToken(t, ds)

	createTests := []struct {
		name, description string
	}{
		{"foo_team", "foobar is the description"},
		{"bar_team", "were you hoping for more?"},
	}

	for _, tt := range createTests {
		t.Run(tt.name, func(t *testing.T) {
			team, err := ds.NewTeam(context.Background(), &automdm.Team{
				Name:        tt.name,
				Description: tt.description,
			})
			require.NoError(t, err)
			assert.NotZero(t, team.ID)

			teamLite, err := ds.TeamLite(context.Background(), team.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.name, teamLite.Name)
			assert.Equal(t, tt.description, teamLite.Description)

			team, err = ds.TeamWithExtras(context.Background(), team.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.name, team.Name)
			assert.Equal(t, tt.description, team.Description)

			team, err = ds.TeamByName(context.Background(), tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.name, team.Name)
			assert.Equal(t, tt.description, team.Description)

			dummyMC := mobileconfig.Mobileconfig([]byte("DummyTestMobileconfigBytes"))
			dummyCP := automdm.MDMAppleConfigProfile{
				Name:         "DummyTestName",
				Identifier:   "DummyTestIdentifier",
				Mobileconfig: dummyMC,
				TeamID:       &team.ID,
			}
			cp, err := ds.NewMDMAppleConfigProfile(context.Background(), dummyCP, nil)
			require.NoError(t, err)

			wcp, err := ds.NewMDMWindowsConfigProfile(context.Background(), automdm.MDMWindowsConfigProfile{
				Name:   "abc",
				TeamID: &team.ID,
				SyncML: []byte(`<Replace></Replace>`),
			}, nil)
			require.NoError(t, err)

			dec, err := ds.NewMDMAppleDeclaration(context.Background(), &automdm.MDMAppleDeclaration{
				Identifier: "decl-1",
				Name:       "decl-1",
				TeamID:     &team.ID,
				RawJSON:    json.RawMessage(`{"Type": "com.apple.configuration.test", "Identifier": "decl-1"}`),
			}, nil)
			require.NoError(t, err)

			teamLabel, err := ds.NewLabel(t.Context(), &automdm.Label{
				Name:   "Team label",
				Query:  "SELECT 1;",
				TeamID: &team.ID,
			})
			require.NoError(t, err)

			// Create host on the team.
			host, err := ds.NewHost(t.Context(), &automdm.Host{
				OsqueryHostID:  ptr.String(fmt.Sprintf("foobar-%d", team.ID)),
				NodeKey:        ptr.String(fmt.Sprintf("foobar-%d", team.ID)),
				UUID:           fmt.Sprintf("foobar-%d", team.ID),
				Hostname:       fmt.Sprintf("foobar-%d.local", team.ID),
				HardwareSerial: fmt.Sprintf("darwin-serial-%d", team.ID),
				Platform:       "darwin",
			})
			require.NoError(t, err)

			// Add a bunch of data into tables with team IDs that should be cleared when team is deleted
			ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
				res, err := q.ExecContext(context.Background(), fmt.Sprintf(`INSERT INTO software_titles (name, source) VALUES ('MyCoolApp_%s', 'apps')`, tt.name))
				if err != nil {
					return err
				}
				titleID, _ := res.LastInsertId()
				_, err = q.ExecContext(
					context.Background(),
					`INSERT INTO
					      mdm_apple_configuration_profiles (profile_uuid, team_id, identifier, name, mobileconfig, checksum)
				     VALUES (?, ?, ?, ?, ?, ?)`,
					fmt.Sprintf("uuid_%s", tt.name),
					0,
					fmt.Sprintf("TestPayloadIdentifier_%s", tt.name),
					fmt.Sprintf("TestPayloadName_%s", tt.name),
					`<?xml version="1.0"`,
					[]byte("test"),
				)
				if err != nil {
					return err
				}
				_, err = q.ExecContext(context.Background(), `
								  INSERT INTO
								      mdm_windows_configuration_profiles (team_id, name, syncml, profile_uuid)
								  VALUES (?, ?, ?, ?)`, 0, fmt.Sprintf("TestPayloadName_%s", tt.name), `<?xml version="1.0"`, fmt.Sprintf("uuid_%s", tt.name))
				if err != nil {
					return err
				}
				_, err = q.ExecContext(context.Background(), `
								  INSERT INTO
								      mdm_android_configuration_profiles (profile_uuid, team_id, name, raw_json)
								  VALUES (?, ?, ?, ?)`, fmt.Sprintf("uuid_%s", tt.name), 0, fmt.Sprintf("TestPayloadName_%s", tt.name), `{"foo": "bar"}`)
				if err != nil {
					return err
				}
				_, err = q.ExecContext(
					context.Background(),
					"INSERT INTO software_title_display_names (team_id, software_title_id, display_name) VALUES (?, ?, ?)",
					team.ID,
					titleID,
					"delete_test",
				)
				if err != nil {
					return err
				}
				_, err = q.ExecContext(
					context.Background(),
					"INSERT INTO software_title_icons (team_id, software_title_id, storage_id, filename) VALUES (?, ?, ?, ?)",
					team.ID,
					titleID,
					"delete_test",
					"delete_test.png",
				)
				if err != nil {
					return err
				}

				_, err = q.ExecContext(
					context.Background(),
					"INSERT INTO software_title_team_pins (team_id, title_id, pinned_version) VALUES (?, ?, ?)",
					team.ID,
					titleID,
					"^1",
				)
				if err != nil {
					return err
				}

				// Unassigned (team_id 0) has no team to delete and must survive.
				_, err = q.ExecContext(
					context.Background(),
					"INSERT INTO patch_deadline_settings (team_id, days, `time`) VALUES (?, ?, ?), (0, ?, ?) ON DUPLICATE KEY UPDATE team_id = team_id",
					team.ID,
					"monday",
					"09:00",
					"friday",
					"17:00",
				)
				if err != nil {
					return err
				}

				// Insert the team label on a in-house application.
				res, err = q.ExecContext(context.Background(), fmt.Sprintf(`INSERT INTO software_titles (name, source) VALUES ('ipa_test-%s', 'ipados_apps')`, tt.name))
				if err != nil {
					return err
				}
				inHouseTitleID, _ := res.LastInsertId()
				res, err = q.ExecContext(context.Background(), `INSERT INTO in_house_apps
					(title_id, team_id, global_or_team_id, filename, version, platform, storage_id)
					VALUES (?, ?, ?, 'ipa_test.ipa', '1.0', 'ipados', '1dbbaf76f371ecb4c3dcdcfb53b8915b09ffe6c812586105e1ef1d421eb6fd6b')`,
					inHouseTitleID, team.ID, team.ID)
				if err != nil {
					return err
				}
				inHouseAppID, _ := res.LastInsertId()
				_, err = q.ExecContext(
					context.Background(),
					"INSERT INTO in_house_app_labels (in_house_app_id, label_id) VALUES (?, ?)",
					inHouseAppID,
					teamLabel.ID,
				)
				if err != nil {
					return err
				}

				// Insert the team label on a custom software installer.
				installer, err := automdm.NewTempFileReader(strings.NewReader("echo"), t.TempDir)
				require.NoError(t, err)
				_, _, err = ds.MatchOrCreateSoftwareInstaller(t.Context(), &automdm.UploadSoftwareInstallerPayload{
					InstallScript: "install zoo",
					InstallerFile: installer,
					StorageID:     uuid.NewString(),
					Filename:      "zoo.pkg",
					Title:         "zoo",
					Source:        "apps",
					Version:       "0.0.1",
					UserID:        user.ID,
					TeamID:        &team.ID,
					ValidatedLabels: &automdm.LabelIdentsWithScope{
						LabelScope: automdm.LabelScopeIncludeAny,
						ByName: map[string]automdm.LabelIdent{teamLabel.Name: {
							LabelID:   teamLabel.ID,
							LabelName: teamLabel.Name,
						}},
					},
				})
				require.NoError(t, err)

				// Insert the team label on a VPP app.
				vppApp := &automdm.VPPApp{
					Name: "vpp_app_labels_1" + t.Name(),
					VPPAppTeam: automdm.VPPAppTeam{
						VPPAppID: automdm.VPPAppID{AdamID: "5", Platform: automdm.MacOSPlatform},
						ValidatedLabels: &automdm.LabelIdentsWithScope{
							LabelScope: automdm.LabelScopeIncludeAny,
							ByName: map[string]automdm.LabelIdent{
								teamLabel.Name: {
									LabelID:   teamLabel.ID,
									LabelName: teamLabel.Name,
								},
							},
						},
					},
					BundleIdentifier: "b5",
				}
				_, err = ds.InsertVPPAppWithTeam(t.Context(), vppApp, &team.ID, nil)
				require.NoError(t, err)

				// Record a label membership on the team label.
				err = ds.RecordLabelQueryExecutions(t.Context(), host, map[uint]*bool{teamLabel.ID: ptr.Bool(true)}, time.Now(), false)
				require.NoError(t, err)
				hostLabels, err := ds.ListLabelsForHost(t.Context(), host.ID)
				require.NoError(t, err)
				require.Len(t, hostLabels, 1)
				require.Equal(t, teamLabel.ID, hostLabels[0].ID)

				return nil
			})

			err = ds.DeleteTeam(context.Background(), team.ID)
			require.NoError(t, err)

			// Check that related tables were cleared.
			ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
				for _, table := range teamRefs {
					var count int
					err := sqlx.GetContext(context.Background(), q, &count, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE team_id = ?`, table), team.ID)
					if err != nil {
						return err
					}
					assert.Zerof(t, count, "table: %s", table)
				}
				return nil
			})

			unassigned, err := ds.GetPatchDeadlineSettings(t.Context(), 0)
			require.NoError(t, err)
			require.Equal(t, []string{"friday"}, unassigned.Days)

			// Check that team-label related tables were cleared.
			ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
				for _, table := range teamLabelsRefs {
					var count int
					err := sqlx.GetContext(context.Background(), q, &count, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE label_id = ?`, table), teamLabel.ID)
					if err != nil {
						return err
					}
					assert.Zerof(t, count, "table: %s", table)
				}
				return nil
			})

			_, err = ds.TeamByName(context.Background(), tt.name)
			require.Error(t, err)

			_, err = ds.GetMDMAppleConfigProfile(context.Background(), cp.ProfileUUID)
			var nfe automdm.NotFoundError
			require.ErrorAs(t, err, &nfe)

			_, err = ds.GetMDMWindowsConfigProfile(context.Background(), wcp.ProfileUUID)
			require.ErrorAs(t, err, &nfe)

			_, err = ds.GetMDMAppleConfigProfile(context.Background(), dec.DeclarationUUID)
			require.ErrorAs(t, err, &nfe)

			// Check team label is gone.
			labels, err := ds.LabelsByName(context.Background(), []string{teamLabel.Name}, automdm.TeamFilter{})
			require.NoError(t, err)
			require.Empty(t, labels)

			hostLabels, err := ds.ListLabelsForHost(t.Context(), host.ID)
			require.NoError(t, err)
			require.Empty(t, hostLabels)

			// ListLabelsForHost joins labels, so check the membership rows themselves.
			require.Eventually(t, func() bool {
				var membershipCount int
				ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
					return sqlx.GetContext(t.Context(), q, &membershipCount, `SELECT COUNT(*) FROM label_membership WHERE label_id = ?`, teamLabel.ID)
				})
				return membershipCount == 0
			}, 10*time.Second, 50*time.Millisecond)
		})
	}
}

func testTeamsUsers(t *testing.T, ds *Datastore) {
	users := createTestUsers(t, ds)
	user1 := automdm.User{Name: users[0].Name, Email: users[0].Email, ID: users[0].ID}
	user2 := automdm.User{Name: users[1].Name, Email: users[1].Email, ID: users[1].ID}

	team1, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team2"})
	require.NoError(t, err)

	team1, err = ds.TeamWithExtras(context.Background(), team1.ID)
	require.NoError(t, err)
	assert.Len(t, team1.Users, 0)

	team1Users := []automdm.TeamUser{
		{User: user1, Role: "maintainer"},
		{User: user2, Role: "observer"},
	}
	team1.Users = team1Users
	team1, err = ds.SaveTeam(context.Background(), team1)
	require.NoError(t, err)

	team1, err = ds.TeamWithExtras(context.Background(), team1.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, team1Users, team1.Users)
	// Ensure team 2 not effected
	team2, err = ds.TeamWithExtras(context.Background(), team2.ID)
	require.NoError(t, err)
	assert.Len(t, team2.Users, 0)

	team1Users = []automdm.TeamUser{
		{User: user2, Role: "maintainer"},
	}
	team1.Users = team1Users
	team1, err = ds.SaveTeam(context.Background(), team1)
	require.NoError(t, err)
	team1, err = ds.TeamWithExtras(context.Background(), team1.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, team1Users, team1.Users)

	team2Users := []automdm.TeamUser{
		{User: user2, Role: "observer"},
	}
	team2.Users = team2Users
	team1, err = ds.SaveTeam(context.Background(), team1)
	require.NoError(t, err)
	team1, err = ds.TeamWithExtras(context.Background(), team1.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, team1Users, team1.Users)
	team2, err = ds.SaveTeam(context.Background(), team2)
	require.NoError(t, err)
	team2, err = ds.TeamWithExtras(context.Background(), team2.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, team2Users, team2.Users)
}

func testTeamsUpdateUsers(t *testing.T, ds *Datastore) {
	ctx := t.Context()
	users := createTestUsers(t, ds)
	team, err := ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
	require.NoError(t, err)
	otherTeam, err := ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + " other"})
	require.NoError(t, err)
	unknownID := users[1].ID + 100000

	roleIn := func(user automdm.User, teamID uint) string {
		for _, userTeam := range user.Teams {
			if userTeam.ID == teamID {
				return userTeam.Role
			}
		}
		return ""
	}
	set := func(id uint, role string) automdm.TeamUser {
		return automdm.TeamUser{ID: id, Role: role}
	}

	_, err = ds.UpdateTeamUsers(ctx, otherTeam.ID, []automdm.TeamUser{set(users[0].ID, automdm.RoleAdmin)}, nil)
	require.NoError(t, err)

	previous, err := ds.UpdateTeamUsers(ctx, team.ID, []automdm.TeamUser{set(users[0].ID, automdm.RoleObserver), set(users[1].ID, automdm.RoleObserver)}, nil)
	require.NoError(t, err)
	assert.Empty(t, roleIn(previous[users[0].ID], team.ID))
	assert.Equal(t, automdm.RoleAdmin, roleIn(previous[users[0].ID], otherTeam.ID))
	assert.Equal(t, users[0].Email, previous[users[0].ID].Email)

	// Changing one member leaves the other member's role in place.
	previous, err = ds.UpdateTeamUsers(ctx, team.ID, []automdm.TeamUser{set(users[0].ID, automdm.RoleMaintainer)}, nil)
	require.NoError(t, err)
	assert.Equal(t, automdm.RoleObserver, roleIn(previous[users[0].ID], team.ID))
	got, err := ds.TeamWithExtras(ctx, team.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{automdm.RoleMaintainer, automdm.RoleObserver}, []string{got.Users[0].Role, got.Users[1].Role})

	previous, err = ds.UpdateTeamUsers(ctx, team.ID, nil, []uint{users[0].ID, unknownID})
	require.NoError(t, err)
	require.Len(t, previous, 1)
	assert.Equal(t, automdm.RoleMaintainer, roleIn(previous[users[0].ID], team.ID))
	got, err = ds.TeamWithExtras(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, got.Users, 1)
	assert.Equal(t, users[1].ID, got.Users[0].ID)
	otherGot, err := ds.TeamWithExtras(ctx, otherTeam.ID)
	require.NoError(t, err)
	require.Len(t, otherGot.Users, 1)

	// Whichever update runs second must see the first one's role.
	for range 25 {
		_, err = ds.UpdateTeamUsers(ctx, team.ID, []automdm.TeamUser{set(users[0].ID, automdm.RoleObserver)}, nil)
		require.NoError(t, err)

		roles := []string{automdm.RoleAdmin, automdm.RoleMaintainer}
		seen := make([]string, len(roles))
		var g errgroup.Group
		for i, role := range roles {
			g.Go(func() error {
				previous, err := ds.UpdateTeamUsers(ctx, team.ID, []automdm.TeamUser{set(users[0].ID, role)}, nil)
				if err != nil {
					return err
				}
				seen[i] = roleIn(previous[users[0].ID], team.ID)
				return nil
			})
		}
		require.NoError(t, g.Wait())
		assert.Contains(t, seen, automdm.RoleObserver)
		assert.False(t, seen[0] == automdm.RoleObserver && seen[1] == automdm.RoleObserver, "both updates read the role before either saved: %v", seen)
	}
}

func testTeamsList(t *testing.T, ds *Datastore) {
	users := createTestUsers(t, ds)
	user1 := automdm.User{Name: users[0].Name, Email: users[0].Email, ID: users[0].ID, GlobalRole: ptr.String(automdm.RoleAdmin)}
	user2 := automdm.User{Name: users[1].Name, Email: users[1].Email, ID: users[1].ID, GlobalRole: ptr.String(automdm.RoleObserver)}

	team1, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team2"})
	require.NoError(t, err)

	teams, err := ds.ListTeams(context.Background(), automdm.TeamFilter{User: &user1}, automdm.ListOptions{})
	require.NoError(t, err)
	sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })

	assert.Equal(t, "team1", teams[0].Name)
	assert.Equal(t, 0, teams[0].HostCount)
	assert.Equal(t, 0, teams[0].UserCount)

	assert.Equal(t, "team2", teams[1].Name)
	assert.Equal(t, 0, teams[1].HostCount)
	assert.Equal(t, 0, teams[1].UserCount)

	host1 := test.NewHost(t, ds, "1", "1", "1", "1", time.Now())
	host2 := test.NewHost(t, ds, "2", "2", "2", "2", time.Now())
	host3 := test.NewHost(t, ds, "3", "3", "3", "3", time.Now())
	require.NoError(t, ds.AddHostsToTeam(context.Background(), automdm.NewAddHostsToTeamParams(&team1.ID, []uint{host1.ID})))
	require.NoError(t, ds.AddHostsToTeam(context.Background(), automdm.NewAddHostsToTeamParams(&team2.ID, []uint{host2.ID, host3.ID})))

	team1.Users = []automdm.TeamUser{
		{User: user1, Role: "maintainer"},
		{User: user2, Role: "observer"},
	}
	_, err = ds.SaveTeam(context.Background(), team1)
	require.NoError(t, err)

	team2.Users = []automdm.TeamUser{
		{User: user1, Role: "maintainer"},
	}
	_, err = ds.SaveTeam(context.Background(), team2)
	require.NoError(t, err)

	teams, err = ds.ListTeams(context.Background(), automdm.TeamFilter{User: &user1}, automdm.ListOptions{})
	require.NoError(t, err)
	sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })

	assert.Equal(t, "team1", teams[0].Name)
	assert.Equal(t, 1, teams[0].HostCount)
	assert.Equal(t, 2, teams[0].UserCount)

	assert.Equal(t, "team2", teams[1].Name)
	assert.Equal(t, 2, teams[1].HostCount)
	assert.Equal(t, 1, teams[1].UserCount)

	// Test that ds.TeamsWithExtras returns the same data as ds.ListTeams
	// (except list of users).
	for _, t1 := range teams {
		t2, err := ds.TeamWithExtras(context.Background(), t1.ID)
		require.NoError(t, err)
		t2.Users = nil
		require.Equal(t, t1, t2)
	}

	for _, key := range []string{"id", "name", "created_at", "user_count", "host_count"} {
		t.Run("order_"+key, func(t *testing.T) {
			result, err := ds.ListTeams(context.Background(), automdm.TeamFilter{User: &user1}, automdm.ListOptions{OrderKey: key, PerPage: 10})
			require.NoError(t, err)
			require.NotEmpty(t, result)
		})
	}

	t.Run("rejects_unknown_key", func(t *testing.T) {
		_, err := ds.ListTeams(context.Background(), automdm.TeamFilter{User: &user1}, automdm.ListOptions{OrderKey: "h.node_key"})
		require.Error(t, err)
	})
}

func testTeamsSummary(t *testing.T, ds *Datastore) {
	_, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "ts1"})
	require.NoError(t, err)
	_, err = ds.NewTeam(context.Background(), &automdm.Team{Name: "ts2"})
	require.NoError(t, err)

	teams, err := ds.TeamsSummary(context.Background())
	require.NoError(t, err)
	sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })

	assert.Equal(t, "ts1", teams[0].Name)
	assert.Equal(t, "ts2", teams[1].Name)
}

func testTeamsSearch(t *testing.T, ds *Datastore) {
	team1, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "team2"})
	require.NoError(t, err)
	team3, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "foobar"})
	require.NoError(t, err)
	team4, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "floobar"})
	require.NoError(t, err)

	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	filter := automdm.TeamFilter{User: user}

	teams, err := ds.SearchTeams(context.Background(), filter, "")
	require.NoError(t, err)
	assert.Len(t, teams, 4)

	teams, err = ds.SearchTeams(context.Background(), filter, "", team1.ID, team2.ID, team3.ID)
	require.NoError(t, err)
	assert.Len(t, teams, 1)
	assert.Equal(t, team4.Name, teams[0].Name)

	teams, err = ds.SearchTeams(context.Background(), filter, "oo", team1.ID, team2.ID, team3.ID)
	require.NoError(t, err)
	assert.Len(t, teams, 1)
	assert.Equal(t, team4.Name, teams[0].Name)

	teams, err = ds.SearchTeams(context.Background(), filter, "oo")
	require.NoError(t, err)
	assert.Len(t, teams, 2)

	teams, err = ds.SearchTeams(context.Background(), filter, "none")
	require.NoError(t, err)
	assert.Len(t, teams, 0)
}

func testTeamsEnrollSecrets(t *testing.T, ds *Datastore) {
	secrets := []*automdm.EnrollSecret{{Secret: "secret1"}, {Secret: "secret2"}}
	team1, err := ds.NewTeam(context.Background(), &automdm.Team{
		Name:    "team1",
		Secrets: secrets,
	})
	require.NoError(t, err)

	enrollSecrets, err := ds.TeamEnrollSecrets(context.Background(), team1.ID)
	require.NoError(t, err)

	var justSecrets []*automdm.EnrollSecret
	for _, secret := range enrollSecrets {
		require.NotNil(t, secret.TeamID)
		assert.Equal(t, team1.ID, *secret.TeamID)
		justSecrets = append(justSecrets, &automdm.EnrollSecret{Secret: secret.Secret})
	}
	test.ElementsMatchSkipTimestampsID(t, secrets, justSecrets)
}

func testTeamsAgentOptions(t *testing.T, ds *Datastore) {
	team1, err := ds.NewTeam(context.Background(), &automdm.Team{
		Name: "team1",
	})
	require.NoError(t, err)

	teamAgentOptions1, err := ds.TeamAgentOptions(context.Background(), team1.ID)
	require.NoError(t, err)
	require.Nil(t, teamAgentOptions1)

	agentOptions := json.RawMessage(`{"config":{"foo":"bar"},"overrides":{"platforms":{"darwin":{"foo":"override"}}}}`)
	team2, err := ds.NewTeam(context.Background(), &automdm.Team{
		Name: "team2",
		Config: automdm.TeamConfig{
			AgentOptions: &agentOptions,
		},
	})
	require.NoError(t, err)

	teamAgentOptions2, err := ds.TeamAgentOptions(context.Background(), team2.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(agentOptions), string(*teamAgentOptions2))
}

func testTeamsDeleteIntegrationsFromTeams(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	urla, urlb, urlc, urld, urle, urlf, urlg := "http://a.com", "http://b.com", "http://c.com", "http://d.com", "http://e.com", "http://f.com", "http://g.com"

	// create some teams
	team1, err := ds.NewTeam(ctx, &automdm.Team{
		Name: "team1",
		Config: automdm.TeamConfig{
			Integrations: automdm.TeamIntegrations{
				Jira: []*automdm.TeamJiraIntegration{
					{URL: urla, ProjectKey: "A"},
					{URL: urlb, ProjectKey: "B"},
				},
				Zendesk: []*automdm.TeamZendeskIntegration{
					{URL: urlc, GroupID: 1},
					{URL: urld, GroupID: 2},
				},
			},
		},
	})
	require.NoError(t, err)

	team2, err := ds.NewTeam(ctx, &automdm.Team{
		Name: "team2",
		Config: automdm.TeamConfig{
			Integrations: automdm.TeamIntegrations{
				Jira: []*automdm.TeamJiraIntegration{
					{URL: urla, ProjectKey: "A"},
					{URL: urle, ProjectKey: "E"},
				},
				Zendesk: []*automdm.TeamZendeskIntegration{
					{URL: urlc, GroupID: 1},
					{URL: urlf, GroupID: 3},
				},
			},
		},
	})
	require.NoError(t, err)

	team3, err := ds.NewTeam(ctx, &automdm.Team{
		Name: "team3",
		Config: automdm.TeamConfig{
			Integrations: automdm.TeamIntegrations{
				Jira: []*automdm.TeamJiraIntegration{
					{URL: urle, ProjectKey: "E"},
				},
				Zendesk: []*automdm.TeamZendeskIntegration{
					{URL: urlf, GroupID: 3},
				},
			},
		},
	})
	require.NoError(t, err)

	assertIntgURLs := func(wantTm1, wantTm2, wantTm3 []string) {
		// assert that the integrations' URLs of each team corresponds to the
		// expected values
		expected := [][]string{wantTm1, wantTm2, wantTm3}
		for i, id := range []uint{team1.ID, team2.ID, team3.ID} {
			tm, err := ds.TeamLite(ctx, id)
			require.NoError(t, err)

			var urls []string
			for _, j := range tm.Config.Integrations.Jira {
				urls = append(urls, j.URL)
			}
			for _, z := range tm.Config.Integrations.Zendesk {
				urls = append(urls, z.URL)
			}

			want := expected[i]
			require.ElementsMatch(t, want, urls)
		}
	}

	// delete nothing
	err = ds.DeleteIntegrationsFromTeams(context.Background(), automdm.Integrations{})
	require.NoError(t, err)
	assertIntgURLs([]string{urla, urlb, urlc, urld}, []string{urla, urle, urlc, urlf}, []string{urle, urlf})

	// delete a, b, c (in the url) so that team1 and team2 are impacted
	err = ds.DeleteIntegrationsFromTeams(context.Background(), automdm.Integrations{
		Jira: []*automdm.JiraIntegration{
			{URL: urla, ProjectKey: "A"},
			{URL: urlb, ProjectKey: "B"},
		},
		Zendesk: []*automdm.ZendeskIntegration{
			{URL: urlc, GroupID: 1},
		},
	})
	require.NoError(t, err)
	assertIntgURLs([]string{urld}, []string{urle, urlf}, []string{urle, urlf})

	// delete g, no team is impacted
	err = ds.DeleteIntegrationsFromTeams(context.Background(), automdm.Integrations{
		Jira: []*automdm.JiraIntegration{
			{URL: urlg, ProjectKey: "G"},
		},
	})
	require.NoError(t, err)
	assertIntgURLs([]string{urld}, []string{urle, urlf}, []string{urle, urlf})
}

func testTeamsFeatures(t *testing.T, ds *Datastore) {
	defaultFeatures := automdm.Features{}
	defaultFeatures.ApplyDefaultsForNewInstalls()
	ctx := context.Background()

	t.Run("NULL config in the database", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{Name: "team_null_config"})
		require.NoError(t, err)
		ExecAdhocSQL(t, ds, func(tx sqlx.ExtContext) error {
			_, err = tx.ExecContext(
				ctx,
				"UPDATE teams SET config = NULL WHERE id = ?",
				team.ID,
			)
			return err
		})

		features, err := ds.TeamFeatures(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, &defaultFeatures, features)

		// retrieving a team also returns a team with the default
		// features
		team, err = ds.TeamWithExtras(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultFeatures, team.Config.Features)

		team, err = ds.TeamByName(ctx, team.Name)
		require.NoError(t, err)
		assert.Equal(t, defaultFeatures, team.Config.Features)
	})

	t.Run("NULL config.features in the database", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{Name: "team_null_config_features"})
		require.NoError(t, err)
		ExecAdhocSQL(t, ds, func(tx sqlx.ExtContext) error {
			_, err = tx.ExecContext(
				ctx,
				"UPDATE teams SET config = '{}' WHERE id = ?",
				team.ID,
			)
			return err
		})

		features, err := ds.TeamFeatures(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, &defaultFeatures, features)

		// retrieving a team also returns a team with the default
		// features
		team, err = ds.TeamWithExtras(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultFeatures, team.Config.Features)

		team, err = ds.TeamByName(ctx, team.Name)
		require.NoError(t, err)
		assert.Equal(t, defaultFeatures, team.Config.Features)
	})

	t.Run("saves and retrieves configs", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{
			Name: "team1",
			Config: automdm.TeamConfig{
				Features: automdm.Features{
					EnableHostUsers:         false,
					EnableSoftwareInventory: false,
					AdditionalQueries:       nil,
				},
			},
		})
		require.NoError(t, err)
		features, err := ds.TeamFeatures(ctx, team.ID)
		require.NoError(t, err)

		assert.Equal(t, &automdm.Features{
			EnableHostUsers:         false,
			EnableSoftwareInventory: false,
			AdditionalQueries:       nil,
		}, features)
	})
}

func testTeamsMDMConfig(t *testing.T, ds *Datastore) {
	defaultMDM := automdm.TeamMDM{}
	ctx := context.Background()

	t.Run("missing team", func(t *testing.T) {
		_, err := ds.TeamMDMConfig(ctx, 999999)
		require.True(t, automdm.IsNotFound(err))
	})

	t.Run("NULL config in the database", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{Name: "team_null_config"})
		require.NoError(t, err)
		ExecAdhocSQL(t, ds, func(tx sqlx.ExtContext) error {
			_, err = tx.ExecContext(
				ctx,
				"UPDATE teams SET config = NULL WHERE id = ?",
				team.ID,
			)
			return err
		})

		mdm, err := ds.TeamMDMConfig(ctx, team.ID)
		require.NoError(t, err)
		assert.Nil(t, mdm)

		// retrieving a team also returns a team with the default
		// settings
		team, err = ds.TeamWithExtras(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, team.Config.MDM)

		teamLite, err := ds.TeamLite(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, teamLite.Config.MDM)

		team, err = ds.TeamByName(ctx, team.Name)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, team.Config.MDM)
	})

	t.Run("NULL config.mdm in the database", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{Name: "team_null_config_mdm"})
		require.NoError(t, err)
		ExecAdhocSQL(t, ds, func(tx sqlx.ExtContext) error {
			_, err = tx.ExecContext(
				ctx,
				"UPDATE teams SET config = '{}' WHERE id = ?",
				team.ID,
			)
			return err
		})

		mdm, err := ds.TeamMDMConfig(ctx, team.ID)
		require.NoError(t, err)
		assert.Nil(t, mdm)

		// retrieving a team also returns a team with the default
		// settings
		team, err = ds.TeamWithExtras(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, team.Config.MDM)

		teamLite, err := ds.TeamLite(ctx, team.ID)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, teamLite.Config.MDM)

		team, err = ds.TeamByName(ctx, team.Name)
		require.NoError(t, err)
		assert.Equal(t, defaultMDM, team.Config.MDM)
	})

	t.Run("saves and retrieves configs", func(t *testing.T) {
		team, err := ds.NewTeam(ctx, &automdm.Team{
			Name: "team1",
			Config: automdm.TeamConfig{
				MDM: automdm.TeamMDM{
					MacOSUpdates: automdm.AppleOSUpdateSettings{
						MinimumVersion: optjson.SetString("10.15.0"),
						Deadline:       optjson.SetString("2025-10-01"),
					},
					IOSUpdates: automdm.AppleOSUpdateSettings{
						MinimumVersion: optjson.SetString("11.11.11"),
						Deadline:       optjson.SetString("2024-04-04"),
					},
					IPadOSUpdates: automdm.AppleOSUpdateSettings{
						MinimumVersion: optjson.SetString("12.12.12"),
						Deadline:       optjson.SetString("2023-03-03"),
					},
					WindowsUpdates: automdm.WindowsUpdates{
						DeadlineDays:    optjson.SetInt(7),
						GracePeriodDays: optjson.SetInt(3),
					},
					MacOSSetup: automdm.MacOSSetup{
						ManualAgentInstall: optjson.SetBool(true),
					},
					AndroidSettings: automdm.AndroidSettings{
						Certificates: optjson.Slice[automdm.CertificateTemplateSpec]{Set: true, Value: []automdm.CertificateTemplateSpec{}},
					},
				},
			},
		})
		require.NoError(t, err)
		mdm, err := ds.TeamMDMConfig(ctx, team.ID)
		require.NoError(t, err)

		// The config round-trips through JSON, which always carries
		// deadline_days, so it reads back set-but-null rather than unset.
		// Disk encryption settings are normalized on marshal, so they read
		// back as explicit false.
		assert.Equal(t, &automdm.TeamMDM{
			MacOSSettings: automdm.MacOSSettings{
				EnableDiskEncryption:          optjson.SetBool(false),
				EnableEscrowDiskEncryptionKey: optjson.SetBool(false),
			},
			LinuxSettings: automdm.LinuxSettings{
				EnableEscrowDiskEncryptionKey: optjson.SetBool(false),
			},
			MacOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("10.15.0"),
				Deadline:       optjson.SetString("2025-10-01"),
				DeadlineDays:   optjson.Int{Set: true},
				UpdateNewHosts: optjson.Bool{Set: true},
			},
			IOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("11.11.11"),
				Deadline:       optjson.SetString("2024-04-04"),
				DeadlineDays:   optjson.Int{Set: true},
				UpdateNewHosts: optjson.Bool{Set: true},
			},
			IPadOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("12.12.12"),
				Deadline:       optjson.SetString("2023-03-03"),
				DeadlineDays:   optjson.Int{Set: true},
				UpdateNewHosts: optjson.Bool{Set: true},
			},
			WindowsUpdates: automdm.WindowsUpdates{
				DeadlineDays:    optjson.SetInt(7),
				GracePeriodDays: optjson.SetInt(3),
			},
			MacOSSetup: automdm.MacOSSetup{
				EnableReleaseDeviceManually:      optjson.SetBool(false),
				Software:                         optjson.Slice[*automdm.MacOSSetupSoftware]{Set: true, Value: []*automdm.MacOSSetupSoftware{}},
				ManualAgentInstall:               optjson.SetBool(true),
				LockEndUserInfo:                  optjson.SetBool(false),
				EnableManagedLocalAccount:        optjson.SetBool(false),
				EndUserLocalAccountType:          optjson.SetString("admin"),
				EnableManagedLocalAccountWindows: optjson.SetBool(false),
			},
			WindowsSettings: automdm.WindowsSettings{
				EnableDiskEncryption: optjson.SetBool(false),
				RequireBitLockerPIN:  optjson.SetBool(false),
			},
			AndroidSettings: automdm.AndroidSettings{
				Certificates: optjson.Slice[automdm.CertificateTemplateSpec]{Set: true, Value: []automdm.CertificateTemplateSpec{}},
			},
		}, mdm)
	})
}

func testTeamsNameUnicode(t *testing.T, ds *Datastore) {
	var equivalentNames []string
	item, _ := strconv.Unquote(`"\uAC00"`) // 가
	equivalentNames = append(equivalentNames, item)
	item, _ = strconv.Unquote(`"\u1100\u1161"`) // ᄀ + ᅡ
	equivalentNames = append(equivalentNames, item)

	// Save team
	team, err := ds.NewTeam(context.Background(), &automdm.Team{Name: equivalentNames[0]})
	require.NoError(t, err)
	assert.Equal(t, equivalentNames[0], team.Name)

	// Try to create team with equivalent name
	_, err = ds.NewTeam(context.Background(), &automdm.Team{Name: equivalentNames[1]})
	assert.True(t, IsDuplicate(err), err)

	// Try to update a different team with equivalent name -- not allowed
	teamEmoji, err := ds.NewTeam(context.Background(), &automdm.Team{Name: "💻"})
	require.NoError(t, err)
	_, err = ds.SaveTeam(context.Background(), &automdm.Team{ID: teamEmoji.ID, Name: equivalentNames[1]})
	assert.True(t, IsDuplicate(err), err)

	// Try to find team with equivalent name
	teamFilter := automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}}
	results, err := ds.ListTeams(context.Background(), teamFilter, automdm.ListOptions{MatchQuery: equivalentNames[1]})
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, equivalentNames[0], results[0].Name)

	results, err = ds.SearchTeams(context.Background(), teamFilter, equivalentNames[1])
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, equivalentNames[0], results[0].Name)

	result, err := ds.TeamByName(context.Background(), equivalentNames[1])
	assert.NoError(t, err)
	assert.Equal(t, equivalentNames[0], result.Name)
}

func testTeamConflictsWithName(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	// No teams exist → (nil, nil).
	conflict, err := ds.TeamConflictsWithName(ctx, "anything", 0)
	require.NoError(t, err)
	require.Nil(t, conflict)

	// Create a team and confirm excludeID=0 returns it, excludeID=team.ID
	// returns (nil, nil).
	team, err := ds.NewTeam(ctx, &automdm.Team{Name: "ABC"})
	require.NoError(t, err)

	conflict, err = ds.TeamConflictsWithName(ctx, "ABC", 0)
	require.NoError(t, err)
	require.NotNil(t, conflict)
	require.Equal(t, team.ID, conflict.ID)

	conflict, err = ds.TeamConflictsWithName(ctx, "ABC", team.ID)
	require.NoError(t, err)
	require.Nil(t, conflict)

	// Collation-equal variants: ASCII case.
	conflict, err = ds.TeamConflictsWithName(ctx, "abc", 0)
	require.NoError(t, err)
	require.Equal(t, team.ID, conflict.ID)

	// Collation-equal variants: NFC normalization (é written as combined vs.
	// e + combining acute accent).
	reneeCombined, err := ds.NewTeam(ctx, &automdm.Team{Name: "Renée"})
	require.NoError(t, err)
	reneeDecomposed := "Renée" // e + U+0301 COMBINING ACUTE ACCENT
	conflict, err = ds.TeamConflictsWithName(ctx, reneeDecomposed, 0)
	require.NoError(t, err)
	require.Equal(t, reneeCombined.ID, conflict.ID)

	// Deterministic exclude-self under a legacy collation-equal duplicate
	// pair. The production schema's unique index is on name_bin (binary
	// collation), so two rows that differ only by case — e.g., "ABC" and
	// "abc" — coexist as distinct rows but match each other under the
	// collation-aware WHERE clause used by this method. This is exactly the
	// scenario that motivated the excludeID parameter.
	res, err := ds.writer(ctx).ExecContext(ctx,
		`INSERT INTO teams (name, description, config) VALUES (?, ?, ?)`,
		"abc", "", []byte("{}"))
	require.NoError(t, err)
	dupID64, err := res.LastInsertId()
	require.NoError(t, err)
	dupID := uint(dupID64) //nolint:gosec // test code

	// Excluding the original team's id returns the legacy duplicate...
	conflict, err = ds.TeamConflictsWithName(ctx, "ABC", team.ID)
	require.NoError(t, err)
	require.Equal(t, dupID, conflict.ID)

	// ...and excluding the duplicate's id returns the original.
	conflict, err = ds.TeamConflictsWithName(ctx, "abc", dupID)
	require.NoError(t, err)
	require.Equal(t, team.ID, conflict.ID)

	// Only id and name are populated — this is a hot path so extras are not
	// loaded.
	require.NotZero(t, conflict.ID)
	require.NotEmpty(t, conflict.Name)
	require.Empty(t, conflict.Description)
	require.Nil(t, conflict.Users)
	require.Nil(t, conflict.Secrets)

	// Name key matches, which the collation doesn't see.
	existing := make(map[string]uint)
	for _, name := range []string{"Workstations", "Laptops 💻", "開発チーム", "Team 𝐀", "straße", "はは", "राम", "กิน", "мой"} {
		tm, err := ds.NewTeam(ctx, &automdm.Team{Name: name})
		require.NoError(t, err)
		existing[name] = tm.ID
	}
	for _, tc := range []struct {
		name     string
		conflict string
	}{
		{"Work-stations", "Workstations"},
		{"Work stations", "Workstations"},
		{"workstations!", "Workstations"},
		{"WORK_STATIONS", "Workstations"},
		{"Workstations 💻", "Workstations"},
		{"Wörkstations", "Workstations"},
		{"Ｗｏｒｋｓｔａｔｉｏｎｓ", "Workstations"},
		{"Laptops", "Laptops 💻"},
		{"Laptops 🚀", "Laptops 💻"},
		{"開発・チーム", "開発チーム"},
		{"開発チーム。", "開発チーム"},
		{"strasse", "straße"},
		{"Strasse!", "straße"},
		{"Team A", "Team 𝐀"},
		// Collation matches the name key doesn't see.
		{"Team 𝐁", "Team 𝐀"},
		{"ハハ", "はは"},
		{"パパ", "はは"},
		{"राम!", "राम"},
		{"กิน 🚀", "กิน"},
		{"МОЙ.", "мой"},
		// Outside Latin script a combining mark can make a different word.
		{"रम", ""},
		{"रम!", ""},
		{"กัน", ""},
		{"мои", ""},
		{"Workstations 2", ""},
		{"Workstation", ""},
		{"Laptop", ""},
		{"開発", ""},
		{"Team B!", ""},
		{"Servers", ""},
	} {
		conflict, err := ds.TeamConflictsWithName(ctx, tc.name, 0)
		require.NoError(t, err, tc.name)
		if tc.conflict == "" {
			require.Nil(t, conflict, tc.name)
			continue
		}
		require.NotNil(t, conflict, tc.name)
		require.Equal(t, tc.conflict, conflict.Name, tc.name)
		require.Equal(t, existing[tc.conflict], conflict.ID, tc.name)

		// A team can be renamed to a variant of its own name.
		conflict, err = ds.TeamConflictsWithName(ctx, tc.name, existing[tc.conflict])
		require.NoError(t, err, tc.name)
		require.Nil(t, conflict, tc.name)
	}
}

func testTeamsNameEmoji(t *testing.T, ds *Datastore) {
	// Try to save teams with emojis
	emoji0 := "🔥"
	_, err := ds.NewTeam(context.Background(), &automdm.Team{Name: emoji0})
	require.NoError(t, err)
	emoji1 := "💻"
	teamEmoji, err := ds.NewTeam(context.Background(), &automdm.Team{Name: emoji1})
	require.NoError(t, err)
	assert.Equal(t, emoji1, teamEmoji.Name)

	// Try to find team with emoji0
	teamFilter := automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}}
	results, err := ds.ListTeams(context.Background(), teamFilter, automdm.ListOptions{MatchQuery: emoji0})
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, emoji0, results[0].Name)

	// Try to find team with emoji1
	results, err = ds.SearchTeams(context.Background(), teamFilter, emoji1)
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, emoji1, results[0].Name)
}

// Ensure case-insensitive sort order for ames
func testTeamsNameSort(t *testing.T, ds *Datastore) {
	var teams [3]*automdm.Team
	var err error
	// Save teams
	teams[1], err = ds.NewTeam(context.Background(), &automdm.Team{Name: "В"})
	require.NoError(t, err)
	teams[2], err = ds.NewTeam(context.Background(), &automdm.Team{Name: "о"})
	require.NoError(t, err)
	teams[0], err = ds.NewTeam(context.Background(), &automdm.Team{Name: "а"})
	require.NoError(t, err)

	teamFilter := automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}}
	results, err := ds.ListTeams(context.Background(), teamFilter, automdm.ListOptions{OrderKey: "name"})
	assert.NoError(t, err)
	require.Len(t, teams, 3)
	for i, item := range teams {
		assert.Equal(t, item.Name, results[i].Name)
	}
}

func testTeamIDsWithSetupExperienceIdPEnabled(t *testing.T, ds *Datastore) {
	ctx := t.Context()

	// no team exists (well, except "no team" but not enabled there)
	teamIDs, err := ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.Empty(t, teamIDs)

	// create a couple teams
	team1, err := ds.NewTeam(ctx, &automdm.Team{Name: "A"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(ctx, &automdm.Team{Name: "B"})
	require.NoError(t, err)

	// still no team with IdP enabled
	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.Empty(t, teamIDs)
	_, _ = team1, team2

	// enable it for "No team"
	ac, err := ds.AppConfig(ctx)
	require.NoError(t, err)
	ac.MDM.MacOSSetup.EnableEndUserAuthentication = true
	err = ds.SaveAppConfig(ctx, ac)
	require.NoError(t, err)

	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{0}, teamIDs)

	// enable it for team1
	team1.Config.MDM.MacOSSetup.EnableEndUserAuthentication = true
	_, err = ds.SaveTeam(ctx, team1)
	require.NoError(t, err)

	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{0, team1.ID}, teamIDs)

	// enable it for team2
	team2.Config.MDM.MacOSSetup.EnableEndUserAuthentication = true
	_, err = ds.SaveTeam(ctx, team2)
	require.NoError(t, err)

	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{0, team1.ID, team2.ID}, teamIDs)

	// disable it for team1
	team1.Config.MDM.MacOSSetup.EnableEndUserAuthentication = false
	_, err = ds.SaveTeam(ctx, team1)
	require.NoError(t, err)

	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{0, team2.ID}, teamIDs)

	// delete team2
	err = ds.DeleteTeam(ctx, team2.ID)
	require.NoError(t, err)

	teamIDs, err = ds.TeamIDsWithSetupExperienceIdPEnabled(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{0}, teamIDs)
}

func testDefaultTeamConfig(t *testing.T, ds *Datastore) {
	ctx := t.Context()

	// Test getting default config (should be initialized by migration)
	config, err := ds.DefaultTeamConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Default config should have webhook disabled
	assert.False(t, config.WebhookSettings.FailingPoliciesWebhook.Enable)
	assert.Empty(t, config.WebhookSettings.FailingPoliciesWebhook.DestinationURL)

	// Test saving a new config
	newConfig := &automdm.TeamConfig{
		WebhookSettings: automdm.TeamWebhookSettings{
			FailingPoliciesWebhook: automdm.FailingPoliciesWebhookSettings{
				Enable:         true,
				DestinationURL: "https://example.com/webhook",
				HostBatchSize:  100,
			},
		},
		Features: automdm.Features{
			EnableHostUsers:         true,
			EnableSoftwareInventory: true,
		},
	}

	err = ds.SaveDefaultTeamConfig(ctx, newConfig)
	require.NoError(t, err)

	// Verify the config was saved
	savedConfig, err := ds.DefaultTeamConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, savedConfig)

	assert.True(t, savedConfig.WebhookSettings.FailingPoliciesWebhook.Enable)
	assert.Equal(t, "https://example.com/webhook", savedConfig.WebhookSettings.FailingPoliciesWebhook.DestinationURL)
	assert.Equal(t, 100, savedConfig.WebhookSettings.FailingPoliciesWebhook.HostBatchSize)

	// Test updating existing config
	updatedConfig := &automdm.TeamConfig{
		WebhookSettings: automdm.TeamWebhookSettings{
			FailingPoliciesWebhook: automdm.FailingPoliciesWebhookSettings{
				Enable:         false,
				DestinationURL: "https://updated.com/webhook",
				HostBatchSize:  50,
			},
		},
	}

	err = ds.SaveDefaultTeamConfig(ctx, updatedConfig)
	require.NoError(t, err)

	// Verify the update
	finalConfig, err := ds.DefaultTeamConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, finalConfig)

	assert.False(t, finalConfig.WebhookSettings.FailingPoliciesWebhook.Enable)
	assert.Equal(t, "https://updated.com/webhook", finalConfig.WebhookSettings.FailingPoliciesWebhook.DestinationURL)
	assert.Equal(t, 50, finalConfig.WebhookSettings.FailingPoliciesWebhook.HostBatchSize)
}

func testTeamLitesByIDs(t *testing.T, ds *Datastore) {
	ctx := t.Context()
	teamA, err := ds.NewTeam(ctx, &automdm.Team{Name: "lites-a"})
	require.NoError(t, err)
	teamB, err := ds.NewTeam(ctx, &automdm.Team{
		Name: "lites-b",
		Config: automdm.TeamConfig{WebhookSettings: automdm.TeamWebhookSettings{
			HostActivitiesWebhook: &automdm.HostActivitiesWebhookSettings{Enable: true, DestinationURL: "https://example.com/hook"},
		}},
	})
	require.NoError(t, err)

	lites, err := ds.TeamLitesByIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, lites)

	lites, err = ds.TeamLitesByIDs(ctx, []uint{teamA.ID, teamB.ID, teamB.ID + 1000, 0})
	require.NoError(t, err)
	require.Len(t, lites, 3)
	byID := make(map[uint]*automdm.TeamLite, len(lites))
	for _, l := range lites {
		byID[l.ID] = l
	}
	liteA, liteB := byID[teamA.ID], byID[teamB.ID]
	require.NotNil(t, liteA)
	require.NotNil(t, liteB)
	require.Equal(t, "lites-a", liteA.Name)
	noTeam := byID[0]
	require.NotNil(t, noTeam)
	require.Equal(t, automdm.DisplayNameNoTeam, noTeam.Name)
	webhook := liteB.Config.WebhookSettings.HostActivitiesWebhook
	require.NotNil(t, webhook)
	require.Equal(t, "https://example.com/hook", webhook.DestinationURL)
}

func testDeleteTeamCancelsSoftwareInstalls(t *testing.T, ds *Datastore) {
	ctx := t.Context()
	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)
	team, err := ds.NewTeam(ctx, &automdm.Team{Name: "team1"})
	require.NoError(t, err)

	newInstaller := func(name string, teamID *uint) uint {
		tfr, err := automdm.NewTempFileReader(strings.NewReader(name), t.TempDir)
		require.NoError(t, err)
		id, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &automdm.UploadSoftwareInstallerPayload{
			InstallScript:   "install",
			UninstallScript: "uninstall",
			InstallerFile:   tfr,
			StorageID:       name,
			Filename:        name,
			Title:           name,
			Source:          "apps",
			Platform:        "darwin",
			TeamID:          teamID,
			UserID:          user.ID,
			ValidatedLabels: &automdm.LabelIdentsWithScope{},
		})
		require.NoError(t, err)
		return id
	}
	installerID := newInstaller("ins1", &team.ID)

	hostA := test.NewHost(t, ds, "hostA", "1", "hostAkey", "hostAuuid", time.Now(), test.WithTeamID(team.ID))
	hostB := test.NewHost(t, ds, "hostB", "2", "hostBkey", "hostBuuid", time.Now(), test.WithTeamID(team.ID))
	hostC := test.NewHost(t, ds, "hostC", "3", "hostCkey", "hostCuuid", time.Now(), test.WithTeamID(team.ID))

	// host A: an in-flight install, a queued install, then a script
	inFlightA, err := ds.InsertSoftwareInstallRequest(ctx, hostA.ID, installerID, automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	queuedA, err := ds.InsertSoftwareInstallRequest(ctx, hostA.ID, installerID, automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	scriptA, err := ds.NewHostScriptExecutionRequest(ctx, &automdm.HostScriptRequestPayload{
		HostID: hostA.ID, ScriptContents: "echo", UserID: &user.ID, SyncRequest: true,
	})
	require.NoError(t, err)
	// host B: an in-flight uninstall
	uninstallB := uuid.NewString()
	require.NoError(t, ds.InsertSoftwareUninstallRequest(ctx, uninstallB, hostB.ID, installerID, false))
	// host C: an in-flight setup experience install
	setupC, err := ds.InsertSoftwareInstallRequest(ctx, hostC.ID, installerID, automdm.HostSoftwareInstallOptions{ForSetupExperience: true})
	require.NoError(t, err)
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `INSERT INTO setup_experience_status_results
			(host_uuid, name, status, software_installer_id, host_software_installs_execution_id)
			VALUES (?, 'ins1', 'running', ?, ?)`, hostC.UUID, installerID, setupC)
		return err
	})

	checkUpcomingActivities(t, ds, hostA, inFlightA, queuedA, scriptA.ExecutionID)
	checkUpcomingActivities(t, ds, hostB, uninstallB)
	checkUpcomingActivities(t, ds, hostC, setupC)

	require.NoError(t, ds.DeleteTeam(ctx, team.ID))

	checkUpcomingActivities(t, ds, hostA, scriptA.ExecutionID)
	checkUpcomingActivities(t, ds, hostB)
	checkUpcomingActivities(t, ds, hostC)

	var rows []struct {
		ExecutionID string `db:"execution_id"`
		Status      string `db:"status"`
	}
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		return sqlx.SelectContext(ctx, q, &rows, `SELECT execution_id, status FROM host_software_installs`)
	})
	statuses := map[string]string{}
	for _, r := range rows {
		statuses[r.ExecutionID] = r.Status
	}
	require.Equal(t, map[string]string{
		inFlightA:  "canceled_install",
		uninstallB: "canceled_uninstall",
		setupC:     "canceled_install",
	}, statuses)

	for _, h := range []*automdm.Host{hostA, hostB, hostC} {
		got, err := ds.Host(ctx, h.ID)
		require.NoError(t, err)
		require.Nil(t, got.TeamID)
	}

	// The FK blocks deleting an installer that a queued install still references.
	globalInstallerID := newInstaller("ins2", nil)
	_, err = ds.InsertSoftwareInstallRequest(ctx, hostA.ID, globalInstallerID, automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `DELETE FROM software_installers WHERE id = ?`, globalInstallerID)
		require.True(t, isMySQLForeignKey(err), "got %v", err)
		return nil
	})
}
