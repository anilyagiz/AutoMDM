package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/anilyagiz/automdm/v4/pkg/nettest"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/tokenpki"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/schedule/scheduletest"
	"github.com/anilyagiz/automdm/v4/server/vulnerabilities/govulndb"
	"github.com/jmoiron/sqlx"
	"github.com/smallstep/pkcs7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// safeStore is a wrapper around mock.Store to allow for concurrent calling to
// AppConfig, Lock, and Unlock, in the past we have seen this test fail with a data race warning.
//
// TODO: if we see other tests failing for similar reasons, we should build a
// more robust pattern instead of doing this everywhere
type safeStore struct {
	mock.Store
	mu sync.Mutex
}

func (s *safeStore) AppConfig(ctx context.Context) (*automdm.AppConfig, error) {
	s.mu.Lock()
	s.AppConfigFuncInvoked = true
	s.mu.Unlock()
	return s.AppConfigFunc(ctx)
}

func (s *safeStore) Lock(ctx context.Context, name string, owner string, expiration time.Duration) (bool, error) {
	s.mu.Lock()
	s.LockFuncInvoked = true
	s.mu.Unlock()
	return s.LockFunc(ctx, name, owner, expiration)
}

func (s *safeStore) Unlock(ctx context.Context, name string, owner string) error {
	s.mu.Lock()
	s.UnlockFuncInvoked = true
	s.mu.Unlock()
	return s.UnlockFunc(ctx, name, owner)
}

func TestMaybeSendStatistics(t *testing.T) {
	ds := new(mock.Store)

	automdmConfig := config.AutoMDMConfig{Osquery: config.OsqueryConfig{DetailUpdateInterval: 1 * time.Hour}}

	requestBody := ""

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requestBody = string(requestBodyBytes)
	}))
	defer ts.Close()

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{EnableAnalytics: true}}, nil
	}

	ds.ShouldSendStatisticsFunc = func(ctx context.Context, frequency time.Duration, config config.AutoMDMConfig) (automdm.StatisticsPayload, bool, error) {
		return automdm.StatisticsPayload{
			AnonymousIdentifier:                  "ident",
			AutoMDMVersion:                       "1.2.3",
			LicenseTier:                          "premium",
			NumHostsEnrolled:                     999,
			NumHostsABMPending:                   888,
			NumUsers:                             99,
			NumSoftwareVersions:                  100,
			NumHostSoftwares:                     101,
			NumSoftwareTitles:                    102,
			NumHostSoftwareInstalledPaths:        103,
			NumSoftwareCPEs:                      104,
			NumSoftwareCVEs:                      105,
			NumTeams:                             9,
			NumPolicies:                          0,
			NumPoliciesAutomationEnabledSoftware: 7,
			NumQueries:                           200,
			NumLabels:                            3,
			SoftwareInventoryEnabled:             true,
			VulnDetectionEnabled:                 true,
			SystemUsersEnabled:                   true,
			HostsStatusWebHookEnabled:            true,
			NumWeeklyActiveUsers:                 111,
			NumWeeklyPolicyViolationDaysActual:   0,
			NumWeeklyPolicyViolationDaysPossible: 0,
			HostsEnrolledByOperatingSystem: map[string][]automdm.HostsCountByOSVersion{
				"linux": {
					automdm.HostsCountByOSVersion{Version: "1.2.3", NumEnrolled: 22},
				},
			},
			HostsEnrolledByOrbitVersion:   []automdm.HostsCountByOrbitVersion{},
			HostsEnrolledByOsqueryVersion: []automdm.HostsCountByOsqueryVersion{},
			StoredErrors:                  []byte(`[]`),
			Organization:                  "AutoMDM",
			AIFeaturesDisabled:            true,
			MaintenanceWindowsEnabled:     true,
			MaintenanceWindowsConfigured:  true,
			NumHostsAutoMDMDesktopEnabled: 1984,
			AutoMDMMaintainedAppsMacOS: []automdm.AutoMDMMaintainedAppUsage{
				{Name: "1password/darwin", PatchPolicy: true, SoftwareAutomation: false},
			},
			AutoMDMMaintainedAppsWindows: []automdm.AutoMDMMaintainedAppUsage{
				{Name: "google-chrome/windows", PatchPolicy: true, SoftwareAutomation: true},
			},
			GitOpsModeEnabled:                 true,
			GitOpsModeExceptions:              []string{"labels", "software", "secrets"},
			AutoMDMDesktopSSOEnabled:          true,
			NumHostsAutoMDMMDMEnrolledMacOS:   12,
			NumHostsAutoMDMMDMEnrolledWindows: 34,
			NumHostsAutoMDMMDMEnrolledIOS:     56,
			NumHostsAutoMDMMDMEnrolledIpadOS:  78,
			ResultLogDestination:              "firehose",
			StatusLogDestination:              "filesystem",
			AuditLogDestination:               "kinesis",
			AnyVulnerabilitiesWebhookEnabled:  true,
			AnyFailingPoliciesWebhookEnabled:  true,
			AnyHostActivitiesWebhookEnabled:   true,
			GlobalActivityWebhookEnabled:      true,
			TicketDestinationConfigured:       true,
			SSOConfiguredTeamUsers:            true,
			SSOConfiguredEndUsers:             true,
			AccountProvisioningConfigured:     true,
			IDPSCIMConfigured:                 true,
			IDPGoogleWorkspaceConfigured:      true,
			CertificateAuthorityConfigured:    true,
			OktaConditionalAccessConfigured:   true,
			EntraConditionalAccessConfigured:  true,
			GoogleConditionalAccessConfigured: true,
		}, true, nil
	}
	recorded := false
	ds.RecordStatisticsSentFunc = func(ctx context.Context) error {
		recorded = true
		return nil
	}
	cleanedup := false
	ds.CleanupStatisticsFunc = func(ctx context.Context) error {
		cleanedup = true
		return nil
	}

	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	err := trySendStatistics(ctx, ds, automdm.StatisticsFrequency, ts.URL, automdmConfig, slog.Default())
	require.NoError(t, err)
	assert.True(t, recorded)
	require.True(t, cleanedup)
	assert.JSONEq(t, `{"anonymousIdentifier":"ident","automdmVersion":"1.2.3","licenseTier":"premium","organization":"AutoMDM","numHostsEnrolled":999,"numHostsABMPending":888,"numUsers":99,"numSoftwareVersions":100,"numHostSoftwares":101,"numSoftwareTitles":102,"numHostSoftwareInstalledPaths":103,"numSoftwareCPEs":104,"numSoftwareCVEs":105,"numTeams":9,"numPolicies":0,"numPoliciesAutomationEnabledSoftware":7,"numQueries":200,"numLabels":3,"softwareInventoryEnabled":true,"vulnDetectionEnabled":true,"systemUsersEnabled":true,"hostsStatusWebHookEnabled":true,"mdmMacOsEnabled":false,"hostExpiryEnabled":false,"mdmWindowsEnabled":false,"mdmAndroidEnabled":false,"mdmRecoveryLockPasswordEnabled":false,"liveQueryDisabled":false,"numWeeklyActiveUsers":111,"numWeeklyPolicyViolationDaysActual":0,"numWeeklyPolicyViolationDaysPossible":0,"hostsEnrolledByOperatingSystem":{"linux":[{"version":"1.2.3","numEnrolled":22}]},"hostsEnrolledByOrbitVersion":[],"hostsEnrolledByOsqueryVersion":[],"storedErrors":[],"numHostsNotResponding":0,"aiFeaturesDisabled":true,"maintenanceWindowsEnabled":true,"maintenanceWindowsConfigured":true,"idpGoogleWorkspaceConfigured":true,"numHostsAutoMDMDesktopEnabled":1984,"automdmDesktopSSOEnabled":true,"automdmMaintainedAppsMacOS":[{"name":"1password/darwin","patchPolicy":true,"softwareAutomation":false}],"automdmMaintainedAppsWindows":[{"name":"google-chrome/windows","patchPolicy":true,"softwareAutomation":true}],"conditionalAccessEnabled":false,"oktaConditionalAccessConfigured":true,"conditionalAccessBypassDisabled":false,"entraConditionalAccessConfigured":true,"googleConditionalAccessConfigured":true,"gitOpsModeEnabled":true,"gitOpsModeExceptions":["labels","software","secrets"],"numHostsAutoMDMMDMEnrolledMacOS":12,"numHostsAutoMDMMDMEnrolledWindows":34,"numHostsAutoMDMMDMEnrolledIOS":56,"numHostsAutoMDMMDMEnrolledIpadOS":78,"numMDMAppleProfiles":0,"numMDMWindowsProfiles":0,"numMDMAppleDeclarations":0,"numMDMAndroidProfiles":0,"numAppleAiGeneratedProfiles":0,"numWindowsAiGeneratedProfiles":0,"numAndroidAiGeneratedProfiles":0,"resultLogDestination":"firehose","statusLogDestination":"filesystem","auditLogDestination":"kinesis","anyVulnerabilitiesWebhookEnabled":true,"anyFailingPoliciesWebhookEnabled":true,"anyHostActivitiesWebhookEnabled":true,"globalActivityWebhookEnabled":true,"ticketDestinationConfigured":true,"ssoConfiguredTeamUsers":true,"ssoConfiguredEndUsers":true,"accountProvisioningConfigured":true,"idpSCIMConfigured":true,"certificateAuthorityConfigured":true}`, requestBody)
}

