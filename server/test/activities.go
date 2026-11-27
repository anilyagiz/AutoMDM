package test

import (
	"context"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// CreateHostScriptUpcomingActivity creates a host script execution request
// for the provided host. It returns the upcoming activity's execution ID.
func CreateHostScriptUpcomingActivity(t *testing.T, ds automdm.Datastore, host *automdm.Host) string {
	ctx := context.Background()
	hsr, err := ds.NewHostScriptExecutionRequest(ctx, &automdm.HostScriptRequestPayload{
		HostID:         host.ID,
		ScriptContents: "echo 'a'",
	})
	require.NoError(t, err)
	return hsr.ExecutionID
}

// SetHostScriptResult sets the result of a host script queued via
// CreateHostScriptUpcomingActivity.
func SetHostScriptResult(t *testing.T, ds automdm.Datastore, host *automdm.Host, execID string, exitCode int) {
	ctx := context.Background()
	_, _, err := ds.SetHostScriptExecutionResult(ctx, &automdm.HostScriptResultPayload{
		HostID: host.ID, ExecutionID: execID, Output: "a", ExitCode: exitCode,
	}, nil) // nil = manual script run, not policy automation
	require.NoError(t, err)
}

// CreateHostSoftwareInstallUpcomingActivity creates a host software install
// execution request for the provided host. It returns the upcoming activity's
// execution ID.
func CreateHostSoftwareInstallUpcomingActivity(t *testing.T, ds automdm.Datastore, host *automdm.Host, user *automdm.User) string {
	ctx := context.Background()
	installer, err := automdm.NewTempFileReader(strings.NewReader("echo"), t.TempDir)
	require.NoError(t, err)
	installerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &automdm.UploadSoftwareInstallerPayload{
		InstallScript:   "install foo",
		InstallerFile:   installer,
		StorageID:       uuid.NewString(),
		Filename:        "foo.pkg",
		Title:           uuid.NewString(),
		Source:          "apps",
		Version:         "0.0.1",
		UserID:          user.ID,
		UninstallScript: "uninstall foo",
		ValidatedLabels: &automdm.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	execID, err := ds.InsertSoftwareInstallRequest(ctx, host.ID, installerID, automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	return execID
}

// SetHostSoftwareInstallResult sets the result of a host software install
// queued via CreateHostSoftwareInstallUpcomingActivity.
func SetHostSoftwareInstallResult(t *testing.T, ds automdm.Datastore, host *automdm.Host, execID string, exitCode int) {
	ctx := context.Background()
	_, err := ds.SetHostSoftwareInstallResult(ctx, &automdm.HostSoftwareInstallResultPayload{
		HostID:                host.ID,
		InstallUUID:           execID,
		InstallScriptExitCode: &exitCode,
	}, nil)
	require.NoError(t, err)
}

// CreateHostSoftwareUninstallUpcomingActivity creates a host software uninstall
// execution request for the provided host. It returns the upcoming activity's
// execution ID.
func CreateHostSoftwareUninstallUpcomingActivity(t *testing.T, ds automdm.Datastore, host *automdm.Host, user *automdm.User) string {
	ctx := context.Background()
	installer, err := automdm.NewTempFileReader(strings.NewReader("echo"), t.TempDir)
	require.NoError(t, err)
	installerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &automdm.UploadSoftwareInstallerPayload{
		InstallScript:   "install foo",
		InstallerFile:   installer,
		StorageID:       uuid.NewString(),
		Filename:        "foo.pkg",
		Title:           uuid.NewString(),
		Source:          "apps",
		Version:         "0.0.1",
		UserID:          user.ID,
		UninstallScript: "uninstall foo",
		ValidatedLabels: &automdm.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	execID := uuid.NewString()
	err = ds.InsertSoftwareUninstallRequest(ctx, execID, host.ID, installerID, false)
	require.NoError(t, err)
	return execID
}

// SetHostSoftwareUninstallResult sets the result of a host software uninstall
// queued via CreateHostSoftwareUninstallUpcomingActivity.
func SetHostSoftwareUninstallResult(t *testing.T, ds automdm.Datastore, host *automdm.Host, execID string, exitCode int) {
	ctx := context.Background()
	_, _, err := ds.SetHostScriptExecutionResult(ctx, &automdm.HostScriptResultPayload{
		HostID:      host.ID,
		ExecutionID: execID,
		ExitCode:    exitCode,
	}, nil) // nil = manual/uninstall script, not policy automation
	require.NoError(t, err)
}

// CreateHostVPPAppInstallUpcomingActivity creates a VPP app install request
// for the provided host. It returns the upcoming activity's execution ID.
// Note that test.CreateInsertGlobalVPPToken(t, ds) should be used to enable
// VPP apps (create a VPP token).
func CreateHostVPPAppInstallUpcomingActivity(t *testing.T, ds automdm.Datastore, host *automdm.Host) (execID, adamID string) {
	ctx := context.Background()
	// Generate a short adamID that fits within the 16 character limit
	adamID = uuid.NewString()[:16]
	vppApp := &automdm.VPPApp{
		Name: "vpp_1", VPPAppTeam: automdm.VPPAppTeam{VPPAppID: automdm.VPPAppID{AdamID: adamID, Platform: automdm.MacOSPlatform}},
		BundleIdentifier: adamID,
	}
	_, err := ds.InsertVPPAppWithTeam(ctx, vppApp, nil, nil)
	require.NoError(t, err)
	execID = uuid.NewString()
	err = ds.InsertHostVPPSoftwareInstall(ctx, host.ID, vppApp.VPPAppID, execID, "event-id-1", automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	return execID, adamID
}

// SetHostVPPAppInstallResult sets the result of a VPP app install queued via
// CreateHostVPPAppInstallUpcomingActivity.
// The adamID is the one for the VPP app created by that call, and status is
// one of the Apple MDM status string (Acknowledged, Error, CommandFormatError,
// etc).
func SetHostVPPAppInstallResult(t *testing.T, ds automdm.Datastore, nanods storage.CommandAndReportResultsStore, host *automdm.Host, execID, adamID, status string, newActivityFn automdm.NewActivityFunc) {
	ctx := context.Background()
	nanoCtx := &mdm.Request{EnrollID: &mdm.EnrollID{ID: host.UUID}, Context: ctx}

	cmdRes := &mdm.CommandResults{
		CommandUUID: execID,
		Status:      status,
		Raw:         []byte(`<?xml version="1.0" encoding="UTF-8"?>`),
	}
	err := nanods.StoreCommandReport(nanoCtx, cmdRes)
	require.NoError(t, err)
	err = newActivityFn(ctx, nil, automdm.ActivityInstalledAppStoreApp{
		HostID:      host.ID,
		AppStoreID:  adamID,
		CommandUUID: execID,
		Status:      status,
	})
	require.NoError(t, err)
}

// CreateHostInHouseAppInstallUpcomingActivity creates an in-house app install
// request for the provided host. It returns the upcoming activity's execution
// ID.
func CreateHostInHouseAppInstallUpcomingActivity(t *testing.T, ds automdm.Datastore, host *automdm.Host, user *automdm.User) (execID string) {
	ctx := context.Background()
	rnd := uuid.NewString()
	ihaID, ihaTitleID, err := ds.MatchOrCreateSoftwareInstaller(ctx, &automdm.UploadSoftwareInstallerPayload{
		Filename:         rnd + ".ipa",
		Source:           "ios_apps",
		Extension:        "ipa",
		BundleIdentifier: "com.example." + rnd,
		UserID:           user.ID,
		ValidatedLabels:  &automdm.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	execID = uuid.NewString()
	err = ds.InsertHostInHouseAppInstall(ctx, host.ID, ihaID, ihaTitleID, execID, automdm.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	return execID
}

func SetHostInHouseAppInstallResult(t *testing.T, ds automdm.Datastore, nanods storage.CommandAndReportResultsStore, host *automdm.Host, execID, status string, newActivityFn automdm.NewActivityFunc) {
	ctx := context.Background()
	nanoCtx := &mdm.Request{EnrollID: &mdm.EnrollID{ID: host.UUID}, Context: ctx}

	cmdRes := &mdm.CommandResults{
		CommandUUID: execID,
		Status:      status,
		Raw:         []byte(`<?xml version="1.0" encoding="UTF-8"?>`),
	}
	err := nanods.StoreCommandReport(nanoCtx, cmdRes)
	require.NoError(t, err)
	err = newActivityFn(ctx, nil, automdm.ActivityTypeInstalledSoftware{
		HostID:      host.ID,
		CommandUUID: execID,
		Status:      status,
	})
	require.NoError(t, err)
}
