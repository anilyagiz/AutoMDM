package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	hostidentity_types "github.com/anilyagiz/automdm/v4/ee/pkg/hostidentity/types"
	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mock"
	akvmock "github.com/anilyagiz/automdm/v4/server/mock/redis_advanced"
	"github.com/stretchr/testify/require"
)

// memoryKVStore is an in-memory automdm.KeyValueStore for exercising the
// activity rate limit without Redis.
func memoryKVStore() *akvmock.AdvancedKeyValueStore {
	var mu sync.Mutex
	values := map[string]string{}
	return &akvmock.AdvancedKeyValueStore{
		SetFunc: func(ctx context.Context, key, value string, expireTime time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			values[key] = value
			return nil
		},
		GetFunc: func(ctx context.Context, key string) (*string, error) {
			mu.Lock()
			defer mu.Unlock()
			v, ok := values[key]
			if !ok {
				return nil, nil
			}
			return &v, nil
		},
	}
}

type oneTimeEnrollFixture struct {
	ds         *mock.DataStore
	svc        automdm.Service
	ctx        context.Context
	row        automdm.HostOneTimeEnrollSecret
	rejections *[]automdm.ActivityTypeHostEnrollmentRejected
	enrolled   *int
	logs       *bytes.Buffer
	kv         *akvmock.AdvancedKeyValueStore
}

func newOneTimeEnrollFixture(t *testing.T, useOneTimeEnrollSecrets bool) *oneTimeEnrollFixture {
	return newOneTimeEnrollFixtureWithMDM(t, func(mdmConfig *config.MDMConfig) { mdmConfig.AppleOneTimeEnrollSecrets = useOneTimeEnrollSecrets })
}

func newOneTimeEnrollFixtureWithMDM(t *testing.T, setMDM func(*config.MDMConfig)) *oneTimeEnrollFixture {
	hostID := uint(42)
	row := automdm.HostOneTimeEnrollSecret{
		ID:             7,
		Secret:         "one-time-secret",
		HostID:         &hostID,
		TeamID:         new(uint(3)),
		Platform:       "darwin",
		HardwareUUID:   "UUID-1",
		HardwareSerial: "SERIAL-1",
	}

	ds := new(mock.DataStore)
	cfg := config.TestConfig()
	setMDM(&cfg.MDM)
	var logs bytes.Buffer
	kv := memoryKVStore()
	opts := &TestServerOpts{KeyValueStore: kv, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, opts)

	var rejections []automdm.ActivityTypeHostEnrollmentRejected
	var enrolled int
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, a activity_api.ActivityDetails) error {
		switch act := a.(type) {
		case automdm.ActivityTypeHostEnrollmentRejected:
			rejections = append(rejections, act)
		case automdm.ActivityTypeAutoMDMEnrolled:
			enrolled++
		}
		return nil
	}

	ds.GetHostOneTimeEnrollSecretFunc = func(ctx context.Context, secret string) (*automdm.HostOneTimeEnrollSecret, error) {
		if secret == row.Secret {
			r := row
			return &r, nil
		}
		return nil, newNotFoundError()
	}
	ds.VerifyEnrollSecretFunc = func(ctx context.Context, secret string) (*automdm.EnrollSecret, error) {
		if secret == "shared-secret" {
			return &automdm.EnrollSecret{Secret: secret, TeamID: new(uint(9))}, nil
		}
		return nil, newNotFoundError()
	}
	ds.GetHostIdentityCertByNameFunc = func(ctx context.Context, name string) (*hostidentity_types.HostIdentityCertificate, error) {
		return nil, newNotFoundError()
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		ac := &automdm.AppConfig{}
		ac.MDM.EnabledAndConfigured = true
		return ac, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid}, nil
	}
	ds.ClearEndUserAfterWindowsResetFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (bool, error) {
		return false, nil
	}
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{ID: id, Hostname: "victim-mac"}, nil
	}
	ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
		return &automdm.Host{ID: hostID, UUID: row.HardwareUUID, Platform: "darwin"}, nil
	}
	ds.EnrollOsqueryFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
		return &automdm.Host{ID: hostID, UUID: row.HardwareUUID, Platform: "darwin"}, nil
	}
	ds.ExtendHostOrbitDebugUntilFunc = func(ctx context.Context, hostID uint, until time.Time) error { return nil }
	ds.UpdateHostFunc = func(ctx context.Context, host *automdm.Host) error { return nil }
	ds.SerialUpdateHostFunc = func(ctx context.Context, host *automdm.Host) error { return nil }

	return &oneTimeEnrollFixture{ds: ds, svc: svc, ctx: ctx, row: row, rejections: &rejections, enrolled: &enrolled, logs: &logs, kv: kv}
}