func TestMaybeSendStatisticsSkipsSendingIfNotNeeded(t *testing.T) {
	ds := new(mock.Store)

	automdmConfig := config.AutoMDMConfig{Osquery: config.OsqueryConfig{DetailUpdateInterval: 1 * time.Hour}}

	called := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{EnableAnalytics: true}}, nil
	}

	ds.ShouldSendStatisticsFunc = func(ctx context.Context, frequency time.Duration, cfg config.AutoMDMConfig) (automdm.StatisticsPayload, bool, error) {
		return automdm.StatisticsPayload{}, false, nil
	}
	recorded := false
	ds.RecordStatisticsSentFunc = func(ctx context.Context) error {
		recorded = true
		return nil
	}
	cleanedup := false
	ds.CleanupStatisticsFunc = func(ctx context.Context) error {
		cleanedup = true
		return nil
	}

	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	err := trySendStatistics(ctx, ds, automdm.StatisticsFrequency, ts.URL, automdmConfig, slog.Default())
	require.NoError(t, err)
	assert.False(t, recorded)
	assert.False(t, cleanedup)
	assert.False(t, called)
}

func TestMaybeSendStatisticsSkipsIfNotConfigured(t *testing.T) {
	ds := new(mock.Store)

	automdmConfig := config.AutoMDMConfig{Osquery: config.OsqueryConfig{DetailUpdateInterval: 1 * time.Hour}}

	called := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierFree})
	err := trySendStatistics(ctx, ds, automdm.StatisticsFrequency, ts.URL, automdmConfig, slog.Default())
	require.NoError(t, err)
	assert.False(t, called)
}

func TestMaybeSendStatisticsSendsIfNotConfiguredForPremium(t *testing.T) {
	ds := new(mock.Store)

	automdmConfig := config.AutoMDMConfig{Osquery: config.OsqueryConfig{DetailUpdateInterval: 1 * time.Hour}}

	called := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ds.ShouldSendStatisticsFunc = func(ctx context.Context, frequency time.Duration, config config.AutoMDMConfig) (automdm.StatisticsPayload, bool, error) {
		return automdm.StatisticsPayload{}, true, nil
	}

	ds.CleanupStatisticsFunc = func(ctx context.Context) error { return nil }

	ds.RecordStatisticsSentFunc = func(ctx context.Context) error { return nil }

	ctx := license.NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	err := trySendStatistics(ctx, ds, automdm.StatisticsFrequency, ts.URL, automdmConfig, slog.Default())
	require.NoError(t, err)
	assert.True(t, called)
}

func TestAutomationsSchedule(t *testing.T) {
	ds := new(safeStore)

	endpointCalled := int32(0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&endpointCalled, 1)
	}))
	defer ts.Close()

	ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
		return []*automdm.TeamSummary{}, nil
	}

	ds.OutdatedAutomationBatchFunc = func(ctx context.Context) ([]automdm.PolicyFailure, error) {
		return []automdm.PolicyFailure{}, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			WebhookSettings: automdm.WebhookSettings{
				HostStatusWebhook: automdm.HostStatusWebhookSettings{
					Enable:         true,
					DestinationURL: ts.URL,
					HostPercentage: 43,
					DaysCount:      2,
				},
				Interval: automdm.Duration{Duration: 2 * time.Second},
			},
		}, nil
	}

	mockLocker := scheduletest.SetupMockLocker("automations", "test_instance", time.Now().UTC())
	ds.LockFunc = mockLocker.Lock
	ds.UnlockFunc = mockLocker.Unlock

	mockStatsStore := scheduletest.SetUpMockStatsStore("automations")
	ds.GetLatestCronStatsFunc = mockStatsStore.GetLatestCronStats
	ds.InsertCronStatsFunc = mockStatsStore.InsertCronStats
	ds.UpdateCronStatsFunc = mockStatsStore.UpdateCronStats
	ds.ClaimCronStatsFunc = mockStatsStore.ClaimCronStats

	calledOnce := make(chan struct{})
	calledTwice := make(chan struct{})
	ds.TotalAndUnseenHostsSinceFunc = func(ctx context.Context, teamID *uint, daysCount int) (int, []uint, error) {
		defer func() {
			select {
			case <-calledOnce:
				select {
				case <-calledTwice:
				default:
					close(calledTwice)
				}
			default:
				close(calledOnce)
			}
		}()
		return 10, []uint{1, 2, 3, 4, 5, 6}, nil
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	failingPoliciesSet := service.NewMemFailingPolicySet()
	s, err := newAutomationsSchedule(ctx, "test_instance", ds, slog.New(slog.DiscardHandler), 5*time.Minute, failingPoliciesSet, &mock.MockActivityService{})
	require.NoError(t, err)
	s.Start()

	<-calledOnce
	time.Sleep(1 * time.Second)
	assert.Equal(t, int32(1), atomic.LoadInt32(&endpointCalled))
	<-calledTwice
	time.Sleep(1 * time.Second)
	assert.GreaterOrEqual(t, int32(2), atomic.LoadInt32(&endpointCalled))
}

