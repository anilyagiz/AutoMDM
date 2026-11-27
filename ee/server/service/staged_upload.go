package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/installersize"
	"github.com/google/uuid"
)

const stagedUploadUnavailableMsg = "Direct upload isn't available on this server."

func (svc *Service) CreateStagedUpload(ctx context.Context, target automdm.StagedUploadTarget, teamID uint, size int64) (*automdm.StagedUpload, error) {
	var err error
	switch target {
	case automdm.StagedUploadTargetBootstrapPackage:
		err = svc.authz.Authorize(ctx, &automdm.MDMAppleBootstrapPackage{TeamID: teamID}, automdm.ActionWrite)
	case automdm.StagedUploadTargetSoftwarePackage:
		err = svc.authz.Authorize(ctx, &automdm.SoftwareInstaller{TeamID: &teamID}, automdm.ActionWrite)
	default:
		svc.authz.SkipAuthorization(ctx)
		return nil, &automdm.BadRequestError{Message: fmt.Sprintf("Unsupported upload target %q.", target)}
	}
	if err != nil {
		return nil, err
	}

	if svc.stagedUploadStore == nil {
		return nil, &automdm.BadRequestError{Message: stagedUploadUnavailableMsg}
	}
	// The installer size middleware only runs on upload routes, so read the config.
	if size <= 0 {
		return nil, &automdm.BadRequestError{Message: "size is required and must be greater than 0."}
	}
	if maxSize := svc.config.Server.MaxInstallerSizeBytes; size > maxSize {
		return nil, &automdm.BadRequestError{Message: fmt.Sprintf("The maximum file size is %s.", installersize.Human(maxSize))}
	}

	uploadID := uuid.NewString()
	url, err := svc.stagedUploadStore.PresignPut(ctx, uploadID, size, automdm.StagedUploadURLExpiry)
	if err != nil {
		if errors.Is(err, automdm.ErrNotConfigured) {
			return nil, &automdm.BadRequestError{Message: stagedUploadUnavailableMsg}
		}
		return nil, ctxerr.Wrap(ctx, err, "presign staged upload")
	}
	return &automdm.StagedUpload{UploadID: uploadID, URL: url, ExpiresAt: time.Now().Add(automdm.StagedUploadURLExpiry)}, nil
}

// openStagedUpload copies a staged upload into a temp file the caller must close.
func (svc *Service) openStagedUpload(ctx context.Context, uploadID string) (*automdm.TempFileReader, error) {
	if svc.stagedUploadStore == nil || !svc.config.StagedUploadAvailable() {
		return nil, &automdm.BadRequestError{Message: stagedUploadUnavailableMsg}
	}
	// The id becomes part of the object key, so only accept the ids AutoMDM hands out.
	parsed, err := uuid.Parse(uploadID)
	if err != nil {
		return nil, &automdm.BadRequestError{Message: "Invalid upload_id."}
	}
	rc, _, err := svc.stagedUploadStore.Get(ctx, parsed.String())
	if err != nil {
		if automdm.IsNotFound(err) {
			return nil, &automdm.BadRequestError{Message: "Upload not found or expired. Please upload the file again."}
		}
		return nil, ctxerr.Wrap(ctx, err, "get staged upload")
	}
	defer rc.Close()
	tfr, err := automdm.NewTempFileReader(rc, nil)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "copy staged upload to temp file")
	}
	return tfr, nil
}

// finishStagedUpload closes the temp file and, if *err is nil, deletes the
// staged upload.
func (svc *Service) finishStagedUpload(ctx context.Context, uploadID string, tfr *automdm.TempFileReader, err *error) {
	tfr.Close()
	if *err == nil {
		svc.deleteStagedUpload(ctx, uploadID)
	}
}

// deleteStagedUpload removes a staged upload once its package is stored. The
// cleanup cron is the backstop, so a failure is only logged.
func (svc *Service) deleteStagedUpload(ctx context.Context, uploadID string) {
	parsed, err := uuid.Parse(uploadID)
	if err != nil {
		return
	}
	if err := svc.stagedUploadStore.Delete(ctx, parsed.String()); err != nil {
		svc.logger.WarnContext(ctx, "deleting staged upload", "upload_id", uploadID, "err", err)
	}
}
