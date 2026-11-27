package service

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWindowsProfileAutoMDMVariablesLicense(t *testing.T) {
	t.Parallel()
	profileWithVars := `<Replace>
			<Item>
				<Target>
					<LocURI>./Device/Vendor/MSFT/Accounts/DomainName</LocURI>
				</Target>
				<Data>Host UUID: $AUTOMDM_VAR_HOST_UUID</Data>
			</Item>
		</Replace>`

	// Test with free license
	freeLic := &automdm.LicenseInfo{Tier: automdm.TierFree}
	_, err := validateWindowsProfileAutoMDMVariables(profileWithVars, freeLic, nil)
	require.ErrorIs(t, err, automdm.ErrMissingLicense)

	// Test with premium license
	premiumLic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
	vars, err := validateWindowsProfileAutoMDMVariables(profileWithVars, premiumLic, nil)
	require.NoError(t, err)
	require.Contains(t, vars, "HOST_UUID")

	// Test profile without variables (should work with free license)
	profileNoVars := `<Replace>
			<Item>
				<Target>
					<LocURI>./Device/Vendor/MSFT/Accounts/DomainName</LocURI>
				</Target>
				<Data>Static Value</Data>
			</Item>
		</Replace>`
	vars, err = validateWindowsProfileAutoMDMVariables(profileNoVars, freeLic, nil)
	require.NoError(t, err)
	require.Nil(t, vars)
}

