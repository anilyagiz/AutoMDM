package removednames

func init() { register(teamEntries...) }

// teamEntries are team-related names that AutoMDM 5 removed or replaced. Team names themselves (team_id, /teams, --team, kind team)
// are the current names and are not listed here.
var teamEntries = []Entry{
	// JSON keys in request and response bodies.
	{Kind: KindJSONKey, Scope: ScopeRequest | ScopeResponse, Old: "apple_bm_default_team", New: "apple_business"},

	// Query parameters.

	// Multipart form fields (software and script uploads).

	// CSV export column headers, which GET /hosts/report also takes in its columns parameter.

	// Keys inside the stored details of activities.

	// Activity type names.

	// URL paths, relative to /api/_version_/automdm. Path parameters use {name}.
	pathEntry("/team/{team_id}/policies", "/policies"),
	pathEntry("/team/{team_id}/policies/count", "/policies/count"),
	pathEntry("/team/{team_id}/policies/delete", "/policies/delete"),
	pathEntry("/team/{team_id}/policies/{policy_id}", "/policies/{policy_id}"),
	pathEntry("/team/{team_id}/schedule", "/teams/{team_id}/schedule"),
	pathEntry("/team/{team_id}/schedule/{report_id}", "/teams/{team_id}/schedule/{report_id}"),

	// Values of order_key parameters.

	// Target type.

	// automdmctl commands, flags, and environment variables.
	{Kind: KindFlag, Scope: ScopeRequest, Old: "t", New: "team", Context: "automdmctl user create"},
	{Kind: KindFlagValue, Scope: ScopeRequest, Old: "--team no-team", New: "--team unassigned", Context: "automdmctl generate-gitops"},

	// GitOps and apply YAML keys, team-related only.
	{Kind: KindYAMLKey, Scope: ScopeRequest, Old: "team_settings", New: "settings"},
	yamlEntry("org_settings.mdm.apple_bm_default_team", "org_settings.mdm.apple_business"),

	// The key that holds a team in an automdmctl apply spec of kind team.

	// The GitOps file for hosts that belong to no team, and the team name in it. The name is compared without regard to case.
	{Kind: KindFileName, Scope: ScopeRequest, Old: "no-team.yml", New: "unassigned.yml"},
	{Kind: KindYAMLValue, Scope: ScopeRequest, Old: "name: No team", New: "name: Unassigned"},

	// SAML attributes.

	// Spec kinds accepted by automdmctl apply.
}

func abmEntry(oldLeaf, newLeaf string) Entry {
	const prefix = "org_settings.mdm.apple_business[]."
	return Entry{Kind: KindYAMLKey, Scope: ScopeRequest, Old: prefix + oldLeaf, New: prefix + newLeaf}
}
