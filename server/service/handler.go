package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	"github.com/anilyagiz/automdm/v4/server/agentws"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	carvestorectx "github.com/anilyagiz/automdm/v4/server/contexts/carvestore"
	"github.com/anilyagiz/automdm/v4/server/contexts/publicip"
	"github.com/anilyagiz/automdm/v4/server/contexts/redact"
	"github.com/anilyagiz/automdm/v4/server/contexts/token"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	mdmcrypto "github.com/anilyagiz/automdm/v4/server/mdm/crypto"
	microsoft_mdm "github.com/anilyagiz/automdm/v4/server/mdm/microsoft"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/cryptoutil"
	httpmdm "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/http/mdm"
	nanomdm_service "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/service"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/service/certauth"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/service/multi"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/service/nanomdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/scep/challenge"
	scep_depot "github.com/anilyagiz/automdm/v4/server/mdm/scep/depot"
	scepserver "github.com/anilyagiz/automdm/v4/server/mdm/scep/server"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
	"github.com/anilyagiz/automdm/v4/server/platform/middleware/ratelimit"
	"github.com/anilyagiz/automdm/v4/server/platform/tracing"
	"github.com/anilyagiz/automdm/v4/server/service/contract"
	"github.com/anilyagiz/automdm/v4/server/service/middleware/auth"
	"github.com/anilyagiz/automdm/v4/server/service/middleware/log"
	"github.com/anilyagiz/automdm/v4/server/service/middleware/mdmconfigured"
	"github.com/anilyagiz/automdm/v4/server/service/middleware/otel"

	"github.com/docker/go-units"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/klauspost/compress/gzhttp"
	nanomdm_log "github.com/micromdm/nanolib/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/realclientip/realclientip-go"
	"github.com/throttled/throttled/v2"
)

func checkLicenseExpiration(svc automdm.Service) func(context.Context, http.ResponseWriter) context.Context {
	return func(ctx context.Context, w http.ResponseWriter) context.Context {
		license, err := svc.License(ctx)
		if err != nil || license == nil {
			return ctx
		}
		if license.IsPremium() && license.IsExpired() {
			w.Header().Set(automdm.HeaderLicenseKey, automdm.HeaderLicenseValueExpired)
		}
		return ctx
	}
}

type extraHandlerOpts struct {
	loginRateLimit  *throttled.Rate
	mdmSsoRateLimit *throttled.Rate
	ssoRateLimit    *throttled.Rate
	httpSigVerifier func(http.Handler) http.Handler
	agentWSHub      *agentws.Hub
	mcpConn         *MCPConn
	mcpHandler      http.Handler
}

// ExtraHandlerOption allows adding extra configuration to the HTTP handler.
type ExtraHandlerOption func(*extraHandlerOpts)

// WithLoginRateLimit configures the rate limit for the login endpoints.
func WithLoginRateLimit(r throttled.Rate) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.loginRateLimit = &r
	}
}

// WithMdmSsoRateLimit configures the rate limit for the MDM SSO endpoints (falls back to login rate limit otherwise).
func WithMdmSsoRateLimit(r throttled.Rate) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.mdmSsoRateLimit = &r
	}
}

// WithSsoRateLimit configures the rate of the SSO callback's dedicated rate
// limit bucket (the rate defaults to the login rate limit otherwise; the bucket
// is always separate from the login bucket).
func WithSsoRateLimit(r throttled.Rate) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.ssoRateLimit = &r
	}
}

func WithHTTPSigVerifier(m func(http.Handler) http.Handler) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.httpSigVerifier = m
	}
}

// WithAgentWSHub provides the hub of agent WebSocket connections; when set
// (and the websocket transport is enabled in the config), the agent
// notifications WebSocket endpoint is registered.
func WithAgentWSHub(hub *agentws.Hub) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.agentWSHub = hub
	}
}

// WithMCPConn provides the in-memory connection the embedded MCP uses to reach
// this server's API; the caller starts it once the root handler exists.
func WithMCPConn(conn *MCPConn) ExtraHandlerOption {
	return func(o *extraHandlerOpts) {
		o.mcpConn = conn
	}
}

func setCarveStoreInRequestContext(carveStore automdm.CarveStore) kithttp.RequestFunc {
	return func(ctx context.Context, r *http.Request) context.Context {
		ctx = carvestorectx.NewContext(ctx, carveStore)
		return ctx
	}
}

// apiVersions are the versions the AutoMDM API routes are registered for, in addition to "latest".
var apiVersions = []string{"v1", "2022-04"}

// FeatureRoutes lists the routes of the bounded contexts that MakeHandler serves next to the core API. cmd/automdm and the route
// tests both build the list with it, so a context added to one can't be left out of the other.
func FeatureRoutes(android, activity, notifications, acme, chart endpointer.HandlerRoutesFunc) []endpointer.HandlerRoutesFunc {
	return []endpointer.HandlerRoutesFunc{android, activity, notifications, acme, chart}
}

// MakeHandler creates an HTTP handler for the AutoMDM server endpoints.
func MakeHandler(
	svc automdm.Service,
	config config.AutoMDMConfig,
	logger *slog.Logger,
	limitStore throttled.GCRAStore,
	redisPool automdm.RedisPool,
	carveStore automdm.CarveStore,
	featureRoutes []endpointer.HandlerRoutesFunc,
	extra ...ExtraHandlerOption,
) (http.Handler, error) {
	var eopts extraHandlerOpts
	for _, fn := range extra {
		fn(&eopts)
	}

	// Create the client IP extraction strategy based on config.
	ipStrategy, err := endpointer.NewClientIPStrategy(config.Server.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("invalid server.trusted_proxies configuration: %w", err)
	}

	automdmAPIOptions := []kithttp.ServerOption{
		kithttp.ServerBefore(
			kithttp.PopulateRequestContext, // populate the request context with common fields
			func(ctx context.Context, _ *http.Request) context.Context { return redact.NewContext(ctx) },
			auth.SetRequestsContexts(svc),
			setCarveStoreInRequestContext(carveStore),
		),
		kithttp.ServerErrorHandler(&endpointer.ErrorHandler{Logger: logger}),
		kithttp.ServerErrorEncoder(automdmErrorEncoder),
		kithttp.ServerAfter(
			kithttp.SetContentType("application/json; charset=utf-8"),
			log.LogRequestEnd(logger),
			checkLicenseExpiration(svc),
		),
	}

	r := endpointer.NewRouter()
	useRouteMiddleware(r, config, ipStrategy, eopts.httpSigVerifier)

	if eopts.mcpConn == nil {
		eopts.mcpConn = NewMCPConn()
	}
	eopts.mcpHandler, err = newMCPHandler(svc, logger.With("component", "mcp"), eopts.mcpConn, config.Server.URLPrefix)
	if err != nil {
		return nil, fmt.Errorf("initializing the embedded MCP: %w", err)
	}

	attachAutoMDMAPIRoutes(r, svc, config, logger, limitStore, redisPool, automdmAPIOptions, eopts)
	for _, featureRoute := range featureRoutes {
		featureRoute(r, automdmAPIOptions)
	}
	endpointer.RegisterRemovedPaths(r, apiVersions, logger, removedPaths())
	addMetrics(r)

	return r, nil
}

// useRouteMiddleware adds the route-agnostic middleware, outermost first. It is route middleware rather than a wrapper around
// the router, so a request no route matches (a 404 or 405) never reaches it.
func useRouteMiddleware(r *endpointer.Router, cfg config.AutoMDMConfig, ipStrategy realclientip.Strategy,
	httpSigVerifier func(http.Handler) http.Handler,
) {
	if cfg.OTELEnabled() {
		r.Use(func(rt endpointer.Route, next http.Handler) http.Handler {
			// Spans read "GET /api/_version_/automdm/hosts/{id}" instead of the full template.
			route := tracing.CleanRouteTemplate(rt.Template())
			return otel.WrapHandler(otel.WithRouteTag(route, next), route, cfg)
		})
	}
	if cfg.Server.GzipResponses {
		r.Use(func(_ endpointer.Route, next http.Handler) http.Handler { return gzhttp.GzipHandler(next) })
	}
	r.Use(func(_ endpointer.Route, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := ipStrategy.ClientIP(req.Header, req.RemoteAddr)
			if ip != "" {
				req.RemoteAddr = ip
			}
			next.ServeHTTP(w, req.WithContext(publicip.NewContext(req.Context(), ip)))
		})
	})
	if httpSigVerifier != nil {
		r.Use(func(_ endpointer.Route, next http.Handler) http.Handler { return httpSigVerifier(next) })
	}
}

// PrometheusMetricsHandler wraps the provided handler with prometheus metrics
// middleware and returns the resulting handler that should be mounted for that
// route.
func PrometheusMetricsHandler(name string, handler http.Handler) http.Handler {
	reg := prometheus.DefaultRegisterer
	registerOrExisting := func(coll prometheus.Collector) prometheus.Collector {
		if err := reg.Register(coll); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				return are.ExistingCollector
			}
			panic(err)
		}
		return coll
	}

	// this configuration is to keep prometheus metrics as close as possible to
	// what the v0.9.3 (that we used to use) provided via the now-deprecated
	// prometheus.InstrumentHandler.

	reqCnt := registerOrExisting(prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem:   "http",
			Name:        "requests_total",
			Help:        "Total number of HTTP requests made.",
			ConstLabels: prometheus.Labels{"handler": name},
		},
		[]string{"method", "code"},
	)).(*prometheus.CounterVec)

	reqDur := registerOrExisting(prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem:   "http",
			Name:        "request_duration_seconds",
			Help:        "The HTTP request latencies in seconds.",
			ConstLabels: prometheus.Labels{"handler": name},
			// Use default buckets, as they are suited for durations.
		},
		nil,
	)).(*prometheus.HistogramVec)

	// 1KB, 100KB, 1MB, 100MB, 1GB
	sizeBuckets := []float64{1024, 100 * 1024, 1024 * 1024, 100 * 1024 * 1024, 1024 * 1024 * 1024}

	resSz := registerOrExisting(prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem:   "http",
			Name:        "response_size_bytes",
			Help:        "The HTTP response sizes in bytes.",
			ConstLabels: prometheus.Labels{"handler": name},
			Buckets:     sizeBuckets,
		},
		nil,
	)).(*prometheus.HistogramVec)

	reqSz := registerOrExisting(prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem:   "http",
			Name:        "request_size_bytes",
			Help:        "The HTTP request sizes in bytes.",
			ConstLabels: prometheus.Labels{"handler": name},
			Buckets:     sizeBuckets,
		},
		nil,
	)).(*prometheus.HistogramVec)

	return promhttp.InstrumentHandlerDuration(reqDur,
		promhttp.InstrumentHandlerCounter(reqCnt,
			promhttp.InstrumentHandlerResponseSize(resSz,
				promhttp.InstrumentHandlerRequestSize(reqSz, handler))))
}

// addMetrics decorates each handler with prometheus instrumentation
func addMetrics(r *endpointer.Router) {
	r.WrapHandlers(func(rt endpointer.Route, h http.Handler) http.Handler {
		return PrometheusMetricsHandler(rt.Name, h)
	})
}

// These are defined as const so that they can be used in tests.
const (
	forgotPasswordRateLimitMaxBurst = 9 // Max burst used for rate limiting on the the forgot_password endpoint.

	// AutoMDM Desktop API endpoints rate limiting:
	//
	// Allow up to 1_000 consecutive failing requests per minute.
	// If the threshold of 1_000 consecutive failures is reached for an IP,
	// ban requests from such IP for a duration of 1 minute.
	//

	// Exported so bounded contexts with their own device-token endpoints (e.g.
	// notifications) can apply the same IP ban policy.
	DeviceIPAllowedConsecutiveFailingRequestsCount      = 1_000
	DeviceIPAllowedConsecutiveFailingRequestsTimeWindow = 1 * time.Minute
	DeviceIPBanTime                                     = 1 * time.Minute
)

