package service

import (
	"fmt"
	"net/url"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// ApplyLabels sends the list of Labels to be applied (upserted) to the
// AutoMDM instance. Use teamID = nil for global labels.
func (c *Client) ApplyLabels(
	specs []*automdm.LabelSpec,
	teamID *uint,
	moves []automdm.LabelToMove,
) error {
	req := automdm.ApplyLabelSpecsRequest{TeamID: teamID, Specs: specs, LabelsToMove: moves}
	verb, path := "POST", "/api/latest/automdm/spec/labels"
	var responseBody automdm.ApplyLabelSpecsResponse

	if teamID != nil {
		return c.authenticatedRequestWithQuery(
			req,
			verb,
			path,
			&responseBody,
			fmt.Sprintf("team_id=%d", *teamID),
		)
	}
	return c.authenticatedRequest(req, verb, path, &responseBody)
}

// GetLabel retrieves information about a label by name
func (c *Client) GetLabel(name string, automdmID *uint) (*automdm.LabelSpec, error) {
	verb, path := "GET", "/api/latest/automdm/spec/labels/"+url.PathEscape(name)
	var responseBody automdm.GetLabelSpecResponse
	var query string
	if automdmID != nil {
		query = fmt.Sprintf("team_id=%d", *automdmID)
	}
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	return responseBody.Spec, err
}

// GetLabels retrieves the list of all LabelSpecs.
func (c *Client) GetLabels(teamID uint) ([]*automdm.LabelSpec, error) {
	verb, path := "GET", "/api/latest/automdm/spec/labels"
	var responseBody automdm.GetLabelSpecsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, fmt.Sprintf("team_id=%d", teamID))
	return responseBody.Specs, err
}

// DeleteLabelByID deletes the label with the given ID.
func (c *Client) DeleteLabelByID(id uint) error {
	verb, path := "DELETE", fmt.Sprintf("/api/latest/automdm/labels/id/%d", id)
	var responseBody automdm.DeleteLabelByIDResponse
	return c.authenticatedRequest(nil, verb, path, &responseBody)
}
