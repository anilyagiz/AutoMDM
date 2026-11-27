package service

import "github.com/anilyagiz/automdm/v4/server/automdm"

// GetCertificateAuthoritiesSpec fetches the certificate authorities stored on the server
func (c *Client) GetCertificateAuthoritiesSpec(includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
	verb, path := "GET", "/api/latest/automdm/spec/certificate_authorities"
	var responseBody getCertificateAuthoritiesSpecResponse
	query := ""
	if includeSecrets {
		query = "include_secrets=true"
	}
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	return responseBody.CertificateAuthorities, err
}

// ApplyCertificateAuthoritiesSpec applies the certificate authorities.
func (c *Client) ApplyCertificateAuthoritiesSpec(groupedCAs automdm.GroupedCertificateAuthorities, specOpts automdm.ApplySpecOptions, opts automdm.BatchApplyCertificateAuthoritiesOpts) error {
	req := batchApplyCertificateAuthoritiesRequest{
		CertificateAuthorities: groupedCAs,
		DryRun:                 specOpts.DryRun,
		SkipDeletes:            opts.SkipDeletes,
	}
	verb, path := "POST", "/api/latest/automdm/spec/certificate_authorities"
	var responseBody batchApplyCertificateAuthoritiesResponse
	return c.authenticatedRequestWithQuery(req, verb, path, &responseBody, specOpts.RawQuery())
}

// GetCertificateAuthorities fetches the list of certificate authorities
func (c *Client) GetCertificateAuthorities() ([]*automdm.CertificateAuthoritySummary, error) {
	verb, path := "GET", "/api/latest/automdm/certificate_authorities"
	var responseBody listCertificateAuthoritiesResponse
	err := c.authenticatedRequest(nil, verb, path, &responseBody)
	return responseBody.CertificateAuthorities, err
}
