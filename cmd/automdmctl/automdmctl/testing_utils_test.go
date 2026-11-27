package automdmctl

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// runAppForTest mirrors automdmctltest.runAppForTest for internal tests in
// this package, which can't import automdmctltest (cycle: automdmctltest -> automdmctl).
func runAppForTest(t *testing.T, args []string) string {
	w, err := runAppNoChecks(args)
	require.NoError(t, err)
	return w.String()
}

// runAppCheckErr mirrors automdmctltest.runAppCheckErr for internal tests.
func runAppCheckErr(t *testing.T, args []string, errorMsg string) string {
	w, err := runAppNoChecks(args)
	require.Error(t, err)
	require.Contains(t, err.Error(), errorMsg)
	return w.String()
}

// runWithErrWriter mirrors automdmctltest.runWithErrWriter for internal tests.
func runWithErrWriter(args []string, errWriter io.Writer) (*bytes.Buffer, error) {
	args = append([]string{""}, args...)

	w := new(bytes.Buffer)
	app := CreateApp(nil, w, errWriter, noopExitErrHandler)
	StashRawArgs(app, args)
	err := app.Run(args)
	return w, err
}

func noopExitErrHandler(_ *cli.Context, _ error) {}

// runAppNoChecks is an alias for RunApp; added rather than changing all
// existing calls to `RunApp`, to avoid confusion and in case the behavior
// of `RunApp` needs to diverge in the future.
func runAppNoChecks(args []string) (*bytes.Buffer, error) {
	return RunApp(args)
}

type gitopsTestNotFoundError struct{}

func (e *gitopsTestNotFoundError) Error() string    { return "not found" }
func (e *gitopsTestNotFoundError) IsNotFound() bool { return true }

