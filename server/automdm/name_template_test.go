package automdm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/stretchr/testify/require"
)

func TestValidateHostNameTemplate(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		wantNorm string // expected normalized template on success
		wantErr  string
	}{
		{name: "plain string", tmpl: "workstation", wantNorm: "workstation"},
		{name: "hardware serial", tmpl: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", wantNorm: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"},
		{name: "uuid braced", tmpl: "${AUTOMDM_VAR_HOST_UUID}", wantNorm: "${AUTOMDM_VAR_HOST_UUID}"},
		{name: "platform", tmpl: "$AUTOMDM_VAR_HOST_PLATFORM", wantNorm: "$AUTOMDM_VAR_HOST_PLATFORM"},
		// IdP end-user variables are supported.
		{name: "idp username", tmpl: "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME", wantNorm: "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"},
		{name: "idp groups braced", tmpl: "${AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS}", wantNorm: "${AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS}"},
		{name: "idp full name", tmpl: "$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME", wantNorm: "$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME"},
		{name: "idp department", tmpl: "$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", wantNorm: "$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT"},
		{name: "idp external id", tmpl: "$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID", wantNorm: "$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID"},
		{name: "idp username local part", tmpl: "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART", wantNorm: "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART"},
		{
			name:     "mixed",
			tmpl:     "mac-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-${AUTOMDM_VAR_HOST_UUID}",
			wantNorm: "mac-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-${AUTOMDM_VAR_HOST_UUID}",
		},
		{
			name:     "surrounding whitespace is trimmed in the returned value",
			tmpl:     "  serial-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL  ",
			wantNorm: "serial-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL",
		},

		{name: "empty", tmpl: "", wantErr: "can't be empty"},
		{name: "whitespace only", tmpl: "   ", wantErr: "can't be empty"},
		{
			name:    "unsupported CA variable",
			tmpl:    "$AUTOMDM_VAR_NDES_SCEP_CHALLENGE",
			wantErr: "AutoMDM variable $AUTOMDM_VAR_NDES_SCEP_CHALLENGE is not supported in host name templates.",
		},
		{
			name:    "unsupported DigiCert variable",
			tmpl:    "$AUTOMDM_VAR_DIGICERT_DATA_MYCA",
			wantErr: "is not supported in host name templates.",
		},
		{
			name:    "deprecated legacy idp email var is not supported",
			tmpl:    "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP",
			wantErr: "is not supported in host name templates.",
		},
		{
			name:    "supported var with unsupported suffix",
			tmpl:    "$AUTOMDM_VAR_HOST_UUID_EXTRA",
			wantErr: "AutoMDM variable $AUTOMDM_VAR_HOST_UUID_EXTRA is not supported in host name templates.",
		},
		// Custom (secret) variables are allowed syntactically; their existence is
		// checked separately (needs the datastore).
		{name: "secret var", tmpl: "$AUTOMDM_SECRET_FOO", wantNorm: "$AUTOMDM_SECRET_FOO"},
		{name: "secret var braced", tmpl: "${AUTOMDM_SECRET_FOO}", wantNorm: "${AUTOMDM_SECRET_FOO}"},
		{
			name:     "built-in and secret vars mixed",
			tmpl:     "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-$AUTOMDM_SECRET_SITE",
			wantNorm: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-$AUTOMDM_SECRET_SITE",
		},
		{
			// A long secret token doesn't count toward the fixed-text byte floor,
			// since the secret can resolve to an empty value.
			name:     "long secret token doesn't hit byte floor",
			tmpl:     "WS-${AUTOMDM_SECRET_" + strings.Repeat("A", 80) + "}",
			wantNorm: "WS-${AUTOMDM_SECRET_" + strings.Repeat("A", 80) + "}",
		},
		// Custom host vital references are allowed syntactically here too; their
		// existence is checked separately (needs the datastore), same as secrets.
		{name: "vital var", tmpl: "$AUTOMDM_HOST_VITAL_5", wantNorm: "$AUTOMDM_HOST_VITAL_5"},
		{name: "vital var braced", tmpl: "${AUTOMDM_HOST_VITAL_5}", wantNorm: "${AUTOMDM_HOST_VITAL_5}"},
		{
			name:     "built-in, secret, and vital vars mixed",
			tmpl:     "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-$AUTOMDM_SECRET_SITE-$AUTOMDM_HOST_VITAL_5",
			wantNorm: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-$AUTOMDM_SECRET_SITE-$AUTOMDM_HOST_VITAL_5",
		},
		{
			// A long vital token doesn't count toward the fixed-text byte floor
			// either: the substituted value's length isn't known until resolve time.
			name:     "long vital token doesn't hit byte floor",
			tmpl:     "WS-${AUTOMDM_HOST_VITAL_" + strings.Repeat("9", 80) + "}",
			wantNorm: "WS-${AUTOMDM_HOST_VITAL_" + strings.Repeat("9", 80) + "}",
		},
		{
			// A malformed vital reference (non-numeric suffix) is allowed
			// syntactically here; ValidateHostNameTemplateWithSecrets rejects it once
			// the datastore is available to distinguish malformed from unknown IDs.
			name:     "malformed vital ref allowed at the syntax layer",
			tmpl:     "$AUTOMDM_HOST_VITAL_asset_tag",
			wantNorm: "$AUTOMDM_HOST_VITAL_asset_tag",
		},
		{name: "tab control char", tmpl: "bad\tname", wantErr: "control characters"},
		{name: "rtl override format char", tmpl: "bad\u202ename", wantErr: "control characters"},
		{name: "zero-width joiner format char", tmpl: "bad\u200dname", wantErr: "control characters"},
		{name: "invalid utf-8", tmpl: "bad\xff\xfename", wantErr: "valid UTF-8"},
		// The 255-char cap is on the whole template string and counts runes, not
		// bytes; it fires before the byte-floor check below.
		{name: "template over 255 chars", tmpl: strings.Repeat("a", 256), wantErr: "255 characters"},
		{name: "256 multi-byte runes too long", tmpl: strings.Repeat("é", 256), wantErr: "255 characters"},
		// A resolved name can't exceed the device-name byte limit, so a template
		// whose fixed text alone already exceeds it is rejected at save time.
		{name: "literal at 63-byte limit ok", tmpl: strings.Repeat("a", 63), wantNorm: strings.Repeat("a", 63)},
		{name: "literal over 63 bytes", tmpl: strings.Repeat("a", 64), wantErr: "63 bytes"},
		// The floor is bytes, not runes: 32 two-byte runes is 64 bytes.
		{name: "multi-byte literal over 63 bytes", tmpl: strings.Repeat("é", 32), wantErr: "63 bytes"},
		// Only the fixed text counts toward the floor — a short literal plus a
		// (longer) variable token is fine.
		{name: "short literal with variable ok", tmpl: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", wantNorm: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			norm, err := ValidateHostNameTemplate(c.tmpl)
			if c.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, c.wantNorm, norm)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), c.wantErr)
			require.Empty(t, norm)
		})
	}
}

