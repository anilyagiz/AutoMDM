package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestConditionalAccessGetIdPSigningCertAuth(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	cfg := config.TestConfig()
	cfg.Server.PrivateKey = "test-private-key"
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)

	// Mock the datastore to return a valid IdP certificate
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetConditionalAccessIDPCert: {
				Name:  automdm.MDMAssetConditionalAccessIDPCert,
				Value: []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"),
			},
		}, nil
	}

	testCases := []struct {
		name       string
		user       *automdm.User
		shouldFail bool
	}{
		{"global admin", test.UserAdmin, false},
		{"global maintainer", test.UserMaintainer, false},
		{"global observer", test.UserObserver, true},
		{"global observer+", test.UserObserverPlus, true},
		{"global gitops", test.UserGitOps, false},
		{"team admin", test.UserTeamAdminTeam1, true},
		{"team maintainer", test.UserTeamMaintainerTeam1, true},
		{"team observer", test.UserTeamObserverTeam1, true},
		{"team observer+", test.UserTeamObserverPlusTeam1, true},
		{"team gitops", test.UserTeamGitOpsTeam1, true},
		{"user no roles", test.UserNoRoles, true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := test.UserContext(ctx, tt.user)

			certPEM, err := svc.ConditionalAccessGetIdPSigningCert(ctx)
			if tt.shouldFail {
				require.Error(t, err)
				var forbiddenError *authz.Forbidden
				require.ErrorAs(t, err, &forbiddenError)
				require.Nil(t, certPEM)
			} else {
				require.NoError(t, err)
				require.NotNil(t, certPEM)
			}
		})
	}
}

func TestConditionalAccessGetIdPSigningCert(t *testing.T) {
	t.Parallel()
	t.Run("missing server private key", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "" // Not configured
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)
		ctx = test.UserContext(ctx, test.UserAdmin)

		certPEM, err := svc.ConditionalAccessGetIdPSigningCert(ctx)
		var badReqErr *automdm.BadRequestError
		require.ErrorAs(t, err, &badReqErr)
		require.Contains(t, err.Error(), "AutoMDM server private key is not configured")
		require.Nil(t, certPEM)
	})
}

func TestConditionalAccessGetIdPAppleProfileAuth(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	cfg := config.TestConfig()
	cfg.Server.PrivateKey = "test-private-key"
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	// Mock valid certificate
	certPEM := generateTestCertPEM(t)

	// Mock the datastore methods
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetConditionalAccessCACert: {
				Name:  automdm.MDMAssetConditionalAccessCACert,
				Value: certPEM,
			},
		}, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			ServerSettings: automdm.ServerSettings{
				ServerURL: "https://automdm.example.com",
			},
		}, nil
	}

	testCases := []struct {
		name       string
		user       *automdm.User
		shouldFail bool
	}{
		{"global admin", test.UserAdmin, false},
		{"global maintainer", test.UserMaintainer, false},
		{"global observer", test.UserObserver, true},
		{"global observer+", test.UserObserverPlus, true},
		{"global gitops", test.UserGitOps, false},
		{"team admin", test.UserTeamAdminTeam1, true},
		{"team maintainer", test.UserTeamMaintainerTeam1, true},
		{"team observer", test.UserTeamObserverTeam1, true},
		{"team observer+", test.UserTeamObserverPlusTeam1, true},
		{"team gitops", test.UserTeamGitOpsTeam1, true},
		{"user no roles", test.UserNoRoles, true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := test.UserContext(ctx, tt.user)

			profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
			if tt.shouldFail {
				require.Error(t, err)
				var forbiddenError *authz.Forbidden
				require.ErrorAs(t, err, &forbiddenError)
				require.Nil(t, profileData)
			} else {
				require.NoError(t, err)
				require.NotNil(t, profileData)

				// Verify the profile contains expected content
				profileStr := string(profileData)
				require.Contains(t, profileStr, "com.fleetdm.conditional-access")
				require.Contains(t, profileStr, "https://okta.automdm.example.com")
			}
		})
	}
}

// generateTestCertPEM generates a test certificate in PEM format for testing
func generateTestCertPEM(t *testing.T) []byte {
	// Create a simple self-signed certificate
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	return certPEM
}

