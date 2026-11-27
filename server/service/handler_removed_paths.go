package service

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
)

// removedPathTemplates are the paths that AutoMDM 5 removed, as they appear in pkg/removednames. A request to one gets 410 Gone with the
// message of its table entry, for every method, or only for the entry's Context when it names one. A path moves here from
// its own route in the change that stops serving it. A path that a later template also matches
// (/mdm/profiles/summary and /mdm/profiles/{profile_uuid}) must come first to get its own message. A path variable can add a regex
// constraint that the table entry leaves out.
var removedPathTemplates = []string{
	"/api/_version_/automdm/spec/teams",
	"/api/_version_/automdm/teams",
	"/api/_version_/automdm/teams/{id}",
	"/api/_version_/automdm/teams/{id}/agent_options",
	"/api/_version_/automdm/teams/{id}/secrets",
	"/api/_version_/automdm/teams/{id}/users",
	"/api/_version_/automdm/team/{team_id}/policies",
	"/api/_version_/automdm/team/{team_id}/policies/count",
	"/api/_version_/automdm/team/{team_id}/policies/delete",
	"/api/_version_/automdm/team/{team_id}/policies/{policy_id}",
	"/api/_version_/automdm/team/{team_id}/schedule",
	"/api/_version_/automdm/team/{team_id}/schedule/{report_id}",
	"/api/_version_/automdm/teams/{team_id}/policies",
	"/api/_version_/automdm/teams/{team_id}/policies/count",
	"/api/_version_/automdm/teams/{team_id}/policies/delete",
	"/api/_version_/automdm/teams/{team_id}/policies/{policy_id}",
	"/api/_version_/automdm/teams/{team_id}/schedule",
	"/api/_version_/automdm/teams/{team_id}/schedule/{report_id}",
	"/api/_version_/automdm/global/policies",
	"/api/_version_/automdm/global/policies/delete",
	"/api/_version_/automdm/global/policies/{policy_id}",
	"/api/_version_/automdm/teams/{team_id}/policies",
	"/api/_version_/automdm/teams/{team_id}/policies/count",
	"/api/_version_/automdm/teams/{team_id}/policies/delete",
	"/api/_version_/automdm/teams/{team_id}/policies/{policy_id}",
	"/api/_version_/automdm/mdm/apple/profiles/preassign",
	"/api/_version_/automdm/mdm/apple/profiles/match",
	"/api/_version_/automdm/hosts/{host_id}/configuration_profiles/resend/{profile_uuid}",
	"/api/_version_/automdm/abm",
	"/api/_version_/automdm/abm_tokens",
	"/api/_version_/automdm/abm_tokens/count",
	"/api/_version_/automdm/abm_tokens/{id}",
	"/api/_version_/automdm/abm_tokens/{id}/renew",
	"/api/_version_/automdm/ab_tokens/{id}/teams",
	"/api/_version_/automdm/abm_tokens/{id}/teams",
	"/api/_version_/automdm/abm_tokens/{id}/teams",
	"/api/_version_/automdm/vpp_tokens/{id}/teams",
	"/api/_version_/automdm/mdm/apple_bm",
	"/api/_version_/automdm/mdm/apple/abm_public_key",
	"/api/_version_/automdm/scripts/batch/summary/{batch_execution_id}",
	"/api/_version_/automdm/users/roles/spec",
	"/api/_version_/automdm/software",
	"/api/_version_/automdm/software/count",
	"/api/_version_/automdm/software/{id:[0-9]+}",
	"/api/_version_/automdm/hosts/identifier/{identifier}",
	"/api/_version_/automdm/hosts/identifier/{identifier}/query",
	"/api/_version_/automdm/queries/{id}",
	"/api/_version_/automdm/queries",
	"/api/_version_/automdm/queries/{id}/report",
	"/api/_version_/automdm/queries/id/{id}",
	"/api/_version_/automdm/queries/delete",
	"/api/_version_/automdm/queries/{id}/run",
	"/api/_version_/automdm/queries/run",
	"/api/_version_/automdm/queries/run_by_identifiers",
	"/api/_version_/automdm/queries/run_by_names",
	"/api/_version_/automdm/spec/queries",
	"/api/_version_/automdm/spec/queries/{name}",
	"/api/_version_/automdm/hosts/{id}/queries",
	"/api/_version_/automdm/hosts/{id}/queries/{report_id}",
	"/api/_version_/automdm/reports/id/{id}",
	"/api/_version_/automdm/reports/{name}",
	"/api/_version_/automdm/reports/run_by_names",
	"/api/_version_/automdm/packs",
	"/api/_version_/automdm/packs/schedule",
	"/api/_version_/automdm/packs/schedule/{id}",
	"/api/_version_/automdm/packs/id/{id}",
	"/api/_version_/automdm/packs/{id}",
	"/api/_version_/automdm/packs/{id}/scheduled",
	"/api/_version_/automdm/spec/packs",
	"/api/_version_/automdm/spec/packs/{name}",
	"/api/_version_/automdm/global/schedule",
	"/api/_version_/automdm/global/schedule/{id}",
	"/api/_version_/automdm/schedule",
	"/api/_version_/automdm/schedule/{id}",
	"/api/_version_/automdm/teams/{team_id}/schedule",
	"/api/_version_/automdm/teams/{team_id}/schedule/{report_id}",

	"/api/_version_/automdm/mdm/commands",
	"/api/_version_/automdm/mdm/apple/commands",
	"/api/_version_/automdm/mdm/commandresults",
	"/api/_version_/automdm/mdm/commands/run",
	"/api/_version_/automdm/mdm/apple/enqueue",
	"/api/_version_/automdm/mdm/hosts/{id}/lock",
	"/api/_version_/automdm/mdm/hosts/{id}/wipe",
	"/api/_version_/automdm/mdm/hosts/{id}/unenroll",
	"/api/_version_/automdm/mdm/hosts/{id}/profiles",
	"/api/_version_/automdm/mdm/hosts/{id}/encryption_key",
	"/api/_version_/automdm/mdm/disk_encryption/summary",
	"/api/_version_/automdm/mdm/apple/filevault/summary",
	"/api/_version_/automdm/mdm/apple/settings",
	"/api/_version_/automdm/mdm/profiles",
	"/api/_version_/automdm/mdm/profiles/summary",
	"/api/_version_/automdm/mdm/profiles/{profile_uuid}",
	"/api/_version_/automdm/mdm/apple/profiles",
	"/api/_version_/automdm/mdm/apple/profiles/summary",
	"/api/_version_/automdm/mdm/apple/profiles/batch",
	"/api/_version_/automdm/mdm/apple/profiles/{profile_id}",
	"/api/_version_/automdm/mdm/apple",
	"/api/_version_/automdm/mdm/apple/setup",
	"/api/_version_/automdm/mdm/apple/enrollment_profile",
	"/api/_version_/automdm/mdm/manual_enrollment_profile",
	"/api/_version_/automdm/mdm/apple/request_csr",
	"/api/_version_/automdm/mdm/apple/dep/key_pair",
	"/api/_version_/automdm/mdm/bootstrap",
	"/api/_version_/automdm/mdm/apple/bootstrap",
	"/api/_version_/automdm/mdm/bootstrap/summary",
	"/api/_version_/automdm/mdm/apple/bootstrap/summary",
	"/api/_version_/automdm/mdm/bootstrap/{team_id}/metadata",
	"/api/_version_/automdm/mdm/apple/bootstrap/{team_id}/metadata",
	"/api/_version_/automdm/mdm/bootstrap/{team_id}",
	"/api/_version_/automdm/mdm/apple/bootstrap/{team_id}",
	"/api/_version_/automdm/mdm/setup/eula",
	"/api/_version_/automdm/mdm/apple/setup/eula",
	"/api/_version_/automdm/mdm/setup/eula/metadata",
	"/api/_version_/automdm/mdm/apple/setup/eula/metadata",
	"/api/_version_/automdm/mdm/setup/eula/{token}",
	"/api/_version_/automdm/mdm/apple/setup/eula/{token}",
	"/api/_version_/automdm/labels/{name}",
	"/api/_version_/automdm/mdm/apple/installers",
	"/api/_version_/automdm/mdm/apple/installers/{installer_id}",
	"/api/mdm/apple/installer",
}

