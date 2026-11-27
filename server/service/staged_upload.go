package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type createStagedUploadRequest struct {
	Target    automdm.StagedUploadTarget `json:"target"`
	AutoMDMID uint                       `json:"team_id"`
	Size      int64                      `json:"size"`
}

type createStagedUploadResponse struct {
	*automdm.StagedUpload
	Err error `json:"error,omitempty"`
}

func (r createStagedUploadResponse) Error() error { return r.Err }

func createStagedUploadEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*createStagedUploadRequest)
	upload, err := svc.CreateStagedUpload(ctx, req.Target, req.AutoMDMID, req.Size)
	if err != nil {
		return createStagedUploadResponse{Err: err}, nil
	}
	return createStagedUploadResponse{StagedUpload: upload}, nil
}

func (svc *Service) CreateStagedUpload(ctx context.Context, target automdm.StagedUploadTarget, teamID uint, size int64) (*automdm.StagedUpload, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}