func TestCronVulnerabilitiesCreatesDatabasesPath(t *testing.T) {
	t.Parallel()
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	ds := new(safeStore)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			Features: automdm.Features{EnableSoftwareInventory: true},
		}, nil
	}
	ds.InsertCVEMetaFunc = func(ctx context.Context, x []automdm.CVEMeta) error {
		return nil
	}
	ds.AllSoftwareIteratorFunc = func(ctx context.Context, query automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
		// we should not get this far before we see the directory being created
		return nil, errors.New("shouldn't happen")
	}
	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{}, nil
	}
	ds.SyncHostsSoftwareFunc = func(ctx context.Context, updatedAt time.Time) error {
		return nil
	}

	mockLocker := scheduletest.SetupMockLocker("vulnerabilities", "test_instance", time.Now().UTC())
	ds.LockFunc = mockLocker.Lock
	ds.UnlockFunc = mockLocker.Unlock

	mockStatsStore := scheduletest.SetUpMockStatsStore("vulnerabilities")
	ds.GetLatestCronStatsFunc = mockStatsStore.GetLatestCronStats
	ds.InsertCronStatsFunc = mockStatsStore.InsertCronStats
	ds.UpdateCronStatsFunc = mockStatsStore.UpdateCronStats
	ds.ClaimCronStatsFunc = mockStatsStore.ClaimCronStats

	vulnPath := filepath.Join(t.TempDir(), "something")
	require.NoDirExists(t, vulnPath)

	config := config.VulnerabilitiesConfig{
		DatabasesPath:         vulnPath,
		Periodicity:           time.Second,
		CurrentInstanceChecks: "auto",
	}
	// Use schedule to test that the schedule does indeed call cronVulnerabilities.
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	ctx, cancel := context.WithCancel(ctx)
	lg := slog.New(slog.DiscardHandler)

	go func() {
		defer func() {
			// this test may panic as we're ending it early, so we shouldn't fail our suite on this panic
			// but depending on where we are in the cron call it may not panic, hence not checking the recover
			// value either way
			// nolint:errcheck
			recover()
		}()
		_ = cronVulnerabilities(ctx, ds, lg, &config)
	}()

	assert.Eventually(t, func() bool {
		info, err := os.Lstat(vulnPath)
		return err == nil && info.IsDir()
	}, 10*time.Second, 1*time.Second)

	t.Cleanup(func() {
		cancel()
	})

	// at this point, the assertion has succeeded or failed, try to remove the
	// temp dir and all content instead of leaving it to the testing package to
	// handle - the assumption is that this test used to fail because it tried to
	// remove the temp dir while trying to write to it concurrently, and resulted
	// in e.g.:
	// === RUN   TestCronVulnerabilitiesCreatesDatabasesPath
	//  testing.go:1231: TempDir RemoveAll cleanup: unlinkat /tmp/TestCronVulnerabilitiesCreatesDatabasesPath2986870230/001/something: directory not empty
	// --- FAIL: TestCronVulnerabilitiesCreatesDatabasesPath (60.22s)
	for err := os.RemoveAll(vulnPath); err != nil; err = os.RemoveAll(vulnPath) {
		if strings.Contains(err.Error(), "directory not empty") {
			time.Sleep(time.Second)
			continue
		}
		// for any other unexpected error, fail the test
		require.NoError(t, err)
	}
}

type softwareIterator struct {
	index     int
	softwares []*automdm.Software
}

func (f *softwareIterator) Next() bool {
	return f.index < len(f.softwares)
}

func (f *softwareIterator) Value() (*automdm.Software, error) {
	s := f.softwares[f.index]
	f.index++
	return s, nil
}

func (f *softwareIterator) Err() error   { return nil }
func (f *softwareIterator) Close() error { return nil }

func TestScanVulnerabilities(t *testing.T) {
	nettest.Run(t)

	logger := slog.New(slog.DiscardHandler)

	ctx := context.Background()

	webhookCount := 0
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCount++

		var payload map[string]json.RawMessage
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		expected := `
{
  "cve": "CVE-2022-39348",
  "cve_published": "2022-10-26T14:15:00Z",
  "details_link": "https://nvd.nist.gov/vuln/detail/CVE-2022-39348",
  "epss_probability": 0.0089,
  "cvss_score": 5.4,
  "cisa_known_exploit": false,
  "hosts_affected": [
    {
      "id": 1,
      "hostname": "1",
      "display_name": "1",
      "url": "hosts/1"
    }
  ]
}
`
		require.JSONEq(t, expected, string(payload["vulnerability"]))
	}))

	appConfig := &automdm.AppConfig{
		Features: automdm.Features{
			EnableSoftwareInventory: true,
		},
		WebhookSettings: automdm.WebhookSettings{
			VulnerabilitiesWebhook: automdm.VulnerabilitiesWebhookSettings{
				Enable:         true,
				DestinationURL: svr.URL,
			},
		},
	}

	ds := new(mock.Store)
	ds.InsertCVEMetaFunc = func(ctx context.Context, x []automdm.CVEMeta) error {
		return nil
	}
	ds.AllSoftwareIteratorFunc = func(ctx context.Context, query automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
		iterator := &softwareIterator{
			softwares: []*automdm.Software{
				{
					ID:               1,
					Name:             "Twisted",
					Version:          "22.2.0",
					BundleIdentifier: "",
					Source:           "python_packages",
				},
			},
		}
		return iterator, nil
	}
	ds.ListSoftwareCPEsFunc = func(ctx context.Context) ([]automdm.SoftwareCPE, error) {
		return []automdm.SoftwareCPE{
			{
				ID:         1,
				SoftwareID: 1,
				CPE:        "cpe:2.3:a:twistedmatrix:twisted:22.2.0:*:*:*:*:python:*:*",
			},
		}, nil
	}
	ds.InsertSoftwareVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.SoftwareVulnerability, src automdm.VulnerabilitySource) ([]automdm.SoftwareVulnerability, error) {
		// Return the vuln that triggers the webhook.
		return []automdm.SoftwareVulnerability{
			{SoftwareID: 1, CVE: "CVE-2022-39348"},
		}, nil
	}
	ds.UpsertSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) {
		return int64(0), nil
	}
	ds.DeleteSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) {
		return int64(0), nil
	}
	ds.InsertOSVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.OSVulnerability, src automdm.VulnerabilitySource) (int64, error) {
		return 0, nil
	}
	ds.DeleteOutOfDateVulnerabilitiesFunc = func(ctx context.Context, source automdm.VulnerabilitySource, olderThan time.Time) error {
		return nil
	}
	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{
			CountsUpdatedAt: time.Now(),
			OSVersions: []automdm.OSVersion{
				{HostsCount: 1, Name: "Ubuntu 22.04.1 LTS", Platform: "ubuntu"},
			},
		}, nil
	}
	ds.HostIDsByOSVersionFunc = func(ctx context.Context, osVersion automdm.OSVersion, offset int, limit int) ([]uint, error) {
		if offset == 0 {
			return []uint{1}, nil
		}
		return []uint{}, nil
	}
	ds.ListSoftwareForVulnDetectionFunc = func(ctx context.Context, filter automdm.VulnSoftwareFilter) ([]automdm.Software, error) {
		return []automdm.Software{
			{
				ID:               1,
				Name:             "Twisted",
				Version:          "22.2.0",
				BundleIdentifier: "",
				Source:           "python_packages",
			},
		}, nil
	}
	ds.ListSoftwareVulnerabilitiesByHostIDsSourceFunc = func(ctx context.Context, hostIDs []uint, source automdm.VulnerabilitySource) (map[uint][]automdm.SoftwareVulnerability, error) {
		require.Equal(t, []uint{1}, hostIDs)
		require.Equal(t, automdm.UbuntuOVALSource, source)
		return map[uint][]automdm.SoftwareVulnerability{}, nil
	}
	ds.ListOperatingSystemsFunc = func(ctx context.Context) ([]automdm.OperatingSystem, error) {
		return []automdm.OperatingSystem{
			{
				ID:            1,
				Name:          "Ubuntu",
				Version:       "22.04.1 LTS",
				Arch:          "x86_64",
				KernelVersion: "5.10.124-linuxkit",
			},
		}, nil
	}

	ds.ListOperatingSystemsForPlatformFunc = func(ctx context.Context, platform string) ([]automdm.OperatingSystem, error) {
		return []automdm.OperatingSystem{}, nil
	}

	ds.DeleteOutOfDateOSVulnerabilitiesFunc = func(ctx context.Context, src automdm.VulnerabilitySource, t time.Time) error {
		return nil
	}
	ds.DeleteOrphanedSoftwareVulnerabilitiesFunc = func(ctx context.Context) error {
		return nil
	}
	ds.DeleteOrphanedOSVulnerabilitiesFunc = func(ctx context.Context) error {
		return nil
	}
	ds.ListCVEsFunc = func(ctx context.Context, maxAge time.Duration) ([]automdm.CVEMeta, error) {
		published := time.Date(2022, time.October, 26, 14, 15, 0, 0, time.UTC)

		return []automdm.CVEMeta{
			{
				CVE:              "CVE-2022-39348",
				CVSSScore:        ptr.Float64(5.4),
				EPSSProbability:  ptr.Float64(0.0089),
				CISAKnownExploit: ptr.Bool(false),
				Published:        &published,
			},
		}, nil
	}
	ds.HostVulnSummariesBySoftwareIDsFunc = func(ctx context.Context, softwareIDs []uint) ([]automdm.HostVulnerabilitySummary, error) {
		return []automdm.HostVulnerabilitySummary{
			{
				ID:          1,
				Hostname:    "1",
				DisplayName: "1",
			},
		}, nil
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}

	vulnPath := filepath.Join("..", "..", "server", "vulnerabilities", "testdata")

	vulnsConfig := config.VulnerabilitiesConfig{
		DatabasesPath:         vulnPath,
		Periodicity:           10 * time.Second,
		CurrentInstanceChecks: "auto",
		DisableDataSync:       true,
	}

	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	err := scanVulnerabilities(ctx, ds, logger, &vulnsConfig, appConfig, vulnPath)
	require.NoError(t, err)

	// ensure that nvd vulnerabilities are not deleted
	require.False(t, ds.DeleteSoftwareVulnerabilitiesFuncInvoked)

	// ensure that webhook was called
	require.Equal(t, 1, webhookCount)
}

