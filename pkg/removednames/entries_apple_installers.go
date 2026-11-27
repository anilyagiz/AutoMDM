package removednames

func init() { register(appleInstallerEntries...) }

// appleInstallerEntries are the installer store of the removed automdmctl apple-mdm commands.
var appleInstallerEntries = []Entry{
	removedPath("/mdm/apple/installers"),
	removedPath("/mdm/apple/installers/{installer_id}"),
	// The device download sat outside the AutoMDM API, so Old is the full path.
	{Kind: KindPath, Scope: ScopeRequest, Old: "/api/mdm/apple/installer", Format: EndpointFormat{}},
}
