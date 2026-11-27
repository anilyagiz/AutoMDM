package automdm

import (
	"context"
)

type DigiCertCertificate = IssuedCertificate

type DigiCertService interface {
	VerifyProfileID(ctx context.Context, config DigiCertCA) error
	GetCertificate(ctx context.Context, config DigiCertCA) (*DigiCertCertificate, error)
}