func attachAutoMDMAPIRoutes(r *endpointer.Router, svc automdm.Service, config config.AutoMDMConfig,
	logger *slog.Logger, limitStore throttled.GCRAStore, redisPool automdm.RedisPool, opts []kithttp.ServerOption,
	extra extraHandlerOpts,
) {
	// user-authenticated endpoints
	ue := newUserAuthenticatedEndpointer(svc, opts, r, apiVersions...)

	ue.POST("/api/_version_/automdm/trigger", triggerEndpoint, triggerRequest{})

	ue.GET("/api/_version_/automdm/me", meEndpoint, getMeRequest{})
	ue.GET("/api/_version_/automdm/sessions/{id:[0-9]+}", getInfoAboutSessionEndpoint, getInfoAboutSessionRequest{})
	ue.DELETE("/api/_version_/automdm/sessions/{id:[0-9]+}", deleteSessionEndpoint, deleteSessionRequest{})

	ue.GET("/api/_version_/automdm/config/certificate", getCertificateEndpoint, nil)
	ue.GET("/api/_version_/automdm/config", getAppConfigEndpoint, nil)
	ue.PATCH("/api/_version_/automdm/config", modifyAppConfigEndpoint, modifyAppConfigRequest{})
	ue.WithRequestBodySizeLimit(automdm.OrgLogoMaxFileSize).PUT("/api/_version_/automdm/logo", putOrgLogoEndpoint, putOrgLogoRequest{})
	ue.DELETE("/api/_version_/automdm/logo", deleteOrgLogoEndpoint, deleteOrgLogoRequest{})
	ue.POST("/api/_version_/automdm/spec/enroll_secret", applyEnrollSecretSpecEndpoint, applyEnrollSecretSpecRequest{})
	ue.GET("/api/_version_/automdm/spec/enroll_secret", getEnrollSecretSpecEndpoint, nil)
	ue.WithHTTPPreAuth(automdmctl4PreAuth(logger)).GET("/api/_version_/automdm/version", versionEndpoint, nil)

	ue.POST("/api/_version_/automdm/translate", translatorEndpoint, translatorRequest{})
	ue.WithRequestBodySizeLimit(5*units.MiB).POST("/api/_version_/automdm/spec/teams", applyTeamSpecsEndpoint, applyTeamSpecsRequest{})
	ue.PATCH("/api/_version_/automdm/teams/{team_id:[0-9]+}/secrets", modifyTeamEnrollSecretsEndpoint, modifyTeamEnrollSecretsRequest{})
	ue.POST("/api/_version_/automdm/teams", createTeamEndpoint, createTeamRequest{})
	ue.GET("/api/_version_/automdm/teams", listTeamsEndpoint, listTeamsRequest{})
	ue.GET("/api/_version_/automdm/teams/{id:[0-9]+}", getTeamEndpoint, getTeamRequest{})
	ue.PATCH("/api/_version_/automdm/teams/{id:[0-9]+}", modifyTeamEndpoint, modifyTeamRequest{})
	ue.DELETE("/api/_version_/automdm/teams/{id:[0-9]+}", deleteTeamEndpoint, deleteTeamRequest{})
	ue.WithRequestBodySizeLimit(2*units.MiB).POST("/api/_version_/automdm/teams/{id:[0-9]+}/agent_options", modifyTeamAgentOptionsEndpoint, modifyTeamAgentOptionsRequest{})
	ue.GET("/api/_version_/automdm/teams/{id:[0-9]+}/users", listTeamUsersEndpoint, listTeamUsersRequest{})
	ue.PATCH("/api/_version_/automdm/teams/{id:[0-9]+}/users", addTeamUsersEndpoint, modifyTeamUsersRequest{})
	ue.DELETE("/api/_version_/automdm/teams/{id:[0-9]+}/users", deleteTeamUsersEndpoint, modifyTeamUsersRequest{})
	ue.GET("/api/_version_/automdm/teams/{id:[0-9]+}/secrets", teamEnrollSecretsEndpoint, teamEnrollSecretsRequest{})

	ue.GET("/api/_version_/automdm/users", listUsersEndpoint, listUsersRequest{})
	ue.POST("/api/_version_/automdm/users/admin", createUserEndpoint, createUserRequest{})
	ue.POST("/api/_version_/automdm/users/api_only", createAPIOnlyUserEndpoint, createAPIOnlyUserRequest{})
	ue.PATCH("/api/_version_/automdm/users/api_only/{id:[0-9]+}", modifyAPIOnlyUserEndpoint, modifyAPIOnlyUserRequest{})
	ue.GET("/api/_version_/automdm/users/{id:[0-9]+}", getUserEndpoint, getUserRequest{})
	ue.PATCH("/api/_version_/automdm/users/{id:[0-9]+}", modifyUserEndpoint, modifyUserRequest{})
	ue.DELETE("/api/_version_/automdm/users/{id:[0-9]+}", deleteUserEndpoint, deleteUserRequest{})
	ue.POST("/api/_version_/automdm/users/{id:[0-9]+}/require_password_reset", requirePasswordResetEndpoint, requirePasswordResetRequest{})
	ue.GET("/api/_version_/automdm/users/{id:[0-9]+}/sessions", getInfoAboutSessionsForUserEndpoint, getInfoAboutSessionsForUserRequest{})
	ue.DELETE("/api/_version_/automdm/users/{id:[0-9]+}/sessions", deleteSessionsForUserEndpoint, deleteSessionsForUserRequest{})
	ue.POST("/api/_version_/automdm/change_password", changePasswordEndpoint, changePasswordRequest{})

	ue.GET("/api/_version_/automdm/email/change/{token}", changeEmailEndpoint, changeEmailRequest{})
	// TODO: searchTargetsEndpoint will be removed in AutoMDM 5.0
	ue.POST("/api/_version_/automdm/targets", searchTargetsEndpoint, automdm.SearchTargetsRequest{})
	ue.POST("/api/_version_/automdm/targets/count", countTargetsEndpoint, automdm.CountTargetsRequest{})

	ue.POST("/api/_version_/automdm/invites", createInviteEndpoint, createInviteRequest{})
	ue.GET("/api/_version_/automdm/invites", listInvitesEndpoint, listInvitesRequest{})
	ue.DELETE("/api/_version_/automdm/invites/{id:[0-9]+}", deleteInviteEndpoint, deleteInviteRequest{})
	ue.PATCH("/api/_version_/automdm/invites/{id:[0-9]+}", updateInviteEndpoint, updateInviteRequest{})

	// Policies in any scope; team_id picks "All teams" (none), "Unassigned" (0), or a team.
	ue.POST("/api/_version_/automdm/policies", createPolicyEndpoint, automdm.CreatePolicyRequest{})
	ue.GET("/api/_version_/automdm/policies", listPoliciesEndpoint, automdm.ListPoliciesRequest{})
	ue.GET("/api/_version_/automdm/policies/count", countPoliciesEndpoint, automdm.CountPoliciesRequest{})
	ue.GET("/api/_version_/automdm/policies/{policy_id}", getPolicyByIDEndpoint, automdm.GetPolicyByIDRequest{})
	ue.GET("/api/_version_/automdm/policies/{policy_id}/automation_activities", listPolicyAutomationActivitiesEndpoint, automdm.ListPolicyAutomationActivitiesRequest{})
	ue.POST("/api/_version_/automdm/policies/delete", deletePoliciesEndpoint, automdm.DeletePoliciesRequest{})
	ue.PATCH("/api/_version_/automdm/policies/{policy_id}", modifyPolicyEndpoint, automdm.ModifyPolicyRequest{})
	ue.POST("/api/_version_/automdm/policies/{policy_id}/reset", resetPolicyEndpoint, automdm.ResetPolicyRequest{})
	ue.POST("/api/_version_/automdm/automations/reset", resetAutomationEndpoint, automdm.ResetAutomationRequest{})

	ue.WithRequestBodySizeLimit(automdm.MaxSpecSize).POST("/api/_version_/automdm/spec/policies", applyPolicySpecsEndpoint, automdm.ApplyPolicySpecsRequest{})

	ue.POST("/api/_version_/automdm/certificates", createCertificateTemplateEndpoint, createCertificateTemplateRequest{})
	ue.GET("/api/_version_/automdm/certificates", listCertificateTemplatesEndpoint, listCertificateTemplatesRequest{})
	ue.GET("/api/_version_/automdm/certificates/{id:[0-9]+}", getCertificateTemplateEndpoint, getCertificateTemplateRequest{})
	ue.DELETE("/api/_version_/automdm/certificates/{id:[0-9]+}", deleteCertificateTemplateEndpoint, deleteCertificateTemplateRequest{})
	ue.POST("/api/_version_/automdm/spec/certificates", applyCertificateTemplateSpecsEndpoint, applyCertificateTemplateSpecsRequest{})
	ue.DELETE("/api/_version_/automdm/spec/certificates", deleteCertificateTemplateSpecsEndpoint, deleteCertificateTemplateSpecsRequest{})

	ue.GET("/api/_version_/automdm/reports/{id:[0-9]+}", getQueryEndpoint, automdm.GetQueryRequest{})
	ue.GET("/api/_version_/automdm/reports", listQueriesEndpoint, automdm.ListQueriesRequest{})
	ue.GET("/api/_version_/automdm/reports/{id:[0-9]+}/report", getQueryReportEndpoint, automdm.GetQueryReportRequest{})
	ue.POST("/api/_version_/automdm/reports", createQueryEndpoint, automdm.CreateQueryRequest{})
	ue.PATCH("/api/_version_/automdm/reports/{id:[0-9]+}", modifyQueryEndpoint, automdm.ModifyQueryRequest{})
	ue.DELETE("/api/_version_/automdm/reports/{id:[0-9]+}", deleteQueryByIDEndpoint, automdm.DeleteQueryByIDRequest{})
	ue.POST("/api/_version_/automdm/reports/delete", deleteQueriesEndpoint, automdm.DeleteQueriesRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxSpecSize).POST("/api/_version_/automdm/spec/reports", applyQuerySpecsEndpoint, automdm.ApplyQuerySpecsRequest{})
	ue.GET("/api/_version_/automdm/spec/reports", getQuerySpecsEndpoint, automdm.GetQuerySpecsRequest{})
	ue.GET("/api/_version_/automdm/spec/reports/{name}", getQuerySpecEndpoint, automdm.GetQuerySpecRequest{})

	ue.GET("/api/_version_/automdm/software/versions", listSoftwareVersionsEndpoint, listSoftwareRequest{})
	ue.GET("/api/_version_/automdm/software/versions/{id:[0-9]+}", getSoftwareEndpoint, getSoftwareRequest{})

	ue.GET("/api/_version_/automdm/software/titles", listSoftwareTitlesEndpoint, listSoftwareTitlesRequest{})
	ue.GET("/api/_version_/automdm/software/titles/{id:[0-9]+}", getSoftwareTitleEndpoint, getSoftwareTitleRequest{})
	ue.POST("/api/_version_/automdm/hosts/{host_id:[0-9]+}/software/{software_title_id:[0-9]+}/install", installSoftwareTitleEndpoint,
		installSoftwareRequest{})
	ue.POST("/api/_version_/automdm/hosts/{host_id:[0-9]+}/software/{software_title_id:[0-9]+}/uninstall", uninstallSoftwareTitleEndpoint,
		uninstallSoftwareRequest{})

	// Software installers
	ue.GET("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/package", getSoftwareInstallerEndpoint, getSoftwareInstallerRequest{})
	ue.POST("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/package/token", getSoftwareInstallerTokenEndpoint,
		getSoftwareInstallerRequest{})
	// Software package endpoints are already limited to max installer size in serve.go
	ue.SkipRequestBodySizeLimit().WithHTTPPreAuth(uploadPreAuth(svc, logger)).POST("/api/_version_/automdm/software/package", uploadSoftwareInstallerEndpoint, uploadSoftwareInstallerRequest{})
	ue.POST("/api/_version_/automdm/staged_upload", createStagedUploadEndpoint, createStagedUploadRequest{})
	ue.PATCH("/api/_version_/automdm/software/titles/{id:[0-9]+}/name", updateSoftwareNameEndpoint, updateSoftwareNameRequest{})
	// Software package endpoints are already limited to max installer size in serve.go
	ue.SkipRequestBodySizeLimit().WithHTTPPreAuth(uploadPreAuth(svc, logger)).PATCH("/api/_version_/automdm/software/titles/{id:[0-9]+}/package", updateSoftwareInstallerEndpoint, updateSoftwareInstallerRequest{})
	ue.DELETE("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/available_for_install", deleteSoftwareInstallerEndpoint, deleteSoftwareInstallerRequest{})
	ue.GET("/api/_version_/automdm/software/install/{install_uuid}/results", getSoftwareInstallResultsEndpoint,
		getSoftwareInstallResultsRequest{})
	// POST /api/_version_/automdm/software/batch is asynchronous, meaning it will start the process of software download+upload in the background
	// and will return a request UUID to be used in GET /api/_version_/automdm/software/batch/{request_uuid} to query for the status of the operation.
	ue.WithRequestBodySizeLimit(automdm.MaxSoftwareBatchSize).POST("/api/_version_/automdm/software/batch", batchSetSoftwareInstallersEndpoint, batchSetSoftwareInstallersRequest{})
	ue.GET("/api/_version_/automdm/software/batch/{request_uuid}", batchSetSoftwareInstallersResultEndpoint, batchSetSoftwareInstallersResultRequest{})

	// software title custom icons
	ue.GET("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/icon", getSoftwareTitleIconsEndpoint, getSoftwareTitleIconsRequest{})
	ue.PUT("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/icon", putSoftwareTitleIconEndpoint, putSoftwareTitleIconRequest{})
	ue.DELETE("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/icon", deleteSoftwareTitleIconEndpoint, deleteSoftwareTitleIconRequest{})

	// App store software
	ue.GET("/api/_version_/automdm/software/app_store_apps", getAppStoreAppsEndpoint, getAppStoreAppsRequest{})
	ue.POST("/api/_version_/automdm/software/app_store_apps", addAppStoreAppEndpoint, addAppStoreAppRequest{})
	ue.PATCH("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/app_store_app", updateAppStoreAppEndpoint, updateAppStoreAppRequest{})

	// Self-service categories
	ue.GET("/api/_version_/automdm/software/self_service_categories", getSelfServiceCategoriesEndpoint, getSelfServiceCategoriesRequest{})
	ue.POST("/api/_version_/automdm/software/self_service_categories", addSelfServiceCategoriesEndpoint, addSelfServiceCategoriesRequest{})
	ue.PATCH("/api/_version_/automdm/software/self_service_categories/{id:[0-9]+}", patchSelfServiceCategoriesEndpoint, patchSelfServiceCategoriesRequest{})
	ue.DELETE("/api/_version_/automdm/software/self_service_categories/{id:[0-9]+}", deleteSelfServiceCategoriesEndpoint, deleteSelfServiceCategoriesRequest{})

	ue.GET("/api/_version_/automdm/software/patch_deadline_settings", getPatchDeadlineSettingsEndpoint, getPatchDeadlineSettingsRequest{})
	ue.PATCH("/api/_version_/automdm/software/patch_deadline_settings", updatePatchDeadlineSettingsEndpoint, updatePatchDeadlineSettingsRequest{})

	// Setup Experience
	//
	// Setup experience software endpoints:
	ue.PUT("/api/_version_/automdm/setup_experience/software", putSetupExperienceSoftware, putSetupExperienceSoftwareRequest{})
	ue.GET("/api/_version_/automdm/setup_experience/software", getSetupExperienceSoftware, getSetupExperienceSoftwareRequest{})

	// Setup experience script endpoints:
	ue.GET("/api/_version_/automdm/setup_experience/script", getSetupExperienceScriptEndpoint, getSetupExperienceScriptRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxScriptSize).POST("/api/_version_/automdm/setup_experience/script", setSetupExperienceScriptEndpoint, setSetupExperienceScriptRequest{})
	ue.DELETE("/api/_version_/automdm/setup_experience/script", deleteSetupExperienceScriptEndpoint, deleteSetupExperienceScriptRequest{})

	// AutoMDM-maintained apps
	ue.WithRequestBodySizeLimit(automdm.MaxMultiScriptQuerySize).POST("/api/_version_/automdm/software/automdm_maintained_apps", addAutoMDMMaintainedAppEndpoint, addAutoMDMMaintainedAppRequest{})
	ue.GET("/api/_version_/automdm/software/automdm_maintained_apps", listAutoMDMMaintainedAppsEndpoint, listAutoMDMMaintainedAppsRequest{})
	ue.GET("/api/_version_/automdm/software/automdm_maintained_apps/{app_id}", getAutoMDMMaintainedApp, getAutoMDMMaintainedAppRequest{})

	// Vulnerabilities
	ue.GET("/api/_version_/automdm/vulnerabilities", listVulnerabilitiesEndpoint, listVulnerabilitiesRequest{})
	ue.GET("/api/_version_/automdm/vulnerabilities/{cve}", getVulnerabilityEndpoint, getVulnerabilityRequest{})

	// Hosts
	ue.GET("/api/_version_/automdm/host_summary", getHostSummaryEndpoint, getHostSummaryRequest{})
	ue.GET("/api/_version_/automdm/hosts", listHostsEndpoint, listHostsRequest{})
	ue.POST("/api/_version_/automdm/hosts/delete", deleteHostsEndpoint, deleteHostsRequest{})
	ue.GET("/api/_version_/automdm/hosts/count", countHostsEndpoint, countHostsRequest{})
	ue.POST("/api/_version_/automdm/hosts/search", searchHostsEndpoint, searchHostsRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id}/query", runLiveQueryOnHostEndpoint, runLiveQueryOnHostRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}", deleteHostEndpoint, deleteHostRequest{})
	ue.POST("/api/_version_/automdm/hosts/transfer", addHostsToTeamEndpoint, addHostsToTeamRequest{})
	ue.POST("/api/_version_/automdm/hosts/transfer/filter", addHostsToTeamByFilterEndpoint, addHostsToTeamByFilterRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/refetch", refetchHostEndpoint, refetchHostRequest{})
	// Deprecated: Device mappings are included in the host details endpoint: /api/_version_/automdm/hosts/{id}
	ue.GET("/api/_version_/automdm/hosts/{id:[0-9]+}/device_mapping", listHostDeviceMappingEndpoint, listHostDeviceMappingRequest{})
	ue.PUT("/api/_version_/automdm/hosts/{id:[0-9]+}/device_mapping", putHostDeviceMappingEndpoint, putHostDeviceMappingRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/device_mapping/idp", deleteHostIDPEndpoint, deleteHostIDPRequest{})
	ue.PUT("/api/_version_/automdm/hosts/{id:[0-9]+}/google_device_id", putHostGoogleDeviceIDEndpoint, putHostGoogleDeviceIDRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/google_device_id", deleteHostGoogleDeviceIDEndpoint, deleteHostGoogleDeviceIDRequest{})
	ue.GET("/api/_version_/automdm/hosts/report", hostsReportEndpoint, hostsReportRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}", getHostEndpoint, getHostRequest{})
	ue.GET("/api/_version_/automdm/os_versions", osVersionsEndpoint, osVersionsRequest{})
	ue.GET("/api/_version_/automdm/os_versions/{id:[0-9]+}", getOSVersionEndpoint, getOSVersionRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/reports/{report_id:[0-9]+}", getHostQueryReportEndpoint, getHostQueryReportRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/reports", listHostReportsEndpoint, listHostReportsRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/health", getHostHealthEndpoint, getHostHealthRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/labels", addLabelsToHostEndpoint, addLabelsToHostRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/labels", removeLabelsFromHostEndpoint, automdm.RemoveLabelsFromHostRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/software", getHostSoftwareEndpoint, getHostSoftwareRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/certificates", listHostCertificatesEndpoint, listHostCertificatesRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/certificates/{template_id:[0-9]+}/resend", resendHostCertificateTemplateEndpoint, resendHostCertificateTemplateRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/ai_sessions", listHostAISessionsEndpoint, listHostAISessionsRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/ai_sessions/{ai_session_id:[0-9]+}", getHostAISessionEndpoint, getHostAISessionRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/ai_sessions/{ai_session_id:[0-9]+}/events", listHostAISessionEventsEndpoint, listHostAISessionEventsRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/recovery_lock_password", getHostRecoveryLockPasswordEndpoint, getHostRecoveryLockPasswordRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id:[0-9]+}/device_url", getHostDeviceURLEndpoint, getHostDeviceURLRequest{})

	ue.GET("/api/_version_/automdm/hosts/summary/mdm", getHostMDMSummary, getHostMDMSummaryRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/mdm", getHostMDM, getHostMDMRequest{})

	ue.GET("/api/_version_/automdm/hosts/{id:[0-9]+}/dep_assignment", getHostDEPAssignmentEndpoint, getHostDEPAssignmentRequest{})

	ue.POST("/api/_version_/automdm/labels", createLabelEndpoint, automdm.CreateLabelRequest{})
	ue.PATCH("/api/_version_/automdm/labels/{id:[0-9]+}", modifyLabelEndpoint, automdm.ModifyLabelRequest{})
	ue.GET("/api/_version_/automdm/labels/{id:[0-9]+}", getLabelEndpoint, automdm.GetLabelRequest{})
	ue.GET("/api/_version_/automdm/labels", listLabelsEndpoint, automdm.ListLabelsRequest{})
	ue.GET("/api/_version_/automdm/labels/summary", getLabelsSummaryEndpoint, automdm.GetLabelsSummaryRequest{})
	ue.GET("/api/_version_/automdm/labels/{id:[0-9]+}/hosts", listHostsInLabelEndpoint, automdm.ListHostsInLabelRequest{})
	ue.DELETE("/api/_version_/automdm/labels/id/{id:[0-9]+}", deleteLabelByIDEndpoint, automdm.DeleteLabelByIDRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxSpecSize).POST("/api/_version_/automdm/spec/labels", applyLabelSpecsEndpoint, automdm.ApplyLabelSpecsRequest{})
	ue.GET("/api/_version_/automdm/spec/labels", getLabelSpecsEndpoint, automdm.GetLabelSpecsRequest{})
	ue.GET("/api/_version_/automdm/spec/labels/{name}", getLabelSpecEndpoint, automdm.GetLabelSpecRequest{})

	// This endpoint runs live queries synchronously (with a configured timeout).
	ue.POST("/api/_version_/automdm/reports/{id:[0-9]+}/run", runOneLiveQueryEndpoint, runOneLiveQueryRequest{})
	// Old endpoint, removed from docs. This GET endpoint runs live queries synchronously (with a configured timeout).
	ue.GET("/api/_version_/automdm/reports/run", runLiveQueryEndpoint, runLiveQueryRequest{})
	// The following two POST APIs are the asynchronous way to run live queries.
	// The live queries are created with these two endpoints and their results can be queried via
	// websockets via the `GET /api/_version_/automdm/results/` endpoint.
	ue.POST("/api/_version_/automdm/reports/run", createDistributedQueryCampaignEndpoint, createDistributedQueryCampaignRequest{})
	ue.POST("/api/_version_/automdm/reports/run_by_identifiers", createDistributedQueryCampaignByIdentifierEndpoint, createDistributedQueryCampaignByIdentifierRequest{})

	ue.GET("/api/_version_/automdm/carves", listCarvesEndpoint, automdm.ListCarvesRequest{})
	ue.GET("/api/_version_/automdm/carves/{id:[0-9]+}", getCarveEndpoint, automdm.GetCarveRequest{})
	ue.GET("/api/_version_/automdm/carves/{id:[0-9]+}/block/{block_id}", getCarveBlockEndpoint, automdm.GetCarveBlockRequest{})

	ue.GET("/api/_version_/automdm/hosts/{id}/macadmins", getMacadminsDataEndpoint, getMacadminsDataRequest{})
	ue.GET("/api/_version_/automdm/macadmins", getAggregatedMacadminsDataEndpoint, getAggregatedMacadminsDataRequest{})

	ue.GET("/api/_version_/automdm/status/result_store", statusResultStoreEndpoint, nil)
	ue.GET("/api/_version_/automdm/status/live_query", statusLiveQueryEndpoint, nil)

	ue.WithRequestBodySizeLimit(automdm.MaxScriptSize).POST("/api/_version_/automdm/scripts/run", runScriptEndpoint, automdm.RunScriptRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxScriptSize).POST("/api/_version_/automdm/scripts/run/sync", runScriptSyncEndpoint, automdm.RunScriptSyncRequest{})
	ue.POST("/api/_version_/automdm/scripts/run/batch", batchScriptRunEndpoint, automdm.BatchScriptRunRequest{})
	ue.GET("/api/_version_/automdm/scripts/results/{execution_id}", getScriptResultEndpoint, automdm.GetScriptResultRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxScriptSize).POST("/api/_version_/automdm/scripts", createScriptEndpoint, automdm.CreateScriptRequest{})
	ue.GET("/api/_version_/automdm/scripts", listScriptsEndpoint, automdm.ListScriptsRequest{})
	ue.GET("/api/_version_/automdm/scripts/{script_id:[0-9]+}", getScriptEndpoint, automdm.GetScriptRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxScriptSize).PATCH("/api/_version_/automdm/scripts/{script_id:[0-9]+}", updateScriptEndpoint, automdm.UpdateScriptRequest{})
	ue.DELETE("/api/_version_/automdm/scripts/{script_id:[0-9]+}", deleteScriptEndpoint, automdm.DeleteScriptRequest{})
	ue.WithRequestBodySizeLimit(automdm.MaxBatchScriptSize).POST("/api/_version_/automdm/scripts/batch", batchSetScriptsEndpoint, automdm.BatchSetScriptsRequest{})
	ue.POST("/api/_version_/automdm/scripts/batch/{batch_execution_id:[a-zA-Z0-9-]+}/cancel", batchScriptCancelEndpoint, automdm.BatchScriptCancelRequest{})
	ue.WithAltPaths("/api/_version_/automdm/scripts/batch/{batch_execution_id:[a-zA-Z0-9-]+}/host-results"). // .../host-results is DEPRECATED but we need to maintain for backwards compatibility because customers may already be using it
															GET("/api/_version_/automdm/scripts/batch/{batch_execution_id:[a-zA-Z0-9-]+}/host_results", batchScriptExecutionHostResultsEndpoint, automdm.BatchScriptExecutionHostResultsRequest{})
	ue.GET("/api/_version_/automdm/scripts/batch/{batch_execution_id:[a-zA-Z0-9-]+}", batchScriptExecutionStatusEndpoint, automdm.BatchScriptExecutionStatusRequest{})
	ue.GET("/api/_version_/automdm/scripts/batch", batchScriptExecutionListEndpoint, automdm.BatchScriptExecutionListRequest{})

	ue.GET("/api/_version_/automdm/hosts/{id}/scripts", getHostScriptDetailsEndpoint, automdm.GetHostScriptDetailsRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/activities/upcoming", listHostUpcomingActivitiesEndpoint, listHostUpcomingActivitiesRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/activities/upcoming/{activity_id}", cancelHostUpcomingActivityEndpoint, cancelHostUpcomingActivityRequest{})
	ue.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/commands/{command_uuid}", cancelHostMDMCommandEndpoint, cancelHostMDMCommandRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/lock", lockHostEndpoint, automdm.LockHostRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/unlock", unlockHostEndpoint, automdm.UnlockHostRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/wipe", wipeHostEndpoint, automdm.WipeHostRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/reset", resetHostEndpoint, automdm.ResetHostRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/clear_passcode", clearPasscodeEndpoint, clearPasscodeRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/recovery_lock_password/rotate", rotateRecoveryLockPasswordEndpoint, rotateRecoveryLockPasswordRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/managed_account_password", getHostManagedAccountPasswordEndpoint, getHostManagedAccountPasswordRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/managed_account_password/rotate", rotateManagedLocalAccountPasswordEndpoint, rotateManagedLocalAccountPasswordRequest{})
	ue.POST("/api/_version_/automdm/hosts/release_ab", releaseABDevicesEndpoint, releaseABDevicesRequest{})

	// Generative AI
	ue.POST("/api/_version_/automdm/autofill/policy", autofillPoliciesEndpoint, automdm.AutofillPoliciesRequest{})
	ue.POST("/api/_version_/automdm/autofill/configuration_profile/name_description", autofillConfigProfileNameDescriptionEndpoint, autofillConfigProfileNameDescriptionRequest{})
	ue.POST("/api/_version_/automdm/autofill/configuration_profile/content", autofillConfigProfileContentEndpoint, autofillConfigProfileContentRequest{})

	// Secret variables
	ue.PUT("/api/_version_/automdm/spec/secret_variables", createSecretVariablesEndpoint, automdm.CreateSecretVariablesRequest{})
	ue.POST("/api/_version_/automdm/custom_variables", createSecretVariableEndpoint, automdm.CreateSecretVariableRequest{})
	ue.GET("/api/_version_/automdm/custom_variables", listSecretVariablesEndpoint, automdm.ListSecretVariablesRequest{})
	ue.DELETE("/api/_version_/automdm/custom_variables/{id:[0-9]+}", deleteSecretVariableEndpoint, automdm.DeleteSecretVariableRequest{})

	// Custom host vitals
	ue.GET("/api/_version_/automdm/custom_host_vitals", listCustomHostVitalsEndpoint, automdm.ListCustomHostVitalsRequest{})
	ue.POST("/api/_version_/automdm/custom_host_vitals", createCustomHostVitalEndpoint, automdm.CreateCustomHostVitalRequest{})
	ue.PATCH("/api/_version_/automdm/custom_host_vitals/{id:[0-9]+}", updateCustomHostVitalEndpoint, automdm.UpdateCustomHostVitalRequest{})
	ue.DELETE("/api/_version_/automdm/custom_host_vitals/{id:[0-9]+}", deleteCustomHostVitalEndpoint, automdm.DeleteCustomHostVitalRequest{})
	ue.PUT("/api/_version_/automdm/hosts/{host_id:[0-9]+}/custom_host_vitals/{id:[0-9]+}", setHostCustomHostVitalValueEndpoint, automdm.SetHostCustomHostVitalValueRequest{})
	ue.PUT("/api/_version_/automdm/spec/custom_host_vitals", upsertCustomHostVitalsEndpoint, automdm.UpsertCustomHostVitalsRequest{})

	// API end-points
	ue.GET("/api/_version_/automdm/rest_api", listAPIEndpointsEndpoint, listAPIEndpointsRequest{})

	// Scim details
	ue.GET("/api/_version_/automdm/scim/details", getScimDetailsEndpoint, nil)

	// Microsoft Compliance Partner
	ue.POST("/api/_version_/automdm/conditional-access/microsoft", conditionalAccessMicrosoftCreateEndpoint, conditionalAccessMicrosoftCreateRequest{})
	ue.POST("/api/_version_/automdm/conditional-access/microsoft/confirm", conditionalAccessMicrosoftConfirmEndpoint, conditionalAccessMicrosoftConfirmRequest{})
	ue.DELETE("/api/_version_/automdm/conditional-access/microsoft", conditionalAccessMicrosoftDeleteEndpoint, conditionalAccessMicrosoftDeleteRequest{})
	ue.POST("/api/_version_/automdm/conditional_access/google", connectGoogleConditionalAccessEndpoint, connectGoogleConditionalAccessRequest{})
	ue.GET("/api/_version_/automdm/conditional_access/google", getGoogleConditionalAccessEndpoint, nil)
	ue.PATCH("/api/_version_/automdm/conditional_access/google", updateGoogleConditionalAccessEndpoint, updateGoogleConditionalAccessRequest{})
	ue.DELETE("/api/_version_/automdm/conditional_access/google", deleteGoogleConditionalAccessEndpoint, nil)

	// Okta Conditional Access
	ue.GET("/api/_version_/automdm/conditional_access/idp/signing_cert", conditionalAccessGetIdPSigningCertEndpoint, conditionalAccessGetIdPSigningCertRequest{})
	ue.GET("/api/_version_/automdm/conditional_access/idp/apple/profile", conditionalAccessGetIdPAppleProfileEndpoint, nil)
	ue.GET("/api/_version_/automdm/conditional_access/idp/windows/profiles", conditionalAccessGetIdPWindowsProfilesEndpoint, nil)

	ue.PATCH("/api/_version_/automdm/setup_experience", updateMDMAppleSetupEndpoint, updateMDMAppleSetupRequest{})

	// Only AutoMDM specific endpoints should be within the root /mdm/ path.
	// NOTE: remember to update
	// `service.mdmConfigurationRequiredEndpoints` when you add an
	// endpoint that's behind the mdmConfiguredMiddleware, this applies
	// both to this set of endpoints and to any public/token-authenticated
	// endpoints using `neMDM` below in this file.
	mdmConfiguredMiddleware := mdmconfigured.NewMDMConfigMiddleware(svc)
	mdmAppleMW := ue.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAppleMDM())
	mdmAnyMW := ue.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAnyMDM())

	mdmAppleMW.WithRequestBodySizeLimit(automdm.MaxProfileSize).POST("/api/_version_/automdm/enrollment_profiles/automatic", createMDMAppleSetupAssistantEndpoint, createMDMAppleSetupAssistantRequest{})
	mdmAppleMW.GET("/api/_version_/automdm/enrollment_profiles/automatic", getMDMAppleSetupAssistantEndpoint, getMDMAppleSetupAssistantRequest{})
	mdmAppleMW.GET("/api/_version_/automdm/enrollment_profiles/automatic/default", getDefaultMDMAppleSetupAssistantProfileEndpoint, nil)
	mdmAppleMW.POST("/api/_version_/automdm/enrollment_profiles/automatic/rotate_token", rotateMDMAppleAutomaticEnrollmentTokenEndpoint, rotateMDMAppleAutomaticEnrollmentTokenRequest{})
	mdmAppleMW.DELETE("/api/_version_/automdm/enrollment_profiles/automatic", deleteMDMAppleSetupAssistantEndpoint, deleteMDMAppleSetupAssistantRequest{})

	// Deprecated: GET /enrollment_profiles/manual is deprecated and will be removed in a future major release.
	mdmAppleMW.GET("/api/_version_/automdm/enrollment_profiles/manual", getManualEnrollmentProfileEndpoint, getManualEnrollmentProfileRequest{})

	// bootstrap-package routes

	// Bootstrap endpoints are already max size limited to installer size in serve.go
	mdmAppleMW.SkipRequestBodySizeLimit().WithHTTPPreAuth(uploadPreAuth(svc, logger)).POST("/api/_version_/automdm/bootstrap", uploadBootstrapPackageEndpoint, uploadBootstrapPackageRequest{})
	mdmAppleMW.GET("/api/_version_/automdm/bootstrap/{team_id:[0-9]+}/metadata", bootstrapPackageMetadataEndpoint, bootstrapPackageMetadataRequest{})
	mdmAppleMW.DELETE("/api/_version_/automdm/bootstrap/{team_id:[0-9]+}", deleteBootstrapPackageEndpoint, deleteBootstrapPackageRequest{})
	mdmAppleMW.GET("/api/_version_/automdm/bootstrap/summary", getMDMAppleBootstrapPackageSummaryEndpoint, getMDMAppleBootstrapPackageSummaryRequest{})

	mdmAppleMW.GET("/api/_version_/automdm/apns", getAppleMDMEndpoint, nil)

	// EULA routes, for macOS and Windows. The platform's MDM is checked past the
	// route, once the platform param or the token says which platform it is.
	ue.WithRequestBodySizeLimit(automdm.MaxEULASize).POST("/api/_version_/automdm/setup_experience/eula", createMDMEULAEndpoint, createMDMEULARequest{})
	ue.GET("/api/_version_/automdm/setup_experience/eula/metadata", getMDMEULAMetadataEndpoint, getMDMEULAMetadataRequest{})
	mdmAnyMW.DELETE("/api/_version_/automdm/setup_experience/eula/{token}", deleteMDMEULAEndpoint, deleteMDMEULARequest{})

	// This section handles MDM "assets", specifically for Apple DDM.
	mdmAppleMW.GET("/api/_version_/automdm/assets", listAppleDDMAssetsEndpoint, listAppleDDMAssetsRequest{})
	mdmAppleMW.GET("/api/_version_/automdm/assets/{asset_uuid}", getAppleDDMAssetEndpoint, getAppleDDMAssetRequest{})
	mdmAppleMW.WithRequestBodySizeLimit(automdm.MaxMDMAssetSize).POST("/api/_version_/automdm/assets", createAppleDDMAssetEndpoint, createAppleDDMAssetRequest{})
	mdmAppleMW.DELETE("/api/_version_/automdm/assets/{asset_uuid}", deleteAppleDDMAssetEndpoint, deleteAppleDDMAssetRequest{})
	mdmAppleMW.WithRequestBodySizeLimit(automdm.MaxBatchProfileSize).POST("/api/_version_/automdm/assets/batch", batchSetAppleDDMAssetsEndpoint, batchSetAppleDDMAssetsRequest{})

	mdmAppleMW.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/apns_ping", apnsPingRequestEndpoint, sendAPNSPingRequest{})

	mdmAppleMW.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/configuration_profiles/{profile_uuid}/install", installSelfServiceConfigurationProfileEndpoint, installSelfServiceConfigurationProfileRequest{})
	mdmAppleMW.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/configuration_profiles/{profile_uuid}/uninstall", uninstallSelfServiceConfigurationProfileEndpoint, uninstallSelfServiceConfigurationProfileRequest{})

	// Deprecated: use the /automdm/hosts/{id} mdm.profiles instead for host scoped, or /automdm/configuration_profiles for team specific profiles
	// This returns a list of all Apple configuration profiles for the given team the host is on, not scoped to the individual host.
	mdmAnyMW.GET("/api/_version_/automdm/hosts/{id}/configuration_profiles", getHostProfilesEndpoint, getHostProfilesRequest{})

	mdmAnyMW.WithRequestBodySizeLimit(automdm.MaxMDMCommandSize).POST("/api/_version_/automdm/commands/run", runMDMCommandEndpoint, runMDMCommandRequest{})
	mdmAnyMW.GET("/api/_version_/automdm/commands/results", getMDMCommandResultsEndpoint, getMDMCommandResultsRequest{})
	mdmAnyMW.GET("/api/_version_/automdm/commands", listMDMCommandsEndpoint, listMDMCommandsRequest{})
	mdmAnyMW.DELETE("/api/_version_/automdm/hosts/{id:[0-9]+}/mdm", mdmUnenrollEndpoint, mdmUnenrollRequest{})
	ue.GET("/api/_version_/automdm/disk_encryption", getMDMDiskEncryptionSummaryEndpoint, getMDMDiskEncryptionSummaryRequest{})
	ue.GET("/api/_version_/automdm/hosts/{id}/encryption_key", getHostEncryptionKey, getHostEncryptionKeyRequest{})
	ue.POST("/api/_version_/automdm/hosts/{id:[0-9]+}/encryption_key/rotate", rotateDiskEncryptionKeyEndpoint, rotateDiskEncryptionKeyRequest{})
	ue.GET("/api/_version_/automdm/configuration_profiles/summary", getMDMProfilesSummaryEndpoint, getMDMProfilesSummaryRequest{})
	mdmAnyMW.GET("/api/_version_/automdm/configuration_profiles/{profile_uuid}", getMDMConfigProfileEndpoint, getMDMConfigProfileRequest{})
	ue.DELETE("/api/_version_/automdm/configuration_profiles/{profile_uuid}", deleteMDMConfigProfileEndpoint, deleteMDMConfigProfileRequest{})
	mdmAnyMW.GET("/api/_version_/automdm/configuration_profiles", listMDMConfigProfilesEndpoint, listMDMConfigProfilesRequest{})
	mdmAnyMW.WithRequestBodySizeLimit(automdm.MaxProfileSize).POST("/api/_version_/automdm/configuration_profiles", newMDMConfigProfileEndpoint, newMDMConfigProfileRequest{})
	mdmAnyMW.WithRequestBodySizeLimit(automdm.MaxProfileSize).PATCH("/api/_version_/automdm/configuration_profiles/{profile_uuid}", updateMDMConfigProfileEndpoint, updateMDMConfigProfileRequest{})
	// Batch needs to allow being called without any MDM enabled, to support deleting profiles, but will fail later if trying to add
	ue.WithRequestBodySizeLimit(automdm.MaxBatchProfileSize).POST("/api/_version_/automdm/configuration_profiles/batch", batchModifyMDMConfigProfilesEndpoint, batchModifyMDMConfigProfilesRequest{})

	mdmAnyMW.POST("/api/_version_/automdm/hosts/{host_id:[0-9]+}/configuration_profiles/{profile_uuid}/resend", resendHostMDMProfileEndpoint, resendHostMDMProfileRequest{})
	mdmAnyMW.POST("/api/_version_/automdm/hosts/{host_id:[0-9]+}/name_template/resend", resendHostNameTemplateEndpoint, resendHostNameTemplateRequest{})
	mdmAnyMW.POST("/api/_version_/automdm/configuration_profiles/resend/batch", batchResendMDMProfileToHostsEndpoint, batchResendMDMProfileToHostsRequest{})
	mdmAnyMW.GET("/api/_version_/automdm/configuration_profiles/{profile_uuid}/status", getMDMConfigProfileStatusEndpoint, getMDMConfigProfileStatusRequest{})

	ue.POST("/api/_version_/automdm/disk_encryption", updateDiskEncryptionEndpoint, updateDiskEncryptionRequest{})
	ue.POST("/api/_version_/automdm/host_name_template", updateHostNameTemplateEndpoint, updateHostNameTemplateRequest{})

	// the following set of mdm endpoints must always be accessible (even
	// if MDM is not configured) as it bootstraps the setup of MDM
	// (generates CSR request for APNs, plus the SCEP and ABM keypairs).
	ue.GET("/api/_version_/automdm/mdm/apple/ab_public_key", generateABMKeyPairEndpoint, nil)
	ue.POST("/api/_version_/automdm/ab_tokens", uploadABMTokenEndpoint, uploadABMTokenRequest{})
	ue.DELETE("/api/_version_/automdm/ab_tokens/{id:[0-9]+}", deleteABMTokenEndpoint, deleteABMTokenRequest{})
	ue.GET("/api/_version_/automdm/ab_tokens", listABMTokensEndpoint, nil)
	ue.GET("/api/_version_/automdm/ab_tokens/count", countABMTokensEndpoint, nil)
	ue.PATCH("/api/_version_/automdm/ab_tokens/{id:[0-9]+}/teams", updateABMTokenTeamsEndpoint, updateABMTokenTeamsRequest{})
	ue.PATCH("/api/_version_/automdm/ab_tokens/{id:[0-9]+}/default", setABMTokenDefaultEndpoint, setABMTokenDefaultRequest{})
	ue.PATCH("/api/_version_/automdm/ab_tokens/{id:[0-9]+}/renew", renewABMTokenEndpoint, renewABMTokenRequest{})

	ue.GET("/api/_version_/automdm/mdm/apple/request_csr", getMDMAppleCSREndpoint, getMDMAppleCSRRequest{})
	ue.POST("/api/_version_/automdm/mdm/apple/apns_certificate", uploadMDMAppleAPNSCertEndpoint, uploadMDMAppleAPNSCertRequest{})
	ue.DELETE("/api/_version_/automdm/mdm/apple/apns_certificate", deleteMDMAppleAPNSCertEndpoint, deleteMDMAppleAPNSCertRequest{})

	// VPP Tokens
	ue.GET("/api/_version_/automdm/vpp_tokens", getVPPTokens, getVPPTokensRequest{})
	ue.POST("/api/_version_/automdm/vpp_tokens", uploadVPPTokenEndpoint, uploadVPPTokenRequest{})
	ue.PATCH("/api/_version_/automdm/vpp_tokens/{id}/teams", patchVPPTokensTeams, patchVPPTokensTeamsRequest{})
	ue.PATCH("/api/_version_/automdm/vpp_tokens/{id}/renew", patchVPPTokenRenewEndpoint, patchVPPTokenRenewRequest{})
	ue.DELETE("/api/_version_/automdm/vpp_tokens/{id}", deleteVPPToken, deleteVPPTokenRequest{})

	// Batch VPP Associations
	ue.POST("/api/_version_/automdm/software/app_store_apps/batch", batchAssociateAppStoreAppsEndpoint, batchAssociateAppStoreAppsRequest{})

	// batch-apply is accessible even though MDM is not enabled, it needs
	// to support the case where `automdmctl get config`'s output is used as
	// input to `automdmctl apply`
	ue.WithRequestBodySizeLimit(automdm.MaxBatchProfileSize).POST("/api/_version_/automdm/mdm/profiles/batch", batchSetMDMProfilesEndpoint, batchSetMDMProfilesRequest{})

	// Certificate Authority endpoints
	ue.POST("/api/_version_/automdm/certificate_authorities", createCertificateAuthorityEndpoint, createCertificateAuthorityRequest{})
	ue.GET("/api/_version_/automdm/certificate_authorities", listCertificateAuthoritiesEndpoint, listCertificateAuthoritiesRequest{})
	ue.GET("/api/_version_/automdm/microsoft_graph_credentials", listMicrosoftGraphCredentialsEndpoint, nil)
	ue.PUT("/api/_version_/automdm/microsoft_graph_credentials", applyMicrosoftGraphCredentialsEndpoint, applyMicrosoftGraphCredentialsRequest{})
	ue.GET("/api/_version_/automdm/certificate_authorities/{id:[0-9]+}", getCertificateAuthorityEndpoint, getCertificateAuthorityRequest{})
	ue.DELETE("/api/_version_/automdm/certificate_authorities/{id:[0-9]+}", deleteCertificateAuthorityEndpoint, deleteCertificateAuthorityRequest{})
	ue.PATCH("/api/_version_/automdm/certificate_authorities/{id:[0-9]+}", updateCertificateAuthorityEndpoint, updateCertificateAuthorityRequest{})
	ue.POST("/api/_version_/automdm/certificate_authorities/{id:[0-9]+}/request_certificate", requestCertificateEndpoint, requestCertificateRequest{})
	ue.POST("/api/_version_/automdm/spec/certificate_authorities", batchApplyCertificateAuthoritiesEndpoint, batchApplyCertificateAuthoritiesRequest{})
	ue.GET("/api/_version_/automdm/spec/certificate_authorities", getCertificateAuthoritiesSpecEndpoint, getCertificateAuthoritiesSpecRequest{})

	mdmAndroidMW := ue.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAndroidMDM())
	mdmAndroidMW.POST("/api/_version_/automdm/software/web_apps", createAndroidWebAppEndpoint, createAndroidWebAppRequest{})

	ipBanner := redis.NewIPBanner(
		redisPool, "ipbanner::",
		DeviceIPAllowedConsecutiveFailingRequestsCount,
		DeviceIPAllowedConsecutiveFailingRequestsTimeWindow,
		DeviceIPBanTime,
	)
	errorLimiter := ratelimit.NewErrorMiddleware(ipBanner).Limit(logger)

	// Rate limiters shared across the login/SSO endpoints.
	limiter := ratelimit.NewMiddleware(limitStore)

	// By default, MDM SSO shares the login rate limit bucket; if MDM SSO limit is overridden, MDM SSO gets its
	// own rate limit bucket.
	loginRateLimit := throttled.PerMin(10)
	if extra.loginRateLimit != nil {
		loginRateLimit = *extra.loginRateLimit
	}
	loginLimiter := limiter.Limit("login", throttled.RateQuota{MaxRate: loginRateLimit, MaxBurst: 9})
	mdmSsoLimiter := loginLimiter
	if extra.mdmSsoRateLimit != nil {
		mdmSsoLimiter = limiter.Limit("mdm_sso", throttled.RateQuota{MaxRate: *extra.mdmSsoRateLimit, MaxBurst: 9})
	}
	// The SSO callback gets its own dedicated bucket (separate from the login
	// bucket) so a flood on the unauthenticated callback can't exhaust the
	// rate-limit budget that legitimate password logins depend on. The rate
	// defaults to the login rate unless explicitly overridden.
	ssoRateLimit := loginRateLimit
	if extra.ssoRateLimit != nil {
		ssoRateLimit = *extra.ssoRateLimit
	}
	ssoLimiter := limiter.Limit("sso", throttled.RateQuota{MaxRate: ssoRateLimit, MaxBurst: 9})

	// Device-authenticated endpoints.
	de := newDeviceAuthenticatedEndpointer(svc, logger, opts, r, apiVersions...)
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}", getDeviceHostEndpoint, getDeviceHostRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/refetch", refetchDeviceHostEndpoint, refetchDeviceHostRequest{})
	// Deprecated: Device mapping data is now included in host details endpoint
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/device_mapping", listDeviceHostDeviceMappingEndpoint, listDeviceHostDeviceMappingRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/macadmins", getDeviceMacadminsDataEndpoint, getDeviceMacadminsDataRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/policies", listDevicePoliciesEndpoint, listDevicePoliciesRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software", getDeviceSoftwareEndpoint, getDeviceSoftwareRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/software/install/{software_title_id}", submitSelfServiceSoftwareInstall, automdmSelfServiceSoftwareInstallRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/software/install_all", submitSelfServiceSoftwareInstallAll, automdmSelfServiceSoftwareInstallAllRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/software/uninstall/{software_title_id}", submitDeviceSoftwareUninstall, automdmDeviceSoftwareUninstallRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software/install/{install_uuid}/results", getDeviceSoftwareInstallResultsEndpoint, getDeviceSoftwareInstallResultsRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software/uninstall/{execution_id}/results", getDeviceSoftwareUninstallResultsEndpoint, getDeviceSoftwareUninstallResultsRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software/self_service_categories", getDeviceSelfServiceCategoriesEndpoint, getDeviceSelfServiceCategoriesRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/certificates", listDeviceCertificatesEndpoint, listDeviceCertificatesRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/setup_experience/status", getDeviceSetupExperienceStatusEndpoint, getDeviceSetupExperienceStatusRequest{})
	de.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software/titles/{software_title_id}/icon", getDeviceSoftwareIconEndpoint, getDeviceSoftwareIconRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/mdm/linux/trigger_escrow", triggerLinuxDiskEncryptionEscrowEndpoint, triggerLinuxDiskEncryptionEscrowRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/disk_encryption_pin", submitDiskEncryptionPINEndpoint, submitDiskEncryptionPINRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/bypass_conditional_access", bypassConditionalAccessEndpoint, bypassConditionalAccessRequest{})
	de.WithCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/conditional_access/google", getDeviceGoogleConditionalAccessEndpoint, getDeviceGoogleConditionalAccessRequest{})

	// Endpoints exempt from the AutoMDM Desktop SSO gate.
	deNoSSO := newDeviceSSOExemptEndpointer(svc, logger, opts, r, apiVersions...)
	deNoSSO.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/desktop", getAutoMDMDesktopEndpoint, getAutoMDMDesktopRequest{})
	deNoSSO.WithCustomMiddleware(errorLimiter).HEAD("/api/_version_/automdm/device/{token}/ping", devicePingEndpoint, deviceAuthPingRequest{})
	deNoSSO.WithCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/transparency", transparencyURL, transparencyURLRequest{})
	deNoSSO.WithCustomMiddleware(errorLimiter).WithRequestBodySizeLimit(automdm.MaxAutoMDMdErrorReportSize).POST("/api/_version_/automdm/device/{token}/debug/errors", automdmdError, automdmdErrorRequest{})
	// Device-authenticated, so unlike the unauthenticated SSO routes this needs no
	// pre-auth flood guard. It is quota-limited for amplification instead: each
	// successful call refetches the IdP metadata, so one leaked device token could
	// drive sustained outbound traffic at the customer's IdP from AutoMDM's own IP,
	// and errorLimiter cannot see it because those requests succeed. Own bucket so
	// device traffic cannot exhaust the budget logins and enrollments depend on,
	// at the same rate as the other end-user-facing SSO path, so
	// auth.sso_rate_limit_per_minute tunes both.
	deviceSsoLimiter := limiter.Limit("device_sso", throttled.RateQuota{MaxRate: ssoRateLimit, MaxBurst: 9})
	deNoSSO.WithCustomMiddleware(errorLimiter, deviceSsoLimiter).POST("/api/_version_/automdm/device/{token}/sso", initiateDeviceSSOEndpoint, initiateDeviceSSORequest{})
	// migrate_mdm is posted by the tray app's "Migrate to AutoMDM" dialog with the
	// device token alone (orbit/pkg/useraction -> DeviceClient.MigrateMDM), and it
	// has no browser to complete an IdP round-trip in.
	deNoSSOmdm := deNoSSO.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAppleMDM())
	deNoSSOmdm.AppendCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/migrate_mdm", migrateMDMDeviceEndpoint, deviceMigrateMDMRequest{})

	// Device authenticated, Apple MDM endpoints.
	demdm := de.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAppleMDM())
	demdm.AppendCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/mdm/apple/manual_enrollment_profile", getDeviceMDMManualEnrollProfileEndpoint, getDeviceMDMManualEnrollProfileRequest{})
	demdm.AppendCustomMiddleware(errorLimiter).GET("/api/_version_/automdm/device/{token}/software/commands/{command_uuid}/results", getDeviceMDMCommandResultsEndpoint, getDeviceMDMCommandResultsRequest{})
	demdm.AppendCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/apns_ping", deviceSendAPNSPing, deviceSendAPNSPingRequest{})
	demdm.AppendCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/configuration_profiles/{profile_uuid}/install", deviceInstallSelfServiceConfigurationProfileEndpoint, deviceInstallSelfServiceConfigurationProfileRequest{})
	demdm.AppendCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/configuration_profiles/{profile_uuid}/uninstall", deviceUninstallSelfServiceConfigurationProfileEndpoint, deviceUninstallSelfServiceConfigurationProfileRequest{})

	// Device authenticated, any MDM: the resend serves Windows profiles too.
	deAnyMDM := de.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAnyMDM())
	deAnyMDM.AppendCustomMiddleware(errorLimiter).POST("/api/_version_/automdm/device/{token}/configuration_profiles/{profile_uuid}/resend", resendDeviceConfigurationProfileEndpoint, resendDeviceConfigurationProfileRequest{})

	// host-authenticated endpoints
	//
	// The HTTP-level pre-auth middleware authenticates osquery requests via the
	// Authorization: NodeKey <token> header BEFORE the request body is read.
	//
	// osquery.allow_body_auth_fallback selects which auth scheme is in
	// effect:
	//   - true (default): header is ignored entirely; body-based
	//     auth is the sole authenticator.
	//   - false: header is required; body-based auth is not
	//     consulted. Pre-auth rejects on absent/invalid headers before the
	//     body is read.
	//
	// `he` is the base host-authenticated endpointer with no pre-auth wrap.
	// `heHeader` is the same endpointer optionally wrapped with the
	// header pre-auth in strict mode.
	he := newHostAuthenticatedEndpointer(svc, logger, opts, r, apiVersions...)
	heHeader := he
	if !config.Osquery.AllowBodyAuthFallback {
		heHeader = he.WithHTTPPreAuth(osqueryHeaderPreAuth(svc, logger))
	}

	// Note that the /osquery/ endpoints are *not* versioned, i.e. there is no
	// `_version_` placeholder in the path. This is deliberate, see
	// https://github.com/fleetdm/fleet/pull/4731#discussion_r838931732 For now
	// we add an alias to `/api/v1/osquery` so that it is backwards compatible,
	// but even that `v1` is *not* part of the standard versioning, it will still
	// work even after we remove support for the `v1` version for the rest of the
	// API. This allows us to deprecate osquery endpoints separately.
	heHeader.WithAltPaths("/api/v1/osquery/config").
		POST("/api/osquery/config", getClientConfigEndpoint, getClientConfigRequest{})
	distReadEndpoint := getDistributedQueriesEndpoint
	if extra.agentWSHub != nil && config.Logging.Debug {
		// Count distributed/read requests per host and path for /debug/agentws.
		// Debug-mode only: production servers skip the per-request accounting.
		distReadEndpoint = recordDistributedReadStats(extra.agentWSHub, getDistributedQueriesEndpoint)
	}
	heHeader.WithAltPaths("/api/v1/osquery/distributed/read").
		POST("/api/osquery/distributed/read", distReadEndpoint, getDistributedQueriesRequest{})
		// /distributed/write and /log accept large payloads. The body-size
		// policy depends on which auth scheme is in effect:
		//   - body-auth mode: per-route limit (operator-tunable via
		//     MaxLogWriteBodySize / MaxDistributedWriteBodySize, defaulting to
		//     the historical DefaultMaxOsquery* constants).
		//   - header-auth mode: no per-route limit. Once pre-auth accepts the
		//     request via Authorization: NodeKey, the agent is authenticated
		//     and the body can be any size.
	distWriteReg, logWriteReg := heHeader, heHeader
	if config.Osquery.AllowBodyAuthFallback {
		distLimit := config.Osquery.MaxDistributedWriteBodySize
		if distLimit == 0 {
			distLimit = automdm.DefaultMaxOsqueryDistributedWriteSize
		}
		distWriteReg = heHeader.WithRequestBodySizeLimit(distLimit)

		logLimit := config.Osquery.MaxLogWriteBodySize
		if logLimit == 0 {
			logLimit = automdm.DefaultMaxOsqueryLogWriteSize
		}
		logWriteReg = heHeader.WithRequestBodySizeLimit(logLimit)
	} else {
		distWriteReg = heHeader.SkipRequestBodySizeLimit()
		logWriteReg = heHeader.SkipRequestBodySizeLimit()
	}
	distWriteReg.WithAltPaths("/api/v1/osquery/distributed/write").
		POST("/api/osquery/distributed/write", submitDistributedQueryResultsEndpoint, submitDistributedQueryResultsRequestShim{})
	heHeader.WithAltPaths("/api/v1/osquery/carve/begin").
		POST("/api/osquery/carve/begin", carveBeginEndpoint, automdm.CarveBeginRequest{})
	logWriteReg.WithAltPaths("/api/v1/osquery/log").
		POST("/api/osquery/log", submitLogsEndpoint, submitLogsRequest{})
	he.WithAltPaths("/api/v1/osquery/yara/{name}").
		POST("/api/osquery/yara/{name}", getYaraEndpoint, getYaraRequest{})

	// android authenticated end-points
	// Authentication is implemented using the orbit_node_key from the 'Authentication' header.
	// The 'orbit_node_key' is used because it's the only thing we have available when the device gets enrolled
	// after the MDM setup is complete.
	androidEndpoints := androidAuthenticatedEndpointer(svc, logger, opts, r, apiVersions...)
	androidEndpoints.GET("/api/automdmd/certificates/{id:[0-9]+}", getDeviceCertificateTemplateEndpoint, getDeviceCertificateTemplateRequest{})
	androidEndpoints.PUT("/api/automdmd/certificates/{id:[0-9]+}/status", updateCertificateStatusEndpoint, updateCertificateStatusRequest{})

	// orbit authenticated endpoints
	oe := newOrbitAuthenticatedEndpointer(svc, logger, opts, r, apiVersions...)
	oe.POST("/api/automdm/orbit/device_token", setOrUpdateDeviceTokenEndpoint, automdm.SetOrUpdateDeviceTokenRequest{})
	oe.POST("/api/automdm/orbit/config", getOrbitConfigEndpoint, automdm.OrbitGetConfigRequest{})
	// using POST to get a script execution request since all authenticated orbit
	// endpoints are POST due to passing the device token in the JSON body.
	oe.POST("/api/automdm/orbit/scripts/request", getOrbitScriptEndpoint, automdm.OrbitGetScriptRequest{})
	oe.POST("/api/automdm/orbit/scripts/result", postOrbitScriptResultEndpoint, automdm.OrbitPostScriptResultRequest{})
	oe.PUT("/api/automdm/orbit/device_mapping", putOrbitDeviceMappingEndpoint, automdm.OrbitPutDeviceMappingRequest{})
	oe.WithRequestBodySizeLimit(automdm.MaxMultiScriptQuerySize).POST("/api/automdm/orbit/software_install/result", postOrbitSoftwareInstallResultEndpoint, automdm.OrbitPostSoftwareInstallResultRequest{})
	oe.POST("/api/automdm/orbit/software_install/package", orbitDownloadSoftwareInstallerEndpoint, automdm.OrbitDownloadSoftwareInstallerRequest{})
	oe.POST("/api/automdm/orbit/software_install/details", getOrbitSoftwareInstallDetails, automdm.OrbitGetSoftwareInstallRequest{})
	oe.POST("/api/automdm/orbit/setup_experience/init", orbitSetupExperienceInitEndpoint, automdm.OrbitSetupExperienceInitRequest{})

	// POST /api/automdm/orbit/setup_experience/status is used by macOS and Linux hosts.
	// For macOS hosts we verify Apple MDM is enabled and configured.
	oeAppleMDM := oe.WithCustomMiddlewareAfterAuth(mdmConfiguredMiddleware.VerifyAppleMDMOnMacOSHosts())
	oeAppleMDM.POST("/api/automdm/orbit/setup_experience/status", getOrbitSetupExperienceStatusEndpoint, automdm.GetOrbitSetupExperienceStatusRequest{})

	oeWindowsMDM := oe.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyWindowsMDM())
	oeWindowsMDM.POST("/api/automdm/orbit/disk_encryption_key", postOrbitDiskEncryptionKeyEndpoint, automdm.OrbitPostDiskEncryptionKeyRequest{})
	oeWindowsMDM.POST("/api/automdm/orbit/disk_encryption_protection", postOrbitDiskEncryptionProtectionEndpoint, automdm.OrbitPostDiskEncryptionProtectionRequest{})
	oeWindowsMDM.POST("/api/automdm/orbit/disk_encryption_pin/details", getOrbitDiskEncryptionPINDetailsEndpoint, automdm.OrbitGetDiskEncryptionPINDetailsRequest{})
	oeWindowsMDM.POST("/api/automdm/orbit/disk_encryption_pin/result", postOrbitDiskEncryptionPINResultEndpoint, automdm.OrbitPostDiskEncryptionPINResultRequest{})
	// managed local account escrow is Windows-MDM-specific, so it fails fast when Windows MDM is off.
	oeWindowsMDM.POST("/api/automdm/orbit/managed_local_account", postOrbitManagedLocalAccountEndpoint, automdm.OrbitPostManagedLocalAccountRequest{})

	oe.POST("/api/automdm/orbit/luks_data", postOrbitLUKSEndpoint, automdm.OrbitPostLUKSRequest{})
	oe.POST("/api/automdm/orbit/entra_device_token", postOrbitEntraDeviceTokenEndpoint, automdm.OrbitPostEntraDeviceTokenRequest{})

	// unauthenticated endpoints - most of those are either login-related,
	// invite-related or host-enrolling. So they typically do some kind of
	// one-time authentication by verifying that a valid secret token is provided
	// with the request.
	ne := newNoAuthEndpointer(svc, opts, r, apiVersions...)
	ne.WithAltPaths("/api/v1/osquery/enroll").
		POST("/api/osquery/enroll", enrollAgentEndpoint, contract.EnrollOsqueryAgentRequest{})

	// These endpoint are token authenticated.
	// NOTE: remember to update
	// `service.mdmConfigurationRequiredEndpoints` when you add an
	// endpoint that's behind the mdmConfiguredMiddleware, this applies
	// both to this set of endpoints and to any user authenticated
	// endpoints using `mdmAppleMW.*` above in this file.
	neAppleMDM := ne.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAppleMDM())
	neAnyMDM := ne.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyAnyMDM())
	neAppleMDM.GET(apple_mdm.EnrollPath, mdmAppleEnrollEndpoint, mdmAppleEnrollRequest{})
	neAppleMDM.POST(apple_mdm.EnrollPath, mdmAppleEnrollEndpoint, mdmAppleEnrollRequest{})

	neAppleMDM.POST("/api/_version_/automdm/ota_enrollment", mdmAppleOTAEndpoint, mdmAppleOTARequest{})

	// Deprecated: GET /mdm/bootstrap is replaced by GET /bootstrap. AutoMDM no longer sends hosts this URL, but it stays for hosts that
	// were sent it in an InstallEnterpriseApplication command before the upgrade.
	neAppleMDM.GET("/api/_version_/automdm/mdm/bootstrap", downloadBootstrapPackageEndpoint, downloadBootstrapPackageRequest{})
	neAppleMDM.GET("/api/_version_/automdm/bootstrap", downloadBootstrapPackageEndpoint, downloadBootstrapPackageRequest{})

	neAnyMDM.GET("/api/_version_/automdm/setup_experience/eula/{token}", getMDMEULAEndpoint, getMDMEULARequest{})

	// Get OTA profile
	neAppleMDM.GET("/api/_version_/automdm/enrollment_profiles/ota", getOTAProfileEndpoint, getOTAProfileRequest{})

	// This is the account-driven enrollment endpoint for BYoD Apple devices, also known as User Enrollment.
	neAppleMDM.POST(apple_mdm.AccountDrivenEnrollTokenPath, mdmAppleAccountEnrollEndpoint, mdmAppleAccountEnrollRequest{})
	// Deprecated: Non unique token enrollment is deprecated in favour of AccountDrivenEnrollTokenPath. This is the account-driven enrollment endpoint for BYoD Apple devices, also known as User Enrollment.
	neAppleMDM.POST(apple_mdm.AccountDrivenEnrollPath, mdmAppleAccountEnrollEndpoint, mdmAppleAccountEnrollRequest{})

	// Apple Platform SSO (PSSO) endpoints, used by AutoMDM's Platform SSO
	// extension. Unauthenticated at the HTTP layer: the token endpoint
	// authenticates protocol-level via JWTs signed with registered device
	// keys, and all endpoints are gated in the service layer on the feature
	// being configured. Request bodies are capped at the endpointer's default
	// size limit. The related /.well-known/apple-app-site-association document
	// is served from the root mux (see registerPSSO).
	ne.POST(pssoNoncePath, pssoNonceEndpoint, pssoNonceRequest{})
	ne.POST(pssoRegistrationPath, pssoRegistrationEndpoint, pssoRegistrationRequest{})
	ne.POST(pssoTokenPath, pssoTokenEndpoint, pssoTokenRequest{})
	ne.GET(pssoJWKSPath, pssoJWKSEndpoint, pssoJWKSRequest{})
	// This is for OAUTH2 token based auth
	// ne.POST(apple_mdm.EnrollPath+"/token", mdmAppleAccountEnrollTokenEndpoint, mdmAppleAccountEnrollTokenRequest{})

	// These endpoint are used by Microsoft devices during MDM device enrollment phase
	neWindowsMDM := ne.WithCustomMiddleware(mdmConfiguredMiddleware.VerifyWindowsMDM())

	// Microsoft MS-MDE2 Endpoints
	// This endpoint is unauthenticated and is used by Microsoft devices to discover the MDM server endpoints
	neWindowsMDM.WithRequestBodySizeLimit(automdm.MaxMicrosoftMDMSize).POST(microsoft_mdm.MDE2DiscoveryPath, mdmMicrosoftDiscoveryEndpoint, SoapRequestContainer{})

	// This endpoint is authenticated using the BinarySecurityToken header field
	neWindowsMDM.WithRequestBodySizeLimit(automdm.MaxMicrosoftMDMSize).POST(microsoft_mdm.MDE2PolicyPath, mdmMicrosoftPolicyEndpoint, SoapRequestContainer{})

	// This endpoint is authenticated using the BinarySecurityToken header field
	neWindowsMDM.WithRequestBodySizeLimit(automdm.MaxMicrosoftMDMSize).POST(microsoft_mdm.MDE2EnrollPath, mdmMicrosoftEnrollEndpoint, SoapRequestContainer{})

	// This endpoint is unauthenticated for now
	// It should be authenticated through TLS headers once proper implementation is in place
	neWindowsMDM.WithRequestBodySizeLimit(automdm.MaxMicrosoftMDMSize).POST(microsoft_mdm.MDE2ManagementPath, mdmMicrosoftManagementEndpoint, SyncMLReqMsgContainer{})

	// This endpoint is unauthenticated and is used by to retrieve the MDM enrollment Terms of Use
	neWindowsMDM.WithRequestBodySizeLimit(automdm.MaxMicrosoftMDMSize).GET(microsoft_mdm.MDE2TOSPath, mdmMicrosoftTOSEndpoint, MDMWebContainer{})

	// These endpoints are unauthenticated and made from orbit, and add the orbit capabilities header.
	neOrbit := newOrbitNoAuthEndpointer(svc, opts, r, apiVersions...)
	neOrbit.POST("/api/automdm/orbit/enroll", enrollOrbitEndpoint, automdm.EnrollOrbitRequest{})

	// Token regex rejects non-hex garbage at the router; further length and
	// existence checks happen in the service layer.
	ne.GET("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/in_house_app/{token:[a-f0-9-]+}", getInHouseAppPackageEndpoint, getInHouseAppPackageRequest{})
	ne.GET("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/in_house_app/manifest/{token:[a-f0-9-]+}", getInHouseAppManifestEndpoint, getInHouseAppManifestRequest{})

	// For some reason osquery does not provide a node key with the block data.
	// Instead the carve session ID should be verified in the service method.
	// Since []byte slices is encoded as base64 in JSON, increase the limit to 1.5x
	//
	// When osquery.allow_body_auth_fallback is false the
	// osqueryCarveBlockHeaderPreAuth wrapper is installed: it requires a
	// valid Authorization: NodeKey header (rejecting absent or invalid
	// headers before the body is read) and stashes the authenticated host
	// in ctx so CarveBlock can enforce the carve-ownership check. When
	// the flag is true the wrapper is
	// not installed and /carve/block falls back to its existing
	// streaming-parse auth (session_id + request_id only).
	carveBlockReg := ne.SkipRequestBodySizeLimit()
	if !config.Osquery.AllowBodyAuthFallback {
		carveBlockReg = carveBlockReg.WithHTTPPreAuth(osqueryCarveBlockHeaderPreAuth(svc, logger))
	}
	carveBlockReg.WithAltPaths("/api/v1/osquery/carve/block").
		POST("/api/osquery/carve/block", carveBlockEndpoint, decodeCarveBlockRequest{})

	ne.GET("/api/_version_/automdm/software/titles/{title_id:[0-9]+}/package/token/{token}", downloadSoftwareInstallerEndpoint,
		downloadSoftwareInstallerRequest{})

	ne.POST("/api/_version_/automdm/perform_required_password_reset", performRequiredPasswordResetEndpoint, performRequiredPasswordResetRequest{})
	ne.POST("/api/_version_/automdm/users", createUserFromInviteEndpoint, createUserRequest{})
	ne.GET("/api/_version_/automdm/invites/{token}", verifyInviteEndpoint, verifyInviteRequest{})
	ne.POST("/api/_version_/automdm/reset_password", resetPasswordEndpoint, resetPasswordRequest{})
	ne.POST("/api/_version_/automdm/logout", logoutEndpoint, nil)

	orgLogoLimiter := ratelimit.NewMiddleware(limitStore).Limit(
		"org_logo",
		throttled.RateQuota{MaxRate: throttled.PerMin(60), MaxBurst: 20},
	)
	ne.WithCustomMiddleware(orgLogoLimiter).
		GET("/api/_version_/automdm/logo", getOrgLogoEndpoint, getOrgLogoRequest{})

	ne.POST("/api/v1/automdm/sso", initiateSSOEndpoint, initiateSSORequest{})
	// The SSO callback is unauthenticated and internet-reachable. Rate-limit it
	// (dedicated bucket) and cap the body to keep pre-auth attacks surface small.
	ne.WithCustomMiddleware(ssoLimiter).
		WithRequestBodySizeLimit(automdm.MaxSSOCallbackSize).
		POST("/api/v1/automdm/sso/callback", makeCallbackSSOEndpoint(config.Server.URLPrefix), callbackSSORequest{})
	ne.GET("/api/v1/automdm/sso", settingsSSOEndpoint, nil)

	// the websocket distributed query results endpoint is a bit different - the
	// provided path is a prefix, not an exact match, and it is not a go-kit
	// endpoint but a raw http.Handler. It uses the NoAuthEndpointer because
	// authentication is done when the websocket session is established, inside
	// the handler.
	ne.UsePathPrefix().PathHandler("GET", "/api/_version_/automdm/results/",
		makeStreamDistributedQueryCampaignResultsHandler(config.Server, svc, logger))

	// The agent notifications WebSocket endpoint is a raw http.Handler on the
	// NoAuth endpointer: the upgrade request is authenticated inside the
	// handler with the orbit node key. Registered only when the websocket
	// transport is enabled, so with the feature off (the default) upgrade
	// attempts get a 404.
	if config.WebSocket.TransportEnabled && extra.agentWSHub != nil {
		ne.HandleHTTPHandler("/api/automdm/orbit/notifications",
			agentws.NewHandler(extra.agentWSHub, svc, logger), "GET")
	}

	// A raw handler: Streamable HTTP owns the request and response, and the
	// handler runs AutoMDM's auth and license checks itself. Always registered,
	// since server_settings.mcp_disabled can change at runtime.
	ue.HandleHTTPHandler("/api/_version_/automdm/mcp", extra.mcpHandler, "POST")

	quota := throttled.RateQuota{MaxRate: throttled.PerHour(10), MaxBurst: forgotPasswordRateLimitMaxBurst}
	ne.
		WithCustomMiddleware(limiter.Limit("forgot_password", quota)).
		POST("/api/_version_/automdm/forgot_password", forgotPasswordEndpoint, forgotPasswordRequest{})

	ne.WithCustomMiddleware(loginLimiter).
		POST("/api/_version_/automdm/login", loginEndpoint, automdm.LoginRequest{})
	ne.WithCustomMiddleware(limiter.Limit("mfa", throttled.RateQuota{MaxRate: loginRateLimit, MaxBurst: 9})).
		POST("/api/_version_/automdm/sessions", sessionCreateEndpoint, sessionCreateRequest{})

	ne.HEAD("/api/automdm/device/ping", devicePingEndpoint, devicePingRequest{})

	ne.HEAD("/api/automdm/orbit/ping", orbitPingEndpoint, automdm.OrbitPingRequest{})

	// Called by automdm.com with this server's secret after an end user signs in to Google.
	ne.WithCustomMiddleware(limiter.Limit("google_conditional_access_register",
		throttled.RateQuota{MaxRate: throttled.PerMin(600), MaxBurst: 100})).
		POST("/api/_version_/automdm/google_conditional_access/register", registerGoogleConditionalAccessDeviceEndpoint, registerGoogleConditionalAccessDeviceRequest{})

	// This is a callback endpoint for calendar integration -- it is called to notify an event change in a user calendar
	ne.POST("/api/_version_/automdm/calendar/webhook/{event_uuid}", calendarWebhookEndpoint, calendarWebhookRequest{})

	neAppleMDM.WithCustomMiddleware(mdmSsoLimiter).
		POST("/api/_version_/automdm/mdm/sso", initiateMDMSSOEndpoint, initiateMDMSSORequest{})
	// Same posture as the regular SSO callback: rate-limited (already) plus a
	// tight body cap to keep pre-auth attacks surface small.
	ne.WithCustomMiddleware(mdmSsoLimiter).
		WithRequestBodySizeLimit(automdm.MaxSSOCallbackSize).
		POST("/api/_version_/automdm/mdm/sso/callback", callbackMDMSSOEndpoint, callbackMDMSSORequest{})
}

