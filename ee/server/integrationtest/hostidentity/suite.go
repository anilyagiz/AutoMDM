//go:build !windows

// Windows is disabled because the TPM simulator requires CGO, which causes lint failures on Windows.

package hostidentity

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

// enrollOrbitResponse is the response structure for orbit enrollment
type enrollOrbitResponse struct {
	OrbitNodeKey string `json:"orbit_node_key,omitempty"`
	Err          error  `json:"error,omitempty"`
}

// orbitConfigRequest is used for orbit config endpoint requests
type orbitConfigRequest struct {
	OrbitNodeKey string `json:"orbit_node_key"`
}

// osqueryConfigRequest is used for osquery config endpoint requests
type osqueryConfigRequest struct {
	NodeKey string `json:"node_key"`
}

type Suite struct {
	integrationtest.BaseSuite
}

func SetUpSuite(t *testing.T, uniqueTestName string, requireSignature bool) *Suite {
	return SetUpSuiteWithConfig(t, uniqueTestName, requireSignature, nil)
}

func SetUpSuiteWithConfig(t *testing.T, uniqueTestName string, requireSignature bool, configModifier func(cfg *config.AutoMDMConfig)) *Suite {
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
	hostIdentitySCEPDepot, err := ds.NewHostIdentitySCEPDepot(slogLogger.With("component", "host-id-scep-depot"), &automdmCfg)
	require.NoError(t, err)
	users, server := svctest.RunServerForTestsWithServiceWithDS(t, ctx, ds, automdmSvc, &service.TestServerOpts{
		License:       license,
		AutoMDMConfig: &automdmCfg,
		Logger:        slogLogger,
		HostIdentity: &service.HostIdentity{
			SCEPStorage:                 hostIdentitySCEPDepot,
			RequireHTTPMessageSignature: requireSignature,
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