// removedPathOverlaps has the removed paths that a stdlib ServeMux cannot register next to a live route.
var removedPathOverlaps = map[string]endpointer.RemovedPathOverlap{
	// Wildcards the team segment rather than reports, so the wildcard only catches DELETE requests under /reports and wrong-method
	// requests to other /automdm/x/y routes keep their 405.
	"/api/_version_/automdm/reports/{name}": {
		Method:        http.MethodDelete,
		RouteTemplate: "/api/_version_/{automdm_segment}/reports/{name}",
	},
	"/api/_version_/automdm/packs/{id}/scheduled": {
		Method:        http.MethodGet,
		RouteTemplate: "/api/_version_/automdm/packs/{pack_id}/{scheduled_segment}",
	},
	"/api/_version_/automdm/hosts/{host_id}/configuration_profiles/resend/{profile_uuid}": {
		Method:        http.MethodPost,
		RouteTemplate: "/api/_version_/automdm/hosts/{host_id}/configuration_profiles/{resend_segment}/{profile_uuid}",
	},
	"/api/_version_/automdm/scripts/batch/summary/{batch_execution_id}": {
		Method:            http.MethodGet,
		RouteTemplate:     "/api/_version_/automdm/scripts/batch/{summary_segment}/{batch_execution_id}",
		WrongMethodRoutes: map[string][]string{"/api/_version_/automdm/scripts/batch/{batch_execution_id}/cancel": {http.MethodPost}},
	},
	"/api/_version_/automdm/hosts/identifier/{identifier}": {
		Method:        http.MethodGet,
		RouteTemplate: "/api/_version_/automdm/hosts/{identifier_segment}/{identifier}",
		WrongMethodRoutes: map[string][]string{
			"/api/_version_/automdm/hosts/transfer/filter":       {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/apns_ping":        {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/clear_passcode":   {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/google_device_id": {http.MethodPut, http.MethodDelete},
			"/api/_version_/automdm/hosts/{id}/labels":           {http.MethodPost, http.MethodDelete},
			"/api/_version_/automdm/hosts/{id}/lock":             {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/query":            {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/refetch":          {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/unlock":           {http.MethodPost},
			"/api/_version_/automdm/hosts/{id}/wipe":             {http.MethodPost},
		},
	},
}

func removedPaths() []endpointer.RemovedPath {
	paths := make([]endpointer.RemovedPath, 0, len(removedPathTemplates))
	for _, template := range removedPathTemplates {
		entry, ok := removedPathEntry(template)
		if !ok {
			panic(fmt.Sprintf("removed path %s has no entry in pkg/removednames", template))
		}
		paths = append(paths, removedPathFor(template, entry))
	}
	return paths
}

// RemovedRootPathHandler answers with the 410 of a removed path that the root mux serves, outside the API router.
func RemovedRootPathHandler(path string, logger *slog.Logger) http.Handler {
	entry, ok := removedPathEntry(path)
	if !ok {
		panic(fmt.Sprintf("removed path %s has no entry in pkg/removednames", path))
	}
	return endpointer.RemovedPathHandler(removedPathFor(path, entry), logger)
}

var pathVarConstraint = regexp.MustCompile(`\{(\w+):[^}]+\}`)

// removedPathEntry finds the entry whatever its Context: removednames.Find with an empty context skips an entry pinned to a method.
func removedPathEntry(template string) (removednames.Entry, bool) {
	tableName := pathVarConstraint.ReplaceAllString(template, "{$1}")
	for _, entry := range removednames.Filter(removednames.KindPath, 0) {
		if entry.Old == tableName {
			return entry, true
		}
	}
	return removednames.Entry{}, false
}

func removedPathFor(template string, entry removednames.Entry) endpointer.RemovedPath {
	var methods []string
	if entry.Context != "" {
		// The entry removed only this method; the path's other methods stay with whatever routes serve them.
		methods = []string{entry.Context}
	}
	return endpointer.RemovedPath{Path: template, Methods: methods, Overlap: removedPathOverlaps[template], Message: func(method string) string {
		// The message names the method that was used. Requests share the closure, so set it on a copy.
		withMethod := entry
		withMethod.Context = method
		return withMethod.Message()
	}}
}