// WithSetup is an http middleware that checks if setup procedures have been completed.
// If setup hasn't been completed it serves the API with a setup middleware.
// If the server is already configured, the default API handler is exposed.
func WithSetup(svc automdm.Service, logger *slog.Logger, applyStarterLibrary func(ctx context.Context, serverURL, token string) error, next http.Handler) http.HandlerFunc {
	rxOsquery := regexp.MustCompile(`^/api/[^/]+/osquery`)
	// Once setup has completed, never re-open it for the life of the process,
	// even if the users table later reads as empty.
	var setupDone atomic.Bool
	return func(w http.ResponseWriter, r *http.Request) {
		if setupDone.Load() {
			next.ServeHTTP(w, r)
			return
		}
		configRouter := http.NewServeMux()
		srv := kithttp.NewServer(
			makeSetupEndpoint(svc, logger, applyStarterLibrary),
			decodeSetupRequest,
			encodeResponse,
		)
		// NOTE: support setup on both /v1/ and version-less, in the future /v1/
		// will be dropped.
		configRouter.Handle("/api/v1/setup", srv)
		configRouter.Handle("/api/setup", srv)

		// whitelist osqueryd endpoints
		if rxOsquery.MatchString(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		requireSetup, err := svc.SetupRequired(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "fetching setup info from db", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if requireSetup {
			configRouter.ServeHTTP(w, r)
			return
		}
		setupDone.Store(true)
		next.ServeHTTP(w, r)
	}
}

// RedirectLoginToSetup detects if the setup endpoint should be used. If setup is required it redirect all
// frontend urls to /setup, otherwise the frontend router is used.
func RedirectLoginToSetup(svc automdm.Service, logger *slog.Logger, next http.Handler, urlPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/setup" {
				next.ServeHTTP(w, r)
				return
			}
			newURL := r.URL
			newURL.Path = urlPrefix + "/setup"
			http.Redirect(w, r, newURL.String(), http.StatusTemporaryRedirect)
		})

		ctx := r.Context()
		setupRequired, err := svc.SetupRequired(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "fetching setupinfo from db", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if setupRequired {
			redirect.ServeHTTP(w, r)
			return
		}
		RedirectSetupToLogin(svc, logger, next, urlPrefix).ServeHTTP(w, r)
	}
}

