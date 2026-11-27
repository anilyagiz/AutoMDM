package service

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// SearchTargets searches for the supplied targets in the AutoMDM instance.
func (c *Client) SearchTargets(query string, hostIDs, labelIDs []uint) (*automdm.TargetSearchResults, error) {
	req := automdm.SearchTargetsRequest{
		MatchQuery: query,
		Selected: automdm.HostTargets{
			LabelIDs: labelIDs,
			HostIDs:  hostIDs,
			// TODO handle TeamIDs
		},
	}
	verb, path := "POST", "/api/latest/automdm/targets"
	var responseBody automdm.SearchTargetsResponse
	err := c.authenticatedRequest(req, verb, path, &responseBody)
	if err != nil {
		return nil, err
	}

	hosts := make([]*automdm.Host, len(responseBody.Targets.Hosts))
	for i, h := range responseBody.Targets.Hosts {
		hosts[i] = h.Host
	}

	labels := make([]*automdm.Label, len(responseBody.Targets.Labels))
	for i, h := range responseBody.Targets.Labels {
		labels[i] = h.Label
	}

	return &automdm.TargetSearchResults{
		Hosts:  hosts,
		Labels: labels,
	}, nil
}
