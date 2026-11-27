// Package acmeacl provides the anti-corruption layer between the ACME
// bounded context and legacy AutoMDM code.
//
// This package is the ONLY place that imports both ACME types and team types.
// It translates between them, allowing the ACME context to remain decoupled
// from legacy code.
package acmeacl

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/acme"
)

// AutoMDMDatastoreAdapter adapts automdm.Datastore to the narrow
// acme.DataProviders interface that the ACME bounded context requires.
type AutoMDMDatastoreAdapter struct {
	ds     automdm.Datastore
	signer acme.CSRSigner
}

// NewTeamDatastoreAdapter creates a new adapter for the AutoMDM datastore.
func NewTeamDatastoreAdapter(ds automdm.Datastore, signer acme.CSRSigner) *AutoMDMDatastoreAdapter {
	return &AutoMDMDatastoreAdapter{ds: ds, signer: signer}
}

// Ensure AutoMDMDatastoreAdapter implements acme.DataProviders
var _ acme.DataProviders = (*AutoMDMDatastoreAdapter)(nil)

func (a *AutoMDMDatastoreAdapter) ServerURL(ctx context.Context) (string, error) {
	appCfg, err := a.ds.AppConfig(ctx)
	if err != nil {
		return "", err
	}
	return appCfg.MDMUrl(), nil
}

func (a *AutoMDMDatastoreAdapter) GetCACertificatePEM(ctx context.Context) ([]byte, error) {
	assets, err := a.ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{automdm.MDMAssetCACert}, nil)
	if err != nil {
		return nil, err
	}
	return assets[automdm.MDMAssetCACert].Value, nil
}

func (a *AutoMDMDatastoreAdapter) CSRSigner(_ context.Context) (acme.CSRSigner, error) {
	return a.signer, nil
}

func (a *AutoMDMDatastoreAdapter) IsDEPEnrolled(ctx context.Context, serial string) (bool, error) {
	assignments, err := a.ds.GetHostDEPAssignmentsBySerial(ctx, serial)
	if err != nil {
		return false, err
	}
	return len(assignments) > 0, nil
}
