package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/platform/mysql/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWhenCreatingNewLabelsPlatformIsValidated(t *testing.T) {
	t.Parallel()

	ds := new(mock.Store)
	mockDuplicateStringGroupsExactMatch(ds)
	user := &automdm.User{
		ID:         3,
		Email:      "foo@bar.com",
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	// Stubs
	expectedNewLabel := automdm.Label{Name: "newly created label"}
	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		return &expectedNewLabel, nil
	}
	ds.HostIDsByLabelIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		return nil, nil
	}
	ds.UpdateLabelMembershipByHostIDsFunc = func(ctx context.Context, label automdm.Label, hostIds []uint, teamFilter automdm.TeamFilter) (*automdm.Label, []uint, error) {
		return nil, nil, nil
	}
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{}, nil
	}
	ds.SetAsideLabelsFunc = func(ctx context.Context, notOnTeamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
		return nil
	}
	ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorId *uint) error {
		return nil
	}

	testCases := []struct {
		Name       string
		Platforms  []string
		ShouldFail bool
	}{
		{
			Name:      "valid platforms",
			Platforms: slices.Collect(maps.Keys(automdm.ValidLabelPlatformVariants)),
		},
		{
			Name:      "empty platforms",
			Platforms: []string{""},
		},
		{
			Name:       "invalid platforms",
			Platforms:  []string{"biscuits_with_gravy"},
			ShouldFail: true,
		},
	}

	name := t.Name()
	description := "bar"
	query := "select * from foo;"
	labelType := automdm.LabelTypeRegular
	labelMembershipType := automdm.LabelMembershipTypeDynamic

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			for _, platform := range tc.Platforms {
				actualNewLabel, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
					Name:        name,
					Query:       query,
					Description: description,
					Platform:    platform,
				})
				if tc.ShouldFail {
					require.Contains(t, err.Error(), fmt.Sprintf("invalid platform: %q", platform))
				} else {
					require.NoError(t, err)
					require.NotNil(t, actualNewLabel)
				}

				err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{{
					Name:                name,
					Description:         description,
					Query:               query,
					LabelType:           labelType,
					LabelMembershipType: labelMembershipType,
					Platform:            platform,
				}}, nil, nil)
				if tc.ShouldFail {
					require.Contains(t, err.Error(), fmt.Sprintf("invalid platform: %q", platform))
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestLabelsAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		lbl.ID = 1
		if lbl.Name == "Other label" {
			lbl.ID = 2
		}
		return lbl, nil
	}
	ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{Label: *lbl}, nil, nil
	}
	ds.DeleteLabelFunc = func(ctx context.Context, id uint) error {
		return nil
	}
	ds.ApplyLabelSpecsFunc = func(ctx context.Context, specs []*automdm.LabelSpec) error {
		return nil
	}

	team1ID := uint(1)
	team2ID := uint(2)

	team1LabelID := uint(3)
	team2LabelID := uint(4)

	team1Label := automdm.Label{ID: team1LabelID, Name: "team1-label", TeamID: &team1ID}
	team2Label := automdm.Label{ID: team2LabelID, Name: "team2-label", TeamID: &team2ID}

	// Update LabelFunc to handle team labels
	ds.LabelFunc = func(ctx context.Context, id uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		switch id {
		case uint(1):
			return &automdm.LabelWithTeamName{Label: automdm.Label{ID: id, AuthorID: &filter.User.ID}}, nil, nil
		case uint(2):
			return &automdm.LabelWithTeamName{Label: automdm.Label{ID: id}}, nil, nil
		case team1LabelID: // team1 label
			return &automdm.LabelWithTeamName{Label: team1Label}, nil, nil
		case team2LabelID: // team2 label
			return &automdm.LabelWithTeamName{Label: team2Label}, nil, nil
		}
		return nil, nil, ctxerr.Wrap(ctx, &notFoundErr{Msg: "label"})
	}

	ds.ListLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, opts automdm.ListOptions, includeHostCounts bool) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LabelsSummaryFunc = func(ctx context.Context, filter automdm.TeamFilter) ([]*automdm.LabelSummary, error) {
		return nil, nil
	}
	ds.ListHostsInLabelFunc = func(ctx context.Context, filter automdm.TeamFilter, lid uint, opts automdm.HostListOptions) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.GetLabelSpecsFunc = func(ctx context.Context, filter automdm.TeamFilter) ([]*automdm.LabelSpec, error) {
		return nil, nil
	}
	ds.GetLabelSpecFunc = func(ctx context.Context, filter automdm.TeamFilter, name string) (*automdm.LabelSpec, error) {
		return &automdm.LabelSpec{}, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: fmt.Sprintf("team-%d", tid)}, nil
	}

	testCases := []struct {
		name                          string
		user                          *automdm.User
		shouldFailGlobalWrite         bool
		shouldFailGlobalRead          bool
		shouldFailGlobalWriteIfAuthor bool
		shouldFailTeam1Write          bool
		shouldFailTeam1Read           bool
		shouldFailTeam2Write          bool
		shouldFailTeam2Read           bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
			false,
			false,
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
			false,
			false,
			false,
			false,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			false,
			true,
			true,
			false,
			true,
			false,
		},
		{
			"team 1 maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
			false,
			false,
			false,
			false,
			true,
			true,
		},
		{
			"team 1 observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			false,
			true,
			true,
			false,
			true,
			true,
		},
	}

	// add a new label authored by no one so we can check writes for labels that aren't authored by the user
	otherLabel, _, err := svc.NewLabel(viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: ptr.String(automdm.RoleMaintainer)}}), automdm.LabelPayload{Name: "Other label", Query: "SELECT 0"})
	require.NoError(t, err)

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			myLabel, _, err := svc.NewLabel(ctx, automdm.LabelPayload{Name: t.Name(), Query: `SELECT 1`})
			checkAuthErr(t, tt.shouldFailGlobalWriteIfAuthor, err) // team write users can still create global labels

			if myLabel != nil {
				_, _, err = svc.ModifyLabel(ctx, myLabel.ID, automdm.ModifyLabelPayload{})
				checkAuthErr(t, tt.shouldFailGlobalWriteIfAuthor, err)
			}

			if myLabel != nil {
				err = svc.DeleteLabelByID(ctx, myLabel.ID)
				checkAuthErr(t, tt.shouldFailGlobalWriteIfAuthor, err)
			}

			for _, tc := range []struct {
				label           automdm.Label
				shouldFailWrite bool
				shouldFailRead  bool
			}{
				{*otherLabel, tt.shouldFailGlobalWrite, tt.shouldFailGlobalRead},
				{team1Label, tt.shouldFailTeam1Write, tt.shouldFailTeam1Read},
				{team2Label, tt.shouldFailTeam2Write, tt.shouldFailTeam2Read},
			} {
				t.Run(tc.label.Name, func(t *testing.T) {
					ctx2 := ctx
					if tc.label.TeamID != nil {
						// license check is after auth check
						if !tc.shouldFailRead {
							_, err = svc.GetLabelSpecs(ctx2, tc.label.TeamID)
							require.ErrorIs(t, err, automdm.ErrMissingLicense)

							_, err = svc.GetLabelSpec(ctx2, "abc", tc.label.TeamID)
							require.ErrorIs(t, err, automdm.ErrMissingLicense)

							_, err = svc.ListLabels(ctx2, automdm.ListOptions{}, tc.label.TeamID, true)
							require.ErrorIs(t, err, automdm.ErrMissingLicense)

							_, err = svc.LabelsSummary(ctx2, tc.label.TeamID)
							require.ErrorIs(t, err, automdm.ErrMissingLicense)
						}
						if !tc.shouldFailWrite {
							require.ErrorIs(t, svc.ApplyLabelSpecs(ctx2, []*automdm.LabelSpec{}, tc.label.TeamID, nil), automdm.ErrMissingLicense)

							// We'll let global admins clean up team labels if they downgraded to Free
							require.NoError(t, svc.DeleteLabelByID(ctx2, tc.label.ID))
						}

						ctx2 = license.NewContext(ctx2, &automdm.LicenseInfo{Tier: automdm.TierPremium})
					}

					err = svc.ApplyLabelSpecs(ctx2, []*automdm.LabelSpec{}, tc.label.TeamID, nil)
					checkAuthErr(t, tc.shouldFailWrite, err)

					_, err = svc.GetLabelSpecs(ctx2, tc.label.TeamID)
					checkAuthErr(t, tc.shouldFailRead, err)

					_, err = svc.GetLabelSpec(ctx2, "abc", tc.label.TeamID)
					checkAuthErr(t, tc.shouldFailRead, err)

					_, err = svc.ListLabels(ctx2, automdm.ListOptions{}, tc.label.TeamID, true)
					checkAuthErr(t, tc.shouldFailRead, err)

					_, err = svc.LabelsSummary(ctx2, tc.label.TeamID)
					checkAuthErr(t, tc.shouldFailRead, err)

					err = svc.DeleteLabelByID(ctx2, tc.label.ID)
					checkAuthErr(t, tc.shouldFailWrite, err)
				})
			}

			// filtering is done in the data store for these; NOT enforcing premium license checks here
			_, err = svc.GetLabelSpec(ctx, "abc", nil)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, err = svc.ListHostsInLabel(ctx, 1, automdm.HostListOptions{})
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, _, err = svc.GetLabel(ctx, 1)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, _, err = svc.ModifyLabel(ctx, otherLabel.ID, automdm.ModifyLabelPayload{})
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)

			// global label listing should work if you can read global labels
			_, err = svc.GetLabelSpecs(ctx, ptr.Uint(0))
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, err = svc.ListLabels(ctx, automdm.ListOptions{}, ptr.Uint(0), true)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
		})
	}
}

