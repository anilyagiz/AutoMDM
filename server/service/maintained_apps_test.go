package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAddFMASvc struct {
	automdm.Service
	err error
}

func (s fakeAddFMASvc) AddAutoMDMMaintainedApp(ctx context.Context, _ *uint, _ uint, _, _, _, _ string, _ bool, _ bool, _, _, _ []string) (uint, error) {
	return 0, s.err
}

func TestAddAutoMDMMaintainedAppEndpointErrorTranslation(t *testing.T) {
	ctx := t.Context()
	// inner error mirrors the real wrap chain from DownloadInstaller
	wrap := func(sentinel error) error {
		return ctxerr.Wrap(ctx, ctxerr.Wrapf(ctx, sentinel, "reading installer %q contents", "https://cdn/app.pkg"), "downloading app installer")
	}

	cases := []struct {
		name    string
		in      error
		wantMsg string // expected GatewayError.Message
		want504 bool
	}{
		{"canceled", wrap(context.Canceled), automdm.AddMaintainedAppCanceledErrMsg, true},
		{"deadline", wrap(context.DeadlineExceeded), automdm.AddMaintainedAppTimeoutErrMsg, true},
		{"other", wrap(errors.New("boom")), "", false}, // passthrough, untouched
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := addAutoMDMMaintainedAppEndpoint(ctx, &addAutoMDMMaintainedAppRequest{}, fakeAddFMASvc{err: tc.in})
			require.NoError(t, err)
			gotErr := resp.(*addAutoMDMMaintainedAppResponse).Err
			require.Error(t, gotErr)

			if tc.want504 {
				var ge *automdm.GatewayError
				require.ErrorAs(t, gotErr, &ge)
				require.Equal(t, http.StatusGatewayTimeout, ge.StatusCode())
				require.Equal(t, tc.wantMsg, ge.Message)
				// GatewayError has no Unwrap, so errors.Is can't reach the sentinel
				// and EncodeError's 499 branch won't fire.
				require.NotErrorIs(t, gotErr, context.Canceled)
				require.NotErrorIs(t, gotErr, context.DeadlineExceeded)
			} else {
				var ge *automdm.GatewayError
				require.NotErrorAs(t, gotErr, &ge) // untouched passthrough
			}
		})
	}
}

func TestAddAutoMDMMaintainedAppDecodeRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantTeamID *uint
	}{
		{
			name:       "team_id accepted",
			body:       `{"team_id": 42, "automdm_maintained_app_id": 1}`,
			wantTeamID: ptr.Uint(42),
		},
		{
			name:       "neither provided",
			body:       `{"automdm_maintained_app_id": 1}`,
			wantTeamID: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/", io.NopCloser(bytes.NewBufferString(tt.body)))
			require.NoError(t, err)

			result, err := addAutoMDMMaintainedAppRequest{}.DecodeRequest(context.Background(), r)
			require.NoError(t, err)
			req := result.(*addAutoMDMMaintainedAppRequest)
			if tt.wantTeamID == nil {
				assert.Nil(t, req.TeamID)
			} else {
				require.NotNil(t, req.TeamID)
				assert.Equal(t, *tt.wantTeamID, *req.TeamID)
			}
		})
	}
}
