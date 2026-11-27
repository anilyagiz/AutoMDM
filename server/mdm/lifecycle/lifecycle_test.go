package mdmlifecycle

import (
	"context"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func nopNewActivity(ctx context.Context, user *automdm.User, details automdm.ActivityDetails) error {
	return nil
}

func TestDoUnsupportedParams(t *testing.T) {
	ds := new(mock.Store)
	lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)

	err := lc.Do(context.Background(), HostOptions{})
	require.ErrorContains(t, err, "unsupported platform")

	err = lc.Do(context.Background(), HostOptions{Platform: "linux"})
	require.ErrorContains(t, err, "unsupported platform")

	err = lc.Do(context.Background(), HostOptions{Platform: "darwin", Action: "invalid"})
	require.ErrorContains(t, err, "unknown action")

	err = lc.Do(context.Background(), HostOptions{Platform: "windows", Action: "invalid"})
	require.ErrorContains(t, err, "unknown action")
}

func TestDoParamValidation(t *testing.T) {
	ds := new(mock.Store)
	lf := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
	ctx := context.Background()

	cases := []struct {
		platform string
		action   HostAction
		wantErr  bool
	}{

		{"darwin", HostActionTurnOn, true},
		{"darwin", HostActionTurnOff, true},
		{"darwin", HostActionReset, true},
		{"darwin", HostActionDelete, true},
		{"windows", HostActionTurnOn, true},
		{"windows", HostActionTurnOff, true},
		{"windows", HostActionReset, true},
		{"windows", HostActionDelete, false},
	}

	for _, tc := range cases {
		err := lf.Do(ctx, HostOptions{
			Action:   tc.action,
			Platform: tc.platform,
		})
		if tc.wantErr {
			require.ErrorContains(t, err, "required")
		} else {
			require.NoError(t, err)
		}
	}
}