// setupEmptyGitOpsMocks sets all datastore mock functions needed by the gitops command to
// no-op defaults. Tests should override individual mocks after calling this to add tracking
// or return specific values.
func setupEmptyGitOpsMocks(ds *mock.Store) {
	// App config
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.SaveAppConfigFunc = func(ctx context.Context, config *automdm.AppConfig) error { return nil }

	// Labels
	ds.GetLabelSpecsFunc = func(ctx context.Context, filter automdm.TeamFilter) ([]*automdm.LabelSpec, error) {
		return nil, nil
	}
	ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) error {
		return nil
	}
	ds.SetAsideLabelsFunc = func(ctx context.Context, teamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
		return nil
	}
	ds.DeleteLabelFunc = func(ctx context.Context, id uint) error { return nil }
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{}, nil
	}
	ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
		return map[string]uint{}, nil
	}

	// Secrets
	ds.ApplyEnrollSecretsFunc = func(ctx context.Context, teamID *uint, secrets []*automdm.EnrollSecret) error {
		return nil
	}
	ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, isNew bool, teamID *uint) (bool, error) {
		return true, nil
	}

	// MDM profiles
	ds.GetMDMProfileChecksumsFunc = func(ctx context.Context, teamID uint) (*automdm.MDMProfileChecksums, error) {
		return &automdm.MDMProfileChecksums{}, nil
	}
	ds.BatchSetMDMProfilesFunc = func(
		ctx context.Context, tmID *uint, macProfiles []*automdm.MDMAppleConfigProfile, winProfiles []*automdm.MDMWindowsConfigProfile,
		macDecls []*automdm.MDMAppleDeclaration, androidProfiles []*automdm.MDMAndroidConfigProfile, vars []automdm.MDMProfileIdentifierAutoMDMVariables,
	) (automdm.MDMProfilesUpdates, error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMConfigProfilesFunc = func(
		ctx context.Context, teamID *uint, opt automdm.ListOptions,
	) ([]*automdm.MDMConfigProfilePayload, *automdm.PaginationMetadata, error) {
		return nil, &automdm.PaginationMetadata{}, nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(
		ctx context.Context, hostIDs []uint, teamIDs []uint, profileUUIDs []string, hostUUIDs []string,
	) (automdm.MDMProfilesUpdates, error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.SetOrUpdateMDMAppleDeclarationFunc = func(ctx context.Context, declaration *automdm.MDMAppleDeclaration, usesAutoMDMVars []automdm.AutoMDMVarName, activationAction automdm.MDMAppleActivationAction) (*automdm.MDMAppleDeclaration, error) {
		return &automdm.MDMAppleDeclaration{}, nil
	}
	ds.DeleteMDMAppleDeclarationByNameFunc = func(ctx context.Context, teamID *uint, name string) error {
		return nil
	}
	ds.SetOrUpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, cp automdm.MDMWindowsConfigProfile) error {
		return nil
	}
	ds.DeleteMDMWindowsConfigProfileByTeamAndNameFunc = func(ctx context.Context, teamID *uint, profileName string) error {
		return nil
	}
	ds.NewMDMAppleConfigProfileFunc = func(ctx context.Context, p automdm.MDMAppleConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMAppleConfigProfile, error) {
		return &automdm.MDMAppleConfigProfile{}, nil
	}
	ds.GetMDMAppleBootstrapPackageMetaFunc = func(ctx context.Context, teamID uint) (*automdm.MDMAppleBootstrapPackage, error) {
		return &automdm.MDMAppleBootstrapPackage{}, nil
	}
	ds.DeleteMDMAppleBootstrapPackageFunc = func(ctx context.Context, teamID uint) error { return nil }
	ds.GetMDMAppleSetupAssistantFunc = func(ctx context.Context, teamID *uint) (*automdm.MDMAppleSetupAssistant, error) {
		return nil, &gitopsTestNotFoundError{}
	}
	ds.DeleteMDMAppleSetupAssistantFunc = func(ctx context.Context, teamID *uint) error { return nil }
	ds.InsertOrReplaceMDMConfigAssetFunc = func(ctx context.Context, asset automdm.MDMConfigAsset) error {
		return nil
	}
	ds.HardDeleteMDMConfigAssetFunc = func(ctx context.Context, assetName automdm.MDMAssetName) error {
		return nil
	}
	ds.DeleteMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) error {
		return nil
	}
	ds.MDMGetEULAMetadataFunc = func(ctx context.Context, platform automdm.MDMEULAPlatform) (*automdm.MDMEULA, error) {
		return nil, &gitopsTestNotFoundError{}
	}
	ds.MDMInsertEULAFunc = func(ctx context.Context, eula *automdm.MDMEULA) error { return nil }
	ds.MDMDeleteEULAFunc = func(ctx context.Context, platform automdm.MDMEULAPlatform, token string) error { return nil }
	ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
		return document, nil, nil
	}

	// Scripts
	ds.BatchSetScriptsFunc = func(ctx context.Context, tmID *uint, scripts []*automdm.Script) ([]automdm.ScriptResponse, error) {
		return []automdm.ScriptResponse{}, nil
	}

	// Policies and queries
	ds.ListGlobalPoliciesFunc = func(ctx context.Context, opts automdm.ListOptions, platform string) ([]*automdm.Policy, error) {
		return nil, nil
	}
	ds.ListTeamPoliciesFunc = func(
		ctx context.Context, teamID uint, opts automdm.ListOptions, iopts automdm.ListOptions, automationType automdm.PolicyAutomationType, platform string,
	) ([]*automdm.Policy, []*automdm.Policy, error) {
		return nil, nil, nil
	}
	ds.ListQueriesFunc = func(ctx context.Context, opts automdm.ListQueryOptions) ([]*automdm.Query, int, int, *automdm.PaginationMetadata, error) {
		return nil, 0, 0, nil, nil
	}
	ds.QueryByNameFunc = func(ctx context.Context, teamID *uint, name string) (*automdm.Query, error) {
		return nil, &gitopsTestNotFoundError{}
	}

	// Teams
	ds.ListTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions) ([]*automdm.Team, error) {
		return nil, nil
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, &gitopsTestNotFoundError{}
	}
	ds.TeamByFilenameFunc = func(ctx context.Context, filename string) (*automdm.Team, error) {
		return nil, nil
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{}, nil
	}
	ds.NewTeamFunc = func(ctx context.Context, newTeam *automdm.Team) (*automdm.Team, error) {
		newTeam.ID = 1
		return newTeam, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return team, nil
	}
	ds.DefaultTeamConfigFunc = func(ctx context.Context) (*automdm.TeamConfig, error) {
		return &automdm.TeamConfig{}, nil
	}
	ds.SaveDefaultTeamConfigFunc = func(ctx context.Context, config *automdm.TeamConfig) error { return nil }
	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return nil, nil
	}

	// Software
	ds.BatchSetSoftwareInstallersFunc = func(ctx context.Context, teamID *uint, installers []*automdm.UploadSoftwareInstallerPayload) ([]uint, error) {
		return nil, nil
	}
	ds.BatchSetInHouseAppsInstallersFunc = func(ctx context.Context, teamID *uint, installers []*automdm.UploadSoftwareInstallerPayload) error {
		return nil
	}
	ds.GetSoftwareInstallersFunc = func(ctx context.Context, tmID uint) ([]automdm.SoftwarePackageResponse, error) {
		return nil, nil
	}
	ds.GetSoftwareInstallersPendingDeletionFunc = func(ctx context.Context, tmID *uint, incoming []automdm.SoftwareTitleIdentifier) ([]automdm.DeletedSoftwarePackage, error) {
		return nil, nil
	}
	ds.ListSoftwareTitlesFunc = func(ctx context.Context, opt automdm.SoftwareTitleListOptions, tmFilter automdm.TeamFilter) ([]automdm.SoftwareTitleListResult, int, *automdm.PaginationMetadata, error) {
		return nil, 0, nil, nil
	}
	ds.ListSoftwareAutoUpdateSchedulesFunc = func(ctx context.Context, teamID uint, source string, optionalFilter ...automdm.SoftwareAutoUpdateScheduleFilter) ([]automdm.SoftwareAutoUpdateSchedule, error) {
		return []automdm.SoftwareAutoUpdateSchedule{}, nil
	}
	ds.GetSoftwareCategoryNameToIDMapFunc = func(ctx context.Context, teamID uint, names []string) (map[string]uint, error) {
		return nil, nil
	}
	ds.ListSoftwareCategoriesFunc = func(ctx context.Context, teamID uint) ([]automdm.SoftwareCategory, error) {
		return nil, nil
	}
	ds.BatchNewSoftwareCategoriesFunc = func(ctx context.Context, teamID uint, names []string) error {
		return nil
	}
	ds.DeleteSoftwareCategoryFunc = func(ctx context.Context, id uint) error {
		return nil
	}
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) {
		return true, nil
	}
	ds.GetTeamsWithInstallerByHashFunc = func(ctx context.Context, sha256, url string) (map[uint][]*automdm.ExistingSoftwareInstaller, error) {
		return map[uint][]*automdm.ExistingSoftwareInstaller{}, nil
	}
	ds.GetInstallerByTeamAndURLFunc = func(ctx context.Context, teamID *uint, url string) (*automdm.ExistingSoftwareInstaller, error) {
		return nil, nil
	}
	ds.DeleteIconsAssociatedWithTitlesWithoutInstallersFunc = func(ctx context.Context, teamID uint) error {
		return nil
	}
	ds.DeleteSetupExperienceScriptFunc = func(ctx context.Context, teamID *uint) error { return nil }

	// VPP / App Store
	ds.SetTeamVPPAppsFunc = func(ctx context.Context, teamID *uint, adamIDs []automdm.VPPAppTeam, _ map[string]uint) (bool, error) {
		return false, nil
	}
	ds.BatchInsertVPPAppsFunc = func(ctx context.Context, apps []*automdm.VPPApp) error { return nil }
	ds.GetVPPAppsFunc = func(ctx context.Context, teamID *uint) ([]automdm.VPPAppResponse, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{}, nil
	}
	// Default to "no existing vpp_apps row" so resolveAddAnchor takes the
	// first-add path. Tests that exercise re-anchor behavior override these.
	ds.GetVPPAppByAdamIDPlatformFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform) (*automdm.VPPApp, error) {
		return nil, vppAppNotFound{}
	}
	ds.GetVPPTokenOwningAppInCountryFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform, country string) (*automdm.VPPTokenDB, error) {
		return nil, vppAppNotFound{}
	}

	// ABM
	ds.ListABMTokensFunc = func(ctx context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{}, nil
	}
	ds.GetABMTokenCountFunc = func(ctx context.Context) (int, error) { return 0, nil }
	ds.SaveABMTokenFunc = func(ctx context.Context, tok *automdm.ABMToken) error { return nil }

	ds.ListMicrosoftGraphCredentialsFunc = func(ctx context.Context) ([]*automdm.MicrosoftGraphCredential, error) {
		return nil, nil
	}
	ds.ListMicrosoftGraphCredentialMetadataFunc = func(ctx context.Context) ([]*automdm.MicrosoftGraphCredentialMetadata, error) {
		return nil, nil
	}
	ds.UpdateMicrosoftGraphCredentialInvalidAggregateFunc = func(ctx context.Context) error { return nil }
	ds.ReplaceMicrosoftGraphCredentialsFunc = func(ctx context.Context, upsert []*automdm.MicrosoftGraphCredential, deleteTenantIDs []string) error {
		return nil
	}

	// Certificate authorities
	ds.BatchApplyCertificateAuthoritiesFunc = func(ctx context.Context, ops automdm.CertificateAuthoritiesBatchOperations) ([]uint, error) {
		return nil, nil
	}
	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}
	ds.ListCertificateAuthoritiesFunc = func(ctx context.Context) ([]*automdm.CertificateAuthoritySummary, error) {
		return nil, nil
	}
	ds.GetCertificateTemplatesByTeamIDFunc = func(ctx context.Context, teamID uint, opts automdm.ListOptions) ([]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error) {
		return []*automdm.CertificateTemplateResponseSummary{}, &automdm.PaginationMetadata{}, nil
	}
	ds.BatchDeleteCertificateTemplatesFunc = func(ctx context.Context, ids []uint) (bool, error) {
		return false, nil
	}
	ds.CreatePendingCertificateTemplatesForExistingHostsFunc = func(ctx context.Context, certificateTemplateID uint, teamID uint) (int64, error) {
		return 0, nil
	}
	ds.SetCertificateTemplateVariablesFunc = func(ctx context.Context, certTemplateID uint, automdmVars []automdm.AutoMDMVarName) error {
		return nil
	}
	ds.SetHostCertificateTemplatesToPendingRemoveFunc = func(ctx context.Context, certTmplID uint) error {
		return nil
	}

	// Conditional access
	ds.ConditionalAccessMicrosoftGetFunc = func(ctx context.Context) (*automdm.ConditionalAccessMicrosoftIntegration, error) {
		return &automdm.ConditionalAccessMicrosoftIntegration{}, nil
	}

	// Setup experience
	ds.TeamIDsWithSetupExperienceIdPEnabledFunc = func(ctx context.Context) ([]uint, error) {
		return nil, nil
	}

	// Jobs
	ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
		return &automdm.Job{}, nil
	}
}

type vppAppNotFound struct{}

func (vppAppNotFound) Error() string    { return "vpp app not found" }
func (vppAppNotFound) IsNotFound() bool { return true }