func (f *oneTimeEnrollFixture) orbitInfo() automdm.OrbitHostInfo {
	return automdm.OrbitHostInfo{HardwareUUID: f.row.HardwareUUID, HardwareSerial: f.row.HardwareSerial, Platform: f.row.Platform, Hostname: "mac"}
}

func (f *oneTimeEnrollFixture) osqueryDetails() map[string]map[string]string {
	return map[string]map[string]string{
		"system_info": {"uuid": f.row.HardwareUUID, "hardware_serial": f.row.HardwareSerial},
		"os_version":  {"platform": f.row.Platform},
	}
}

func orbitEnrollConfig(opts []automdm.DatastoreEnrollOrbitOption) *automdm.DatastoreEnrollOrbitConfig {
	c := &automdm.DatastoreEnrollOrbitConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}

func osqueryEnrollConfig(opts []automdm.DatastoreEnrollOsqueryOption) *automdm.DatastoreEnrollOsqueryConfig {
	c := &automdm.DatastoreEnrollOsqueryConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}

func TestEnrollOrbitWithOneTimeEnrollSecret(t *testing.T) {
	t.Run("matching identifiers consume the secret and use its team", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		var got *automdm.DatastoreEnrollOrbitConfig
		f.ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
			got = orbitEnrollConfig(opts)
			return &automdm.Host{ID: *f.row.HostID, UUID: f.row.HardwareUUID, Platform: "darwin"}, nil
		}

		nodeKey, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), f.row.Secret, "")
		require.NoError(t, err)
		require.NotEmpty(t, nodeKey)
		require.NotNil(t, got.OneTimeEnrollSecretID)
		require.Equal(t, f.row.ID, *got.OneTimeEnrollSecretID)
		require.Equal(t, f.row.TeamID, got.TeamID)
		require.False(t, got.RejectSharedSecretForAppleMDMHosts)
		require.Empty(t, *f.rejections)
		require.Equal(t, 1, *f.enrolled)
	})

	t.Run("identifier mismatch is refused before touching the datastore", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		info := f.orbitInfo()
		info.HardwareSerial = "SOMEONE-ELSE"

		for range 3 {
			_, err := f.svc.EnrollOrbit(f.ctx, info, f.row.Secret, "")
			requireAuthFailed(t, err)
		}
		require.False(t, f.ds.EnrollOrbitFuncInvoked)
		// one activity for three attempts against the same host and reason
		require.Len(t, *f.rejections, 1)
		got := (*f.rejections)[0]
		require.Equal(t, automdm.EnrollmentRejectedOneTimeSecretIdentifierMismatch, got.Reason)
		require.Equal(t, f.row.HostID, got.HostID)
		require.Equal(t, "victim-mac", got.HostDisplayName)
		require.Equal(t, "SOMEONE-ELSE", got.HostSerial)
		require.Equal(t, string(automdm.EnrollmentPlaneOrbit), got.EnrollmentPlane)

		// a different reason for the same host is its own activity
		f.ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
			return nil, &automdm.EnrollmentRejectedError{Reason: automdm.EnrollmentRejectedOneTimeSecretSpent, HostID: f.row.HostID}
		}
		_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), f.row.Secret, "")
		requireAuthFailed(t, err)
		require.Len(t, *f.rejections, 2)
		require.Equal(t, automdm.EnrollmentRejectedOneTimeSecretSpent, (*f.rejections)[1].Reason)
	})

	t.Run("shared secret carries the rejection option only when the flag is on", func(t *testing.T) {
		for _, flag := range []bool{true, false} {
			f := newOneTimeEnrollFixture(t, flag)
			var got *automdm.DatastoreEnrollOrbitConfig
			f.ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
				got = orbitEnrollConfig(opts)
				return &automdm.Host{ID: 1, UUID: "other", Platform: "darwin"}, nil
			}
			_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), "shared-secret", "")
			require.NoError(t, err)
			require.Nil(t, got.OneTimeEnrollSecretID)
			require.Equal(t, new(uint(9)), got.TeamID)
			require.Equal(t, flag, got.RejectSharedSecretForAppleMDMHosts)
		}
	})

	t.Run("datastore rejection of a shared secret is generic to the caller and recorded", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		victim := uint(77)
		f.ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
			return nil, &automdm.EnrollmentRejectedError{Reason: automdm.EnrollmentRejectedSharedSecretForMDMManagedHost, HostID: &victim}
		}
		_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), "shared-secret", "")
		requireAuthFailed(t, err)
		var orbitErr automdm.OrbitError
		require.NotErrorAs(t, err, &orbitErr)
		require.Len(t, *f.rejections, 1)
		require.Equal(t, automdm.EnrollmentRejectedSharedSecretForMDMManagedHost, (*f.rejections)[0].Reason)
		require.Equal(t, &victim, (*f.rejections)[0].HostID)
		require.Zero(t, *f.enrolled)
		require.Contains(t, f.logs.String(), `msg="enrollment rejected"`)
		require.Contains(t, f.logs.String(), "host_id=77 ")
	})

	t.Run("unsigned enrollment for a host with an identity cert is rejected and recorded", func(t *testing.T) {
		for _, plane := range []string{"orbit", "osquery"} {
			t.Run(plane, func(t *testing.T) {
				f := newOneTimeEnrollFixture(t, true)
				victim := uint(77)
				f.ds.GetHostIdentityCertByNameFunc = func(ctx context.Context, name string) (*hostidentity_types.HostIdentityCertificate, error) {
					return &hostidentity_types.HostIdentityCertificate{SerialNumber: 5, HostID: &victim}, nil
				}
				var err error
				if plane == "orbit" {
					_, err = f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), "shared-secret", "")
				} else {
					_, err = f.svc.EnrollOsquery(f.ctx, "shared-secret", f.row.HardwareUUID, f.osqueryDetails())
				}
				requireAuthFailed(t, err)
				require.Len(t, *f.rejections, 1)
				require.Equal(t, automdm.EnrollmentRejectedHostIdentityCertRequired, (*f.rejections)[0].Reason)
				require.Equal(t, &victim, (*f.rejections)[0].HostID)
				require.Zero(t, *f.enrolled)
			})
		}
	})

	t.Run("unknown secret is still invalid", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), "nope", "")
		requireAuthFailed(t, err)
		require.Empty(t, *f.rejections)
	})

	t.Run("a secret is honored only while the switch for the platform that minted it is on", func(t *testing.T) {
		for _, tc := range []struct {
			name                        string
			platform                    string
			useOneTimeEnrollSecrets     bool
			windowsOneTimeEnrollSecrets bool
			wantHonored                 bool
		}{
			{name: "windows secret, windows on", platform: "windows", windowsOneTimeEnrollSecrets: true, wantHonored: true},
			{name: "windows secret, only apple on", platform: "windows", useOneTimeEnrollSecrets: true},
			{name: "apple secret, only windows on", platform: "darwin", windowsOneTimeEnrollSecrets: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newOneTimeEnrollFixtureWithMDM(t, func(mdmConfig *config.MDMConfig) {
					mdmConfig.AppleOneTimeEnrollSecrets = tc.useOneTimeEnrollSecrets
					mdmConfig.WindowsOneTimeEnrollSecrets = tc.windowsOneTimeEnrollSecrets
				})
				f.row.Platform = tc.platform
				f.ds.GetHostOneTimeEnrollSecretFunc = func(ctx context.Context, secret string) (*automdm.HostOneTimeEnrollSecret, error) {
					r := f.row
					return &r, nil
				}
				_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), f.row.Secret, "")
				if !tc.wantHonored {
					// Treated as a shared secret, which it is not.
					requireAuthFailed(t, err)
					require.False(t, f.ds.EnrollOrbitFuncInvoked)
					return
				}
				require.NoError(t, err)
			})
		}
	})
}

