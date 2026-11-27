package service

import (
	"context"
	"net/http"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type listCertificateAuthoritiesRequest struct{}

type listCertificateAuthoritiesResponse struct {
	CertificateAuthorities []*automdm.CertificateAuthoritySummary `json:"certificate_authorities"`
	Err                    error                                  `json:"error,omitempty"`
}

func (r listCertificateAuthoritiesResponse) Error() error { return r.Err }

func listCertificateAuthoritiesEndpoint(ctx context.Context, req any, svc automdm.Service) (automdm.Errorer, error) {
	certAuths, err := svc.ListCertificateAuthorities(ctx)
	if err != nil {
		return listCertificateAuthoritiesResponse{Err: err}, nil
	}
	return listCertificateAuthoritiesResponse{
		CertificateAuthorities: certAuths,
	}, nil
}

func (svc *Service) ListCertificateAuthorities(ctx context.Context) ([]*automdm.CertificateAuthoritySummary, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}

type getCertificateAuthorityRequest struct {
	ID uint `url:"id"`
}

type getCertificateAuthorityResponse struct {
	*automdm.CertificateAuthority
	Err error `json:"error,omitempty"`
}

func (r getCertificateAuthorityResponse) Error() error { return r.Err }

func getCertificateAuthorityEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getCertificateAuthorityRequest)
	certAuth, err := svc.GetCertificateAuthority(ctx, req.ID)
	if err != nil {
		return getCertificateAuthorityResponse{Err: err}, nil
	}
	return getCertificateAuthorityResponse{
		CertificateAuthority: certAuth,
	}, nil
}

func (svc *Service) GetCertificateAuthority(ctx context.Context, id uint) (*automdm.CertificateAuthority, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}

type createCertificateAuthorityRequest struct {
	automdm.CertificateAuthorityPayload
}

type createCertificateAuthorityResponse struct {
	ID   uint           `json:"id,omitempty"`
	Name string         `json:"name,omitempty"`
	Type automdm.CAType `json:"type,omitempty"`
	Err  error          `json:"error,omitempty"`
}

func (r createCertificateAuthorityResponse) Error() error { return r.Err }

func createCertificateAuthorityEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*createCertificateAuthorityRequest)

	ca, err := svc.NewCertificateAuthority(ctx, req.CertificateAuthorityPayload)
	if err != nil {
		return createCertificateAuthorityResponse{Err: err}, nil
	}

	return createCertificateAuthorityResponse{ID: ca.ID, Name: *ca.Name, Type: automdm.CAType(ca.Type)}, nil
}

func (svc *Service) NewCertificateAuthority(ctx context.Context, p automdm.CertificateAuthorityPayload) (*automdm.CertificateAuthority, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}

type deleteCertificateAuthorityRequest struct {
	ID uint `url:"id"`
}

type deleteCertificateAuthorityResponse struct {
	Err error `json:"error,omitempty"`
}

func (r deleteCertificateAuthorityResponse) Error() error { return r.Err }
func (r deleteCertificateAuthorityResponse) Status() int  { return http.StatusNoContent }

func deleteCertificateAuthorityEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*deleteCertificateAuthorityRequest)

	err := svc.DeleteCertificateAuthority(ctx, req.ID)
	if err != nil {
		return &deleteCertificateAuthorityResponse{Err: err}, nil
	}

	return &deleteCertificateAuthorityResponse{}, nil
}

func (svc *Service) DeleteCertificateAuthority(ctx context.Context, certificateAuthorityID uint) error {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return automdm.ErrMissingLicense
}

type updateCertificateAuthorityRequest struct {
	ID uint `url:"id"`
	automdm.CertificateAuthorityUpdatePayload
}

type updateCertificateAuthorityResponse struct {
	Err error `json:"error,omitempty"`
}

func (r updateCertificateAuthorityResponse) Error() error { return r.Err }

func updateCertificateAuthorityEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*updateCertificateAuthorityRequest)

	err := svc.UpdateCertificateAuthority(ctx, req.ID, req.CertificateAuthorityUpdatePayload)
	if err != nil {
		return &updateCertificateAuthorityResponse{Err: err}, nil
	}

	return &updateCertificateAuthorityResponse{}, nil
}

func (svc *Service) UpdateCertificateAuthority(ctx context.Context, id uint, payload automdm.CertificateAuthorityUpdatePayload) error {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return automdm.ErrMissingLicense
}

type requestCertificateRequest struct {
	automdm.RequestCertificatePayload
}

type requestCertificateResponse struct {
	Certificate string `json:"certificate"`
	Err         error  `json:"error,omitempty"`
}

func (r requestCertificateResponse) Error() error { return r.Err }

func requestCertificateEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*requestCertificateRequest)

	certificate, err := svc.RequestCertificate(ctx, req.RequestCertificatePayload)
	if err != nil {
		return requestCertificateResponse{Err: err}, nil
	}

	return requestCertificateResponse{Certificate: *certificate}, nil
}

func (svc *Service) RequestCertificate(ctx context.Context, p automdm.RequestCertificatePayload) (*string, error) {
	// skipauth: No authorization check needed due to implementation returning only license error.
	svc.authz.SkipAuthorization(ctx)
	return nil, automdm.ErrMissingLicense
}

type batchApplyCertificateAuthoritiesRequest struct {
	CertificateAuthorities automdm.GroupedCertificateAuthorities `json:"certificate_authorities"`
	DryRun                 bool                                  `json:"dry_run"`
	SkipDeletes            bool                                  `json:"skip_deletes"`
}

// TODO(hca): do we need to return anything to facilitate logging by the gitops client?
type batchApplyCertificateAuthoritiesResponse struct {
	Err error `json:"error,omitempty"`
}

func (r batchApplyCertificateAuthoritiesResponse) Error() error { return r.Err }

func batchApplyCertificateAuthoritiesEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*batchApplyCertificateAuthoritiesRequest)

	err := svc.BatchApplyCertificateAuthorities(ctx, req.CertificateAuthorities, automdm.BatchApplyCertificateAuthoritiesOpts{
		DryRun:      req.DryRun,
		ViaGitOps:   true,
		SkipDeletes: req.SkipDeletes,
	})
	if err != nil {
		return &batchApplyCertificateAuthoritiesResponse{Err: err}, nil
	}

	return &batchApplyCertificateAuthoritiesResponse{}, nil
}

func (svc *Service) BatchApplyCertificateAuthorities(ctx context.Context, incoming automdm.GroupedCertificateAuthorities, opts automdm.BatchApplyCertificateAuthoritiesOpts) error {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionWrite); err != nil {
		return err
	}

	if incoming.IsEmpty() {
		return nil
	}

	return automdm.ErrMissingLicense
}

type getCertificateAuthoritiesSpecRequest struct {
	IncludeSecrets bool `query:"include_secrets,optional"`
}

type getCertificateAuthoritiesSpecResponse struct {
	CertificateAuthorities *automdm.GroupedCertificateAuthorities `json:"certificate_authorities"`
	Err                    error                                  `json:"error,omitempty"`
}

func (r getCertificateAuthoritiesSpecResponse) Error() error { return r.Err }

func getCertificateAuthoritiesSpecEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getCertificateAuthoritiesSpecRequest)

	certificateAuthorities, err := svc.GetGroupedCertificateAuthorities(ctx, req.IncludeSecrets)
	if err != nil {
		return &getCertificateAuthoritiesSpecResponse{Err: err}, nil
	}

	return &getCertificateAuthoritiesSpecResponse{CertificateAuthorities: certificateAuthorities}, nil
}

func (svc *Service) GetGroupedCertificateAuthorities(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
	action := automdm.ActionRead
	if includeSecrets {
		action = automdm.ActionReadSecrets
	}
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, action); err != nil {
		return nil, err
	}

	return svc.ds.GetGroupedCertificateAuthorities(ctx, includeSecrets)
}
