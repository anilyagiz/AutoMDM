package capabilities

import (
	"context"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestCapabilitiesExist(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  automdm.CapabilityMap
	}{
		{"empty", "", automdm.CapabilityMap{}},
		{"one", "test", automdm.CapabilityMap{automdm.Capability("test"): struct{}{}}},
		{
			"many",
			"test,foo,bar",
			automdm.CapabilityMap{
				automdm.Capability("test"): struct{}{},
				automdm.Capability("foo"):  struct{}{},
				automdm.Capability("bar"):  struct{}{},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := http.Request{
				Header: http.Header{automdm.CapabilitiesHeader: []string{tt.in}},
			}
			ctx := NewContext(context.Background(), &r)
			mp, ok := FromContext(ctx)
			require.True(t, ok)
			require.Equal(t, tt.out, mp)
		})
	}
}

func TestCapabilitiesNotExist(t *testing.T) {
	mp, ok := FromContext(context.Background())
	require.False(t, ok)
	require.Nil(t, mp)
}
