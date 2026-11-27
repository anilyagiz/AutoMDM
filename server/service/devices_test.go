package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAutoMDMDesktopSummary(t *testing.T) {
	t.Run("free implementation", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)
		sum, err := svc.GetAutoMDMDesktopSummary(ctx)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.Empty(t, sum)
	})

	t.Run("alternative browser host URL gets mapped from app config", func(t *testing.T) {
		ds := new(mock.Store)
		license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

		ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
			return false, nil
		}
		ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
			return nil, nil
		}
		ds.HasSelfServiceSoftwareInstallersFunc = func(ctx context.Context, platform string, teamID *uint) (bool, error) {
			return false, nil
		}
		ds.FailingPoliciesCountFunc = func(ctx context.Context, host *automdm.Host) (uint, uint, error) {
			return uint(0), uint(0), nil
		}

		testCases := []struct {
			name                   string
			appCfg                 automdm.AppConfig
			expectedBrowserHostURL string
		}{
			{
				name:                   "empty app config",
				appCfg:                 automdm.AppConfig{},
				expectedBrowserHostURL: "",
			},
			{
				name: "with some value stored",
				appCfg: automdm.AppConfig{AutoMDMDesktop: automdm.AutoMDMDesktopSettings{
					AlternativeBrowserHost: "https://example.com",
				}},
				expectedBrowserHostURL: "https://example.com",
			},
		}
		for _, tc := range testCases {
			c := tc
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &c.appCfg, nil
			}

			ctx := test.HostContext(ctx, &automdm.Host{
				OsqueryHostID: ptr.String("test"),
			})
			sum, err := svc.GetAutoMDMDesktopSummary(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedBrowserHostURL, sum.AlternativeBrowserHost)
		}
	})

	t.Run("different app config values for managed host", func(t *testing.T) {
		ds := new(mock.Store)
		license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})
		ds.FailingPoliciesCountFunc = func(ctx context.Context, host *automdm.Host) (uint, uint, error) {
			return uint(1), uint(1), nil
		}
		const expectedPlatform = "darwin"
		ds.HasSelfServiceSoftwareInstallersFunc = func(ctx context.Context, platform string, teamID *uint) (bool, error) {
			assert.Equal(t, expectedPlatform, platform)
			return true, nil
		}

		cases := []struct {
			mdm         automdm.MDM
			depAssigned bool
			out         automdm.DesktopNotifications
		}{
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: true,
					MacOSMigration: automdm.MacOSMigration{
						Enable: true,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      true,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: true,
					MacOSMigration: automdm.MacOSMigration{
						Enable: true,
					},
				},
				depAssigned: false,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: false,
					MacOSMigration: automdm.MacOSMigration{
						Enable: true,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: true,
					MacOSMigration: automdm.MacOSMigration{
						Enable: false,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: false,
					MacOSMigration: automdm.MacOSMigration{
						Enable: false,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
		}

		for _, c := range cases {
			c := c
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				appCfg := automdm.AppConfig{}
				appCfg.MDM = c.mdm
				return &appCfg, nil
			}

			ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
				return false, nil
			}
			ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
				return &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
				}, nil
			}

			ctx := test.HostContext(ctx, &automdm.Host{
				OsqueryHostID:        ptr.String("test"),
				DEPAssignedToAutoMDM: &c.depAssigned,
				Platform:             expectedPlatform,
			})
			sum, err := svc.GetAutoMDMDesktopSummary(ctx)
			require.NoError(t, err)
			require.Equal(t, c.out, sum.Notifications, fmt.Sprintf("enabled_and_configured: %t | macos_migration.enable: %t", c.mdm.EnabledAndConfigured, c.mdm.MacOSMigration.Enable))
			require.EqualValues(t, 1, *sum.FailingPolicies)
			assert.Equal(t, ptr.Bool(true), sum.SelfService)
		}
	})

	t.Run("different app config values for unmanaged host", func(t *testing.T) {
		ds := new(mock.Store)
		license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})
		ds.FailingPoliciesCountFunc = func(ctx context.Context, host *automdm.Host) (uint, uint, error) {
			return uint(1), uint(1), nil
		}
		ds.HasSelfServiceSoftwareInstallersFunc = func(ctx context.Context, platform string, teamID *uint) (bool, error) {
			return true, nil
		}
		cases := []struct {
			mdm         automdm.MDM
			depAssigned bool
			out         automdm.DesktopNotifications
		}{
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: true,
					MacOSMigration: automdm.MacOSMigration{
						Enable: true,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: true,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: false,
					MacOSMigration: automdm.MacOSMigration{
						Enable: true,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: true,
					MacOSMigration: automdm.MacOSMigration{
						Enable: false,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				mdm: automdm.MDM{
					EnabledAndConfigured: false,
					MacOSMigration: automdm.MacOSMigration{
						Enable: false,
					},
				},
				depAssigned: true,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
		}

		mdmInfo := &automdm.HostMDM{
			IsServer:               false,
			InstalledFromDep:       true,
			Enrolled:               false,
			Name:                   automdm.WellKnownMDMAutoMDM,
			DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
		}

		for _, c := range cases {
			c := c
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				appCfg := automdm.AppConfig{}
				appCfg.MDM = c.mdm
				return &appCfg, nil
			}
			ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
				return false, nil
			}

			ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
				return mdmInfo, nil
			}

			ctx = test.HostContext(ctx, &automdm.Host{
				OsqueryHostID:        ptr.String("test"),
				DEPAssignedToAutoMDM: &c.depAssigned,
			})
			sum, err := svc.GetAutoMDMDesktopSummary(ctx)
			require.NoError(t, err)
			require.Equal(t, c.out, sum.Notifications, fmt.Sprintf("enabled_and_configured: %t | macos_migration.enable: %t", c.mdm.EnabledAndConfigured, c.mdm.MacOSMigration.Enable))
			require.EqualValues(t, 1, *sum.FailingPolicies)
		}
	})

	t.Run("different host attributes", func(t *testing.T) {
		ds := new(mock.Store)
		license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

		// context without a host
		sum, err := svc.GetAutoMDMDesktopSummary(ctx)
		require.Empty(t, sum)
		var authErr *automdm.AuthRequiredError
		require.ErrorAs(t, err, &authErr)

		ds.FailingPoliciesCountFunc = func(ctx context.Context, host *automdm.Host) (uint, uint, error) {
			return uint(1), uint(1), nil
		}

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			appCfg := automdm.AppConfig{}
			appCfg.MDM.EnabledAndConfigured = true
			appCfg.MDM.MacOSMigration.Enable = true
			return &appCfg, nil
		}

		ds.HasSelfServiceSoftwareInstallersFunc = func(ctx context.Context, platform string, teamID *uint) (bool, error) {
			return false, nil
		}

		cases := []struct {
			name    string
			host    *automdm.Host
			hostMDM *automdm.HostMDM
			err     error
			out     automdm.DesktopNotifications
		}{
			{
				name: "not enrolled into osquery",
				host: &automdm.Host{OsqueryHostID: nil},
				err:  nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "manually enrolled into another MDM",
				host: &automdm.Host{
					OsqueryHostID:        ptr.String("test"),
					DEPAssignedToAutoMDM: ptr.Bool(false),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       false,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "DEP capable, but already unenrolled",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               false,
					Name:                   automdm.WellKnownMDMAutoMDM,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: true,
				},
			},
			{
				name: "DEP capable, but enrolled into AutoMDM",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMAutoMDM,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "failed ADE assignment status",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseFailed)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "throttled ADE assignment status",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseThrottled)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "not accessible ADE assignment status",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseNotAccessible)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "empty ADE assignment status",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(""),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "nil ADE assignment status",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: nil,
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      false,
					RenewEnrollmentProfile: false,
				},
			},
			{
				name: "all conditions met",
				host: &automdm.Host{
					DEPAssignedToAutoMDM: ptr.Bool(true),
					OsqueryHostID:        ptr.String("test"),
				},
				hostMDM: &automdm.HostMDM{
					IsServer:               false,
					InstalledFromDep:       true,
					Enrolled:               true,
					Name:                   automdm.WellKnownMDMIntune,
					DEPProfileAssignStatus: ptr.String(string(automdm.DEPAssignProfileResponseSuccess)),
				},
				err: nil,
				out: automdm.DesktopNotifications{
					NeedsMDMMigration:      true,
					RenewEnrollmentProfile: false,
				},
			},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				ctx = test.HostContext(ctx, c.host)

				ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
					if c.hostMDM == nil {
						return nil, sql.ErrNoRows
					}
					return c.hostMDM, nil
				}

				ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
					return c.hostMDM != nil && c.hostMDM.Enrolled == true && c.hostMDM.Name == automdm.WellKnownMDMAutoMDM, nil
				}

				sum, err := svc.GetAutoMDMDesktopSummary(ctx)

				if c.err != nil {
					require.ErrorIs(t, err, c.err)
					require.Empty(t, sum)
				} else {

					require.NoError(t, err)
					require.Equal(t, c.out, sum.Notifications)
					require.EqualValues(t, 1, *sum.FailingPolicies)
				}
			})
		}
	})

	t.Run("BitLocker PIN prompt", func(t *testing.T) {
		// The decision itself is tested in ee/server/service. These cases check the summary uses it.
		for _, tc := range []struct {
			name       string
			statusErr  error
			wantPrompt bool
		}{
			{name: "a host that needs a PIN is prompted", wantPrompt: true},
			{name: "a failed lookup fails the summary", statusErr: errors.New("bitlocker status unavailable")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ds := new(mock.Store)
				license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
				svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})
				ds.HasSelfServiceSoftwareInstallersFunc = func(ctx context.Context, platform string, teamID *uint) (bool, error) {
					return false, nil
				}
				ds.FailingPoliciesCountFunc = func(ctx context.Context, host *automdm.Host) (uint, uint, error) { return 0, 0, nil }
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					ac := &automdm.AppConfig{}
					ac.MDM.WindowsSettings.EnableDiskEncryption = optjson.SetBool(true)
					ac.MDM.RequireBitLockerPIN = optjson.SetBool(true)
					return ac, nil
				}
				ds.GetMDMWindowsHostConfigStateFunc = func(ctx context.Context, hostUUID string) (*automdm.MDMWindowsHostConfigState, error) {
					return &automdm.MDMWindowsHostConfigState{AutoMDMdBitLockerPINCapable: true}, nil
				}
				ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
					return &automdm.HostMDMDiskEncryption{ActionRequired: new(automdm.ActionRequiredCreatePIN)}, tc.statusErr
				}

				ctx = test.HostContext(ctx, &automdm.Host{ID: 1, UUID: "win-uuid", Platform: "windows", OsqueryHostID: new("win")})
				sum, err := svc.GetAutoMDMDesktopSummary(ctx)
				if tc.statusErr != nil {
					require.ErrorIs(t, err, tc.statusErr)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantPrompt, sum.Notifications.NeedsBitLockerPIN)
			})
		}
	})
}

