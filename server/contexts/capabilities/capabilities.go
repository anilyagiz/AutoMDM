package capabilities

import (
	"context"
	"net/http"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type key int

const capabilitiesKey key = 0

// NewContext creates a new context with the given capabilities.
func NewContext(ctx context.Context, r *http.Request) context.Context {
	capabilities := automdm.CapabilityMap{}
	capabilities.PopulateFromString(r.Header.Get(automdm.CapabilitiesHeader))
	return context.WithValue(ctx, capabilitiesKey, capabilities)
}

// FromContext returns the capabilities in the request if present.
func FromContext(ctx context.Context) (automdm.CapabilityMap, bool) {
	v, ok := ctx.Value(capabilitiesKey).(automdm.CapabilityMap)
	return v, ok
}
