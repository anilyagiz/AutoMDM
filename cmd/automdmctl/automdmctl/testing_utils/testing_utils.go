package testing_utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/cached_mysql"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/vpp"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/tokenpki"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/push"
	mdmtesting "github.com/anilyagiz/automdm/v4/server/mdm/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mock2 "github.com/anilyagiz/automdm/v4/server/mock/mdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
	"github.com/docker/go-units"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

const (
	teamName         = "Team Test"
	automdmServerURL = "https://automdm.example.com"
	orgName          = "GitOps Test"
)

// RunServerWithMockedDS runs the automdm server with several mocked DS methods.
//
// NOTE: Assumes the current session is always from the admin user (see ds.SessionByKeyFunc below).
func RunServerWithMockedDS(t *testing.T, opts ...*service.TestServerOpts) (*httptest.Server, *mock.Store) {
	ds := new(mock.Store)
	// Custom host vitals are declarative and global-only, so every `automdmctl gitops`
	// run against a global config calls these, even when custom_host_vitals: is absent.
	ds.ListCustomHostVitalsFunc = func(ctx context.Context, opt automdm.ListOptions) ([]automdm.CustomHostVital, *automdm.PaginationMetadata, int, error) {
		return nil, &automdm.PaginationMetadata{}, 0, nil
	}
	ds.UpsertCustomHostVitalsFunc = func(ctx context.Context, vitals []automdm.CustomHostVital) ([]automdm.CustomHostVital, []automdm.CustomHostVital, error) {
		return nil, nil, nil
	}
	ds.BatchSetAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint, assets []*automdm.MDMAppleDDMAssetToSet) (*automdm.MDMAppleDDMAssetsBatchChanges, error) {
		return &automdm.MDMAppleDDMAssetsBatchChanges{}, nil
	}
	ds.ListAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.DDMAsset, error) {
		return nil, nil
	}
	var users []*automdm.User
	var admin *automdm.User
	ds.NewUserFunc = func(ctx context.Context, user *automdm.User) (*automdm.User, error) {
		if user.GlobalRole != nil && *user.GlobalRole == automdm.RoleAdmin {
			admin = user
		}
		users = append(users, user)
		return user, nil
	}
	ds.SessionByKeyFunc = func(ctx context.Context, key string) (*automdm.Session, error) {
		return &automdm.Session{
			CreateTimestamp: automdm.CreateTimestamp{CreatedAt: time.Now()},
			ID:              1,
			AccessedAt:      time.Now(),
			UserID:          admin.ID,
			Key:             key,
		}, nil
	}
	ds.MarkSessionAccessedFunc = func(ctx context.Context, session *automdm.Session) error {
		return nil
	}
	ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
		return admin, nil
	}
	ds.ListUsersFunc = func(ctx context.Context, opt automdm.UserListOptions) ([]*automdm.User, error) {
		return users, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	// On Premium, AppConfig assembly reads the Microsoft conditional access
	// integration. Default to an empty (not set up) integration so tests that
	// don't care about it don't panic on a nil mock.
	ds.ConditionalAccessMicrosoftGetFunc = func(ctx context.Context) (*automdm.ConditionalAccessMicrosoftIntegration, error) {
		return &automdm.ConditionalAccessMicrosoftIntegration{}, nil
	}
	ds.GetWindowsEnrollmentDefaultTeamFunc = func(ctx context.Context) (*uint, string, error) {
		return nil, "", nil
	}
	mockWebhooksAndTickets(ds)
	mockPoliciesInScope(ds)
	ds.ListMicrosoftGraphCredentialMetadataFunc = func(ctx context.Context) ([]*automdm.MicrosoftGraphCredentialMetadata, error) {
		return nil, nil
	}
	ds.NewGlobalPolicyFunc = func(ctx context.Context, authorID *uint, args automdm.PolicyPayload) (*automdm.Policy, error) {
		return &automdm.Policy{
			PolicyData: automdm.PolicyData{
				Name:        args.Name,
				Query:       args.Query,
				Critical:    args.Critical,
				Platform:    args.Platform,
				Description: args.Description,
				Resolution:  &args.Resolution,
				AuthorID:    authorID,
				Type:        "dynamic",
			},
		}, nil
	}
	ds.NewScriptFunc = func(ctx context.Context, script *automdm.Script) (*automdm.Script, error) {
		if !strings.HasPrefix(script.ScriptContents, "#!/") {
			return nil, errors.New("script not uploaded properly")
		}
		return &automdm.Script{
			ID: 1,
		}, nil
	}
	ds.GetEnrollSecretsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.EnrollSecret, error) { return nil, nil }
	apnsCert, apnsKey, err := mysqltest.GenerateTestCertBytes(mdmtesting.NewTestMDMAppleCertTemplate())
	require.NoError(t, err)
	certPEM, keyPEM, tokenBytes, err := mysqltest.GenerateTestABMAssets(t)
	require.NoError(t, err)
	ds.GetAllMDMConfigAssetsHashesFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) (map[automdm.MDMAssetName]string, error) {
		return map[automdm.MDMAssetName]string{
			automdm.MDMAssetABMCert:            "abmcert",
			automdm.MDMAssetABMKey:             "abmkey",
			automdm.MDMAssetABMTokenDeprecated: "abmtoken",
			automdm.MDMAssetAPNSCert:           "apnscert",
			automdm.MDMAssetAPNSKey:            "apnskey",
			automdm.MDMAssetCACert:             "scepcert",
			automdm.MDMAssetCAKey:              "scepkey",
		}, nil
	}
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetABMCert:            {Name: automdm.MDMAssetABMCert, Value: certPEM},
			automdm.MDMAssetABMKey:             {Name: automdm.MDMAssetABMKey, Value: keyPEM},
			automdm.MDMAssetABMTokenDeprecated: {Name: automdm.MDMAssetABMTokenDeprecated, Value: tokenBytes},
			automdm.MDMAssetAPNSCert:           {Name: automdm.MDMAssetAPNSCert, Value: apnsCert},
			automdm.MDMAssetAPNSKey:            {Name: automdm.MDMAssetAPNSKey, Value: apnsKey},
			automdm.MDMAssetCACert:             {Name: automdm.MDMAssetCACert, Value: certPEM},
			automdm.MDMAssetCAKey:              {Name: automdm.MDMAssetCAKey, Value: keyPEM},
		}, nil
	}

	ds.ApplyYaraRulesFunc = func(context.Context, []automdm.YaraRule) error {
		return nil
	}
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.ValidateReferencedCustomHostVitalsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.GetPatchDeadlineSettingsFunc = func(ctx context.Context, teamID uint) (*automdm.PatchDeadlineSettings, error) {
		return automdm.DefaultPatchDeadlineSettings(teamID), nil
	}
	ds.SetPatchDeadlineSettingsFunc = func(ctx context.Context, settings automdm.PatchDeadlineSettings) error {
		return nil
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
	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}
	ds.GetCertificateTemplatesByTeamIDFunc = func(ctx context.Context, teamID uint, opts automdm.ListOptions) ([]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error) {
		return []*automdm.CertificateTemplateResponseSummary{}, &automdm.PaginationMetadata{}, nil
	}
	ds.GetVPPAppsFunc = func(ctx context.Context, teamID *uint) ([]automdm.VPPAppResponse, error) {
		return []automdm.VPPAppResponse{}, nil
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, values []string) ([]automdm.DuplicateStringGroup, error) {
		return nil, nil
	}
	ds.GetEnterpriseFunc = func(ctx context.Context) (*android.Enterprise, error) {
		return nil, nil
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
	ds.TeamMDMConfigFunc = func(ctx context.Context, teamID uint) (*automdm.TeamMDM, error) {
		return &automdm.TeamMDM{}, nil
	}
	// The FileVault reconciler reads the settings above and then writes or
	// removes the profile accordingly; both outcomes need a mock.
	ds.UpsertMDMAppleAutoMDMConfigProfileFunc = func(ctx context.Context, p automdm.MDMAppleConfigProfile) error {
		return nil
	}
	ds.DeleteMDMAppleConfigProfileByTeamAndIdentifierFunc = func(ctx context.Context, teamID *uint, profileIdentifier string) error {
		return nil
	}
	// Default mocks used by ApplyLabelSpecs to detect created vs edited labels
	// for activity emission. Tests can override these.
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{}, nil
	}
	ds.LabelFunc = func(ctx context.Context, lid uint, filter automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		return &automdm.LabelWithTeamName{Label: automdm.Label{ID: lid}}, nil, nil
	}
	ds.LabelMembershipHostIDsFunc = func(ctx context.Context, labelID uint) ([]uint, error) {
		return nil, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid}, nil
	}
	// GitOps clears the setup experience script on every non-dry-run team apply, which now reads
	// the existing script first. Default to "none set" so the delete is a clean no-op; tests that
	// care about setup experience scripts override this.
	ds.GetSetupExperienceScriptFunc = func(ctx context.Context, teamID *uint) (*automdm.Script, error) {
		return nil, &notFoundError{}
	}
	ds.VerifyAppleConfigProfileScopesDoNotConflictFunc = func(ctx context.Context, cps []*automdm.MDMAppleConfigProfile) error {
		return nil
	}

	var cachedDS automdm.Datastore
	if len(opts) > 0 && opts[0].NoCacheDatastore {
		cachedDS = ds
	} else {
		cachedDS = cached_mysql.New(ds)
	}
	_, server := svctest.RunServerForTestsWithDS(t, cachedDS, opts...)
	os.Setenv("AUTOMDM_SERVER_ADDRESS", server.URL)

	return server, ds
}