func TestValidateWindowsProfileAutoMDMVariables(t *testing.T) {
	tests := []struct {
		name        string
		profileXML  string
		wantErr     bool
		errContains string
	}{
		{
			name: "no variables",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>1</Data>
				</Item>
			</Replace>`,
			wantErr: false,
		},
		{
			name: "HOST_UUID variable",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>$AUTOMDM_VAR_HOST_UUID</Data>
				</Item>
			</Replace>`,
			wantErr: false,
		},
		{
			name: "HOST_UUID variable with braces",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>${AUTOMDM_VAR_HOST_UUID}</Data>
				</Item>
			</Replace>`,
			wantErr: false,
		},
		{
			name: "multiple HOST_UUID variables",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>$AUTOMDM_VAR_HOST_UUID-${AUTOMDM_VAR_HOST_UUID}</Data>
				</Item>
			</Replace>`,
			wantErr: false,
		},
		{
			name: "unsupported variable",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>$AUTOMDM_VAR_HOST_FAKE</Data>
				</Item>
			</Replace>`,
			wantErr:     true,
			errContains: "AutoMDM variable $AUTOMDM_VAR_HOST_FAKE is not supported in Windows profiles",
		},
		{
			name: "EJBCA variable",
			profileXML: `<Replace><Item><Target><LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI></Target>` +
				`<Data>$AUTOMDM_VAR_EJBCA_DATA_myCA</Data></Item></Replace>`,
			wantErr:     true,
			errContains: "AutoMDM variable $AUTOMDM_VAR_EJBCA_DATA_myCA is not supported in Windows profiles",
		},
		{
			name: "HOST_UUID with another unsupported variable",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>$AUTOMDM_VAR_HOST_UUID-$AUTOMDM_VAR_BOGUS_VAR</Data>
				</Item>
			</Replace>`,
			wantErr:     true,
			errContains: "AutoMDM variable $AUTOMDM_VAR_BOGUS_VAR is not supported in Windows profiles",
		},
		{
			name: "unknown AutoMDM variable",
			profileXML: `<Replace>
				<Item>
					<Target>
						<LocURI>./Device/Vendor/MSFT/Policy/Config/System/AllowLocation</LocURI>
					</Target>
					<Data>${AUTOMDM_VAR_UNKNOWN_VAR}</Data>
				</Item>
			</Replace>`,
			wantErr:     true,
			errContains: "AutoMDM variable $AUTOMDM_VAR_UNKNOWN_VAR is not supported in Windows profiles",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass a premium license for testing (we're not testing license validation here)
			premiumLic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
			_, err := validateWindowsProfileAutoMDMVariables(tt.profileXML, premiumLic, nil)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// syncMLAddItem builds a SyncML Add item with a LocURI target and Data content.
func syncMLAddItem(locURI, data string) string {
	return fmt.Sprintf(`<Add><Item><Target><LocURI>%s</LocURI></Target><Data>%s</Data></Item></Add>`, locURI, data)
}

// TestValidateWindowsProfileConditionalAccessVariables covers what is specific to the conditional access group on
// Windows: the URL variable marks the profile and must sit in the ServerURL node, and the challenge is either the
// challenge variable or, in profiles written before it existed, a global enroll secret the admin writes in (directly or
// through a secret variable, expanded before validation) that Windows can send. The required-variable rules are in
// TestValidateProfileCertificateAuthorityVariables.
func TestValidateWindowsProfileConditionalAccessVariables(t *testing.T) {
	const scepPrefix = "./User/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_SCEP_WINDOWS_CERTIFICATE_ID/Install/"
	challengeItem := syncMLAddItem(scepPrefix+"Challenge", "aB3+/= secret")
	urlItem := syncMLAddItem(scepPrefix+"ServerURL", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL")
	subjectItem := syncMLAddItem(scepPrefix+"SubjectName", "CN=AutoMDM conditional access for Okta,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID")
	sanItem := syncMLAddItem(scepPrefix+"SubjectAlternativeNames", "7+urn:device:automdm:uuid:$AUTOMDM_VAR_HOST_UUID")
	challengeVarItem := syncMLAddItem(scepPrefix+"Challenge", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE")
	const challengeMsg = `The SCEP certificate's "Challenge" field must contain $AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE`
	const challengeOnlyMsg = `Variable "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE" must only be in the SCEP certificate's "Challenge" field.`

	cases := []struct {
		name        string
		contents    string
		errContains string
	}{
		{
			name:     "valid profile",
			contents: urlItem + challengeItem + subjectItem + sanItem,
		},
		{
			name:     "valid profile with braces syntax",
			contents: syncMLAddItem(scepPrefix+"ServerURL", "${AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL}") + challengeItem + subjectItem + sanItem,
		},
		{
			name:     "valid profile with the challenge variable",
			contents: urlItem + challengeVarItem + subjectItem + sanItem,
		},
		{
			name:     "valid profile with the challenge variable in braces syntax",
			contents: urlItem + syncMLAddItem(scepPrefix+"Challenge", "${AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE}") + subjectItem + sanItem,
		},
		{
			name:        "challenge variable with other text in the Challenge node",
			contents:    urlItem + syncMLAddItem(scepPrefix+"Challenge", "x$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE") + subjectItem + sanItem,
			errContains: challengeMsg,
		},
		{
			name: "challenge variable outside the Challenge node",
			contents: urlItem + challengeItem + sanItem +
				syncMLAddItem(scepPrefix+"SubjectName", "CN=$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID"),
			errContains: challengeOnlyMsg,
		},
		{
			// Each variable can appear only once, so requiring the Challenge item to be exactly the variable keeps it out of
			// every other part of the profile, Meta and attributes included.
			name: "challenge variable in an item's Meta while the Challenge item holds a literal",
			contents: urlItem + challengeItem + subjectItem + sanItem +
				`<Add><Item><Target><LocURI>` + scepPrefix + `KeyLength</LocURI></Target>` +
				`<Meta><Type xmlns="syncml:metinf">$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE</Type></Meta><Data>2048</Data></Item></Add>`,
			errContains: `Variable "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE" must be in the SCEP certificate's "Challenge" field.`,
		},
		{
			name: "challenge variable in a LocURI",
			contents: urlItem + challengeItem + subjectItem +
				syncMLAddItem("./User/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE/Install/KeyProtection", "2"),
			errContains: "Conditional access AutoMDM variables must not appear in LocURI target paths.",
		},
		{
			name:        "challenge with characters windows rejects",
			contents:    urlItem + syncMLAddItem(scepPrefix+"Challenge", "bad_secret") + subjectItem + sanItem,
			errContains: challengeMsg,
		},
		{
			name:        "empty challenge",
			contents:    urlItem + syncMLAddItem(scepPrefix+"Challenge", "") + subjectItem + sanItem,
			errContains: challengeMsg,
		},
		{
			name:        "no challenge item",
			contents:    urlItem + subjectItem + sanItem,
			errContains: challengeMsg,
		},
		{
			name:        "url variable in the Challenge node",
			contents:    syncMLAddItem(scepPrefix+"Challenge", "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL") + subjectItem,
			errContains: `Variable "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL" must only be in the SCEP certificate's "ServerURL" field.`,
		},
		{
			name: "variable in a LocURI",
			contents: challengeItem + subjectItem +
				syncMLAddItem("./User/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL/Install/KeyProtection", "2"),
			errContains: "Conditional access AutoMDM variables must not appear in LocURI target paths.",
		},
		{
			name:        "subject name without the renewal ID",
			contents:    urlItem + challengeItem + syncMLAddItem(scepPrefix+"SubjectName", "CN=x,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID") + syncMLAddItem(scepPrefix+"SubjectName", "CN=y"),
			errContains: "SubjectName item must contain the",
		},
		{
			name:        "url variable only in an XML comment",
			contents:    "<!-- $AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL -->" + challengeItem + subjectItem + sanItem,
			errContains: `Variable "$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL" must be in the SCEP certificate's "ServerURL" field.`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateWindowsProfileAutoMDMVariables(c.contents, &automdm.LicenseInfo{Tier: automdm.TierPremium}, &automdm.GroupedCertificateAuthorities{})
			if c.errContains == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.errContains)
		})
	}
}

func TestAdditionalNDESValidationForWindowsProfiles(t *testing.T) {
	ndesVars := &NDESVarsFound{}
	ndesVars, _ = ndesVars.SetChallenge()
	ndesVars, _ = ndesVars.SetURL()

	// A valid NDES profile with all required fields.
	validProfile := syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
		syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
		syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", "CN=test,OU=$AUTOMDM_VAR_SCEP_RENEWAL_ID")

	tests := []struct {
		name        string
		contents    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid NDES profile",
			contents: validProfile,
		},
		{
			name: "valid NDES profile with braces syntax",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "${AUTOMDM_VAR_NDES_SCEP_CHALLENGE}") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "${AUTOMDM_VAR_NDES_SCEP_PROXY_URL}") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", "CN=test,OU=${AUTOMDM_VAR_SCEP_RENEWAL_ID}"),
		},
		{
			name: "valid NDES profile wrapped in atomic",
			contents: `<Atomic>` +
				`<Add><CmdID>1</CmdID><Item><Target><LocURI>./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge</LocURI></Target>` +
				`<Data>$AUTOMDM_VAR_NDES_SCEP_CHALLENGE</Data></Item></Add>` +
				`<Add><CmdID>2</CmdID><Item><Target><LocURI>./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL</LocURI></Target>` +
				`<Data>$AUTOMDM_VAR_NDES_SCEP_PROXY_URL</Data></Item></Add>` +
				`<Add><CmdID>3</CmdID><Item><Target><LocURI>./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName</LocURI></Target>` +
				`<Data>CN=test,OU=$AUTOMDM_VAR_SCEP_RENEWAL_ID</Data></Item></Add>` +
				`</Atomic>`,
		},
		{
			name: "challenge var in wrong field",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE"),
			wantErr:     true,
			errContains: `must only be in the SCEP certificate's "Challenge" field`,
		},
		{
			name: "challenge var in arbitrary data field",
			contents: syncMLAddItem("./Device/Vendor/MSFT/Something/Else", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE"),
			wantErr:     true,
			errContains: `must only be in the SCEP certificate's "Challenge" field`,
		},
		{
			name: "proxy url var in wrong field",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL"),
			wantErr:     true,
			errContains: `must only be in the SCEP certificate's "ServerURL" field`,
		},
		{
			name: "proxy url var in arbitrary data field",
			contents: syncMLAddItem("./Device/Vendor/MSFT/Something/Else", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL"),
			wantErr:     true,
			errContains: `must only be in the SCEP certificate's "ServerURL" field`,
		},
		{
			name: "challenge var in LocURI target",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_NDES_SCEP_CHALLENGE/Install/Challenge",
				"$AUTOMDM_VAR_NDES_SCEP_CHALLENGE",
			),
			wantErr:     true,
			errContains: "must not appear in LocURI target paths",
		},
		{
			name: "proxy url var in LocURI target",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_NDES_SCEP_PROXY_URL/Install/ServerURL",
				"$AUTOMDM_VAR_NDES_SCEP_PROXY_URL",
			),
			wantErr:     true,
			errContains: "must not appear in LocURI target paths",
		},
		{
			name: "challenge field has wrong value",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "hardcoded-password") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL"),
			wantErr:     true,
			errContains: `must be in the SCEP certificate's "Challenge" field`,
		},
		{
			name: "server url field has wrong value",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "https://hardcoded.example.com"),
			wantErr:     true,
			errContains: `must be in the SCEP certificate's "ServerURL" field`,
		},
		{
			name: "subject name missing renewal id",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", "CN=test"),
			wantErr:     true,
			errContains: subjectNameMissingRenewalIDMsg,
		},
		{
			name: "valid NDES profile with preferred CERTIFICATE_RENEWAL_ID",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", "CN=test,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID"),
		},
		{
			name: "valid NDES profile with preferred CERTIFICATE_RENEWAL_ID (braces syntax)",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "${AUTOMDM_VAR_NDES_SCEP_CHALLENGE}") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "${AUTOMDM_VAR_NDES_SCEP_PROXY_URL}") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", "CN=test,OU=${AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID}"),
		},
		{
			// CDATA reaches the validator undecoded, and the raw "&" in it would fail a plain XML parse.
			name: "valid NDES profile with renewal ID in a CDATA CN",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName",
					"<![CDATA[CN=AT&T $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID]]>"),
		},
		{
			name:     "nil ndes vars returns nil",
			contents: validProfile,
		},
		{
			name: "subject name with trailing whitespace in LocURI is still validated for renewal id",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName ", "CN=test"),
			wantErr:     true,
			errContains: subjectNameMissingRenewalIDMsg,
		},
		{
			name: "challenge with trailing whitespace in LocURI still validates correctly",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge ", "hardcoded-password") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL"),
			wantErr:     true,
			errContains: `must be in the SCEP certificate's "Challenge" field`,
		},
		{
			name: "server url with trailing whitespace in LocURI still validates correctly",
			contents: syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
				syncMLAddItem("./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL ", "https://hardcoded.example.com"),
			wantErr:     true,
			errContains: `must be in the SCEP certificate's "ServerURL" field`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := ndesVars
			if tt.name == "nil ndes vars returns nil" {
				vars = nil
			}
			err := additionalNDESValidationForWindowsProfiles(tt.contents, vars)
			if tt.wantErr {
				require.Error(t, err)
				var badReqErr *automdm.BadRequestError
				require.ErrorAs(t, err, &badReqErr, "expected BadRequestError for: %s", tt.name)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAdditionalCustomSCEPValidationForWindowsProfiles(t *testing.T) {
	t.Parallel()

	customSCEPVars := &CustomSCEPVarsFound{}
	customSCEPVars, _ = customSCEPVars.SetURL("ca1")
	customSCEPVars, _ = customSCEPVars.SetChallenge("ca1")
	customSCEPVars, _ = customSCEPVars.SetRenewalID()

	tests := []struct {
		name        string
		contents    string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid custom SCEP profile",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName",
				"CN=test,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID",
			),
		},
		{
			name: "valid custom SCEP profile with renewal ID in an entity-quoted CN",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName",
				"CN=&quot;Doe, Jane $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID&quot;",
			),
		},
		{
			name: "subject name missing renewal id",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName",
				"CN=test",
			),
			wantErr:     true,
			errContains: subjectNameMissingRenewalIDMsg,
		},
		{
			name: "subject name with trailing whitespace in LocURI is still validated for renewal id",
			contents: syncMLAddItem(
				"./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName ",
				"CN=test",
			),
			wantErr:     true,
			errContains: subjectNameMissingRenewalIDMsg,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := additionalCustomSCEPValidationForWindowsProfiles(tt.contents, customSCEPVars)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				_, isBadRequest := errors.AsType[*automdm.BadRequestError](err)
				require.True(t, isBadRequest, "expected a BadRequestError, got %T", err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateWindowsProfileAutoMDMVariablesRenewalIDPlacement(t *testing.T) {
	t.Parallel()

	ndesProfile := func(subjectName string) string {
		return syncMLAddItem("./User/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/Challenge", "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE") +
			syncMLAddItem("./User/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/ServerURL", "$AUTOMDM_VAR_NDES_SCEP_PROXY_URL") +
			syncMLAddItem("./User/Vendor/MSFT/ClientCertificateInstall/SCEP/cert1/Install/SubjectName", subjectName)
	}
	premiumLic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
	groupedCAs := &automdm.GroupedCertificateAuthorities{NDESSCEP: &automdm.NDESSCEPProxyCA{}}

	tests := []struct {
		name        string
		subjectName string
		errContains string
	}{
		{name: "CN", subjectName: "CN=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID managementAttestation"},
		{
			name:        "both CN and OU",
			subjectName: "CN=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID managementAttestation,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID",
			errContains: renewalIDDuplicateMsg,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateWindowsProfileAutoMDMVariables(ndesProfile(tt.subjectName), premiumLic, groupedCAs)
			if tt.errContains == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.errContains)
		})
	}
}

