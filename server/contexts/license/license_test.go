package license

import (
	"context"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestIsPremium(t *testing.T) {
	cases := []struct {
		desc string
		ctx  context.Context
		want bool
	}{
		{"no license", context.Background(), false},
		{"free license", NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierFree}), false},
		{"premium license", NewContext(context.Background(), &automdm.LicenseInfo{Tier: automdm.TierPremium}), true},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got := IsPremium(c.ctx)
			require.Equal(t, c.want, got)
		})
	}
}