func getPathRelative(relativePath string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get runtime caller info")
	}
	sourceDir := filepath.Dir(currentFile)
	return filepath.Join(sourceDir, relativePath)
}

func ServeMDMBootstrapPackage(t *testing.T, pkgPath, pkgName string) (*httptest.Server, int) {
	pkgBytes, err := os.ReadFile(pkgPath)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(pkgBytes)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment;filename="%s"`, pkgName))
		if n, err := w.Write(pkgBytes); err != nil {
			require.NoError(t, err)
			require.Equal(t, len(pkgBytes), n)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, len(pkgBytes)
}

func StartSoftwareInstallerServer(t *testing.T) {
	// load the ruby installer to use as base bytes to repeat for the "too large" case
	b, err := os.ReadFile(getPathRelative("../../../../server/service/testdata/software-installers/ruby.deb"))
	require.NoError(t, err)

	// get the base dir of all installers
	baseDir := getPathRelative("../../../../server/service/testdata/software-installers/")

	// start the web server that will serve the installer
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "notfound"):
					w.WriteHeader(http.StatusNotFound)
					return
				case strings.HasSuffix(r.URL.Path, ".txt"):
					w.Header().Set("Content-Type", "text/plain")
					_, _ = w.Write([]byte(`a simple text file`))
					return
				case strings.Contains(r.URL.Path, "toolarge"):
					w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
					var sz int
					for sz < 513*units.MiB {
						n, _ := w.Write(b)
						sz += n
					}
				case strings.Contains(r.URL.Path, "other.deb"):
					// serve same content as ruby.deb
					w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
					_, _ = w.Write(b)
				case strings.Contains(r.URL.Path, "ruby_variant.deb"):
					// ruby.deb with extra trailing bytes: same package (the deb archive
					// ignores trailing bytes) but a different hash, so it is a distinct
					// package of the same title.
					w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
					_, _ = w.Write(b)
					_, _ = w.Write([]byte("\n# variant\n"))
				case strings.HasSuffix(r.URL.Path, ".pkg"):
					pkgDir := getPathRelative("../testdata/gitops/lib/")
					http.ServeFile(w, r, filepath.Join(pkgDir, filepath.Base(r.URL.Path)))
				default:
					http.ServeFile(w, r, filepath.Join(baseDir, filepath.Base(r.URL.Path)))
				}
			},
		),
	)
	t.Cleanup(srv.Close)
	t.Setenv("SOFTWARE_INSTALLER_URL", srv.URL)
}

func SetupFullGitOpsPremiumServer(t *testing.T) (*mock.Store, **automdm.AppConfig, map[string]**automdm.Team) {
	testCert, testKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)
	testCertPEM := tokenpki.PEMCertificate(testCert.Raw)
	testKeyPEM := tokenpki.PEMRSAPrivateKey(testKey)
	automdmCfg := config.TestConfig()
	config.SetTestMDMConfig(t, &automdmCfg, testCertPEM, testKeyPEM, getPathRelative("../../../../server/service/testdata"))

	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	_, ds := RunServerWithMockedDS(
		t, &service.TestServerOpts{
			MDMStorage:       new(mock2.MDMAppleStore),
			MDMPusher:        MockPusher{},
			AutoMDMConfig:    &automdmCfg,
			License:          license,
			NoCacheDatastore: true,
			KeyValueStore:    NewMemKeyValueStore(),
		},
	)

	// Mock appConfig
	savedAppConfig := &automdm.AppConfig{
		MDM: automdm.MDM{
			EnabledAndConfigured:        true,
			AndroidEnabledAndConfigured: true,
		},
	}
	AddLabelMocks(ds)

	// Add DefaultTeamConfig mocks for enterprise features
	ds.DefaultTeamConfigFunc = func(ctx context.Context) (*automdm.TeamConfig, error) {
		return &automdm.TeamConfig{}, nil
	}
	ds.SaveDefaultTeamConfigFunc = func(ctx context.Context, config *automdm.TeamConfig) error {
		return nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		appConfigCopy := *savedAppConfig
		return &appConfigCopy, nil
	}
	ds.SaveAppConfigFunc = func(ctx context.Context, config *automdm.AppConfig) error {
		appConfigCopy := *config
		savedAppConfig = &appConfigCopy
		return nil
	}
	ds.SetTeamVPPAppsFunc = func(ctx context.Context, teamID *uint, adamIDs []automdm.VPPAppTeam, _ map[string]uint) (bool, error) {
		return false, nil
	}
	ds.BatchInsertVPPAppsFunc = func(ctx context.Context, apps []*automdm.VPPApp) error {
		return nil
	}
	// Default to "no existing vpp_apps row" so resolveAddAnchor takes the
	// first-add path. Tests that assert subsequent-add or re-anchor behavior
	// override these.
	ds.GetVPPAppByAdamIDPlatformFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform) (*automdm.VPPApp, error) {
		return nil, &notFoundError{}
	}
	ds.GetVPPTokenOwningAppInCountryFunc = func(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform, country string) (*automdm.VPPTokenDB, error) {
		return nil, &notFoundError{}
	}
	ds.ListSoftwareAutoUpdateSchedulesFunc = func(ctx context.Context, teamID uint, source string, optionalFilter ...automdm.SoftwareAutoUpdateScheduleFilter) ([]automdm.SoftwareAutoUpdateSchedule, error) {
		return []automdm.SoftwareAutoUpdateSchedule{}, nil
	}

	savedTeams := map[string]**automdm.Team{}

	ds.ApplyEnrollSecretsFunc = func(ctx context.Context, teamID *uint, secrets []*automdm.EnrollSecret) error {
		return nil
	}
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
		return nil
	}
	ds.ApplyQueriesFunc = func(
		ctx context.Context, authorID uint, queries []*automdm.Query, queriesToDiscardResults map[uint]struct{},
	) error {
		return nil
	}
	ds.GetMDMProfileChecksumsFunc = func(ctx context.Context, teamID uint) (*automdm.MDMProfileChecksums, error) {
		return &automdm.MDMProfileChecksums{}, nil
	}
	ds.BatchSetMDMProfilesFunc = func(
		ctx context.Context, tmID *uint, macProfiles []*automdm.MDMAppleConfigProfile, winProfiles []*automdm.MDMWindowsConfigProfile,
		macDecls []*automdm.MDMAppleDeclaration, androidProfiles []*automdm.MDMAndroidConfigProfile, vars []automdm.MDMProfileIdentifierAutoMDMVariables,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListMDMConfigProfilesFunc = func(
		ctx context.Context, teamID *uint, opt automdm.ListOptions,
	) ([]*automdm.MDMConfigProfilePayload, *automdm.PaginationMetadata, error) {
		return nil, &automdm.PaginationMetadata{}, nil
	}
	ds.BatchSetScriptsFunc = func(ctx context.Context, tmID *uint, scripts []*automdm.Script) ([]automdm.ScriptResponse, error) {
		return []automdm.ScriptResponse{}, nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(
		ctx context.Context, hostIDs []uint, teamIDs []uint, profileUUIDs []string, hostUUIDs []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.SetOrUpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, cp automdm.MDMWindowsConfigProfile) error {
		return nil
	}
	ds.DeleteMDMWindowsConfigProfileByTeamAndNameFunc = func(ctx context.Context, teamID *uint, profileName string) error {
		return nil
	}
	ds.DeleteMDMAppleDeclarationByNameFunc = func(ctx context.Context, teamID *uint, name string) error {
		return nil
	}
	ds.GetMDMAppleBootstrapPackageMetaFunc = func(ctx context.Context, teamID uint) (*automdm.MDMAppleBootstrapPackage, error) {
		return &automdm.MDMAppleBootstrapPackage{}, nil
	}
	ds.DeleteMDMAppleBootstrapPackageFunc = func(ctx context.Context, teamID uint) error {
		return nil
	}
	ds.InsertMDMAppleBootstrapPackageFunc = func(ctx context.Context, bp *automdm.MDMAppleBootstrapPackage, pkgStore automdm.MDMBootstrapPackageStore) error {
		return nil
	}
	ds.GetMDMAppleSetupAssistantFunc = func(ctx context.Context, teamID *uint) (*automdm.MDMAppleSetupAssistant, error) {
		return nil, nil
	}
	ds.DeleteMDMAppleSetupAssistantFunc = func(ctx context.Context, teamID *uint) error {
		return nil
	}
	ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, isNew bool, teamID *uint) (bool, error) {
		return true, nil
	}
	ds.LabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]uint, error) {
		require.ElementsMatch(t, names, []string{automdm.BuiltinLabelMacOS14Plus})
		return map[string]uint{automdm.BuiltinLabelMacOS14Plus: 1}, nil
	}
	ds.ListGlobalPoliciesFunc = func(ctx context.Context, opts automdm.ListOptions, platform string) ([]*automdm.Policy, error) {
		return nil, nil
	}
	ds.ListTeamPoliciesFunc = func(
		ctx context.Context, teamID uint, opts automdm.ListOptions, iopts automdm.ListOptions, automationType automdm.PolicyAutomationType, platform string,
	) (teamPolicies []*automdm.Policy, inheritedPolicies []*automdm.Policy, err error) {
		return nil, nil, nil
	}
	ds.ListTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions) ([]*automdm.Team, error) {
		if savedTeams != nil {
			var result []*automdm.Team
			for _, t := range savedTeams {
				result = append(result, *t)
			}
			return result, nil
		}
		return nil, nil
	}
	ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
		summary := make([]*automdm.TeamSummary, 0, len(savedTeams))
		for _, team := range savedTeams {
			summary = append(summary, &automdm.TeamSummary{
				ID:          (*team).ID,
				Name:        (*team).Name,
				Description: (*team).Description,
			})
		}
		return summary, nil
	}
	ds.ListQueriesFunc = func(ctx context.Context, opts automdm.ListQueryOptions) ([]*automdm.Query, int, int, *automdm.PaginationMetadata, error) {
		return nil, 0, 0, nil, nil
	}
	ds.NewMDMAppleConfigProfileFunc = func(ctx context.Context, p automdm.MDMAppleConfigProfile, vars []automdm.AutoMDMVarName) (*automdm.MDMAppleConfigProfile, error) {
		return nil, nil
	}
	ds.NewJobFunc = func(ctx context.Context, job *automdm.Job) (*automdm.Job, error) {
		job.ID = 1
		return job, nil
	}
	ds.HasQueuedJobWithArgsFunc = func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
		return false, nil
	}
	ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		team.ID = uint(len(savedTeams) + 1) //nolint:gosec // dismiss G115
		savedTeams[team.Name] = &team
		return team, nil
	}
	ds.QueryByNameFunc = func(ctx context.Context, teamID *uint, name string) (*automdm.Query, error) {
		return nil, &notFoundError{}
	}
	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		for _, tm := range savedTeams {
			if (*tm).ID == tid {
				return *tm, nil
			}
		}
		return nil, &notFoundError{}
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		if tid == 0 {
			return &automdm.TeamLite{}, nil
		}
		for _, tm := range savedTeams {
			if (*tm).ID == tid {
				teamToCopy := *tm
				return &automdm.TeamLite{
					ID:          teamToCopy.ID,
					Filename:    teamToCopy.Filename,
					CreatedAt:   teamToCopy.CreatedAt,
					Name:        teamToCopy.Name,
					Description: teamToCopy.Description,
					Config:      teamToCopy.Config.ToLite(),
				}, nil
			}
		}
		return nil, &notFoundError{}
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		for _, tm := range savedTeams {
			if (*tm).Name == name {
				return *tm, nil
			}
		}
		return nil, &notFoundError{}
	}
	mockWebhooksAndTickets(ds)
	mockPoliciesInScope(ds)
	// Stateful default for the Windows enrollment default automdm config row. Tests can override.
	var windowsEnrollmentDefaultTeamID *uint
	ds.GetWindowsEnrollmentDefaultTeamFunc = func(ctx context.Context) (*uint, string, error) {
		if windowsEnrollmentDefaultTeamID == nil {
			return nil, "", nil
		}
		for _, tm := range savedTeams {
			if (*tm).ID == *windowsEnrollmentDefaultTeamID {
				return windowsEnrollmentDefaultTeamID, (*tm).Name, nil
			}
		}
		return nil, "", nil
	}
	ds.SetWindowsEnrollmentDefaultTeamFunc = func(ctx context.Context, teamID *uint) error {
		windowsEnrollmentDefaultTeamID = teamID
		return nil
	}
	ds.TeamByFilenameFunc = func(ctx context.Context, filename string) (*automdm.Team, error) {
		for _, tm := range savedTeams {
			if (*tm).Filename != nil && *(*tm).Filename == filename {
				return *tm, nil
			}
		}
		return nil, &notFoundError{}
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		// Match production semantics: both sides get NFC-normalized before
		// the case-insensitive compare, so two names that only differ by
		// Unicode composition (e.g., precomposed "é" vs. "e" + combining
		// acute accent) are treated as equal.
		needle := norm.NFC.String(name)
		for _, tm := range savedTeams {
			if (*tm).ID == excludeID {
				continue
			}
			if strings.EqualFold(norm.NFC.String((*tm).Name), needle) {
				return *tm, nil
			}
		}
		return nil, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		savedTeams[team.Name] = &team
		return team, nil
	}
	ds.TeamExistsFunc = func(ctx context.Context, teamID uint) (bool, error) {
		return true, nil
	}
	ds.SetOrUpdateMDMAppleDeclarationFunc = func(ctx context.Context, declaration *automdm.MDMAppleDeclaration, usesAutoMDMVars []automdm.AutoMDMVarName, activationAction automdm.MDMAppleActivationAction) (
		*automdm.MDMAppleDeclaration, error,
	) {
		declaration.DeclarationUUID = uuid.NewString()
		return declaration, nil
	}
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
	ds.ListSoftwareCategoriesFunc = func(ctx context.Context, teamID uint) ([]automdm.SoftwareCategory, error) {
		return nil, nil
	}
	ds.BatchNewSoftwareCategoriesFunc = func(ctx context.Context, teamID uint, names []string) error {
		return nil
	}
	ds.DeleteSoftwareCategoryFunc = func(ctx context.Context, id uint) error {
		return nil
	}

	ds.InsertVPPTokenFunc = func(ctx context.Context, tok *automdm.VPPTokenData) (*automdm.VPPTokenDB, error) {
		return &automdm.VPPTokenDB{}, nil
	}
	ds.GetVPPTokenFunc = func(ctx context.Context, tokenID uint) (*automdm.VPPTokenDB, error) {
		return &automdm.VPPTokenDB{}, err
	}
	ds.GetVPPTokenByTeamIDFunc = func(ctx context.Context, teamID *uint) (*automdm.VPPTokenDB, error) {
		return &automdm.VPPTokenDB{}, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}
	ds.GetAssignedVPPAppsFunc = func(ctx context.Context, teamID *uint) (map[automdm.VPPAppID]automdm.VPPAppTeam, error) {
		return map[automdm.VPPAppID]automdm.VPPAppTeam{}, nil
	}
	ds.UpdateVPPTokenTeamsFunc = func(ctx context.Context, id uint, teams []uint) (*automdm.VPPTokenDB, error) {
		return &automdm.VPPTokenDB{}, nil
	}
	ds.ListABMTokensFunc = func(ctx context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{OrganizationName: "Fleet Device Management Inc."}}, nil
	}
	ds.ListSoftwareTitlesFunc = func(ctx context.Context, opt automdm.SoftwareTitleListOptions,
		tmFilter automdm.TeamFilter,
	) ([]automdm.SoftwareTitleListResult, int, *automdm.PaginationMetadata, error) {
		return nil, 0, nil, nil
	}
	ds.SaveABMTokenFunc = func(ctx context.Context, tok *automdm.ABMToken) error {
		return nil
	}
	ds.SetABMTokenDefaultFunc = func(ctx context.Context, tokenID uint) error {
		return nil
	}
	ds.ClearABMTokenDefaultFunc = func(ctx context.Context) error {
		return nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return []*automdm.VPPTokenDB{}, nil
	}
	ds.ListABMTokensFunc = func(ctx context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{}, nil
	}
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
	ds.GetSetupExperienceScriptFunc = func(ctx context.Context, teamID *uint) (*automdm.Script, error) {
		return nil, &notFoundError{}
	}
	ds.DeleteSetupExperienceScriptFunc = func(ctx context.Context, teamID *uint) error {
		return nil
	}
	ds.SetSetupExperienceScriptFunc = func(ctx context.Context, script *automdm.Script) (bool, error) {
		return true, nil
	}
	ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
		return document, nil, nil
	}
	ds.MDMGetEULAMetadataFunc = func(ctx context.Context, platform automdm.MDMEULAPlatform) (*automdm.MDMEULA, error) {
		return nil, &notFoundError{} // No existing EULA
	}
	// A token's platform is whichever EULA the test's metadata mock gives that token, so tests only mock the metadata.
	ds.MDMGetEULAPlatformFunc = func(ctx context.Context, token string) (automdm.MDMEULAPlatform, error) {
		for _, platform := range []automdm.MDMEULAPlatform{automdm.MDMEULAPlatformDarwin, automdm.MDMEULAPlatformWindows} {
			if eula, err := ds.MDMGetEULAMetadataFunc(ctx, platform); err == nil && eula != nil && eula.Token == token {
				return platform, nil
			}
		}
		return "", &notFoundError{}
	}
	ds.BatchApplyCertificateAuthoritiesFunc = func(ctx context.Context, ops automdm.CertificateAuthoritiesBatchOperations) ([]uint, error) {
		return nil, nil
	}
	ds.DeleteIconsAssociatedWithTitlesWithoutInstallersFunc = func(ctx context.Context, teamID uint) error {
		return nil
	}

	ds.GetCertificateTemplatesByTeamIDFunc = func(ctx context.Context, teamID uint, options automdm.ListOptions) ([]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error) {
		return []*automdm.CertificateTemplateResponseSummary{}, &automdm.PaginationMetadata{}, nil
	}

	ds.ListCertificateAuthoritiesFunc = func(ctx context.Context) ([]*automdm.CertificateAuthoritySummary, error) {
		return nil, nil
	}

	t.Setenv("AUTOMDM_SERVER_URL", automdmServerURL)
	t.Setenv("ORG_NAME", orgName)
	t.Setenv("TEST_TEAM_NAME", teamName)
	t.Setenv("APPLE_BM_DEFAULT_TEAM", teamName)

	return ds, &savedAppConfig, savedTeams
}

type AppleVPPConfigSrvConf struct {
	Assets []vpp.Asset
	// AssetsByBearer overrides Assets for requests whose Authorization header
	// is "Bearer <key>". The bearer AutoMDM sends is the uploaded token's base64
	// content.
	AssetsByBearer map[string][]vpp.Asset
	SerialNumbers  []string
}

func StartVPPApplyServer(t *testing.T, config *AppleVPPConfigSrvConf) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "associate") {
			var associations vpp.AssociateAssetsRequest

			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&associations); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}

			if len(associations.Assets) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				res := vpp.ErrorResponse{
					ErrorNumber:  9718,
					ErrorMessage: "This request doesn't contain an asset, which is a required argument. Change the request to provide an asset.",
				}
				if err := json.NewEncoder(w).Encode(res); err != nil {
					panic(err)
				}
				return
			}

			if len(associations.SerialNumbers) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				res := vpp.ErrorResponse{
					ErrorNumber:  9719,
					ErrorMessage: "Either clientUserIds or serialNumbers are required arguments. Change the request to provide assignable users and devices.",
				}
				if err := json.NewEncoder(w).Encode(res); err != nil {
					panic(err)
				}
				return
			}

			var badAssets []vpp.Asset
			for _, reqAsset := range associations.Assets {
				var found bool
				for _, goodAsset := range config.Assets {
					if reqAsset == goodAsset {
						found = true
					}
				}
				if !found {
					badAssets = append(badAssets, reqAsset)
				}
			}

			var badSerials []string
			for _, reqSerial := range associations.SerialNumbers {
				var found bool
				for _, goodSerial := range config.SerialNumbers {
					if reqSerial == goodSerial {
						found = true
					}
				}
				if !found {
					badSerials = append(badSerials, reqSerial)
				}
			}

			if len(badAssets) != 0 || len(badSerials) != 0 {
				errMsg := "error associating assets."
				if len(badAssets) > 0 {
					var badAdamIds []string
					for _, asset := range badAssets {
						badAdamIds = append(badAdamIds, asset.AdamID)
					}
					errMsg += fmt.Sprintf(" assets don't exist on account: %s.", strings.Join(badAdamIds, ", "))
				}
				if len(badSerials) > 0 {
					errMsg += fmt.Sprintf(" bad serials: %s.", strings.Join(badSerials, ", "))
				}
				res := vpp.ErrorResponse{
					ErrorInfo: vpp.ResponseErrorInfo{
						Assets:        badAssets,
						ClientUserIds: []string{"something"},
						SerialNumbers: badSerials,
					},
					// Not sure what error should be returned on each
					// error type
					ErrorNumber:  1,
					ErrorMessage: errMsg,
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				if err := json.NewEncoder(w).Encode(res); err != nil {
					panic(err)
				}
			}
			return
		}

		if strings.Contains(r.URL.Path, "assets") {
			// Then we're responding to GetAssets
			w.Header().Set("Content-Type", "application/json")
			encoder := json.NewEncoder(w)
			assets, ok := config.AssetsByBearer[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
			if !ok {
				assets = config.Assets
			}
			err := encoder.Encode(map[string][]vpp.Asset{"assets": assets})
			if err != nil {
				panic(err)
			}
			return
		}

		resp := []byte(`{"locationName": "AutoMDM Location One", "countryISO2ACode": "US"}`)
		if strings.Contains(r.URL.RawQuery, "invalidToken") {
			// This replicates the response sent back from Apple's VPP endpoints when an invalid
			// token is passed. For more details see:
			// https://developer.apple.com/documentation/devicemanagement/app_and_book_management/app_and_book_management_legacy/interpreting_error_codes
			// https://developer.apple.com/documentation/devicemanagement/client_config
			// https://developer.apple.com/documentation/devicemanagement/errorresponse
			// Note that the Apple server returns 200 in this case.
			resp = []byte(`{"errorNumber": 9622,"errorMessage": "Invalid authentication token"}`)
		}

		if strings.Contains(r.URL.RawQuery, "serverError") {
			resp = []byte(`{"errorNumber": 9603,"errorMessage": "Internal server error"}`)
			w.WriteHeader(http.StatusInternalServerError)
		}

		_, _ = w.Write(resp)
	}))

	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", srv.URL, t)
	t.Cleanup(srv.Close)
}

func StartAndServeVPPServer(t *testing.T) {
	StartAndServeVPPServerWithAssets(t, nil)
}

// StartAndServeVPPServerWithAssets is StartAndServeVPPServer with per-token
// assets, keyed by the bearer AutoMDM sends for each token.
func StartAndServeVPPServerWithAssets(t *testing.T, assetsByBearer map[string][]vpp.Asset) {
	config := &AppleVPPConfigSrvConf{
		AssetsByBearer: assetsByBearer,
		Assets: []vpp.Asset{
			{
				AdamID:         "1",
				PricingParam:   "STDQ",
				AvailableCount: 12,
			},
			{
				AdamID:         "2",
				PricingParam:   "STDQ",
				AvailableCount: 3,
			},
		},
		SerialNumbers: []string{"123", "456"},
	}

	StartVPPApplyServer(t, config)

	// Set up the VPP proxy metadata server using the new format
	// This replaces the old iTunes API format
	vppProxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-bearer-token" || r.Header.Get("vpp-token") == "" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		// deviceFamilies: "mac" -> osx platform, "iphone" -> ios platform, "ipad" -> ios platform
		db := map[string]string{
			// macos app
			"1": `{
				"id": "1",
				"attributes": {
					"name": "App 1",
					"platformAttributes": {
						"osx": {
							"bundleId": "a-1",
							"artwork": {"url": "https://example.com/images/1/{w}x{h}.{f}"},
							"latestVersionInfo": {"versionDisplay": "1.0.0"}
						}
					},
					"deviceFamilies": ["mac"]
				}
			}`,
			// macos, ios, ipados app
			"2": `{
				"id": "2",
				"attributes": {
					"name": "App 2",
					"platformAttributes": {
						"osx": {
							"bundleId": "b-2",
							"artwork": {"url": "https://example.com/images/2-mac/{w}x{h}.{f}"},
							"latestVersionInfo": {"versionDisplay": "1.2.3"}
						},
						"ios": {
							"bundleId": "b-2",
							"artwork": {"url": "https://example.com/images/2/{w}x{h}.{f}"},
							"latestVersionInfo": {"versionDisplay": "2.0.0"}
						}
					},
					"deviceFamilies": ["mac", "iphone", "ipad"]
				}
			}`,
			// ipados app
			"3": `{
				"id": "3",
				"attributes": {
					"name": "App 3",
					"platformAttributes": {
						"ios": {
							"bundleId": "c-3",
							"artwork": {"url": "https://example.com/images/3/{w}x{h}.{f}"},
							"latestVersionInfo": {"versionDisplay": "3.0.0"}
						}
					},
					"deviceFamilies": ["ipad"]
				}
			}`,
		}

		adamIDString := r.URL.Query().Get("ids")
		adamIDs := strings.Split(adamIDString, ",")

		var objs []string
		for _, a := range adamIDs {
			if obj, ok := db[a]; ok {
				objs = append(objs, obj)
			}
		}

		_, _ = w.Write(fmt.Appendf(nil, `{"data": [%s]}`, strings.Join(objs, ",")))
	}))

	vppProxyAuthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"automdmServerSecret": "test-bearer-token"}`))
	}))

	t.Cleanup(vppProxySrv.Close)
	t.Cleanup(vppProxyAuthSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_STOKEN_AUTHENTICATED_APPS_URL", vppProxySrv.URL, t)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_PROXY_AUTH_URL", vppProxyAuthSrv.URL, t)
}