func TestListLabelsHostCountOptions(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	ds.ListLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, opts automdm.ListOptions, includeHostCounts bool) ([]*automdm.Label, error) {
		// Expect host counts not to be requested
		require.False(t, includeHostCounts)
		return nil, nil
	}

	// Test explicitly setting include_host_counts to false
	_, err := svc.ListLabels(ctx, automdm.ListOptions{}, nil, false)
	require.NoError(t, err)

	ds.ListLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, opts automdm.ListOptions, includeHostCounts bool) ([]*automdm.Label, error) {
		// Expect host counts to be requested
		require.True(t, includeHostCounts)
		// Expect the team filter to be set
		require.Equal(t, filter.User, user)
		return nil, nil
	}

	// Test explicitly setting include_host_counts to true
	_, err = svc.ListLabels(ctx, automdm.ListOptions{}, nil, true)
	require.NoError(t, err)
}

func TestLabelsWithDS(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)

	cases := []struct {
		name string
		fn   func(t *testing.T, ds *mysql.Datastore)
	}{
		{"GetLabel", testLabelsGetLabel},
		{"ListLabels", testLabelsListLabels},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer mysqltest.TruncateTables(t, ds)
			c.fn(t, ds)
		})
	}
}

func testLabelsGetLabel(t *testing.T, ds *mysql.Datastore) {
	svc, ctx := newTestService(t, ds, nil, nil)

	label := &automdm.Label{
		Name:  "foo",
		Query: "select * from foo;",
	}
	label, err := ds.NewLabel(ctx, label)
	assert.Nil(t, err)
	assert.NotZero(t, label.ID)

	labelVerify, _, err := svc.GetLabel(test.UserContext(ctx, test.UserAdmin), label.ID)
	assert.Nil(t, err)
	assert.Equal(t, label.ID, labelVerify.ID)
	assert.Nil(t, label.AuthorID)
}

func testLabelsListLabels(t *testing.T, ds *mysql.Datastore) {
	svc, ctx := newTestService(t, ds, nil, nil)
	test.AddBuiltinLabels(t, ds)

	labels, err := svc.ListLabels(test.UserContext(ctx, test.UserAdmin), automdm.ListOptions{Page: 0, PerPage: 1000}, nil, true)
	require.NoError(t, err)
	require.Len(t, labels, 13)

	labelsSummary, err := svc.LabelsSummary(test.UserContext(ctx, test.UserAdmin), nil)
	require.NoError(t, err)
	require.Len(t, labelsSummary, 13)
}

func TestApplyLabelSpecsWithBuiltInLabels(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	user := &automdm.User{
		ID:         3,
		Email:      "foo@bar.com",
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})
	name := "foo"
	description := "bar"
	query := "select * from foo;"
	platform := ""
	labelType := automdm.LabelTypeBuiltIn
	labelMembershipType := automdm.LabelMembershipTypeDynamic
	spec := &automdm.LabelSpec{
		Name:                name,
		Description:         description,
		Query:               query,
		LabelType:           labelType,
		LabelMembershipType: labelMembershipType,
	}

	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{
			name: {
				Name:                name,
				Description:         description,
				Query:               query,
				Platform:            platform,
				LabelType:           labelType,
				LabelMembershipType: labelMembershipType,
			},
		}, nil
	}

	// all good
	err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	require.NoError(t, err)

	// trying to add a regular label with the same name as a built-in label should fail
	for name := range automdm.ReservedLabelNames() {
		err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{
				Name:        name,
				Description: description,
				Query:       query,
				LabelType:   automdm.LabelTypeRegular,
			},
		}, nil, nil)
		assert.ErrorContains(t, err,
			fmt.Sprintf("cannot add label '%s' because it conflicts with the name of a built-in label", name))
	}

	// case-variant names must also be rejected: labels.name has a case-insensitive
	// collation, so the upsert would overwrite the built-in label row.
	for _, variant := range []string{"all hosts", "ALL HOSTS", "All hosts", "fedora linux", "MS WINDOWS"} {
		err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{
				Name:      variant,
				Query:     "select 1;",
				LabelType: automdm.LabelTypeRegular,
			},
		}, nil, nil)
		require.ErrorContains(t, err, "conflicts with the name of a built-in label", "case variant %q should be rejected", variant)
	}

	const errorMessage = "cannot modify or add built-in label"
	// not ok -- built-in label name doesn't exist
	name = "not-foo"
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorContains(t, err, errorMessage)
	name = "foo"

	// not ok -- description does not match
	description = "not-bar"
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorContains(t, err, errorMessage)
	description = "bar"

	// not ok -- query does not match
	query = "select * from not-foo;"
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorContains(t, err, errorMessage)
	query = "select * from foo;"

	// not ok -- label type does not match
	labelType = automdm.LabelTypeRegular
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorContains(t, err, errorMessage)
	labelType = automdm.LabelTypeBuiltIn

	// not ok -- label membership type does not match
	labelMembershipType = automdm.LabelMembershipTypeManual
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorContains(t, err, errorMessage)
	labelMembershipType = automdm.LabelMembershipTypeDynamic

	// not ok -- DB error
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return nil, assert.AnError
	}
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{spec}, nil, nil)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestBuiltInLabelNameCaseVariants(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		lbl.ID = 1
		return lbl, nil
	}
	ds.LabelFunc = func(ctx context.Context, lid uint, teamFilter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{
			Label: automdm.Label{ID: lid, Name: "a regular label", Query: "select 1;", LabelType: automdm.LabelTypeRegular},
		}, nil, nil
	}
	ds.DeleteLabelFunc = func(ctx context.Context, id uint) error {
		return nil
	}
	ds.SaveLabelFunc = func(ctx context.Context, label *automdm.Label, hostIDs []uint, teamFilter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{Label: *label}, nil, nil
	}

	for _, variant := range []string{"all hosts", "ALL HOSTS", "aLl HoStS"} {
		t.Run(variant, func(t *testing.T) {
			_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{Name: variant, Query: "select 1;"})
			require.ErrorContains(t, err, "cannot add label 'All Hosts' because it conflicts with the name of a built-in label")
			require.False(t, ds.NewLabelFuncInvoked)

			_, _, err = svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{Name: &variant})
			require.ErrorContains(t, err, "cannot rename label to 'All Hosts' because it conflicts with the name of a built-in label")
			require.False(t, ds.SaveLabelFuncInvoked)

		})
	}
}

