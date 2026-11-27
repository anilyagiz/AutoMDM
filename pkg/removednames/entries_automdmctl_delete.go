package removednames

func init() { register(automdmctlDeleteEntries...) }

// automdmctlDeleteEntries are the automdmctl commands removed with automdmctl delete.
var automdmctlDeleteEntries = []Entry{
	{Kind: KindCommand, Scope: ScopeRequest, Old: "automdmctl delete"},
}