type MockPusher struct{}

func (MockPusher) Push(ctx context.Context, ids []string) (map[string]*push.Response, error) {
	m := make(map[string]*push.Response, len(ids))
	for _, id := range ids {
		m[id] = &push.Response{Id: id}
	}
	return m, nil
}

type MemKeyValueStore struct {
	m sync.Map
}

func NewMemKeyValueStore() *MemKeyValueStore {
	return &MemKeyValueStore{}
}

func (m *MemKeyValueStore) Set(ctx context.Context, key string, value string, expireTime time.Duration) error {
	m.m.Store(key, value)
	return nil
}

func (m *MemKeyValueStore) Get(ctx context.Context, key string) (*string, error) {
	v, ok := m.m.Load(key)
	if !ok {
		return nil, nil
	}
	vAsString := v.(string)
	return &vAsString, nil
}

func AddLabelMocks(ds *mock.Store) {
	ds.GetLabelSpecsFunc = func(ctx context.Context, filter automdm.TeamFilter) ([]*automdm.LabelSpec, error) {
		return []*automdm.LabelSpec{
			{
				Name:                "a",
				Description:         "A global label",
				LabelMembershipType: automdm.LabelMembershipTypeManual,
				Hosts:               []string{"host2", "host3"},
			},
			{
				Name:                "b",
				Description:         "Another global label",
				LabelMembershipType: automdm.LabelMembershipTypeDynamic,
				Query:               "SELECT 1 from osquery_info",
			},
		}, nil
	}
	ds.ApplyLabelSpecsWithAuthorFunc = func(ctx context.Context, specs []*automdm.LabelSpec, authorID *uint) (err error) {
		return nil
	}
	ds.SetAsideLabelsFunc = func(ctx context.Context, teamID *uint, labels []automdm.LabelToMove, user automdm.User) error {
		return nil
	}

	ds.DeleteLabelFunc = func(ctx context.Context, id uint) error { return nil }
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		validLabels := map[string]*automdm.Label{
			"a": {
				ID:   1,
				Name: "a",
			},
			"b": {
				ID:   2,
				Name: "b",
			},
		}

		found := make(map[string]*automdm.Label)
		for _, l := range names {
			if label, ok := validLabels[l]; ok {
				found[l] = label
			}
		}
		return found, nil
	}
}