func TestEnrollOsqueryWithOneTimeEnrollSecret(t *testing.T) {
	t.Run("matching identifiers consume the secret and use its team", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		var got *automdm.DatastoreEnrollOsqueryConfig
		f.ds.EnrollOsqueryFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
			got = osqueryEnrollConfig(opts)
			return &automdm.Host{ID: *f.row.HostID, UUID: f.row.HardwareUUID, Platform: "darwin", OsqueryHostID: new(got.OsqueryHostID), NodeKey: new(got.NodeKey)}, nil
		}

		nodeKey, err := f.svc.EnrollOsquery(f.ctx, f.row.Secret, f.row.HardwareUUID, f.osqueryDetails())
		require.NoError(t, err)
		require.NotEmpty(t, nodeKey)
		require.NotNil(t, got.OneTimeEnrollSecretID)
		require.Equal(t, f.row.ID, *got.OneTimeEnrollSecretID)
		require.Equal(t, f.row.TeamID, got.TeamID)
		require.Equal(t, f.row.HardwareUUID, got.HardwareUUID)
		require.Equal(t, f.row.HardwareSerial, got.HardwareSerial)
	})

	t.Run("missing or mismatched identifiers are refused", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		cases := map[string]map[string]map[string]string{
			"no host details":  nil,
			"wrong uuid":       {"system_info": {"uuid": "OTHER", "hardware_serial": f.row.HardwareSerial}, "os_version": {"platform": "darwin"}},
			"wrong platform":   {"system_info": {"uuid": f.row.HardwareUUID, "hardware_serial": f.row.HardwareSerial}, "os_version": {"platform": "ubuntu"}},
			"missing platform": {"system_info": {"uuid": f.row.HardwareUUID, "hardware_serial": f.row.HardwareSerial}},
		}
		for name, details := range cases {
			t.Run(name, func(t *testing.T) {
				_, err := f.svc.EnrollOsquery(f.ctx, f.row.Secret, f.row.HardwareUUID, details)
				require.Error(t, err)
				require.ErrorContains(t, err, "enroll failed")
			})
		}
		require.False(t, f.ds.EnrollOsqueryFuncInvoked)
		// same host, same reason: a single activity across all attempts
		require.Len(t, *f.rejections, 1)
		require.Equal(t, automdm.EnrollmentRejectedOneTimeSecretIdentifierMismatch, (*f.rejections)[0].Reason)
		require.Equal(t, string(automdm.EnrollmentPlaneOsquery), (*f.rejections)[0].EnrollmentPlane)
	})

	t.Run("shared secret carries the rejection option only when the flag is on", func(t *testing.T) {
		for _, flag := range []bool{true, false} {
			f := newOneTimeEnrollFixture(t, flag)
			var got *automdm.DatastoreEnrollOsqueryConfig
			f.ds.EnrollOsqueryFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
				got = osqueryEnrollConfig(opts)
				return &automdm.Host{ID: 1, OsqueryHostID: new(got.OsqueryHostID), NodeKey: new(got.NodeKey)}, nil
			}
			_, err := f.svc.EnrollOsquery(f.ctx, "shared-secret", f.row.HardwareUUID, f.osqueryDetails())
			require.NoError(t, err)
			require.Nil(t, got.OneTimeEnrollSecretID)
			require.Equal(t, flag, got.RejectSharedSecretForAppleMDMHosts)
		}
	})

	t.Run("datastore rejection is generic and recorded once", func(t *testing.T) {
		f := newOneTimeEnrollFixture(t, true)
		f.ds.EnrollOsqueryFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
			return nil, &automdm.EnrollmentRejectedError{Reason: automdm.EnrollmentRejectedSharedSecretForMDMManagedHost, HostID: f.row.HostID}
		}
		for range 2 {
			_, err := f.svc.EnrollOsquery(f.ctx, "shared-secret", f.row.HardwareUUID, f.osqueryDetails())
			require.ErrorContains(t, err, "enroll failed")
		}
		require.Len(t, *f.rejections, 1)
	})
}

