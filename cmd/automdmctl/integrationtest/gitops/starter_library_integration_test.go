package gitops

import (
	"context"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/automdmctltest"
	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/integrationtest"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/svctest"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestIntegrationsStarterLibrary(t *testing.T) {
	testingSuite := new(starterLibraryIntegrationTestSuite)
	testingSuite.WithServer.Suite = &testingSuite.Suite
	suite.Run(t, testingSuite)
}

type starterLibraryIntegrationTestSuite struct {
	suite.Suite
	integrationtest.WithServer
}

func (s *starterLibraryIntegrationTestSuite) SetupSuite() {
	s.WithDS.SetupSuite("starterLibraryIntegrationTestSuite")

	appConf, err := s.DS.AppConfig(context.Background())
	s.Require().NoError(err)
	err = s.DS.SaveAppConfig(context.Background(), appConf)
	s.Require().NoError(err)

	automdmCfg := config.TestConfig()
	automdmCfg.Osquery.EnrollCooldown = 0

	redisPool := redistest.SetupRedis(s.T(), "starter_library", false, false, false)

	serverConfig := service.TestServerOpts{
		AutoMDMConfig: &automdmCfg,
		Pool:          redisPool,
	}

	users, server := svctest.RunServerForTestsWithDS(s.T(), s.DS, &serverConfig)
	s.T().Setenv("AUTOMDM_SERVER_ADDRESS", server.URL)
	s.Server = server
	s.Users = users

	appConf, err = s.DS.AppConfig(context.Background())
	s.Require().NoError(err)
	appConf.ServerSettings.ServerURL = server.URL
	appConf.OrgInfo.OrgName = "Test Org"
	appConf.GitOpsConfig.Exceptions = automdm.GitOpsExceptions{}
	err = s.DS.SaveAppConfig(context.Background(), appConf)
	s.Require().NoError(err)
}

// TestApplyStarterLibraryFree verifies that ApplyStarterLibrary applies only
// the global config (no teams) when using a free license.
func (s *starterLibraryIntegrationTestSuite) TestApplyStarterLibraryFree() {
	t := s.T()
	ctx := context.Background()

	token := s.GetTestToken("admin1@example.com", test.GoodPassword)
	logger := slog.New(slog.DiscardHandler)

	err := service.ApplyStarterLibrary(
		ctx,
		s.Server.URL,
		token,
		logger,
		func(args []string) error {
			_, err := automdmctltest.RunAppNoChecks(args)
			return err
		},
	)
	require.NoError(t, err)

	// Verify the org name was applied.
	appConfig, err := s.DS.AppConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Test Org", appConfig.OrgInfo.OrgName)

	// Verify that no teams were created for a free license.
	teams, err := s.DS.ListTeams(ctx, automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String("admin")}}, automdm.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, teams)

	// Verify labels were created (global labels, team_id=0).
	labelSpecs, err := s.DS.GetLabelSpecs(ctx, automdm.TeamFilter{User: &automdm.User{GlobalRole: ptr.String("admin")}})
	require.NoError(t, err)
	var customLabelNames []string
	for _, l := range labelSpecs {
		if l.LabelType != automdm.LabelTypeBuiltIn {
			customLabelNames = append(customLabelNames, l.Name)
		}
	}
	assert.Contains(t, customLabelNames, "Apple Silicon macOS hosts")
	assert.Contains(t, customLabelNames, "ARM-based Windows hosts")
	assert.Contains(t, customLabelNames, "Debian-based Linux hosts")
	assert.Contains(t, customLabelNames, "x86-based Windows hosts")
}
