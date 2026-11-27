package service

import (
	"context"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomHostVitalsAuth(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.CreateCustomHostVitalFunc = func(ctx context.Context, name string) (automdm.CustomHostVital, error) {
		return automdm.CustomHostVital{ID: 1, Name: name}, nil
	}
	ds.UpdateCustomHostVitalFunc = func(ctx context.Context, id uint, name string) (automdm.CustomHostVital, error) {
		return automdm.CustomHostVital{ID: id, Name: name}, nil
	}
	ds.DeleteCustomHostVitalFunc = func(ctx context.Context, id uint) (string, error) {
		return "Asset tag", nil
	}
	ds.ListCustomHostVitalsFunc = func(ctx context.Context, opt automdm.ListOptions) ([]automdm.CustomHostVital, *automdm.PaginationMetadata, int, error) {
		return nil, &automdm.PaginationMetadata{}, 0, nil
	}
	ds.GetCustomHostVitalsFunc = func(ctx context.Context, ids []uint) ([]automdm.CustomHostVital, error) {
		return []automdm.CustomHostVital{{ID: 1, Name: "Asset tag"}}, nil
	}
	ds.SetHostCustomHostVitalValueFunc = func(ctx context.Context, hostID, vitalID uint, value string) error {
		return nil
	}
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{ID: id}, nil
	}
	ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
		return nil, nil, nil
	}

	globalRoles := []struct {
		name    string
		user    *automdm.User
		readOK  bool
		writeOK bool
	}{
		{"global admin", &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}, true, true},
		{"global maintainer", &automdm.User{ID: 2, GlobalRole: new(automdm.RoleMaintainer)}, true, true},
		{"global gitops", &automdm.User{ID: 3, GlobalRole: new(automdm.RoleGitOps)}, true, true},
		{"global observer", &automdm.User{ID: 4, GlobalRole: new(automdm.RoleObserver)}, true, false},
		{"global observer+", &automdm.User{ID: 5, GlobalRole: new(automdm.RoleObserverPlus)}, true, false},
		{"team admin", &automdm.User{ID: 6, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}, true, false},
		{"team maintainer", &automdm.User{ID: 7, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}}, true, false},
		{"team gitops", &automdm.User{ID: 8, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}}, true, false},
		{"team observer", &automdm.User{ID: 9, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}, true, false},
	}

	for _, tt := range globalRoles {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, _, _, err := svc.ListCustomHostVitals(ctx, automdm.ListOptions{})
			checkAuthErr(t, !tt.readOK, err)

			_, err = svc.CreateCustomHostVital(ctx, "Asset tag")
			checkAuthErr(t, !tt.writeOK, err)

			_, err = svc.UpdateCustomHostVital(ctx, 1, "Asset tag")
			checkAuthErr(t, !tt.writeOK, err)

			err = svc.DeleteCustomHostVital(ctx, 1)
			checkAuthErr(t, !tt.writeOK, err)

			err = svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: "Asset tag"}}, false)
			checkAuthErr(t, !tt.writeOK, err)
		})
	}
}

func TestListCustomHostVitalsPassesSearchQuery(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	var gotOpts automdm.ListOptions
	ds.ListCustomHostVitalsFunc = func(ctx context.Context, opt automdm.ListOptions) ([]automdm.CustomHostVital, *automdm.PaginationMetadata, int, error) {
		gotOpts = opt
		return nil, &automdm.PaginationMetadata{}, 0, nil
	}

	_, _, _, err := svc.ListCustomHostVitals(ctx, automdm.ListOptions{MatchQuery: "asset"})
	require.NoError(t, err)
	require.True(t, ds.ListCustomHostVitalsFuncInvoked)
	// MatchQuery is forwarded to the datastore (search by name or variable name).
	assert.Equal(t, "asset", gotOpts.MatchQuery)
}

func TestSetHostCustomHostVitalValueAuth(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	hostTeamID := uint(1)
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{ID: id, TeamID: &hostTeamID}, nil
	}
	ds.GetCustomHostVitalsFunc = func(ctx context.Context, ids []uint) ([]automdm.CustomHostVital, error) {
		return []automdm.CustomHostVital{{ID: 1, Name: "Asset tag"}}, nil
	}
	ds.SetHostCustomHostVitalValueFunc = func(ctx context.Context, hostID, vitalID uint, value string) error {
		return nil
	}

	// Per-host value is a host-scoped write (authz type host_custom_vital): global
	// admin/maintainer and admins/maintainers of the host's team can set it;
	// observers, gitops (blocked at the host-list gate), and users of another team
	// cannot.
	testCases := []struct {
		name       string
		user       *automdm.User
		shouldFail bool
	}{
		{"global admin", &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}, false},
		{"global maintainer", &automdm.User{ID: 2, GlobalRole: new(automdm.RoleMaintainer)}, false},
		{"global gitops", &automdm.User{ID: 3, GlobalRole: new(automdm.RoleGitOps)}, true},
		{"global observer", &automdm.User{ID: 4, GlobalRole: new(automdm.RoleObserver)}, true},
		{"team admin (host team)", &automdm.User{ID: 5, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}, false},
		{"team maintainer (host team)", &automdm.User{ID: 6, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}}, false},
		{"team observer (host team)", &automdm.User{ID: 7, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}, true},
		{"team maintainer (other team)", &automdm.User{ID: 8, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}}, true},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			err := svc.SetHostCustomHostVitalValue(ctx, 42, 1, "engineering")
			checkAuthErr(t, tt.shouldFail, err)
		})
	}
}