func TestRecordEnrollmentRejectedWithoutKeyValueStore(t *testing.T) {
	// With no key-value store the rejection is still refused and recorded; the
	// rate limit simply does not apply.
	f := newOneTimeEnrollFixture(t, true)
	svc, ctx := newTestServiceWithConfig(t, f.ds, func() config.AutoMDMConfig {
		cfg := config.TestConfig()
		cfg.MDM.AppleOneTimeEnrollSecrets = true
		return cfg
	}(), nil, nil, &TestServerOpts{})
	info := f.orbitInfo()
	info.HardwareUUID = "OTHER"
	for range 2 {
		_, err := svc.EnrollOrbit(ctx, info, f.row.Secret, "")
		requireAuthFailed(t, err)
	}
}

// requireAuthFailed asserts the generic authentication failure orbit gets for
// every rejected enrollment, whatever the reason.
func requireAuthFailed(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var authErr *automdm.AuthFailedError
	require.ErrorAs(t, err, &authErr)
}

func TestEnsureAutoMDMdConfigWithOneTimeEnrollSecrets(t *testing.T) {
	ctx := t.Context()
	ds := new(mock.Store)
	mdmConfig := config.MDMConfig{AppleSCEPCert: "./testdata/server.pem", AppleSCEPKey: "./testdata/server.key"}
	signingCert, _, _, err := mdmConfig.AppleSCEP()
	require.NoError(t, err)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		ac := &automdm.AppConfig{}
		ac.ServerSettings.ServerURL = "https://automdm.example.com"
		return ac, nil
	}
	// team 1 has a shared secret, team 2 has none, and "no team" has none at
	// all (so the aggregate omits it entirely).
	ds.GetMDMAppleConfigProfileByTeamAndIdentifierFunc = func(ctx context.Context, teamID *uint, identifier string) (*automdm.MDMAppleConfigProfile, error) {
		return nil, newNotFoundError()
	}
	ds.AggregateEnrollSecretPerTeamFunc = func(ctx context.Context) ([]*automdm.EnrollSecret, error) {
		return []*automdm.EnrollSecret{
			{Secret: "team-1-shared", TeamID: new(uint(1))},
			{Secret: "", TeamID: new(uint(2))},
		}, nil
	}
	var upserted []*automdm.MDMAppleConfigProfile
	ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, ps []*automdm.MDMAppleConfigProfile) error {
		upserted = ps
		return nil
	}

	require.NoError(t, ensureAutoMDMProfiles(ctx, ds, slog.New(slog.DiscardHandler), signingCert.Certificate[0], true))

	automdmdByTeam := map[string]*automdm.MDMAppleConfigProfile{}
	caCount := 0
	for _, p := range upserted {
		key := "no-team"
		if p.TeamID != nil {
			key = fmt.Sprintf("team-%d", *p.TeamID)
		}
		switch p.Name {
		case mdm.AutoMDMdConfigProfileName:
			automdmdByTeam[key] = p
		case mdm.AutoMDMCAConfigProfileName:
			caCount++
		}
	}
	require.Len(t, automdmdByTeam, 3, "automdmd profile for team 1, team 2 and no team")
	require.Equal(t, 3, caCount)
	for key, p := range automdmdByTeam {
		require.Contains(t, string(p.Mobileconfig), automdm.HostSecretPlaceholder(automdm.HostSecretEnrollSecret), key)
		require.NotContains(t, string(p.Mobileconfig), "team-1-shared", key)
	}

	// flag off keeps today's behavior: team 2 falls back to... nothing, since
	// there is no global secret, and no "no team" profile is generated.
	upserted = nil
	require.NoError(t, ensureAutoMDMProfiles(ctx, ds, slog.New(slog.DiscardHandler), signingCert.Certificate[0], false))
	var automdmdOff int
	for _, p := range upserted {
		if p.Name == mdm.AutoMDMdConfigProfileName {
			automdmdOff++
			require.Contains(t, string(p.Mobileconfig), "team-1-shared")
			require.NotContains(t, string(p.Mobileconfig), automdm.HostSecretPrefix)
		}
	}
	require.Equal(t, 1, automdmdOff)
}

