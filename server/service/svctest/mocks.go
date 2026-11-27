package svctest

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/scep/depot"
)

// mockMailService is the no-op mailer used by the test service.
type mockMailService struct {
	SendEmailFn func(e automdm.Email) error
	Invoked     bool
}

func (svc *mockMailService) SendEmail(_ context.Context, e automdm.Email) error {
	svc.Invoked = true
	return svc.SendEmailFn(e)
}

func (svc *mockMailService) CanSendEmail(smtpSettings automdm.SMTPSettings) bool {
	return smtpSettings.SMTPConfigured
}

// nopEnrollHostLimiter is a no-op automdm.EnrollHostLimiter for tests.
type nopEnrollHostLimiter struct{}

func (nopEnrollHostLimiter) CanEnrollNewHost(_ context.Context) (bool, error) {
	return true, nil
}

func (nopEnrollHostLimiter) SyncEnrolledHostIDs(_ context.Context) error {
	return nil
}

// acmeCSRSigner adapts a depot.Signer to the acme.CSRSigner interface.
type acmeCSRSigner struct {
	signer *depot.Signer
}

func (a *acmeCSRSigner) SignX509CSRWithCallback(csr *x509.CertificateRequest, subject pkix.Name, callback func(*x509.Certificate)) (*x509.Certificate, error) {
	return a.signer.SignX509CSRWithCallback(csr, subject, callback)
}
