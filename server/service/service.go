// Package service holds the implementation of the team interface and HTTP
// endpoints for the API
package service

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WatchBeam/clock"
	gocache "github.com/patrickmn/go-cache"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	microsoft_mdm "github.com/anilyagiz/automdm/v4/server/mdm/microsoft"
	nanodep_storage "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/storage"
	nanomdm_push "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/push"
	nanomdm_storage "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/storage"
	"github.com/anilyagiz/automdm/v4/server/service/async"
	"github.com/anilyagiz/automdm/v4/server/service/conditional_access_microsoft_proxy"
	"github.com/anilyagiz/automdm/v4/server/sso"
)

var _ automdm.Service = (*Service)(nil)

// Service is the struct implementing automdm.Service. Create a new one with NewService.
type Service struct {
	ds             automdm.Datastore
	task           *async.Task
	carveStore     automdm.CarveStore
	resultStore    automdm.QueryResultStore
	liveQueryStore automdm.LiveQueryStore
	logger         *slog.Logger
	config         config.AutoMDMConfig
	clock          clock.Clock

	osqueryLogWriter *OsqueryLogger

	mailService     automdm.MailService
	ssoSessionStore sso.SessionStore

	failingPolicySet  automdm.FailingPolicySet
	enrollHostLimiter automdm.EnrollHostLimiter

	authz *authz.Authorizer

	jitterMu *sync.RWMutex
	jitterH  map[time.Duration]*jitterHashTable

	geoIP automdm.GeoIP

	*automdm.EnterpriseOverrides

	depStorage        nanodep_storage.AllDEPStorage
	mdmStorage        nanomdm_storage.AllStorage
	mdmPushService    nanomdm_push.Pusher
	mdmAppleCommander *apple_mdm.MDMAppleCommander

	cronSchedulesService automdm.CronSchedulesService

	wstepCertManager  microsoft_mdm.CertManager
	scepConfigService automdm.SCEPConfigService
	digiCertService   automdm.DigiCertService

	conditionalAccessMicrosoftProxy ConditionalAccessMicrosoftProxy

	googleConditionalAccess *GoogleConditionalAccessClient

	keyValueStore         automdm.KeyValueStore
	installAttemptCounter automdm.SoftwareInstallAttemptCounter

	// configETagStore powers the osquery config ETag SHORT CIRCUIT (see
	// GetClientConfigWithETag in osquery.go). It is nil unless the
	// osquery.redis_config_etags feature flag is enabled AND Redis is
	// configured — nil is what turns the short circuit off, there is no other
	// gate at request time.
	configETagStore automdm.ConfigETagStore
	// configETagStateOnce bounds the "optimization state first observed" log
	// to once per AutoMDM container (see GetClientConfigWithETag). A pointer,
	// because some Service methods use value receivers and sync.Once must
	// not be copied.
	configETagStateOnce *sync.Once
	// configETagErrLast rate-limits config-ETag error logging (unix seconds
	// of the last emitted error; see logConfigETagError). A pointer for the
	// same no-copy reason.
	configETagErrLast *atomic.Int64

	androidSvc android.Service

	// activitySvc is the activity bounded context service for write operations.
	activitySvc automdm.ActivityWriteService

	// acmeSvc is the ACME service module for write operations.
	acmeSvc automdm.ACMEWriteService

	// orgLogoStore stores the bytes of customer-uploaded org logos.
	orgLogoStore automdm.OrgLogoStore

	// notificationsSvc is the notifications bounded context service for write operations.
	notificationsSvc automdm.NotificationsWriteService

	// agentNotifier publishes check-in wake-ups for agents connected over the
	// WebSocket transport; nil when the transport is disabled.
	agentNotifier automdm.AgentCheckInNotifier

	// packConfigCache caches marshaled pack config JSON per (teamID, queryReportsDisabled).
	// Avoids redundant DB queries and JSON marshaling for identical pack configs.
	// Nil when osquery.config_in_memory_cache is disabled.
	packConfigCache *gocache.Cache

	// queryReportReadSem bounds concurrent query report reads on this server (see
	// saveResultLogsToQueryReports). Nil when osquery.max_concurrent_query_report_reads <= 0.
	queryReportReadSem chan struct{}
	// queryReportWriteLimit bounds concurrent query report writes across all AutoMDM servers, with
	// slots held in Redis (see acquireQueryReportWriteSlot). No limit when <= 0.
	queryReportWriteLimit int
	// queryReportWriteFallbackSem bounds this server's writes when Redis can't be reached.
	queryReportWriteFallbackSem chan struct{}
}

