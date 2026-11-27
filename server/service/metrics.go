package service

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/go-kit/kit/metrics"
)

type metricsMiddleware struct {
	automdm.Service
	requestCount   metrics.Counter
	requestLatency metrics.Histogram
}

// NewMetricsService service takes an existing service and wraps it
// with instrumentation middleware.
func NewMetricsService(
	svc automdm.Service,
	requestCount metrics.Counter,
	requestLatency metrics.Histogram,
) automdm.Service {
	return metricsMiddleware{
		Service:        svc,
		requestCount:   requestCount,
		requestLatency: requestLatency,
	}
}