func TestConditionalAccessGetIdPAppleProfile(t *testing.T) {
	certPEM := generateTestCertPEM(t)

	t.Run("free license", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)
		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(test.UserContext(ctx, test.UserAdmin))
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.Nil(t, profileData)
	})

	t.Run("missing server private key", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "" // Not configured
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		var badReqErr *automdm.BadRequestError
		require.ErrorAs(t, err, &badReqErr)
		require.Contains(t, err.Error(), "AutoMDM server private key is not configured")
		require.Nil(t, profileData)
	})

	t.Run("success - generates valid profile", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: certPEM,
				},
			}, nil
		}

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: "https://automdm.example.com:8080",
				},
			}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, profileData)

		profileStr := string(profileData)
		// Verify XML structure
		require.Contains(t, profileStr, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>")
		require.Contains(t, profileStr, "<!DOCTYPE plist")

		// AutoMDM fills in the SCEP URL and a one-time challenge for each host at delivery.
		require.Regexp(t, `<key>URL</key>\s*<string>\$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_URL</string>`, profileStr)
		require.Regexp(t, `<key>Challenge</key>\s*<string>\$AUTOMDM_VAR_CONDITIONAL_ACCESS_SCEP_CHALLENGE</string>`, profileStr)
		require.Contains(t, profileStr, "https://okta.automdm.example.com:8080")
		vars, err := validateConfigProfileAutoMDMVariables(profileStr, &automdm.LicenseInfo{Tier: automdm.TierPremium}, &automdm.GroupedCertificateAuthorities{})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{
			string(automdm.AutoMDMVarConditionalAccessSCEPURL),
			string(automdm.AutoMDMVarConditionalAccessSCEPChallenge),
			string(automdm.AutoMDMVarCertificateRenewalID),
		}, vars)

		// Verify payload identifiers
		require.Contains(t, profileStr, "com.fleetdm.conditional-access-ca")
		require.Contains(t, profileStr, "com.fleetdm.conditional-access-scep")
		require.Contains(t, profileStr, "com.fleetdm.conditional-access-preference")
		require.Contains(t, profileStr, "com.fleetdm.chrome.certs")

		// Top-level PayloadIdentifier and certificate CN must match the
		// shared constants so the InstallProfile-ack cleanup hook can
		// reliably gate on them.
		require.Contains(t, profileStr, automdm.ConditionalAccessOktaProfileIdentifier)
		require.Contains(t, profileStr, automdm.ConditionalAccessOktaCertificateCN)

		// Verify the renewal-ID marker is in the SCEP payload's Subject OU
		// so auto-renewal activates by default. Substituted to
		// automdm-<profile_uuid> at delivery time; AutoMDM's own SCEP CA
		// preserves OU in the issued cert.
		require.Contains(t, profileStr, "$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID")
		require.Regexp(t,
			`(?s)<key>Subject</key>.*<string>OU</string>\s*<string>\$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID</string>`,
			profileStr,
			"renewal-ID marker must be in Subject OU, not CN",
		)
	})

	t.Run("missing CA certificate", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "conditional access CA certificate not configured")
		require.Nil(t, profileData)
	})

	t.Run("invalid PEM certificate", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: []byte("not a valid PEM"),
				},
			}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to decode CA certificate PEM")
		require.Nil(t, profileData)
	})

	t.Run("invalid DER certificate", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		// Valid PEM structure but invalid DER content
		invalidCertPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: []byte("invalid DER data"),
		})

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: invalidCertPEM,
				},
			}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse CA certificate")
		require.Nil(t, profileData)
	})

	t.Run("server URL not configured", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: certPEM,
				},
			}, nil
		}

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: "",
				},
			}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		var badReqErr *automdm.BadRequestError
		require.ErrorAs(t, err, &badReqErr)
		require.Contains(t, err.Error(), "server URL is not configured")
		require.Nil(t, profileData)
	})

	t.Run("invalid server URL", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: certPEM,
				},
			}, nil
		}

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: "://invalid-url",
				},
			}, nil
		}

		profileData, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse server URL")
		require.Nil(t, profileData)
	})

	t.Run("deterministic UUIDs based on server URL", func(t *testing.T) {
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = test.UserContext(ctx, test.UserAdmin)

		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetConditionalAccessCACert: {
					Name:  automdm.MDMAssetConditionalAccessCACert,
					Value: certPEM,
				},
			}, nil
		}

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: "https://automdm.example.com",
				},
			}, nil
		}

		// Generate profile twice with same server URL
		profileData1, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.NoError(t, err)

		profileData2, err := svc.ConditionalAccessGetIdPAppleProfile(ctx)
		require.NoError(t, err)

		// UUIDs should be identical
		require.Equal(t, profileData1, profileData2)
	})
}
