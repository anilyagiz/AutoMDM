package webhooks

import (
	"net/url"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	automdmwebhooks "github.com/anilyagiz/automdm/v4/server/webhooks"
)

type Mapper struct {
	automdmwebhooks.Mapper
}

func NewMapper() automdmwebhooks.VulnMapper {
	return &Mapper{}
}

func (m *Mapper) GetPayload(
	hostBaseURL *url.URL,
	hosts []automdm.HostVulnerabilitySummary,
	cve string,
	meta automdm.CVEMeta,
) automdmwebhooks.WebhookPayload {
	r := m.Mapper.GetPayload(hostBaseURL,
		hosts,
		cve,
		meta,
	)
	r.EPSSProbability = meta.EPSSProbability
	r.CVSSScore = meta.CVSSScore
	r.CISAKnownExploit = meta.CISAKnownExploit
	r.CVEPublished = meta.Published
	return r
}
