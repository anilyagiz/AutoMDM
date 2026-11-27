package automdm_test

import (
	"encoding/json"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetTypeJSON(t *testing.T) {
	testCases := []struct {
		expected  automdm.TargetType
		shouldErr bool
	}{
		{automdm.TargetLabel, false},
		{automdm.TargetHost, false},
		{automdm.TargetTeam, false},
		{automdm.TargetType(37), true},
	}
	for _, tt := range testCases {
		t.Run(tt.expected.String(), func(t *testing.T) {
			b, err := json.Marshal(tt.expected)
			require.NoError(t, err)
			var target automdm.TargetType
			err = json.Unmarshal(b, &target)
			if tt.shouldErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, target)
			}
		})
	}
}