// TestReconcileHostNameEnforcementOnEnrollment verifies that both Apple
// enrollment lifecycle branches — turn-on (TokenUpdate) and reset (Authenticate,
// covering re-enrollment) — reconcile the host's host-name template enforcement,
// so a host enrolling into a team with a template gets a queued row.
func TestReconcileHostNameEnforcementOnEnrollment(t *testing.T) {
	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	const hostID = uint(99)

	t.Run("turn-on reconciles with the enrolled host id", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetNanoMDMEnrollmentFunc = func(ctx context.Context, uuid string) (*automdm.NanoEnrollment, error) {
			return &automdm.NanoEnrollment{Enabled: true, Type: "Device", TokenUpdateTally: 1}, nil
		}
		ds.GetHostMDMCheckinInfoFunc = func(ctx context.Context, uuid string) (*automdm.HostMDMCheckinInfo, error) {
			return &automdm.HostMDMCheckinInfo{HostID: hostID, Platform: "darwin"}, nil
		}
		ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) { return job, nil }
		var gotIDs []uint
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error {
			gotIDs = hostIDs
			return nil
		}

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		require.NoError(t, lc.Do(ctx, HostOptions{Action: HostActionTurnOn, Platform: "darwin", UUID: "host-uuid"}))
		require.True(t, ds.ReconcileHostDeviceNamesForHostsFuncInvoked)
		require.Equal(t, []uint{hostID}, gotIDs)
	})

	t.Run("turn-on reconciles on the DEP branch before its early return", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetNanoMDMEnrollmentFunc = func(ctx context.Context, uuid string) (*automdm.NanoEnrollment, error) {
			return &automdm.NanoEnrollment{Enabled: true, Type: "Device", TokenUpdateTally: 1}, nil
		}
		// DEPAssignedToAutoMDM takes the DEP branch, which queues a job and returns
		// early — the reconcile must run before that branch.
		ds.GetHostMDMCheckinInfoFunc = func(ctx context.Context, uuid string) (*automdm.HostMDMCheckinInfo, error) {
			return &automdm.HostMDMCheckinInfo{HostID: hostID, Platform: "darwin", DEPAssignedToAutoMDM: true}, nil
		}
		ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) { return job, nil }
		var gotIDs []uint
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error {
			gotIDs = hostIDs
			return nil
		}

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		require.NoError(t, lc.Do(ctx, HostOptions{Action: HostActionTurnOn, Platform: "darwin", UUID: "host-uuid"}))
		require.True(t, ds.ReconcileHostDeviceNamesForHostsFuncInvoked)
		require.Equal(t, []uint{hostID}, gotIDs)
		require.True(t, ds.NewJobFuncInvoked, "DEP branch should have been taken")
	})

	t.Run("turn-on skips reconcile when the enrollment is not ready", func(t *testing.T) {
		ds := new(mock.Store)
		// TokenUpdateTally != 1 makes turnOnApple short-circuit before reconciling.
		ds.GetNanoMDMEnrollmentFunc = func(ctx context.Context, uuid string) (*automdm.NanoEnrollment, error) {
			return &automdm.NanoEnrollment{Enabled: true, Type: "Device", TokenUpdateTally: 2}, nil
		}
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error { return nil }

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		require.NoError(t, lc.Do(ctx, HostOptions{Action: HostActionTurnOn, Platform: "darwin", UUID: "host-uuid"}))
		require.False(t, ds.ReconcileHostDeviceNamesForHostsFuncInvoked)
	})

	t.Run("reset reconciles with the upserted host id", func(t *testing.T) {
		ds := new(mock.Store)
		ds.MDMAppleUpsertHostFunc = func(ctx context.Context, mdmHost *automdm.Host, personalType automdm.PersonalEnrollmentType) error {
			mdmHost.ID = hostID
			return nil
		}
		ds.MDMResetEnrollmentFunc = func(ctx context.Context, uuid string, scepRenewalInProgress bool) error { return nil }
		var gotIDs []uint
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error {
			gotIDs = hostIDs
			return nil
		}

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		require.NoError(t, lc.Do(ctx, HostOptions{
			Action: HostActionReset, Platform: "darwin",
			UUID: "host-uuid", HardwareSerial: "serial", HardwareModel: "MacBookPro",
		}))
		require.True(t, ds.ReconcileHostDeviceNamesForHostsFuncInvoked)
		require.Equal(t, []uint{hostID}, gotIDs)
	})

	t.Run("reset classifies the personal enrollment type", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			opts HostOptions
			want automdm.PersonalEnrollmentType
		}{
			{
				name: "account-driven user enrollment",
				opts: HostOptions{Platform: "ios", HardwareModel: "iPhone14,2", UserEnrollmentID: "enrollment-id"},
				want: automdm.PersonalEnrollmentTypeAccountDriven,
			},
			{
				name: "manual BYOD",
				opts: HostOptions{
					Platform: "ios", UUID: "host-uuid", HardwareSerial: "serial", HardwareModel: "iPhone14,2",
					PersonalEnrollmentType: automdm.PersonalEnrollmentTypeManualProfile,
				},
				want: automdm.PersonalEnrollmentTypeManualProfile,
			},
			{
				name: "company-owned",
				opts: HostOptions{Platform: "darwin", UUID: "host-uuid", HardwareSerial: "serial", HardwareModel: "MacBookPro"},
				want: automdm.PersonalEnrollmentTypeNone,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ds := new(mock.Store)
				var got automdm.PersonalEnrollmentType
				ds.MDMAppleUpsertHostFunc = func(ctx context.Context, mdmHost *automdm.Host, personalType automdm.PersonalEnrollmentType) error {
					got = personalType
					mdmHost.ID = hostID
					return nil
				}
				ds.MDMResetEnrollmentFunc = func(ctx context.Context, uuid string, scepRenewalInProgress bool) error { return nil }
				ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error { return nil }

				tc.opts.Action = HostActionReset
				lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
				require.NoError(t, lc.Do(ctx, tc.opts))
				require.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("reset skips reconcile during SCEP renewal", func(t *testing.T) {
		ds := new(mock.Store)
		ds.MDMResetEnrollmentFunc = func(ctx context.Context, uuid string, scepRenewalInProgress bool) error { return nil }
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error { return nil }

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		require.NoError(t, lc.Do(ctx, HostOptions{
			Action: HostActionReset, Platform: "darwin",
			UUID: "host-uuid", HardwareSerial: "serial", HardwareModel: "MacBookPro",
			SCEPRenewalInProgress: true,
		}))
		require.False(t, ds.ReconcileHostDeviceNamesForHostsFuncInvoked)
	})
}

// TestDeleteAppleDuplicateDEPHost verifies that deleting one of a set of
// duplicate DEP hosts (same serial) does not recreate a pending "ghost" host
// when another DEP-assigned host with that serial still exists, while a host
// with no duplicate is still restored as before.
// TestMDMEnrolledActivityHostIDAndSerial verifies the Apple mdm_enrolled activity
// carries host_id (so it lands on the host's activity timeline via HostIDs) and a
// populated host_serial for both device and account-driven user (BYOD)
// enrollments. Regression coverage for #49777.
func TestMDMEnrolledActivityHostIDAndSerial(t *testing.T) {
	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	const hostID = uint(99)

	// runTurnOn drives an Apple turn-on enrollment of the given nano enrollment
	// type and returns the mdm_enrolled activity that was recorded.
	runTurnOn := func(t *testing.T, enrollType, hardwareSerial string, opts HostOptions) *automdm.ActivityTypeMDMEnrolled {
		ds := new(mock.Store)
		ds.GetNanoMDMEnrollmentFunc = func(ctx context.Context, uuid string) (*automdm.NanoEnrollment, error) {
			return &automdm.NanoEnrollment{Enabled: true, Type: enrollType, TokenUpdateTally: 1}, nil
		}
		ds.GetHostMDMCheckinInfoFunc = func(ctx context.Context, uuid string) (*automdm.HostMDMCheckinInfo, error) {
			return &automdm.HostMDMCheckinInfo{HostID: hostID, Platform: opts.Platform, HardwareSerial: hardwareSerial}, nil
		}
		ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) { return job, nil }
		ds.ReconcileHostDeviceNamesForHostsFunc = func(ctx context.Context, hostIDs []uint) error { return nil }

		var captured *automdm.ActivityTypeMDMEnrolled
		newAct := func(ctx context.Context, user *automdm.User, details automdm.ActivityDetails) error {
			if a, ok := details.(*automdm.ActivityTypeMDMEnrolled); ok {
				captured = a
			}
			return nil
		}

		lc := New(ds, slog.New(slog.DiscardHandler), newAct)
		require.NoError(t, lc.Do(ctx, opts))
		require.NotNil(t, captured, "an mdm_enrolled activity should have been recorded")
		return captured
	}

	t.Run("device enrollment: host_id set, host_serial is the hardware serial", func(t *testing.T) {
		a := runTurnOn(t, mdm.EnrollType(mdm.Device).String(), "C08VQ2AXHT96",
			HostOptions{Action: HostActionTurnOn, Platform: "darwin", UUID: "host-uuid"})

		require.Equal(t, hostID, a.HostID)
		require.Equal(t, []uint{hostID}, a.HostIDs())
		require.NotNil(t, a.HostSerial)
		require.Equal(t, "C08VQ2AXHT96", *a.HostSerial)
		require.Nil(t, a.EnrollmentID)
	})

	t.Run("account-driven user enrollment: host_id set, host_serial is the enrollment id", func(t *testing.T) {
		a := runTurnOn(t, mdm.EnrollType(mdm.UserEnrollmentDevice).String(), "",
			HostOptions{Action: HostActionTurnOn, Platform: "ios", UUID: "ADUE-ENROLL-ID", UserEnrollmentID: "ADUE-ENROLL-ID"})

		require.Equal(t, hostID, a.HostID)
		require.Equal(t, []uint{hostID}, a.HostIDs())
		require.NotNil(t, a.EnrollmentID)
		require.Equal(t, "ADUE-ENROLL-ID", *a.EnrollmentID)
		require.NotNil(t, a.HostSerial)
		require.Equal(t, "ADUE-ENROLL-ID", *a.HostSerial)
	})
}