// ConditionalAccessMicrosoftProxy is the interface of the Microsoft compliance proxy.
type ConditionalAccessMicrosoftProxy interface {
	// Create creates the integration on the MS proxy and returns the consent URL.
	Create(ctx context.Context, tenantID string) (*conditional_access_microsoft_proxy.CreateResponse, error)
	// Get returns the integration settings.
	Get(ctx context.Context, tenantID string, secret string) (*conditional_access_microsoft_proxy.GetResponse, error)
	// Delete deprovisions the tenant on Microsoft and deletes the integration in the proxy service.
	// Returns an automdm.IsNotFound error if the integration doesn't exist.
	Delete(ctx context.Context, tenantID string, secret string) (*conditional_access_microsoft_proxy.DeleteResponse, error)
	// SetComplianceStatus sets the inventory and compliance status of a host.
	// Returns the message ID to query the status of the operation (MS has an asynchronous API).
	SetComplianceStatus(
		ctx context.Context,
		tenantID string, secret string,
		deviceID string,
		userPrincipalName string,
		mdmEnrolled bool,
		deviceName, osName, osVersion string,
		compliant bool,
		lastCheckInTime time.Time,
	) (*conditional_access_microsoft_proxy.SetComplianceStatusResponse, error)
	// GetMessageStatusResponse returns the status of a "compliance set" operation.
	GetMessageStatus(ctx context.Context, tenantID string, secret string, messageID string) (*conditional_access_microsoft_proxy.GetMessageStatusResponse, error)
}

func (svc *Service) LookupGeoIP(ctx context.Context, ip string) *automdm.GeoLocation {
	return svc.geoIP.Lookup(ctx, ip)
}

func (svc *Service) SetEnterpriseOverrides(overrides automdm.EnterpriseOverrides) {
	svc.EnterpriseOverrides = &overrides
}

// OsqueryLogger holds osqueryd's status and result loggers.
type OsqueryLogger struct {
	// Status holds the osqueryd's status logger.
	//
	// See https://osquery.readthedocs.io/en/stable/deployment/logging/#status-logs
	Status automdm.JSONLogger
	// Result holds the osqueryd's result logger.
	//
	// See https://osquery.readthedocs.io/en/stable/deployment/logging/#results-logs
	Result automdm.JSONLogger
}

// PackConfigCacheTTL is how long a marshaled pack config stays in packConfigCache
const PackConfigCacheTTL = 1 * time.Minute

// NewService creates a new service from the config struct
func NewService(
	ctx context.Context,
	ds automdm.Datastore,
	task *async.Task,
	resultStore automdm.QueryResultStore,
	logger *slog.Logger,
	osqueryLogger *OsqueryLogger,
	config config.AutoMDMConfig,
	mailService automdm.MailService,
	c clock.Clock,
	sso sso.SessionStore,
	lq automdm.LiveQueryStore,
	carveStore automdm.CarveStore,
	failingPolicySet automdm.FailingPolicySet,
	geoIP automdm.GeoIP,
	enrollHostLimiter automdm.EnrollHostLimiter,
	depStorage nanodep_storage.AllDEPStorage,
	mdmStorage automdm.MDMAppleStore,
	mdmPushService nanomdm_push.Pusher,
	cronSchedulesService automdm.CronSchedulesService,
	wstepCertManager microsoft_mdm.CertManager,
	scepConfigService automdm.SCEPConfigService,
	digiCertService automdm.DigiCertService,
	conditionalAccessProxy ConditionalAccessMicrosoftProxy,
	googleConditionalAccess *GoogleConditionalAccessClient,
	keyValueStore automdm.KeyValueStore,
	installAttemptCounter automdm.SoftwareInstallAttemptCounter,
	androidSvc android.Service,
	orgLogoStore automdm.OrgLogoStore,
) (automdm.Service, error) {
	authorizer, err := authz.NewAuthorizer()
	if err != nil {
		return nil, fmt.Errorf("new authorizer: %w", err)
	}

	var packConfigCache *gocache.Cache
	if config.Osquery.ConfigInMemoryCache {
		packConfigCache = gocache.New(PackConfigCacheTTL, 30*time.Second)
	}

	var queryReportReadSem chan struct{}
	if n := config.Osquery.MaxConcurrentQueryReportReads; n > 0 {
		queryReportReadSem = make(chan struct{}, n)
	}

	svc := &Service{
		ds:                ds,
		task:              task,
		carveStore:        carveStore,
		resultStore:       resultStore,
		liveQueryStore:    lq,
		logger:            logger,
		config:            config,
		clock:             c,
		osqueryLogWriter:  osqueryLogger,
		mailService:       mailService,
		ssoSessionStore:   sso,
		failingPolicySet:  failingPolicySet,
		authz:             authorizer,
		jitterH:           make(map[time.Duration]*jitterHashTable),
		jitterMu:          new(sync.RWMutex),
		geoIP:             geoIP,
		enrollHostLimiter: enrollHostLimiter,
		depStorage:        depStorage,
		// TODO: remove mdmStorage and mdmPushService when
		// we remove deprecated top-level service methods
		// from the prototype.
		mdmStorage:           mdmStorage,
		mdmPushService:       mdmPushService,
		mdmAppleCommander:    apple_mdm.NewMDMAppleCommander(mdmStorage, mdmPushService),
		cronSchedulesService: cronSchedulesService,
		wstepCertManager:     wstepCertManager,
		scepConfigService:    scepConfigService,
		digiCertService:      digiCertService,

		conditionalAccessMicrosoftProxy: conditionalAccessProxy,
		googleConditionalAccess:         googleConditionalAccess,
		keyValueStore:                   keyValueStore,
		configETagStateOnce:             new(sync.Once),
		configETagErrLast:               new(atomic.Int64),
		installAttemptCounter:           installAttemptCounter,
		androidSvc:                      androidSvc,
		orgLogoStore:                    orgLogoStore,
		packConfigCache:                 packConfigCache,
		queryReportReadSem:              queryReportReadSem,
		queryReportWriteLimit:           config.Osquery.MaxConcurrentQueryReportWrites,
		queryReportWriteFallbackSem:     make(chan struct{}, queryReportWriteFallbackLimit),
	}
	return validationMiddleware{svc, ds, sso}, nil
}