func TestApplyLabelSpecsCustomHostVitalCriteria(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	specWithCriteria := func(criteria automdm.HostVitalCriteria) *automdm.LabelSpec {
		marshalled, err := json.Marshal(&criteria)
		require.NoError(t, err)
		raw := json.RawMessage(marshalled)
		return &automdm.LabelSpec{
			Name:                "custom-vital-spec",
			LabelType:           automdm.LabelTypeRegular,
			LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
			HostVitalsCriteria:  &raw,
		}
	}

	// A custom_host_vital criterion without an id fails structural validation
	// before any datastore call.
	err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{specWithCriteria(automdm.HostVitalCriteria{
		Vital: new("custom_host_vital"),
		Value: new("Engineering"),
	})}, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom_host_vital_id")

	// A criterion referencing a non-existent custom vital is rejected.
	ds.GetCustomHostVitalsFunc = func(ctx context.Context, ids []uint) ([]automdm.CustomHostVital, error) {
		return nil, nil
	}
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{specWithCriteria(automdm.HostVitalCriteria{
		Vital:             new("custom_host_vital"),
		Value:             new("Engineering"),
		CustomHostVitalID: new(uint(999)),
	})}, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
	require.True(t, ds.GetCustomHostVitalsFuncInvoked)
}

func TestLabelsWithReplica(t *testing.T) {
	opts := &testing_utils.DatastoreTestOptions{DummyReplica: true}
	ds := mysqltest.CreateMySQLDSWithOptions(t, opts)
	defer ds.Close()

	svc, ctx := newTestService(t, ds, nil, nil)
	user, err := ds.NewUser(ctx, &automdm.User{
		Name:       "Adminboi",
		Password:   []byte("p4ssw0rd.123"),
		Email:      "admin@example.com",
		GlobalRole: ptr.String(automdm.RoleAdmin),
	})
	require.NoError(t, err)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	// create a couple hosts
	h1, err := ds.NewHost(ctx, &automdm.Host{
		Hostname:        "host1",
		HardwareSerial:  uuid.NewString(),
		UUID:            uuid.NewString(),
		Platform:        "darwin",
		LastEnrolledAt:  time.Now(),
		DetailUpdatedAt: time.Now(),
	})
	require.NoError(t, err)
	h2, err := ds.NewHost(ctx, &automdm.Host{
		Hostname:        "host2",
		HardwareSerial:  uuid.NewString(),
		UUID:            uuid.NewString(),
		Platform:        "darwin",
		LastEnrolledAt:  time.Now(),
		DetailUpdatedAt: time.Now(),
	})
	require.NoError(t, err)
	// make the newly-created hosts available to the reader
	opts.RunReplication()

	lbl, hostIDs, err := svc.NewLabel(ctx, automdm.LabelPayload{Name: "label1", Hosts: []string{h1.HardwareSerial, h2.UUID}})
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{h1.ID, h2.ID}, hostIDs)
	require.Equal(t, 2, lbl.HostCount)
	require.Equal(t, user.ID, *lbl.AuthorID)

	// make the newly-created label available to the reader
	opts.RunReplication("labels", "label_membership")

	lblWithName, hostIDs, err := svc.ModifyLabel(ctx, lbl.ID, automdm.ModifyLabelPayload{Hosts: []string{h1.HardwareSerial}})
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{h1.ID}, hostIDs)
	require.Equal(t, 1, lblWithName.HostCount)
	require.Equal(t, user.ID, *lblWithName.AuthorID)

	// reading this label without replication returns the old data as it only uses the reader
	lblWithName, hostIDs, err = svc.GetLabel(ctx, lblWithName.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{h1.ID, h2.ID}, hostIDs)
	require.Equal(t, 2, lblWithName.HostCount)
	require.Equal(t, user.ID, *lblWithName.AuthorID)

	// running the replication makes the updated data available
	opts.RunReplication("labels", "label_membership")

	lblWithName, hostIDs, err = svc.GetLabel(ctx, lblWithName.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{h1.ID}, hostIDs)
	require.Equal(t, 1, lblWithName.HostCount)
	require.Equal(t, user.ID, *lblWithName.AuthorID)
}

