package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

var eeValidVulnSortColumns = []string{
	"cve",
	"hosts_count",
	"created_at",
	"cvss_score",
	"epss_probability",
	"cve_published",
}

func (svc *Service) ListVulnerabilities(ctx context.Context, opt automdm.VulnListOptions) ([]automdm.VulnerabilityWithMetadata, *automdm.PaginationMetadata, error) {
	opt.ValidSortColumns = eeValidVulnSortColumns
	opt.IsEE = true
	return svc.Service.ListVulnerabilities(ctx, opt)
}

func (svc *Service) Vulnerability(ctx context.Context, cve string, teamID *uint, useCVSScores bool) (vuln *automdm.VulnerabilityWithMetadata,
	known bool, err error) {
	return svc.Service.Vulnerability(ctx, cve, teamID, true)
}
