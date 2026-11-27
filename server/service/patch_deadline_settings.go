package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

//////////////////////////////////////////////////////////////////////////////
// Get patch deadline settings
//////////////////////////////////////////////////////////////////////////////

type getPatchDeadlineSettingsRequest struct {
	AutoMDMID *uint `query:"team_id,optional"`
}

type patchDeadlineSettingsResponse struct {
	PatchDeadlineSettings *automdm.PatchDeadlineSettings `json:"patch_deadline_settings,omitempty"`
	Err                   error                          `json:"error,omitempty"`
}

func (r patchDeadlineSettingsResponse) Error() error { return r.Err }

func getPatchDeadlineSettingsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getPatchDeadlineSettingsRequest)
	settings, err := svc.GetPatchDeadlineSettings(ctx, req.AutoMDMID)
	if err != nil {
		return patchDeadlineSettingsResponse{Err: err}, nil
	}
	return patchDeadlineSettingsResponse{PatchDeadlineSettings: settings}, nil
}

func (svc *Service) GetPatchDeadlineSettings(ctx context.Context, _ *uint) (*automdm.PatchDeadlineSettings, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

//////////////////////////////////////////////////////////////////////////////
// Update patch deadline settings
//////////////////////////////////////////////////////////////////////////////

type updatePatchDeadlineSettingsRequest struct {
	AutoMDMID *uint `json:"team_id"`
	Days      any   `json:"days"`
	Time      any   `json:"time"`
}

func updatePatchDeadlineSettingsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*updatePatchDeadlineSettingsRequest)
	settings, err := svc.UpdatePatchDeadlineSettings(ctx, req.AutoMDMID, req.Days, req.Time)
	if err != nil {
		return patchDeadlineSettingsResponse{Err: err}, nil
	}
	return patchDeadlineSettingsResponse{PatchDeadlineSettings: settings}, nil
}

func (svc *Service) UpdatePatchDeadlineSettings(ctx context.Context, _ *uint, _, _ any) (*automdm.PatchDeadlineSettings, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}