// RedirectSetupToLogin forces the /setup path to be redirected to login. This middleware is used after
// the app has been setup.
func RedirectSetupToLogin(svc automdm.Service, logger *slog.Logger, next http.Handler, urlPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" {
			newURL := r.URL
			newURL.Path = urlPrefix + "/login"
			http.Redirect(w, r, newURL.String(), http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// RegisterAppleMDMProtocolServices registers the HTTP handlers that serve
// the MDM services to Apple devices.
func RegisterAppleMDMProtocolServices(
	mux *http.ServeMux,
	scepConfig config.MDMConfig,
	mdmStorage automdm.MDMAppleStore,
	scepStorage scep_depot.Depot,
	logger *slog.Logger,
	checkinAndCommandService nanomdm_service.CheckinAndCommandService,
	ddmService nanomdm_service.DeclarativeManagement,
	profileService nanomdm_service.ProfileService,
	getTokenService nanomdm_service.GetToken,
	serverURLPrefix string,
	automdmConfig config.AutoMDMConfig,
	svc automdm.Service,
	ds automdm.Datastore,
) error {
	if err := registerAppleMDMSCEP(mux, scepConfig, scepStorage, ds, logger, automdmConfig, ds); err != nil {
		return fmt.Errorf("scep: %w", err)
	}
	if err := registerMDM(mux, mdmStorage, checkinAndCommandService, ddmService, profileService, getTokenService, logger, automdmConfig, ds); err != nil {
		return fmt.Errorf("mdm: %w", err)
	}
	if err := registerMDMServiceDiscovery(mux, logger, serverURLPrefix, automdmConfig, ds); err != nil {
		return fmt.Errorf("service discovery: %w", err)
	}
	if err := registerPSSO(mux, svc, logger, automdmConfig); err != nil {
		return fmt.Errorf("psso: %w", err)
	}
	return nil
}

func registerMDMServiceDiscovery(
	mux *http.ServeMux,
	logger *slog.Logger,
	serverURLPrefix string,
	automdmConfig config.AutoMDMConfig,
	appCfgGetter automdm.GetsAppConfig,
) error {
	serviceDiscoveryLogger := logger.With("component", "mdm-apple-service-discovery")
	serviceDiscoveryHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appCfg, err := appCfgGetter.AppConfig(r.Context())
		if err != nil {
			serviceDiscoveryLogger.ErrorContext(r.Context(), "error loading app config for service discovery", "err", err)
			return
		} else if appCfg.MDM.OnlyAllowAppleBusinessEnrollment {
			err := &automdm.ABOnlyEnrollmentForbiddenError{}
			http.Error(w, err.Error(), err.StatusCode())
			return
		}

		var mdmEnrollmentURL string
		token := r.PathValue("token")
		if token != "" {
			tokenPath := strings.Replace(apple_mdm.AccountDrivenEnrollTokenPath, "{token}", url.PathEscape(token), 1)
			mdmEnrollmentURL = serverURLPrefix + tokenPath
		} else {
			mdmEnrollmentURL = serverURLPrefix + apple_mdm.AccountDrivenEnrollPath // nolint:staticcheck // deprecated path, kept for backwards compatibility
		}

		ctx := r.Context()
		serviceDiscoveryLogger.InfoContext(ctx, "serving MDM service discovery response", "url", mdmEnrollmentURL)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = fmt.Fprintf(w, `{"Servers":[{"Version": "mdm-byod", "BaseURL": "%s"}]}`, mdmEnrollmentURL)
		if err != nil {
			serviceDiscoveryLogger.ErrorContext(ctx, "error writing service discovery response", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
	mux.Handle(apple_mdm.ServiceDiscoveryTokenPath, otel.WrapHandler(serviceDiscoveryHandler, apple_mdm.ServiceDiscoveryTokenPath, automdmConfig))
	mux.Handle(apple_mdm.ServiceDiscoveryPath, otel.WrapHandler(serviceDiscoveryHandler, apple_mdm.ServiceDiscoveryPath, automdmConfig))
	return nil
}

func registerPSSO(
	mux *http.ServeMux,
	svc automdm.Service,
	logger *slog.Logger,
	automdmConfig config.AutoMDMConfig,
) error {
	// Only the apple-app-site-association document is served from the root
	// *http.ServeMux: Apple's CDN fetches it at a spec-defined /.well-known
	// path that can't live under /api. The rest of the PSSO endpoints are
	// registered on the unauthenticated endpointer in attachAutoMDMAPIRoutes.
	pssoLogger := logger.With("component", "mdm-apple-psso")
	handler := pssoAASAHandler(svc, pssoLogger)
	mux.Handle(pssoAASAPath, otel.WrapHandler(handler, pssoAASAPath, automdmConfig))
	return nil
}

// registerAppleMDMSCEP registers the HTTP handler for SCEP service needed for enrollment to Apple MDM.
// Returns the SCEP CA certificate that can be used by verifiers.
func registerAppleMDMSCEP(
	mux *http.ServeMux,
	scepConfig config.MDMConfig,
	scepStorage scep_depot.Depot,
	mdmStorage challenge.AppleMDMSCEPStore,
	logger *slog.Logger,
	automdmConfig config.AutoMDMConfig,
	appCfgGetter automdm.GetsAppConfig,
) error {
	depotSigner := scep_depot.NewSigner(
		scepStorage,
		scep_depot.WithValidityDays(scepConfig.AppleSCEPSignerValidityDays),
	)

	signer := challenge.AppleMDMChallengeMiddleware(logger.With("component", "mdm-apple-scep"), mdmStorage, scepConfig.AppleSCEPStaticChallengeEnabled, depotSigner)
	scepService := NewSCEPService(
		mdmStorage,
		signer,
		logger.With("component", "mdm-apple-scep"),
	)

	scepSlogLogger := logger.With("component", "http-mdm-apple-scep")
	e := scepserver.MakeServerEndpoints(scepService)
	e.GetEndpoint = scepserver.ACMEOnlyEnrollmentMiddleware(appCfgGetter)(e.GetEndpoint)
	e.PostEndpoint = scepserver.ACMEOnlyEnrollmentMiddleware(appCfgGetter)(e.PostEndpoint)
	e.GetEndpoint = scepserver.EndpointLoggingMiddleware(scepSlogLogger)(e.GetEndpoint)
	e.PostEndpoint = scepserver.EndpointLoggingMiddleware(scepSlogLogger)(e.PostEndpoint)

	scepHandler := scepserver.MakeHTTPHandler(e, scepService, scepSlogLogger)
	mux.Handle(apple_mdm.SCEPPath, otel.WrapHandler(scepHandler, apple_mdm.SCEPPath, automdmConfig))
	return nil
}

func RegisterSCEPProxy(
	rootMux *http.ServeMux,
	ds automdm.Datastore,
	logger *slog.Logger,
	timeout *time.Duration,
	automdmConfig *config.AutoMDMConfig,
) error {
	if automdmConfig == nil {
		return errors.New("automdm config is nil")
	}
	scepService := scep.NewSCEPProxyService(
		ds,
		logger.With("component", "scep-proxy-service"),
		timeout,
	)
	scepProxySlogLogger := logger.With("component", "http-scep-proxy")
	e := scepserver.MakeServerEndpointsWithIdentifier(scepService)
	e.GetEndpoint = scepserver.EndpointLoggingMiddleware(scepProxySlogLogger)(e.GetEndpoint)
	e.PostEndpoint = scepserver.EndpointLoggingMiddleware(scepProxySlogLogger)(e.PostEndpoint)
	scepHandler := scepserver.MakeHTTPHandlerWithIdentifier(e, apple_mdm.SCEPProxyPath, scepProxySlogLogger)
	// Not using OTEL dynamic wrapper so as not to expose {identifier} in the span name
	scepHandler = otel.WrapHandler(scepHandler, apple_mdm.SCEPProxyPath, *automdmConfig)
	rootMux.Handle(apple_mdm.SCEPProxyPath, scepHandler)
	return nil
}

// NanoMDMLogger is a logger adapter for nanomdm.
type NanoMDMLogger struct {
	logger *slog.Logger
}

func NewNanoMDMLogger(logger *slog.Logger) *NanoMDMLogger {
	return &NanoMDMLogger{
		logger: logger,
	}
}

func (l *NanoMDMLogger) Info(keyvals ...interface{}) {
	l.logger.InfoContext(context.TODO(), "", keyvals...)
}

func (l *NanoMDMLogger) Debug(keyvals ...interface{}) {
	l.logger.DebugContext(context.TODO(), "", keyvals...)
}

func (l *NanoMDMLogger) With(keyvals ...interface{}) nanomdm_log.Logger {
	return &NanoMDMLogger{
		logger: l.logger.With(keyvals...),
	}
}

// registerMDM registers the HTTP handlers that serve core MDM services (like checking in for MDM commands).
func registerMDM(
	mux *http.ServeMux,
	mdmStorage automdm.MDMAppleStore,
	checkinAndCommandService nanomdm_service.CheckinAndCommandService,
	ddmService nanomdm_service.DeclarativeManagement,
	profileService nanomdm_service.ProfileService,
	getTokenService nanomdm_service.GetToken,
	logger *slog.Logger,
	automdmConfig config.AutoMDMConfig,
	ds automdm.Datastore,
) error {
	certVerifier := mdmcrypto.NewSCEPVerifier(mdmStorage)
	mdmLogger := NewNanoMDMLogger(logger.With("component", "http-mdm-apple-mdm"))

	// As usual, handlers are applied from bottom to top:
	// 1. Extract and verify MDM signature.
	// 2. Verify signer certificate with CA.
	// 3. Verify new or enrolled certificate (certauth.CertAuth which wraps the MDM service).
	// 4. Pass a copy of the request to AutoMDM middleware that ingests new hosts from pending MDM
	// enrollments and updates the AutoMDM hosts table accordingly with the UDID and serial number of
	// the device.
	// 5. Run actual MDM service operation (checkin handler or command and results handler).
	coreMDMService := nanomdm.New(mdmStorage, nanomdm.WithLogger(mdmLogger), nanomdm.WithDeclarativeManagement(ddmService),
		nanomdm.WithProfileService(profileService), nanomdm.WithUserAuthenticate(checkinAndCommandService),
		nanomdm.WithGetToken(getTokenService))

	// NOTE: it is critical that the coreMDMService runs first, as the first
	// service in the multi-service feature is run to completion _before_ running
	// the other ones in parallel. This way, subsequent services have access to
	// the result of the core service, e.g. the device is enrolled, etc.
	var mdmService nanomdm_service.CheckinAndCommandService = multi.New(mdmLogger, coreMDMService, checkinAndCommandService)

	mdmService = certauth.New(mdmService, mdmStorage, certauth.WithLogger(mdmLogger.With("handler", "cert-auth")))
	// Wraps the whole chain above (not a multi.New sub-service) so a rejection here is what
	// the HTTP handler actually returns to the device. See abOnlyEnrollmentCheckinService's
	// doc comment for why this can't live inside checkinAndCommandService itself.
	mdmService = newABOnlyEnrollmentCheckinService(mdmService, ds, logger.With("component", "http-mdm-apple-mdm", "handler", "ab-only-enrollment"))
	mdmService = newEnrolledSerialCheckinService(mdmService, ds, logger.With("component", "http-mdm-apple-mdm", "handler", "enrolled-serial"))
	mdmService = newCertVerifierEnrollmentCheckinService(mdmService, ds, mdmStorage, automdmConfig.MDM, logger.With("component", "http-mdm-apple-mdm", "handler", "cert-verifier"))
	var mdmHandler http.Handler = httpmdm.CheckinAndCommandHandler(mdmService, mdmLogger.With("handler", "checkin-command"))
	verifyDisable, exists := os.LookupEnv("AUTOMDM_MDM_APPLE_SCEP_VERIFY_DISABLE")
	if exists && (strings.EqualFold(verifyDisable, "true") || verifyDisable == "1") {
		logger.InfoContext(context.TODO(),
			"disabling verification of macOS SCEP certificates as AUTOMDM_MDM_APPLE_SCEP_VERIFY_DISABLE is set to true")
	} else {
		mdmHandler = httpmdm.CertVerifyMiddleware(mdmHandler, certVerifier, mdmLogger.With("handler", "cert-verify"))
	}
	mdmHandler = httpmdm.CertExtractMdmSignatureMiddleware(mdmHandler, httpmdm.MdmSignatureVerifierFunc(cryptoutil.VerifyMdmSignature),
		httpmdm.SigLogWithLogger(mdmLogger.With("handler", "cert-extract")))
	// Bound the request body before any middleware reads it.
	mdmHandler = http.MaxBytesHandler(mdmHandler, automdm.MaxAppleMDMRequestBodySize)
	mux.Handle(apple_mdm.MDMPath, otel.WrapHandler(mdmHandler, apple_mdm.MDMPath, automdmConfig))
	return nil
}

func WithMDMSSOCallbackRedirect(svc automdm.Service, logger *slog.Logger, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/automdm/mdm/sso/callback") {
			next.ServeHTTP(w, r)
			return
		}
		// First check if the cookie is set on the current domain, if it is, just serve as is.
		_, err := r.Cookie(cookieNameSSOSession)
		if err == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Check for custom apple URL set and does not match the current request URL, then do a redirect to the custom URL, where the Cookie is set.
		appCfg, err := svc.AppConfigUrls(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "fetching app config", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		parsedUrl, err := url.Parse(appCfg.MDMUrl())
		if err != nil {
			logger.ErrorContext(r.Context(), "parsing custom Apple MDM URL", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		reqHost := r.Host
		if h, _, err := net.SplitHostPort(reqHost); err == nil {
			reqHost = h
		}

		if !strings.EqualFold(parsedUrl.Hostname(), reqHost) {
			target := *parsedUrl
			target.Path = r.URL.Path
			target.RawQuery = r.URL.RawQuery
			logger.InfoContext(r.Context(), "redirecting to custom Apple MDM URL for SSO callback")
			http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func WithMDMEnrollmentMiddleware(svc automdm.Service, logger *slog.Logger, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mdm/sso" && r.URL.Path != "/account_driven_enroll/sso" {
			// TODO: redirects for non-SSO config web url?
			next.ServeHTTP(w, r)
			return
		}

		// if x-apple-aspen-deviceinfo custom header is present, we need to check for minimum os version
		di := r.Header.Get("x-apple-aspen-deviceinfo")
		if di != "" {
			parsed, p7, err := apple_mdm.ParseDeviceinfo(di)
			if err != nil {
				if apple_mdm.MachineInfoVerificationEnabled() {
					// a present-but-unparseable header can't be verified; reject
					// it in enforce mode rather than letting it through
					logger.ErrorContext(r.Context(), "parsing x-apple-aspen-deviceinfo failed, rejecting request", "err", err)
					http.Error(w, "unable to parse deviceinfo", http.StatusForbidden)
					return
				}
				// audit mode: log the error and continue to next
				logger.WarnContext(r.Context(), "parsing x-apple-aspen-deviceinfo failed, continuing because verification is disabled", "err", err)
				next.ServeHTTP(w, r)
				return
			}

			if err := apple_mdm.VerifyMachineInfoSignature(p7); err != nil {
				logAttrs := []any{
					"lane", "mdm_sso",
					"machine_info_verify_reason", err.Error(),
					"serial", parsed.Serial,
					"udid", parsed.UDID,
					"product", parsed.Product,
				}
				if apple_mdm.MachineInfoVerificationEnabled() {
					logger.ErrorContext(r.Context(), "x-apple-aspen-deviceinfo signature verification failed, rejecting request", logAttrs...)
					http.Error(w, "unable to verify deviceinfo signature", http.StatusForbidden)
					return
				}
				logger.WarnContext(r.Context(), "x-apple-aspen-deviceinfo signature verification failed, continuing because verification is disabled", logAttrs...)
			}

			// TODO: skip os version check if deviceinfo query param is present? or find another way
			// to avoid polling the DB and Apple endpoint twice for each enrollment.

			sur, err := svc.CheckMDMAppleEnrollmentWithMinimumOSVersion(r.Context(), parsed)
			if err != nil {
				// just log the error and continue to next
				logger.ErrorContext(r.Context(), "checking minimum os version for mdm", "err", err)
				next.ServeHTTP(w, r)
				return
			}

			if sur != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				if err := json.NewEncoder(w).Encode(sur); err != nil {
					logger.ErrorContext(r.Context(), "failed to encode software update required", "err", err)
					http.Redirect(w, r, r.URL.String()+"?error=true", http.StatusSeeOther)
				}
				return
			}

			// TODO: Do non-Apple devices ever use this route? If so, we probably need to change the
			// approach below so we don't endlessly redirect non-Apple clients to the same URL.

			// if we get here, the minimum os version is satisfied, so we continue with SSO flow
			q := r.URL.Query()
			v, ok := q["deviceinfo"]
			if !ok || len(v) == 0 {
				// If the deviceinfo query param is empty, we add the deviceinfo to the URL and
				// redirect.
				//
				// Note: We'll apply this redirect only if query params are empty because want to
				// redirect to the same URL with added query params after parsing the x-apple-aspen-deviceinfo
				// header. Whenever we see a request with any query params already present, we'll
				// skip this step and just continue to the next handler.
				newURL := *r.URL
				q.Set("deviceinfo", di)
				newURL.RawQuery = q.Encode()
				logger.InfoContext(r.Context(), "handling mdm sso: redirect with deviceinfo", "host_uuid", parsed.UDID, "serial", parsed.Serial)
				http.Redirect(w, r, newURL.String(), http.StatusTemporaryRedirect)
				return
			}
			if len(v) > 0 && v[0] != di {
				// something is wrong, the device info in the query params does not match
				// the one in the header, so we just log the error and continue to next
				logger.ErrorContext(r.Context(), "device info in query params does not match header", "header", di, "query", v[0])
			}
			logger.InfoContext(r.Context(), "handling mdm sso: proceed to next", "host_uuid", parsed.UDID, "serial", parsed.Serial)
		}

		next.ServeHTTP(w, r)
	}
}

// uploadPreAuth rejects package uploads without a package write role before the
// body is read, and only then lifts the read deadline for large uploads.
func uploadPreAuth(svc automdm.Service, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reject := func(err error) {
				logger.WarnContext(ctx, "package upload rejected before body read", "path", r.URL.Path, "remote_addr", r.RemoteAddr, "err", err)
				encodeError(ctx, err, w)
			}
			if r.Header.Get("Content-Encoding") != "" {
				reject(automdm.NewUserMessageError(errors.New("unsupported Content-Encoding"), http.StatusUnsupportedMediaType))
				return
			}
			bearer := token.FromHTTPRequest(r)
			if bearer == "" {
				reject(automdm.NewAuthHeaderRequiredError("no auth token"))
				return
			}
			v, err := auth.AuthViewer(ctx, string(bearer), svc)
			if err != nil {
				reject(err)
				return
			}
			// Runs the forced password reset and API-only checks that follow decode, in the same order.
			probeCtx := viewer.NewContext(kithttp.PopulateRequestContext(ctx, r), *v)
			noop := func(context.Context, any) (any, error) { return nil, nil }
			if _, err := auth.AuthenticatedUser(svc, auth.APIOnlyEndpointCheck(noop))(probeCtx, nil); err != nil {
				reject(err)
				return
			}
			// Mirrors the package write rules in policy.rego, pinned by TestUploadPreAuthMatchesAuthz.
			// team_id arrives after the file in the body, so only a role on some team is checkable here.
			canWrite := func(role string) bool {
				return role == automdm.RoleAdmin || role == automdm.RoleMaintainer || role == automdm.RoleGitOps
			}
			if (v.User.GlobalRole == nil || !canWrite(*v.User.GlobalRole)) &&
				!slices.ContainsFunc(v.User.Teams, func(t automdm.UserTeam) bool { return canWrite(t.Role) }) {
				reject(authz.ForbiddenWithInternal("no package write role on any team", nil, nil, automdm.ActionWrite))
				return
			}
			if err := http.NewResponseController(w).SetReadDeadline(time.Time{}); err != nil {
				logger.ErrorContext(ctx, "failed to remove read deadline for package upload", "err", err)
			}
			next.ServeHTTP(w, r)
		})
	}
}

const automdmctl4NotSupportedMessage = "AutoMDM 5.x isn't compatible with automdmctl 4.x. Upgrade automdmctl."

// automdmctl4PreAuth refuses the version check that automdmctl 4 makes before anything else, so that it stops with an upgrade error. It
// runs before auth so that an expired token gets this error too.
func automdmctl4PreAuth(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAutoMDMctl4Request(r) {
				next.ServeHTTP(w, r)
				return
			}
			logger.WarnContext(r.Context(), "refused automdmctl 4 request", "uri", r.URL.Path, "user_agent", r.UserAgent(),
				"ip_addr", r.RemoteAddr, "x_for_ip_addr", r.Header.Get("X-Forwarded-For"))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUpgradeRequired)
			// No errors list: automdmctl 4 prints the message followed by the first error's reason, which would repeat it.
			_ = json.NewEncoder(w).Encode(endpointer.JsonError{Message: automdmctl4NotSupportedMessage})
		})
	}
}

// isAutoMDMctl4Request reports whether r looks like it comes from automdmctl 4, which doesn't send its version. It sends Go's default
// User-Agent and JSON Content-Type and Accept headers even on a GET, which other Go clients rarely do. automdmctl 5 sends its version,
// and admins can set its User-Agent to Go's default with custom headers (to get past a WAF, for example).
func isAutoMDMctl4Request(r *http.Request) bool {
	if r.Header.Get(automdm.HeaderClientVersion) != "" {
		return false
	}
	return strings.HasPrefix(r.UserAgent(), "Go-http-client/") &&
		r.Header.Get("Content-Type") == "application/json" &&
		r.Header.Get("Accept") == "application/json"
}