func TestScanVulnerabilitiesFreeTier(t *testing.T) {
	nettest.Run(t)

	logger := slog.New(slog.DiscardHandler)

	ctx := context.Background()

	var mu sync.Mutex
	var webhookCVEs []string
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		// Free tier payload
		var vuln map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload["vulnerability"], &vuln))
		require.NotContains(t, vuln, "epss_probability")
		require.NotContains(t, vuln, "cvss_score")
		require.NotContains(t, vuln, "cisa_known_exploit")
		require.NotContains(t, vuln, "cve_published")
		require.Contains(t, vuln, "cve")
		require.Contains(t, vuln, "details_link")
		require.Contains(t, vuln, "hosts_affected")

		var cve string
		require.NoError(t, json.Unmarshal(vuln["cve"], &cve))
		mu.Lock()
		webhookCVEs = append(webhookCVEs, cve)
		mu.Unlock()
	}))

	appConfig := &automdm.AppConfig{
		Features: automdm.Features{
			EnableSoftwareInventory: true,
		},
		WebhookSettings: automdm.WebhookSettings{
			VulnerabilitiesWebhook: automdm.VulnerabilitiesWebhookSettings{
				Enable:         true,
				DestinationURL: svr.URL,
			},
		},
	}

	ds := new(mock.Store)
	ds.InsertCVEMetaFunc = func(ctx context.Context, x []automdm.CVEMeta) error {
		return nil
	}
	ds.AllSoftwareIteratorFunc = func(ctx context.Context, query automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
		iterator := &softwareIterator{
			softwares: []*automdm.Software{
				{
					ID:               1,
					Name:             "Twisted",
					Version:          "22.2.0",
					BundleIdentifier: "",
					Source:           "python_packages",
				},
			},
		}
		return iterator, nil
	}
	ds.ListSoftwareCPEsFunc = func(ctx context.Context) ([]automdm.SoftwareCPE, error) {
		return []automdm.SoftwareCPE{
			{
				ID:         1,
				SoftwareID: 1,
				CPE:        "cpe:2.3:a:twistedmatrix:twisted:22.2.0:*:*:*:*:python:*:*",
			},
		}, nil
	}
	ds.InsertSoftwareVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.SoftwareVulnerability, src automdm.VulnerabilitySource) ([]automdm.SoftwareVulnerability, error) {
		return vulns, nil
	}
	ds.InsertOSVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.OSVulnerability, src automdm.VulnerabilitySource) (int64, error) {
		return 0, nil
	}
	ds.UpsertSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) {
		return int64(0), nil
	}
	ds.DeleteSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) {
		return int64(0), nil
	}
	ds.DeleteOutOfDateVulnerabilitiesFunc = func(ctx context.Context, source automdm.VulnerabilitySource, olderThan time.Time) error {
		return nil
	}
	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{
			CountsUpdatedAt: time.Now(),
			OSVersions: []automdm.OSVersion{
				{HostsCount: 1, Name: "Ubuntu 22.04.1 LTS", Platform: "ubuntu"},
			},
		}, nil
	}
	ds.HostIDsByOSVersionFunc = func(ctx context.Context, osVersion automdm.OSVersion, offset int, limit int) ([]uint, error) {
		if offset == 0 {
			return []uint{1}, nil
		}
		return []uint{}, nil
	}
	ds.ListSoftwareForVulnDetectionFunc = func(ctx context.Context, filter automdm.VulnSoftwareFilter) ([]automdm.Software, error) {
		return []automdm.Software{
			{
				ID:               1,
				Name:             "Twisted",
				Version:          "22.2.0",
				BundleIdentifier: "",
				Source:           "python_packages",
			},
		}, nil
	}
	ds.ListSoftwareVulnerabilitiesByHostIDsSourceFunc = func(ctx context.Context, hostIDs []uint, source automdm.VulnerabilitySource) (map[uint][]automdm.SoftwareVulnerability, error) {
		require.Equal(t, []uint{1}, hostIDs)
		require.Equal(t, automdm.UbuntuOVALSource, source)
		return map[uint][]automdm.SoftwareVulnerability{}, nil
	}
	ds.ListOperatingSystemsFunc = func(ctx context.Context) ([]automdm.OperatingSystem, error) {
		return []automdm.OperatingSystem{
			{
				ID:            1,
				Name:          "Ubuntu",
				Version:       "22.04.1 LTS",
				Arch:          "x86_64",
				KernelVersion: "5.10.124-linuxkit",
			},
		}, nil
	}
	ds.ListOperatingSystemsForPlatformFunc = func(ctx context.Context, platform string) ([]automdm.OperatingSystem, error) {
		return []automdm.OperatingSystem{}, nil
	}
	ds.DeleteOutOfDateOSVulnerabilitiesFunc = func(ctx context.Context, src automdm.VulnerabilitySource, t time.Time) error {
		return nil
	}
	ds.DeleteOrphanedSoftwareVulnerabilitiesFunc = func(ctx context.Context) error {
		return nil
	}
	ds.DeleteOrphanedOSVulnerabilitiesFunc = func(ctx context.Context) error {
		return nil
	}
	ds.ListCVEsFunc = func(ctx context.Context, maxAge time.Duration) ([]automdm.CVEMeta, error) {
		t.Error("ListCVEs should not be called on free tier")
		return nil, nil
	}
	ds.HostVulnSummariesBySoftwareIDsFunc = func(ctx context.Context, softwareIDs []uint) ([]automdm.HostVulnerabilitySummary, error) {
		return []automdm.HostVulnerabilitySummary{
			{
				ID:          1,
				Hostname:    "1",
				DisplayName: "1",
			},
		}, nil
	}
	ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
		return true, nil
	}

	vulnPath := filepath.Join("..", "..", "server", "vulnerabilities", "testdata")

	vulnsConfig := config.VulnerabilitiesConfig{
		DatabasesPath:         vulnPath,
		Periodicity:           10 * time.Second,
		CurrentInstanceChecks: "auto",
		DisableDataSync:       true,
	}

	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierFree})
	err := scanVulnerabilities(ctx, ds, logger, &vulnsConfig, appConfig, vulnPath)
	require.NoError(t, err)

	require.False(t, ds.DeleteSoftwareVulnerabilitiesFuncInvoked)

	require.NotEmpty(t, webhookCVEs)
}

