package update

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/constant"
	"github.com/stretchr/testify/assert"
)

func TestIsAutoMDMdUninstallEntry(t *testing.T) {
	for _, tc := range []struct {
		displayName, publisher string
		want                   bool
	}{
		{"AutoMDM", constant.AgentPublisher, true},
		{"AutoMDM osquery", constant.AgentPublisher, true},
		{"AutoMDM osquery", "", true},
		{"AutoMDM", "JetBrains s.r.o.", false},
		{"AutoMDM", "", false},
		{"automdm", constant.AgentPublisher, false},
		{"AutoMDM Desktop", constant.AgentPublisher, false},
		{"osquery", "osquery", false},
	} {
		assert.Equal(t, tc.want, isAutoMDMdUninstallEntry(tc.displayName, tc.publisher), "%q by %q", tc.displayName, tc.publisher)
	}
}

func TestPackedProductCode(t *testing.T) {
	for _, tc := range []struct {
		code, want string
		ok         bool
	}{
		{"{AFA95216-3541-4281-BD30-8C16737ACBA1}", "61259AFA14531824DB03C86137A7BC1A", true},
		{"{afa95216-3541-4281-bd30-8c16737acba1}", "61259afa14531824db03c86137a7bc1a", true},
		{"Git_is1", "", false},
		{"AFA95216-3541-4281-BD30-8C16737ACBA1", "", false},
		{"{AFA95216-3541-4281-BD308-C16737ACBA1}", "", false},
		{"{AFA95216-3541-4281-BD30-8C16737ACBAZ}", "", false},
	} {
		got, ok := packedProductCode(tc.code)
		assert.Equal(t, tc.ok, ok, tc.code)
		assert.Equal(t, tc.want, got, tc.code)
	}
}