func TestTriggerLinuxDiskEncryptionEscrow(t *testing.T) {
	t.Run("unavailable in AutoMDM Free", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})
		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, &automdm.Host{ID: 1})
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
	})

	t.Run("no-op on already pending", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			return &automdm.HostEscrowState{Pending: true}, nil
		}

		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, &automdm.Host{ID: 1})
		require.NoError(t, err)
		require.True(t, ds.GetHostEscrowStateFuncInvoked)
	})

	t.Run("conflict while the agent is still prompting for the previous request", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			require.Equal(t, uint(1), hostID)
			since := automdm.LinuxEscrowInFlightWindow - (90*time.Second + 300*time.Millisecond)
			return &automdm.HostEscrowState{SinceLastActivity: &since}, nil
		}

		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, &automdm.Host{ID: 1})
		var inFlightErr *automdm.LinuxEscrowInFlightError
		require.ErrorAs(t, err, &inFlightErr)
		require.Equal(t, http.StatusConflict, inFlightErr.StatusCode())
		require.Equal(t, automdm.LinuxEscrowInFlightMessage, inFlightErr.Error())
		// rounded up so the caller never retries a moment too early
		require.Equal(t, 91, inFlightErr.RetryAfter())
		require.True(t, ds.GetHostEscrowStateFuncInvoked)
		require.False(t, ds.QueueEscrowFuncInvoked)
		require.False(t, ds.ReportEscrowErrorFuncInvoked)
	})

	t.Run("encryption key is already escrowed", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		var reportedErrors []string
		host := &automdm.Host{ID: 1, Platform: "rhel", OSVersion: "Red Hat Enterprise Linux 9.0.0"}

		ds.ReportEscrowErrorFunc = func(ctx context.Context, hostID uint, err string) error {
			require.Equal(t, hostID, host.ID)
			reportedErrors = append(reportedErrors, err)
			return nil
		}

		orbitInfo := &automdm.HostOrbitInfo{Version: automdm.MinOrbitLUKSVersion}
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			return &automdm.HostEscrowState{}, nil
		}
		ds.GetHostOrbitInfoFunc = func(ctx context.Context, id uint) (*automdm.HostOrbitInfo, error) {
			return orbitInfo, nil
		}
		ds.AssertHasNoEncryptionKeyStoredFunc = func(ctx context.Context, hostID uint) error {
			return errors.New("encryption key is already escrowed")
		}

		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "encryption key is already escrowed")
		require.Len(t, reportedErrors, 0, "No error should be reported when key is already escrowed")
	})

	t.Run("validation failures", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			return &automdm.HostEscrowState{}, nil
		}
		var reportedErrors []string
		host := &automdm.Host{ID: 1, Platform: "rhel", OSVersion: "Red Hat Enterprise Linux 9.0.0"}
		ds.AssertHasNoEncryptionKeyStoredFunc = func(ctx context.Context, hostID uint) error { return nil }
		ds.ReportEscrowErrorFunc = func(ctx context.Context, hostID uint, err string) error {
			require.Equal(t, hostID, host.ID)
			reportedErrors = append(reportedErrors, err)
			return nil
		}

		// invalid platform
		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "AutoMDM does not yet support creating LUKS disk encryption keys on this platform.")
		require.True(t, ds.GetHostEscrowStateFuncInvoked)

		// Linux distributions AutoMDM does not escrow for, even though they are recognized platforms
		unsupportedLinux := []string{"arch", "cachyos", "debian", "pop"}
		for _, platform := range unsupportedLinux {
			host.Platform = platform
			host.OSVersion = platform
			err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
			require.ErrorContains(t, err, "AutoMDM does not yet support creating LUKS disk encryption keys on this platform.", platform)
		}

		// valid platform, no-team, encryption not enabled
		host.Platform = "rhel"
		host.OSVersion = "Fedora 32.0.0"
		appConfig := &automdm.AppConfig{MDM: automdm.MDM{EnableDiskEncryption: optjson.SetBool(false)}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appConfig, nil
		}
		err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Disk encryption is not enabled for hosts not assigned to a team.")

		// valid platform, team, encryption not enabled
		host.TeamID = ptr.Uint(1)
		teamConfig := &automdm.TeamMDM{}
		ds.TeamMDMConfigFunc = func(ctx context.Context, teamID uint) (*automdm.TeamMDM, error) {
			require.Equal(t, uint(1), teamID)
			return teamConfig, nil
		}
		err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Disk encryption is not enabled for this host's team.")

		// valid platform, team, host disk is not encrypted or unknown encryption state
		teamConfig = &automdm.TeamMDM{
			EnableDiskEncryption: true,
			LinuxSettings:        automdm.LinuxSettings{EnableEscrowDiskEncryptionKey: optjson.SetBool(true)},
		}
		err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Host's disk is not encrypted. Please encrypt your disk first.")
		host.DiskEncryptionEnabled = ptr.Bool(false)
		err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Host's disk is not encrypted. Please encrypt your disk first.")

		// No AutoMDM Desktop
		host.DiskEncryptionEnabled = ptr.Bool(true)
		orbitInfo := &automdm.HostOrbitInfo{Version: "1.35.1"}
		ds.GetHostOrbitInfoFunc = func(ctx context.Context, id uint) (*automdm.HostOrbitInfo, error) {
			return orbitInfo, nil
		}
		err = svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Your version of automdmd does not support creating disk encryption keys on Linux. Please upgrade automdmd, then click Refetch, then try again.")

		require.Len(t, reportedErrors, 6+len(unsupportedLinux))
	})

	t.Run("omarchy requires the automdmd that fixed the Hyprland prompt", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			return &automdm.HostEscrowState{}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{
				EnableDiskEncryption: optjson.SetBool(true),
				LinuxSettings:        automdm.LinuxSettings{EnableEscrowDiskEncryptionKey: optjson.SetBool(true)},
			}}, nil
		}
		ds.AssertHasNoEncryptionKeyStoredFunc = func(ctx context.Context, hostID uint) error { return nil }
		ds.ReportEscrowErrorFunc = func(ctx context.Context, hostID uint, err string) error { return nil }
		ds.QueueEscrowFunc = func(ctx context.Context, hostID uint) error { return nil }
		orbitVersion := "1.63.0"
		ds.GetHostOrbitInfoFunc = func(ctx context.Context, id uint) (*automdm.HostOrbitInfo, error) {
			return &automdm.HostOrbitInfo{Version: orbitVersion}, nil
		}
		host := &automdm.Host{ID: 1, Platform: "omarchy", DiskEncryptionEnabled: new(true)}

		// new enough for every other distribution, but not for Omarchy
		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.ErrorContains(t, err, "Your version of automdmd does not support creating disk encryption keys on Linux.")
		require.True(t, ds.ReportEscrowErrorFuncInvoked)
		require.False(t, ds.QueueEscrowFuncInvoked)

		orbitVersion = automdm.MinOrbitOmarchyLUKSVersion
		require.NoError(t, svc.TriggerLinuxDiskEncryptionEscrow(ctx, host))
		require.True(t, ds.QueueEscrowFuncInvoked)
	})

	t.Run("validation success", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true})
		ds.GetHostEscrowStateFunc = func(ctx context.Context, hostID uint) (*automdm.HostEscrowState, error) {
			return &automdm.HostEscrowState{}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{
				EnableDiskEncryption: optjson.SetBool(true),
				LinuxSettings:        automdm.LinuxSettings{EnableEscrowDiskEncryptionKey: optjson.SetBool(true)},
			}}, nil
		}
		ds.GetHostOrbitInfoFunc = func(ctx context.Context, id uint) (*automdm.HostOrbitInfo, error) {
			return &automdm.HostOrbitInfo{Version: "1.36.0", DesktopVersion: ptr.String("42")}, nil
		}
		ds.AssertHasNoEncryptionKeyStoredFunc = func(ctx context.Context, hostID uint) error {
			return nil
		}
		host := &automdm.Host{ID: 1, Platform: "ubuntu", DiskEncryptionEnabled: ptr.Bool(true), OrbitVersion: ptr.String(automdm.MinOrbitLUKSVersion)}
		ds.QueueEscrowFunc = func(ctx context.Context, hostID uint) error {
			require.Equal(t, uint(1), hostID)
			return nil
		}

		err := svc.TriggerLinuxDiskEncryptionEscrow(ctx, host)
		require.NoError(t, err)
		require.True(t, ds.QueueEscrowFuncInvoked)
	})
}