func TestResendAutoMDMdProfileWithOneTimeEnrollSecrets(t *testing.T) {
	const (
		automdmdProfileUUID = "a-automdmd-profile"
		customProfileUUID   = "a-custom-profile"
		hostID              = uint(1)
	)
	newSvc := func(t *testing.T, flag bool, status automdm.MDMDeliveryStatus) (*mock.Store, automdm.Service, context.Context) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.MDM.AppleOneTimeEnrollSecrets = flag
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{
			License:             &automdm.LicenseInfo{Tier: automdm.TierPremium},
			SkipCreateTestUsers: true,
		})
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		ds.HostLiteFunc = func(ctx context.Context, hid uint) (*automdm.Host, error) {
			return &automdm.Host{ID: hid, UUID: "host-uuid-1", Platform: "darwin"}, nil
		}
		ds.GetMDMAppleConfigProfileFunc = func(ctx context.Context, pid string) (*automdm.MDMAppleConfigProfile, error) {
			name := "Custom profile"
			if pid == automdmdProfileUUID {
				name = mdm.AutoMDMdConfigProfileName
			}
			return &automdm.MDMAppleConfigProfile{ProfileUUID: pid, Name: name}, nil
		}
		ds.GetHostMDMProfileInstallStatusFunc = func(ctx context.Context, hostUUID string, profUUID string) (automdm.MDMDeliveryStatus, error) {
			return status, nil
		}
		ds.ResendHostMDMProfileFunc = func(ctx context.Context, hostUUID, profUUID string) error { return nil }
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
		return ds, svc, ctx
	}

	t.Run("admin can resend the automdmd profile while verifying", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, true, automdm.MDMDeliveryVerifying)
		require.NoError(t, svc.ResendHostMDMProfile(ctx, hostID, automdmdProfileUUID))
		require.True(t, ds.ResendHostMDMProfileFuncInvoked)
	})

	t.Run("verified and failed are resendable as before", func(t *testing.T) {
		for _, status := range []automdm.MDMDeliveryStatus{automdm.MDMDeliveryVerified, automdm.MDMDeliveryFailed} {
			ds, svc, ctx := newSvc(t, true, status)
			require.NoError(t, svc.ResendHostMDMProfile(ctx, hostID, automdmdProfileUUID))
			require.True(t, ds.ResendHostMDMProfileFuncInvoked)
		}
	})

	t.Run("pending stays a conflict", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, true, automdm.MDMDeliveryPending)
		err := svc.ResendHostMDMProfile(ctx, hostID, automdmdProfileUUID)
		require.Error(t, err)
		require.ErrorContains(t, err, "can’t be resent")
		require.False(t, ds.ResendHostMDMProfileFuncInvoked)
	})

	t.Run("custom profiles in verifying are still a conflict", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, true, automdm.MDMDeliveryVerifying)
		err := svc.ResendHostMDMProfile(ctx, hostID, customProfileUUID)
		require.ErrorContains(t, err, "can’t be resent")
		require.False(t, ds.ResendHostMDMProfileFuncInvoked)
	})

	t.Run("flag off: automdmd profile in verifying is a conflict", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, false, automdm.MDMDeliveryVerifying)
		err := svc.ResendHostMDMProfile(ctx, hostID, automdmdProfileUUID)
		require.ErrorContains(t, err, "can’t be resent")
		require.False(t, ds.ResendHostMDMProfileFuncInvoked)

		ds, svc, ctx = newSvc(t, false, automdm.MDMDeliveryVerified)
		require.NoError(t, svc.ResendHostMDMProfile(ctx, hostID, automdmdProfileUUID))
		require.True(t, ds.ResendHostMDMProfileFuncInvoked)
	})

	t.Run("device-token resend of the automdmd profile is refused with a descriptive error", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, true, automdm.MDMDeliveryVerified)
		host := &automdm.Host{ID: hostID, UUID: "host-uuid-1", Platform: "darwin"}
		err := svc.ResendDeviceHostMDMProfile(ctx, host, automdmdProfileUUID)
		require.Error(t, err)
		require.ErrorContains(t, err, "Ask your IT admin")
		var statusErr interface{ Status() int }
		require.ErrorAs(t, err, &statusErr)
		require.Equal(t, http.StatusForbidden, statusErr.Status())
		require.False(t, ds.ResendHostMDMProfileFuncInvoked)

		// other profiles resend as before
		require.NoError(t, svc.ResendDeviceHostMDMProfile(ctx, host, customProfileUUID))
		require.True(t, ds.ResendHostMDMProfileFuncInvoked)
	})

	t.Run("flag off: device-token resend of the automdmd profile behaves as before", func(t *testing.T) {
		ds, svc, ctx := newSvc(t, false, automdm.MDMDeliveryVerified)
		host := &automdm.Host{ID: hostID, UUID: "host-uuid-1", Platform: "darwin"}
		require.NoError(t, svc.ResendDeviceHostMDMProfile(ctx, host, automdmdProfileUUID))
		require.True(t, ds.ResendHostMDMProfileFuncInvoked)
	})
}

