package service

import (
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

// This test covers validation errors and not happy paths, that should be done in the respective platforms calling this, as they may depend on additional validations.
func TestValidateProfileCertificateAuthorityVariables(t *testing.T) {
	t.Parallel()
	groupedCAs := &automdm.GroupedCertificateAuthorities{
		DigiCert: []automdm.DigiCertCA{
			newMockDigicertCA("https://example.com", "caName"),
			newMockDigicertCA("https://example.com", "caName2"),
		},
		CustomScepProxy: []automdm.CustomSCEPProxyCA{
			newMockCustomSCEPProxyCA("https://example.com", "scepName"),
			newMockCustomSCEPProxyCA("https://example.com", "scepName2"),
		},
		Smallstep: []automdm.SmallstepSCEPProxyCA{
			newMockSmallstepSCEPProxyCA("https://example.com", "https://example.com/challenge", "smallstepName"),
			newMockSmallstepSCEPProxyCA("https://example.com", "https://example.com/challenge", "smallstepName2"),
		},
	}

	cases := []struct {
		name    string
		profile string
		errMsg  string
	}{
		{
			name: "DigiCert badCA",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_bad", "$AUTOMDM_VAR_DIGICERT_DATA_bad", "Name",
				"com.apple.security.pkcs12"),
			errMsg: "_bad does not exist",
		},
		{
			name: "DigiCert password shows up an extra time",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_caName", "$AUTOMDM_VAR_DIGICERT_DATA_caName",
				"$AUTOMDM_VAR_DIGICERT_PASSWORD_caName",
				"com.apple.security.pkcs12"),
			errMsg: "$AUTOMDM_VAR_DIGICERT_PASSWORD_caName is already present in configuration profile",
		},
		{
			name: "DigiCert data shows up an extra time",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_caName", "$AUTOMDM_VAR_DIGICERT_DATA_caName",
				"$AUTOMDM_VAR_DIGICERT_DATA_caName",
				"com.apple.security.pkcs12"),
			errMsg: "$AUTOMDM_VAR_DIGICERT_DATA_caName is already present in configuration profile",
		},
		{
			name:    "DigiCert password missing",
			profile: digiCertForValidation("password", "$AUTOMDM_VAR_DIGICERT_DATA_caName", "Name", "com.apple.security.pkcs12"),
			errMsg:  "Missing $AUTOMDM_VAR_DIGICERT_PASSWORD_caName",
		},
		{
			name: "DigiCert data missing",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_caName", "data", "Name",
				"com.apple.security.pkcs12"),
			errMsg: "Missing $AUTOMDM_VAR_DIGICERT_DATA_caName",
		},
		{
			name: "DigiCert password and data CA names don't match",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_caName", "$AUTOMDM_VAR_DIGICERT_DATA_caName2", "Name",
				"com.apple.security.pkcs12"),
			errMsg: "Missing $AUTOMDM_VAR_DIGICERT_DATA_caName in the profile",
		},
		{
			name: "DigiCert CA name in another case",
			profile: digiCertForValidation("$AUTOMDM_VAR_DIGICERT_PASSWORD_CANAME", "$AUTOMDM_VAR_DIGICERT_DATA_CANAME", "Name",
				"com.apple.security.pkcs12"),
			errMsg: "AutoMDM variable $AUTOMDM_VAR_DIGICERT_PASSWORD_CANAME does not exist.",
		},
		{
			name: "Custom SCEP CA name in another case",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepname", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepname", "Name",
				"com.apple.security.scep"),
			errMsg: "AutoMDM variable $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepname does not exist.",
		},
		{
			name: "Custom SCEP badCA",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_bad", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_bad", "Name",
				"com.apple.security.scep"),
			errMsg: "_bad does not exist",
		},
		{
			name: "Custom SCEP challenge shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName",
				"$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName is already present in configuration profile",
		},
		{
			name: "Custom SCEP url shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName",
				"$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName is already present in configuration profile",
		},
		{
			name:    "Custom SCEP challenge missing",
			profile: customSCEPForValidation("challenge", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName", "Name", "com.apple.security.scep"),
			errMsg:  "SCEP profile for custom SCEP certificate authority requires: $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables.",
		},
		{
			name: "Custom SCEP url missing",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName", "https://bozo.com", "Name",
				"com.apple.security.scep"),
			errMsg: "SCEP profile for custom SCEP certificate authority requires: $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables.",
		},
		{
			name: "Custom SCEP renewal ID missing",
			profile: strings.Replace(customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName",
				"Name",
				"com.apple.security.scep"), "$AUTOMDM_VAR_SCEP_RENEWAL_ID", "", 1),
			errMsg: "SCEP profile for custom SCEP certificate authority requires: $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables.",
		},
		{
			// This variable was renamed but needs to still validate
			// for back-compat.
			name: "Custom SCEP accepts legacy $AUTOMDM_VAR_SCEP_RENEWAL_ID",
			profile: customSCEPForValidation(
				"$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName",
				"$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName",
				"Name",
				"com.apple.security.scep",
			),
			errMsg: "",
		},
		{
			name: "Custom SCEP challenge and url CA names don't match",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_scepName", "$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName2",
				"Name", "com.apple.security.scep"),
			errMsg: "Missing $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_scepName in the profile",
		},
		{
			name: "NDES challenge shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_NDES_SCEP_CHALLENGE", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL",
				"$AUTOMDM_VAR_NDES_SCEP_CHALLENGE",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE is already present in configuration profile",
		},
		{
			name: "NDES url shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_NDES_SCEP_CHALLENGE", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL",
				"$AUTOMDM_VAR_NDES_SCEP_PROXY_URL",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL is already present in configuration profile",
		},
		{
			name:    "NDES challenge missing",
			profile: customSCEPForValidation("challenge", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL", "Name", "com.apple.security.scep"),
			errMsg:  automdm.NDESSCEPVariablesMissingErrMsg,
		},
		{
			name: "NDES url missing",
			profile: customSCEPForValidation("$AUTOMDM_VAR_NDES_SCEP_CHALLENGE", "https://bozo.com", "Name",
				"com.apple.security.scep"),
			errMsg: automdm.NDESSCEPVariablesMissingErrMsg,
		},
		{
			name: "Conditional access url shows up an extra time",
			profile: customSCEPForValidation("challenge", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL",
				"$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL is already present in configuration profile",
		},
		{
			name: "Conditional access challenge shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL",
				"$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE is already present in configuration profile",
		},
		{
			// A one-time challenge must never be sent to another SCEP server.
			name: "Conditional access challenge without the url",
			profile: customSCEPForValidation("$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE", "https://bozo.com", "Name",
				"com.apple.security.scep"),
			errMsg: automdm.ConditionalAccessSCEPVariablesMissingErrMsg,
		},
		{
			name: "Conditional access renewal ID missing",
			profile: strings.Replace(customSCEPForValidation("challenge", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL",
				"Name", "com.apple.security.scep"), "$AUTOMDM_VAR_SCEP_RENEWAL_ID", "", 1),
			errMsg: automdm.ConditionalAccessSCEPVariablesMissingErrMsg,
		},
		{
			name: "Smallstep badCA",
			profile: customSCEPForValidation("$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_bad", "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_bad", "Name",
				"com.apple.security.scep"),
			errMsg: "_bad does not exist",
		},
		{
			name: "Smallstep challenge shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName", "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName",
				"$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName is already present in configuration profile",
		},
		{
			name: "Smallstep url shows up an extra time",
			profile: customSCEPForValidation("$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName", "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName",
				"$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName",
				"com.apple.security.scep"),
			errMsg: "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName is already present in configuration profile",
		},
		{
			name:    "Smallstep challenge missing",
			profile: customSCEPForValidation("challenge", "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName", "Name", "com.apple.security.scep"),
			errMsg:  "Smallstep certificate authority requires: $AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables.",
		},
		{
			name: "Smallstep url missing",
			profile: customSCEPForValidation("$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName", "https://bozo.com", "Name",
				"com.apple.security.scep"),
			errMsg: "Smallstep certificate authority requires: $AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables.",
		},
		{
			name: "Smallstep challenge and url CA names don't match",
			profile: customSCEPForValidation("$AUTOMDM_VAR_SMALLSTEP_SCEP_CHALLENGE_smallstepName", "$AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName2",
				"Name", "com.apple.security.scep"),
			errMsg: "Missing $AUTOMDM_VAR_SMALLSTEP_SCEP_PROXY_URL_smallstepName in the profile",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pass a premium license for testing (we're not testing license validation here)
			premiumLic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
			err := validateProfileCertificateAuthorityVariables(tc.profile, premiumLic, groupedCAs, caVarValidators{})
			if tc.errMsg != "" {
				require.ErrorContains(t, err, tc.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
