package service

import (
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (c *Client) SaveCustomHostVitals(customHostVitals []automdm.CustomHostVital, dryRun bool) error {
	verb, path := "PUT", "/api/latest/automdm/spec/custom_host_vitals"
	params := automdm.UpsertCustomHostVitalsRequest{
		CustomHostVitals: customHostVitals,
		DryRun:           dryRun,
	}
	var responseBody automdm.UpsertCustomHostVitalsResponse
	return c.authenticatedRequest(params, verb, path, &responseBody)
}

// ListCustomHostVitals returns a page of custom host vital definitions.
func (c *Client) ListCustomHostVitals(query string) ([]automdm.CustomHostVital, error) {
	verb, path := "GET", "/api/latest/automdm/custom_host_vitals"
	var responseBody automdm.ListCustomHostVitalsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	if err != nil {
		return nil, err
	}
	return responseBody.CustomHostVitals, nil
}

// listAllCustomHostVitals pages through ListCustomHostVitals to return every definition.
func (c *Client) listAllCustomHostVitals() ([]automdm.CustomHostVital, error) {
	const perPage = 1000
	var all []automdm.CustomHostVital
	for page := 0; ; page++ {
		pageVitals, err := c.ListCustomHostVitals(fmt.Sprintf("per_page=%d&page=%d", perPage, page))
		if err != nil {
			return nil, err
		}
		all = append(all, pageVitals...)
		if len(pageVitals) < perPage {
			return all, nil
		}
	}
}
