package removednames

func init() { register(userRolesEntries...) }

// userRolesEntries are the bulk user role and CSV user management features removed in favor of the per-user endpoints and commands.
var userRolesEntries = []Entry{
	{Kind: KindPath, Scope: ScopeRequest, Old: pathPrefix + "/users/roles/spec", New: pathPrefix + "/users/{id}"},
	{Kind: KindSpecKind, Scope: ScopeRequest, Old: "user_roles"},
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl user create-users", New: "automdmctl user create"},
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl user delete-users", New: "automdmctl user delete"},
}
