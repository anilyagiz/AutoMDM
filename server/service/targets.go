package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

////////////////////////////////////////////////////////////////////////////////
// Search Targets
////////////////////////////////////////////////////////////////////////////////

func searchTargetsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.SearchTargetsRequest)

	results, err := svc.SearchTargets(ctx, req.MatchQuery, req.QueryID, req.Selected)
	if err != nil {
		return automdm.SearchTargetsResponse{Err: err}, nil
	}

	targets := &automdm.TargetsData{
		Hosts:  []*automdm.HostResponse{},
		Labels: []automdm.LabelSearchResult{},
		Teams:  []automdm.TeamSearchResult{},
	}

	for _, host := range results.Hosts {
		targets.Hosts = append(targets.Hosts, automdm.HostResponseForHostCheap(host))
	}

	for _, label := range results.Labels {
		targets.Labels = append(targets.Labels,
			automdm.LabelSearchResult{
				Label:       label,
				DisplayText: label.Name,
				Count:       label.HostCount,
			},
		)
	}

	for _, team := range results.Teams {
		targets.Teams = append(targets.Teams,
			automdm.TeamSearchResult{
				Team:        team,
				DisplayText: team.Name,
				Count:       team.HostCount,
			},
		)
	}

	metrics, err := svc.CountHostsInTargets(ctx, req.QueryID, req.Selected)
	if err != nil {
		return automdm.SearchTargetsResponse{Err: err}, nil
	}

	return automdm.SearchTargetsResponse{
		Targets:                targets,
		TargetsCount:           metrics.TotalHosts,
		TargetsOnline:          metrics.OnlineHosts,
		TargetsOffline:         metrics.OfflineHosts,
		TargetsMissingInAction: metrics.MissingInActionHosts,
	}, nil
}

func (svc *Service) SearchTargets(ctx context.Context, matchQuery string, queryID *uint, targets automdm.HostTargets) (*automdm.TargetSearchResults, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Target{}, automdm.ActionRead); err != nil {
		return nil, err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, automdm.ErrNoContext
	}

	filter := automdm.TeamFilter{User: vc.User}
	if queryID != nil {
		query, err := svc.ds.Query(ctx, *queryID)
		if err != nil {
			return nil, err
		}
		filter.IncludeObserver = query.ObserverCanRun
		filter.ObserverTeamID = query.TeamID
	}

	results := &automdm.TargetSearchResults{}

	hosts, err := svc.ds.SearchHosts(ctx, filter, matchQuery, true, targets.HostIDs...)
	if err != nil {
		return nil, err
	}

	results.Hosts = append(results.Hosts, hosts...)

	labels, err := svc.ds.SearchLabels(ctx, filter, matchQuery, targets.LabelIDs...)
	if err != nil {
		return nil, err
	}
	results.Labels = labels

	teams, err := svc.ds.SearchTeams(ctx, filter, matchQuery, targets.TeamIDs...)
	if err != nil {
		return nil, err
	}

	// Strip unnecessary info from target search results.
	// The target picker only needs team ID, name, and host count.
	for _, t := range teams {
		t.Secrets = nil
		t.Config.AgentOptions = nil
	}

	results.Teams = teams

	return results, nil
}

func (svc *Service) CountHostsInTargets(ctx context.Context, queryID *uint, targets automdm.HostTargets) (*automdm.TargetMetrics, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Target{}, automdm.ActionRead); err != nil {
		return nil, err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, automdm.ErrNoContext
	}

	filter := automdm.TeamFilter{User: vc.User}
	if queryID != nil {
		query, err := svc.ds.Query(ctx, *queryID)
		if err != nil {
			return nil, err
		}
		filter.IncludeObserver = query.ObserverCanRun
		filter.ObserverTeamID = query.TeamID
	}

	metrics, err := svc.ds.CountHostsInTargets(ctx, filter, targets, svc.clock.Now())
	if err != nil {
		return nil, err
	}

	return &metrics, nil
}

func countTargetsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.CountTargetsRequest)

	counts, err := svc.CountHostsInTargets(ctx, req.QueryID, req.Selected)
	if err != nil {
		return automdm.CountTargetsResponse{Err: err}, nil
	}

	return automdm.CountTargetsResponse{
		TargetsCount:   counts.TotalHosts,
		TargetsOnline:  counts.OnlineHosts,
		TargetsOffline: counts.OfflineHosts,
	}, nil
}