func TestValidateSubjectNameRenewalID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		subjectName string
		want        bool
		errMsg      string // when want is false; defaults to subjectNameMissingRenewalIDMsg
	}{
		{name: "OU", subjectName: "CN=test,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{name: "OU legacy SCEP_RENEWAL_ID", subjectName: "CN=test,OU=$AUTOMDM_VAR_SCEP_RENEWAL_ID", want: true},
		{name: "OU among other values", subjectName: "CN=test,OU=team $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{
			name:        "CN among other values",
			subjectName: "CN=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID managementAttestation",
			want:        true,
		},
		{name: "CN quoted", subjectName: `CN="$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID, device",O=AutoMDM`, want: true},
		{name: "lowercase keys", subjectName: "cn=test,ou=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{name: "space after separator", subjectName: "CN=test, OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{name: "renewal ID in O", subjectName: "CN=test,O=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: false},
		{name: "renewal ID in O after an ampersand", subjectName: "CN=AT&amp;T;O=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: false},
		{name: "OU in CDATA", subjectName: "<![CDATA[OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID,CN=test]]>", want: true},
		{name: "CN in CDATA with an ampersand", subjectName: "<![CDATA[CN=AT&T $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID]]>", want: true},
		{name: "CN quoted with entities", subjectName: "CN=&quot;a, $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID&quot;,O=AutoMDM", want: true},
		{name: "OU after an encoded separator", subjectName: "CN=test&#44;OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{
			name:        "OU text inside an entity-quoted O value",
			subjectName: "CN=test,O=&quot;OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID&quot;",
			errMsg:      subjectNameEntityQuotedVariableMsg,
		},
		{name: "OU with several CNs", subjectName: "CN=a,CN=b,OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: true},
		{name: "second CN", subjectName: "CN=a,CN=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", errMsg: subjectNameRenewalIDInOneOfManyCNsMsg},
		{name: "malformed XML", subjectName: "CN=AT&T $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", want: false},
		{name: "OU text inside a quoted O value", subjectName: `CN=test,O="OU=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID"`, want: false},
		{name: "different variable", subjectName: "CN=$AUTOMDM_VAR_HOST_UUID,OU=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", want: false},
		{name: "empty", subjectName: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSubjectNameRenewalID(tt.subjectName)
			if tt.want {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, cmp.Or(tt.errMsg, subjectNameMissingRenewalIDMsg))
		})
	}
}

func TestNewMDMWindowsConfigProfileSoftwareUpdate(t *testing.T) {
	// osUpdateSyncML contains the Windows Update install policy LocURI, marking it
	// as a software update profile. otherSyncML is an unrelated policy.
	osUpdateSyncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Update/Install")
	otherSyncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Camera/AllowCamera")

	// configuredSettings returns a WindowsUpdates that reports Configured() == true.
	configuredSettings := func() automdm.WindowsUpdates {
		return automdm.WindowsUpdates{
			DeadlineDays:    optjson.SetInt(7),
			GracePeriodDays: optjson.SetInt(2),
		}
	}

	// setup wires the common mocks required for NewMDMWindowsConfigProfile to reach
	// the software-update handling code.
	setup := func(t *testing.T, premium bool) (automdm.Service, context.Context, *mock.Store) {
		lic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
		if !premium {
			lic = &automdm.LicenseInfo{Tier: automdm.TierFree}
		}
		svc, ctx, ds, _ := setupAppleMDMService(t, lic)
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

		ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
			return nil
		}
		ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
			return &automdm.GroupedCertificateAuthorities{}, nil
		}
		// The existing-profile check and tracking insert now happen inside this
		// datastore call's transaction; the unit test only verifies that the
		// service passes the correct isSoftwareUpdate flag and surfaces errors.
		ds.NewMDMWindowsConfigProfileFunc = func(ctx context.Context, cp automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			cp.ProfileUUID = "w-profile-uuid"
			return &cp, nil
		}
		ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
		) (updates automdm.MDMProfilesUpdates, err error) {
			return automdm.MDMProfilesUpdates{}, nil
		}
		// Team lookup used by the enterprise overrides for teamID > 0 cases.
		ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
			return &automdm.Team{ID: tid, Name: "team1"}, nil
		}
		return svc, ctx, ds
	}

	// appConfigWith builds an AppConfig with Windows MDM enabled (so
	// VerifyMDMWindowsConfigured passes) plus the provided mutation applied for OS
	// update settings.
	appConfigWith := func(apply func(*automdm.AppConfig)) func(context.Context) (*automdm.AppConfig, error) {
		return func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.WindowsEnabledAndConfigured = true
			if apply != nil {
				apply(ac)
			}
			return ac, nil
		}
	}

	t.Run("non software-update profile skips OS update checks", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)
		ds.AppConfigFunc = appConfigWith(nil)

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "other", otherSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.NoError(t, err)
		assert.NotNil(t, p)
		assert.False(t, ds.TeamMDMConfigFuncInvoked)
	})

	t.Run("software-update profile requires premium license", func(t *testing.T) {
		svc, ctx, ds := setup(t, false)
		ds.AppConfigFunc = appConfigWith(nil)

		_, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "other", osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		// The gate fails before the profile is inserted.
		assert.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
		assert.False(t, ds.TeamMDMConfigFuncInvoked)
	})

	t.Run("no team - app config has no OS updates configured", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)
		ds.AppConfigFunc = appConfigWith(nil)

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "os-update", osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.NoError(t, err)
		assert.NotNil(t, p)
		assert.False(t, ds.TeamMDMConfigFuncInvoked)
		assert.True(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("team - team config has no OS updates configured", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)
		ds.AppConfigFunc = appConfigWith(nil)
		ds.TeamMDMConfigFunc = func(ctx context.Context, teamID uint) (*automdm.TeamMDM, error) {
			assert.EqualValues(t, 5, teamID)
			return &automdm.TeamMDM{}, nil
		}

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 5, "os-update", osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.NoError(t, err)
		assert.NotNil(t, p)
		assert.True(t, ds.TeamMDMConfigFuncInvoked)
		assert.True(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("no team - app config has OS updates configured is rejected", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)
		ds.AppConfigFunc = appConfigWith(func(ac *automdm.AppConfig) {
			ac.MDM.WindowsUpdates = configuredSettings()
		})

		_, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "os-update", osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.Error(t, err)
		require.ErrorContains(t, err, automdm.OSUpdatesAlreadyConfiguredErrorMessage)
		// The gate fails before the profile is inserted.
		assert.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("team - team config has OS updates configured is rejected", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)
		ds.AppConfigFunc = appConfigWith(nil)
		ds.TeamMDMConfigFunc = func(ctx context.Context, teamID uint) (*automdm.TeamMDM, error) {
			return &automdm.TeamMDM{WindowsUpdates: configuredSettings()}, nil
		}

		_, err := svc.NewMDMWindowsConfigProfile(ctx, 5, "os-update", osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.Error(t, err)
		require.ErrorContains(t, err, automdm.OSUpdatesAlreadyConfiguredErrorMessage)
		assert.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})
}

