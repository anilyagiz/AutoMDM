package service

import (
	"net/http"

	"github.com/anilyagiz/automdm/v4/server/platform/tracing"
)

// RegisterTracingTiers populates the trace sampling registry with the tier classifications for routes owned by this package
// (the legacy flat server/service handler).
//
// Bounded contexts that have moved out of this package (e.g. server/activity/) own their own registrations. They
// should expose a RegisterTracingTiers function the serve command calls during startup. The platform/tracing package itself
// stays free of route knowledge.
//
// Paths use "_version_" as the placeholder for the route template's automdmversion segment. The sampler normalizes incoming span
// names back to that form before lookup. Alternate path forms (e.g. /api/v1/osquery/...) are registered separately.
//
// Unregistered routes (including all cron jobs, enroll, SCEP, MDM checkin, command ack/result, GitOps batch, etc.) fall to
// TierAlways by design.
func RegisterTracingTiers(registry *tracing.Registry) {
	// Hot agent endpoints. These dominate request volume at scale (tens of thousands of spans per second on a 100k-host
	// team) without being individually interesting. Sampled at the configured high volume ratio (default 0.1%).
	registry.Register(http.MethodPost, "/api/osquery/config", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/v1/osquery/config", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/osquery/distributed/read", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/v1/osquery/distributed/read", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/osquery/distributed/write", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/v1/osquery/distributed/write", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/osquery/log", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/v1/osquery/log", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/automdm/orbit/config", tracing.TierHighVolume)
	registry.Register(http.MethodPost, "/api/automdm/orbit/device_token", tracing.TierHighVolume)
	registry.Register(http.MethodHead, "/api/automdm/orbit/ping", tracing.TierHighVolume)
	registry.Register(http.MethodHead, "/api/automdm/device/ping", tracing.TierHighVolume)
	registry.Register(http.MethodHead, "/api/_version_/automdm/device/{token}/ping", tracing.TierHighVolume)
	registry.Register(http.MethodGet, "/api/_version_/automdm/device/{token}/desktop", tracing.TierHighVolume)

	// Admin / read endpoints. Moderate volume, moderate diagnostic value. Sampled at the configured standard ratio (default
	// 2%). Starting set covers the highest traffic admin reads plus the per page load endpoints (config, me, version, etc.)
	// that the UI hits on every navigation. Expand as we observe traffic patterns in dogfood and the customer pilot.
	registry.Register(http.MethodGet, "/api/_version_/automdm/config", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/teams", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/host_summary", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/hosts", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/hosts/{id}", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/hosts/count", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/hosts/summary/mdm", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/labels", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/me", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/policies", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/reports", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/software/titles", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/software/versions", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/spec/enroll_secret", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/users", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/version", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/charts/{metric}", tracing.TierStandard)
	registry.Register(http.MethodGet, "/api/_version_/automdm/android_enterprise", tracing.TierStandard)
}
