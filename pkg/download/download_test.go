package download

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/stretchr/testify/require"
)

func TestDownloadNotFoundNoRetries(t *testing.T) {
	c := automdmhttp.NewClient()
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "not-used")
	url, err := url.Parse("https://github.com/fleetdm/non-existent")
	require.NoError(t, err)
	start := time.Now()
	err = Download(c, url, outputFile)
	require.Error(t, err)
	require.ErrorIs(t, err, NotFound)
	require.True(t, time.Since(start) < backoffMaxElapsedTime)
}
