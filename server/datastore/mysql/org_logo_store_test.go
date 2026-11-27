package mysql

import (
	"bytes"
	"io"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestOrgLogoStore(t *testing.T) {
	ds := CreateMySQLDS(t)
	store := ds.NewOrgLogoStore()
	ctx := t.Context()

	light := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic
	dark := []byte{0xFF, 0xD8, 0xFF}                                // JPEG magic

	// Nothing stored yet.
	exists, err := store.Exists(ctx, automdm.OrgLogoModeLight)
	require.NoError(t, err)
	require.False(t, exists)

	_, _, err = store.Get(ctx, automdm.OrgLogoModeLight)
	require.Error(t, err)
	require.True(t, automdm.IsNotFound(err))

	// Put light, then read it back.
	require.NoError(t, store.Put(ctx, automdm.OrgLogoModeLight, bytes.NewReader(light)))

	exists, err = store.Exists(ctx, automdm.OrgLogoModeLight)
	require.NoError(t, err)
	require.True(t, exists)

	rc, size, err := store.Get(ctx, automdm.OrgLogoModeLight)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, light, got)
	require.EqualValues(t, len(light), size)

	// Modes are independent: dark is still absent.
	exists, err = store.Exists(ctx, automdm.OrgLogoModeDark)
	require.NoError(t, err)
	require.False(t, exists)

	// Put overwrites in place (upsert on the mode primary key).
	require.NoError(t, store.Put(ctx, automdm.OrgLogoModeLight, bytes.NewReader(dark)))
	rc, _, err = store.Get(ctx, automdm.OrgLogoModeLight)
	require.NoError(t, err)
	got, err = io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, dark, got)

	// Delete is idempotent.
	require.NoError(t, store.Delete(ctx, automdm.OrgLogoModeLight))
	exists, err = store.Exists(ctx, automdm.OrgLogoModeLight)
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, store.Delete(ctx, automdm.OrgLogoModeLight))
}
