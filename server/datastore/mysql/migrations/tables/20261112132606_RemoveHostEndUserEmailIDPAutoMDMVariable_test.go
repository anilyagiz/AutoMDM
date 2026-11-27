package tables

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUp_20261112132606(t *testing.T) {
	db := applyUpToPrev(t)

	// A profile saved before the upgrade that uses both the removed variable and its replacement.
	execNoErr(t, db, `INSERT INTO mdm_apple_configuration_profiles (profile_uuid, team_id, identifier, name, mobileconfig, checksum)
		VALUES ('a-profile', 0, 'com.example.profile', 'Profile', '<plist/>', UNHEX(MD5('x')))`)
	execNoErr(t, db, `INSERT INTO mdm_configuration_profile_variables (apple_profile_uuid, automdm_variable_id)
		SELECT 'a-profile', id FROM automdm_variables WHERE name IN ('AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP', 'AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME')`)
	var linked int
	require.NoError(t, db.Get(&linked, `SELECT COUNT(*) FROM mdm_configuration_profile_variables WHERE apple_profile_uuid = 'a-profile'`))
	require.Equal(t, 2, linked)

	applyNext(t, db)

	var removed int
	require.NoError(t, db.Get(&removed, `SELECT COUNT(*) FROM automdm_variables WHERE name = 'AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP'`))
	require.Zero(t, removed)

	// The profile stays, linked only to the replacement.
	var names []string
	require.NoError(t, db.Select(&names, `SELECT fv.name FROM mdm_configuration_profile_variables v
		JOIN automdm_variables fv ON fv.id = v.automdm_variable_id WHERE v.apple_profile_uuid = 'a-profile'`))
	require.Equal(t, []string{"AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"}, names)
	var profiles int
	require.NoError(t, db.Get(&profiles, `SELECT COUNT(*) FROM mdm_apple_configuration_profiles WHERE profile_uuid = 'a-profile'`))
	require.Equal(t, 1, profiles)
}
