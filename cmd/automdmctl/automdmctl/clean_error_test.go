package automdmctl

import (
	"errors"
	"fmt"
	"testing"

	"github.com/anilyagiz/automdm/v4/client"
	"github.com/stretchr/testify/require"
)

func TestCleanStatusCodeErr(t *testing.T) {
	sce := &client.StatusCodeErr{Code: 422, Body: "Validation Failed: name may not be empty"}
	wrapped := fmt.Errorf("PATCH /api/latest/automdm/config received status %w", sce)
	outer := fmt.Errorf("applying automdm config: %w", wrapped)

	cases := []struct {
		name string
		in   error
		want string
	}{
		{"nil", nil, ""},
		{"plain error untouched", errors.New("boom"), "boom"},
		{"bare wrapped status code err", wrapped, "Validation Failed: name may not be empty"},
		{"outer-wrapped status code err", outer, "applying automdm config: Validation Failed: name may not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanStatusCodeErr(tc.in)
			if tc.in == nil {
				require.NoError(t, got)
				return
			}
			require.Equal(t, tc.want, got.Error())
		})
	}
}