func TestLabelCrossTeamHostMembership(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()

	svc, ctx := newTestService(t, ds, nil, nil)

	// A global admin used only to inspect the resulting label membership.
	adminUser, err := ds.NewUser(ctx, &automdm.User{
		Name:       "Admin",
		Password:   []byte("p4ssw0rd.123"),
		Email:      "admin@example.com",
		GlobalRole: new(automdm.RoleAdmin),
	})
	require.NoError(t, err)
	adminFilter := automdm.TeamFilter{User: adminUser}

	team1, err := ds.NewTeam(ctx, &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(ctx, &automdm.Team{Name: "team2"})
	require.NoError(t, err)

	// A user who can only write to team1 (team admins/maintainers are allowed
	// to create global labels per the authorization policy).
	team1User, err := ds.NewUser(ctx, &automdm.User{
		Name:     "Team1 Maintainer",
		Password: []byte("p4ssw0rd.123"),
		Email:    "team1@example.com",
		Teams: []automdm.UserTeam{
			{Team: automdm.Team{ID: team1.ID}, Role: automdm.RoleMaintainer},
		},
	})
	require.NoError(t, err)
	team1Ctx := viewer.NewContext(ctx, viewer.Viewer{User: team1User})

	// A host on team1 (team1User can write to it) and a host on team2 (it can't).
	team1Host, err := ds.NewHost(ctx, &automdm.Host{
		Hostname:        "team1-host",
		HardwareSerial:  uuid.NewString(),
		UUID:            uuid.NewString(),
		Platform:        "darwin",
		LastEnrolledAt:  time.Now(),
		DetailUpdatedAt: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team1.ID, []uint{team1Host.ID})))

	team2Host, err := ds.NewHost(ctx, &automdm.Host{
		Hostname:        "team2-host",
		HardwareSerial:  uuid.NewString(),
		UUID:            uuid.NewString(),
		Platform:        "darwin",
		LastEnrolledAt:  time.Now(),
		DetailUpdatedAt: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team2.ID, []uint{team2Host.ID})))

	t.Run("NewLabel rejects cross-team host_ids", func(t *testing.T) {
		_, _, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{
			Name:    "cross-team-create",
			HostIDs: []uint{team2Host.ID},
		})
		// team1User has no write access to the team2 host, so this must fail
		// authorization rather than silently attaching the host.
		checkAuthErr(t, true, err)
	})

	t.Run("ModifyLabel rejects cross-team host_ids", func(t *testing.T) {
		// Create an empty manual global label as the team1 user.
		lbl, _, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{Name: "cross-team-modify"})
		require.NoError(t, err)

		_, _, err = svc.ModifyLabel(team1Ctx, lbl.ID, automdm.ModifyLabelPayload{
			HostIDs: []uint{team2Host.ID},
		})
		checkAuthErr(t, true, err)

		// The membership must be unchanged (empty).
		hosts, err := ds.ListHostsInLabel(ctx, adminFilter, lbl.ID, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Empty(t, hosts, "team1 user must not attach a team2 host via raw host_ids")
	})

	t.Run("read-only access to the target team is rejected", func(t *testing.T) {
		// A user who can write to team1 but only has read-only (observer_plus)
		// access to team2 must not be able to attach team2 hosts: membership is
		// a write operation and requires write authorization on the host.
		readOnlyUser, err := ds.NewUser(ctx, &automdm.User{
			Name:     "Team1 Maintainer, Team2 Observer+",
			Password: []byte("p4ssw0rd.123"),
			Email:    "team1team2@example.com",
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: team1.ID}, Role: automdm.RoleMaintainer},
				{Team: automdm.Team{ID: team2.ID}, Role: automdm.RoleObserverPlus},
			},
		})
		require.NoError(t, err)
		readOnlyCtx := viewer.NewContext(ctx, viewer.Viewer{User: readOnlyUser})

		_, _, err = svc.NewLabel(readOnlyCtx, automdm.LabelPayload{
			Name:    "read-only-target-team",
			HostIDs: []uint{team2Host.ID},
		})
		checkAuthErr(t, true, err)
	})

	t.Run("read-only access to the target team is rejected by host identifier", func(t *testing.T) {
		// The by-identifier path resolves hosts the caller can see, so an
		// observer_plus on team2 resolves the team2 host and must then be
		// rejected by the write-authorization check (unlike a user with no
		// access to team2, for whom the identifier resolves to nothing).
		readOnlyUser, err := ds.NewUser(ctx, &automdm.User{
			Name:     "Team1 Maintainer, Team2 Observer+ (by identifier)",
			Password: []byte("p4ssw0rd.123"),
			Email:    "team1team2-byname@example.com",
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: team1.ID}, Role: automdm.RoleMaintainer},
				{Team: automdm.Team{ID: team2.ID}, Role: automdm.RoleObserverPlus},
			},
		})
		require.NoError(t, err)
		readOnlyCtx := viewer.NewContext(ctx, viewer.Viewer{User: readOnlyUser})

		_, _, err = svc.NewLabel(readOnlyCtx, automdm.LabelPayload{
			Name:  "read-only-target-team-by-identifier",
			Hosts: []string{team2Host.HardwareSerial},
		})
		checkAuthErr(t, true, err)
	})

	t.Run("a mix of in-scope and out-of-scope hosts is rejected atomically", func(t *testing.T) {
		// Create a manual label with an in-scope team1 host, then attempt to
		// add a second team1 host together with the out-of-scope team2 host.
		// The request must be rejected and leave membership unchanged, so the
		// in-scope host is not partially applied.
		team1Host2, err := ds.NewHost(ctx, &automdm.Host{
			Hostname:        "team1-host-2",
			HardwareSerial:  uuid.NewString(),
			UUID:            uuid.NewString(),
			Platform:        "darwin",
			LastEnrolledAt:  time.Now(),
			DetailUpdatedAt: time.Now(),
		})
		require.NoError(t, err)
		require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team1.ID, []uint{team1Host2.ID})))

		lbl, _, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{
			Name:    "atomic-mix",
			HostIDs: []uint{team1Host.ID},
		})
		require.NoError(t, err)

		_, _, err = svc.ModifyLabel(team1Ctx, lbl.ID, automdm.ModifyLabelPayload{
			HostIDs: []uint{team1Host2.ID, team2Host.ID},
		})
		checkAuthErr(t, true, err)

		// Membership must be unchanged: still only the original team1 host.
		hosts, err := ds.ListHostsInLabel(ctx, adminFilter, lbl.ID, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Len(t, hosts, 1)
		require.Equal(t, team1Host.ID, hosts[0].ID)
	})

	t.Run("non-existent host_ids are rejected", func(t *testing.T) {
		// A host ID that does not exist must not silently bypass the
		// authorization check; the request is rejected as invalid.
		_, _, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{
			Name:    "missing-host",
			HostIDs: []uint{999999},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "does not exist")
	})

	t.Run("hosts the caller can write to are still allowed", func(t *testing.T) {
		// Sanity check that the write-authorization check doesn't over-restrict:
		// team1User can attach a team1 host to a global label they create, both
		// by ID and by identifier, and can remove all hosts.
		lbl, hostIDs, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{
			Name:    "in-scope-create",
			HostIDs: []uint{team1Host.ID},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, []uint{team1Host.ID}, hostIDs)

		hosts, err := ds.ListHostsInLabel(ctx, adminFilter, lbl.ID, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Len(t, hosts, 1)
		require.Equal(t, team1Host.ID, hosts[0].ID)

		// Modifying by identifier with an in-scope host is allowed.
		_, hostIDs, err = svc.ModifyLabel(team1Ctx, lbl.ID, automdm.ModifyLabelPayload{
			Hosts: []string{team1Host.UUID},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, []uint{team1Host.ID}, hostIDs)

		// Removing all hosts (empty list) is allowed and needs no host authz.
		_, hostIDs, err = svc.ModifyLabel(team1Ctx, lbl.ID, automdm.ModifyLabelPayload{
			Hosts: []string{},
		})
		require.NoError(t, err)
		require.Empty(t, hostIDs)
	})

	t.Run("hosts that match no identifier replace membership with an empty set", func(t *testing.T) {
		lbl, _, err := svc.NewLabel(team1Ctx, automdm.LabelPayload{
			Name:    "unmatched-identifiers",
			HostIDs: []uint{team1Host.ID},
		})
		require.NoError(t, err)

		// Hostname is no longer a label identifier, so this resolves to no hosts.
		_, hostIDs, err := svc.ModifyLabel(team1Ctx, lbl.ID, automdm.ModifyLabelPayload{
			Hosts: []string{team1Host.Hostname},
		})
		require.NoError(t, err)
		require.Empty(t, hostIDs)

		hosts, err := ds.ListHostsInLabel(ctx, adminFilter, lbl.ID, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Empty(t, hosts)
	})
}

func TestBatchValidateLabels(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	t.Run("no auth context", func(t *testing.T) {
		_, err := svc.BatchValidateLabels(context.Background(), nil, nil)
		require.ErrorContains(t, err, "Authentication required")
	})

	authCtx := authz_ctx.AuthorizationContext{}
	ctx = authz_ctx.NewContext(ctx, &authCtx)

	t.Run("no auth checked", func(t *testing.T) {
		_, err := svc.BatchValidateLabels(ctx, nil, nil)
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

	mockLabelIdent := func(name string, id uint) automdm.LabelIdent {
		return automdm.LabelIdent{LabelID: id, LabelName: name}
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
		name         string
		labelNames   []string
		expectLabels map[string]automdm.LabelIdent
		expectError  string
	}{
		{
			"no labels",
			nil,
			nil,
			"",
		},
		{
			"include labels",
			[]string{"foo", "bar"},
			map[string]automdm.LabelIdent{
				"foo": mockLabelIdent("foo", 1),
				"bar": mockLabelIdent("bar", 2),
			},
			"",
		},
		{
			"non-existent label",
			[]string{"foo", "qux"},
			nil,
			"some or all the labels provided don't exist",
		},
		{
			"duplicate label",
			[]string{"foo", "foo"},
			map[string]automdm.LabelIdent{
				"foo": mockLabelIdent("foo", 1),
			},
			"",
		},
		{
			"empty slice",
			[]string{},
			nil,
			"",
		},
		{
			"empty string",
			[]string{""},
			nil,
			"some or all the labels provided don't exist",
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.BatchValidateLabels(ctx, nil, tt.labelNames)
			if tt.expectError != "" {
				require.Contains(t, err.Error(), tt.expectError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectLabels, got)
			}
		})
	}

	t.Run("name shared by several teams resolves to the entity's team", func(t *testing.T) {
		const teamID = uint(5)
		ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
			require.NotNil(t, filter.TeamID)
			require.Equal(t, teamID, *filter.TeamID)
			return map[string][]uint{"shared": {20}}, nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			require.NotNil(t, filter.TeamID)
			require.Equal(t, teamID, *filter.TeamID)
			return map[string]*automdm.Label{"shared": {ID: 20, Name: "shared", TeamID: new(teamID)}}, nil
		}

		got, err := svc.BatchValidateLabels(ctx, new(teamID), []string{"shared"})
		require.NoError(t, err)
		require.Equal(t, map[string]automdm.LabelIdent{"shared": mockLabelIdent("shared", 20)}, got)
	})

	t.Run("a name only on another team doesn't exist for the entity", func(t *testing.T) {
		const teamID = uint(5)
		ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
			require.NotNil(t, filter.TeamID)
			require.Equal(t, teamID, *filter.TeamID)
			return map[string][]uint{}, nil
		}

		_, err := svc.BatchValidateLabels(ctx, new(teamID), []string{"other-team-label"})
		var missing *automdm.MissingLabelError
		require.ErrorAs(t, err, &missing)
		require.Equal(t, "other-team-label", missing.MissingLabelName)
	})
}

func TestDeleteLabelWithSharedNames(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	const teamID = uint(3)
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team"}, nil
	}
	var deletedID uint
	ds.DeleteLabelFunc = func(ctx context.Context, id uint) error {
		deletedID = id
		return nil
	}

	t.Run("by ID deletes the requested label, not a label found by its name", func(t *testing.T) {
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{ID: lid, Name: "shared", TeamID: new(teamID)}, nil, nil
		}
		require.NoError(t, svc.DeleteLabelByID(ctx, 2))
		require.Equal(t, uint(2), deletedID)
	})
}

