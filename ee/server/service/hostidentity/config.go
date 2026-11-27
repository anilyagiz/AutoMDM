package hostidentity

import (
	"context"
	"fmt"

	"github.com/anilyagiz/automdm/v4/pkg/certificate"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/scep/depot"
)

func initAssets(ds automdm.Datastore) error {
	// Check if we have existing certs and keys
	expectedAssets := []automdm.MDMAssetName{
		automdm.MDMAssetHostIdentityCACert,
		automdm.MDMAssetHostIdentityCAKey,
	}
	savedAssets, err := ds.GetAllMDMConfigAssetsByName(context.Background(), expectedAssets, nil)
	if err != nil {
		// allow not found errors as it means we're generating the assets for the first time.
		if !automdm.IsNotFound(err) {
			return fmt.Errorf("loading existing host identity assets from the database: %w", err)
		}
	}

	if len(savedAssets) != len(expectedAssets) {
		// Then we should create them
		caCert := depot.NewCACert(
			depot.WithYears(10),
			depot.WithCommonName("AutoMDM Host Identity CA"),
			// Signal that the CA is local to the deployment and not necessarily managed by AutoMDM or another external vendor
			depot.WithOrganization("Local Certificate Authority"),
			depot.WithCountry("US"),
		)
		scepCert, scepKey, err := depot.NewCACertKey(caCert)
		if err != nil {
			return fmt.Errorf("generating host identity SCEP cert and key: %w", err)
		}

		// Store our config assets encrypted
		var assets []automdm.MDMConfigAsset
		for k, v := range map[automdm.MDMAssetName][]byte{
			automdm.MDMAssetHostIdentityCACert: certificate.EncodeCertPEM(scepCert),
			automdm.MDMAssetHostIdentityCAKey:  certificate.EncodePrivateKeyPEM(scepKey),
		} {
			assets = append(assets, automdm.MDMConfigAsset{
				Name:  k,
				Value: v,
			})
		}

		if err := ds.InsertMDMConfigAssets(context.Background(), assets, nil); err != nil {
			return fmt.Errorf("inserting host identity SCEP assets: %w", err)
		}
	}
	return nil
}