func TestEnsureAutoMDMdConfigRemovesPlaceholderProfilesWhenOff(t *testing.T) {
	ctx := t.Context()
	mdmConfig := config.MDMConfig{AppleSCEPCert: "./testdata/server.pem", AppleSCEPKey: "./testdata/server.key"}
	signingCert, _, _, err := mdmConfig.AppleSCEP()
	require.NoError(t, err)

	type deletion struct {
		teamID     *uint
		identifier string
	}
	newDS := func(existingProfile string) (*mock.Store, *[]deletion) {
		ds := new(mock.Store)
		var deleted []deletion
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.ServerSettings.ServerURL = "https://automdm.example.com"
			return ac, nil
		}
		// team 1 has no shared secret and there is no global secret, so the
		// aggregate has no "no team" entry at all.
		ds.AggregateEnrollSecretPerTeamFunc = func(ctx context.Context) ([]*automdm.EnrollSecret, error) {
			return []*automdm.EnrollSecret{{Secret: "", TeamID: new(uint(1))}}, nil
		}
		ds.GetMDMAppleConfigProfileByTeamAndIdentifierFunc = func(ctx context.Context, teamID *uint, identifier string) (*automdm.MDMAppleConfigProfile, error) {
			return &automdm.MDMAppleConfigProfile{TeamID: teamID, Identifier: identifier, Mobileconfig: []byte(existingProfile)}, nil
		}
		ds.DeleteMDMAppleConfigProfileByTeamAndIdentifierFunc = func(ctx context.Context, teamID *uint, identifier string) error {
			deleted = append(deleted, deletion{teamID, identifier})
			return nil
		}
		ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, ps []*automdm.MDMAppleConfigProfile) error {
			require.Empty(t, ps)
			return nil
		}
		return ds, &deleted
	}
	placeholderProfile := "<string>" + automdm.HostSecretPlaceholder(automdm.HostSecretEnrollSecret) + "</string>"

	t.Run("placeholder automdmd profiles are removed for the team and for no team", func(t *testing.T) {
		ds, deleted := newDS(placeholderProfile)
		require.NoError(t, ensureAutoMDMProfiles(ctx, ds, slog.New(slog.DiscardHandler), signingCert.Certificate[0], false))
		// only the automdmd profile; the CA profile has nothing to do with enroll secrets
		require.ElementsMatch(t, []deletion{
			{nil, mobileconfig.AutoMDMdConfigPayloadIdentifier},
			{new(uint(1)), mobileconfig.AutoMDMdConfigPayloadIdentifier},
		}, *deleted)
	})

	t.Run("profiles built from a shared secret are left alone", func(t *testing.T) {
		ds, deleted := newDS("<string>an-old-shared-secret</string>")
		require.NoError(t, ensureAutoMDMProfiles(ctx, ds, slog.New(slog.DiscardHandler), signingCert.Certificate[0], false))
		require.Empty(t, *deleted)
	})

	t.Run("nothing is removed while the setting is on", func(t *testing.T) {
		ds, deleted := newDS(placeholderProfile)
		ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, ps []*automdm.MDMAppleConfigProfile) error {
			require.NotEmpty(t, ps)
			return nil
		}
		require.NoError(t, ensureAutoMDMProfiles(ctx, ds, slog.New(slog.DiscardHandler), signingCert.Certificate[0], true))
		require.Empty(t, *deleted)
		require.False(t, ds.GetMDMAppleConfigProfileByTeamAndIdentifierFuncInvoked)
	})
}

