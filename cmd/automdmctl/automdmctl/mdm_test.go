package automdmctl

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mdmmock "github.com/anilyagiz/automdm/v4/server/mock/mdm"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type testhost struct {
	host    *automdm.Host
	mdmInfo *automdm.HostMDM
}

func TestMDMRunCommand(t *testing.T) {
	// define some hosts to use in the tests
	hosts := []testhost{
		{
			host: &automdm.Host{
				ID:       1,
				UUID:     "mac-enrolled",
				Platform: "darwin",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			host: &automdm.Host{
				ID:       2,
				UUID:     "win-enrolled",
				Platform: "windows",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			host: &automdm.Host{
				ID:       3,
				UUID:     "mac-unenrolled",
				Platform: "darwin",
			},
		},
		{
			host: &automdm.Host{
				ID:       4,
				UUID:     "win-unenrolled",
				Platform: "windows",
			},
		},
		{
			host: &automdm.Host{
				ID:       5,
				UUID:     "linux-unenrolled",
				Platform: "linux",
			},
		},
		{
			host: &automdm.Host{
				ID:       6,
				UUID:     "mac-enrolled-2",
				Platform: "darwin",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			host: &automdm.Host{
				ID:       7,
				UUID:     "win-enrolled-2",
				Platform: "windows",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			host: &automdm.Host{
				ID:       8,
				UUID:     "mac-non-automdm-enrolled",
				Platform: "darwin",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMJamf, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(false)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMJamf},
		},
		{
			host: &automdm.Host{
				ID:       9,
				UUID:     "win-non-automdm-enrolled",
				Platform: "windows",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMIntune, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(false)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMIntune},
		},
		{
			host: &automdm.Host{
				ID:       10,
				UUID:     "mac-pending",
				Platform: "darwin",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			host: &automdm.Host{
				ID:       11,
				UUID:     "win-pending",
				Platform: "windows",
				MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
		},
		{
			// ABM and Autopilot pending hosts have no UUID until they enroll.
			host: &automdm.Host{
				ID:             12,
				HardwareSerial: "abm-pending-serial",
				Platform:       "darwin",
				MDM:            automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("Pending"), ConnectedToAutoMDM: new(false)},
			},
			mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
		},
	}
	hostByUUID := make(map[string]testhost)
	hostByID := make(map[uint]testhost)
	for _, h := range hosts {
		hostByUUID[cmp.Or(h.host.UUID, h.host.HardwareSerial)] = h
		hostByID[h.host.ID] = h
	}

	// define some files to use in the tests
	yamlFilePath := writeTmpYml(t, `invalid`)
	mobileConfigFilePath := writeTmpMobileconfig(t, "Mobileconfig")
	appleCmdFilePath := writeTmpAppleMDMCmd(t, "FooBar")
	winCmdFilePath := writeTmpWindowsMDMCmd(t, "FooBar")
	applePremiumCmdFilePath := writeTmpAppleMDMCmd(t, "EraseDevice")
	winPremiumCmdFilePath := writeTmpWindowsMDMCmd(t, "./Device/Vendor/MSFT/RemoteWipe/doWipe")

	emptyAppleCmdFilePath, err := os.CreateTemp(t.TempDir(), "*.xml")
	require.NoError(t, err)
	_, err = emptyAppleCmdFilePath.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
</plist>`)
	require.NoError(t, err)
	emptyAppleCmdFilePath.Close()

	emptyWinCmdFilePath, err := os.CreateTemp(t.TempDir(), "*.xml")
	require.NoError(t, err)
	_, err = emptyWinCmdFilePath.WriteString(`<Exec>
</Exec>`)
	require.NoError(t, err)
	emptyWinCmdFilePath.Close()

	nonExecWinCmdFilePath, err := os.CreateTemp(t.TempDir(), "*.xml")
	require.NoError(t, err)
	_, err = nonExecWinCmdFilePath.WriteString(`<Get>
	<CmdID>22</CmdID>
	<Item>
		<Target>
			<LocURI>FooBar</LocURI>
		</Target>
		<Meta>
			<Format xmlns="syncml:metinf">chr</Format>
			<Type xmlns="syncml:metinf">text/plain</Type>
		</Meta>
		<Data>NamedValuesList=MinPasswordLength,8;</Data>
	</Item>
</Get>`)
	require.NoError(t, err)
	nonExecWinCmdFilePath.Close()

	// define some app configs variations to use in the tests
	appCfgAllMDM := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}
	appCfgWinMDM := &automdm.AppConfig{MDM: automdm.MDM{WindowsEnabledAndConfigured: true}}
	appCfgMacMDM := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}
	appCfgNoMDM := &automdm.AppConfig{MDM: automdm.MDM{}}

	for _, lic := range []string{automdm.TierFree, automdm.TierPremium} {
		t.Run(lic, func(t *testing.T) {
			enqueuer := new(mdmmock.MDMAppleStore)
			license := &automdm.LicenseInfo{Tier: lic, Expiration: time.Now().Add(24 * time.Hour)}

			_, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
				MDMStorage:       enqueuer,
				MDMPusher:        testing_utils.MockPusher{},
				License:          license,
				NoCacheDatastore: true,
			})

			ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
				h, ok := hostByUUID[identifier]
				if !ok {
					return 0, &notFoundError{}
				}
				return h.host.ID, nil
			}
			ds.HostFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
				h, ok := hostByID[hostID]
				if !ok {
					return nil, &notFoundError{}
				}
				return h.host, nil
			}
			ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostID uint) (time.Time, error) {
				return time.Now(), nil
			}
			ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
				return nil
			}
			ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
				return nil, nil
			}
			ds.ListHostBatteriesFunc = func(ctx context.Context, id uint) ([]*automdm.HostBattery, error) {
				return nil, nil
			}
			ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
				return nil, nil
			}
			ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
				return nil, nil
			}
			ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
				return nil, nil
			}
			ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
				return nil, nil
			}
			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMAppleProfile, error) {
				return nil, nil
			}
			ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
				return true, nil
			}
			ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
				return nil, nil
			}
			ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
				return automdm.DiskEncryptionConfig{}, nil
			}
			ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMWindowsProfile, error) {
				return nil, nil
			}
			ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDMMacOSSetup, error) {
				return nil, nil
			}
			ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
				return &automdm.HostLockWipeStatus{}, nil
			}
			ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
				return nil, nil
			}
			ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, filter automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
				if len(uuids) == 0 {
					return nil, nil
				}
				hosts := make([]*automdm.Host, 0, len(uuids))
				for _, uid := range uuids {
					if h := hostByUUID[uid]; h.host != nil {
						hosts = append(hosts, h.host)
					}
				}
				return hosts, nil
			}
			winCmds := map[string]struct{}{}
			ds.MDMWindowsInsertCommandForHostsFunc = func(ctx context.Context, deviceIDs []string, cmd *automdm.MDMWindowsCommand) error {
				// every command uuid is different
				require.NotContains(t, winCmds, cmd.CommandUUID)
				winCmds[cmd.CommandUUID] = struct{}{}
				return nil
			}
			ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
				return &automdm.HostMDMDiskEncryption{}, nil
			}
			ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
				h, ok := hostByID[hostID]
				require.True(t, ok)
				if h.mdmInfo == nil {
					return nil, &notFoundError{}
				}
				return h.mdmInfo, nil
			}
			ds.AreHostsConnectedToAutoMDMMDMFunc = func(ctx context.Context, hosts []*automdm.Host) (map[string]bool, error) {
				res := make(map[string]bool, len(hosts))
				for _, h := range hosts {
					res[h.UUID] = h.MDM.ConnectedToAutoMDM != nil && *h.MDM.ConnectedToAutoMDM
				}
				return res, nil
			}
			ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
				return &automdm.NanoMDMEnrollmentDetails{}, nil
			}
			ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
				return false, nil
			}
			ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
				return &automdm.HostDEPAssignment{}, nil
			}
			ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
				return nil, nil
			}

			enqueuer.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
				return map[string]error{}, nil
			}

			cases := []struct {
				desc    string
				flags   []string
				appCfg  *automdm.AppConfig
				wantErr string
			}{
				{"no flags", nil, appCfgAllMDM, `Required flags "hosts, payload" not set`},
				{"no payload", []string{"--hosts", "abc"}, appCfgAllMDM, `Required flag "payload" not set`},
				{"no hosts", []string{"--payload", winCmdFilePath}, appCfgAllMDM, `Required flag "hosts" not set`},
				{"invalid payload", []string{"--hosts", "abc", "--payload", "no-such-file"}, appCfgAllMDM, `open no-such-file: no such file or directory`},
				{"macOS yaml payload", []string{"--hosts", "mac-enrolled", "--payload", yamlFilePath}, appCfgAllMDM, `The payload isn't valid XML`},
				{"win yaml payload", []string{"--hosts", "win-enrolled", "--payload", yamlFilePath}, appCfgAllMDM, `The payload isn't valid XML`},
				{"non-mdm-command plist payload", []string{"--hosts", "mac-enrolled", "--payload", mobileConfigFilePath}, appCfgAllMDM, `The payload isn't valid. Please provide a valid MDM command in the form of a plist-encoded XML file:`},
				{"single host not found", []string{"--hosts", "no-such-host", "--payload", appleCmdFilePath}, appCfgAllMDM, automdm.TargetedHostsDontExistErrMsg},
				{"unenrolled macOS host", []string{"--hosts", "mac-unenrolled", "--payload", appleCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"unenrolled windows host", []string{"--hosts", "win-unenrolled", "--payload", winCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"macOS non-automdm host", []string{"--hosts", "mac-non-automdm-enrolled", "--payload", appleCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"windows non-automdm host", []string{"--hosts", "win-non-automdm-enrolled", "--payload", winCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"macOS pending host", []string{"--hosts", "mac-pending", "--payload", appleCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"windows pending host", []string{"--hosts", "win-pending", "--payload", winCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"pending host without UUID", []string{"--hosts", "abm-pending-serial", "--payload", appleCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"valid single mac", []string{"--hosts", "mac-enrolled", "--payload", appleCmdFilePath}, appCfgAllMDM, ""},
				{"valid single windows", []string{"--hosts", "win-enrolled", "--payload", winCmdFilePath}, appCfgAllMDM, ""},
				{"no mdm enabled", []string{"--hosts", "win-enrolled", "--payload", winCmdFilePath}, appCfgNoMDM, "MDM features aren't turned on in AutoMDM."},
				{"valid single mac only win mdm", []string{"--hosts", "mac-enrolled", "--payload", appleCmdFilePath}, appCfgWinMDM, "macOS MDM isn't turned on."},
				{"valid single win only mac mdm", []string{"--hosts", "win-enrolled", "--payload", winCmdFilePath}, appCfgMacMDM, "Windows MDM isn't turned on."},
				{"macOS premium cmd", []string{"--hosts", "mac-enrolled", "--payload", applePremiumCmdFilePath}, appCfgAllMDM, func() string {
					if lic == automdm.TierFree {
						return `missing or invalid license`
					}
					return ""
				}()},
				{"windows premium cmd", []string{"--hosts", "win-enrolled", "--payload", winPremiumCmdFilePath}, appCfgAllMDM, func() string {
					if lic == automdm.TierFree {
						return `Missing or invalid license. Wipe command is available in AutoMDM Premium only.`
					}
					return ""
				}()},
				{"empty plist file", []string{"--hosts", "mac-enrolled", "--payload", emptyAppleCmdFilePath.Name()}, appCfgAllMDM, `The payload isn't valid. Please provide a valid MDM command in the form of a plist-encoded XML file.`},
				{"non-Exec win file", []string{"--hosts", "win-enrolled", "--payload", nonExecWinCmdFilePath.Name()}, appCfgAllMDM, `You can run only <Exec> command type.`},
				{"empty win file", []string{"--hosts", "win-enrolled", "--payload", emptyWinCmdFilePath.Name()}, appCfgAllMDM, `You can run only a single <Exec> command.`},
				{"hosts with different platforms", []string{"--hosts", "win-enrolled,mac-enrolled", "--payload", winCmdFilePath}, appCfgAllMDM, `Command can't run on hosts with different platforms.`},
				{"all hosts not found", []string{"--hosts", "no-such-1,no-such-2,no-such-3", "--payload", winCmdFilePath}, appCfgAllMDM, automdm.TargetedHostsDontExistErrMsg},
				{"one host not found", []string{"--hosts", "win-enrolled,no-such-2,win-enrolled-2", "--payload", winCmdFilePath}, appCfgAllMDM, `One or more targeted hosts don't exist.`},
				{"one windows host not enrolled", []string{"--hosts", "win-enrolled,win-unenrolled,win-enrolled-2", "--payload", winCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"one macOS host not enrolled", []string{"--hosts", "mac-enrolled,mac-unenrolled,mac-enrolled-2", "--payload", appleCmdFilePath}, appCfgAllMDM, `Can't run the MDM command because one or more hosts have MDM turned off.`},
				{"valid multiple mac", []string{"--hosts", "mac-enrolled,mac-enrolled-2", "--payload", appleCmdFilePath}, appCfgAllMDM, ""},
				{"valid multiple windows", []string{"--hosts", "win-enrolled,win-enrolled-2", "--payload", winCmdFilePath}, appCfgAllMDM, ""},
				{"valid multiple mac mac-enabled only", []string{"--hosts", "mac-enrolled,mac-enrolled-2", "--payload", appleCmdFilePath}, appCfgMacMDM, ""},
				{"valid multiple windows win-enabled only", []string{"--hosts", "win-enrolled,win-enrolled-2", "--payload", winCmdFilePath}, appCfgWinMDM, ""},
			}
			for _, c := range cases {
				t.Run(c.desc, func(t *testing.T) {
					ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
						return c.appCfg, nil
					}

					buf, err := runAppNoChecks(append([]string{"mdm", "run-command"}, c.flags...))
					if c.wantErr != "" {
						require.Error(t, err)
						require.ErrorContains(t, err, c.wantErr)
					} else {
						require.NoError(t, err)
						require.Contains(t, buf.String(), `Hosts will run the command the next time they check into AutoMDM.`)
						require.Contains(t, buf.String(), `automdmctl get mdm-command-results --id=`)
					}
				})
			}

			t.Run("gitops user", func(t *testing.T) {
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return appCfgAllMDM, nil
				}
				ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
					return &automdm.User{ID: id, GlobalRole: new(automdm.RoleGitOps)}, nil
				}
				ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
					return nil, nil
				}
				_, err := runAppNoChecks([]string{"mdm", "run-command", "--hosts", "mac-enrolled", "--payload", appleCmdFilePath})
				require.ErrorContains(t, err, automdm.GitOpsMDMCommandErrorMessage)
			})
		})
	}
}

func TestMDMLockCommand(t *testing.T) {
	macEnrolled := testhost{
		host: &automdm.Host{
			ID:       1,
			UUID:     "mac-enrolled",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolled := testhost{
		host: &automdm.Host{
			ID:       2,
			UUID:     "win-enrolled",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}

	linuxEnrolled := testhost{
		host: &automdm.Host{
			ID:       3,
			UUID:     "linux-enrolled",
			Platform: "linux",
		},
	}
	winNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       4,
			UUID:     "win-not-enrolled",
			Platform: "windows",
		},
	}
	macNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       5,
			UUID:     "mac-not-enrolled",
			Platform: "darwin",
		},
	}
	macPending := testhost{
		host: &automdm.Host{
			ID:       6,
			UUID:     "mac-pending",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winPending := testhost{
		host: &automdm.Host{
			ID:       7,
			UUID:     "win-pending",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledUP := testhost{
		host: &automdm.Host{
			ID:       8,
			UUID:     "win-enrolled-up",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       10,
			UUID:     "win-enrolled-lp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       11,
			UUID:     "mac-enrolled-lp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       12,
			UUID:     "win-enrolled-wp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       13,
			UUID:     "mac-enrolled-wp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}

	androidEnrolled := testhost{
		host: &automdm.Host{
			ID:       16,
			UUID:     "android-enrolled",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (personally owned)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	androidNotConnected := testhost{
		host: &automdm.Host{
			ID:       15,
			UUID:     "android-not-connected",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("Off"), ConnectedToAutoMDM: new(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}

	hostByUUID := make(map[string]testhost)
	hostsByID := make(map[uint]testhost)
	for _, h := range []testhost{
		winEnrolled,
		macEnrolled,
		linuxEnrolled,
		macNotEnrolled,
		winNotEnrolled,
		macPending,
		winPending,
		winEnrolledUP,
		winEnrolledLP,
		macEnrolledLP,
		winEnrolledWP,
		macEnrolledWP,
		androidNotConnected,
		androidEnrolled,
	} {
		hostByUUID[h.host.UUID] = h
		hostsByID[h.host.ID] = h
	}

	unlockPending := map[uint]testhost{
		winEnrolledUP.host.ID: winEnrolledUP,
	}

	lockPending := map[uint]testhost{
		winEnrolledLP.host.ID: winEnrolledLP,
		macEnrolledLP.host.ID: macEnrolledLP,
	}

	wipePending := map[uint]testhost{
		winEnrolledWP.host.ID: winEnrolledWP,
		macEnrolledWP.host.ID: macEnrolledWP,
	}

	androidModule := &fakeAndroidModule{}
	ds := setupTestServerWithAndroid(t, androidModule)
	setupDSMocks(ds, hostByUUID, hostsByID)

	// custom ds mocks for these tests
	ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		automdmPlatform := host.AutoMDMPlatform()

		var status automdm.HostLockWipeStatus
		status.HostAutoMDMPlatform = automdmPlatform

		if _, ok := unlockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				return nil, errors.New("apple devices do not have an unlock pending state")
			}

			status.UnlockScript = &automdm.HostScriptResult{}
		}

		if _, ok := lockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				status.LockMDMCommand = &automdm.MDMCommand{}
				return &status, nil
			}

			status.LockScript = &automdm.HostScriptResult{}
		}

		if _, ok := wipePending[host.ID]; ok {
			if automdmPlatform == "linux" {
				status.WipeScript = &automdm.HostScriptResult{ExitCode: nil}
				return &status, nil
			}

			status.WipeMDMCommand = &automdm.MDMCommand{}
			status.WipeMDMCommandResult = nil
			return &status, nil
		}

		return &status, nil
	}
	ds.LockHostViaScriptFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload, platform string) error {
		return nil
	}

	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		h, ok := hostsByID[hostID]
		if !ok || h.mdmInfo == nil {
			return nil, &notFoundError{}
		}

		return h.mdmInfo, nil
	}

	ds.GetHostOrbitInfoFunc = func(ctx context.Context, hostID uint) (*automdm.HostOrbitInfo, error) {
		hostIDMod := hostID % 3
		switch hostIDMod {
		case 0:
			return nil, &notFoundError{}
		case 1:
			return &automdm.HostOrbitInfo{}, nil
		case 2:
			return &automdm.HostOrbitInfo{ScriptsEnabled: ptr.Bool(true)}, nil
		default:
			t.Errorf("unexpected hostIDMod %v", hostIDMod)
			return nil, nil
		}
	}

	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		mdmInfo := hostsByID[host.ID].mdmInfo
		return mdmInfo != nil && mdmInfo.Enrolled == true && mdmInfo.Name == automdm.WellKnownMDMAutoMDM, nil
	}

	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}

	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{}, nil
	}

	appCfgAllMDM, appCfgWinMDM, appCfgMacMDM, appCfgNoMDM := setupAppConigs()

	successfulOutput := func(ident string) string {
		return fmt.Sprintf(`
The host will lock when it comes online.

Copy and run this command to see lock status:

automdmctl get host %s

When you're ready to unlock the host, copy and run this command:

automdmctl mdm unlock --host=%s

`, ident, ident)
	}

	cases := []struct {
		appCfg  *automdm.AppConfig
		desc    string
		flags   []string
		wantErr string
	}{
		{appCfgAllMDM, "no flags", nil, `Required flag "host" not set`},
		{appCfgAllMDM, "host flag empty", []string{"--host", ""}, `No host targeted. Please provide --host.`},
		{appCfgAllMDM, "lock non-existent host", []string{"--host", "notfound"}, automdm.HostNotFoundErrMsg},
		{appCfgMacMDM, "valid windows but only macos mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgWinMDM, "valid macos but only windows mdm", []string{"--host", macEnrolled.host.UUID}, `macOS MDM isn't turned on.`},
		{appCfgAllMDM, "valid windows", []string{"--host", winEnrolled.host.UUID}, ""},
		{appCfgAllMDM, "valid macos", []string{"--host", macEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid linux", []string{"--host", linuxEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid windows but no mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgNoMDM, "valid macos but no mdm", []string{"--host", macEnrolled.host.UUID}, `macOS MDM isn't turned on.`},
		{appCfgMacMDM, "valid macos but not enrolled", []string{"--host", macNotEnrolled.host.UUID}, `Can't lock the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but not enrolled", []string{"--host", winNotEnrolled.host.UUID}, `Can't lock the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but pending ", []string{"--host", winPending.host.UUID}, `Can't lock the host because it doesn't have MDM turned on.`},
		{appCfgMacMDM, "valid macos but pending", []string{"--host", macPending.host.UUID}, `Can't lock the host because it doesn't have MDM turned on.`},
		{appCfgAllMDM, "valid windows but pending unlock", []string{"--host", winEnrolledUP.host.UUID}, "Host has pending unlock request."},
		{appCfgAllMDM, "valid windows but pending lock", []string{"--host", winEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid macos but pending lock", []string{"--host", macEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid windows but pending wipe", []string{"--host", winEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid macos but pending wipe", []string{"--host", macEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid android but not connected", []string{"--host", androidNotConnected.host.UUID}, `Can't lock the host because it doesn't have MDM turned on.`},
	}

	runTestCases(t, ds, "lock", successfulOutput, cases)

	t.Run("android", func(t *testing.T) {
		appCfgAndroid := &automdm.AppConfig{MDM: automdm.MDM{AndroidEnabledAndConfigured: true}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appCfgAndroid, nil
		}

		buf, err := runAppNoChecks([]string{"mdm", "lock", "--host", androidEnrolled.host.UUID})
		require.NoError(t, err)
		require.Equal(t, `
The host will lock when it comes online.

To view the unlock PIN, copy and run this command:

automdmctl mdm unlock --host=android-enrolled

`, buf.String())
		require.Equal(t, []uint{androidEnrolled.host.ID}, androidModule.lockedHostIDs)
	})

	t.Run("gitops user", func(t *testing.T) {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appCfgAllMDM, nil
		}
		ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return &automdm.User{ID: id, GlobalRole: new(automdm.RoleGitOps)}, nil
		}
		ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
			return nil, nil
		}
		for _, action := range []string{"lock", "unlock", "clear-passcode", "wipe"} {
			t.Run(action, func(t *testing.T) {
				_, err := runAppNoChecks([]string{"mdm", action, "--host", macEnrolled.host.UUID})
				require.ErrorContains(t, err, automdm.GitOpsMDMCommandErrorMessage)
			})
		}
	})
}

func TestMDMUnlockCommand(t *testing.T) {
	macEnrolled := testhost{
		host: &automdm.Host{
			ID:       1,
			UUID:     "mac-enrolled",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolled := testhost{
		host: &automdm.Host{
			ID:       2,
			UUID:     "win-enrolled",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	linuxEnrolled := testhost{
		host: &automdm.Host{
			ID:       3,
			UUID:     "linux-enrolled",
			Platform: "linux",
		},
	}
	winNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       4,
			UUID:     "win-not-enrolled",
			Platform: "windows",
		},
	}
	macNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       5,
			UUID:     "mac-not-enrolled",
			Platform: "darwin",
		},
	}
	macPending := testhost{
		host: &automdm.Host{
			ID:       6,
			UUID:     "mac-pending",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winPending := testhost{
		host: &automdm.Host{
			ID:       7,
			UUID:     "win-pending",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledUP := testhost{
		host: &automdm.Host{
			ID:       8,
			UUID:     "win-enrolled-up",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       10,
			UUID:     "win-enrolled-lp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       11,
			UUID:     "mac-enrolled-lp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       12,
			UUID:     "win-enrolled-wp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       13,
			UUID:     "mac-enrolled-wp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	androidLocked := testhost{
		host: &automdm.Host{
			ID:       16,
			UUID:     "android-locked",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (personally owned)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	androidLockPending := testhost{
		host: &automdm.Host{
			ID:       17,
			UUID:     "android-lock-pending",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (personally owned)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	androidNeverLocked := testhost{
		host: &automdm.Host{
			ID:       18,
			UUID:     "android-never-locked",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (personally owned)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	androidNotConnected := testhost{
		host: &automdm.Host{
			ID:       15,
			UUID:     "android-not-connected",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("Off"), ConnectedToAutoMDM: new(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}

	hostByUUID := make(map[string]testhost)
	hostsByID := make(map[uint]testhost)
	for _, h := range []testhost{
		winEnrolled,
		macEnrolled,
		linuxEnrolled,
		macNotEnrolled,
		winNotEnrolled,
		macPending,
		winPending,
		winEnrolledUP,
		winEnrolledLP,
		macEnrolledLP,
		winEnrolledWP,
		macEnrolledWP,
		androidNotConnected,
		androidLocked,
		androidLockPending,
		androidNeverLocked,
	} {
		hostByUUID[h.host.UUID] = h
		hostsByID[h.host.ID] = h
	}

	locked := map[uint]testhost{
		winEnrolled.host.ID:   winEnrolled,
		macEnrolled.host.ID:   macEnrolled,
		linuxEnrolled.host.ID: linuxEnrolled,
	}

	unlockPending := map[uint]testhost{
		winEnrolledUP.host.ID: winEnrolledUP,
	}

	lockPending := map[uint]testhost{
		winEnrolledLP.host.ID: winEnrolledLP,
		macEnrolledLP.host.ID: macEnrolledLP,
	}

	wipePending := map[uint]testhost{
		winEnrolledWP.host.ID: winEnrolledWP,
		macEnrolledWP.host.ID: macEnrolledWP,
	}

	ds := setupTestServer(t)
	setupDSMocks(ds, hostByUUID, hostsByID)

	// custom mocks for these test
	ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		automdmPlatform := host.AutoMDMPlatform()

		var status automdm.HostLockWipeStatus
		status.HostAutoMDMPlatform = automdmPlatform
		if _, ok := locked[host.ID]; ok {
			if automdmPlatform == "darwin" {
				status.LockMDMCommand = &automdm.MDMCommand{}
				status.LockMDMCommandResult = &automdm.MDMCommandResult{Status: automdm.MDMAppleStatusAcknowledged}
				return &status, nil
			}

			status.LockScript = &automdm.HostScriptResult{ExitCode: ptr.Int64(0)}
		}

		if _, ok := unlockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				return nil, errors.New("apple devices do not have an unlock pending state")
			}

			status.UnlockScript = &automdm.HostScriptResult{}
		}

		if _, ok := lockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				status.LockMDMCommand = &automdm.MDMCommand{}
				return &status, nil
			}

			status.LockScript = &automdm.HostScriptResult{}
		}

		if _, ok := wipePending[host.ID]; ok {
			if automdmPlatform == "linux" {
				status.WipeScript = &automdm.HostScriptResult{ExitCode: nil}
				return &status, nil
			}

			status.WipeMDMCommand = &automdm.MDMCommand{}
			status.WipeMDMCommandResult = nil
			return &status, nil
		}

		return &status, nil
	}
	ds.UnlockHostViaScriptFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload, platform string) error {
		return nil
	}
	ds.UnlockHostManuallyFunc = func(ctx context.Context, hostID uint, platform string, ts time.Time) error {
		return nil
	}

	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		h, ok := hostsByID[hostID]
		if !ok || h.mdmInfo == nil {
			return nil, &notFoundError{}
		}

		return h.mdmInfo, nil
	}

	ds.GetHostOrbitInfoFunc = func(ctx context.Context, hostID uint) (*automdm.HostOrbitInfo, error) {
		hostIDMod := hostID % 3
		switch hostIDMod {
		case 0:
			return nil, &notFoundError{}
		case 1:
			return &automdm.HostOrbitInfo{}, nil
		case 2:
			return &automdm.HostOrbitInfo{ScriptsEnabled: ptr.Bool(true)}, nil
		default:
			t.Errorf("unexpected hostIDMod %v", hostIDMod)
			return nil, nil
		}
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return host.MDM.ConnectedToAutoMDM != nil && *host.MDM.ConnectedToAutoMDM, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}

	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{}, nil
	}

	appCfgAllMDM, appCfgWinMDM, appCfgMacMDM, appCfgNoMDM := setupAppConigs()

	successfulOutput := func(ident string) string {
		h := hostByUUID[ident]
		if h.host.Platform == "darwin" {
			return `Use this 6 digit PIN to unlock the host:`
		}
		return fmt.Sprintf(`
The host will unlock when it comes online.

Copy and run this command to see results:

automdmctl get host %s

`, ident)
	}

	cases := []struct {
		appCfg  *automdm.AppConfig
		desc    string
		flags   []string
		wantErr string
	}{
		{appCfgAllMDM, "no flags", nil, `Required flag "host" not set`},
		{appCfgAllMDM, "host flag empty", []string{"--host", ""}, `No host targeted. Please provide --host.`},
		{appCfgAllMDM, "unlock non-existent host", []string{"--host", "notfound"}, automdm.HostNotFoundErrMsg},
		{appCfgMacMDM, "valid windows but only macos mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgAllMDM, "valid windows", []string{"--host", winEnrolled.host.UUID}, ""},
		{appCfgAllMDM, "valid macos", []string{"--host", macEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid linux", []string{"--host", linuxEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid windows but no mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		// TODO: should we error here?
		// {appCfgNoMDM, "valid macos but no mdm", []string{"--host", macEnrolled.host.UUID}, `macOS MDM isn't turned on.`},
		{appCfgMacMDM, "valid macos but not enrolled", []string{"--host", macNotEnrolled.host.UUID}, `Can't unlock the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but not enrolled", []string{"--host", winNotEnrolled.host.UUID}, `Can't unlock the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but pending mdm enroll", []string{"--host", winPending.host.UUID}, `Can't unlock the host because it doesn't have MDM turned on.`},
		{appCfgMacMDM, "valid macos but pending mdm enroll", []string{"--host", macPending.host.UUID}, `Can't unlock the host because it doesn't have MDM turned on.`},
		{appCfgAllMDM, "valid windows but pending unlock", []string{"--host", winEnrolledUP.host.UUID}, "Host has pending unlock request."},
		{appCfgAllMDM, "valid windows but pending lock", []string{"--host", winEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid macos but pending lock", []string{"--host", macEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid windows but pending wipe", []string{"--host", winEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid macos but pending wipe", []string{"--host", macEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid android but not connected", []string{"--host", androidNotConnected.host.UUID}, `Can't unlock the host because it doesn't have MDM turned on.`},
	}

	runTestCases(t, ds, "unlock", successfulOutput, cases)

	t.Run("android", func(t *testing.T) {
		lockedAt := time.Date(2026, 11, 20, 15, 4, 5, 0, time.UTC)
		androidLockStatus := func() *automdm.HostLockWipeStatus {
			return &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform:  "android",
				UnlockPIN:            "654321",
				LockMDMCommand:       &automdm.MDMCommand{RequestType: string(android.MDMAndroidCommandTypeResetPassword)},
				LockMDMCommandResult: &automdm.MDMCommandResult{Status: string(android.MDMAndroidCommandStatusAcknowledged), UpdatedAt: lockedAt},
			}
		}
		ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
			switch host.ID {
			case androidLocked.host.ID:
				return androidLockStatus(), nil
			case androidLockPending.host.ID:
				status := androidLockStatus()
				status.LockMDMCommandResult = nil
				return status, nil
			default:
				return &automdm.HostLockWipeStatus{HostAutoMDMPlatform: "android"}, nil
			}
		}
		appCfgAndroid := &automdm.AppConfig{MDM: automdm.MDM{AndroidEnabledAndConfigured: true}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appCfgAndroid, nil
		}

		t.Run("locked", func(t *testing.T) {
			buf, err := runAppNoChecks([]string{"mdm", "unlock", "--host", androidLocked.host.UUID})
			require.NoError(t, err)
			require.Equal(t,
				fmt.Sprintf("\nThis host was locked at %s with the PIN 654321.\n\n", lockedAt.Local().Format(time.DateTime)),
				buf.String())
		})

		t.Run("pending lock", func(t *testing.T) {
			buf, err := runAppNoChecks([]string{"mdm", "unlock", "--host", androidLockPending.host.UUID})
			require.ErrorContains(t, err, "Host has pending lock request.")
			require.Empty(t, buf.String())
		})

		t.Run("never locked", func(t *testing.T) {
			buf, err := runAppNoChecks([]string{"mdm", "unlock", "--host", androidNeverLocked.host.UUID})
			require.ErrorContains(t, err, "Host is already unlocked.")
			require.Empty(t, buf.String())
		})
	})
}

func TestMDMWipeCommand(t *testing.T) {
	macEnrolled := testhost{
		host: &automdm.Host{
			ID:       1,
			UUID:     "mac-enrolled",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolled := testhost{
		host: &automdm.Host{
			ID:       2,
			UUID:     "win-enrolled",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       4,
			UUID:     "win-not-enrolled",
			Platform: "windows",
		},
	}
	macNotEnrolled := testhost{
		host: &automdm.Host{
			ID:       5,
			UUID:     "mac-not-enrolled",
			Platform: "darwin",
		},
	}
	macPending := testhost{
		host: &automdm.Host{
			ID:       6,
			UUID:     "mac-pending",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winPending := testhost{
		host: &automdm.Host{
			ID:       7,
			UUID:     "win-pending",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("Pending"), ConnectedToAutoMDM: ptr.Bool(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledUP := testhost{
		host: &automdm.Host{
			ID:       8,
			UUID:     "win-enrolled-up",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       10,
			UUID:     "win-enrolled-lp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledLP := testhost{
		host: &automdm.Host{
			ID:       11,
			UUID:     "mac-enrolled-lp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       12,
			UUID:     "win-enrolled-wp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledWP := testhost{
		host: &automdm.Host{
			ID:       13,
			UUID:     "mac-enrolled-wp",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledWiped := testhost{
		host: &automdm.Host{
			ID:       14,
			UUID:     "win-enrolled-wiped",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledWiped := testhost{
		host: &automdm.Host{
			ID:       15,
			UUID:     "mac-enrolled-wiped",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledLocked := testhost{
		host: &automdm.Host{
			ID:       16,
			UUID:     "win-enrolled-locked",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macEnrolledLocked := testhost{
		host: &automdm.Host{
			ID:       17,
			UUID:     "mac-enrolled-locked",
			Platform: "darwin",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual"), ConnectedToAutoMDM: ptr.Bool(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	linuxEnrolled := testhost{
		host: &automdm.Host{
			ID:       18,
			UUID:     "linux-enrolled",
			Platform: "linux",
		},
	}
	linuxEnrolled2 := testhost{
		host: &automdm.Host{
			ID:       19,
			UUID:     "linux-enrolled",
			Platform: "linux",
		},
	}
	linuxEnrolled3 := testhost{
		host: &automdm.Host{
			ID:       20,
			UUID:     "linux-enrolled",
			Platform: "linux",
		},
	}
	androidNotConnected := testhost{
		host: &automdm.Host{
			ID:       21,
			UUID:     "android-not-connected",
			Platform: "android",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("Off"), ConnectedToAutoMDM: new(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledRP := testhost{
		host: &automdm.Host{
			ID:       22,
			UUID:     "win-enrolled-rp",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (manual)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	winEnrolledReset := testhost{
		host: &automdm.Host{
			ID:       23,
			UUID:     "win-enrolled-reset",
			Platform: "windows",
			MDM:      automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("On (manual)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}

	linuxHostIDs := []uint{linuxEnrolled.host.ID, linuxEnrolled2.host.ID, linuxEnrolled3.host.ID}

	hostByUUID := make(map[string]testhost)
	hostsByID := make(map[uint]testhost)
	for _, h := range []testhost{
		winEnrolled,
		macEnrolled,
		linuxEnrolled,
		linuxEnrolled2,
		linuxEnrolled3,
		macNotEnrolled,
		winNotEnrolled,
		macPending,
		winPending,
		winEnrolledUP,
		winEnrolledLP,
		macEnrolledLP,
		winEnrolledWP,
		macEnrolledWP,
		winEnrolledWiped,
		macEnrolledWiped,
		winEnrolledLocked,
		macEnrolledLocked,
		androidNotConnected,
		winEnrolledRP,
		winEnrolledReset,
	} {
		hostByUUID[h.host.UUID] = h
		hostsByID[h.host.ID] = h
	}

	locked := map[uint]testhost{
		winEnrolledLocked.host.ID: winEnrolledLocked,
		macEnrolledLocked.host.ID: macEnrolledLocked,
	}
	resetCmd := &automdm.MDMCommand{RequestType: automdm.WindowsRemoteWipeLocURI(automdm.WindowsRemoteWipePersistProvisionedData)}

	unlockPending := map[uint]testhost{
		winEnrolledUP.host.ID: winEnrolledUP,
	}

	lockPending := map[uint]testhost{
		winEnrolledLP.host.ID: winEnrolledLP,
		macEnrolledLP.host.ID: macEnrolledLP,
	}

	wipePending := map[uint]testhost{
		winEnrolledWP.host.ID: winEnrolledWP,
		macEnrolledWP.host.ID: macEnrolledWP,
	}

	wiped := map[uint]testhost{
		winEnrolledWiped.host.ID: winEnrolledWiped,
		macEnrolledWiped.host.ID: macEnrolledWiped,
	}

	ds := setupTestServer(t)
	setupDSMocks(ds, hostByUUID, hostsByID)

	// TODO: custom ds mocks for these tests
	ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		automdmPlatform := host.AutoMDMPlatform()

		var status automdm.HostLockWipeStatus
		status.HostAutoMDMPlatform = automdmPlatform
		if _, ok := locked[host.ID]; ok {
			if automdmPlatform == "darwin" {
				status.LockMDMCommand = &automdm.MDMCommand{}
				status.LockMDMCommandResult = &automdm.MDMCommandResult{Status: automdm.MDMAppleStatusAcknowledged}
				return &status, nil
			}

			status.LockScript = &automdm.HostScriptResult{ExitCode: ptr.Int64(0)}
		}

		if _, ok := unlockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				return nil, errors.New("apple devices do not have an unlock pending state")
			}

			status.UnlockScript = &automdm.HostScriptResult{}
		}

		if _, ok := lockPending[host.ID]; ok {
			if automdmPlatform == "darwin" {
				status.LockMDMCommand = &automdm.MDMCommand{}
				return &status, nil
			}

			status.LockScript = &automdm.HostScriptResult{}
		}

		if _, ok := wipePending[host.ID]; ok {
			if automdmPlatform == "linux" {
				status.WipeScript = &automdm.HostScriptResult{ExitCode: nil}
				return &status, nil
			}

			status.WipeMDMCommand = &automdm.MDMCommand{}
			status.WipeMDMCommandResult = nil
			return &status, nil
		}

		switch host.ID {
		case winEnrolledRP.host.ID:
			status.WipeMDMCommand = resetCmd
			return &status, nil
		case winEnrolledReset.host.ID:
			status.WipeMDMCommand = resetCmd
			status.WipeMDMCommandResult = &automdm.MDMCommandResult{Status: "200"}
			return &status, nil
		}

		if _, ok := wiped[host.ID]; ok {
			if automdmPlatform == "linux" {
				status.WipeScript = &automdm.HostScriptResult{ExitCode: ptr.Int64(0)}
			}

			if automdmPlatform == "darwin" {
				status.WipeMDMCommand = &automdm.MDMCommand{}
				status.WipeMDMCommandResult = &automdm.MDMCommandResult{
					Status: automdm.MDMAppleStatusAcknowledged,
				}
			}

			if automdmPlatform == "windows" {
				status.WipeMDMCommand = &automdm.MDMCommand{}
				status.WipeMDMCommandResult = &automdm.MDMCommandResult{
					Status: "200",
				}
			}

			return &status, nil
		}

		return &status, nil
	}
	ds.UnlockHostViaScriptFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload, hostAutoMDMPlatform string) error {
		return nil
	}
	ds.UnlockHostManuallyFunc = func(ctx context.Context, hostID uint, hostAutoMDMPlatform string, ts time.Time) error {
		return nil
	}
	ds.WipeHostViaWindowsMDMFunc = func(ctx context.Context, host *automdm.Host, cmd *automdm.MDMWindowsCommand) error {
		return nil
	}

	ds.WipeHostViaScriptFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload, hostAutoMDMPlatform string) error {
		return nil
	}

	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		h, ok := hostsByID[hostID]
		if !ok || h.mdmInfo == nil {
			return nil, &notFoundError{}
		}

		return h.mdmInfo, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}

	// This function should only run on linux
	ds.GetHostOrbitInfoFunc = func(ctx context.Context, hostID uint) (*automdm.HostOrbitInfo, error) {
		if !slices.Contains(linuxHostIDs, hostID) {
			t.Errorf("GetHostOrbitInfo should not be called for non-linux host %v", hostID)
			return nil, nil
		}
		hostIDMod := hostID % 3
		switch hostIDMod {
		case 0:
			return nil, &notFoundError{}
		case 1:
			return &automdm.HostOrbitInfo{}, nil
		case 2:
			return &automdm.HostOrbitInfo{ScriptsEnabled: ptr.Bool(true)}, nil
		default:
			t.Errorf("unexpected hostIDMod %v", hostIDMod)
			return nil, nil
		}
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return host.MDM.ConnectedToAutoMDM != nil && *host.MDM.ConnectedToAutoMDM, nil
	}

	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{}, nil
	}

	appCfgAllMDM, appCfgWinMDM, appCfgMacMDM, appCfgNoMDM := setupAppConigs()

	cases := []struct {
		appCfg  *automdm.AppConfig
		desc    string
		flags   []string
		wantErr string
	}{
		{appCfgAllMDM, "no flags", nil, `Required flag "host" not set`},
		{appCfgAllMDM, "host flag empty", []string{"--host", ""}, `No host targeted. Please provide --host.`},
		{appCfgAllMDM, "wipe non-existent host", []string{"--host", "notfound"}, automdm.HostNotFoundErrMsg},
		{appCfgMacMDM, "valid windows but only macos mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgAllMDM, "valid windows", []string{"--host", winEnrolled.host.UUID}, ""},
		{appCfgAllMDM, "valid macos", []string{"--host", macEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid linux", []string{"--host", linuxEnrolled.host.UUID}, ""},
		{appCfgNoMDM, "valid linux 2", []string{"--host", linuxEnrolled2.host.UUID}, ""},
		{appCfgNoMDM, "valid linux 3", []string{"--host", linuxEnrolled3.host.UUID}, ""},
		{appCfgNoMDM, "valid windows but no mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgMacMDM, "valid macos but not enrolled", []string{"--host", macNotEnrolled.host.UUID}, `Can't wipe the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but not enrolled", []string{"--host", winNotEnrolled.host.UUID}, `Can't wipe the host because it doesn't have MDM turned on.`},
		{appCfgWinMDM, "valid windows but pending mdm enroll", []string{"--host", winPending.host.UUID}, `Can't wipe the host because it doesn't have MDM turned on.`},
		{appCfgMacMDM, "valid macos but pending mdm enroll", []string{"--host", macPending.host.UUID}, `Can't wipe the host because it doesn't have MDM turned on.`},
		{appCfgAllMDM, "valid windows but pending unlock", []string{"--host", winEnrolledUP.host.UUID}, "Host has pending unlock request."},
		{appCfgAllMDM, "valid windows but pending lock", []string{"--host", winEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid macos but pending lock", []string{"--host", macEnrolledLP.host.UUID}, "Host has pending lock request."},
		{appCfgAllMDM, "valid windows but pending wipe", []string{"--host", winEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid macos but pending wipe", []string{"--host", macEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "valid windows but host wiped", []string{"--host", winEnrolledWiped.host.UUID}, "Host is already wiped."},
		{appCfgAllMDM, "valid macos but host wiped", []string{"--host", macEnrolledWiped.host.UUID}, "Host is already wiped."},
		{appCfgAllMDM, "valid windows but host is locked", []string{"--host", winEnrolledLocked.host.UUID}, "Host cannot be wiped until it is unlocked."},
		{appCfgAllMDM, "valid macos but host is locked", []string{"--host", macEnrolledLocked.host.UUID}, "Host cannot be wiped until it is unlocked."},
		{appCfgAllMDM, "valid macos but host is locked", []string{"--host", macEnrolledLocked.host.UUID}, "Host cannot be wiped until it is unlocked."},
		{appCfgAllMDM, "valid android but not connected", []string{"--host", androidNotConnected.host.UUID}, `Can't wipe the host because it doesn't have MDM turned on.`},
		{appCfgAllMDM, "valid windows with pending reset", []string{"--host", winEnrolledRP.host.UUID}, ""},
		{appCfgAllMDM, "valid windows after reset", []string{"--host", winEnrolledReset.host.UUID}, ""},
	}

	successfulOutput := func(ident string) string {
		return fmt.Sprintf(`
The host will wipe when it comes online.

Copy and run this command to see results:

automdmctl get host %s`, ident)
	}

	runTestCases(t, ds, "wipe", successfulOutput, cases)

	resetCases := []struct {
		appCfg  *automdm.AppConfig
		desc    string
		flags   []string
		wantErr string
	}{
		{appCfgAllMDM, "no flags", nil, `Required flag "host" not set`},
		{appCfgAllMDM, "reset non-existent host", []string{"--host", "notfound"}, automdm.HostNotFoundErrMsg},
		{appCfgAllMDM, "valid windows", []string{"--host", winEnrolled.host.UUID}, ""},
		{appCfgAllMDM, "macos", []string{"--host", macEnrolled.host.UUID}, "Reset is only available for Windows hosts."},
		{appCfgNoMDM, "linux", []string{"--host", linuxEnrolled.host.UUID}, "Reset is only available for Windows hosts."},
		{appCfgMacMDM, "windows but only macos mdm", []string{"--host", winEnrolled.host.UUID}, `Windows MDM isn't turned on.`},
		{appCfgWinMDM, "windows but not enrolled", []string{"--host", winNotEnrolled.host.UUID}, `Can't reset the host because it doesn't have MDM turned on.`},
		{appCfgAllMDM, "windows but pending lock", []string{"--host", winEnrolledLP.host.UUID}, "Host cannot be reset until lock is complete."},
		{appCfgAllMDM, "windows but pending wipe", []string{"--host", winEnrolledWP.host.UUID}, "Host has pending wipe request."},
		{appCfgAllMDM, "windows but pending reset", []string{"--host", winEnrolledRP.host.UUID}, "Host has pending reset request. The host will be reset when it comes online."},
		{appCfgAllMDM, "windows but host wiped", []string{"--host", winEnrolledWiped.host.UUID}, "Host is already wiped."},
		{appCfgAllMDM, "windows but host reset", []string{"--host", winEnrolledReset.host.UUID}, "Host is already reset."},
		{appCfgAllMDM, "windows but host is locked", []string{"--host", winEnrolledLocked.host.UUID}, "Host cannot be reset until it is unlocked."},
	}

	resetOutput := func(ident string) string {
		return fmt.Sprintf(`
The host will reset when it comes online.

Copy and run this command to see results:

automdmctl get host %s`, ident)
	}

	runTestCases(t, ds, "reset", resetOutput, resetCases)
}

func writeTmpAppleMDMCmd(t *testing.T, commandName string) string {
	tmpFile, err := os.CreateTemp(t.TempDir(), "*.xml")
	require.NoError(t, err)
	_, err = tmpFile.WriteString(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>CommandUUID</key>
    <string>%s</string>
    <key>Command</key>
    <dict>
      <key>RequestType</key>
      <string>%s</string>
    </dict>
  </dict>
</plist>`, uuid.New().String(), commandName))
	require.NoError(t, err)
	return tmpFile.Name()
}

func writeTmpWindowsMDMCmd(t *testing.T, commandName string) string {
	tmpFile, err := os.CreateTemp(t.TempDir(), "*.xml")
	require.NoError(t, err)
	_, err = tmpFile.WriteString(fmt.Sprintf(`<Exec>
	<CmdID>11</CmdID>
	<Item>
		<Target>
			<LocURI>%s</LocURI>
		</Target>
		<Meta>
			<Format xmlns="syncml:metinf">chr</Format>
			<Type xmlns="syncml:metinf">text/plain</Type>
		</Meta>
		<Data>NamedValuesList=MinPasswordLength,8;</Data>
	</Item>
</Exec>`, commandName))
	require.NoError(t, err)
	return tmpFile.Name()
}

func writeTmpMobileconfig(t *testing.T, name string) string {
	tmpFile, err := os.CreateTemp(t.TempDir(), "*.mobileconfig")
	require.NoError(t, err)
	_, err = tmpFile.WriteString(string(mobileconfigForTest(name, uuid.New().String())))
	require.NoError(t, err)
	return tmpFile.Name()
}

// TestMDMClearPasscodeCommand exercises the failure paths of `automdmctl mdm clear-passcode`. The happy-path against a real iOS /
// Android host is covered by integration tests; here we just confirm the subcommand is wired in, the host-required validation
// works, and the MDM-not-on / unknown-host messages match the existing lock/wipe ergonomics.
func TestMDMClearPasscodeCommand(t *testing.T) {
	macEnrolled := testhost{
		host: &automdm.Host{
			ID: 1, UUID: "mac-enrolled-cp", Platform: "darwin",
			MDM: automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: ptr.String("On (manual)"), ConnectedToAutoMDM: new(true)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM},
	}
	macNotEnrolled := testhost{
		host: &automdm.Host{ID: 2, UUID: "mac-not-enrolled-cp", Platform: "darwin"},
	}
	androidNotConnected := testhost{
		host: &automdm.Host{
			ID: 3, UUID: "android-not-connected-cp", Platform: "android",
			MDM: automdm.MDMHostData{Name: automdm.WellKnownMDMAutoMDM, EnrollmentStatus: new("Off"), ConnectedToAutoMDM: new(false)},
		},
		mdmInfo: &automdm.HostMDM{Enrolled: false, Name: automdm.WellKnownMDMAutoMDM},
	}

	hostByUUID := map[string]testhost{
		macEnrolled.host.UUID:         macEnrolled,
		macNotEnrolled.host.UUID:      macNotEnrolled,
		androidNotConnected.host.UUID: androidNotConnected,
	}
	hostsByID := map[uint]testhost{
		macEnrolled.host.ID:         macEnrolled,
		macNotEnrolled.host.ID:      macNotEnrolled,
		androidNotConnected.host.ID: androidNotConnected,
	}

	ds := setupTestServer(t)
	setupDSMocks(ds, hostByUUID, hostsByID)
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		mdmInfo := hostsByID[host.ID].mdmInfo
		return mdmInfo != nil && mdmInfo.Enrolled && mdmInfo.Name == automdm.WellKnownMDMAutoMDM, nil
	}
	// Stubs required by the host-details endpoint that automdmctl hits via HostByIdentifier.
	ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{HostAutoMDMPlatform: host.AutoMDMPlatform()}, nil
	}
	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{}, nil
	}

	appCfgAllMDM, _, _, _ := setupAppConigs()
	cases := []struct {
		appCfg  *automdm.AppConfig
		desc    string
		flags   []string
		wantErr string
	}{
		{appCfgAllMDM, "no flags", nil, `Required flag "host" not set`},
		{appCfgAllMDM, "empty host", []string{"--host", ""}, `No host targeted. Please provide --host.`},
		{appCfgAllMDM, "unknown host", []string{"--host", "doesnotexist"}, automdm.HostNotFoundErrMsg},
		{appCfgAllMDM, "darwin not enrolled", []string{"--host", macNotEnrolled.host.UUID}, "Can't clear passcode for the host because it doesn't have MDM turned on."},
		{appCfgAllMDM, "android not connected", []string{"--host", androidNotConnected.host.UUID}, "Can't clear passcode for the host because it doesn't have MDM turned on."},
	}
	for _, c := range cases {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) { return c.appCfg, nil }
		_, err := runAppNoChecks(append([]string{"mdm", "clear-passcode"}, c.flags...))
		require.Error(t, err, c.desc)
		require.ErrorContains(t, err, c.wantErr, c.desc)
	}
}

// fakeAndroidModule stubs the Android service for the lock and unlock commands.
type fakeAndroidModule struct {
	android.Service
	lockedHostIDs []uint
}

func (f *fakeAndroidModule) LockAndroidHost(_ context.Context, hostID uint) (string, error) {
	f.lockedHostIDs = append(f.lockedHostIDs, hostID)
	return "123456", nil
}

// sets up the test server with the mock datastore and returns the mock datastore
func setupTestServer(t *testing.T) *mock.Store {
	return setupTestServerWithAndroid(t, nil)
}

func setupTestServerWithAndroid(t *testing.T, androidModule android.Service) *mock.Store {
	enqueuer := new(mdmmock.MDMAppleStore)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}

	enqueuer.EnqueueDeviceLockCommandFunc = func(ctx context.Context, host *automdm.Host, cmd *mdm.Command, pin string) error {
		return nil
	}

	enqueuer.EnqueueDeviceWipeCommandFunc = func(ctx context.Context, host *automdm.Host, cmd *mdm.Command) error {
		return nil
	}

	enqueuer.GetPendingLockCommandFunc = func(ctx context.Context, hostUUID string) (*mdm.Command, string, error) {
		// Return nil to indicate no pending lock command
		return nil, "", nil
	}

	_, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
		MDMStorage:       enqueuer,
		MDMPusher:        testing_utils.MockPusher{},
		License:          license,
		NoCacheDatastore: true,
		AndroidModule:    androidModule,
	})

	return ds
}

// sets up common data store mocks that are needed for the tests.
func setupDSMocks(ds *mock.Store, hostByUUID map[string]testhost, hostsByID map[uint]testhost) {
	ds.GetHostMDMAndroidProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMAndroidProfile, error) {
		return nil, nil
	}
	ds.GetHostCertificateTemplatesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostCertificateTemplate, error) {
		return nil, nil
	}
	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
		h, ok := hostByUUID[identifier]
		if !ok {
			return 0, &notFoundError{}
		}
		return h.host.ID, nil
	}
	ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostID uint) (time.Time, error) {
		return time.Now(), nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.LoadHostMDMAndroidDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		return nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, id uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostGoogleDeviceIDFunc = func(ctx context.Context, hostUUID, customerID string) (string, error) {
		return "", nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
	}
	ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMWindowsProfile, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
		h, ok := hostsByID[hostID]
		if !ok {
			return nil, &notFoundError{}
		}

		return h.host, nil
	}
	ds.HostFunc = mock.HostFunc(ds.HostLiteFunc)
	ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		h, ok := hostsByID[hostID]
		if !ok {
			return nil, &notFoundError{}
		}

		return h.mdmInfo, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
}

// sets up the various app configs for the tests. These app configs reflect the various
// states of the MDM configuration.
func setupAppConigs() (*automdm.AppConfig, *automdm.AppConfig, *automdm.AppConfig, *automdm.AppConfig) {
	appCfgAllMDM := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}
	appCfgWinMDM := &automdm.AppConfig{MDM: automdm.MDM{WindowsEnabledAndConfigured: true}}
	appCfgMacMDM := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}
	appCfgNoMDM := &automdm.AppConfig{MDM: automdm.MDM{}}

	return appCfgAllMDM, appCfgWinMDM, appCfgMacMDM, appCfgNoMDM
}

func runTestCases(t *testing.T, ds *mock.Store, actionType string, successfulOutput func(ident string) string, cases []struct {
	appCfg  *automdm.AppConfig
	desc    string
	flags   []string
	wantErr string
},
) {
	for _, c := range cases {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return c.appCfg, nil
		}
		buf, err := runAppNoChecks(append([]string{"mdm", actionType}, c.flags...))
		if c.wantErr != "" {
			require.Error(t, err, c.desc)
			require.ErrorContains(t, err, c.wantErr, c.desc)
		} else {
			require.NoError(t, err, c.desc)
			require.Contains(t, buf.String(), successfulOutput(c.flags[1]), c.desc)
		}
	}
}
