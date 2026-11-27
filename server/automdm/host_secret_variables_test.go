package automdm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMissingSecretNames(t *testing.T) {
	for _, err := range []error{
		MissingSecretsError{MissingSecrets: []string{"A"}},
		&MissingSecretsError{MissingSecrets: []string{"A"}},
		fmt.Errorf("wrapped: %w", MissingSecretsError{MissingSecrets: []string{"A"}}),
	} {
		names, ok := MissingSecretNames(err)
		require.True(t, ok, err)
		require.Equal(t, []string{"A"}, names)
		require.True(t, IsMissingSecretsError(err))
	}

	_, ok := MissingSecretNames(errors.New("other"))
	require.False(t, ok)
	require.False(t, IsMissingSecretsError(nil))
}

func TestValidateNoHostSecretVariables(t *testing.T) {
	require.NoError(t, ValidateNoHostSecretVariables(""))
	require.NoError(t, ValidateNoHostSecretVariables("$AUTOMDM_SECRET_X $AUTOMDM_VAR_HOST_UUID AUTOMDM_HOST_SECRET_ENROLL_SECRET without a dollar sign"))

	err := ValidateNoHostSecretVariables("<string>" + HostSecretPlaceholder(HostSecretEnrollSecret) + "</string>")
	require.Error(t, err)
	require.Contains(t, err.Error(), "$AUTOMDM_HOST_SECRET_ENROLL_SECRET is reserved")

	err = ValidateNoHostSecretVariables("${AUTOMDM_HOST_SECRET_RECOVERY_LOCK_PASSWORD}")
	require.Error(t, err)
	require.Contains(t, err.Error(), "$AUTOMDM_HOST_SECRET_RECOVERY_LOCK_PASSWORD is reserved")
}

func TestValidateNoHostSecretVariablesAdded(t *testing.T) {
	unlock := HostSecretPlaceholder(HostSecretMDMUnlockToken)
	recoveryLock := "${" + HostSecretPrefix + HostSecretRecoveryLockPassword + "}"

	// nothing expanded, or only placeholders the document already had
	require.NoError(t, ValidateNoHostSecretVariablesAdded("", ""))
	require.NoError(t, ValidateNoHostSecretVariablesAdded("a $AUTOMDM_SECRET_X b", "a value b"))
	require.NoError(t, ValidateNoHostSecretVariablesAdded(unlock+" $AUTOMDM_SECRET_X", unlock+" value"))

	// a placeholder that only appears once custom variables are expanded
	err := ValidateNoHostSecretVariablesAdded("$AUTOMDM_SECRET_X", unlock)
	require.ErrorContains(t, err, unlock)
	err = ValidateNoHostSecretVariablesAdded(unlock+" $AUTOMDM_SECRET_X", unlock+" "+recoveryLock)
	require.ErrorContains(t, err, HostSecretPlaceholder(HostSecretRecoveryLockPassword))

	// one more occurrence of a placeholder the document already had
	err = ValidateNoHostSecretVariablesAdded(unlock+" $AUTOMDM_SECRET_X", unlock+" "+unlock)
	require.ErrorContains(t, err, unlock)
	require.NoError(t, ValidateNoHostSecretVariablesAdded(unlock+" "+unlock, unlock+" "+unlock))
}
