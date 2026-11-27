package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/mdm/mdmtest"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/scep/depot"
	scepserver "github.com/anilyagiz/automdm/v4/server/mdm/scep/server"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/jmoiron/sqlx"
	"github.com/smallstep/scep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSCEPPKIOperationUndecryptableEnvelope(t *testing.T) {
	caCert, caKey, err := depot.NewSCEPCACertKey()
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)})

	ds := new(mock.Store)
	ds.GetAllMDMConfigAssetsByNameFunc = func(_ context.Context, _ []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Name: automdm.MDMAssetCACert, Value: certPEM},
			automdm.MDMAssetCAKey:  {Name: automdm.MDMAssetCAKey, Value: keyPEM},
		}, nil
	}

	signerCalled := false
	signer := scepserver.CSRSignerContextFunc(func(context.Context, *scep.CSRReqMessage) (*x509.Certificate, error) {
		signerCalled = true
		return &x509.Certificate{}, nil
	})
	svc := NewSCEPService(ds, signer, slog.New(slog.DiscardHandler))

	req, err := mdmtest.NewPKCSReqUndecryptableBy(caCert)
	require.NoError(t, err)

	respBytes, err := svc.PKIOperation(t.Context(), req.Raw)
	require.NoError(t, err)

	certRep, err := scep.ParsePKIMessage(respBytes)
	require.NoError(t, err)
	assert.Equal(t, scep.FAILURE, certRep.PKIStatus)
	assert.Equal(t, scep.BadRequest, certRep.FailInfo)
	assert.Equal(t, req.TransactionID, certRep.TransactionID)
	assert.False(t, signerCalled, "signer must not run when the envelope cannot be decrypted")
}

func TestSCEPPKIOperationRejectsMalformedBody(t *testing.T) {
	svc := NewSCEPService(new(mock.Store), nil, slog.New(slog.DiscardHandler))

	_, err := svc.PKIOperation(t.Context(), []byte("hello"))

	var badReq *automdm.BadRequestError
	require.ErrorAs(t, err, &badReq)
}
