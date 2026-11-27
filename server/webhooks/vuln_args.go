package webhooks

import (
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type VulnArgs struct {
	Vulnerablities []automdm.SoftwareVulnerability
	Meta           map[string]automdm.CVEMeta
	AppConfig      *automdm.AppConfig
	Time           time.Time
}
