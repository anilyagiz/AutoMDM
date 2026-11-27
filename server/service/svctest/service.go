package svctest

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/WatchBeam/clock"
	eeservice "github.com/anilyagiz/automdm/v4/ee/server/service"
	"github.com/anilyagiz/automdm/v4/ee/server/service/digicert"
	"github.com/anilyagiz/automdm/v4/ee/server/service/ejbca"
	"github.com/anilyagiz/automdm/v4/ee/server/service/est"
	"github.com/anilyagiz/automdm/v4/ee/server/service/googlecas"
	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/datastore/filesystem"
	"github.com/anilyagiz/automdm/v4/server/errorstore"
	"github.com/anilyagiz/automdm/v4/server/logging"
	"github.com/anilyagiz/automdm/v4/server/mail"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	microsoft_mdm "github.com/anilyagiz/automdm/v4/server/mdm/microsoft"
	nanodep_storage "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/storage"
	nanomdm_push "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/push"
	"github.com/anilyagiz/automdm/v4/server/microsoft/msgraph"
	automdm_mock "github.com/anilyagiz/automdm/v4/server/mock"
	nanodep_mock "github.com/anilyagiz/automdm/v4/server/mock/nanodep"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/service/async"
	"github.com/anilyagiz/automdm/v4/server/service/redis_install_attempts"
	"github.com/anilyagiz/automdm/v4/server/service/redis_key_value"
	"github.com/anilyagiz/automdm/v4/server/service/redis_lock"
	"github.com/anilyagiz/automdm/v4/server/sso"
	"github.com/stretchr/testify/require"
)

// NewTestService builds an automdm.Service backed by ds for use in tests. It
// honors the relevant fields on opts (License, Logger, Pool, etc.).
func NewTestService(t *testing.T, ds automdm.Datastore, cfg config.AutoMDMConfig, opts ...*service.TestServerOpts) (automdm.Service, context.Context) {
	var rs automdm.QueryResultStore
	if len(opts) > 0 && opts[0].Rs != nil {
		rs = opts[0].Rs
	}
	var lq automdm.LiveQueryStore
	if len(opts) > 0 && opts[0].Lq != nil {
		lq = opts[0].Lq
	}
	return newTestServiceWithConfig(t, ds, cfg, rs, lq, opts...)
}