// TestEnrollRejectSharedSecretForMDMHosts covers when the service asks the datastore to refuse shared secrets for a platform's
// MDM hosts: only with the platform's setting on and its MDM on, since otherwise a refused host's credential can't be resent.
func TestEnrollRejectSharedSecretForMDMHosts(t *testing.T) {
	type rejectFlags struct{ windows, android bool }
	platforms := []struct {
		name        string
		setOneTime  func(*config.MDMConfig, bool)
		setMDM      func(*automdm.AppConfig, bool)
		rejectFlags func(on bool) rejectFlags
	}{
		{
			name:        "Windows",
			setOneTime:  func(c *config.MDMConfig, on bool) { c.WindowsOneTimeEnrollSecrets = on },
			setMDM:      func(ac *automdm.AppConfig, on bool) { ac.MDM.WindowsEnabledAndConfigured = on },
			rejectFlags: func(on bool) rejectFlags { return rejectFlags{windows: on} },
		},
		{
			name:        "Android",
			setOneTime:  func(c *config.MDMConfig, on bool) { c.AndroidOneTimeEnrollSecrets = on },
			setMDM:      func(ac *automdm.AppConfig, on bool) { ac.MDM.AndroidEnabledAndConfigured = on },
			rejectFlags: func(on bool) rejectFlags { return rejectFlags{android: on} },
		},
	}
	cases := []struct {
		name       string
		oneTime    bool
		mdmEnabled bool
		wantReject bool
	}{
		{name: "enabled, MDM on", oneTime: true, mdmEnabled: true, wantReject: true},
		// With the platform's MDM off its credential can't be resent, so a refused host would have no way back.
		{name: "enabled, MDM off", oneTime: true, mdmEnabled: false},
		{name: "disabled", oneTime: false, mdmEnabled: true},
	}
	for _, platform := range platforms {
		for _, tc := range cases {
			t.Run(platform.name+", "+tc.name, func(t *testing.T) {
				f := newOneTimeEnrollFixtureWithMDM(t, func(mdmConfig *config.MDMConfig) {
					platform.setOneTime(mdmConfig, tc.oneTime)
				})
				f.ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					ac := &automdm.AppConfig{}
					ac.MDM.EnabledAndConfigured = true
					platform.setMDM(ac, tc.mdmEnabled)
					return ac, nil
				}
				var orbitCfg *automdm.DatastoreEnrollOrbitConfig
				f.ds.EnrollOrbitFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
					orbitCfg = orbitEnrollConfig(opts)
					return &automdm.Host{ID: 1, UUID: "other", Platform: "darwin"}, nil
				}
				var osqueryCfg *automdm.DatastoreEnrollOsqueryConfig
				f.ds.EnrollOsqueryFunc = func(ctx context.Context, opts ...automdm.DatastoreEnrollOsqueryOption) (*automdm.Host, error) {
					osqueryCfg = osqueryEnrollConfig(opts)
					return &automdm.Host{ID: 1, OsqueryHostID: new(osqueryCfg.OsqueryHostID), NodeKey: new(osqueryCfg.NodeKey)}, nil
				}
				want := platform.rejectFlags(tc.wantReject)

				_, err := f.svc.EnrollOrbit(f.ctx, f.orbitInfo(), "shared-secret", "")
				require.NoError(t, err)
				require.False(t, orbitCfg.RejectSharedSecretForAppleMDMHosts)
				require.Equal(t, want.windows, orbitCfg.RejectSharedSecretForWindowsMDMHosts)
				require.Equal(t, want.android, orbitCfg.RejectSharedSecretForAndroidMDMHosts)

				_, err = f.svc.EnrollOsquery(f.ctx, "shared-secret", f.row.HardwareUUID, f.osqueryDetails())
				require.NoError(t, err)
				require.False(t, osqueryCfg.RejectSharedSecretForAppleMDMHosts)
				require.Equal(t, want.windows, osqueryCfg.RejectSharedSecretForWindowsMDMHosts)
				require.Equal(t, want.android, osqueryCfg.RejectSharedSecretForAndroidMDMHosts)
			})
		}
	}
}