// A Go toolchain install's CVEs move from NVD to govulndb. The govulndb phase has to refresh
// them before NVD's stale cleanup runs, or they are deleted and then inserted again as new,
// firing vulnerability automations for CVEs hosts already had.
func TestScanVulnerabilitiesGoToolchainsBeforeNVDCleanup(t *testing.T) {
	type vulnWrite struct {
		op     string
		source automdm.VulnerabilitySource
	}
	logger := slog.New(slog.DiscardHandler)

	// The shared NVD fixtures plus a Go vulnerability database artifact.
	vulnPath := t.TempDir()
	testdata, err := filepath.Abs(filepath.Join("..", "..", "server", "vulnerabilities", "testdata"))
	require.NoError(t, err)
	entries, err := os.ReadDir(testdata)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, os.Symlink(filepath.Join(testdata, e.Name()), filepath.Join(vulnPath, e.Name())))
	}
	var artifact bytes.Buffer
	gz := gzip.NewWriter(&artifact)
	require.NoError(t, json.NewEncoder(gz).Encode(govulndb.Artifact{
		SchemaVersion: govulndb.SchemaVersion,
		Modules: map[string][]govulndb.Advisory{
			govulndb.StdlibModule: {{ID: "GO-2099-0001", CVEs: []string{"CVE-2099-0001"}, Ranges: []govulndb.VersionRange{{Introduced: "0", Fixed: "1.30.0"}}}},
		},
		ToolchainInstalls: govulndb.PublishedToolchainInstalls(),
	}))
	require.NoError(t, gz.Close())
	require.NoError(t, os.WriteFile(filepath.Join(vulnPath, govulndb.FilePrefix+"2099-01-01"+govulndb.FileExt), artifact.Bytes(), 0o600))

	twisted := &automdm.Software{ID: 1, Name: "Twisted", Version: "22.2.0", Source: "python_packages"}
	goInstall := &automdm.Software{ID: 2, Name: "go", Version: "1.24.0", Source: "homebrew_packages"}

	var (
		mu     sync.Mutex
		events []vulnWrite
	)
	record := func(event vulnWrite) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	ds := new(mock.Store)
	ds.AllSoftwareIteratorFunc = func(ctx context.Context, query automdm.SoftwareIterQueryOptions) (automdm.SoftwareIterator, error) {
		switch {
		case slices.Contains(query.IncludedSources, "homebrew_packages"):
			return &softwareIterator{softwares: []*automdm.Software{goInstall}}, nil
		case len(query.IncludedSources) > 0:
			return &softwareIterator{}, nil
		default:
			return &softwareIterator{softwares: []*automdm.Software{twisted, goInstall}}, nil
		}
	}
	ds.InsertSoftwareVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.SoftwareVulnerability, src automdm.VulnerabilitySource) ([]automdm.SoftwareVulnerability, error) {
		record(vulnWrite{"insert", src})
		return nil, nil
	}
	ds.DeleteOutOfDateVulnerabilitiesFunc = func(ctx context.Context, src automdm.VulnerabilitySource, olderThan time.Time) error {
		record(vulnWrite{"delete", src})
		return nil
	}
	ds.ListSoftwareCPEsFunc = func(ctx context.Context) ([]automdm.SoftwareCPE, error) {
		return []automdm.SoftwareCPE{{ID: 1, SoftwareID: 1, CPE: "cpe:2.3:a:twistedmatrix:twisted:22.2.0:*:*:*:*:python:*:*"}}, nil
	}
	ds.InsertCVEMetaFunc = func(ctx context.Context, x []automdm.CVEMeta) error { return nil }
	ds.UpsertSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) { return 0, nil }
	ds.DeleteSoftwareCPEsFunc = func(ctx context.Context, cpes []automdm.SoftwareCPE) (int64, error) { return 0, nil }
	ds.InsertOSVulnerabilitiesFunc = func(ctx context.Context, vulns []automdm.OSVulnerability, src automdm.VulnerabilitySource) (int64, error) {
		return 0, nil
	}
	ds.OSVersionsFunc = func(
		ctx context.Context, teamFilter *automdm.TeamFilter, platform *string, name *string, version *string,
	) (*automdm.OSVersions, error) {
		return &automdm.OSVersions{CountsUpdatedAt: time.Now()}, nil
	}
	ds.ListSoftwareForVulnDetectionFunc = func(ctx context.Context, filter automdm.VulnSoftwareFilter) ([]automdm.Software, error) {
		return nil, nil
	}
	ds.ListOperatingSystemsFunc = func(ctx context.Context) ([]automdm.OperatingSystem, error) { return nil, nil }
	ds.ListOperatingSystemsForPlatformFunc = func(ctx context.Context, platform string) ([]automdm.OperatingSystem, error) {
		return nil, nil
	}
	ds.DeleteOutOfDateOSVulnerabilitiesFunc = func(ctx context.Context, src automdm.VulnerabilitySource, t time.Time) error { return nil }
	ds.DeleteOrphanedSoftwareVulnerabilitiesFunc = func(ctx context.Context) error { return nil }
	ds.DeleteOrphanedOSVulnerabilitiesFunc = func(ctx context.Context) error { return nil }

	vulnsConfig := config.VulnerabilitiesConfig{
		DatabasesPath:         vulnPath,
		Periodicity:           10 * time.Second,
		CurrentInstanceChecks: "auto",
		DisableDataSync:       true,
	}
	ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	appConfig := &automdm.AppConfig{Features: automdm.Features{EnableSoftwareInventory: true}}
	require.NoError(t, scanVulnerabilities(ctx, ds, logger, &vulnsConfig, appConfig, vulnPath))

	insertGo := slices.Index(events, vulnWrite{"insert", automdm.GoVulnDBSource})
	deleteNVD := slices.Index(events, vulnWrite{"delete", automdm.NVDSource})
	require.NotEqual(t, -1, insertGo, events)
	require.NotEqual(t, -1, deleteNVD, events)
	require.Less(t, insertGo, deleteNVD, events)
}

func TestUpdateVulnHostCounts(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	ctx := context.Background()

	ds := new(mock.Store)

	testCases := []struct {
		desc                   string
		maxConcurrency         int
		expectedMaxConcurrency int
	}{
		{
			desc:                   "invalid max concurrency count: 0",
			maxConcurrency:         0,
			expectedMaxConcurrency: 1,
		},
		{
			desc:                   "invalid max concurrency count: < 0",
			maxConcurrency:         -1,
			expectedMaxConcurrency: 1,
		},
		{
			desc:                   "valid max concurrency count",
			maxConcurrency:         10,
			expectedMaxConcurrency: 10,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			var gotMaxConcurrency int
			ds.UpdateVulnerabilityHostCountsFunc = func(ctx context.Context, maxRoutines int) error {
				gotMaxConcurrency = maxRoutines
				return nil
			}

			err := updateVulnHostCounts(ctx, ds, logger, tc.maxConcurrency)
			require.NoError(t, err)

			require.Equal(t, tc.expectedMaxConcurrency, gotMaxConcurrency)
		})
	}
}

func TestScanVulnerabilitiesMkdirFailsIfVulnPathIsFile(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	appConfig := &automdm.AppConfig{
		Features: automdm.Features{EnableSoftwareInventory: true},
	}
	ds := new(safeStore)

	// creating a file with the same path should result in an error when creating the directory
	fileVulnPath := filepath.Join(t.TempDir(), "somefile")
	_, err := os.Create(fileVulnPath)
	require.NoError(t, err)

	config := config.VulnerabilitiesConfig{
		DatabasesPath:         fileVulnPath,
		Periodicity:           10 * time.Second,
		CurrentInstanceChecks: "auto",
	}

	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	err = scanVulnerabilities(ctx, ds, logger, &config, appConfig, fileVulnPath)
	require.ErrorContains(t, err, "create vulnerabilities databases directory: mkdir")
}

