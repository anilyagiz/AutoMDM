package service

import (
	"fmt"
	"net/url"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// ApplyQueries sends the list of Queries to be applied (upserted) to the
// AutoMDM instance.
func (c *Client) ApplyQueries(specs []*automdm.QuerySpec) error {
	req := automdm.ApplyQuerySpecsRequest{Specs: specs}
	verb, path := "POST", "/api/latest/automdm/spec/reports"
	var responseBody automdm.ApplyQuerySpecsResponse
	return c.authenticatedRequest(req, verb, path, &responseBody)
}

// GetQuerySpec returns the query spec of a query by its team+name.
func (c *Client) GetQuerySpec(teamID *uint, name string) (*automdm.QuerySpec, error) {
	verb, path := "GET", "/api/latest/automdm/spec/reports/"+url.PathEscape(name)
	query := url.Values{}
	if teamID != nil {
		query.Set("team_id", fmt.Sprint(*teamID))
	}
	var responseBody automdm.GetQuerySpecResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query.Encode())
	return responseBody.Spec, err
}

// GetQueries retrieves the list of all Queries.
func (c *Client) GetQueries(teamID *uint, name *string) ([]automdm.Query, error) {
	verb, path := "GET", "/api/latest/automdm/reports"
	query := url.Values{}
	if teamID != nil {
		query.Set("team_id", fmt.Sprint(*teamID))
	}
	if name != nil {
		query.Set("query", *name)
	}
	var responseBody automdm.ListQueriesResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query.Encode())
	if err != nil {
		return nil, err
	}
	return responseBody.Queries, nil
}

// DeleteQueries deletes several queries.
func (c *Client) DeleteQueries(ids []uint) error {
	req := automdm.DeleteQueriesRequest{IDs: ids}
	verb, path := "POST", "/api/latest/automdm/reports/delete"
	var responseBody automdm.DeleteQueriesResponse
	return c.authenticatedRequest(req, verb, path, &responseBody)
}
