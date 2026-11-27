package mock

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// MockACMEService is a mock implementation of automdm.ACMEWriteService
// for unit tests that use mock.Store instead of real MySQL connections.
type MockACMEService struct {
	NewACMEEnrollmentFunc        func(ctx context.Context, hostIdentifier string, purpose automdm.AppleMDMCertPurpose, enrollmentID *string) (string, error)
	NewACMEEnrollmentFuncInvoked bool
}

// Ensure MockACMEService implements automdm.ACMEWriteService.
var _ automdm.ACMEWriteService = (*MockACMEService)(nil)

func (m *MockACMEService) NewACMEEnrollment(ctx context.Context, hostIdentifier string, purpose automdm.AppleMDMCertPurpose, enrollmentID *string) (string, error) {
	m.NewACMEEnrollmentFuncInvoked = true
	if m.NewACMEEnrollmentFunc != nil {
		return m.NewACMEEnrollmentFunc(ctx, hostIdentifier, purpose, enrollmentID)
	}
	return "", nil
}
