package tables

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUp_20261113161959(t *testing.T) {
	db := applyUpToPrev(t)

	tokenID := execNoErrLastID(t, db, `
		INSERT INTO android_zero_touch_tokens (team_id, global_or_team_id, token_name, token_value, expires_at)
		VALUES (NULL, 0, 'enterprises/e/enrollmentTokens/legacy', 'legacy-value', '2126-01-01 00:00:00')`)

	applyNext(t, db)

	var usesAutoMDMPolicy bool
	require.NoError(t, db.Get(&usesAutoMDMPolicy, `SELECT uses_automdm_policy FROM android_zero_touch_tokens WHERE id = ?`, tokenID))
	require.False(t, usesAutoMDMPolicy, "tokens created before the migration point at the global default policy")

	execNoErr(t, db, `INSERT INTO android_automdm_default_policies (team_id, checksum) VALUES (0, UNHEX(MD5('a')))`)
	var count int
	require.NoError(t, db.Get(&count, `SELECT COUNT(*) FROM android_automdm_default_policies WHERE team_id = 0`))
	require.Equal(t, 1, count)
}
