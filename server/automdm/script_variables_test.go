package automdm

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAutoMDMVariablesInScript(t *testing.T) {
	t.Parallel()

	t.Run("no variables", func(t *testing.T) {
		for _, contents := range []string{
			"",
			"#!/bin/sh\necho hello",
			"echo $AUTOMDM_SECRET_MY_SECRET",
			"echo $AUTOMDM_HOST_VITAL_computer_name",
			"echo $SOME_OTHER_VAR and ${ANOTHER}",
		} {
			require.NoError(t, ValidateAutoMDMVariablesInScript(contents, false), contents)
			require.NoError(t, ValidateAutoMDMVariablesInScript(contents, true), contents)
		}
	})

	t.Run("supported variables on premium", func(t *testing.T) {
		for _, v := range AutoMDMVarsSupportedInScripts {
			require.NoError(t, ValidateAutoMDMVariablesInScript("echo "+v.WithPrefix(), true), v)
			require.NoError(t, ValidateAutoMDMVariablesInScript("echo "+v.WithBraces(), true), v)
			require.NoError(t, ValidateAutoMDMVariablesInScript(
				fmt.Sprintf("echo user_%s@example.com", v.WithBraces()), true), v)
		}
		require.NoError(t, ValidateAutoMDMVariablesInScript(
			"#!/bin/sh\necho $AUTOMDM_VAR_HOST_UUID on ${AUTOMDM_VAR_HOST_PLATFORM}\n", true))
	})

	t.Run("unsupported variables on premium", func(t *testing.T) {
		unsupported := []AutoMDMVarName{
			"NONEXISTENT",
			"HOST_END_USER_EMAIL_IDP",
			AutoMDMVarNDESSCEPProxyURL,
			AutoMDMVarPSSODeviceRegistrationToken,
			"CUSTOM_SCEP_CHALLENGE_FOO",
			"DIGICERT_DATA_FOO",
			"SMALLSTEP_SCEP_CHALLENGE_FOO",
		}
		for _, v := range unsupported {
			for _, contents := range []string{"echo " + v.WithPrefix(), "echo " + v.WithBraces()} {
				err := ValidateAutoMDMVariablesInScript(contents, true)
				require.Error(t, err, contents)
				var iae *InvalidArgumentError
				require.ErrorAs(t, err, &iae, contents)
				require.ErrorContains(t, err,
					fmt.Sprintf("AutoMDM variable $AUTOMDM_VAR_%s is not supported in scripts.", v))
			}
		}
	})

	t.Run("mixed supported and unsupported", func(t *testing.T) {
		err := ValidateAutoMDMVariablesInScript(
			"echo $AUTOMDM_VAR_HOST_UUID\necho $AUTOMDM_VAR_NONEXISTENT", true)
		require.ErrorContains(t, err, "$AUTOMDM_VAR_NONEXISTENT is not supported in scripts")
	})

	t.Run("any variable on free returns license error", func(t *testing.T) {
		for _, contents := range []string{
			"echo $AUTOMDM_VAR_HOST_UUID",
			"echo ${AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME}",
			"echo $AUTOMDM_VAR_NONEXISTENT",
		} {
			err := ValidateAutoMDMVariablesInScript(contents, false)
			require.ErrorIs(t, err, ErrMissingLicense, contents)
		}
	})
}