func TestCronVulnerabilitiesSkipMkdirIfDisabled(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	ds := new(safeStore)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		// features.enable_software_inventory is false
		return &automdm.AppConfig{}, nil
	}
	ds.SyncHostsSoftwareFunc = func(ctx context.Context, updatedAt time.Time) error {
		return nil
	}

	ds.CleanupSoftwareTitlesFunc = func(ctx context.Context) error {
		return nil
	}

	ds.SyncHostsSoftwareTitlesFunc = func(ctx context.Context, updatedAt time.Time) error {
		return nil
	}

	ds.UpdateHostIssuesVulnerabilitiesFunc = func(ctx context.Context) error {
		return nil
	}

	ds.InsertKernelSoftwareMappingFunc = func(ctx context.Context) error {
		return nil
	}

	mockLocker := scheduletest.SetupMockLocker("vulnerabilities", "test_instance", time.Now().UTC())
	ds.LockFunc = mockLocker.Lock
	ds.UnlockFunc = mockLocker.Unlock

	mockStatsStore := scheduletest.SetUpMockStatsStore("vulnerabilities")
	ds.GetLatestCronStatsFunc = mockStatsStore.GetLatestCronStats
	ds.InsertCronStatsFunc = mockStatsStore.InsertCronStats
	ds.UpdateCronStatsFunc = mockStatsStore.UpdateCronStats
	ds.ClaimCronStatsFunc = mockStatsStore.ClaimCronStats

	vulnPath := filepath.Join(t.TempDir(), "something")
	require.NoDirExists(t, vulnPath)

	config := config.VulnerabilitiesConfig{
		DatabasesPath:         vulnPath,
		Periodicity:           10 * time.Second,
		CurrentInstanceChecks: "1",
	}

	// Use schedule to test that the schedule does indeed call cronVulnerabilities.
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	ctx, cancel := context.WithCancel(ctx)
	s, err := newVulnerabilitiesSchedule(ctx, "test_instance", ds, slog.New(slog.DiscardHandler), &config)
	require.NoError(t, err)
	s.Start()
	t.Cleanup(func() {
		cancel()
		<-s.Done()
	})

	// Every cron tick is 10 seconds ... here we just wait for a loop interation and assert the vuln
	// dir. was not created.
	require.Eventually(t, func() bool {
		_, err := os.Stat(vulnPath)
		return os.IsNotExist(err)
	}, 24*time.Second, 12*time.Second)
}

// TestCronAutomationsLockDuration tests that the Lock method is being called
// for the current automation crons and that their duration is equal to the current
// schedule interval.
func TestAutomationsScheduleLockDuration(t *testing.T) {
	ds := new(safeStore)
	expectedInterval := 1 * time.Second

	intitalConfigLoaded := make(chan struct{}, 1)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		ac := automdm.AppConfig{
			WebhookSettings: automdm.WebhookSettings{
				Interval: automdm.Duration{Duration: 1 * time.Hour},
			},
		}
		select {
		case <-intitalConfigLoaded:
			ac.WebhookSettings.Interval = automdm.Duration{Duration: expectedInterval}
		default:
			// initial config
			close(intitalConfigLoaded)
		}
		return &ac, nil
	}

	ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
		return []*automdm.TeamSummary{}, nil
	}

	ds.OutdatedAutomationBatchFunc = func(ctx context.Context) ([]automdm.PolicyFailure, error) {
		return []automdm.PolicyFailure{}, nil
	}

	hostStatus := make(chan struct{})
	hostStatusClosed := false
	failingPolicies := make(chan struct{})
	failingPoliciesClosed := false
	unknownName := false

	mockLocker := scheduletest.SetupMockLocker("vulnerabilities", "test_instance", time.Now().UTC())
	ds.LockFunc = func(ctx context.Context, name string, owner string, expiration time.Duration) (bool, error) {
		if expiration != expectedInterval {
			return false, nil
		}
		switch name {
		case "automations":
			if !hostStatusClosed {
				close(hostStatus)
				hostStatusClosed = true
			}
			if !failingPoliciesClosed {
				close(failingPolicies)
				failingPoliciesClosed = true
			}
		default:
			unknownName = true
		}
		return mockLocker.Lock(ctx, name, owner, expiration)
	}
	ds.UnlockFunc = mockLocker.Unlock

	mockStatsStore := scheduletest.SetUpMockStatsStore("vulnerabilities")
	ds.GetLatestCronStatsFunc = mockStatsStore.GetLatestCronStats
	ds.InsertCronStatsFunc = mockStatsStore.InsertCronStats
	ds.UpdateCronStatsFunc = mockStatsStore.UpdateCronStats
	ds.ClaimCronStatsFunc = mockStatsStore.ClaimCronStats

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	s, err := newAutomationsSchedule(ctx, "test_instance", ds, slog.New(slog.DiscardHandler), 1*time.Second, service.NewMemFailingPolicySet(), &mock.MockActivityService{})
	require.NoError(t, err)
	s.Start()

	select {
	case <-failingPolicies:
	case <-time.After(5 * time.Second):
		t.Error("failing policies timeout")
	}
	select {
	case <-hostStatus:
	case <-time.After(5 * time.Second):
		t.Error("host status timeout")
	}
	require.False(t, unknownName)
}

func TestAutomationsScheduleIntervalChange(t *testing.T) {
	ds := new(safeStore)

	interval := struct {
		sync.Mutex
		value time.Duration
	}{
		value: 5 * time.Hour,
	}
	configLoaded := make(chan struct{}, 1)

	ds.TeamsSummaryFunc = func(ctx context.Context) ([]*automdm.TeamSummary, error) {
		return []*automdm.TeamSummary{}, nil
	}

	ds.OutdatedAutomationBatchFunc = func(ctx context.Context) ([]automdm.PolicyFailure, error) {
		return []automdm.PolicyFailure{}, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		select {
		case configLoaded <- struct{}{}:
		default:
			// OK
		}

		interval.Lock()
		defer interval.Unlock()

		return &automdm.AppConfig{
			WebhookSettings: automdm.WebhookSettings{
				Interval: automdm.Duration{Duration: interval.value},
			},
		}, nil
	}

	mockLocker := scheduletest.SetupMockLocker("automations", "test_instance", time.Now().UTC())
	require.NoError(t, mockLocker.AddChannels(t, "locked"))
	ds.LockFunc = mockLocker.Lock
	ds.UnlockFunc = mockLocker.Unlock

	mockStatsStore := scheduletest.SetUpMockStatsStore("automations")
	ds.GetLatestCronStatsFunc = mockStatsStore.GetLatestCronStats
	ds.InsertCronStatsFunc = mockStatsStore.InsertCronStats
	ds.UpdateCronStatsFunc = mockStatsStore.UpdateCronStats
	ds.ClaimCronStatsFunc = mockStatsStore.ClaimCronStats

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	s, err := newAutomationsSchedule(ctx, "test_instance", ds, slog.New(slog.DiscardHandler), 200*time.Millisecond, service.NewMemFailingPolicySet(), &mock.MockActivityService{})
	require.NoError(t, err)
	s.Start()

	// wait for config to be called once by startAutomationsSchedule and again by configReloadFunc
	for c := 0; c < 2; c++ {
		select {
		case <-configLoaded:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout: initial config load")
		}
	}

	interval.Lock()
	interval.value = 1 * time.Second
	interval.Unlock()

	select {
	case <-mockLocker.Locked:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: interval change did not trigger lock call")
	}
}

