package scim

import (
	"log/slog"
	"os"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/integrationtest"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
)

type Suite struct {
	integrationtest.BaseSuite
}

func SetUpSuite(t *testing.T, uniqueTestName string) *Suite {
	// Note: t.Parallel() is called when MySQL datastore options are processed
	license := &automdm.LicenseInfo{
		Tier: automdm.TierPremium,
	}
	ds, automdmCfg, automdmSvc, ctx := integrationtest.SetUpMySQLAndService(t, uniqueTestName, &service.TestServerOpts{
		License: license,
	})
	slogLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	users, server := svctest.RunServerForTestsWithServiceWithDS(t, ctx, ds, automdmSvc, &service.TestServerOpts{
		License:       license,
		AutoMDMConfig: &automdmCfg,
		Logger:        slogLogger,
		EnableSCIM:    true,
		// Wire the real activity service so tests can assert audit-log activities.
		DBConns: mysqltest.TestDBConnections(t, ds),
	})

	s := &Suite{
		BaseSuite: integrationtest.BaseSuite{
			Logger:     slogLogger,
			DS:         ds,
			AutoMDMCfg: automdmCfg,
			Users:      users,
			Server:     server,
		},
	}

	integrationtest.SetUpServerURL(t, ds, server)

	s.Token = s.GetTestAdminToken(t)
	return s
}