func TestDeleteAppleDuplicateDEPHost(t *testing.T) {
	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})

	const serial = "ABC123XYZ"
	host := &automdm.Host{ID: 1, HardwareSerial: serial, Platform: "darwin"}

	newDS := func(dupExists bool) *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.AppleBMEnabledAndConfigured = true
			return ac, nil
		}
		abmTokenID := uint(7)
		ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
			return &automdm.HostDEPAssignment{HostID: hostID, ABMTokenID: &abmTokenID}, nil
		}
		ds.ReconcileDuplicateDEPHostOnDeleteFunc = func(ctx context.Context, s, platform string, deletedHostID uint) (bool, error) {
			require.Equal(t, serial, s)
			require.Equal(t, host.Platform, platform)
			require.Equal(t, host.ID, deletedHostID)
			return dupExists, nil
		}
		ds.RestoreMDMApplePendingDEPHostFunc = func(ctx context.Context, h *automdm.Host) error {
			return nil
		}
		return ds
	}

	t.Run("duplicate exists, ghost host not restored", func(t *testing.T) {
		ds := newDS(true)

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		err := lc.Do(ctx, HostOptions{Action: HostActionDelete, Platform: "darwin", Host: host})
		require.NoError(t, err)

		require.True(t, ds.ReconcileDuplicateDEPHostOnDeleteFuncInvoked)
		require.False(t, ds.RestoreMDMApplePendingDEPHostFuncInvoked)
	})

	t.Run("no duplicate, ghost host restored", func(t *testing.T) {
		ds := newDS(false)
		// "No team" default keeps getDefaultTeamForABMToken from needing TeamExists.
		ds.GetABMTokenByIDFunc = func(ctx context.Context, tokenID uint) (*automdm.ABMToken, error) {
			return &automdm.ABMToken{ID: tokenID}, nil
		}
		ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
			return job, nil
		}

		lc := New(ds, slog.New(slog.DiscardHandler), nopNewActivity)
		err := lc.Do(ctx, HostOptions{Action: HostActionDelete, Platform: "darwin", Host: host})
		require.NoError(t, err)

		require.True(t, ds.ReconcileDuplicateDEPHostOnDeleteFuncInvoked)
		require.True(t, ds.RestoreMDMApplePendingDEPHostFuncInvoked)
	})
}
