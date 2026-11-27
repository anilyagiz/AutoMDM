package android

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoMDMDefaultPolicyID(t *testing.T) {
	require.Equal(t, "automdm0", AutoMDMDefaultPolicyID(0))
	require.Equal(t, "automdm42", AutoMDMDefaultPolicyID(42))
}

func TestIsDefaultPolicyID(t *testing.T) {
	tests := []struct {
		policyID string
		want     bool
	}{
		{policyID: "1", want: true},
		{policyID: AutoMDMDefaultPolicyID(0), want: true},
		{policyID: AutoMDMDefaultPolicyID(42), want: true},
		{policyID: "automdm", want: false},
		{policyID: "automdm-1", want: false},
		{policyID: "automdmabc", want: false},
		{policyID: "automdm007", want: false},
		{policyID: "2", want: false},
		{policyID: "", want: false},
		{policyID: "a1b2c3d4e5f6a7b8", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.policyID, func(t *testing.T) {
			require.Equal(t, tt.want, IsDefaultPolicyID(tt.policyID))
		})
	}
}