func TestCustomHostVitalNameValidation(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.CreateCustomHostVitalFunc = func(ctx context.Context, name string) (automdm.CustomHostVital, error) {
		return automdm.CustomHostVital{ID: 1, Name: name}, nil
	}

	invalidNames := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"leading space", " Asset tag"},
		{"trailing space", "Asset tag "},
		{"leading tab", "\tAsset tag"},
		{"trailing newline", "Asset tag\n"},
	}
	for _, tt := range invalidNames {
		t.Run("reject "+tt.name, func(t *testing.T) {
			ds.CreateCustomHostVitalFuncInvoked = false
			_, err := svc.CreateCustomHostVital(ctx, tt.value)
			require.Error(t, err)
			assert.False(t, ds.CreateCustomHostVitalFuncInvoked)
		})
	}

	validNames := []struct {
		name  string
		value string
	}{
		{"internal spaces", "Asset tag"},
		{"lowercase", "asset tag"},
		{"mixed case with digits", "Rack 12B Location"},
	}
	for _, tt := range validNames {
		t.Run("accept "+tt.name, func(t *testing.T) {
			vital, err := svc.CreateCustomHostVital(ctx, tt.value)
			require.NoError(t, err)
			require.NotNil(t, vital)
		})
	}
}

func TestUpsertCustomHostVitals(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("rejects invalid names without persisting", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			t.Fatal("UpsertCustomHostVitals should not be called for an invalid name")
			return nil, nil, nil
		}
		err := svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: " bad"}}, false)
		require.Error(t, err)
	})

	t.Run("rejects duplicate names within the same payload without persisting", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			t.Fatal("UpsertCustomHostVitals should not be called for a duplicate name")
			return nil, nil, nil
		}
		err := svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: "Function"}, {Name: "Function"}}, false)
		require.Error(t, err)
	})

	t.Run("rejects names that are duplicates under the case-insensitive collation", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			t.Fatal("UpsertCustomHostVitals should not be called for a case-only duplicate name")
			return nil, nil, nil
		}
		err := svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: "Function"}, {Name: "function"}}, false)
		require.Error(t, err)
	})

	t.Run("dry run validates without persisting", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			t.Fatal("UpsertCustomHostVitals should not be called on a dry run")
			return nil, nil, nil
		}
		err := svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: "Function"}}, true)
		require.NoError(t, err)
	})

	t.Run("emits an activity per created and deleted vital", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			require.Equal(t, []automdm.CustomHostVital{{Name: "Function"}}, vitals)
			return []automdm.CustomHostVital{{ID: 2, Name: "Function"}}, []automdm.CustomHostVital{{ID: 1, Name: "Department"}}, nil
		}
		var activities []activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			activities = append(activities, activity)
			return nil
		}
		err := svc.UpsertCustomHostVitals(ctx, []automdm.CustomHostVital{{Name: "Function"}}, false)
		require.NoError(t, err)
		require.Len(t, activities, 2)
		require.IsType(t, automdm.ActivityTypeCreatedCustomHostVital{}, activities[0])
		require.IsType(t, automdm.ActivityTypeDeletedCustomHostVital{}, activities[1])
	})

	t.Run("surfaces a still-referenced vital as a conflict", func(t *testing.T) {
		ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
			return nil, nil, &automdm.CustomHostVitalUsedError{CustomHostVitalUsedInfo: automdm.CustomHostVitalUsedInfo{
				CustomHostVitalID:   1,
				CustomHostVitalName: "Department",
				Entity:              automdm.EntityUsingCustomHostVital{Type: automdm.CustomHostVitalEntityScript, Name: "collect.sh", AutoMDMName: "Unassigned"},
			}}
		}
		err := svc.UpsertCustomHostVitals(ctx, nil, false)
		require.Error(t, err)
		var conflictErr *automdm.ConflictError
		require.ErrorAs(t, err, &conflictErr)
	})
}