func TestTranslateSharedLabelName(t *testing.T) {
	ds := new(mock.Store)
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		switch names[0] {
		case "shared":
			return map[string][]uint{"shared": {1, 2}}, nil
		case "unique":
			return map[string][]uint{"unique": {3}}, nil
		case "unique-case":
			// The database matches names case-insensitively, so the stored spelling can differ.
			return map[string][]uint{"Unique-Case": {4}}, nil
		}
		return map[string][]uint{}, nil
	}

	_, err := translateLabelToID(ctx, ds, "shared")
	var conflictErr *automdm.ConflictError
	require.ErrorAs(t, err, &conflictErr)
	id, err := translateLabelToID(ctx, ds, "unique")
	require.NoError(t, err)
	require.Equal(t, uint(3), id)
	id, err = translateLabelToID(ctx, ds, "unique-case")
	require.NoError(t, err)
	require.Equal(t, uint(4), id)
	id, err = translateLabelToID(ctx, ds, "missing")
	require.NoError(t, err)
	require.Zero(t, id)
}

func TestApplyLabelSpecsManualLabelNilHosts(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	mockDuplicateStringGroupsExactMatch(ds)
	user := &automdm.User{
		ID:         3,
		Email:      "foo@bar.com",
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{}, nil
	}
	ds.SetAsideLabelsFunc = func(ctx context.Context, teamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
		return nil
	}
	ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
		return nil
	}

	// Manual label with nil hosts (omitted) should be accepted
	err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "manual_no_hosts",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               nil,
		},
	}, nil, nil)
	require.NoError(t, err)

	// Manual label with empty hosts should also be accepted
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "manual_empty_hosts",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               []string{},
		},
	}, nil, nil)
	require.NoError(t, err)

	// Dynamic label with hosts should still be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "dynamic_with_hosts",
			Query:               "SELECT 1",
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
			Hosts:               []string{"host1"},
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as dynamic but contains hosts")

	// Dynamic label without query should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "dynamic_no_query",
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as dynamic but is missing a query")

	// Dynamic label with criteria should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "dynamic_with_criteria",
			Query:               "SELECT 1",
			LabelMembershipType: automdm.LabelMembershipTypeDynamic,
			HostVitalsCriteria:  new(json.RawMessage(`{"vital":"end_user_idp_group","operator":"=","value":"Engineering"}`)),
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as dynamic but contains criteria")

	// Manual label with query should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "manual_with_query",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Query:               "SELECT 1",
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as manual but contains a query")

	// Manual label with criteria should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "manual_with_criteria",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			HostVitalsCriteria:  new(json.RawMessage(`{"vital":"end_user_idp_group","operator":"=","value":"Engineering"}`)),
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as manual but contains criteria")

	// Manual label with platform should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "manual_with_platform",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Platform:            "darwin",
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as manual but contains a platform")

	// Host_vitals label without criteria should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "host_vitals_no_criteria",
			LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as host_vitals but is missing criteria")

	// Host_vitals label with query should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "host_vitals_with_query",
			LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
			HostVitalsCriteria:  new(json.RawMessage(`{"vital":"end_user_idp_group","operator":"=","value":"Engineering"}`)),
			Query:               "SELECT 1",
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as host_vitals but contains a query")

	// Host_vitals label with platform should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "host_vitals_with_platform",
			LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
			HostVitalsCriteria:  new(json.RawMessage(`{"vital":"end_user_idp_group","operator":"=","value":"Engineering"}`)),
			Platform:            "darwin",
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as host_vitals but contains a platform")

	// Host_vitals label with hosts should be rejected
	err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
		{
			Name:                "host_vitals_with_hosts",
			LabelMembershipType: automdm.LabelMembershipTypeHostVitals,
			HostVitalsCriteria:  new(json.RawMessage(`{"vital":"end_user_idp_group","operator":"=","value":"Engineering"}`)),
			Hosts:               []string{"host1"},
		},
	}, nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as host_vitals but contains hosts")
}

// mockListHostsLiteByIDs sets up ListHostsLiteByIDs to return a lite host for
// each requested ID, so that label membership authorization can run in tests
// that use a mock datastore.
func mockListHostsLiteByIDs(ds *mock.Store) {
	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		hosts := make([]*automdm.Host, 0, len(ids))
		for _, id := range ids {
			hosts = append(hosts, &automdm.Host{ID: id})
		}
		return hosts, nil
	}
}

func TestNewManualLabel(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		lbl.ID = 1
		lbl.LabelMembershipType = automdm.LabelMembershipTypeManual
		return lbl, nil
	}
	ds.HostIDsByLabelIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		return []uint{99, 100}, nil
	}
	mockListHostsLiteByIDs(ds)

	t.Run("using host identifiers", func(t *testing.T) {
		ds.UpdateLabelMembershipByHostIDsFunc = func(ctx context.Context, label automdm.Label, hostIds []uint, teamFilter automdm.TeamFilter) (*automdm.Label, []uint, error) {
			require.Equal(t, uint(1), label.ID)
			require.Equal(t, []uint{99, 100}, hostIds)
			return nil, nil, nil
		}
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name:  "foo",
			Hosts: []string{"serial1", "uuid2"},
		})
		require.NoError(t, err)
	})

	t.Run("using IDs", func(t *testing.T) {
		ds.UpdateLabelMembershipByHostIDsFunc = func(ctx context.Context, label automdm.Label, hostIds []uint, teamFilter automdm.TeamFilter) (*automdm.Label, []uint, error) {
			require.Equal(t, uint(1), label.ID)
			require.Equal(t, []uint{1, 2}, hostIds)
			return nil, nil, nil
		}
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name:    "foo",
			HostIDs: []uint{1, 2},
		})
		require.NoError(t, err)
	})
}

func TestModifyManualLabel(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.LabelFunc = func(ctx context.Context, lid uint, teamFilter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{
			Label: automdm.Label{
				ID:                  lid,
				LabelMembershipType: automdm.LabelMembershipTypeManual,
			},
		}, nil, nil
	}
	ds.HostIDsByLabelIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		return []uint{99, 100}, nil
	}
	mockListHostsLiteByIDs(ds)

	t.Run("using host identifiers", func(t *testing.T) {
		ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			require.Equal(t, uint(1), lbl.ID)
			require.Equal(t, []uint{99, 100}, hostIDs)
			return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
		}
		_, _, err := svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{
			Hosts: []string{"serial1", "uuid2"},
		})
		require.NoError(t, err)
		require.True(t, ds.SaveLabelFuncInvoked)
	})

	t.Run("using IDs", func(t *testing.T) {
		ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			require.Equal(t, uint(1), lbl.ID)
			require.Equal(t, []uint{1, 2}, hostIDs)
			return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
		}
		_, _, err := svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{
			HostIDs: []uint{1, 2},
		})
		require.NoError(t, err)
		require.True(t, ds.SaveLabelFuncInvoked)
	})
}

