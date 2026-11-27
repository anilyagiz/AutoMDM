package mock

import (
	"context"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

//go:generate go run ./mockimpl/impl.go -o datastore_mock.go "s *DataStore" "automdm.Datastore"
//go:generate go run ./mockimpl/impl.go -o datastore_installers.go "s *InstallerStore" "automdm.InstallerStore"
//go:generate go run ./mockimpl/impl.go -o nanodep/storage.go "s *Storage" "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/storage.AllDEPStorage"
//go:generate go run ./mockimpl/impl.go -o mdm/datastore_mdm_mock.go "fs *MDMAppleStore" "automdm.MDMAppleStore"
//go:generate go run ./mockimpl/impl.go -o scep/depot.go "d *Depot" "depot.Depot"
//go:generate go run ./mockimpl/impl.go -o scep/config.go "s *SCEPConfigService" "automdm.SCEPConfigService"
//go:generate go run ./mockimpl/impl.go -o scep/enrollment_client.go "c *SCEPEnrollmentClient" "automdm.SCEPEnrollmentClient"
//go:generate go run ./mockimpl/impl.go -o digicert/digicert.go "s *Service" "automdm.DigiCertService"
//go:generate go run ./mockimpl/impl.go -o ejbca/ejbca.go "s *Service" "automdm.EJBCAService"
//go:generate go run ./mockimpl/impl.go -o googlecas/googlecas.go "s *Service" "automdm.GoogleCASService"
//go:generate go run ./mockimpl/impl.go -o mdm/bootstrap_package_store.go "s *MDMBootstrapPackageStore" "automdm.MDMBootstrapPackageStore"
//go:generate go run ./mockimpl/impl.go -o software/software_installer_store.go "s *SoftwareInstallerStore" "automdm.SoftwareInstallerStore"

var _ automdm.Datastore = (*Store)(nil)

type Store struct {
	DataStore
}

func (m *Store) EnrollOrbit(ctx context.Context, opts ...automdm.DatastoreEnrollOrbitOption) (*automdm.Host, error) {
	return nil, nil
}

func (m *Store) LoadHostByOrbitNodeKey(ctx context.Context, orbitNodeKey string) (*automdm.Host, error) {
	if m.LoadHostByOrbitNodeKeyFunc != nil {
		return m.DataStore.LoadHostByOrbitNodeKey(ctx, orbitNodeKey)
	}
	// Default to not-found rather than a nil host with a nil error: callers
	// dereference the returned host, and "no host matches this orbit node
	// key" is the meaningful default for tests that don't mock this method.
	return nil, &mockNotFoundError{}
}

// RefreshMDMWindowsProfilesStatusRollup is a no-op unless a test mocks it: the Windows profile reconciler calls it on every window with
// work, and most of its tests do not care about the rollup.
func (m *Store) RefreshMDMWindowsProfilesStatusRollup(ctx context.Context, hostUUIDs []string) error {
	if m.RefreshMDMWindowsProfilesStatusRollupFunc != nil {
		return m.DataStore.RefreshMDMWindowsProfilesStatusRollup(ctx, hostUUIDs)
	}
	return nil
}

// AllLabelIDsByName falls back to the LabelIDsByName mock unless a test mocks it, so a test's label fixture serves both
// lookups.
func (m *Store) AllLabelIDsByName(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
	if m.AllLabelIDsByNameFunc != nil {
		return m.DataStore.AllLabelIDsByName(ctx, names, filter)
	}
	ids, err := m.LabelIDsByName(ctx, names, filter)
	if err != nil {
		return nil, err
	}
	all := make(map[string][]uint, len(ids))
	for name, id := range ids {
		all[name] = []uint{id}
	}
	return all, nil
}

type mockNotFoundError struct{}

func (e *mockNotFoundError) Error() string    { return "not found" }
func (e *mockNotFoundError) IsNotFound() bool { return true }

func (m *Store) GetCurrentTime(ctx context.Context) (time.Time, error) {
	return time.Time{}, nil
}

func (m *Store) Drop() error                             { return nil }
func (m *Store) MigrateTables(ctx context.Context) error { return nil }
func (m *Store) MigrationStatus(ctx context.Context) (*automdm.MigrationStatus, error) {
	return &automdm.MigrationStatus{}, nil
}
func (m *Store) Name() string { return "mock" }
