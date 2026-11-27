package removednames

func init() { register(automdmVariableEntries...) }

// automdmVariableEntries are the AutoMDM variables removed from configuration profiles, managed app configurations, and certificate
// authority fields.
var automdmVariableEntries = []Entry{
	{Kind: KindAutoMDMVariable, Scope: ScopeRequest, Old: "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP", New: "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"},
}