func TestModifyLabelRejectsHostsForComputedMembership(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	membershipType := automdm.LabelMembershipTypeDynamic
	ds.LabelFunc = func(ctx context.Context, lid uint, teamFilter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{
			Label: automdm.Label{
				ID:                  lid,
				LabelMembershipType: membershipType,
			},
		}, nil, nil
	}
	ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
	}
	ds.HostIDsByLabelIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		return []uint{99}, nil
	}
	mockListHostsLiteByIDs(ds)

	type labelTypeCase struct {
		name           string
		membershipType automdm.LabelMembershipType
	}
	computedTypes := []labelTypeCase{
		{"dynamic", automdm.LabelMembershipTypeDynamic},
		{"host vitals", automdm.LabelMembershipTypeHostVitals},
	}
	manualType := labelTypeCase{"manual", automdm.LabelMembershipTypeManual}
	payloads := []struct {
		name    string
		payload automdm.ModifyLabelPayload
	}{
		{"empty host IDs", automdm.ModifyLabelPayload{HostIDs: []uint{}}},
		{"host IDs", automdm.ModifyLabelPayload{HostIDs: []uint{1}}},
		{"empty hostnames", automdm.ModifyLabelPayload{Hosts: []string{}}},
		{"hostnames", automdm.ModifyLabelPayload{Hosts: []string{"host1"}}},
	}

	for _, lblType := range computedTypes {
		for _, tc := range payloads {
			t.Run(lblType.name+"/"+tc.name, func(t *testing.T) {
				membershipType = lblType.membershipType
				ds.SaveLabelFuncInvoked = false

				_, _, err := svc.ModifyLabel(ctx, 1, tc.payload)
				require.ErrorContains(t, err, `"hosts" or "host_ids" can only be provided for a manual label`)
				require.False(t, ds.SaveLabelFuncInvoked)
			})
		}
	}

	// SaveLabel only replaces membership when it gets a non-nil list, so a request
	// that omits both host fields must reach it as nil.
	for _, lblType := range append(computedTypes, manualType) {
		t.Run(lblType.name+"/rename without host fields", func(t *testing.T) {
			membershipType = lblType.membershipType
			ds.SaveLabelFuncInvoked = false
			ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
				require.Nil(t, hostIDs)
				return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
			}

			_, _, err := svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{Name: new("renamed")})
			require.NoError(t, err)
			require.True(t, ds.SaveLabelFuncInvoked)
		})
	}

	t.Run("manual label can still be cleared", func(t *testing.T) {
		membershipType = automdm.LabelMembershipTypeManual
		ds.SaveLabelFuncInvoked = false
		ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			require.NotNil(t, hostIDs)
			require.Empty(t, hostIDs)
			return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
		}

		_, _, err := svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{HostIDs: []uint{}})
		require.NoError(t, err)
		require.True(t, ds.SaveLabelFuncInvoked)
	})
}

func TestNewHostVitalsLabel(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		return lbl, nil
	}

	t.Run("create host vitals label", func(t *testing.T) {
		lbl, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name: "foo",
			Criteria: &automdm.HostVitalCriteria{
				Vital: ptr.String("end_user_idp_group"),
				Value: ptr.String("admin"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, automdm.LabelTypeRegular, lbl.LabelType)
		assert.Equal(t, automdm.LabelMembershipTypeHostVitals, lbl.LabelMembershipType)

		// Test parsing the criteria
		query, queryValues, err := lbl.CalculateHostVitalsQuery()
		require.NoError(t, err)
		queryValuesJson, err := json.Marshal(queryValues)
		require.NoError(t, err)
		// Compare whitespace-normalized SQL: the IdP join fragment is a multi-line
		// raw string whose indentation is irrelevant to the query's meaning.
		assert.Equal(t,
			"SELECT %s FROM %s JOIN host_scim_user ON (hosts.id = host_scim_user.host_id) JOIN scim_users ON (host_scim_user.scim_user_id = scim_users.id) LEFT JOIN ( WITH RECURSIVE scim_user_group_expanded AS ( SELECT scim_user_id, group_id FROM scim_user_group WHERE scim_user_id IN (SELECT scim_user_id FROM host_scim_user) UNION SELECT e.scim_user_id, gg.parent_group_id AS group_id FROM scim_user_group_expanded e JOIN scim_group_group gg ON gg.child_group_id = e.group_id ) SELECT scim_user_id, group_id FROM scim_user_group_expanded ) scim_user_group ON (host_scim_user.scim_user_id = scim_user_group.scim_user_id) LEFT JOIN scim_groups ON (scim_user_group.group_id = scim_groups.id) WHERE scim_groups.display_name = ? GROUP BY hosts.id",
			strings.Join(strings.Fields(query), " "))
		assert.Equal(t, `["admin"]`, string(queryValuesJson))
	})

	t.Run("create custom host vital label", func(t *testing.T) {
		ds.GetCustomHostVitalsFunc = func(ctx context.Context, ids []uint) ([]automdm.CustomHostVital, error) {
			return []automdm.CustomHostVital{{ID: 7, Name: "Department"}}, nil
		}
		ds.GetCustomHostVitalsFuncInvoked = false

		lbl, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name: "custom-vital-label",
			Criteria: &automdm.HostVitalCriteria{
				Vital:             new("custom_host_vital"),
				Value:             new("Engineering"),
				CustomHostVitalID: new(uint(7)),
			},
		})
		require.NoError(t, err)
		assert.True(t, ds.GetCustomHostVitalsFuncInvoked)
		assert.Equal(t, automdm.LabelMembershipTypeHostVitals, lbl.LabelMembershipType)

		query, queryValues, err := lbl.CalculateHostVitalsQuery()
		require.NoError(t, err)
		queryValuesJson, err := json.Marshal(queryValues)
		require.NoError(t, err)
		assert.Equal(t, "SELECT %s FROM %s JOIN host_custom_host_vitals ON (hosts.id = host_custom_host_vitals.host_id AND host_custom_host_vitals.custom_host_vital_id = ?) WHERE host_custom_host_vitals.value = ? GROUP BY hosts.id", query)
		assert.JSONEq(t, `[7,"Engineering"]`, string(queryValuesJson))
	})

	t.Run("custom host vital label missing id is rejected", func(t *testing.T) {
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name: "custom-vital-no-id",
			Criteria: &automdm.HostVitalCriteria{
				Vital: new("custom_host_vital"),
				Value: new("Engineering"),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "custom_host_vital_id")
	})

	t.Run("custom host vital label with unknown id is rejected", func(t *testing.T) {
		ds.GetCustomHostVitalsFunc = func(ctx context.Context, ids []uint) ([]automdm.CustomHostVital, error) {
			return nil, nil
		}
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
			Name: "custom-vital-bad-id",
			Criteria: &automdm.HostVitalCriteria{
				Vital:             new("custom_host_vital"),
				Value:             new("Engineering"),
				CustomHostVitalID: new(uint(999)),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not exist")
	})
}