func TestResolveHostNameTemplate(t *testing.T) {
	host := &Host{
		HardwareSerial: "C02ABC123",
		UUID:           "1234-5678",
		Platform:       "darwin",
	}

	cases := []struct {
		name string
		tmpl string
		host *Host
		want string
	}{
		{name: "no vars", tmpl: "workstation", host: host, want: "workstation"},
		{name: "serial", tmpl: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", host: host, want: "C02ABC123"},
		{name: "uuid braced", tmpl: "${AUTOMDM_VAR_HOST_UUID}", host: host, want: "1234-5678"},
		{name: "platform darwin maps to macOS", tmpl: "$AUTOMDM_VAR_HOST_PLATFORM", host: host, want: "macOS"},
		{
			name: "platform ios maps to iOS",
			tmpl: "$AUTOMDM_VAR_HOST_PLATFORM",
			host: &Host{Platform: "ios"},
			want: "iOS",
		},
		{
			name: "platform ipados maps to iPadOS",
			tmpl: "$AUTOMDM_VAR_HOST_PLATFORM",
			host: &Host{Platform: "ipados"},
			want: "iPadOS",
		},
		{
			name: "mixed and repeated",
			tmpl: "$AUTOMDM_VAR_HOST_PLATFORM-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL-${AUTOMDM_VAR_HOST_HARDWARE_SERIAL}",
			host: host,
			want: "macOS-C02ABC123-C02ABC123",
		},
		{
			// IdP variables need a datastore lookup, so ResolveHostNameTemplate must
			// leave them untouched (they're resolved separately in the service layer)
			// while still resolving the identity variables around them.
			name: "idp tokens left untouched, identity resolved",
			tmpl: "u=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME;lp=${AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART};s=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL",
			host: host,
			want: "u=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME;lp=${AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART};s=C02ABC123",
		},
		{
			// Non-Apple platforms fall back to the raw value (the feature only
			// applies to Apple devices, so this is defensive).
			name: "non-apple platform falls back to raw value",
			tmpl: "$AUTOMDM_VAR_HOST_PLATFORM",
			host: &Host{Platform: "windows"},
			want: "windows",
		},
		{
			name: "empty host field resolves to empty string",
			tmpl: "serial=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL",
			host: &Host{},
			want: "serial=",
		},
		{
			// A host value that itself contains variable syntax must not be
			// re-substituted by a later pass (single-pass replacement).
			name: "host value containing variable syntax is not re-substituted",
			tmpl: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL",
			host: &Host{HardwareSerial: "$AUTOMDM_VAR_HOST_UUID", UUID: "real-uuid"},
			want: "$AUTOMDM_VAR_HOST_UUID",
		},
		{
			name: "unsupported longer variable name is left untouched",
			tmpl: "$AUTOMDM_VAR_HOST_UUID_EXTRA",
			host: host,
			want: "$AUTOMDM_VAR_HOST_UUID_EXTRA",
		},
		{name: "nil host leaves template unchanged", tmpl: "$AUTOMDM_VAR_HOST_UUID", host: nil, want: "$AUTOMDM_VAR_HOST_UUID"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ResolveHostNameTemplate(c.tmpl, c.host))
		})
	}
}

