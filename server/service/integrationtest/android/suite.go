package android

import (
	"github.com/anilyagiz/automdm/v4/server/service/redis_key_value"
	"log/slog"
	"os"
	"testing"

	ee_android "github.com/anilyagiz/automdm/v4/ee/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	android_service "github.com/anilyagiz/automdm/v4/server/mdm/android/service"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/integrationtest"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
	"github.com/stretchr/testify/require"
)

type Suite struct {
	integrationtest.BaseSuite
	AndroidProxy  *android_mock.Client
	KeyValueStore automdm.KeyValueStore
}

type suiteOptions struct {
	licenseTier   string
	wrapEEService bool
}

type SuiteOption func(*suiteOptions)

// WithEEAndroidService wraps the core Android service with the premium one, as
// cmd/automdm/serve.go does, and serves requests with the given license tier.
func WithEEAndroidService(licenseTier string) SuiteOption {
	return func(o *suiteOptions) {
		o.licenseTier = licenseTier
		o.wrapEEService = true
	}
}

func SetUpSuite(t *testing.T, uniqueTestName string, opts ...SuiteOption) *Suite {
	options := suiteOptions{licenseTier: automdm.TierFree}
	for _, opt := range opts {
		opt(&options)
	}

	license := &automdm.LicenseInfo{Tier: options.licenseTier}
	ds, redisPool, automdmCfg, automdmSvc, ctx := integrationtest.SetUpMySQLAndRedisAndService(t, uniqueTestName, &service.TestServerOpts{License: license})
	keyValueStore := redis_key_value.New(redisPool)
	slogLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	proxy := android_mock.Client{}
	proxy.InitCommonMocks()
	coreAndroidSvc, err := android_service.NewServiceWithClient(
		slogLogger,
		ds,
		&proxy,
		"test-private-key",
		ds,
		automdmSvc.NewActivity,
		config.AndroidAgentConfig{
			Package:       "com.fleetdm.agent",
			SigningSHA256: "abc123def456",
		},
		android_service.WithKeyValueStore(keyValueStore),
	)
	require.NoError(t, err)
	coreAndroidSvc.(*android_service.Service).AllowLocalhostServerURL = true
	androidSvc := coreAndroidSvc
	if options.wrapEEService {
		eeAndroidSvc, err := ee_android.NewService(coreAndroidSvc, ds, ds, &proxy, slogLogger)
		require.NoError(t, err)
		androidSvc = eeAndroidSvc
	}
	dbConns := mysqltest.TestDBConnections(t, ds)
	users, server := svctest.RunServerForTestsWithServiceWithDS(t, ctx, ds, automdmSvc, &service.TestServerOpts{
		License:       license,
		AutoMDMConfig: &automdmCfg,
		Pool:          redisPool,
		Logger:        slogLogger,
		AndroidModule: androidSvc,
		DBConns:       dbConns,
	})

	s := &Suite{
		BaseSuite: integrationtest.BaseSuite{
			Logger:     slogLogger,
			DS:         ds,
			AutoMDMCfg: automdmCfg,
			Users:      users,
			Server:     server,
		},
		AndroidProxy:  &proxy,
		KeyValueStore: keyValueStore,
	}

	integrationtest.SetUpServerURL(t, ds, server)

	s.Token = s.GetTestAdminToken(t)
	return s
}