func TestNewLabelFieldValidation(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		lbl.ID = 1
		return lbl, nil
	}

	// Manual label (no query) with platform should be rejected
	_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{
		Name:     "manual_with_platform",
		Platform: "darwin",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as manual but contains a platform")

	// Host_vitals label with platform should be rejected
	_, _, err = svc.NewLabel(ctx, automdm.LabelPayload{
		Name:     "vitals_with_platform",
		Platform: "darwin",
		Criteria: &automdm.HostVitalCriteria{
			Vital: ptr.String("end_user_idp_group"),
			Value: ptr.String("admin"),
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "declared as host_vitals but contains a platform")

	// Dynamic label with invalid platform should be rejected
	_, _, err = svc.NewLabel(ctx, automdm.LabelPayload{
		Name:     "dynamic_bad_platform",
		Query:    "SELECT 1",
		Platform: "invalidplatform",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid platform")

	// Dynamic label with valid platform should succeed
	_, _, err = svc.NewLabel(ctx, automdm.LabelPayload{
		Name:     "dynamic_good_platform",
		Query:    "SELECT 1",
		Platform: "darwin",
	})
	require.NoError(t, err)
}

func TestLabelActivities(t *testing.T) {
	ds := new(mock.Store)
	mockDuplicateStringGroupsExactMatch(ds)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	user := &automdm.User{ID: 7, GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	var activities []activity_api.ActivityDetails
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, a activity_api.ActivityDetails) error {
		activities = append(activities, a)
		return nil
	}

	const teamID = uint(42)
	teamName := "Workstations"
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		require.Equal(t, teamID, tid)
		return &automdm.TeamLite{ID: tid, Name: teamName}, nil
	}

	t.Run("create global label via UI emits created_label", func(t *testing.T) {
		activities = activities[:0]
		ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
			lbl.ID = 100
			return lbl, nil
		}
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{Name: "global1", Query: "SELECT 1"})
		require.NoError(t, err)
		require.Len(t, activities, 1)
		got, ok := activities[0].(automdm.ActivityTypeCreatedLabel)
		require.True(t, ok, "expected ActivityTypeCreatedLabel, got %T", activities[0])
		require.Equal(t, uint(100), got.ID)
		require.Equal(t, "global1", got.Name)
		require.Nil(t, got.AutoMDMID)
		require.Nil(t, got.AutoMDMName)
	})

	t.Run("modify label via UI emits edited_label with team info", func(t *testing.T) {
		activities = activities[:0]
		labelID := uint(101)
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{
				Label: automdm.Label{
					ID:                  lid,
					Name:                "edited",
					LabelMembershipType: automdm.LabelMembershipTypeDynamic,
				},
			}, nil, nil
		}
		ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{
				Label:    automdm.Label{ID: lbl.ID, Name: lbl.Name, TeamID: ptr.Uint(teamID)},
				TeamName: &teamName,
			}, nil, nil
		}
		_, _, err := svc.ModifyLabel(ctx, labelID, automdm.ModifyLabelPayload{Description: ptr.String("new")})
		require.NoError(t, err)
		require.Len(t, activities, 1)
		got, ok := activities[0].(automdm.ActivityTypeEditedLabel)
		require.True(t, ok, "expected ActivityTypeEditedLabel, got %T", activities[0])
		require.Equal(t, labelID, got.ID)
		require.Equal(t, "edited", got.Name)
		require.NotNil(t, got.AutoMDMID)
		require.Equal(t, teamID, *got.AutoMDMID)
		require.NotNil(t, got.AutoMDMName)
		require.Equal(t, teamName, *got.AutoMDMName)
	})

	t.Run("delete label by ID via UI emits deleted_label", func(t *testing.T) {
		activities = activities[:0]
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{
				Label:    automdm.Label{ID: lid, Name: "delete-by-id", TeamID: ptr.Uint(teamID)},
				TeamName: &teamName,
			}, nil, nil
		}
		ds.DeleteLabelFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		err := svc.DeleteLabelByID(ctx, 300)
		require.NoError(t, err)
		require.Len(t, activities, 1)
		got, ok := activities[0].(automdm.ActivityTypeDeletedLabel)
		require.True(t, ok)
		require.Equal(t, uint(300), got.ID)
		require.Equal(t, "delete-by-id", got.Name)
		require.NotNil(t, got.AutoMDMID)
		require.Equal(t, teamID, *got.AutoMDMID)
		require.NotNil(t, got.AutoMDMName)
		require.Equal(t, teamName, *got.AutoMDMName)
	})

	t.Run("apply specs emits per-label created/edited/no-op", func(t *testing.T) {
		activities = activities[:0]

		// Pre-existing label "edited-spec" has the OLD description; "noop-spec"
		// matches the spec exactly so it should produce no activity. After
		// apply, "new-spec" exists with its newly assigned ID.
		var lookupCalls int
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			lookupCalls++
			result := map[string]*automdm.Label{
				"edited-spec": {ID: 401, Name: "edited-spec", Description: "old", Query: "SELECT 1", LabelMembershipType: automdm.LabelMembershipTypeDynamic},
				"noop-spec":   {ID: 402, Name: "noop-spec", Description: "same", Query: "SELECT 2", LabelMembershipType: automdm.LabelMembershipTypeDynamic},
			}
			if lookupCalls > 1 { // post-apply: new-spec now exists; edited-spec has new desc
				result["new-spec"] = &automdm.Label{ID: 403, Name: "new-spec", Description: "fresh", Query: "SELECT 3", LabelMembershipType: automdm.LabelMembershipTypeDynamic}
				result["edited-spec"] = &automdm.Label{ID: 401, Name: "edited-spec", Description: "new", Query: "SELECT 1", LabelMembershipType: automdm.LabelMembershipTypeDynamic}
			}
			return result, nil
		}
		ds.SetAsideLabelsFunc = func(ctx context.Context, notOnTeamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
			return nil
		}
		ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
			return nil
		}

		err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{Name: "new-spec", Description: "fresh", Query: "SELECT 3", LabelMembershipType: automdm.LabelMembershipTypeDynamic},
			{Name: "edited-spec", Description: "new", Query: "SELECT 1", LabelMembershipType: automdm.LabelMembershipTypeDynamic},
			{Name: "noop-spec", Description: "same", Query: "SELECT 2", LabelMembershipType: automdm.LabelMembershipTypeDynamic},
		}, nil, nil)
		require.NoError(t, err)

		// Two activities: one created, one edited; the no-op spec emits nothing.
		require.Len(t, activities, 2)
		var sawCreate, sawEdit bool
		for _, a := range activities {
			switch v := a.(type) {
			case automdm.ActivityTypeCreatedLabel:
				require.Equal(t, "new-spec", v.Name)
				sawCreate = true
			case automdm.ActivityTypeEditedLabel:
				require.Equal(t, "edited-spec", v.Name)
				require.Equal(t, uint(401), v.ID)
				sawEdit = true
			default:
				t.Fatalf("unexpected activity type %T", a)
			}
		}
		require.True(t, sawCreate, "expected created_label for new-spec")
		require.True(t, sawEdit, "expected edited_label for edited-spec")
	})

	t.Run("apply specs detects platform-only change", func(t *testing.T) {
		activities = activities[:0]

		// Existing label "platform-spec" has Platform="darwin"; spec sets it to
		// "linux" with all other fields unchanged.
		var lookupCalls int
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			lookupCalls++
			return map[string]*automdm.Label{
				"platform-spec": {ID: 410, Name: "platform-spec", Platform: "darwin", LabelMembershipType: automdm.LabelMembershipTypeDynamic, Query: "SELECT 1"},
			}, nil
		}
		ds.SetAsideLabelsFunc = func(ctx context.Context, notOnTeamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
			return nil
		}
		ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
			return nil
		}

		err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{Name: "platform-spec", Platform: "windows", LabelMembershipType: automdm.LabelMembershipTypeDynamic, Query: "SELECT 1"},
		}, nil, nil)
		require.NoError(t, err)
		require.Len(t, activities, 1, "platform change should emit edited_label")
		got, ok := activities[0].(automdm.ActivityTypeEditedLabel)
		require.True(t, ok, "expected edited_label, got %T", activities[0])
		require.Equal(t, "platform-spec", got.Name)
	})

	t.Run("apply specs detects manual-label host membership change", func(t *testing.T) {
		activities = activities[:0]

		// Existing manual label "manual-spec" has identical fields to the spec
		// (so field comparison says no-op), but its host membership will change
		// from {1,2} to {1,3} after apply.
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			return map[string]*automdm.Label{
				"manual-spec": {ID: 501, Name: "manual-spec", LabelMembershipType: automdm.LabelMembershipTypeManual},
			}, nil
		}
		var membershipCalls int
		ds.LabelMembershipHostIDsFunc = func(ctx context.Context, labelID uint) ([]uint, error) {
			membershipCalls++
			if membershipCalls == 1 { // before apply
				return []uint{1, 2}, nil
			}
			return []uint{1, 3}, nil // after apply
		}
		ds.SetAsideLabelsFunc = func(ctx context.Context, notOnTeamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
			return nil
		}
		ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
			return nil
		}

		err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{Name: "manual-spec", LabelMembershipType: automdm.LabelMembershipTypeManual, Hosts: []string{"host1", "host3"}},
		}, nil, nil)
		require.NoError(t, err)
		require.Len(t, activities, 1)
		got, ok := activities[0].(automdm.ActivityTypeEditedLabel)
		require.True(t, ok, "expected ActivityTypeEditedLabel for host membership change, got %T", activities[0])
		require.Equal(t, "manual-spec", got.Name)
	})

	t.Run("apply specs treats manual-label host no-op as no-op", func(t *testing.T) {
		activities = activities[:0]

		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			return map[string]*automdm.Label{
				"manual-spec": {ID: 502, Name: "manual-spec", LabelMembershipType: automdm.LabelMembershipTypeManual},
			}, nil
		}
		ds.LabelMembershipHostIDsFunc = func(ctx context.Context, labelID uint) ([]uint, error) {
			return []uint{1, 2}, nil
		}
		ds.SetAsideLabelsFunc = func(ctx context.Context, notOnTeamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
			return nil
		}
		ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
			return nil
		}

		err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{
			{Name: "manual-spec", LabelMembershipType: automdm.LabelMembershipTypeManual, Hosts: []string{"host1", "host2"}},
		}, nil, nil)
		require.NoError(t, err)
		require.Empty(t, activities, "expected no activity for manual-label no-op")
	})

	t.Run("AddLabelsToHost emits edited_label per requested label", func(t *testing.T) {
		activities = activities[:0]
		const hostID = uint(7000)
		const targetTeamID = uint(42)
		host := &automdm.Host{ID: hostID, TeamID: ptr.Uint(targetTeamID), Platform: "darwin"}
		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			require.Equal(t, hostID, id)
			return host, nil
		}
		ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
			return map[string]uint{"global-manual": 600, "team-manual": 601}, nil
		}
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			lbl := automdm.Label{ID: lid, LabelMembershipType: automdm.LabelMembershipTypeManual}
			if lid == 601 {
				lbl.TeamID = ptr.Uint(targetTeamID)
			}
			return &automdm.LabelWithTeamName{Label: lbl}, nil, nil
		}
		ds.AddLabelsToHostFunc = func(ctx context.Context, h uint, ids []uint) error {
			require.Equal(t, hostID, h)
			require.ElementsMatch(t, []uint{600, 601}, ids)
			return nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			require.NotNil(t, filter.TeamID)
			require.Equal(t, targetTeamID, *filter.TeamID)
			return map[string]*automdm.Label{
				"global-manual": {ID: 600, Name: "global-manual"},
				"team-manual":   {ID: 601, Name: "team-manual", TeamID: ptr.Uint(targetTeamID)},
			}, nil
		}

		require.NoError(t, svc.AddLabelsToHost(ctx, hostID, []string{"global-manual", "team-manual"}))
		require.Len(t, activities, 2)
		byName := map[string]automdm.ActivityTypeEditedLabel{}
		for _, a := range activities {
			e, ok := a.(automdm.ActivityTypeEditedLabel)
			require.True(t, ok, "expected edited_label, got %T", a)
			byName[e.Name] = e
		}
		require.Nil(t, byName["global-manual"].AutoMDMID)
		require.NotNil(t, byName["team-manual"].AutoMDMID)
		require.Equal(t, targetTeamID, *byName["team-manual"].AutoMDMID)
		require.NotNil(t, byName["team-manual"].AutoMDMName)
		require.Equal(t, teamName, *byName["team-manual"].AutoMDMName)
	})

	t.Run("AddLabelsToHost dedupes label names in activities", func(t *testing.T) {
		activities = activities[:0]
		const hostID = uint(7100)
		host := &automdm.Host{ID: hostID, Platform: "darwin"}
		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return host, nil
		}
		ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
			require.Len(t, names, 1, "duplicates should be removed before validation")
			require.Equal(t, "dup-label", names[0])
			return map[string]uint{"dup-label": 800}, nil
		}
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{Label: automdm.Label{ID: lid, LabelMembershipType: automdm.LabelMembershipTypeManual}}, nil, nil
		}
		ds.AddLabelsToHostFunc = func(ctx context.Context, h uint, ids []uint) error {
			require.Equal(t, []uint{800}, ids)
			return nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			require.Equal(t, []string{"dup-label"}, names, "duplicates should be removed before activity emission")
			return map[string]*automdm.Label{"dup-label": {ID: 800, Name: "dup-label"}}, nil
		}

		require.NoError(t, svc.AddLabelsToHost(ctx, hostID, []string{"dup-label", "dup-label", "dup-label"}))
		require.Len(t, activities, 1, "duplicate label names must produce a single activity")
		got, ok := activities[0].(automdm.ActivityTypeEditedLabel)
		require.True(t, ok)
		require.Equal(t, "dup-label", got.Name)
	})

	t.Run("RemoveLabelsFromHost emits edited_label", func(t *testing.T) {
		activities = activities[:0]
		const hostID = uint(7001)
		host := &automdm.Host{ID: hostID, Platform: "darwin"} // no team
		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return host, nil
		}
		ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
			return map[string]uint{"global-manual": 700}, nil
		}
		ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
			return &automdm.LabelWithTeamName{Label: automdm.Label{ID: lid, LabelMembershipType: automdm.LabelMembershipTypeManual}}, nil, nil
		}
		ds.RemoveLabelsFromHostFunc = func(ctx context.Context, h uint, ids []uint) error {
			require.Equal(t, hostID, h)
			require.Equal(t, []uint{700}, ids)
			return nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			return map[string]*automdm.Label{
				"global-manual": {ID: 700, Name: "global-manual"},
			}, nil
		}

		require.NoError(t, svc.RemoveLabelsFromHost(ctx, hostID, []string{"global-manual"}))
		require.Len(t, activities, 1)
		got, ok := activities[0].(automdm.ActivityTypeEditedLabel)
		require.True(t, ok)
		require.Equal(t, "global-manual", got.Name)
		require.Nil(t, got.AutoMDMID)
	})
}

