package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// newOTATestSigner returns a certificate issued by ca, carrying binding unless it's nil.
func newOTATestSigner(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, binding *apple_mdm.AppleMDMCertificateBindingExtension) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "AutoMDM Identity"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if binding != nil {
		ext, err := apple_mdm.BuildAppleMDMCertificateBindingExtension(*binding)
		require.NoError(t, err)
		tmpl.ExtraExtensions = []pkix.Extension{ext}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func TestMDMAppleProcessOTAEnrollmentSCEPChallenges(t *testing.T) {
	const (
		secret = "enroll-secret"
		udid   = "phase1-udid"
		serial = "phase1-serial"
	)
	ca, caKey, err := apple_mdm.NewSCEPCACertKey()
	require.NoError(t, err)

	newDS := func() *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: "https://automdm.example.com"}}, nil
		}
		ds.VerifyEnrollSecretFunc = func(ctx context.Context, s string) (*automdm.EnrollSecret, error) {
			return &automdm.EnrollSecret{Secret: s}, nil
		}
		ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, names []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
			return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
				automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})},
			}, nil
		}
		ds.NewAppleSCEPChallengeFunc = func(ctx context.Context, info automdm.AppleSCEPChallengeInfo, ttl time.Duration) (string, error) {
			return "minted-challenge", nil
		}
		return ds
	}

	t.Run("phase 1 hands out a challenge bound to the MachineInfo identity", func(t *testing.T) {
		// treats the test signer as Apple's device CA, which marks phase 1
		dev_mode.SetOverride("AUTOMDM_DEV_MDM_APPLE_DISABLE_DEVICE_INFO_CERT_VERIFY", "1", t)
		ds := newDS()
		var minted automdm.AppleSCEPChallengeInfo
		var mintedTTL time.Duration
		ds.NewAppleSCEPChallengeFunc = func(ctx context.Context, info automdm.AppleSCEPChallengeInfo, ttl time.Duration) (string, error) {
			minted, mintedTTL = info, ttl
			return "minted-challenge", nil
		}
		svc, ctx := newTestService(t, ds, nil, nil)

		signer := newOTATestSigner(t, ca, caKey, nil)
		resp, err := svc.MDMAppleProcessOTAEnrollment(ctx, []*x509.Certificate{signer}, signer, secret, "", false,
			automdm.MDMAppleMachineInfo{UDID: udid, Serial: serial})
		require.NoError(t, err)
		require.Contains(t, string(resp), "minted-challenge")
		require.Equal(t, automdm.AppleSCEPChallengeInfo{Purpose: automdm.AppleMDMCertPurposeOTAPhaseOne, UUID: new(udid), Serial: new(serial)}, minted)
		require.Equal(t, automdm.AppleSCEPEnrollmentChallengeTTL, mintedTTL)
	})

	phaseOne := &apple_mdm.AppleMDMCertificateBindingExtension{Purpose: automdm.AppleMDMCertPurposeOTAPhaseOne, UDID: new(udid), Serial: new(serial)}
	rejected := []struct {
		name       string
		binding    *apple_mdm.AppleMDMCertificateBindingExtension
		deviceInfo automdm.MDMAppleMachineInfo
	}{
		{"signer without a binding", nil, automdm.MDMAppleMachineInfo{UDID: udid, Serial: serial}},
		{"signer with another purpose", &apple_mdm.AppleMDMCertificateBindingExtension{Purpose: automdm.AppleMDMCertPurposeADE, UDID: new(udid), Serial: new(serial)}, automdm.MDMAppleMachineInfo{UDID: udid, Serial: serial}},
		{"device info claims another UDID", phaseOne, automdm.MDMAppleMachineInfo{UDID: "other-udid", Serial: serial}},
		{"device info claims another serial", phaseOne, automdm.MDMAppleMachineInfo{UDID: udid, Serial: "other-serial"}},
	}
	for _, c := range rejected {
		t.Run("phase 2 rejects "+c.name, func(t *testing.T) {
			ds := newDS()
			svc, ctx := newTestService(t, ds, nil, nil)

			signer := newOTATestSigner(t, ca, caKey, c.binding)
			_, err := svc.MDMAppleProcessOTAEnrollment(ctx, []*x509.Certificate{signer}, signer, secret, "", false, c.deviceInfo)
			var statusErr interface{ StatusCode() int }
			require.ErrorAs(t, err, &statusErr)
			require.Equal(t, http.StatusForbidden, statusErr.StatusCode())
			require.False(t, ds.IngestMDMAppleDeviceFromOTAEnrollmentFuncInvoked)
			require.False(t, ds.NewAppleSCEPChallengeFuncInvoked)
		})
	}
}