func TestExpandWindowsHostSecrets(t *testing.T) {
	placeholder := automdm.HostSecretPlaceholder(automdm.HostSecretEnrollSecret)
	withPlaceholder := `<Data>AUTOMDM_SECRET="` + placeholder + `"</Data>`
	withCAChallenge := `<Data>` + automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge) + `</Data>`
	for _, tc := range []struct {
		name    string
		doc     string
		live    string
		liveErr error
		want    string
		wantErr string
	}{
		// The tokens never need escaping, but a value that did would not break the SyncML.
		{name: "live secret is resolved, escaped", doc: withPlaceholder, live: "a&b", want: `<Data>AUTOMDM_SECRET="a&amp;b"</Data>`},
		// Nothing minted: the host gets an empty value, which automdmd reads as nothing waiting.
		{name: "nothing minted resolves to empty", doc: withPlaceholder, want: `<Data>AUTOMDM_SECRET=""</Data>`},
		{name: "failed lookup is an error, not an empty secret", doc: withPlaceholder, liveErr: errors.New("db down"), wantErr: "db down"},
		{name: "conditional access challenge is resolved", doc: withCAChallenge, live: "0a1b", want: `<Data>0a1b</Data>`},
		{name: "conditional access challenge failure is an error", doc: withCAChallenge, liveErr: errors.New("db down"), wantErr: "db down"},
		// The lookup func is set only when the document needs it; an unset mock func panics if called.
		{name: "document without host secrets never reaches the datastore", doc: `<Data>plain</Data>`, want: `<Data>plain</Data>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			switch tc.doc {
			case withPlaceholder:
				ds.GetLiveWindowsMDMOneTimeEnrollSecretFunc = func(ctx context.Context, enrollmentID uint) (string, error) {
					require.EqualValues(t, 7, enrollmentID)
					require.True(t, ctxdb.IsPrimaryRequired(ctx), "the secret may have been minted moments ago")
					return tc.live, tc.liveErr
				}
			case withCAChallenge:
				ds.FindOrCreateWindowsConditionalAccessSCEPChallengeFunc = func(ctx context.Context, enrollmentID uint) (string, error) {
					require.EqualValues(t, 7, enrollmentID)
					return tc.live, tc.liveErr
				}
			}
			svc, _ := newTestService(t, ds, nil, nil)
			got, err := svc.(validationMiddleware).Service.(*Service).expandWindowsHostSecrets(t.Context(), tc.doc, 7)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
