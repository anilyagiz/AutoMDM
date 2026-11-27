package removednames

func init() { register(teamEntries...) }

// teamEntries are the names removed by renaming teams to teams. They mirror what the code accepted as deprecated aliases before AutoMDM 5
// (the renameto struct tags, the deprecated URL path aliases, the automdmctl flag aliases, and pkg/spec GitOps key mappings);
// TestEntriesCoverDeprecatedSources fails if any of those sources has a team-related name that is missing here.
var teamEntries = []Entry{
	// JSON keys in request and response bodies.
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "available_teams", New: "available_teams"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "byod_team", New: "byod_team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "byod_team_id", New: "byod_team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "default_team", New: "default_team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "host_team_id", New: "host_team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "ios_team", New: "ios_team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "ios_team_id", New: "ios_team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "ipados_team", New: "ipados_team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "ipados_team_id", New: "ipados_team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "macos_team", New: "macos_team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "macos_team_id", New: "macos_team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "team", New: "team"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "team_id", New: "team_id"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "team_ids", New: "team_ids"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "team_ids_by_name", New: "team_ids_by_name"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "team_name", New: "team_name"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "teams", New: "teams"},
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "apple_bm_default_team", New: "apple_business"},

	// Query parameters.
	{Kind: KindQueryParam, Scope: ScopeRequest, Old: "team_id", New: "team_id"},
	{Kind: KindQueryParam, Scope: ScopeRequest, Old: "team_name", New: "team_name"},

	// Multipart form fields (software and script uploads).
	{Kind: KindFormField, Scope: ScopeRequest, Old: "team_id", New: "team_id"},

	// CSV export column headers, which GET /hosts/report also takes in its columns parameter.
	{Kind: KindCSVColumn, Scope: ScopeRequest | ScopeResponse, Old: "team_id", New: "team_id"},
	{Kind: KindCSVColumn, Scope: ScopeRequest | ScopeResponse, Old: "team_name", New: "team_name"},

	// Keys inside the stored details of activities.
	{Kind: KindJSONKey, Scope: ScopeActivityDetails, Old: "team_id", New: "team_id"},
	{Kind: KindJSONKey, Scope: ScopeActivityDetails, Old: "team_name", New: "team_name"},
	{Kind: KindJSONKey, Scope: ScopeActivityDetails, Old: "teams", New: "teams"},

	// Activity type names.
	{Kind: KindActivityType, Scope: ScopeRequest | ScopeResponse, Old: "created_team", New: "created_team"},
	{Kind: KindActivityType, Scope: ScopeRequest | ScopeResponse, Old: "deleted_team", New: "deleted_team"},
	{Kind: KindActivityType, Scope: ScopeRequest | ScopeResponse, Old: "applied_spec_team", New: "applied_spec_team"},
	{Kind: KindActivityType, Scope: ScopeRequest | ScopeResponse, Old: "changed_user_team_role", New: "changed_user_team_role"},
	{Kind: KindActivityType, Scope: ScopeRequest | ScopeResponse, Old: "deleted_user_team_role", New: "deleted_user_team_role"},

	// URL paths, relative to /api/_version_/automdm. Path parameters use {name}.
	pathEntry("/ab_tokens/{id}/teams", "/ab_tokens/{id}/teams"),
	pathEntry("/abm_tokens/{id}/teams", "/ab_tokens/{id}/teams"),
	pathEntry("/spec/teams", "/spec/teams"),
	pathEntry("/team/{team_id}/policies", "/policies"),
	pathEntry("/team/{team_id}/policies/count", "/policies/count"),
	pathEntry("/team/{team_id}/policies/delete", "/policies/delete"),
	pathEntry("/team/{team_id}/policies/{policy_id}", "/policies/{policy_id}"),
	pathEntry("/team/{team_id}/schedule", "/teams/{team_id}/schedule"),
	pathEntry("/team/{team_id}/schedule/{report_id}", "/teams/{team_id}/schedule/{report_id}"),
	pathEntry("/teams", "/teams"),
	pathEntry("/teams/{team_id}/policies", "/policies"),
	pathEntry("/teams/{team_id}/policies/count", "/policies/count"),
	pathEntry("/teams/{team_id}/policies/delete", "/policies/delete"),
	pathEntry("/teams/{team_id}/policies/{policy_id}", "/policies/{policy_id}"),
	pathEntry("/teams/{team_id}/schedule", "/teams/{team_id}/schedule"),
	pathEntry("/teams/{team_id}/schedule/{report_id}", "/teams/{team_id}/schedule/{report_id}"),
	pathEntry("/teams/{team_id}/secrets", "/teams/{team_id}/secrets"),
	pathEntry("/teams/{id}", "/teams/{id}"),
	pathEntry("/teams/{id}/agent_options", "/teams/{id}/agent_options"),
	pathEntry("/teams/{id}/secrets", "/teams/{id}/secrets"),
	pathEntry("/teams/{id}/users", "/teams/{id}/users"),
	pathEntry("/vpp_tokens/{id}/teams", "/vpp_tokens/{id}/teams"),

	// Values of order_key parameters.
	{Kind: KindSortKey, Scope: ScopeRequest, Old: "team_id", New: "team_id"},
	{Kind: KindSortKey, Scope: ScopeRequest, Old: "team_name", New: "team_name"},

	// Target type.
	{Kind: KindTargetType, Scope: ScopeRequest, Old: "team", New: "team"},

	// automdmctl commands, flags, and environment variables.
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl get teams", New: "automdmctl get teams"},
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl get team", New: "automdmctl get teams"},
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl get t", New: "automdmctl get teams"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl report"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl user create"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "t", New: "team", Context: "automdmctl user create"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl generate-gitops"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl hosts transfer"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl run-script"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl get reports"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl get labels"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl get hosts"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "team", New: "team", Context: "automdmctl get software"},
	{Kind: KindFlagValue, Scope: ScopeRequest, Old: "--team no-team", New: "--team unassigned", Context: "automdmctl generate-gitops"},
	{Kind: KindFlagValue, Scope: ScopeRequest, Old: "--team no-team", New: "--team unassigned", Context: "automdmctl generate-gitops"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "policies-team", New: "policies-automdm", Context: "automdmctl apply"},
	{Kind: KindFlag, Scope: ScopeRequest, Old: "delete-other-teams", New: "delete-other-teams", Context: "automdmctl gitops"},
	{Kind: KindEnvVar, Scope: ScopeRequest, Old: "DELETE_OTHER_TEAMS", New: "DELETE_OTHER_TEAMS", Context: "automdmctl gitops"},

	// GitOps and apply YAML keys, team-related only.
	{Kind: KindYAMLKey, Scope: ScopeRequest, Old: "team_settings", New: "settings"},
	abmEntry("macos_team", "macos_team"),
	abmEntry("ios_team", "ios_team"),
	abmEntry("ipados_team", "ipados_team"),
	abmEntry("byod_team", "byod_team"),
	yamlEntry("org_settings.mdm.apple_bm_default_team", "org_settings.mdm.apple_business"),
	yamlEntry("org_settings.mdm.volume_purchasing_program[].teams",
		"org_settings.mdm.volume_purchasing_program[].teams"),

	// The key that holds a team in an automdmctl apply spec of kind team.
	yamlEntry("spec.team", "spec.team"),

	// The GitOps file for hosts that belong to no team, and the team name in it. The name is compared without regard to case.
	{Kind: KindFileName, Scope: ScopeRequest, Old: "no-team.yml", New: "unassigned.yml"},
	{Kind: KindYAMLValue, Scope: ScopeRequest, Old: "name: No team", New: "name: Unassigned"},

	// SAML attributes.
	{Kind: KindSAMLAttribute, Scope: ScopeRequest, Old: "AUTOMDM_JIT_USER_ROLE_TEAM_<ID>", New: "AUTOMDM_JIT_USER_ROLE_TEAM_<ID>"},

	// Spec kinds accepted by automdmctl apply.
	{Kind: KindSpecKind, Scope: ScopeRequest, Old: "team", New: "team"},
}

func abmEntry(oldLeaf, newLeaf string) Entry {
	const prefix = "org_settings.mdm.apple_business[]."
	return Entry{Kind: KindYAMLKey, Scope: ScopeRequest, Old: prefix + oldLeaf, New: prefix + newLeaf}
}