// LabelsByID serves label lookups by ID from a test's label specs, since GitOps deletes labels by ID.
func LabelsByID(specs ...*automdm.LabelSpec) func(context.Context, uint, automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
	return func(_ context.Context, lid uint, _ automdm.TeamFilter) (*automdm.LabelWithTeamName, []uint, error) {
		for _, s := range specs {
			if s.ID == lid {
				return &automdm.LabelWithTeamName{ID: s.ID, Name: s.Name, TeamID: s.TeamID, LabelType: s.LabelType}, nil, nil
			}
		}
		return nil, nil, &notFoundError{}
	}
}

// RecordDeletedLabelNames records the name of each label a test deletes. Labels are deleted by ID, so the name comes from the
// test's Label mock.
func RecordDeletedLabelNames(ds *mock.Store, names *[]string) mock.DeleteLabelFunc {
	return func(ctx context.Context, id uint) error {
		l, _, err := ds.LabelFunc(ctx, id, automdm.TeamFilter{})
		if err != nil {
			return err
		}
		*names = append(*names, l.Name)
		return nil
	}
}

type notFoundError struct{}

var _ automdm.NotFoundError = (*notFoundError)(nil)

func (e *notFoundError) IsNotFound() bool {
	return true
}

func (e *notFoundError) Error() string {
	return ""
}

