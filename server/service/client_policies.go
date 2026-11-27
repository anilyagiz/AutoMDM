package service

import (
	"net/url"
	"strconv"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func (c *Client) CreateGlobalPolicy(name, query, description, resolution, platform string) error {
	req := automdm.CreatePolicyRequest{
		Name:        name,
		Query:       query,
		Description: description,
		Resolution:  resolution,
		Platform:    platform,
	}
	var responseBody automdm.CreatePolicyResponse
	return c.authenticatedRequest(req, "POST", "/api/latest/automdm/policies", &responseBody)
}

// ApplyPolicies sends the list of Policies to be applied to the
// AutoMDM instance.
func (c *Client) ApplyPolicies(specs []*automdm.PolicySpec) error {
	req := automdm.ApplyPolicySpecsRequest{Specs: specs}
	verb, path := "POST", "/api/latest/automdm/spec/policies"
	var responseBody automdm.ApplyPolicySpecsResponse
	return c.authenticatedRequest(req, verb, path, &responseBody)
}

// GetPolicies retrieves the policies of a scope: "All teams" when teamID is nil, "Unassigned" when it's 0, and a team
// otherwise. Inherited policies are excluded.
func (c *Client) GetPolicies(teamID *uint) ([]*automdm.Policy, error) {
	var query string
	if teamID != nil {
		query = url.Values{"team_id": {strconv.FormatUint(uint64(*teamID), 10)}}.Encode()
	}
	var responseBody automdm.ListPoliciesResponse
	if err := c.authenticatedRequestWithQuery(nil, "GET", "/api/latest/automdm/policies", &responseBody, query); err != nil {
		return nil, err
	}
	return responseBody.Policies, nil
}

// DeletePolicies deletes several policies, which can be in any scope.
func (c *Client) DeletePolicies(ids []uint) error {
	var responseBody automdm.DeletePoliciesResponse
	return c.authenticatedRequest(automdm.DeletePoliciesRequest{IDs: ids}, "POST", "/api/latest/automdm/policies/delete", &responseBody)
}