func TestBasicAuthHandler(t *testing.T) {
	for _, tc := range []struct {
		name           string
		username       string
		password       string
		passes         bool
		noBasicAuthSet bool
	}{
		{
			name:     "good-credentials",
			username: "foo",
			password: "bar",
			passes:   true,
		},
		{
			name:     "empty-credentials",
			username: "",
			password: "",
			passes:   false,
		},
		{
			name:           "no-basic-auth-set",
			username:       "",
			password:       "",
			noBasicAuthSet: true,
			passes:         false,
		},
		{
			name:     "wrong-username",
			username: "foo1",
			password: "bar",
			passes:   false,
		},
		{
			name:     "wrong-password",
			username: "foo",
			password: "bar1",
			passes:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pass := false
			h := basicAuthHandler("foo", "bar", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pass = true
				w.WriteHeader(http.StatusOK)
			}))

			r, err := http.NewRequest("GET", "", nil)
			require.NoError(t, err)

			if !tc.noBasicAuthSet {
				r.SetBasicAuth(tc.username, tc.password)
			}

			var w httptest.ResponseRecorder
			h.ServeHTTP(&w, r)

			if pass != tc.passes {
				t.Fatal("unexpected pass")
			}

			expStatusCode := http.StatusUnauthorized
			if pass {
				expStatusCode = http.StatusOK
			}
			require.Equal(t, w.Result().StatusCode, expStatusCode)
		})
	}
}

func TestDebugMux(t *testing.T) {
	h1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) })

	cases := []struct {
		desc string
		mux  debugMux
		tok  string
		want int
	}{
		{
			"only automdm auth handler, no token",
			debugMux{automdmAuthenticatedHandler: h1},
			"",
			200,
		},
		{
			"only automdm auth handler, with token",
			debugMux{automdmAuthenticatedHandler: h1},
			"token",
			200,
		},
		{
			"both handlers, no token",
			debugMux{automdmAuthenticatedHandler: h1, tokenAuthenticatedHandler: h2},
			"",
			200,
		},
		{
			"both handlers, with token",
			debugMux{automdmAuthenticatedHandler: h1, tokenAuthenticatedHandler: h2},
			"token",
			400,
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			path := "/debug/pprof"
			if c.tok != "" {
				path += "?token=" + c.tok
			}
			req := httptest.NewRequest("GET", path, nil)
			res := httptest.NewRecorder()
			c.mux.ServeHTTP(res, req)
			require.Equal(t, c.want, res.Code)
		})
	}
}

func TestVerifyDiskEncryptionKeysJob(t *testing.T) {
	ds := new(mock.Store)
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	testCert, testKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)
	testCertPEM := tokenpki.PEMCertificate(testCert.Raw)
	testKeyPEM := tokenpki.PEMRSAPrivateKey(testKey)

	recoveryKey := "AAA-BBB-CCC"
	encryptedKey, err := pkcs7.Encrypt([]byte(recoveryKey), []*x509.Certificate{testCert})
	require.NoError(t, err)
	base64EncryptedKey := base64.StdEncoding.EncodeToString(encryptedKey)

	assets := map[automdm.MDMAssetName]automdm.MDMConfigAsset{
		automdm.MDMAssetCACert: {Value: testCertPEM},
		automdm.MDMAssetCAKey:  {Value: testKeyPEM},
	}
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return assets, nil
	}
	ds.GetAllMDMConfigAssetsByNameIncludingDeletedFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error) {
		return []automdm.MDMConfigAsset{{Name: automdm.MDMAssetCACert, Value: testCertPEM}}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		appCfg := automdm.AppConfig{}
		appCfg.MDM.EnabledAndConfigured = true
		return &appCfg, nil
	}

	now := time.Now()

	t.Run("able to decrypt", func(t *testing.T) {
		ds.GetUnverifiedDiskEncryptionKeysFunc = func(ctx context.Context) ([]automdm.HostDiskEncryptionKey, error) {
			return []automdm.HostDiskEncryptionKey{
				{HostID: 1, Base64Encrypted: base64EncryptedKey, UpdatedAt: now},
				{HostID: 2, Base64Encrypted: base64EncryptedKey, UpdatedAt: now.Add(time.Hour)},
				{HostID: 3, Base64Encrypted: "BAD-KEY", UpdatedAt: now.Add(-time.Hour)},
			}, nil
		}

		calls := 0
		ds.SetHostsDiskEncryptionKeyStatusFunc = func(ctx context.Context, hostIDs []uint, decryptable bool, threshold time.Time) error {
			calls++
			require.Equal(t, now.Add(time.Hour), threshold)

			// first call, decryptable values
			if decryptable {
				require.EqualValues(t, []uint{1, 2}, hostIDs)
				return nil
			}

			// second call, non-decryptable values
			require.EqualValues(t, []uint{3}, hostIDs)
			return nil
		}

		err = verifyDiskEncryptionKeys(ctx, logger, ds)
		require.NoError(t, err)
		require.True(t, ds.GetUnverifiedDiskEncryptionKeysFuncInvoked)
		require.True(t, ds.SetHostsDiskEncryptionKeyStatusFuncInvoked)
		require.Equal(t, 2, calls)
	})

	t.Run("unable to decrypt", func(t *testing.T) {
		ds.GetUnverifiedDiskEncryptionKeysFunc = func(ctx context.Context) ([]automdm.HostDiskEncryptionKey, error) {
			return []automdm.HostDiskEncryptionKey{{HostID: 1, Base64Encrypted: "RANDOM"}}, nil
		}

		calls := 0
		ds.SetHostsDiskEncryptionKeyStatusFunc = func(ctx context.Context, hostIDs []uint, encryptable bool, threshold time.Time) error {
			calls++
			if !encryptable {
				require.EqualValues(t, []uint{1}, hostIDs)
				return nil
			}
			require.Empty(t, hostIDs)
			return nil
		}

		err = verifyDiskEncryptionKeys(ctx, logger, ds)
		require.NoError(t, err)
		require.True(t, ds.GetUnverifiedDiskEncryptionKeysFuncInvoked)
		require.True(t, ds.SetHostsDiskEncryptionKeyStatusFuncInvoked)
		require.Equal(t, 2, calls)
	})
}

func TestHostVitalsLabelMembershipJob(t *testing.T) {
	ds := new(mock.Store)
	ctx := context.Background()

	// Make some mock labels
	labels := []*automdm.Label{
		{Name: "Dynamic Dave", ID: 1, Query: "query1", LabelType: automdm.LabelTypeRegular, LabelMembershipType: automdm.LabelMembershipTypeDynamic},
		{Name: "Manual Michel", ID: 2, LabelType: automdm.LabelTypeRegular, LabelMembershipType: automdm.LabelMembershipTypeManual},
		{Name: "Vital Vince", ID: 3, HostVitalsCriteria: ptr.RawMessage(json.RawMessage(`{"vital":"owl", "value":"hoot"}`)), LabelType: automdm.LabelTypeRegular, LabelMembershipType: automdm.LabelMembershipTypeHostVitals},
	}

	ds.ListLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions, includeHostCounts bool) ([]*automdm.Label, error) {
		// The cron must pass a filter that includes automdm/team-scoped labels,
		// otherwise host vitals labels on a team never get populated (#46869). An
		// empty TeamFilter (nil User) falls back to global-only labels in
		// applyLabelTeamFilter, so require a global-admin user here.
		require.NotNil(t, filter.User, "cron must pass a user-scoped filter so team labels are included")
		require.True(t, filter.User.HasAnyGlobalRole(), "cron must use a global-admin filter to see all team labels")
		return labels, nil
	}

	numCalls := 0
	ds.UpdateLabelMembershipByHostCriteriaFunc = func(ctx context.Context, hvl automdm.HostVitalsLabel) (*automdm.Label, []uint, error) {
		label := hvl.GetLabel()
		// Only the host vitals label should be processed.
		require.Equal(t, label, labels[2])
		numCalls++
		return nil, nil, nil
	}

	err := cronHostVitalsLabelMembership(ctx, ds)
	require.NoError(t, err)
	// Only one label (the host vitals label) should have been processed.
	require.Equal(t, 1, numCalls)
}