// mockWebhooksAndTickets reports no policy with the webhook or ticket automation on. Tests can override.
func mockWebhooksAndTickets(ds *mock.Store) {
	ds.WebhooksAndTicketsByPolicyNameFunc = func(ctx context.Context, teamID *uint, names []string) (map[string]bool, error) {
		return nil, nil
	}
	ds.WebhooksAndTicketsByPatchPolicySlugFunc = func(ctx context.Context, teamID uint, slugs []string) (map[string]bool, error) {
		return nil, nil
	}
}

// mockPoliciesInScope serves the combined policy list and count from the per-scope mocks a test sets, so tests written
// against those keep working.
func mockPoliciesInScope(ds *mock.Store) {
	ds.ListPoliciesInScopeFunc = func(ctx context.Context, teamID *uint, mergeInherited bool, opts automdm.ListOptions,
		filter automdm.PolicyListFilter,
	) ([]*automdm.Policy, error) {
		switch {
		case teamID == nil && ds.ListGlobalPoliciesFunc != nil:
			return ds.ListGlobalPolicies(ctx, opts, filter.Platform)
		case teamID != nil && mergeInherited && ds.ListMergedTeamPoliciesFunc != nil:
			return ds.ListMergedTeamPolicies(ctx, *teamID, opts, automdm.PolicyAutomationTypeNone, filter.Platform)
		case teamID != nil && ds.ListTeamPoliciesFunc != nil:
			policies, _, err := ds.ListTeamPolicies(ctx, *teamID, opts, automdm.ListOptions{}, automdm.PolicyAutomationTypeNone, filter.Platform)
			return policies, err
		}
		return nil, nil
	}
	ds.CountPoliciesInScopeFunc = func(ctx context.Context, teamID *uint, mergeInherited bool, matchQuery string,
		filter automdm.PolicyListFilter,
	) (int, error) {
		policies, err := ds.ListPoliciesInScope(ctx, teamID, mergeInherited, automdm.ListOptions{MatchQuery: matchQuery}, filter)
		return len(policies), err
	}
}