func (svc *Service) SendEmail(ctx context.Context, mail automdm.Email) error {
	return svc.mailService.SendEmail(ctx, mail)
}

// SetConfigETagStore injects the Redis-backed osquery config ETag store,
// enabling the config SHORT CIRCUIT (see GetClientConfigWithETag in
// osquery.go). Called after NewService, and ONLY when the
// osquery.redis_config_etags feature flag is enabled — leaving the store nil
// is what keeps the short circuit off.
func (svc *Service) SetConfigETagStore(store automdm.ConfigETagStore) {
	svc.configETagStore = store
}

// SetActivityService sets the activity bounded context service for write operations.
// This should be called after NewService to inject the activity service dependency.
func (svc *Service) SetActivityService(activitySvc automdm.ActivityWriteService) {
	svc.activitySvc = activitySvc
}

// SetNotificationsService sets the notifications bounded context service for write operations.
// This should be called after NewService to inject the notifications service dependency.
func (svc *Service) SetNotificationsService(notificationsSvc automdm.NotificationsWriteService) {
	svc.notificationsSvc = notificationsSvc
}

// SetACMEService sets the ACME service module service for write operations.
// This should be called after NewService to inject the ACME service dependency.
func (svc *Service) SetACMEService(acmeSvc automdm.ACMEWriteService) {
	svc.acmeSvc = acmeSvc
}

// SetAgentCheckInNotifier sets the notifier used to wake up agents connected
// over the WebSocket transport; when unset, no notifications are published.
func (svc *Service) SetAgentCheckInNotifier(notifier automdm.AgentCheckInNotifier) {
	svc.agentNotifier = notifier
}

type validationMiddleware struct {
	automdm.Service
	ds              automdm.Datastore
	ssoSessionStore sso.SessionStore
}

// getAssetURL simply returns the base url used for retrieving image assets from automdm.com.
func getAssetURL() template.URL {
	return template.URL("https://automdm.com/images/permanent")
}

// emailLinkBaseURL returns the base URL used to build links in transactional
// emails. The server URL is the source of truth; the URL prefix is appended
// only when the server URL does not already carry it. This keeps links correct
// whether an operator configures the subpath in the server URL, in the URL
// prefix, or both, instead of duplicating it (e.g. https://host/p/p/login).
func emailLinkBaseURL(serverURL, urlPrefix string) template.URL {
	if urlPrefix != "" && !strings.HasSuffix(strings.TrimSuffix(serverURL, "/"), urlPrefix) {
		if joined, err := url.JoinPath(serverURL, urlPrefix); err == nil {
			serverURL = joined
		}
	}
	return template.URL(serverURL) //nolint:gosec // G203: operator-configured URL, not user input
}
