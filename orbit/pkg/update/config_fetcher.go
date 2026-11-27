package update

import "github.com/anilyagiz/automdm/v4/server/automdm"

// OrbitConfigFetcher allows fetching Orbit configuration.
type OrbitConfigFetcher interface {
	// GetConfig returns the Orbit configuration.
	GetConfig() (*automdm.OrbitConfig, error)
}