// TestOTELResourceCreation verifies that the OTEL resource can be created without schema URL
// conflicts. The SDK's WithTelemetrySDK() detector embeds its own schema URL, which must match
// the semconv version we import. A mismatch (e.g. after a dependabot SDK bump that doesn't
// update our semconv import) causes a runtime error on server startup.
func TestOTELResourceCreation(t *testing.T) {
	res, err := resource.New(
		t.Context(),
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(
			semconv.ServiceName("automdm-test"),
			semconv.ServiceVersion("0.0.0-test"),
		),
		resource.WithTelemetrySDK(),
	)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestArgsToString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []driver.NamedValue
		want string
	}{
		{
			name: "empty",
			args: nil,
			want: "{}",
		},
		{
			name: "single positional",
			args: []driver.NamedValue{{Value: "hello"}},
			want: "{hello}",
		},
		{
			name: "single named",
			args: []driver.NamedValue{{Name: "id", Value: 42}},
			want: "{id=42}",
		},
		{
			name: "multiple named",
			args: []driver.NamedValue{
				{Name: "id", Value: 1},
				{Name: "name", Value: "alice"},
			},
			want: "{id=1, name=alice}",
		},
		{
			name: "mixed positional and named",
			args: []driver.NamedValue{
				{Name: "id", Value: 1},
				{Value: "second"},
			},
			want: "{id=1, second}",
		},
		{
			name: "mixed value types",
			args: []driver.NamedValue{
				{Value: 1},
				{Value: "two"},
				{Value: true},
				{Value: nil},
			},
			want: "{1, two, true, <nil>}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, argsToString(tc.args))
		})
	}
}

func TestUseS3DevConfig(t *testing.T) {
	t.Run("skip flag set", func(t *testing.T) {
		dev_mode.SetOverride("AUTOMDM_DEV_SKIP_S3_CONFIG", "1", t)
		assert.False(t, useS3DevConfig())
	})

	t.Run("skip flag unset", func(t *testing.T) {
		dev_mode.SetOverride("AUTOMDM_DEV_SKIP_S3_CONFIG", "0", t)
		assert.True(t, useS3DevConfig())
	})
}

func TestGetTLSConfig(t *testing.T) {
	t.Parallel()
	expectedCurves := []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}

	t.Run("modern", func(t *testing.T) {
		cfg := getTLSConfig(config.TLSProfileModern)
		require.NotNil(t, cfg)
		assert.Equal(t, uint16(tls.VersionTLS13), cfg.MinVersion)
		assert.True(t, cfg.PreferServerCipherSuites)
		assert.ElementsMatch(t, expectedCurves, cfg.CurvePreferences)
		assert.ElementsMatch(t, []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		}, cfg.CipherSuites)
	})

	t.Run("intermediate", func(t *testing.T) {
		cfg := getTLSConfig(config.TLSProfileIntermediate)
		require.NotNil(t, cfg)
		assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
		assert.True(t, cfg.PreferServerCipherSuites)
		assert.ElementsMatch(t, expectedCurves, cfg.CurvePreferences)
		assert.ElementsMatch(t, []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		}, cfg.CipherSuites)
	})
}

// TestGetTLSConfigInvalidProfile covers the default case of getTLSConfig,
// which calls initFatal. Not run in parallel because the test mutates the
// package-level initFatal var.
func TestGetTLSConfigInvalidProfile(t *testing.T) {
	var capturedErr error
	var capturedMsg string
	orig := initFatal
	initFatal = func(err error, msg string) {
		capturedErr = err
		capturedMsg = msg
	}
	t.Cleanup(func() { initFatal = orig })

	getTLSConfig("not-a-real-profile")

	require.Error(t, capturedErr)
	require.Contains(t, capturedErr.Error(), "not-a-real-profile")
	require.Contains(t, capturedErr.Error(), "is invalid")
	require.Equal(t, "set TLS profile", capturedMsg)
}

func TestNetworkBlockingModeFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		devModeEnabled bool
		serverConfig   config.ServerConfig
		expectedMode   automdmhttp.NetworkBlockingMode
	}{
		{
			name:         "production default blocks everything",
			serverConfig: config.ServerConfig{},
			expectedMode: automdmhttp.BlockingFull,
		},
		{
			name:         "allow_private_network_integrations allows private networks only",
			serverConfig: config.ServerConfig{AllowPrivateNetworkIntegrations: true},
			expectedMode: automdmhttp.BlockingPrivateAllowed,
		},
		{
			name:         "bypass_network_blocking bypasses all filtering",
			serverConfig: config.ServerConfig{BypassNetworkBlocking: true},
			expectedMode: automdmhttp.BlockingBypassAll,
		},
		{
			name:         "bypass_network_blocking takes precedence over allow_private_network_integrations",
			serverConfig: config.ServerConfig{BypassNetworkBlocking: true, AllowPrivateNetworkIntegrations: true},
			expectedMode: automdmhttp.BlockingBypassAll,
		},
		{
			name:           "dev mode bypasses all filtering regardless of config",
			devModeEnabled: true,
			serverConfig:   config.ServerConfig{},
			expectedMode:   automdmhttp.BlockingBypassAll,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expectedMode, networkBlockingModeFor(c.devModeEnabled, c.serverConfig))
		})
	}
}

func TestInitLicense(t *testing.T) {
	t.Parallel()
	t.Run("dev license", func(t *testing.T) {
		cfg := &config.AutoMDMConfig{}
		license, err := initLicense(cfg, true, false)
		require.NoError(t, err)
		require.NotNil(t, license)
		assert.True(t, license.IsPremium())
		assert.False(t, license.IsExpired())
		assert.NotEmpty(t, cfg.License.Key, "dev license should populate the config key")
	})

	t.Run("dev expired license", func(t *testing.T) {
		cfg := &config.AutoMDMConfig{}
		license, err := initLicense(cfg, false, true)
		require.NoError(t, err)
		require.NotNil(t, license)
		assert.True(t, license.IsExpired())
		assert.NotEmpty(t, cfg.License.Key, "dev expired license should populate the config key")
	})

	t.Run("no license key", func(t *testing.T) {
		cfg := &config.AutoMDMConfig{}
		license, err := initLicense(cfg, false, false)
		require.NoError(t, err)
		require.NotNil(t, license)
		assert.Equal(t, automdm.TierFree, license.Tier)
		assert.Empty(t, cfg.License.Key)
	})
}

func TestPrintMissingMigrationsWarning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		tables         []int64
		wantSubstrings []string
	}{
		{
			name:           "tables",
			tables:         []int64{1, 2, 3},
			wantSubstrings: []string{"tables=[1 2 3]", "missing required migrations"},
		},
		{
			name:           "neither",
			wantSubstrings: []string{"unknown"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printMissingMigrationsWarning(&buf, tc.tables)
			out := buf.String()
			for _, sub := range tc.wantSubstrings {
				assert.Contains(t, out, sub)
			}
			assert.Contains(t, out, os.Args[0])
		})
	}
}

func TestInitOrgLogoStore(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	// With no software installers bucket configured, the store falls back to
	// the database-backed implementation. NewOrgLogoStore does no DB work at
	// construction, so a zero-value Datastore is enough to verify selection.
	ds := &mysql.Datastore{}
	store := initOrgLogoStore(t.Context(), config.StorageBackendLocal, config.S3Config{}, config.AzureConfig{}, ds, logger)
	require.IsType(t, ds.NewOrgLogoStore(), store)
}
