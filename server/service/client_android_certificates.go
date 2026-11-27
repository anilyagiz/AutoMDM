package service

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// GetCertificateTemplates retrieves the list of Certificate Templates for a team.
func (c *Client) GetCertificateTemplates(teamID string) ([]*automdm.CertificateTemplateResponseSummary, error) {
	verb, path := "GET", "/api/latest/automdm/certificates"
	var responseBody listCertificateTemplatesResponse
	query := "team_id=" + teamID
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	if err != nil {
		return nil, err
	}
	return responseBody.Certificates, nil
}

// ApplyCertificateSpecs sends a list of certificate specs to the automdm instance to be added/updated.
func (c *Client) ApplyCertificateSpecs(specs []*automdm.CertificateRequestSpec) error {
	req := applyCertificateTemplateSpecsRequest{Specs: specs}
	verb, path := "POST", "/api/latest/automdm/spec/certificates"
	var responseBody applyCertificateTemplateSpecsResponse
	return c.authenticatedRequest(req, verb, path, &responseBody)
}

// DeleteCertificateTemplates sends a list of certificate template IDs to be deleted.
func (c *Client) DeleteCertificateTemplates(certificateTemplateIDs []uint, teamID uint) error {
	verb, path := "DELETE", "/api/latest/automdm/spec/certificates"
	req := deleteCertificateTemplateSpecsRequest{IDs: certificateTemplateIDs, TeamID: teamID}
	var responseBody deleteCertificateTemplateSpecsResponse
	return c.authenticatedRequest(req, verb, path, &responseBody)
}