func newTestServiceWithConfig(t *testing.T, ds automdm.Datastore, automdmConfig config.AutoMDMConfig, rs automdm.QueryResultStore, lq automdm.LiveQueryStore, opts ...*service.TestServerOpts) (automdm.Service, context.Context) {
	lic := &automdm.LicenseInfo{Tier: automdm.TierFree}
	logger := slog.New(slog.DiscardHandler)
	writer, err := logging.NewFilesystemLogWriter(t.Context(), automdmConfig.Filesystem.StatusLogFile, logger, automdmConfig.Filesystem.EnableLogRotation,
		automdmConfig.Filesystem.EnableLogCompression, 500, 28, 3)
	require.NoError(t, err)

	osqlogger := &service.OsqueryLogger{Status: writer, Result: writer}

	var (
		failingPolicySet                automdm.FailingPolicySet      = service.NewMemFailingPolicySet()
		enrollHostLimiter               automdm.EnrollHostLimiter     = nopEnrollHostLimiter{}
		depStorage                      nanodep_storage.AllDEPStorage = &nanodep_mock.Storage{}
		mailer                          automdm.MailService           = &mockMailService{SendEmailFn: func(e automdm.Email) error { return nil }}
		c                               clock.Clock                   = clock.C
		scepConfigService                                             = scep.NewSCEPConfigService(logger, nil)
		digiCertService                                               = digicert.NewService(digicert.WithLogger(logger))
		ejbcaService                                                  = ejbca.NewService(ejbca.WithLogger(logger))
		estCAService                                                  = est.NewService(est.WithLogger(logger))
		googleCASService                automdm.GoogleCASService      = googlecas.NewService(googlecas.WithLogger(logger))
		conditionalAccessMicrosoftProxy service.ConditionalAccessMicrosoftProxy

		mdmStorage             automdm.MDMAppleStore
		mdmPusher              nanomdm_push.Pusher
		ssoStore               sso.SessionStore
		softwareInstallStore   automdm.SoftwareInstallerStore
		bootstrapPackageStore  automdm.MDMBootstrapPackageStore
		softwareTitleIconStore automdm.SoftwareTitleIconStore
		stagedUploadStore      automdm.StagedUploadStore
		distributedLock        automdm.Lock
		keyValueStore          automdm.KeyValueStore
		androidService         android.Service
		installAttemptCounter  automdm.SoftwareInstallAttemptCounter = service.NewMemSoftwareInstallAttemptCounter()
	)
	if len(opts) > 0 {
		if opts[0].Clock != nil {
			c = opts[0].Clock
		}
	}

	if len(opts) > 0 && opts[0].KeyValueStore != nil {
		keyValueStore = opts[0].KeyValueStore
	}

	task := async.NewTask(ds, false)
	if len(opts) > 0 {
		if opts[0].Task != nil {
			task = opts[0].Task
		} else {
			opts[0].Task = task
		}
	}

	if len(opts) > 0 {
		if opts[0].Logger != nil {
			logger = opts[0].Logger
		}
		if opts[0].License != nil {
			lic = opts[0].License
		}
		if opts[0].Pool != nil {
			ssoStore = sso.NewSessionStore(opts[0].Pool)
			distributedLock = redis_lock.NewLock(opts[0].Pool)
			keyValueStore = redis_key_value.New(opts[0].Pool)
			installAttemptCounter = redis_install_attempts.New(opts[0].Pool, redis_install_attempts.SoftwareInstallerPrefix)
		}
		if opts[0].FailingPolicySet != nil {
			failingPolicySet = opts[0].FailingPolicySet
		}
		if opts[0].EnrollHostLimiter != nil {
			enrollHostLimiter = opts[0].EnrollHostLimiter
		}
		if opts[0].UseMailService {
			mailer, err = mail.NewService(config.TestConfig())
			require.NoError(t, err)
		}
		if opts[0].SoftwareInstallStore != nil {
			softwareInstallStore = opts[0].SoftwareInstallStore
		}
		if opts[0].BootstrapPackageStore != nil {
			bootstrapPackageStore = opts[0].BootstrapPackageStore
		}
		if opts[0].SoftwareTitleIconStore != nil {
			softwareTitleIconStore = opts[0].SoftwareTitleIconStore
		}
		stagedUploadStore = opts[0].StagedUploadStore

		// allow to explicitly set MDM storage to nil
		mdmStorage = opts[0].MDMStorage
		if opts[0].DEPStorage != nil {
			depStorage = opts[0].DEPStorage
		}
		// allow to explicitly set mdm pusher to nil
		mdmPusher = opts[0].MDMPusher
	}

	ctx := license.NewContext(context.Background(), lic)

	cronSchedulesService := automdm.NewCronSchedules()
	service.RegisterImmediatePolicyMembershipCleanup(cronSchedulesService, ds)

	var eh *errorstore.Handler
	if len(opts) > 0 {
		if opts[0].Pool != nil {
			eh = errorstore.NewHandler(ctx, opts[0].Pool, logger, time.Minute*10)
			ctx = ctxerr.NewContext(ctx, eh)
		}
		if opts[0].StartCronSchedules != nil {
			for _, fn := range opts[0].StartCronSchedules {
				err = cronSchedulesService.StartCronSchedule(fn(ctx, ds))
				require.NoError(t, err)
			}
		}
	}
	if len(opts) > 0 && opts[0].SCEPConfigService != nil {
		scepConfigService = opts[0].SCEPConfigService
	}
	if len(opts) > 0 && opts[0].DigiCertService != nil {
		digiCertService = opts[0].DigiCertService
	}
	if len(opts) > 0 && opts[0].GoogleCASService != nil {
		googleCASService = opts[0].GoogleCASService
	}
	if len(opts) > 0 && opts[0].ConditionalAccessMicrosoftProxy != nil {
		conditionalAccessMicrosoftProxy = opts[0].ConditionalAccessMicrosoftProxy
		// The Conditional Access feature is gated on AutoMDM Premium; callers that
		// exercise it must provide a premium license via opts[0].License.
		require.True(t, lic.IsPremium(), "ConditionalAccessMicrosoftProxy requires a premium license via opts.License")
	}

	var googleConditionalAccess *service.GoogleConditionalAccessClient
	if len(opts) > 0 {
		googleConditionalAccess = opts[0].ConditionalAccessGoogleClient
	}
	if len(opts) > 0 && opts[0].AndroidModule != nil {
		androidService = opts[0].AndroidModule
	}

	var wstepManager microsoft_mdm.CertManager
	if automdmConfig.MDM.WindowsWSTEPIdentityCert != "" && automdmConfig.MDM.WindowsWSTEPIdentityKey != "" {
		rawCert, err := os.ReadFile(automdmConfig.MDM.WindowsWSTEPIdentityCert)
		require.NoError(t, err)
		rawKey, err := os.ReadFile(automdmConfig.MDM.WindowsWSTEPIdentityKey)
		require.NoError(t, err)

		wstepManager, err = microsoft_mdm.NewCertManager(ds, rawCert, rawKey)
		require.NoError(t, err)
	}

	orgLogoStore, err := filesystem.NewOrgLogoStore(t.TempDir())
	require.NoError(t, err)

	// Test servers get a no-op factory unless a test injects its own. Without this any test applying a credential would hit the real network.
	msGraphClientFactory := msgraph.ClientFactory(noopMicrosoftGraphClientFactory)
	if len(opts) > 0 && opts[0].MicrosoftGraphClientFactory != nil {
		msGraphClientFactory = opts[0].MicrosoftGraphClientFactory
	}

	svc, err := service.NewService(
		ctx,
		ds,
		task,
		rs,
		logger,
		osqlogger,
		automdmConfig,
		mailer,
		c,
		ssoStore,
		lq,
		ds,
		failingPolicySet,
		&automdm.NoOpGeoIP{},
		enrollHostLimiter,
		depStorage,
		mdmStorage,
		mdmPusher,
		cronSchedulesService,
		wstepManager,
		scepConfigService,
		digiCertService,
		conditionalAccessMicrosoftProxy,
		googleConditionalAccess,
		keyValueStore,
		installAttemptCounter,
		androidService,
		orgLogoStore,
	)
	if err != nil {
		panic(err)
	}
	if lic.IsPremium() {
		if softwareInstallStore == nil {
			// default to file-based
			dir := t.TempDir()
			store, err := filesystem.NewSoftwareInstallerStore(dir)
			if err != nil {
				panic(err)
			}
			softwareInstallStore = store
		}

		var androidModule android.Service
		if len(opts) > 0 {
			androidModule = opts[0].AndroidModule
		}

		svc, err = eeservice.NewService(
			svc,
			ds,
			logger,
			automdmConfig,
			mailer,
			c,
			depStorage,
			apple_mdm.NewMDMAppleCommander(mdmStorage, mdmPusher),
			ssoStore,
			softwareInstallStore,
			bootstrapPackageStore,
			softwareTitleIconStore,
			stagedUploadStore,
			distributedLock,
			keyValueStore,
			installAttemptCounter,
			scepConfigService,
			digiCertService,
			ejbcaService,
			androidModule,
			estCAService,
			googleCASService,
			nil, // PSSO nonce store; integration tests don't exercise PSSO
			msGraphClientFactory,
		)
		if err != nil {
			panic(err)
		}
	}

	// Set up mock activity service for unit tests. When DBConns is provided,
	// RunServerForTestsWithServiceWithDS will overwrite this with the real bounded context.
	activityMock := &automdm_mock.MockActivityService{
		NewActivityFunc: func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			return nil
		},
	}
	svc.SetActivityService(activityMock)
	if len(opts) > 0 {
		opts[0].ActivityMock = activityMock
	}

	// Set up mock ACME service for unit tests. When DBConns is provided,
	// RunServerForTestsWithServiceWithDS will overwrite this with the real service module.
	svc.SetACMEService(&automdm_mock.MockACMEService{})

	// Set up mock notifications service for unit tests. When DBConns is provided,
	// RunServerForTestsWithServiceWithDS will overwrite this with the real bounded context.
	svc.SetNotificationsService(&automdm_mock.MockNotificationsService{})

	return svc, ctx
}

// noopMicrosoftGraphClient accepts any credential without touching the network. Verification runs on every write that
// carries a new or changed credential, so a test server built without an injected factory would reach the real
// login.microsoftonline.com and graph.microsoft.com.
type noopMicrosoftGraphClient struct{}

func (noopMicrosoftGraphClient) VerifyCredential(context.Context) error { return nil }

func (noopMicrosoftGraphClient) ListWindowsAutopilotDevices(context.Context) ([]msgraph.WindowsAutopilotDevice, error) {
	return nil, nil
}

func noopMicrosoftGraphClientFactory(*automdm.MicrosoftGraphCredential) (msgraph.Client, error) {
	return noopMicrosoftGraphClient{}, nil
}