func TestNewMDMWindowsConfigProfileLicense(t *testing.T) {
	// For now this only verifies if labels are blocked on free tier
	syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Camera/AllowCamera")

	setup := func(t *testing.T, premium bool) (automdm.Service, context.Context, *mock.Store) {
		lic := &automdm.LicenseInfo{Tier: automdm.TierPremium}
		if !premium {
			lic = &automdm.LicenseInfo{Tier: automdm.TierFree}
		}
		svc, ctx, ds, _ := setupAppleMDMService(t, lic)
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.WindowsEnabledAndConfigured = true
			return ac, nil
		}
		ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
			return nil
		}
		ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
			return &automdm.GroupedCertificateAuthorities{}, nil
		}
		ds.NewMDMWindowsConfigProfileFunc = func(ctx context.Context, cp automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			cp.ProfileUUID = "w-profile-uuid"
			return &cp, nil
		}
		ds.LabelIDsByNameFunc = func(ctx context.Context, labels []string, filter automdm.TeamFilter) (map[string]uint, error) {
			m := make(map[string]uint)
			for i, label := range labels {
				m[label] = uint(i + 1)
			}
			return m, nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			m := make(map[string]*automdm.Label)
			for i, name := range names {
				m[name] = &automdm.Label{ID: uint(i + 1), Name: name}
			}
			return m, nil
		}
		return svc, ctx, ds
	}

	t.Run("labels not allowed on free tier", func(t *testing.T) {
		svc, ctx, ds := setup(t, false)

		_, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "with-labels", syncML, nil, automdm.LabelsIncludeAll, []string{"label1"}, "", false, false)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.ErrorContains(t, err, "Scoping configuration profile")
		assert.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)

		_, err = svc.NewMDMWindowsConfigProfile(ctx, 0, "with-labels", syncML, []string{"label1"}, automdm.LabelsIncludeAll, nil, "", false, false)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.ErrorContains(t, err, "Scoping configuration profile")
		assert.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("profile without labels allowed on free tier", func(t *testing.T) {
		svc, ctx, ds := setup(t, false)

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "without-labels", syncML, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.NoError(t, err)
		assert.NotNil(t, p)
		assert.True(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("labels allowed on premium tier", func(t *testing.T) {
		svc, ctx, ds := setup(t, true)

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "with-labels", syncML, nil, automdm.LabelsIncludeAll, []string{"label1"}, "", false, false)
		require.NoError(t, err)
		assert.NotNil(t, p)
		assert.True(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})
}

// TestNewMDMWindowsConfigProfileSecretVariableChallenge covers a conditional access SCEP profile uploaded on its own
// with its challenge in a secret variable: the challenge check must run on the expanded value, as the batch path does,
// while the stored profile keeps the variable.
func TestNewMDMWindowsConfigProfileSecretVariableChallenge(t *testing.T) {
	const secretVar = "$AUTOMDM_SECRET_OKTA_SCEP_CHALLENGE"
	// A profile written before the challenge variable existed, with a global enroll secret in a secret variable.
	var rendered bytes.Buffer
	require.NoError(t, conditionalAccessWindowsSCEPProfileTemplateParsed.Execute(&rendered, struct {
		CAThumbprintSHA1 string
		CertificateCN    string
	}{CAThumbprintSHA1: strings.Repeat("A", 40), CertificateCN: automdm.ConditionalAccessOktaCertificateCN}))
	profile := bytes.Replace(rendered.Bytes(), []byte(automdm.AutoMDMVarConditionalAccessSCEPChallenge.WithPrefix()), []byte(secretVar), 1)

	setup := func(t *testing.T, secretValue string) (automdm.Service, context.Context, *mock.Store) {
		svc, ctx, ds, _ := setupAppleMDMService(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.WindowsEnabledAndConfigured = true
			return ac, nil
		}
		ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error { return nil }
		ds.ExpandEmbeddedSecretsFunc = func(ctx context.Context, document string) (string, error) {
			return strings.ReplaceAll(document, secretVar, secretValue), nil
		}
		ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
			return &automdm.GroupedCertificateAuthorities{}, nil
		}
		ds.NewMDMWindowsConfigProfileFunc = func(ctx context.Context, cp automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			cp.ProfileUUID = "w-profile-uuid"
			return &cp, nil
		}
		return svc, ctx, ds
	}

	t.Run("secret expands to a challenge windows can send", func(t *testing.T) {
		svc, ctx, ds := setup(t, "aB3+/= secret")

		p, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "okta-scep", profile, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.NoError(t, err)
		require.True(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
		require.Contains(t, string(p.SyncML), secretVar, "stored profile keeps the variable")
		require.NotContains(t, string(p.SyncML), "aB3+/= secret")
	})

	t.Run("secret expands to a challenge windows rejects", func(t *testing.T) {
		svc, ctx, ds := setup(t, "bad_secret")

		_, err := svc.NewMDMWindowsConfigProfile(ctx, 0, "okta-scep", profile, nil, automdm.LabelsIncludeAll, nil, "", false, false)
		require.ErrorContains(t, err, conditionalAccessChallengeInvalidMsg)
		require.False(t, ds.NewMDMWindowsConfigProfileFuncInvoked)
	})
}

func TestUpdateMDMWindowsConfigProfile(t *testing.T) {
	newExistingProfile := func(name string, teamID uint) *automdm.MDMWindowsConfigProfile {
		return &automdm.MDMWindowsConfigProfile{
			ProfileUUID: "w" + uuid.NewString(),
			Name:        name,
			TeamID:      ptr.UintOrNilIfZero(teamID),
		}
	}

	setup := func(t *testing.T, lic *automdm.LicenseInfo) (automdm.Service, context.Context, *mock.Store, *TestServerOpts) {
		svc, ctx, ds, opts := setupAppleMDMService(t, lic)
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.WindowsEnabledAndConfigured = true
			return ac, nil
		}
		ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
			return nil
		}
		ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
			return &automdm.GroupedCertificateAuthorities{}, nil
		}
		ds.TeamWithExtrasFunc = func(ctx context.Context, teamID uint) (*automdm.Team, error) {
			return &automdm.Team{ID: teamID, Name: fmt.Sprintf("team-%d", teamID)}, nil
		}
		ds.LabelIDsByNameFunc = func(ctx context.Context, labels []string, filter automdm.TeamFilter) (map[string]uint, error) {
			m := make(map[string]uint)
			for i, label := range labels {
				m[label] = uint(i + 1) //nolint:gosec // dismiss G115
			}
			return m, nil
		}
		ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
			m := make(map[string]*automdm.Label)
			for i, name := range names {
				m[name] = &automdm.Label{ID: uint(i + 1), Name: name} //nolint:gosec // dismiss G115
			}
			return m, nil
		}

		return svc, ctx, ds, opts
	}

	t.Run("labels-only update, happy path", func(t *testing.T) {
		svc, ctx, ds, opts := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("Test Profile", 0)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			require.Equal(t, existing.ProfileUUID, puid)
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}
		var firedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			firedActivity = activity
			return nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", nil, []string{"label1"}, automdm.LabelsIncludeAny, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)

		assert.Empty(t, updated.SyncML)
		assert.Equal(t, existing.Name, updated.Name)
		require.Len(t, updated.LabelsIncludeAny, 1)
		assert.Equal(t, "label1", updated.LabelsIncludeAny[0].LabelName)

		require.NotNil(t, firedActivity)
		act, ok := firedActivity.(*automdm.ActivityTypeEditedWindowsProfile)
		require.True(t, ok)
		assert.Equal(t, existing.Name, act.ProfileName)
	})

	// No "different name is rejected" test here: there's no request field a
	// client could use to submit a new name, so nothing to reject at this
	// layer -- that check only has meaning in the datastore integration test.
	t.Run("profile content update, matching name", func(t *testing.T) {
		svc, ctx, ds, opts := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("Test Profile", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}
		var firedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			firedActivity = activity
			return nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, syncML, updated.SyncML)
		assert.Equal(t, existing.Name, updated.Name)

		require.NotNil(t, firedActivity)
		act, ok := firedActivity.(*automdm.ActivityTypeEditedWindowsProfile)
		require.True(t, ok)
		assert.Equal(t, existing.Name, act.ProfileName)
	})

	t.Run("uploading a differently named file renames the profile", func(t *testing.T) {
		svc, ctx, ds, opts := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("disable-onedrive", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Firewall/MdmStore/DomainProfile/EnableFirewall")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}
		var firedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			firedActivity = activity
			return nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "enable-firewall", syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, syncML, updated.SyncML)
		assert.Equal(t, "enable-firewall", updated.Name)
		// the profile keeps its identity: a rename is not a delete + recreate
		assert.Equal(t, existing.ProfileUUID, updated.ProfileUUID)

		// the activity has to name the profile as it is now, not as it was
		require.NotNil(t, firedActivity)
		act, ok := firedActivity.(*automdm.ActivityTypeEditedWindowsProfile)
		require.True(t, ok)
		assert.Equal(t, "enable-firewall", act.ProfileName)
	})

	t.Run("a labels-only edit keeps the stored name", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("disable-onedrive", 0)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", nil, []string{"label1"}, automdm.LabelsIncludeAny, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "disable-onedrive", updated.Name)
	})

	t.Run("renaming onto an AutoMDM-reserved name is rejected", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("disable-onedrive", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			return &p, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, mdm.AutoMDMWindowsOSUpdatesProfileName, syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "is not allowed")
		require.False(t, ds.UpdateMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("a file name longer than the name column is rejected", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("disable-onedrive", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			return &p, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, strings.Repeat("a", automdm.MaxProfileNameLength+1), syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, automdm.MaxProfileNameLengthErrMsg)
		require.False(t, ds.UpdateMDMWindowsConfigProfileFuncInvoked)
	})

	t.Run("profile content update for a team-scoped profile", func(t *testing.T) {
		svc, ctx, ds, opts := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("Test Profile", 5)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}
		var firedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			firedActivity = activity
			return nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, syncML, updated.SyncML)
		require.NotNil(t, updated.TeamID)
		assert.EqualValues(t, 5, *updated.TeamID)

		require.NotNil(t, firedActivity)
		act, ok := firedActivity.(*automdm.ActivityTypeEditedWindowsProfile)
		require.True(t, ok)
		require.NotNil(t, act.TeamID)
		assert.EqualValues(t, 5, *act.TeamID)
		require.NotNil(t, act.TeamName)
		assert.Equal(t, "team-5", *act.TeamName)
	})

	t.Run("profile content and labels update atomically in one call", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("Test Profile", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var updated automdm.MDMWindowsConfigProfile
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			updated = p
			return &p, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, syncML, []string{"label1"}, automdm.LabelsIncludeAny, []string{"label2"}, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, syncML, updated.SyncML)
		require.Len(t, updated.LabelsIncludeAny, 1)
		assert.Equal(t, "label1", updated.LabelsIncludeAny[0].LabelName)
		require.Len(t, updated.LabelsExcludeAny, 1)
		assert.Equal(t, "label2", updated.LabelsExcludeAny[0].LabelName)
	})

	t.Run("editing an AutoMDM-managed profile is rejected", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile(mdm.AutoMDMWindowsOSUpdatesProfileName, 0)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			t.Fatal("should not reach the datastore update")
			return nil, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", nil, []string{"label1"}, automdm.LabelsIncludeAny, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "managed by AutoMDM")
	})

	t.Run("nonexistent profile propagates the not-found error", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		wantErr := errors.New("simulated profile lookup error")
		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return nil, wantErr
		}

		err := svc.UpdateMDMConfigProfile(ctx, "w"+uuid.NewString(), "", nil, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("labels require a premium license, content-only edits do not", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierFree})
		existing := newExistingProfile("Test Profile", 0)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			t.Fatal("should not reach the datastore update")
			return nil, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", nil, []string{"label1"}, automdm.LabelsIncludeAny, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.ErrorContains(t, err, "Scoping configuration profiles with labels requires AutoMDM Premium license")

		// content-only edit (no labels) still succeeds on a free license
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			return &p, nil
		}
		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")
		err = svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
	})

	t.Run("team-scoped update on a free license returns a license error", func(t *testing.T) {
		// team profiles can survive a premium-to-free downgrade; the update
		// must fail with a license error, not panic on the nil
		// EnterpriseOverrides that free servers never populate.
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierFree})
		existing := newExistingProfile("Test Profile", 5)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			t.Fatal("should not reach the datastore update")
			return nil, nil
		}

		syncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Bluetooth/AllowDiscoverableMode")
		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)

		err = svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, "", nil, []string{"label1"}, automdm.LabelsIncludeAny, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
	})

	t.Run("AutoMDM variables used in the upload are threaded through to the datastore call", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
		existing := newExistingProfile("Test Profile", 0)
		syncML := syncMLForTest("./Device/Vendor/MSFT/Accounts/DomainName")
		syncML = append(syncML, []byte("$AUTOMDM_VAR_HOST_UUID")...)

		ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
			return existing, nil
		}
		var capturedVars []automdm.AutoMDMVarName
		ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
			capturedVars = usesAutoMDMVars
			return &p, nil
		}

		err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, syncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.NoError(t, err)
		assert.Contains(t, capturedVars, automdm.AutoMDMVarName("HOST_UUID"))
	})

	t.Run("OS-update profile restrictions apply on update the same as on create", func(t *testing.T) {
		osUpdateSyncML := syncMLForTest("./Device/Vendor/MSFT/Policy/Config/Update/Install")

		t.Run("update succeeds when OS updates are not already configured", func(t *testing.T) {
			svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
			existing := newExistingProfile("Test Profile", 0)

			ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
				return existing, nil
			}
			var updated automdm.MDMWindowsConfigProfile
			ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
				updated = p
				return &p, nil
			}

			err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, osUpdateSyncML, updated.SyncML)
		})

		t.Run("update is rejected when OS updates are already configured", func(t *testing.T) {
			svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
			existing := newExistingProfile("Test Profile", 0)

			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				ac := &automdm.AppConfig{}
				ac.MDM.WindowsEnabledAndConfigured = true
				ac.MDM.WindowsUpdates = automdm.WindowsUpdates{
					DeadlineDays:    optjson.SetInt(7),
					GracePeriodDays: optjson.SetInt(2),
				}
				return ac, nil
			}
			ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
				return existing, nil
			}
			ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
				t.Fatal("should not reach the datastore update")
				return nil, nil
			}

			err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
			require.Error(t, err)
			assert.ErrorContains(t, err, automdm.OSUpdatesAlreadyConfiguredErrorMessage)
		})

		t.Run("update requires a premium license", func(t *testing.T) {
			svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierFree})
			existing := newExistingProfile("Test Profile", 0)

			ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
				return existing, nil
			}
			ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
				t.Fatal("should not reach the datastore update")
				return nil, nil
			}

			err := svc.UpdateMDMConfigProfile(ctx, existing.ProfileUUID, existing.Name, osUpdateSyncML, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
			require.ErrorIs(t, err, automdm.ErrMissingLicense)
		})
	})

	t.Run("fails if Windows MDM is not configured", func(t *testing.T) {
		svc, ctx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierFree})
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.WindowsEnabledAndConfigured = false
			return ac, nil
		}
		err := svc.UpdateMDMConfigProfile(ctx, "wsome-uuid", "", nil, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Windows MDM isn't turned on.")
	})

	t.Run("authorization outcome matches user role and team membership", func(t *testing.T) {
		testCases := []struct {
			name             string
			user             *automdm.User
			shouldFailGlobal bool
			shouldFailTeam   bool
		}{
			{"global admin", &automdm.User{GlobalRole: new(automdm.RoleAdmin)}, false, false},
			{"global maintainer", &automdm.User{GlobalRole: new(automdm.RoleMaintainer)}, false, false},
			{"global observer", &automdm.User{GlobalRole: new(automdm.RoleObserver)}, true, true},
			{"team admin, belongs to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}, true, false},
			{"team admin, DOES NOT belong to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}}, true, true},
			{"team maintainer, belongs to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}}, true, false},
			{"team maintainer, DOES NOT belong to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}}, true, true},
			{"team observer, belongs to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}, true, true},
			{"team observer, DOES NOT belong to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}}, true, true},
			{"user no roles", &automdm.User{ID: 1337}, true, true},
		}

		checkShouldFail := func(t *testing.T, err error, shouldFail bool) {
			t.Helper()
			if !shouldFail {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)
			}
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				svc, baseCtx, ds, _ := setup(t, &automdm.LicenseInfo{Tier: automdm.TierPremium})
				ctx := viewer.NewContext(baseCtx, viewer.Viewer{User: tt.user})

				noTeamProfile := newExistingProfile("No Team Profile", 0)
				teamProfile := newExistingProfile("Team Profile", 1)

				ds.GetMDMWindowsConfigProfileFunc = func(ctx context.Context, puid string) (*automdm.MDMWindowsConfigProfile, error) {
					if puid == noTeamProfile.ProfileUUID {
						return noTeamProfile, nil
					}
					return teamProfile, nil
				}
				ds.UpdateMDMWindowsConfigProfileFunc = func(ctx context.Context, p automdm.MDMWindowsConfigProfile, usesAutoMDMVars []automdm.AutoMDMVarName) (*automdm.MDMWindowsConfigProfile, error) {
					return &p, nil
				}

				// profile content and labels are deliberately nil/empty here --
				// this isolates the authz checks from content/label validation.
				err := svc.UpdateMDMConfigProfile(ctx, noTeamProfile.ProfileUUID, "", nil, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
				checkShouldFail(t, err, tt.shouldFailGlobal)

				err = svc.UpdateMDMConfigProfile(ctx, teamProfile.ProfileUUID, "", nil, nil, automdm.LabelsIncludeAll, nil, optjson.Slice[byte]{}, nil, nil, nil)
				checkShouldFail(t, err, tt.shouldFailTeam)
			})
		}
	})
}