func TestLabelFieldLengthValidation(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.NewLabelFunc = func(ctx context.Context, lbl *automdm.Label, opts ...automdm.OptionalArg) (*automdm.Label, error) {
		lbl.ID = 1
		return lbl, nil
	}
	ds.LabelFunc = func(ctx context.Context, lid uint, teamFilter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{ID: lid, Name: "existing", LabelMembershipType: automdm.LabelMembershipTypeDynamic}, nil, nil
	}
	ds.SaveLabelFunc = func(ctx context.Context, lbl *automdm.Label, hostIDs []uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{Label: *lbl}, hostIDs, nil
	}
	ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
		return nil
	}

	tooLong := strings.Repeat("a", 256)
	const descErr = "description may not exceed 255 characters"
	nameErr := `label "` + strings.Repeat("a", 40) + `..." name may not exceed 255 characters`

	t.Run("NewLabel", func(t *testing.T) {
		ds.NewLabelFuncInvoked = false
		_, _, err := svc.NewLabel(ctx, automdm.LabelPayload{Name: "l", Query: "SELECT 1", Description: tooLong})
		require.ErrorContains(t, err, descErr)
		_, _, err = svc.NewLabel(ctx, automdm.LabelPayload{Name: tooLong, Query: "SELECT 1"})
		require.ErrorContains(t, err, nameErr)
		require.False(t, ds.NewLabelFuncInvoked)

		// varchar(255) counts characters, so 255 two-byte characters fit.
		_, _, err = svc.NewLabel(ctx, automdm.LabelPayload{Name: "l", Query: "SELECT 1", Description: strings.Repeat("é", 255)})
		require.NoError(t, err)
	})

	t.Run("ModifyLabel", func(t *testing.T) {
		ds.SaveLabelFuncInvoked = false
		_, _, err := svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{Description: &tooLong})
		require.ErrorContains(t, err, descErr)
		_, _, err = svc.ModifyLabel(ctx, 1, automdm.ModifyLabelPayload{Name: &tooLong})
		require.ErrorContains(t, err, nameErr)
		require.False(t, ds.SaveLabelFuncInvoked)
	})

	t.Run("ApplyLabelSpecs", func(t *testing.T) {
		ds.ApplyLabelSpecsWithAuthorFuncInvoked = false
		err := svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{{Name: "l", Query: "SELECT 1", Description: tooLong}}, nil, nil)
		require.ErrorContains(t, err, descErr)
		err = svc.ApplyLabelSpecs(ctx, []*automdm.LabelSpec{{Name: tooLong, Query: "SELECT 1"}}, nil, nil)
		require.ErrorContains(t, err, nameErr)
		require.False(t, ds.ApplyLabelSpecsWithAuthorFuncInvoked)
	})
}