func TestAuthenticateDeviceRejectsIOSIPadOS(t *testing.T) {
	t.Run("error - iOS device attempting token auth", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		ds.LoadHostByDeviceAuthTokenFunc = func(ctx context.Context, authToken string, tokenTTL time.Duration) (*automdm.Host, error) {
			return &automdm.Host{
				ID:       1,
				UUID:     "ios-device-uuid",
				Platform: "ios",
			}, nil
		}

		host, debug, err := svc.AuthenticateDevice(ctx, "some-token")
		require.Error(t, err)
		require.Nil(t, host)
		require.False(t, debug)
		var authErr *automdm.AuthRequiredError
		require.ErrorAs(t, err, &authErr)
		require.Contains(t, authErr.Internal(), "iOS and iPadOS devices must use URL authentication")
	})

	t.Run("error - iPadOS device attempting token auth", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		ds.LoadHostByDeviceAuthTokenFunc = func(ctx context.Context, authToken string, tokenTTL time.Duration) (*automdm.Host, error) {
			return &automdm.Host{
				ID:       2,
				UUID:     "ipados-device-uuid",
				Platform: "ipados",
			}, nil
		}

		host, debug, err := svc.AuthenticateDevice(ctx, "some-token")
		require.Error(t, err)
		require.Nil(t, host)
		require.False(t, debug)
		var authErr *automdm.AuthRequiredError
		require.ErrorAs(t, err, &authErr)
		require.Contains(t, authErr.Internal(), "iOS and iPadOS devices must use URL authentication")
	})

	t.Run("success - macOS device with token auth", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		ds.LoadHostByDeviceAuthTokenFunc = func(ctx context.Context, authToken string, tokenTTL time.Duration) (*automdm.Host, error) {
			return &automdm.Host{
				ID:       3,
				UUID:     "macos-device-uuid",
				Platform: "darwin",
			}, nil
		}

		host, debug, err := svc.AuthenticateDevice(ctx, "some-token")
		require.NoError(t, err)
		require.NotNil(t, host)
		require.Equal(t, uint(3), host.ID)
		require.Equal(t, "darwin", host.Platform)
		require.False(t, debug)
	})

	t.Run("success - Windows device with token auth", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		ds.LoadHostByDeviceAuthTokenFunc = func(ctx context.Context, authToken string, tokenTTL time.Duration) (*automdm.Host, error) {
			return &automdm.Host{
				ID:       4,
				UUID:     "windows-device-uuid",
				Platform: "windows",
			}, nil
		}

		host, debug, err := svc.AuthenticateDevice(ctx, "some-token")
		require.NoError(t, err)
		require.NotNil(t, host)
		require.Equal(t, uint(4), host.ID)
		require.Equal(t, "windows", host.Platform)
		require.False(t, debug)
	})

	t.Run("success - Linux device with token auth", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{SkipCreateTestUsers: true})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		ds.LoadHostByDeviceAuthTokenFunc = func(ctx context.Context, authToken string, tokenTTL time.Duration) (*automdm.Host, error) {
			return &automdm.Host{
				ID:       5,
				UUID:     "linux-device-uuid",
				Platform: "ubuntu",
			}, nil
		}

		host, debug, err := svc.AuthenticateDevice(ctx, "some-token")
		require.NoError(t, err)
		require.NotNil(t, host)
		require.Equal(t, uint(5), host.ID)
		require.Equal(t, "ubuntu", host.Platform)
		require.False(t, debug)
	})
}
