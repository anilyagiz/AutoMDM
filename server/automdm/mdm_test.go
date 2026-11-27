package automdm_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	ctxabm "github.com/anilyagiz/automdm/v4/server/contexts/apple_bm"
	automdmmdm "github.com/anilyagiz/automdm/v4/server/mdm"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	nanodep_client "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/godep"
	"github.com/anilyagiz/automdm/v4/server/mock"
	nanodep_mock "github.com/anilyagiz/automdm/v4/server/mock/nanodep"
	"github.com/stretchr/testify/require"
)

func TestDEPClient(t *testing.T) {
	rxToken := regexp.MustCompile(`oauth_token="(\w+)"`)
	const (
		validToken                 = "OK"
		invalidToken               = "FAIL"
		termsChangedToken          = "TERMS"
		termsChangedAfterAuthToken = "TERMS_AFTER"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			matches := rxToken.FindStringSubmatch(r.Header.Get("Authorization"))
			require.NotNil(t, matches)
			token := matches[1]

			switch token {
			case validToken:
				_, _ = w.Write([]byte(`{"auth_session_token": "ok"}`))
			case termsChangedAfterAuthToken:
				_, _ = w.Write([]byte(`{"auth_session_token": "fail"}`))
			case termsChangedToken:
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code": "T_C_NOT_SIGNED"}`))
			case invalidToken:
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code": "ACCESS_DENIED"}`))
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
			return
		}

		require.Equal(t, "/account", r.URL.Path)
		authSsn := r.Header.Get("X-Adm-Auth-Session")
		if authSsn == "fail" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code": "T_C_NOT_SIGNED"}`))
			return
		}

		// otherwise, return account information, details not important for this
		// test.
		_, _ = w.Write([]byte(`{"admin_id": "test"}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.DiscardHandler)
	ds := new(mock.Store)

	appCfg := automdm.AppConfig{}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &appCfg, nil
	}
	ds.SaveAppConfigFunc = func(ctx context.Context, config *automdm.AppConfig) error {
		appCfg = *config
		return nil
	}

	termsExpiredByOrgName := map[string]bool{
		"org1": false,
		"org2": false,
	}
	ds.SetABMTokenTermsExpiredForOrgNameFunc = func(ctx context.Context, orgName string, expired bool) (wasSet bool, err error) {
		was, ok := termsExpiredByOrgName[orgName]
		if !ok {
			return expired, nil
		}
		termsExpiredByOrgName[orgName] = expired
		return was, nil
	}
	ds.CountABMTokensWithTermsExpiredFunc = func(ctx context.Context) (int, error) {
		count := 0
		for _, expired := range termsExpiredByOrgName {
			if expired {
				count++
			}
		}
		return count, nil
	}

	tokenInvalidByOrgName := map[string]bool{
		"org1": false,
		"org2": false,
	}
	ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (wasSet bool, err error) {
		was, ok := tokenInvalidByOrgName[orgName]
		if !ok {
			return invalid, nil
		}
		tokenInvalidByOrgName[orgName] = invalid
		return was, nil
	}
	ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
		return tokenInvalidByOrgName[orgName], nil
	}

	checkDSCalled := func(readInvoked, writeTokInvoked, writeAppCfgInvoked, writeTokenInvalidInvoked bool) {
		require.Equal(t, readInvoked, ds.AppConfigFuncInvoked)
		require.Equal(t, readInvoked, ds.CountABMTokensWithTermsExpiredFuncInvoked)
		require.Equal(t, writeTokInvoked, ds.SetABMTokenTermsExpiredForOrgNameFuncInvoked)
		require.Equal(t, writeAppCfgInvoked, ds.SaveAppConfigFuncInvoked)
		require.Equal(t, writeTokenInvalidInvoked, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		ds.AppConfigFuncInvoked = false
		ds.CountABMTokensWithTermsExpiredFuncInvoked = false
		ds.SaveAppConfigFuncInvoked = false
		ds.SetABMTokenTermsExpiredForOrgNameFuncInvoked = false
		ds.SetABMTokenInvalidForOrgNameFuncInvoked = false
	}

	cases := []struct {
		token                    string
		orgName                  string
		wantErr                  bool
		readInvoked              bool
		writeTokInvoked          bool
		writeAppCfgInvoked       bool
		writeTokenInvalidInvoked bool
		wantAppCfgTermsFlag      bool
		wantToksTermsFlags       map[string]bool
	}{
		// use a valid token, appconfig should not be updated (already unflagged)
		{
			token: validToken, orgName: "org1", wantErr: false, readInvoked: true, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// use a valid token without org, nothing is checked
		{
			token: validToken, orgName: "", wantErr: false, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// use an invalid token without org, call fails but nothing is checked because this is an unsaved token
		{
			token: invalidToken, orgName: "", wantErr: true, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// use an invalid token, appconfig should not even be read (not a terms error); also not a
		// token_rejected/signature_invalid error, so token_invalid is not touched either
		{
			token: invalidToken, orgName: "org1", wantErr: true, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// terms changed for org1 during the auth request; terms-not-signed is
		// proof the token was accepted, so token_invalid would be cleared, but
		// the write is skipped since it was already false (no-op)
		{
			token: termsChangedToken, orgName: "org1", wantErr: true, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: true, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": true, "org2": false},
		},

		// use of an invalid token does not update the flag
		{
			token: invalidToken, orgName: "org1", wantErr: true, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": true, "org2": false},
		},

		// use of a valid token for org1 resets the flags
		{
			token: validToken, orgName: "org1", wantErr: false, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: true, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// use of a valid token again with org2 does not update anything
		{
			token: validToken, orgName: "org2", wantErr: false, readInvoked: true, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},

		// terms changed for org2 during the actual account request, after auth
		{
			token: termsChangedAfterAuthToken, orgName: "org2", wantErr: true, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: true, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": false, "org2": true},
		},

		// again terms changed after auth for org2, doesn't update appConfig
		{
			token: termsChangedAfterAuthToken, orgName: "org2", wantErr: true, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": false, "org2": true},
		},

		// terms changed during auth for org2, doesn't update appConfig
		{
			token: termsChangedToken, orgName: "org2", wantErr: true, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": false, "org2": true},
		},

		// terms changed during auth for org1, now both tokens have the flag, doesn't update appConfig
		{
			token: termsChangedToken, orgName: "org1", wantErr: true, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": true, "org2": true},
		},

		// use a valid token without org, nothing is checked
		{
			token: validToken, orgName: "", wantErr: false, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": true, "org2": true},
		},

		// use an invalid token without org, call fails but nothing is checked because this is an unsaved token
		{
			token: invalidToken, orgName: "", wantErr: true, readInvoked: false, writeTokInvoked: false,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": true, "org2": true},
		},

		// valid token for org1, resets that token's flag but not appConfig
		{
			token: validToken, orgName: "org1", wantErr: false, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": false, "org2": true},
		},

		// valid token again for org1, still no write to appConfig
		{
			token: validToken, orgName: "org1", wantErr: false, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: false, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: true, wantToksTermsFlags: map[string]bool{"org1": false, "org2": true},
		},

		// valid token again for org2, this time resets appConfig
		{
			token: validToken, orgName: "org2", wantErr: false, readInvoked: true, writeTokInvoked: true,
			writeAppCfgInvoked: true, writeTokenInvalidInvoked: false, wantAppCfgTermsFlag: false, wantToksTermsFlags: map[string]bool{"org1": false, "org2": false},
		},
	}

	// order of calls is important, and test must not be parallelized as it would
	// be racy. For that reason, subtests are not used (it would make it possible
	// to run one subtest in isolation, which could fail).
	for i, c := range cases {
		t.Logf("case %d", i)

		ctx := context.Background()

		store := &nanodep_mock.Storage{}
		store.RetrieveAuthTokensFunc = func(ctx context.Context, name string) (*nanodep_client.OAuth1Tokens, error) {
			return &nanodep_client.OAuth1Tokens{AccessToken: c.token}, nil
		}
		store.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
			return &nanodep_client.Config{BaseURL: srv.URL}, nil
		}

		dep := apple_mdm.NewDEPClient(store, ds, logger)
		orgName := c.orgName
		if orgName == "" {
			// simulate using a new token, not yet saved in the DB, so we pass the
			// token directly in the context
			ctx = ctxabm.NewContext(ctx, &nanodep_client.OAuth1Tokens{AccessToken: c.token})
			orgName = apple_mdm.UnsavedABMTokenOrgName
		}
		res, err := dep.AccountDetail(ctx, orgName)

		if c.wantErr {
			var httpErr *godep.HTTPError
			require.Error(t, err)
			if errors.As(err, &httpErr) {
				require.Equal(t, http.StatusForbidden, httpErr.StatusCode)
			} else {
				var authErr *nanodep_client.AuthError
				require.ErrorAs(t, err, &authErr)
				require.Equal(t, http.StatusForbidden, authErr.StatusCode)
			}
			if c.token == termsChangedToken || c.token == termsChangedAfterAuthToken {
				require.True(t, godep.IsTermsNotSigned(err))
			} else {
				require.False(t, godep.IsTermsNotSigned(err))
			}
		} else {
			require.NoError(t, err)
			require.Equal(t, "test", res.AdminID)
			require.True(t, store.RetrieveAuthTokensFuncInvoked)
			require.True(t, store.RetrieveConfigFuncInvoked)
		}
		checkDSCalled(c.readInvoked, c.writeTokInvoked, c.writeAppCfgInvoked, c.writeTokenInvalidInvoked)
		require.Equal(t, c.wantAppCfgTermsFlag, appCfg.MDM.AppleBMTermsExpired)
		require.Equal(t, c.wantToksTermsFlags, termsExpiredByOrgName)
		// none of these cases produce a token_rejected/signature_invalid error,
		// so token_invalid should never actually flip to true.
		require.Equal(t, map[string]bool{"org1": false, "org2": false}, tokenInvalidByOrgName)
	}
}

func TestMDMAppleBootstrapPackage(t *testing.T) {
	bp := &automdm.MDMAppleBootstrapPackage{
		Token: "abc-def",
	}

	url, err := bp.URL("http://example.com")
	require.NoError(t, err)
	require.Equal(t, "http://example.com/api/latest/automdm/bootstrap?token=abc-def", url)

	url, err = bp.URL(" http://example.com")
	require.Empty(t, url)
	require.Error(t, err)
}

func TestMDMProfileSpecUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name         string
		input        []byte
		expectPath   string
		expectLabels []string
		expectError  bool
	}{
		{
			name:         "empty input",
			input:        []byte(""),
			expectPath:   "",
			expectLabels: nil,
			expectError:  false,
		},
		{
			name:         "new format",
			input:        []byte(`{"path": "testpath", "labels": ["label1", "label2"]}`),
			expectPath:   "testpath",
			expectLabels: []string{"label1", "label2"},
			expectError:  false,
		},
		{
			name:         "old format",
			input:        []byte(`"oldpath"`),
			expectPath:   "oldpath",
			expectLabels: nil,
			expectError:  false,
		},
		{
			name:         "invalid JSON",
			input:        []byte(`{invalid json}`),
			expectPath:   "",
			expectLabels: nil,
			expectError:  true,
		},
		{
			name:         "valid JSON with extra fields",
			input:        []byte(`{"path": "testpath", "labels": ["label1"], "extra": "field"}`),
			expectPath:   "testpath",
			expectLabels: []string{"label1"},
			expectError:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p automdm.MDMProfileSpec
			err := p.UnmarshalJSON(tc.input)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectPath, p.Path)
				require.Equal(t, tc.expectLabels, p.Labels)
			}
		})
	}

	t.Run("complex scenario", func(t *testing.T) {
		var p automdm.MDMProfileSpec
		// test new format
		data := []byte(`{"path": "newpath", "labels": ["label1", "label2"]}`)
		err := p.UnmarshalJSON(data)
		require.NoError(t, err)
		require.Equal(t, "newpath", p.Path)
		require.Equal(t, []string{"label1", "label2"}, p.Labels)

		// test old format
		p = automdm.MDMProfileSpec{}
		data = []byte(`"oldpath"`)
		err = p.UnmarshalJSON(data)
		require.NoError(t, err)
		require.Equal(t, "oldpath", p.Path)
		require.Empty(t, p.Labels)
	})

	t.Run("changing labels", func(t *testing.T) {
		// Decoding into an existing slice reuses its elements, so a key that only the old element
		// has must not survive.
		stored := []automdm.MDMProfileSpec{
			{Path: "some-profile-2", LabelsExcludeAny: []string{"bar"}},
			{Path: "some-profile-1", LabelsIncludeAll: []string{"foo"}},
		}
		incoming := []automdm.MDMProfileSpec{
			{Path: "some-profile-1", LabelsIncludeAll: []string{"foo"}},
			{Path: "some-profile-2", LabelsIncludeAny: []string{"bar"}},
		}
		b, err := json.Marshal(incoming)
		require.NoError(t, err)

		err = json.Unmarshal(b, &stored)
		require.NoError(t, err)

		require.Equal(t, incoming, stored)
		require.Nil(t, stored[0].LabelsExcludeAny) // old key should be removed
		require.Nil(t, stored[1].LabelsIncludeAll) // old key should be removed
	})

	t.Run("legacy string replaces the stored spec", func(t *testing.T) {
		// same reuse as above, with the old plain-path form of an entry
		stored := []automdm.MDMProfileSpec{{
			Path:             "some-profile-1",
			Name:             "Wi-Fi",
			Description:      "Office network",
			LabelsIncludeAll: []string{"foo"},
		}}
		err := json.Unmarshal([]byte(`["some-profile-2"]`), &stored)
		require.NoError(t, err)
		require.Equal(t, []automdm.MDMProfileSpec{{Path: "some-profile-2"}}, stored)
	})
}

func TestHasCAVariables(t *testing.T) {
	tests := []struct {
		name     string
		vars     []string
		expected bool
	}{
		{"empty", nil, false},
		{"no CA vars", []string{string(automdm.AutoMDMVarHostUUID), string(automdm.AutoMDMVarHostHardwareSerial)}, false},
		{"NDES challenge", []string{string(automdm.AutoMDMVarHostUUID), string(automdm.AutoMDMVarNDESSCEPChallenge)}, true},
		{"NDES proxy URL", []string{string(automdm.AutoMDMVarNDESSCEPProxyURL)}, true},
		{"SCEP renewal", []string{string(automdm.AutoMDMVarSCEPRenewalID)}, true},
		{"Certificate renewal (preferred)", []string{string(automdm.AutoMDMVarCertificateRenewalID)}, true},
		{"DigiCert data", []string{string(automdm.AutoMDMVarDigiCertDataPrefix) + "my_ca"}, true},
		{"DigiCert password", []string{string(automdm.AutoMDMVarDigiCertPasswordPrefix) + "my_ca"}, true},
		{"EJBCA data", []string{string(automdm.AutoMDMVarEJBCADataPrefix) + "my_ca"}, true},
		{"EJBCA password", []string{string(automdm.AutoMDMVarEJBCAPasswordPrefix) + "my_ca"}, true},
		{"Custom SCEP challenge", []string{string(automdm.AutoMDMVarCustomSCEPChallengePrefix) + "my_ca"}, true},
		{"Custom SCEP proxy URL", []string{string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix) + "my_ca"}, true},
		{"Smallstep challenge", []string{string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix) + "my_ca"}, true},
		{"Smallstep proxy URL", []string{string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix) + "my_ca"}, true},
		{"Windows SCEP cert ID", []string{string(automdm.AutoMDMVarSCEPWindowsCertificateID)}, true},
		{"Conditional access SCEP URL", []string{string(automdm.AutoMDMVarConditionalAccessSCEPURL)}, true},
		{"mixed with CA", []string{string(automdm.AutoMDMVarHostUUID), string(automdm.AutoMDMVarHostHardwareSerial), string(automdm.AutoMDMVarNDESSCEPChallenge)}, true},
		{"unknown var", []string{"UNKNOWN_VAR"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := automdm.HasCAVariables(tc.vars)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestAutoMDMVarRenewalIDRegexp(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", true},
		{"${AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID}", true},
		{"$AUTOMDM_VAR_SCEP_RENEWAL_ID", true},
		{"${AUTOMDM_VAR_SCEP_RENEWAL_ID}", true},
		{"prefix $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID suffix", true},
		{"$AUTOMDM_VAR_OTHER_VAR", false},
		{"static-value", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			require.Equal(t, tc.want, automdm.AutoMDMVarRenewalIDRegexp.MatchString(tc.input))
		})
	}
}

func TestMDMPlatformSupport(t *testing.T) {
	cases := []struct {
		hostPlatform        string
		wantClassicPlatform string
		wantTurnedOn        bool
	}{
		{"darwin", "darwin", true},
		{"ios", "darwin", true},
		{"ipados", "darwin", true},
		{"windows", "windows", true},
		// Android hosts can have MDM turned on, but they don't take part in the
		// classic MDM command pipeline.
		{"android", "", true},
		// "linux" isn't a hosts.platform value, but it is what
		// Host.AutoMDMPlatform collapses the distros to.
		{"linux", "", false},
		{"ubuntu", "", false},
		{"rhel", "", false},
		{"chrome", "", false},
		{"", "", false},
		{"unknown", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.hostPlatform, func(t *testing.T) {
			require.Equal(t, tc.wantClassicPlatform, automdm.ClassicMDMPlatform(tc.hostPlatform))
			require.Equal(t, tc.wantClassicPlatform != "", automdm.ClassicMDMSupported(tc.hostPlatform))
			require.Equal(t, tc.wantTurnedOn, automdm.MDMTurnedOnSupported(tc.hostPlatform))
		})
	}
}

func TestFilterMacOSOnlyProfilesFromIOSIPadOS(t *testing.T) {
	for _, tc := range []struct {
		profiles         []*automdm.MDMAppleProfilePayload
		expectedProfiles []*automdm.MDMAppleProfilePayload
	}{
		{
			profiles:         []*automdm.MDMAppleProfilePayload{},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{},
		},
		{
			profiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ios",
				},
				{
					ProfileName:  "SomeProfile",
					HostPlatform: "darwin",
				},
				{
					ProfileName:  automdmmdm.AutoMDMdConfigProfileName,
					HostPlatform: "ipados",
				},
				{
					ProfileName:  automdmmdm.AutoMDMdConfigProfileName,
					HostPlatform: "ios",
				},
				{
					ProfileName:  "SomeProfile2",
					HostPlatform: "ios",
				},
				{
					ProfileName:  "SomeProfile3",
					HostPlatform: "ipados",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ipados",
				},
			},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
				{
					ProfileName:  "SomeProfile",
					HostPlatform: "darwin",
				},
				{
					ProfileName:  "SomeProfile2",
					HostPlatform: "ios",
				},
				{
					ProfileName:  "SomeProfile3",
					HostPlatform: "ipados",
				},
			},
		},
		{
			profiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
				{
					ProfileName:  "SomeProfile",
					HostPlatform: "ios",
				},
			},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
				{
					ProfileName:  "SomeProfile",
					HostPlatform: "ios",
				},
			},
		},
		{
			profiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ios",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ipados",
				},
			},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{},
		},
		{
			profiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ios",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ipados",
				},
			},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{},
		},
		{
			profiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ios",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "ipados",
				},
			},
			expectedProfiles: []*automdm.MDMAppleProfilePayload{
				{
					ProfileName:  automdmmdm.AutoMDMFileVaultProfileName,
					HostPlatform: "darwin",
				},
			},
		},
	} {
		actualProfiles := automdm.FilterMacOSOnlyProfilesFromIOSIPadOS(tc.profiles)
		require.Equal(t, len(actualProfiles), len(tc.expectedProfiles))
		for i := 0; i < len(actualProfiles); i++ {
			require.Equal(t, *actualProfiles[i], *tc.expectedProfiles[i])
		}

	}
}

func TestFilterOutUserScopedProfiles(t *testing.T) {
	systemScopedProfile := automdm.MDMAppleProfilePayload{
		ProfileName: automdmmdm.AutoMDMFileVaultProfileName,
		Scope:       automdm.PayloadScopeSystem,
	}
	userScopedProfile := automdm.MDMAppleProfilePayload{
		ProfileName: automdmmdm.AutoMDMCAConfigProfileName,
		Scope:       automdm.PayloadScopeUser,
	}

	profilesToFilter := []*automdm.MDMAppleProfilePayload{&systemScopedProfile, &userScopedProfile}

	filteredProfiles := automdm.FilterOutUserScopedProfiles(profilesToFilter)

	require.ElementsMatch(t, filteredProfiles, []*automdm.MDMAppleProfilePayload{&systemScopedProfile})
}

func TestParseSSORelayState(t *testing.T) {
	for _, initiator := range []string{
		automdm.SSOInitiatorOTAEnroll,
		automdm.SSOInitiatorOrbitSetupExperience,
		automdm.SSOInitiatorAccountDrivenEnroll,
		automdm.SSOInitiatorAppleMDMSSO,
		automdm.SSOInitiatorAutoMDMDesktop,
	} {
		require.Equal(t, automdm.SSORelayState(initiator), automdm.ParseSSORelayState(initiator))
	}

	for _, unknown := range []string{
		"",
		"AUTOMDM_DESKTOP",
		"automdm_desktop ",
		"/device/abc123",
		// The SAML bindings cap relay state at 80 bytes; a longer value is not
		// something a conformant IdP echoed back.
		strings.Repeat("a", 81),
	} {
		require.Empty(t, automdm.ParseSSORelayState(unknown), unknown)
	}
}

func TestValidateMDMProfileName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr string
	}{
		{"Disable camera", ""},
		{"", "can't be empty"},
		{"   ", "can't be empty"},
		{strings.Repeat("a", automdm.MaxProfileNameLength), ""},
		{strings.Repeat("a", automdm.MaxProfileNameLength+1), automdm.MaxProfileNameLengthErrMsg},
		{"Wi-Fi $AUTOMDM_SECRET_PASSWORD", "AUTOMDM_SECRET"},
		{automdmmdm.AutoMDMdConfigProfileName, "not allowed"},
	}
	for _, c := range cases {
		err := automdm.ValidateMDMProfileName(c.name)
		if c.wantErr == "" {
			require.NoError(t, err, c.name)
			continue
		}
		require.ErrorContains(t, err, c.wantErr, c.name)
	}
}

func TestValidateMDMProfileDescription(t *testing.T) {
	require.NoError(t, automdm.ValidateMDMProfileDescription(""))
	require.NoError(t, automdm.ValidateMDMProfileDescription(strings.Repeat("d", automdm.MDMProfileMaxDescriptionLen)))
	require.ErrorContains(t, automdm.ValidateMDMProfileDescription(strings.Repeat("d", automdm.MDMProfileMaxDescriptionLen+1)), "longer than")
}

func TestPayloadDisplayNameFromMobileconfig(t *testing.T) {
	// nested payloads first, as exported by Apple's tools
	mc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
	<key>PayloadContent</key>
	<array><dict>
		<key>PayloadDisplayName</key>
		<string>Nested</string>
		<key>PayloadIdentifier</key>
		<string>com.example.nested</string>
		<key>PayloadType</key>
		<string>com.apple.applicationaccess</string>
		<key>PayloadUUID</key>
		<string>11111111-1111-1111-1111-111111111111</string>
		<key>PayloadVersion</key>
		<integer>1</integer>
	</dict></array>
	<key>PayloadDisplayName</key>
	<string> Top level </string>
	<key>PayloadIdentifier</key>
	<string>com.example.top</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>22222222-2222-2222-2222-222222222222</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict></plist>`)
	require.Equal(t, "Top level", automdm.PayloadDisplayNameFromMobileconfig(mc))
	require.Empty(t, automdm.PayloadDisplayNameFromMobileconfig([]byte("<plist/>")))
	require.Empty(t, automdm.PayloadDisplayNameFromMobileconfig(nil))
}

func TestSanitizeEULAFileName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"terms.md", "terms.md"},
		{"../../terms.md", "terms.md"},
		{`..\..\Startup\run.pdf`, "run.pdf"},
		{"a/", ""},
		{"..", ""},
		{".", ""},
		{"a/..", ""},
		{"", ""},
		{" terms.md\t", "terms.md"},
		{"ter\x00ms\r\n.md", "terms.md"},
		{"terms\u202edm.exe", "termsdm.exe"},
	} {
		require.Equal(t, tc.want, automdm.SanitizeEULAFileName(tc.in), "%q", tc.in)
	}
}

func TestMDMEULAPlatformValidated(t *testing.T) {
	for _, tc := range []struct {
		in   automdm.MDMEULAPlatform
		want automdm.MDMEULAPlatform
	}{
		{"", automdm.MDMEULAPlatformDarwin},
		{automdm.MDMEULAPlatformDarwin, automdm.MDMEULAPlatformDarwin},
		{automdm.MDMEULAPlatformWindows, automdm.MDMEULAPlatformWindows},
	} {
		got, err := tc.in.Validated()
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got)
	}
	for _, in := range []automdm.MDMEULAPlatform{"linux", "Windows", "macos", " darwin"} {
		_, err := in.Validated()
		var invalid *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &invalid, in)
	}
}
