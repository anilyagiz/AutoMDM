package service

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/require"
)

// TestBitLockerPINRequiresPremium checks the Free stubs. The Premium implementation is tested in ee/server/service.
func TestBitLockerPINRequiresPremium(t *testing.T) {
	svc, ctx := newTestService(t, new(mock.Store), nil, nil, &TestServerOpts{SkipCreateTestUsers: true})
	host := &automdm.Host{ID: 1, UUID: "host-uuid", Platform: "windows"}
	ctx = test.HostContext(ctx, host)

	require.ErrorIs(t, svc.SubmitBitLockerPIN(ctx, host, "123456"), automdm.ErrMissingLicense)
	_, _, err := svc.GetBitLockerPINForHost(ctx)
	require.ErrorIs(t, err, automdm.ErrMissingLicense)
	require.ErrorIs(t, svc.SetBitLockerPINOutcome(ctx, "req-1", automdm.BitLockerPINRequestSet, ""), automdm.ErrMissingLicense)

	// The My device page gets no PIN fields, so it shows the Manage BitLocker instructions.
	canSetPIN, req, err := svc.BitLockerPINStateForDevice(ctx, host)
	require.NoError(t, err)
	require.False(t, canSetPIN)
	require.Nil(t, req)
}
