package service

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

// Host-secret placeholders are written only by AutoMDM into the profiles and
// commands it manages; every user-provided content path must refuse them, or a
// profile author could have AutoMDM inject another host secret (recovery lock
// password, unlock token, enroll secret) into a payload of their choosing.
func TestUserProvidedContentRejectsHostSecretPlaceholders(t *testing.T) {
	premium := &automdm.LicenseInfo{Tier: automdm.TierPremium}
	placeholders := []string{
		automdm.HostSecretPlaceholder(automdm.HostSecretEnrollSecret),
		automdm.HostSecretPlaceholder(automdm.HostSecretRecoveryLockPassword),
		"${" + automdm.HostSecretPrefix + automdm.HostSecretMDMUnlockToken + "}",
	}

	validators := map[string]func(contents string) error{
		"apple configuration profile": func(contents string) error {
			_, err := validateConfigProfileAutoMDMVariables(contents, premium, &automdm.GroupedCertificateAuthorities{})
			return err
		},
		"apple declaration": func(contents string) error {
			_, err := validateDeclarationAutoMDMVariables(contents, premium)
			return err
		},
		"windows profile": func(contents string) error {
			_, err := validateWindowsProfileAutoMDMVariables(contents, premium, &automdm.GroupedCertificateAuthorities{})
			return err
		},
	}

	for name, validate := range validators {
		t.Run(name, func(t *testing.T) {
			for _, placeholder := range placeholders {
				err := validate("<string>value: " + placeholder + "</string>")
				require.Error(t, err, placeholder)
				var badRequest *automdm.BadRequestError
				require.ErrorAs(t, err, &badRequest)
				require.Contains(t, err.Error(), "reserved for profiles managed by AutoMDM")
			}

			// content without a placeholder is unaffected, including a legitimate
			// AutoMDM variable and a plain org-wide secret reference
			require.NoError(t, validate("<string>$AUTOMDM_VAR_HOST_UUID $AUTOMDM_SECRET_MY_VALUE</string>"))
			require.NoError(t, validate("<string>no variables here</string>"))
		})
	}
}
