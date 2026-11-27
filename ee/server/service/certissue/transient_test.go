package certissue

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestTransient(t *testing.T) {
	cause := errors.New("connection refused")
	t.Run("a failure to get a response is transient and keeps its message", func(t *testing.T) {
		err := Transient(t.Context(), cause)
		transientErr, ok := errors.AsType[automdm.CertificateAuthorityTransientError](err)
		require.True(t, ok)
		require.Equal(t, cause.Error(), transientErr.Error())
	})
	t.Run("a canceled request is not", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(t.Context())
		cancel()
		require.Equal(t, cause, Transient(canceledCtx, cause))
	})
	t.Run("an untrusted TLS certificate is not", func(t *testing.T) {
		untrusted := &tls.CertificateVerificationError{Err: errors.New("unknown authority")}
		require.Equal(t, error(untrusted), Transient(t.Context(), untrusted))
	})

	for status, want := range map[int]bool{
		http.StatusBadRequest: false, http.StatusUnauthorized: false, http.StatusForbidden: false, http.StatusNotFound: false,
		http.StatusRequestTimeout: true, http.StatusTooManyRequests: true, http.StatusInternalServerError: true,
		http.StatusBadGateway: true, http.StatusServiceUnavailable: true, http.StatusGatewayTimeout: true,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, ok := errors.AsType[automdm.CertificateAuthorityTransientError](TransientStatus(cause, status))
			require.Equal(t, want, ok)
		})
	}
}
