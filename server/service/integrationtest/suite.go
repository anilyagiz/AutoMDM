package integrationtest

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/platform/mysql/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/require"
)

type BaseSuite struct {
	Logger     *slog.Logger
	AutoMDMCfg config.AutoMDMConfig
	Server     *httptest.Server
	DS         *mysql.Datastore
	Users      map[string]automdm.User
	Token      string

	cachedAdminToken string
}

func (s *BaseSuite) GetTestAdminToken(t *testing.T) string {
	// because the login endpoint is rate-limited, use the cached admin token
	// if available (if for some reason a test needs to logout the admin user,
	// then set cachedAdminToken = "" so that a new token is retrieved).
	if s.cachedAdminToken == "" {
		s.cachedAdminToken = s.GetTestToken(t, service.TestAdminUserEmail, test.GoodPassword)
	}
	return s.cachedAdminToken
}

func (s *BaseSuite) GetTestToken(t *testing.T, email string, password string) string {
	return svctest.GetToken(t, email, password, s.Server.URL)
}

func SetUpServerURL(t *testing.T, ds *mysql.Datastore, server *httptest.Server) {
	appConf, err := ds.AppConfig(t.Context())
	require.NoError(t, err)
	appConf.ServerSettings.ServerURL = server.URL
	err = ds.SaveAppConfig(t.Context(), appConf)
	require.NoError(t, err)
}

func SetUpMySQLAndService(t *testing.T, uniqueTestName string, opts ...*service.TestServerOpts) (
	*mysql.Datastore,
	config.AutoMDMConfig,
	automdm.Service, context.Context,
) {
	ds := mysqltest.CreateMySQLDSWithOptions(t, &testing_utils.DatastoreTestOptions{
		UniqueTestName: uniqueTestName,
	})
	test.AddAllHostsLabel(t, ds)

	// Set up the required fields on AppConfig
	appConf, err := ds.AppConfig(testContext())
	require.NoError(t, err)
	appConf.OrgInfo.OrgName = "AutoMDMTest"
	appConf.ServerSettings.ServerURL = "https://example.org"
	err = ds.SaveAppConfig(testContext(), appConf)
	require.NoError(t, err)

	automdmCfg := config.TestConfig()
	automdmSvc, ctx := svctest.NewTestService(t, ds, automdmCfg, opts...)
	return ds, automdmCfg, automdmSvc, ctx
}

func SetUpMySQLAndRedisAndService(t *testing.T, uniqueTestName string, opts ...*service.TestServerOpts) (*mysql.Datastore, automdm.RedisPool,
	config.AutoMDMConfig,
	automdm.Service, context.Context,
) {
	redisPool := redistest.SetupRedis(t, uniqueTestName, false, false, false)
	ds, automdmCfg, automdmSvc, ctx := SetUpMySQLAndService(t, uniqueTestName, opts...)
	return ds, redisPool, automdmCfg, automdmSvc, ctx
}

func testContext() context.Context {
	return context.Background()
}
