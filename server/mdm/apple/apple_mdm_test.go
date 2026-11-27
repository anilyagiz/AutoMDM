package apple_mdm

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/godep"
	"github.com/anilyagiz/automdm/v4/server/mock"
	nanodep_mock "github.com/anilyagiz/automdm/v4/server/mock/nanodep"
	"github.com/micromdm/plist"
	"github.com/smallstep/pkcs7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDEPService(t *testing.T) {
	t.Run("EnsureDefaultSetupAssistant", func(t *testing.T) {
		ds := new(mock.Store)
		ctx := context.Background()
		logger := slog.New(slog.DiscardHandler)
		depStorage := new(nanodep_mock.Storage)
		depSvc := NewDEPService(ds, depStorage, logger)
		defaultProfile := depSvc.GetDefaultProfile()
		serverURL := "https://example.com/"

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			switch r.URL.Path {
			case "/session":
				_, _ = w.Write([]byte(`{"auth_session_token": "xyz"}`))
			case "/profile":
				_, _ = w.Write([]byte(`{"profile_uuid": "abcd"}`))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var got godep.Profile
				err = json.Unmarshal(body, &got)
				require.NoError(t, err)
				require.Contains(t, got.URL, serverURL+"api/mdm/apple/enroll?token=")
				assert.Empty(t, got.ConfigurationWebURL)
				got.URL = ""
				got.ConfigurationWebURL = ""
				defaultProfile.AwaitDeviceConfigured = true // this is now always set to true
				require.Equal(t, defaultProfile, &got)
			default:
				require.Fail(t, "unexpected path: %s", r.URL.Path)
			}
		}))
		t.Cleanup(srv.Close)

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			appCfg := &automdm.AppConfig{}
			appCfg.ServerSettings.ServerURL = serverURL
			return appCfg, nil
		}

		var savedProfile *automdm.MDMAppleEnrollmentProfile
		ds.NewMDMAppleEnrollmentProfileFunc = func(ctx context.Context, p automdm.MDMAppleEnrollmentProfilePayload) (*automdm.MDMAppleEnrollmentProfile, error) {
			require.Equal(t, automdm.MDMAppleEnrollmentTypeAutomatic, p.Type)
			require.NotEmpty(t, p.Token)
			res := &automdm.MDMAppleEnrollmentProfile{
				Token:      p.Token,
				Type:       p.Type,
				DEPProfile: p.DEPProfile,
				UpdateCreateTimestamps: automdm.UpdateCreateTimestamps{
					UpdateTimestamp: automdm.UpdateTimestamp{UpdatedAt: time.Now()},
				},
			}
			savedProfile = res
			return res, nil
		}

		ds.GetMDMAppleEnrollmentProfileByTypeFunc = func(ctx context.Context, typ automdm.MDMAppleEnrollmentType) (*automdm.MDMAppleEnrollmentProfile, error) {
			require.Equal(t, automdm.MDMAppleEnrollmentTypeAutomatic, typ)
			if savedProfile == nil {
				return nil, notFoundError{}
			}
			return savedProfile, nil
		}

		var defaultProfileUUID string
		ds.GetMDMAppleDefaultSetupAssistantFunc = func(ctx context.Context, teamID *uint, orgName string) (profileUUID string, updatedAt time.Time, err error) {
			if defaultProfileUUID == "" {
				return "", time.Time{}, nil
			}
			return defaultProfileUUID, time.Now(), nil
		}

		ds.SetMDMAppleDefaultSetupAssistantProfileUUIDFunc = func(ctx context.Context, teamID *uint, profileUUID, orgName string) error {
			require.Nil(t, teamID)
			defaultProfileUUID = profileUUID
			return nil
		}

		ds.SaveAppConfigFunc = func(ctx context.Context, info *automdm.AppConfig) error {
			return nil
		}

		depStorage.RetrieveConfigFunc = func(ctx context.Context, name string) (*client.Config, error) {
			return &client.Config{BaseURL: srv.URL}, nil
		}

		depStorage.RetrieveAuthTokensFunc = func(ctx context.Context, name string) (*client.OAuth1Tokens, error) {
			return &client.OAuth1Tokens{}, nil
		}

		depStorage.StoreAssignerProfileFunc = func(ctx context.Context, name string, profileUUID string) error {
			require.NotEmpty(t, profileUUID)
			return nil
		}

		ds.GetABMTokenOrgNamesAssociatedWithTeamFunc = func(ctx context.Context, teamID *uint) ([]string, error) {
			return []string{"org1"}, nil
		}

		ds.CountABMTokensWithTermsExpiredFunc = func(ctx context.Context) (int, error) {
			return 0, nil
		}

		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return true, nil
		}

		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			require.Equal(t, "org1", orgName)
			require.False(t, invalid)
			return false, nil
		}

		profUUID, modTime, err := depSvc.EnsureDefaultSetupAssistant(ctx, nil, "org1")
		require.NoError(t, err)
		require.Equal(t, "abcd", profUUID)
		require.NotZero(t, modTime)
		require.True(t, ds.NewMDMAppleEnrollmentProfileFuncInvoked)
		require.True(t, ds.GetMDMAppleEnrollmentProfileByTypeFuncInvoked)
		require.True(t, ds.GetMDMAppleDefaultSetupAssistantFuncInvoked)
		require.True(t, ds.SetMDMAppleDefaultSetupAssistantProfileUUIDFuncInvoked)
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		require.True(t, depStorage.RetrieveConfigFuncInvoked)
		require.False(t, depStorage.StoreAssignerProfileFuncInvoked) // not used anymore
	})

	t.Run("EnrollURL", func(t *testing.T) {
		const serverURL = "https://example.com/"

		appCfg := &automdm.AppConfig{}
		appCfg.ServerSettings.ServerURL = serverURL
		url, err := EnrollURL("token", appCfg)
		require.NoError(t, err)
		require.Equal(t, url, serverURL+"api/mdm/apple/enroll?token=token")
	})
}

