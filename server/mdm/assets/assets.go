package assets

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	nanodep_client "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/tokenpki"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/cryptoutil"
)

func CAKeyPair(ctx context.Context, ds automdm.MDMAssetRetriever) (*tls.Certificate, error) {
	return KeyPair(ctx, ds, automdm.MDMAssetCACert, automdm.MDMAssetCAKey)
}

// CADecryptRetriever is the subset of automdm.Datastore needed to load the Apple
// MDM CA private key plus every historical CA certificate for CMS decryption.
type CADecryptRetriever interface {
	automdm.MDMAssetRetriever
	GetAllMDMConfigAssetsByNameIncludingDeleted(ctx context.Context, assetNames []automdm.MDMAssetName) ([]automdm.MDMConfigAsset, error)
}

// CACertsAndKeyForDecryption returns the Apple MDM CA private key together with
// every CA certificate (current and previously-rolled-over) whose public key
// matches that private key, newest first. Pass the returned certs to
// mdm.DecryptBase64CMSWithCerts so payloads escrowed against an earlier CA cert
// still decrypt after a rollover — see that function for why the cert (not just
// the key) matters. Certs whose public key does not match the current private
// key (e.g. a CA from a prior key rotation) are skipped, since they could not
// decrypt with this key anyway.
func CACertsAndKeyForDecryption(ctx context.Context, ds CADecryptRetriever) ([]*x509.Certificate, crypto.PrivateKey, error) {
	keyPair, err := CAKeyPair(ctx, ds)
	if err != nil {
		return nil, nil, err
	}
	signer, ok := keyPair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("apple mdm ca private key is not a crypto.Signer")
	}
	pub := signer.Public()

	historical, err := ds.GetAllMDMConfigAssetsByNameIncludingDeleted(ctx, []automdm.MDMAssetName{automdm.MDMAssetCACert})
	if err != nil {
		return nil, nil, fmt.Errorf("loading historical CA certificates: %w", err)
	}

	type publicKeyEqual interface {
		Equal(crypto.PublicKey) bool
	}

	var certs []*x509.Certificate
	for _, asset := range historical {
		block, _ := pem.Decode(asset.Value)
		if block == nil || block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if eq, ok := cert.PublicKey.(publicKeyEqual); ok && eq.Equal(pub) {
			certs = append(certs, cert)
		}
	}

	// Fall back to the keypair leaf if the include-deleted lookup yielded
	// nothing usable, so behaviour matches the previous single-cert path.
	if len(certs) == 0 {
		certs = append(certs, keyPair.Leaf)
	}

	return certs, keyPair.PrivateKey, nil
}

func APNSKeyPair(ctx context.Context, ds automdm.MDMAssetRetriever) (*tls.Certificate, string, error) {
	return KeyPairWithMD5(ctx, ds, automdm.MDMAssetAPNSCert, automdm.MDMAssetAPNSKey)
}

func KeyPair(ctx context.Context, ds automdm.MDMAssetRetriever, certName, keyName automdm.MDMAssetName) (*tls.Certificate, error) {
	assets, err := ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{
		certName,
		keyName,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("loading %s, %s keypair from the database: %w", certName, keyName, err)
	}

	cert, err := tls.X509KeyPair(assets[certName].Value, assets[keyName].Value)
	if err != nil {
		return nil, fmt.Errorf("parsing %s, %s keypair: %w", certName, keyName, err)
	}

	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing %s certificate leaf: %w", certName, err)
	}

	return &cert, nil
}

// KeyPairWithMD5 returns the certificate from the keypair, along with the MD5 checksum of the certificate.
func KeyPairWithMD5(ctx context.Context, ds automdm.MDMAssetRetriever, certName, keyName automdm.MDMAssetName) (*tls.Certificate, string, error) {
	assets, err := ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{
		certName,
		keyName,
	}, nil)
	if err != nil {
		return nil, "", fmt.Errorf("loading %s, %s keypair from the database: %w", certName, keyName, err)
	}

	cert, err := tls.X509KeyPair(assets[certName].Value, assets[keyName].Value)
	if err != nil {
		return nil, "", fmt.Errorf("parsing %s, %s keypair: %w", certName, keyName, err)
	}

	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, "", fmt.Errorf("parsing %s certificate leaf: %w", certName, err)
	}

	return &cert, assets[certName].MD5Checksum, nil
}

func X509Cert(ctx context.Context, ds automdm.MDMAssetRetriever, certName automdm.MDMAssetName) (*x509.Certificate, error) {
	assets, err := ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{certName}, nil)
	if err != nil {
		return nil, fmt.Errorf("loading certificate %s from the database: %w", certName, err)
	}

	block, _ := pem.Decode(assets[certName].Value)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decoding certificate PEM data: %w", err)
	}

	return x509.ParseCertificate(block.Bytes)
}

func APNSTopic(ctx context.Context, ds automdm.MDMAssetRetriever) (string, error) {
	cert, err := X509Cert(ctx, ds, automdm.MDMAssetAPNSCert)
	if err != nil {
		return "", fmt.Errorf("retrieving APNs cert: %w", err)
	}

	mdmPushCertTopic, err := cryptoutil.TopicFromCert(cert)
	if err != nil {
		return "", fmt.Errorf("extracting topic from APNs certificate: %w", err)
	}

	return mdmPushCertTopic, nil
}

func ABMToken(ctx context.Context, ds automdm.MDMAssetRetriever, abmOrgName string) (*nanodep_client.OAuth1Tokens, error) {
	assets, err := ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{
		automdm.MDMAssetABMKey,
		automdm.MDMAssetABMCert,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("loading ABM assets from the database: %w", err)
	}

	abmTok, err := ds.GetABMTokenByOrgName(ctx, abmOrgName)
	if err != nil {
		return nil, fmt.Errorf("get ABM token by name: %w", err)
	}

	cert, err := tls.X509KeyPair(assets[automdm.MDMAssetABMCert].Value, assets[automdm.MDMAssetABMKey].Value)
	if err != nil {
		return nil, fmt.Errorf("parsing ABM keypair: %w", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing ABM certificate: %w", err)
	}

	oAuthTok, err := DecryptRawABMToken(
		abmTok.EncryptedToken,
		leaf,
		assets[automdm.MDMAssetABMKey].Value,
	)
	if err != nil {
		return nil, fmt.Errorf("decrypting ABM token: %w", err)
	}

	return oAuthTok, nil
}

func DecryptRawABMToken(tokenBytes []byte, cert *x509.Certificate, keyPEM []byte) (*nanodep_client.OAuth1Tokens, error) {
	bmKey, err := tokenpki.RSAKeyFromPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	token, err := tokenpki.DecryptTokenJSON(tokenBytes, cert, bmKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	var jsonTok nanodep_client.OAuth1Tokens
	if err := json.Unmarshal(token, &jsonTok); err != nil {
		return nil, fmt.Errorf("unmarshal JSON token: %w", err)
	}
	return &jsonTok, nil
}