func TestTeamHostNameTemplateRoundTrip(t *testing.T) {
	t.Run("TeamMDM", func(t *testing.T) {
		in := TeamMDM{HostNameTemplate: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"}
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.Contains(t, string(b), `"name_template":"$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"`)

		var out TeamMDM
		require.NoError(t, json.Unmarshal(b, &out))
		require.Equal(t, in.HostNameTemplate, out.HostNameTemplate)
	})

	t.Run("TeamSpecMDM", func(t *testing.T) {
		in := TeamSpecMDM{HostNameTemplate: optjson.SetString("$AUTOMDM_VAR_HOST_UUID")}
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.Contains(t, string(b), `"name_template":"$AUTOMDM_VAR_HOST_UUID"`)

		var out TeamSpecMDM
		require.NoError(t, json.Unmarshal(b, &out))
		require.True(t, out.HostNameTemplate.Set)
		require.True(t, out.HostNameTemplate.Valid)
		require.Equal(t, "$AUTOMDM_VAR_HOST_UUID", out.HostNameTemplate.Value)
	})

	t.Run("TeamSpecMDM absent", func(t *testing.T) {
		var out TeamSpecMDM
		require.NoError(t, json.Unmarshal([]byte(`{}`), &out))
		require.False(t, out.HostNameTemplate.Set)
	})

	t.Run("TeamPayloadMDM", func(t *testing.T) {
		in := TeamPayloadMDM{HostNameTemplate: optjson.SetString("$AUTOMDM_VAR_HOST_PLATFORM")}
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.Contains(t, string(b), `"name_template":"$AUTOMDM_VAR_HOST_PLATFORM"`)

		var out TeamPayloadMDM
		require.NoError(t, json.Unmarshal(b, &out))
		require.True(t, out.HostNameTemplate.Set)
		require.Equal(t, "$AUTOMDM_VAR_HOST_PLATFORM", out.HostNameTemplate.Value)
	})

	t.Run("TeamConfig storage round-trip", func(t *testing.T) {
		// name_template rides in the teams.config JSON blob (no dedicated
		// column), so verify it survives the actual SQL Value()/Scan() boundary.
		in := TeamConfig{MDM: TeamMDM{HostNameTemplate: "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"}}
		val, err := in.Value()
		require.NoError(t, err)

		var out TeamConfig
		require.NoError(t, out.Scan(val))
		require.Equal(t, "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", out.MDM.HostNameTemplate)
	})
}

func TestActivityTypeEditedHostNameTemplate(t *testing.T) {
	require.Equal(t, "edited_host_name_template", ActivityTypeEditedHostNameTemplate{}.ActivityName())

	t.Run("marshal with template", func(t *testing.T) {
		tmpl := "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"
		b, err := json.Marshal(ActivityTypeEditedHostNameTemplate{
			AutoMDMID:        new(uint(1)),
			AutoMDMName:      new("Workstations"),
			HostNameTemplate: &tmpl,
		})
		require.NoError(t, err)
		require.JSONEq(t, `{
			"team_id": 1,
			"team_name": "Workstations",
			"name_template": "$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"
		}`, string(b))
	})

	t.Run("marshal cleared template is null", func(t *testing.T) {
		b, err := json.Marshal(ActivityTypeEditedHostNameTemplate{
			AutoMDMID:   new(uint(1)),
			AutoMDMName: new("Workstations"),
		})
		require.NoError(t, err)
		require.JSONEq(t, `{
			"team_id": 1,
			"team_name": "Workstations",
			"name_template": null
		}`, string(b))
	})
}

// nameTemplateDS stubs the two validation methods the validator calls.
type nameTemplateDS struct {
	Datastore
	secretsChecked bool
	secretsErr     error
}

func (d *nameTemplateDS) ValidateEmbeddedSecrets(context.Context, []string) error {
	d.secretsChecked = true
	return d.secretsErr
}

func (d *nameTemplateDS) ValidateReferencedCustomHostVitals(context.Context, []string) error {
	return nil
}

func TestValidateHostNameTemplateSecretScope(t *testing.T) {
	cases := []struct {
		name                string
		tmpl                string
		canReferenceSecrets bool
		secretsErr          error
		wantErr             string
		wantSecretsChecked  bool
	}{
		{
			name: "secret allowed for a caller who can write secrets", tmpl: "WS-$AUTOMDM_SECRET_SITE",
			canReferenceSecrets: true, wantSecretsChecked: true,
		},
		{
			name: "secret rejected for a caller who cannot", tmpl: "WS-$AUTOMDM_SECRET_SITE",
			wantErr: "$AUTOMDM_SECRET_SITE can only be used in a host name template by a global admin, maintainer, or GitOps user",
		},
		{
			name: "braced form is not a bypass", tmpl: "WS-${AUTOMDM_SECRET_SITE}",
			wantErr: "$AUTOMDM_SECRET_SITE can only be used",
		},
		{
			name: "first referenced secret is named", tmpl: "$AUTOMDM_SECRET_ALPHA-$AUTOMDM_SECRET_BETA",
			wantErr: "$AUTOMDM_SECRET_ALPHA can only be used",
		},
		{
			// Same error as a defined secret, so it isn't an existence oracle.
			name: "undefined secret still gives the scope error", tmpl: "WS-$AUTOMDM_SECRET_NOPE",
			secretsErr: MissingSecretsError{MissingSecrets: []string{"NOPE"}},
			wantErr:    "$AUTOMDM_SECRET_NOPE can only be used",
		},
		{
			name: "built-in variables are not gated", tmpl: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL",
		},
		{
			name: "custom host vitals are not gated", tmpl: "WS-$AUTOMDM_HOST_VITAL_asset_tag",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := &nameTemplateDS{secretsErr: c.secretsErr}
			norm, err := ValidateHostNameTemplateWithSecrets(context.Background(), ds, c.tmpl, c.canReferenceSecrets)
			if c.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, c.tmpl, norm)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), c.wantErr)
				var invalid *InvalidArgumentError
				require.ErrorAs(t, err, &invalid)
				require.Empty(t, norm)
			}
			require.Equal(t, c.wantSecretsChecked, ds.secretsChecked)
		})
	}
}