func TestNewDEPClient_TokenInvalid(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	const orgName = "org1"

	// setupTest wires a DEP client up to a fake Apple DEP server that
	// authenticates a session normally, then responds to the /account request
	// with whatever accountHandler decides. It returns the mock Datastore (so
	// the caller can stub SetABMTokenInvalidForOrgNameFunc before calling) and
	// a call func that triggers the actual AccountDetail request.
	setupTest := func(t *testing.T, accountHandler http.HandlerFunc) (*mock.Store, func() error) {
		ds := new(mock.Store)
		depStorage := new(nanodep_mock.Storage)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/session":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"auth_session_token": "xyz"}`))
			case "/account":
				accountHandler(w, r)
			default:
				t.Errorf("unexpected request to %s", r.URL.Path)
			}
		}))
		t.Cleanup(srv.Close)

		depStorage.RetrieveConfigFunc = func(ctx context.Context, name string) (*client.Config, error) {
			return &client.Config{BaseURL: srv.URL}, nil
		}
		depStorage.RetrieveAuthTokensFunc = func(ctx context.Context, name string) (*client.OAuth1Tokens, error) {
			return &client.OAuth1Tokens{}, nil
		}

		depClient := NewDEPClient(depStorage, ds, logger)
		call := func() error {
			_, err := depClient.AccountDetail(ctx, orgName)
			return err
		}
		return ds, call
	}

	t.Run("token rejected sets token_invalid", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`"token_rejected"`))
		})
		var gotOrgName string
		var gotInvalid bool
		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			gotOrgName, gotInvalid = orgName, invalid
			return true, nil
		}
		// currently valid, so the write is needed to flag it as invalid
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return false, nil
		}

		require.Error(t, call())
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		require.Equal(t, orgName, gotOrgName)
		require.True(t, gotInvalid)
	})

	t.Run("read error falls back to always writing", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`"token_rejected"`))
		})
		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			return true, nil
		}
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return false, errors.New("boom")
		}

		require.Error(t, call())
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
	})

	t.Run("token rejected but already flagged invalid skips the write", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`"token_rejected"`))
		})
		// already invalid, so no write is needed
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return true, nil
		}

		require.Error(t, call())
		require.False(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
	})

	t.Run("signature invalid sets token_invalid", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`"signature_invalid"`))
		})
		var gotOrgName string
		var gotInvalid bool
		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			gotOrgName, gotInvalid = orgName, invalid
			return true, nil
		}
		// currently valid, so the write is needed to flag it as invalid
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return false, nil
		}

		require.Error(t, call())
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		require.Equal(t, orgName, gotOrgName)
		require.True(t, gotInvalid)
	})

	t.Run("success clears token_invalid", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"admin_id": "admin123", "org_name": "org1"}`))
		})
		var gotOrgName string
		var gotInvalid bool
		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			gotOrgName, gotInvalid = orgName, invalid
			return false, nil
		}
		// currently invalid, so the write is needed to clear it
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return true, nil
		}
		// incidental to this test: any successful call also flows through the
		// hook's pre-existing terms-expired bookkeeping, which needs these
		// stubbed to short-circuit cleanly (zero count, clear AppConfig flag).
		ds.CountABMTokensWithTermsExpiredFunc = func(ctx context.Context) (int, error) {
			return 0, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		require.NoError(t, call())
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		require.Equal(t, orgName, gotOrgName)
		require.False(t, gotInvalid)
	})

	t.Run("terms not signed clears token_invalid", func(t *testing.T) {
		// Apple only evaluates terms after authenticating the token, so a
		// terms-not-signed response is definitive proof the token itself was
		// accepted -- token_invalid should clear even though the call fails.
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`"T_C_NOT_SIGNED"`))
		})
		var gotOrgName string
		var gotInvalid bool
		ds.SetABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string, invalid bool) (bool, error) {
			gotOrgName, gotInvalid = orgName, invalid
			return true, nil
		}
		// currently invalid, so the write is needed to clear it
		ds.IsABMTokenInvalidForOrgNameFunc = func(ctx context.Context, orgName string) (bool, error) {
			return true, nil
		}
		ds.CountABMTokensWithTermsExpiredFunc = func(ctx context.Context) (int, error) {
			return 0, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		// wasSet=true means the flag was already set, so stillSetCount stays
		// at 0 and this test doesn't also need to stub SaveAppConfigFunc.
		ds.SetABMTokenTermsExpiredForOrgNameFunc = func(ctx context.Context, orgName string, expired bool) (bool, error) {
			return true, nil
		}

		require.Error(t, call())
		require.True(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
		require.Equal(t, orgName, gotOrgName)
		require.False(t, gotInvalid)
	})

	t.Run("unrelated error does not touch token_invalid", func(t *testing.T) {
		ds, call := setupTest(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`"SERVER_ERROR"`))
		})

		require.Error(t, call())
		require.False(t, ds.SetABMTokenInvalidForOrgNameFuncInvoked)
	})
}

func TestClassifyDEPDeviceError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want automdm.DEPDeviceErrorType
	}{
		{"nil error", nil, ""},
		{"unrelated error", errors.New("boom"), automdm.DEPDeviceErrorUnavailable},
		{
			"token rejected",
			&godep.HTTPError{StatusCode: http.StatusForbidden, Body: []byte(`"token_rejected"`)},
			automdm.DEPDeviceErrorTokenInvalid,
		},
		{
			"signature invalid",
			&godep.HTTPError{StatusCode: http.StatusForbidden, Body: []byte(`"signature_invalid"`)},
			automdm.DEPDeviceErrorTokenInvalid,
		},
		{
			"terms not signed",
			&godep.HTTPError{StatusCode: http.StatusForbidden, Body: []byte(`"T_C_NOT_SIGNED"`)},
			automdm.DEPDeviceErrorTermsExpired,
		},
		{
			"server error",
			&godep.HTTPError{StatusCode: http.StatusServiceUnavailable, Body: []byte(`"SERVICE_UNAVAILABLE"`)},
			automdm.DEPDeviceErrorServerError,
		},
		{
			// DoAuth's /session handshake (client/auth.go) constructs
			// AuthError with whatever status Apple actually returns, so a
			// genuine outage there surfaces as a 5xx AuthError rather than
			// an HTTPError.
			"server error from /session auth failure",
			&client.AuthError{StatusCode: http.StatusServiceUnavailable, Body: []byte(`"SERVICE_UNAVAILABLE"`)},
			automdm.DEPDeviceErrorServerError,
		},
		{
			"unrelated 4xx",
			&godep.HTTPError{StatusCode: http.StatusBadRequest, Body: []byte(`"INVALID_CURSOR"`)},
			automdm.DEPDeviceErrorUnavailable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ClassifyDEPDeviceError(c.err))
		})
	}
}

func TestAddEnrollmentRefToAutoMDMURL(t *testing.T) {
	const (
		baseAutoMDMURL = "https://example.com"
		reference      = "enroll-ref"
	)

	tests := []struct {
		name           string
		automdmURL     string
		reference      string
		expectedOutput string
		expectError    bool
	}{
		{
			name:           "empty Reference",
			automdmURL:     baseAutoMDMURL,
			reference:      "",
			expectedOutput: baseAutoMDMURL,
			expectError:    false,
		},
		{
			name:           "valid URL and Reference",
			automdmURL:     baseAutoMDMURL,
			reference:      reference,
			expectedOutput: baseAutoMDMURL + "?" + mobileconfig.AutoMDMEnrollReferenceKey + "=" + reference,
			expectError:    false,
		},
		{
			name:        "invalid URL",
			automdmURL:  "://invalid-url",
			reference:   reference,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output, err := AddEnrollmentRefToAutoMDMURL(tc.automdmURL, tc.reference)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedOutput, output)
			}
		})
	}
}

func TestGenerateEnrollmentProfileMobileconfig(t *testing.T) {
	type scepPayload struct {
		Challenge string
		URL       string
	}

	type enrollmentPayload struct {
		PayloadType    string
		ServerURL      string      // used by the enrollment payload
		PayloadContent scepPayload // scep contains a nested payload content dict
	}

	type enrollmentProfile struct {
		PayloadIdentifier string
		PayloadContent    []enrollmentPayload
	}

	tests := []struct {
		name          string
		orgName       string
		automdmURL    string
		scepChallenge string
		expectError   bool
	}{
		{
			name:          "valid input with simple values",
			orgName:       "AutoMDM",
			automdmURL:    "https://example.com",
			scepChallenge: "testChallenge",
			expectError:   false,
		},
		{
			name:          "organization name and enroll secret with special characters",
			orgName:       `AutoMDM & Co. "Special" <Org>`,
			automdmURL:    "https://example.com",
			scepChallenge: "test/&Challenge",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateEnrollmentProfileMobileconfig(tt.orgName, tt.automdmURL, tt.scepChallenge, "com.foo.bar", MDMAccessRightAll, true)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				var profile enrollmentProfile

				require.NoError(t, plist.Unmarshal(result, &profile))

				for _, p := range profile.PayloadContent {
					switch p.PayloadType {
					case "com.apple.security.scep":
						scepURL, err := ResolveAppleSCEPURL(tt.automdmURL)
						require.NoError(t, err)
						require.Equal(t, scepURL, p.PayloadContent.URL)
						require.Equal(t, tt.scepChallenge, p.PayloadContent.Challenge)
					case "com.apple.mdm":
						mdmURL, err := ResolveAppleMDMURL(tt.automdmURL)
						require.NoError(t, err)
						require.Contains(t, mdmURL, p.ServerURL)
					default:
						require.Failf(t, "unrecognized payload type in enrollment profile: %s", p.PayloadType)
					}
				}
			}
		})
	}
}

// The new-enrollment Subject OU marker must be present on fresh-enrollment profiles and absent on
// renewal profiles, otherwise the checkin handler would misclassify renewals as fresh enrollments.
func TestEnrollmentProfileNewEnrollmentSubjectOUMarker(t *testing.T) {
	t.Run("standard enrollment", func(t *testing.T) {
		fresh, err := GenerateEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", MDMAccessRightAll, true)
		require.NoError(t, err)
		require.Contains(t, string(fresh), AutoMDMEnrollmentSubjectOU)

		renewal, err := GenerateEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", MDMAccessRightAll, false)
		require.NoError(t, err)
		require.NotContains(t, string(renewal), AutoMDMEnrollmentSubjectOU)
	})

	t.Run("account-driven enrollment", func(t *testing.T) {
		fresh, err := GenerateAccountDrivenEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", "user@example.com", true)
		require.NoError(t, err)
		require.Contains(t, string(fresh), AutoMDMEnrollmentSubjectOU)

		renewal, err := GenerateAccountDrivenEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", "user@example.com", false)
		require.NoError(t, err)
		require.NotContains(t, string(renewal), AutoMDMEnrollmentSubjectOU)
	})

	t.Run("ACME enrollment", func(t *testing.T) {
		fresh, err := GenerateACMEEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "acme-ident", "SERIAL123", "com.foo.bar", MDMAccessRightAll, true)
		require.NoError(t, err)
		require.Contains(t, string(fresh), AutoMDMEnrollmentSubjectOU)

		renewal, err := GenerateACMEEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "acme-ident", "SERIAL123", "com.foo.bar", MDMAccessRightAll, false)
		require.NoError(t, err)
		require.NotContains(t, string(renewal), AutoMDMEnrollmentSubjectOU)

		// The ACME payload gets no %SerialNumber% substitution from the device, so the CN must be
		// the literal serial to match the order's permanent-identifier at finalize.
		for _, profile := range [][]byte{fresh, renewal} {
			require.NotContains(t, string(profile), "%SerialNumber%")
			require.Contains(t, string(profile), "<string>CN</string>\n\t\t\t\t\t\t<string>SERIAL123</string>")
		}
	})
}

// Every enrollment profile must declare the com.apple.mdm.token server capability, otherwise the
// device never sends the GetToken check-in that AutoMDM answers for com.apple.maid.
// We know we can easily add capabilities on a renewing profile, so we enforce it for all types.
func TestEnrollmentProfileServerCapabilities(t *testing.T) {
	const tokenCapability = "com.apple.mdm.token" // nolint:gosec // not a credential

	requireTokenCapability := func(t *testing.T, profile []byte) {
		t.Helper()

		var parsed struct {
			PayloadContent []struct {
				PayloadType        string
				ServerCapabilities []string
			}
		}
		require.NoError(t, plist.Unmarshal(profile, &parsed))

		var found bool
		for _, payload := range parsed.PayloadContent {
			if payload.PayloadType != "com.apple.mdm" {
				continue
			}
			found = true
			require.Contains(t, payload.ServerCapabilities, tokenCapability)
		}
		require.True(t, found, "no com.apple.mdm payload in profile")
	}

	t.Run("standard enrollment", func(t *testing.T) {
		profile, err := GenerateEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", MDMAccessRightAll, true)
		require.NoError(t, err)
		requireTokenCapability(t, profile)
	})

	t.Run("account-driven enrollment", func(t *testing.T) {
		profile, err := GenerateAccountDrivenEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "chal", "com.foo.bar", "user@example.com", true)
		require.NoError(t, err)
		requireTokenCapability(t, profile)
	})

	t.Run("ACME enrollment", func(t *testing.T) {
		profile, err := GenerateACMEEnrollmentProfileMobileconfig("AutoMDM", "https://example.com", "acme-ident", "SERIAL123", "com.foo.bar", MDMAccessRightAll, true)
		require.NoError(t, err)
		requireTokenCapability(t, profile)
	})
}

func TestValidateMDMSettingsAppleSupportedOSVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// load the test data from the file
		b, err := os.ReadFile("./gdmf/testdata/gdmf.json")
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_GDMF_URL", srv.URL, t)

	// selected versions of from testdata/gdmf.json that we'll use in out tests
	expectSupportedMacOSPublic := []string{"14.6.1", "13.6.9", "12.7.6", "11.7.10"}
	expectSupportedMacOSNonPublic := []string{"14.5", "14.6", "13.6.8", "13.6.7", "12.7.5"}
	expectSupportedIOSPublic := "17.6.1"
	expectSupportedIOSNonPublic := "17.5.1"
	// lastIPodSupportedVersion     : = "15.8.3"

	// helper function to initialize app config MDM settings with known good versions (tests will modify as needed)
	mockAppConfigMDM := func() automdm.MDM {
		return automdm.MDM{
			MacOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedMacOSPublic[0]),
			},
			IOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedIOSPublic),
			},
			IPadOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedIOSPublic),
			},
		}
	}
	// helper function to initialize team MDM settings with known good versions (tests will modify as needed)
	mockTeamMDM := func() automdm.TeamMDM {
		return automdm.TeamMDM{
			MacOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedMacOSPublic[0]),
			},
			IOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedIOSPublic),
			},
			IPadOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(expectSupportedIOSPublic),
			},
		}
	}

	// helper function to check if the error matches expectations for a given platform and log appropriately
	checkErr := func(platform string, wantErr string, gotErrs map[string]string, msg string) {
		if wantErr == "" {
			assert.Empty(t, gotErrs, msg+": expected no error for platform %s but got: %v", platform, gotErrs)
		} else {
			assert.Len(t, gotErrs, 1, msg+": expected error for platform %s but got no errors", platform)
			assert.Contains(t, gotErrs, platform, msg+": expected error for platform %s but key not found in error map: %v", platform, gotErrs)
			assert.Contains(t, gotErrs[platform], wantErr, msg+": expected error for platform %s but got: %v", platform, gotErrs[platform])
		}
	}

	t.Run("macos", func(t *testing.T) {
		t.Run("app config mdm settings", func(t *testing.T) {
			ac := mockAppConfigMDM()
			for _, v := range expectSupportedMacOSPublic {
				ac.MacOSUpdates.MinimumVersion = optjson.SetString(v)
				got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect public macOS version to be supported when including non-public asset sets")
				got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect public macOS version to be supported when excluding non-public asset sets")
			}
			for _, v := range expectSupportedMacOSNonPublic {
				ac.MacOSUpdates.MinimumVersion = optjson.SetString(v)
				got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect non-public macOS version to be supported when including non-public asset sets")
				got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
				require.NoError(t, err)
				checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public macOS version to return error when excluding non-public asset sets")
			}

			ac.MacOSUpdates.MinimumVersion = optjson.SetString("11.7.9") // not supported in either asset set, so we expect an error in both cases
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported macOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported macOS version to return error when excluding non-public asset sets")
		})
		t.Run("team mdm settings", func(t *testing.T) {
			tm := mockTeamMDM()
			for _, v := range expectSupportedMacOSPublic {
				tm.MacOSUpdates.MinimumVersion = optjson.SetString(v)
				got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect public macOS version to be supported when including non-public asset sets")
				got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect public macOS version to be supported when excluding non-public asset sets")
			}
			for _, v := range expectSupportedMacOSNonPublic {
				tm.MacOSUpdates.MinimumVersion = optjson.SetString(v)
				got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
				require.NoError(t, err)
				checkErr("macos", "", got, "expect non-public macOS version to be supported when including non-public asset sets")
				got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
				require.NoError(t, err)
				checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public macOS version to return error when excluding non-public asset sets")
			}

			tm.MacOSUpdates.MinimumVersion = optjson.SetString("11.7.9") // not supported in either asset set, so we expect an error in both cases
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported macOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("macos", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported macOS version to return error when excluding non-public asset sets")
		})
	})

	t.Run("ios", func(t *testing.T) {
		t.Run("app config mdm settings", func(t *testing.T) {
			ac := mockAppConfigMDM()
			ac.IOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSPublic)
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect public iOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect public iOS version to be supported when excluding non-public asset sets")

			ac.IOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSNonPublic)
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect non-public iOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public iOS version to return error when excluding non-public asset sets")

			ac.IOSUpdates.MinimumVersion = optjson.SetString("5.3.9") // only supported for Apple Watch, so we expect an error
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iOS version to return error when excluding non-public asset sets")
		})

		t.Run("team mdm settings", func(t *testing.T) {
			tm := mockTeamMDM()
			tm.IOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSPublic)
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect public iOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect public iOS version to be supported when excluding non-public asset sets")

			tm.IOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSNonPublic)
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ios", "", got, "expect non-public iOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public iOS version to return error when excluding non-public asset sets")

			tm.IOSUpdates.MinimumVersion = optjson.SetString("5.3.9") // only supported for Apple Watch, so we expect an error
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iOS version to return error when excluding non-public asset sets")
		})
	})

	t.Run("ipados", func(t *testing.T) {
		t.Run("app config mdm settings", func(t *testing.T) {
			ac := mockAppConfigMDM()
			ac.IPadOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSPublic)
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect public iPadOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect public iPadOS version to be supported when excluding non-public asset sets")

			ac.IPadOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSNonPublic)
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect non-public iPadOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public iPadOS version to return error when excluding non-public asset sets")

			ac.IPadOSUpdates.MinimumVersion = optjson.SetString("5.3.9") // only supported for Apple Watch, so we expect an error
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iPadOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iPadOS version to return error when excluding non-public asset sets")
		})

		t.Run("team mdm settings", func(t *testing.T) {
			tm := mockTeamMDM()
			tm.IPadOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSPublic)
			got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect public iPadOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect public iPadOS version to be supported when excluding non-public asset sets")

			tm.IPadOSUpdates.MinimumVersion = optjson.SetString(expectSupportedIOSNonPublic)
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ipados", "", got, "expect non-public iPadOS version to be supported when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect non-public iPadOS version to return error when excluding non-public asset sets")

			tm.IPadOSUpdates.MinimumVersion = optjson.SetString("5.3.9") // only supported for Apple Watch, so we expect an error
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iPadOS version to return error when including non-public asset sets")
			got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
			require.NoError(t, err)
			checkErr("ipados", automdm.AppleOSVersionUnsupportedMessage, got, "expect unsupported iPadOS version to return error when excluding non-public asset sets")
		})
	})

	t.Run("latest", func(t *testing.T) {
		// "latest" is a sentinel resolved per host later, so it must never be
		// looked up against Apple's published versions.
		t.Run("accepted on every platform", func(t *testing.T) {
			t.Run("app config mdm settings", func(t *testing.T) {
				ac := mockAppConfigMDM()
				ac.MacOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				ac.IOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				ac.IPadOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)

				got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
				require.NoError(t, err)
				assert.Empty(t, got, "expect latest to be accepted when including non-public asset sets")

				got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, true)
				require.NoError(t, err)
				assert.Empty(t, got, "expect latest to be accepted when excluding non-public asset sets")
			})

			t.Run("team mdm settings", func(t *testing.T) {
				tm := mockTeamMDM()
				tm.MacOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				tm.IOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				tm.IPadOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)

				got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
				require.NoError(t, err)
				assert.Empty(t, got, "expect latest to be accepted when including non-public asset sets")

				got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, true)
				require.NoError(t, err)
				assert.Empty(t, got, "expect latest to be accepted when excluding non-public asset sets")
			})
		})

		t.Run("mixed with a real version still validates that version", func(t *testing.T) {
			t.Run("app config mdm settings", func(t *testing.T) {
				ac := mockAppConfigMDM()
				ac.MacOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				// only supported for Apple Watch, so iOS should still be flagged
				ac.IOSUpdates.MinimumVersion = optjson.SetString("5.3.9")
				ac.IPadOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)

				got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
				require.NoError(t, err)
				checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect only the concrete version to be validated")
			})

			t.Run("team mdm settings", func(t *testing.T) {
				tm := mockTeamMDM()
				tm.MacOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
				// only supported for Apple Watch, so iOS should still be flagged
				tm.IOSUpdates.MinimumVersion = optjson.SetString("5.3.9")
				tm.IPadOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)

				got, err := ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
				require.NoError(t, err)
				checkErr("ios", automdm.AppleOSVersionUnsupportedMessage, got, "expect only the concrete version to be validated")
			})
		})
	})

	// These subtests are placed last so that the dev_mode override cleanup for the error server
	// doesn't interfere with the earlier subtests that rely on the valid mock server.
	t.Run("GetAssetMetadata error", func(t *testing.T) {
		// Use a server that returns invalid JSON so GetAssetMetadata returns an error.
		errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("not valid json"))
			require.NoError(t, err)
		}))
		t.Cleanup(errSrv.Close)
		dev_mode.SetOverride("AUTOMDM_DEV_GDMF_URL", errSrv.URL, t)

		ac := mockAppConfigMDM()
		got, err := ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
		require.Error(t, err)
		assert.Nil(t, got)

		tm := mockTeamMDM()
		got, err = ValidateMDMSettingsAppleSupportedOSVersion(tm, false)
		require.Error(t, err)
		assert.Nil(t, got)

		// With every platform set to "latest" there is nothing to look up, so the
		// broken metadata endpoint must never be contacted.
		ac = mockAppConfigMDM()
		ac.MacOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
		ac.IOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
		ac.IPadOSUpdates.MinimumVersion = optjson.SetString(automdm.AppleOSUpdateLatestVersion)
		got, err = ValidateMDMSettingsAppleSupportedOSVersion(ac, false)
		require.NoError(t, err, "latest-only settings must not fetch Apple metadata")
		assert.Nil(t, got)
	})
}

type notFoundError struct{}

func (e notFoundError) IsNotFound() bool { return true }

func (e notFoundError) Error() string { return "not found" }

func TestGenerateRecoveryLockPassword(t *testing.T) {
	// Pattern: 6 groups of 4 characters from the allowed charset, separated by dashes
	pattern := regexp.MustCompile(`^[23456789ABCDEFGHJKLMNPQRSTUVWXYZ]{4}(-[23456789ABCDEFGHJKLMNPQRSTUVWXYZ]{4}){5}$`)

	t.Run("format", func(t *testing.T) {
		password := GenerateRecoveryLockPassword()
		assert.True(t, pattern.MatchString(password), "password %q does not match expected format", password)
		assert.Len(t, password, 29) // 24 chars + 5 dashes
	})

	t.Run("excludes confusing characters", func(t *testing.T) {
		// Generate multiple passwords and check none contain confusing chars
		confusingChars := regexp.MustCompile(`[01OIl]`)
		for range 100 {
			password := GenerateRecoveryLockPassword()
			assert.False(t, confusingChars.MatchString(password), "password %q contains confusing characters", password)
		}
	})

	t.Run("uniqueness", func(t *testing.T) {
		// Generate multiple passwords and verify they're unique
		seen := make(map[string]bool)
		for range 100 {
			password := GenerateRecoveryLockPassword()
			assert.False(t, seen[password], "duplicate password generated: %s", password)
			seen[password] = true
		}
	})
}

// TestSendRecoveryLockCommands tests the cron job that sends SetRecoveryLock commands
// to hosts that need recovery lock passwords.
//
// Note: SetRecoveryLock command results are handled synchronously in the MDM results handler
// (server/service/apple_mdm.go), which is tested separately in apple_mdm_cmd_results_test.go.
func TestSendRecoveryLockCommands(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("no hosts needing recovery lock does not send commands", func(t *testing.T) {
		ds := new(mock.Store)
		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return nil, nil
		}
		// Mock clear flow - no hosts need clearing
		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return nil, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		var commandSent bool
		mockCommander := &mockRecoveryLockCommander{
			setRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				commandSent = true
				return nil
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		require.NoError(t, err)
		assert.False(t, commandSent, "SetRecoveryLock should not be called when no hosts need it")
	})

	t.Run("host needing recovery lock gets SetRecoveryLock and password stored with pending status", func(t *testing.T) {
		ds := new(mock.Store)

		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		hostUUID := "host-uuid-1"
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return map[string]bool{hostUUID: false}, nil
		}
		// Mock clear flow - no hosts need clearing
		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return nil, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		// Track call order to verify correct sequencing
		var callOrder []string
		var storedPasswords []automdm.HostRecoveryLockPasswordPayload
		ds.SetHostsRecoveryLockPasswordsFunc = func(ctx context.Context, passwords []automdm.HostRecoveryLockPasswordPayload) error {
			callOrder = append(callOrder, "SetHostsRecoveryLockPasswords")
			storedPasswords = passwords
			return nil
		}

		var sentCmdUUID string
		mockCommander := &mockRecoveryLockCommander{
			setRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				callOrder = append(callOrder, "SetRecoveryLock")
				assert.Equal(t, []string{hostUUID}, hostUUIDs)
				sentCmdUUID = cmdUUID
				return nil
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		require.NoError(t, err)

		// Verify call order: password must be stored BEFORE command is sent
		require.Equal(t, []string{"SetHostsRecoveryLockPasswords", "SetRecoveryLock"}, callOrder,
			"SetHostsRecoveryLockPasswords must be called before SetRecoveryLock")

		// Password should be stored with pending status atomically
		require.Len(t, storedPasswords, 1, "password should be stored for host")
		assert.Equal(t, hostUUID, storedPasswords[0].HostUUID)
		assert.NotEmpty(t, storedPasswords[0].Password)
		assert.NotEmpty(t, sentCmdUUID, "command UUID should have been sent")
	})

	t.Run("SetRecoveryLock failure clears pending status to allow retry", func(t *testing.T) {
		ds := new(mock.Store)

		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		hostUUID := "host-uuid-1"
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return map[string]bool{hostUUID: false}, nil
		}
		// Mock clear flow - no hosts need clearing
		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return nil, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		// Track call order to verify correct sequencing
		var callOrder []string
		ds.SetHostsRecoveryLockPasswordsFunc = func(ctx context.Context, passwords []automdm.HostRecoveryLockPasswordPayload) error {
			callOrder = append(callOrder, "SetHostsRecoveryLockPasswords")
			return nil
		}

		var clearedHostUUIDs []string
		ds.ClearRecoveryLockPendingStatusFunc = func(ctx context.Context, hostUUIDs []string) error {
			callOrder = append(callOrder, "ClearRecoveryLockPendingStatus")
			clearedHostUUIDs = hostUUIDs
			return nil
		}

		mockCommander := &mockRecoveryLockCommander{
			setRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				callOrder = append(callOrder, "SetRecoveryLock")
				return errors.New("APNs push failed")
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "APNs push failed")

		// Verify call order: password stored -> command attempt -> clear pending on failure
		require.Equal(t, []string{"SetHostsRecoveryLockPasswords", "SetRecoveryLock", "ClearRecoveryLockPendingStatus"}, callOrder,
			"Operations must occur in order: store password, attempt command, clear pending on failure")

		// Status should be cleared to allow retry on next cron run
		assert.Equal(t, []string{hostUUID}, clearedHostUUIDs, "pending status should be cleared on enqueue failure")
	})

	t.Run("APNs delivery failure does not clear pending status", func(t *testing.T) {
		ds := new(mock.Store)

		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		hostUUID := "host-uuid-1"
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return map[string]bool{hostUUID: false}, nil
		}
		// Mock clear flow - no hosts need clearing
		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return nil, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		// Track call order to verify ClearRecoveryLockPendingStatus is NOT called
		var callOrder []string
		ds.SetHostsRecoveryLockPasswordsFunc = func(ctx context.Context, passwords []automdm.HostRecoveryLockPasswordPayload) error {
			callOrder = append(callOrder, "SetHostsRecoveryLockPasswords")
			return nil
		}

		ds.ClearRecoveryLockPendingStatusFunc = func(ctx context.Context, hostUUIDs []string) error {
			callOrder = append(callOrder, "ClearRecoveryLockPendingStatus")
			return nil
		}

		mockCommander := &mockRecoveryLockCommander{
			setRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				callOrder = append(callOrder, "SetRecoveryLock")
				// Return APNs delivery error - command was persisted but push failed
				return &APNSDeliveryError{errorsByUUID: map[string]error{hostUUID: errors.New("push failed")}}
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		// Should NOT return error - command was persisted, just push failed
		require.NoError(t, err)

		// Verify ClearRecoveryLockPendingStatus was NOT called (status should stay pending)
		// Command is queued and will be delivered when device checks in
		require.Equal(t, []string{"SetHostsRecoveryLockPasswords", "SetRecoveryLock"}, callOrder,
			"ClearRecoveryLockPendingStatus should NOT be called when APNs push fails (command is already queued)")
	})
}

// mockRecoveryLockCommander implements RecoveryLockCommander for testing.
type mockRecoveryLockCommander struct {
	setRecoveryLockFn    func(ctx context.Context, hostUUIDs []string, cmdUUID string) error
	clearRecoveryLockFn  func(ctx context.Context, hostUUIDs []string, cmdUUID string) error
	rotateRecoveryLockFn func(ctx context.Context, hostUUIDs []string, cmdUUID string) error
	verifyRecoveryLockFn func(ctx context.Context, hostUUID, cmdUUID string) error
	verifyClearFn        func(ctx context.Context, hostUUID, cmdUUID string) error
}

func (m *mockRecoveryLockCommander) VerifyRecoveryLock(ctx context.Context, hostUUID, cmdUUID string) error {
	if m.verifyRecoveryLockFn != nil {
		return m.verifyRecoveryLockFn(ctx, hostUUID, cmdUUID)
	}
	return nil
}

func (m *mockRecoveryLockCommander) VerifyClearRecoveryLock(ctx context.Context, hostUUID, cmdUUID string) error {
	if m.verifyClearFn != nil {
		return m.verifyClearFn(ctx, hostUUID, cmdUUID)
	}
	return nil
}

func (m *mockRecoveryLockCommander) SetRecoveryLock(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
	if m.setRecoveryLockFn != nil {
		return m.setRecoveryLockFn(ctx, hostUUIDs, cmdUUID)
	}
	return nil
}

func (m *mockRecoveryLockCommander) ClearRecoveryLock(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
	if m.clearRecoveryLockFn != nil {
		return m.clearRecoveryLockFn(ctx, hostUUIDs, cmdUUID)
	}
	return nil
}

func (m *mockRecoveryLockCommander) RotateRecoveryLock(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
	if m.rotateRecoveryLockFn != nil {
		return m.rotateRecoveryLockFn(ctx, hostUUIDs, cmdUUID)
	}
	return nil
}

func TestSendRecoveryLockVerifyCommands(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	claims := []automdm.HostRecoveryLockVerifyClaim{
		{HostUUID: "install-host", OperationType: automdm.MDMOperationTypeInstall, VerifyCommandUUID: "verify-1"},
		{HostUUID: "remove-host", OperationType: automdm.MDMOperationTypeRemove, VerifyCommandUUID: "verify-2"},
	}

	t.Run("sends the verify matching each operation", func(t *testing.T) {
		ds := new(mock.Store)
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return claims, nil
		}
		var verified, verifiedClear []string
		commander := &mockRecoveryLockCommander{
			verifyRecoveryLockFn: func(ctx context.Context, hostUUID, cmdUUID string) error {
				verified = append(verified, hostUUID+"/"+cmdUUID)
				return nil
			},
			verifyClearFn: func(ctx context.Context, hostUUID, cmdUUID string) error {
				verifiedClear = append(verifiedClear, hostUUID+"/"+cmdUUID)
				return nil
			},
		}

		require.NoError(t, sendRecoveryLockVerifyCommands(ctx, ds, commander, logger))
		require.Equal(t, []string{"install-host/verify-1"}, verified)
		require.Equal(t, []string{"remove-host/verify-2"}, verifiedClear)
		require.False(t, ds.UnclaimHostForRecoveryLockVerifyFuncInvoked)
	})

	t.Run("a verify that wasn't enqueued is unclaimed, one whose push failed is kept", func(t *testing.T) {
		ds := new(mock.Store)
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return claims, nil
		}
		var unclaimed []string
		ds.UnclaimHostForRecoveryLockVerifyFunc = func(ctx context.Context, hostUUID, verifyCommandUUID string) error {
			unclaimed = append(unclaimed, hostUUID+"/"+verifyCommandUUID)
			return nil
		}
		commander := &mockRecoveryLockCommander{
			verifyRecoveryLockFn: func(ctx context.Context, hostUUID, cmdUUID string) error {
				return errors.New("enqueue failed")
			},
			verifyClearFn: func(ctx context.Context, hostUUID, cmdUUID string) error {
				// as enqueueAndNotify returns it once the command is persisted
				return ctxerr.Wrap(ctx, &NotificationFailedError{
					err: &APNSDeliveryError{errorsByUUID: map[string]error{hostUUID: errors.New("push failed")}},
				}, "sending notifications")
			},
		}

		err := sendRecoveryLockVerifyCommands(ctx, ds, commander, logger)
		require.ErrorContains(t, err, "enqueue failed")
		require.Equal(t, []string{"install-host/verify-1"}, unclaimed)
	})

	t.Run("a push failure that isn't per device keeps the claim", func(t *testing.T) {
		ds := new(mock.Store)
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return claims[:1], nil
		}
		commander := &mockRecoveryLockCommander{
			verifyRecoveryLockFn: func(ctx context.Context, hostUUID, cmdUUID string) error {
				return ctxerr.Wrap(ctx, &NotificationFailedError{err: errors.New("push certificate expired")}, "sending notifications")
			},
		}

		require.NoError(t, sendRecoveryLockVerifyCommands(ctx, ds, commander, logger))
		require.False(t, ds.UnclaimHostForRecoveryLockVerifyFuncInvoked)
	})
}

func TestSendClearRecoveryLockCommands(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	t.Run("hosts needing clear get ClearRecoveryLock command", func(t *testing.T) {
		ds := new(mock.Store)

		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		// No hosts need SET
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return nil, nil
		}

		hostUUID := "host-uuid-1"
		// ClaimHostsForRecoveryLockClear queries verified hosts where config is disabled and marks them pending
		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return []string{hostUUID}, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		var clearCalled bool
		mockCommander := &mockRecoveryLockCommander{
			clearRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				clearCalled = true
				require.Equal(t, []string{hostUUID}, hostUUIDs)
				return nil
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		require.NoError(t, err)
		require.True(t, clearCalled, "ClearRecoveryLock should have been called")
	})

	t.Run("no hosts needing clear does not send commands", func(t *testing.T) {
		ds := new(mock.Store)

		// Mock restore - no hosts to restore
		ds.RestoreRecoveryLockForReenabledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.SoftDeleteRecoveryLockPasswordsForUnenrolledHostsFunc = func(ctx context.Context) (int64, error) {
			return 0, nil
		}
		ds.ClaimHostsForRecoveryLockVerifyFunc = func(ctx context.Context) ([]automdm.HostRecoveryLockVerifyClaim, error) {
			return nil, nil
		}
		// No hosts need SET
		ds.GetHostsForRecoveryLockActionFunc = func(ctx context.Context) (map[string]bool, error) {
			return nil, nil
		}

		ds.ClaimHostsForRecoveryLockClearFunc = func(ctx context.Context, clearCommandUUID string) ([]string, error) {
			return nil, nil
		}
		// Mock auto-rotation - no hosts need auto-rotation
		ds.GetHostsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostAutoRotationInfo, error) {
			return nil, nil
		}

		mockCommander := &mockRecoveryLockCommander{
			clearRecoveryLockFn: func(ctx context.Context, hostUUIDs []string, cmdUUID string) error {
				t.Fatal("ClearRecoveryLock should not be called when no hosts need clearing")
				return nil
			},
		}

		err := sendRecoveryLockCommandsWithCommander(ctx, ds, mockCommander, logger, nil)
		require.NoError(t, err)
	})
}

// mockManagedLocalAccountCommander implements ManagedLocalAccountRotationCommander for testing.
type mockManagedLocalAccountCommander struct {
	setAutoAdminPasswordFn func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error
}

func (m *mockManagedLocalAccountCommander) SetAutoAdminPassword(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
	if m.setAutoAdminPasswordFn != nil {
		return m.setAutoAdminPasswordFn(ctx, hostUUID, guid, passwordHashPlist, cmdUUID)
	}
	return nil
}

func TestSendManagedLocalAccountRotationCommands(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("no hosts due", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return nil, nil
		}
		mockCommander := &mockManagedLocalAccountCommander{
			setAutoAdminPasswordFn: func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
				t.Fatal("SetAutoAdminPassword must not be called when no hosts are due")
				return nil
			},
		}
		require.NoError(t, sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, nil))
	})

	t.Run("rotates and logs activity for view-driven row", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return []automdm.HostManagedLocalAccountAutoRotationInfo{
				{HostUUID: "uuid-1", HostID: 1, DisplayName: "host-1", AccountUUID: "acct-1", InitiatedByTeam: true},
			}, nil
		}
		var initiateCalled, setCalled bool
		ds.InitiateManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID, plaintextPassword, cmdUUID string) error {
			initiateCalled = true
			require.Equal(t, "uuid-1", hostUUID)
			require.NotEmpty(t, plaintextPassword)
			require.NotEmpty(t, cmdUUID)
			return nil
		}
		mockCommander := &mockManagedLocalAccountCommander{
			setAutoAdminPasswordFn: func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
				setCalled = true
				require.Equal(t, "uuid-1", hostUUID)
				require.Equal(t, "acct-1", guid)
				require.NotEmpty(t, passwordHashPlist)
				return nil
			},
		}
		var loggedActivities []automdm.ActivityDetails
		newActivityFn := func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			loggedActivities = append(loggedActivities, activity)
			require.Nil(t, user, "auto-rotation must log with nil user (AutoMDM actor)")
			return nil
		}
		require.NoError(t, sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, newActivityFn))
		require.True(t, initiateCalled)
		require.True(t, setCalled)
		require.Len(t, loggedActivities, 1)
		rotated, ok := loggedActivities[0].(automdm.ActivityTypeRotatedManagedLocalAccountPassword)
		require.True(t, ok)
		require.True(t, rotated.AutoMDMInitiated)
	})

	t.Run("does NOT log activity for deferred manual rotation", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return []automdm.HostManagedLocalAccountAutoRotationInfo{
				{HostUUID: "uuid-2", HostID: 2, DisplayName: "host-2", AccountUUID: "acct-2", InitiatedByTeam: false},
			}, nil
		}
		ds.InitiateManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID, plaintextPassword, cmdUUID string) error {
			return nil
		}
		mockCommander := &mockManagedLocalAccountCommander{}
		var loggedActivities []automdm.ActivityDetails
		newActivityFn := func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			loggedActivities = append(loggedActivities, activity)
			return nil
		}
		require.NoError(t, sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, newActivityFn))
		require.Empty(t, loggedActivities, "deferred manual rotations were already logged at click time and must not be re-logged by the cron")
	})

	t.Run("benign race - rotation pending - skipped silently", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return []automdm.HostManagedLocalAccountAutoRotationInfo{
				{HostUUID: "uuid-3", HostID: 3, DisplayName: "host-3", AccountUUID: "acct-3", InitiatedByTeam: true},
			}, nil
		}
		ds.InitiateManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID, plaintextPassword, cmdUUID string) error {
			return automdm.ErrManagedLocalAccountRotationPending
		}
		mockCommander := &mockManagedLocalAccountCommander{
			setAutoAdminPasswordFn: func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
				t.Fatal("SetAutoAdminPassword must not be called when initiate failed with benign race")
				return nil
			},
		}
		require.NoError(t, sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, nil))
	})

	t.Run("APNs error keeps pending state and still logs activity", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return []automdm.HostManagedLocalAccountAutoRotationInfo{
				{HostUUID: "uuid-4", HostID: 4, DisplayName: "host-4", AccountUUID: "acct-4", InitiatedByTeam: true},
			}, nil
		}
		ds.InitiateManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID, plaintextPassword, cmdUUID string) error {
			return nil
		}
		var clearCalled bool
		ds.ClearManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID string) error {
			clearCalled = true
			return nil
		}
		mockCommander := &mockManagedLocalAccountCommander{
			setAutoAdminPasswordFn: func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
				return &APNSDeliveryError{errorsByUUID: map[string]error{hostUUID: errors.New("push failed")}}
			},
		}
		var loggedActivities []automdm.ActivityDetails
		newActivityFn := func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			loggedActivities = append(loggedActivities, activity)
			return nil
		}
		require.NoError(t, sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, newActivityFn))
		require.False(t, clearCalled, "APNs failures must not roll back pending rotation — the command is persisted")
		require.Len(t, loggedActivities, 1)
	})

	t.Run("persistence error rolls back pending rotation and skips activity", func(t *testing.T) {
		ds := new(mock.Store)
		ds.GetManagedLocalAccountsForAutoRotationFunc = func(ctx context.Context) ([]automdm.HostManagedLocalAccountAutoRotationInfo, error) {
			return []automdm.HostManagedLocalAccountAutoRotationInfo{
				{HostUUID: "uuid-5", HostID: 5, DisplayName: "host-5", AccountUUID: "acct-5", InitiatedByTeam: true},
			}, nil
		}
		ds.InitiateManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID, plaintextPassword, cmdUUID string) error {
			return nil
		}
		var clearCalled bool
		ds.ClearManagedLocalAccountRotationFunc = func(ctx context.Context, hostUUID string) error {
			clearCalled = true
			return nil
		}
		mockCommander := &mockManagedLocalAccountCommander{
			setAutoAdminPasswordFn: func(ctx context.Context, hostUUID, guid string, passwordHashPlist []byte, cmdUUID string) error {
				return errors.New("storage write failed")
			},
		}
		var loggedActivities []automdm.ActivityDetails
		newActivityFn := func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			loggedActivities = append(loggedActivities, activity)
			return nil
		}
		err := sendManagedLocalAccountRotationCommandsWithCommander(ctx, ds, mockCommander, logger, newActivityFn)
		require.Error(t, err)
		require.True(t, clearCalled)
		require.Empty(t, loggedActivities)
	})
}

func TestParseAppleMDMCertificateBindingExtension(t *testing.T) {
	utf8Value := func(t *testing.T, s string) []byte {
		v, err := asn1.MarshalWithParams(s, "utf8")
		require.NoError(t, err)
		return v
	}
	bindingExt := func(value []byte) pkix.Extension {
		return pkix.Extension{Id: AppleMDMCertificateBindingExtensionOID, Value: value}
	}
	certWith := func(exts ...pkix.Extension) *x509.Certificate {
		return &x509.Certificate{Extensions: exts}
	}

	t.Run("no extension", func(t *testing.T) {
		otherExt := pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 63991, 1, 1}, Value: utf8Value(t, "host-identity")}
		binding, err := ParseAppleMDMCertificateBindingExtension(certWith(otherExt))
		require.NoError(t, err)
		require.Nil(t, binding)
	})

	t.Run("round trips a built extension", func(t *testing.T) {
		ext, err := BuildAppleMDMCertificateBindingExtension(AppleMDMCertificateBindingExtension{
			Purpose: automdm.AppleMDMCertPurposeADE, UDID: new("udid"), Serial: new("serial"),
		})
		require.NoError(t, err)
		binding, err := ParseAppleMDMCertificateBindingExtension(certWith(ext))
		require.NoError(t, err)
		require.Equal(t, &AppleMDMCertificateBindingExtension{
			Version: 1, Purpose: automdm.AppleMDMCertPurposeADE, UDID: new("udid"), Serial: new("serial"),
		}, binding)
	})

	valid := bindingExt(utf8Value(t, `{"v":1,"purpose":"ade","udid":"udid","serial":"serial"}`))
	octetString, err := asn1.Marshal([]byte(`{"v":1,"purpose":"ade"}`))
	require.NoError(t, err)
	printableString, err := asn1.MarshalWithParams("v1", "printable")
	require.NoError(t, err)

	rejected := []struct {
		name string
		cert *x509.Certificate
	}{
		{"duplicate extension", certWith(valid, valid)},
		{"raw JSON instead of DER", certWith(bindingExt([]byte(`{"v":1,"purpose":"ade"}`)))},
		{"empty value", certWith(bindingExt(nil))},
		{"truncated DER", certWith(bindingExt(valid.Value[:len(valid.Value)-3]))},
		{"octet string instead of UTF8String", certWith(bindingExt(octetString))},
		{"printable string instead of UTF8String", certWith(bindingExt(printableString))},
		{"trailing bytes after the UTF8String", certWith(bindingExt(append(append([]byte{}, valid.Value...), 0x00)))},
		{"malformed JSON", certWith(bindingExt(utf8Value(t, `{"v":1,"purpose":`)))},
		{"JSON of the wrong shape", certWith(bindingExt(utf8Value(t, `["ade"]`)))},
		{"version as a string", certWith(bindingExt(utf8Value(t, `{"v":"1","purpose":"ade"}`)))},
		{"missing version", certWith(bindingExt(utf8Value(t, `{"purpose":"ade","udid":"udid","serial":"serial"}`)))},
		{"version 0", certWith(bindingExt(utf8Value(t, `{"v":0,"purpose":"ade","udid":"udid","serial":"serial"}`)))},
		{"unsupported version 2", certWith(bindingExt(utf8Value(t, `{"v":2,"purpose":"ade","udid":"udid","serial":"serial"}`)))},
	}
	for _, c := range rejected {
		t.Run("rejects "+c.name, func(t *testing.T) {
			binding, err := ParseAppleMDMCertificateBindingExtension(c.cert)
			require.Error(t, err)
			require.Nil(t, binding)
		})
	}
}

func TestSilentMigrationRenewalProfile(t *testing.T) {
	const static = "static/><challenge"
	placeholder := automdm.HostSecretPlaceholder(automdm.HostSecretSCEPChallenge)
	profileWith := func(challenge, description string) string {
		var c, d strings.Builder
		require.NoError(t, xml.EscapeText(&c, []byte(challenge)))
		require.NoError(t, xml.EscapeText(&d, []byte(description)))
		return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>PayloadContent</key><array>
<dict><key>PayloadType</key><string>com.apple.security.scep</string>
<key>PayloadContent</key><dict><key>Challenge</key><string>` + c.String() + `</string><key>Keysize</key><integer>2048</integer></dict></dict>
<dict><key>PayloadType</key><string>com.apple.mdm</string><key>ServerURL</key><string>https://example.com/mdm/apple/mdm</string></dict>
</array>
<key>PayloadDescription</key><string>` + d.String() + `</string>
<key>PayloadType</key><string>Configuration</string>
</dict></plist>`
	}
	// returns the SCEP Challenge and PayloadDescription of a renewal profile
	parse := func(t *testing.T, profile []byte) (string, string) {
		var root map[string]any
		err := plist.Unmarshal(profile, &root)
		require.NoError(t, err)
		scep := root["PayloadContent"].([]any)[0].(map[string]any)["PayloadContent"].(map[string]any)
		require.EqualValues(t, 2048, scep["Keysize"])
		return scep["Challenge"].(string), root["PayloadDescription"].(string)
	}

	cases := []struct {
		name      string
		challenge string
	}{
		{"variable", automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithPrefix()},
		{"variable with braces", automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithBraces()},
		{"legacy static challenge", static},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SilentMigrationRenewalProfile([]byte(profileWith(c.challenge, "desc")), static)
			require.NoError(t, err)
			challenge, desc := parse(t, got)
			require.Equal(t, placeholder, challenge)
			require.Equal(t, "desc", desc)
		})
	}

	t.Run("signed profile is unwrapped", func(t *testing.T) {
		cert, key, err := NewSCEPCACertKey()
		require.NoError(t, err)
		signedData, err := pkcs7.NewSignedData([]byte(profileWith(automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithPrefix(), "desc")))
		require.NoError(t, err)
		require.NoError(t, signedData.AddSigner(cert, key, pkcs7.SignerInfoConfig{}))
		signed, err := signedData.Finish()
		require.NoError(t, err)

		got, err := SilentMigrationRenewalProfile(signed, static)
		require.NoError(t, err)
		challenge, _ := parse(t, got)
		require.Equal(t, placeholder, challenge)
	})

	t.Run("works without a static challenge", func(t *testing.T) {
		got, err := SilentMigrationRenewalProfile([]byte(profileWith(automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithPrefix(), "desc")), "")
		require.NoError(t, err)
		challenge, _ := parse(t, got)
		require.Equal(t, placeholder, challenge)
	})

	t.Run("only the SCEP Challenge is replaced", func(t *testing.T) {
		got, err := SilentMigrationRenewalProfile([]byte(profileWith(static, "uses "+static)), static)
		require.NoError(t, err)
		challenge, desc := parse(t, got)
		require.Equal(t, placeholder, challenge)
		require.Equal(t, "uses "+static, desc)
	})

	t.Run("profile without a recognized Challenge is rejected", func(t *testing.T) {
		_, err := SilentMigrationRenewalProfile([]byte(profileWith("other-challenge", "")), static)
		require.Error(t, err)
		_, err = SilentMigrationRenewalProfile([]byte(profileWith("other-challenge", "")), "")
		require.Error(t, err)
		// the marker outside the Challenge field doesn't count
		_, err = SilentMigrationRenewalProfile([]byte(profileWith("other-challenge", automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithPrefix())), static)
		require.Error(t, err)
		_, err = SilentMigrationRenewalProfile([]byte("not a plist"), static)
		require.Error(t, err)
	})
}
