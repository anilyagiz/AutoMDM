package condaccess

import (
	"log/slog"
	"os"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/integrationtest"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
	"github.com/stretchr/testify/require"
)

type Suite struct {
	integrationtest.BaseSuite
}

func SetUpSuite(t *testing.T, uniqueTestName string) *Suite {
	return SetUpSuiteWithConfig(t, uniqueTestName, nil)
}

func SetUpSuiteWithConfig(t *testing.T, uniqueTestName string, configModifier func(cfg *config.AutoMDMConfig)) *Suite {
	// Note: t.Parallel() is called when MySQL datastore options are processed
	license := &automdm.LicenseInfo{
		Tier: automdm.TierPremium,
	}
	ds, automdmCfg, automdmSvc, ctx := integrationtest.SetUpMySQLAndService(t, uniqueTestName, &service.TestServerOpts{
		License: license,
		Pool:    redistest.SetupRedis(t, t.Name(), false, false, false),
	})

	// Apply config modifications
	if configModifier != nil {
		configModifier(&automdmCfg)
	}

	slogLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	condAccessSCEPDepot, err := ds.NewConditionalAccessSCEPDepot(slogLogger.With("component", "conditional-access-scep-depot"), &automdmCfg)
	require.NoError(t, err)

	users, server := svctest.RunServerForTestsWithServiceWithDS(t, ctx, ds, automdmSvc, &service.TestServerOpts{
		License:       license,
		AutoMDMConfig: &automdmCfg,
		Logger:        slogLogger,
		ConditionalAccess: &service.ConditionalAccess{
			SCEPStorage: condAccessSCEPDepot,
		},
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

	s.BaseSuite.Token = s.BaseSuite.GetTestAdminToken(t)
	return s
}
