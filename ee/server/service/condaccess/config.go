package condaccess

import (
	"context"
	"fmt"

	"github.com/anilyagiz/automdm/v4/pkg/certificate"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/mdm/scep/depot"
)

func initAssets(ctx context.Context, ds automdm.Datastore) error {
	// Check if we have existing assets for both SCEP CA and IdP
	expectedAssets := []automdm.MDMAssetName{
		automdm.MDMAssetConditionalAccessCACert,
		automdm.MDMAssetConditionalAccessCAKey,
		automdm.MDMAssetConditionalAccessIDPCert,
		automdm.MDMAssetConditionalAccessIDPKey,
	}
	savedAssets, err := ds.GetAllMDMConfigAssetsByName(ctx, expectedAssets, nil)
	if err != nil {
		// Allow not found errors or partial results (some assets exist, some don't).
		// If we got some assets back, continue to create the missing ones.
		if !automdm.IsNotFound(err) && len(savedAssets) == 0 {
			return fmt.Errorf("loading existing conditional access assets from the database: %w", err)
		}
	}

	// Check if CA assets need to be created
	_, hasCACert := savedAssets[automdm.MDMAssetConditionalAccessCACert]
	_, hasCAKey := savedAssets[automdm.MDMAssetConditionalAccessCAKey]
	if !hasCACert || !hasCAKey {
		// Create CA cert and key for SCEP
		caCert := depot.NewCACert(
			depot.WithYears(10),
			depot.WithCommonName("AutoMDM conditional access CA"),
			// Signal that the CA is local to the deployment and not necessarily managed by AutoMDM or another external vendor
			depot.WithOrganization("Local certificate authority"),
			depot.WithCountry("US"),
		)
		scepCert, scepKey, err := depot.NewCACertKey(caCert)
		if err != nil {
			return fmt.Errorf("generating conditional access SCEP cert and key: %w", err)
		}

		// Store CA assets encrypted
		var assets []automdm.MDMConfigAsset
		for k, v := range map[automdm.MDMAssetName][]byte{
			automdm.MDMAssetConditionalAccessCACert: certificate.EncodeCertPEM(scepCert),
			automdm.MDMAssetConditionalAccessCAKey:  certificate.EncodePrivateKeyPEM(scepKey),
		} {
			assets = append(assets, automdm.MDMConfigAsset{
				Name:  k,
				Value: v,
			})
		}

		if err := ds.InsertMDMConfigAssets(ctx, assets, nil); err != nil && !mysql.IsDuplicate(err) {
			return fmt.Errorf("inserting conditional access SCEP assets: %w", err)
		}
	}

	// Check if IdP assets need to be created
	_, hasIdPCert := savedAssets[automdm.MDMAssetConditionalAccessIDPCert]
	_, hasIdPKey := savedAssets[automdm.MDMAssetConditionalAccessIDPKey]
	if !hasIdPCert || !hasIdPKey {
		// Create IdP cert and key for SAML signing
		idpCert := depot.NewCACert(
			depot.WithYears(10),
			depot.WithCommonName("AutoMDM conditional access IdP"),
			depot.WithOrganization("Local certificate authority"),
			depot.WithCountry("US"),
		)
		idpCertX509, idpKey, err := depot.NewCACertKey(idpCert)
		if err != nil {
			return fmt.Errorf("generating conditional access IdP cert and key: %w", err)
		}

		// Store IdP assets encrypted
		var assets []automdm.MDMConfigAsset
		for k, v := range map[automdm.MDMAssetName][]byte{
			automdm.MDMAssetConditionalAccessIDPCert: certificate.EncodeCertPEM(idpCertX509),
			automdm.MDMAssetConditionalAccessIDPKey:  certificate.EncodePrivateKeyPEM(idpKey),
		} {
			assets = append(assets, automdm.MDMConfigAsset{
				Name:  k,
				Value: v,
			})
		}

		if err := ds.InsertMDMConfigAssets(ctx, assets, nil); err != nil && !mysql.IsDuplicate(err) {
			return fmt.Errorf("inserting conditional access IdP assets: %w", err)
		}
	}

	return nil
}
