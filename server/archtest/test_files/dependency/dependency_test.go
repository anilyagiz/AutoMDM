package dependency

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/archtest/test_files/testfiledeps/transitivetestdep"
)

func TestDependency(t *testing.T) {
	transitivetestdep.TransitiveTestHelper()
}
