package api

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// EnrollmentService stores records in the acme_enrollments table.
type EnrollmentService interface {
	// NewACMEEnrollment creates a new enrollment in the acme_enrollments table with the specified
	// host identifier and purpose, and returns a new path_identifier for the created row. enrollmentID is
	// required for acme_renewal and must be nil for acme.
	NewACMEEnrollment(ctx context.Context, hostIdentifier string, purpose automdm.AppleMDMCertPurpose, enrollmentID *string) (string, error)
}
