package automdmctl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/require"
)

func TestEarlySessionCheck(t *testing.T) {
	_, ds := testing_utils.RunServerWithMockedDS(t)
	ds.ListQueriesFunc = func(ctx context.Context, opt automdm.ListQueryOptions) ([]*automdm.Query, int, int, *automdm.PaginationMetadata, error) {
		return nil, 0, 0, nil, nil
	}
	ds.SessionByKeyFunc = func(ctx context.Context, key string) (*automdm.Session, error) {
		return nil, &notFoundError{}
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	config := `contexts:
  default:
    tls-skip-verify: true
    token: phIEGWGzKxXui1uZYFBXFwZ1Wv1iMxl79gbqMbOmMxgyZP2O5jga5qyhvEjzlGsdM7ax93iDqjnVSu9Fi8q1/w==`
	err := os.WriteFile(configPath, []byte(config), configFilePerms)
	require.NoError(t, err)

	_, err = runAppNoChecks([]string{"get", "queries", "--config", configPath})
	require.ErrorIs(t, err, service.ErrUnauthenticated)
}
