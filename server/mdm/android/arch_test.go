package android_test

import (
	"regexp"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/archtest"
)

const m = archtest.ModuleName

// TestAllAndroidPackageDependencies checks that android packages are not dependent on other AutoMDM packages
// to maintain decoupling and modularity.
// If coupling is necessary, it should be done in the main server/automdm, server/service, or other package.
func TestAllAndroidPackageDependencies(t *testing.T) {
	t.Parallel()
	archtest.NewPackageTest(t, m+"/server/mdm/android...").
		OnlyInclude(regexp.MustCompile(`^github\.com/automdm/`)).
		ShouldNotDependOn(
			m+"/server/service...",
			m+"/server/datastore/mysql...",
		).
		IgnoreRecursively(
			m+"/server/mdm/android/tests",
		).
		IgnoreDeps(
			// Android packages
			m+"/server/mdm/android...",
			// Platform packages
			m+"/server/platform...",
			// Other/infra packages
			m+"/server/datastore/mysql/common_mysql",
			m+"/server/service/externalsvc", // dependency on Jira and Zendesk
			m+"/server/service/middleware/auth",
			m+"/server/service/middleware/log",
		).
		Check()
}

// TestAndroidPackageDependencies checks that android package is NOT dependent on ANY other AutoMDM packages
// to maintain decoupling and modularity. This package should only contain basic structs and interfaces.
// If coupling is necessary, it should be done in the main server/automdm or another package.
func TestAndroidPackageDependencies(t *testing.T) {
	t.Parallel()
	archtest.NewPackageTest(t, m+"/server/mdm/android").
		OnlyInclude(regexp.MustCompile(`^github\.com/automdm/`)).
		ShouldNotDependOn(m + "/...").
		Check()
}
