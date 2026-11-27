package service

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/ptr"
)

func (svc *Service) GetPatchDeadlineSettings(ctx context.Context, teamID *uint) (*automdm.PatchDeadlineSettings, error) {
	if err := svc.authorizePatchDeadlineSettings(ctx, teamID, automdm.ActionRead); err != nil {
		return nil, err
	}
	settings, err := svc.ds.GetPatchDeadlineSettings(ctx, *teamID)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "get patch deadline settings")
	}
	return settings, nil
}

func (svc *Service) UpdatePatchDeadlineSettings(ctx context.Context, teamID *uint, days, tm any) (*automdm.PatchDeadlineSettings, error) {
	if err := svc.authorizePatchDeadlineSettings(ctx, teamID, automdm.ActionWrite); err != nil {
		return nil, err
	}

	validDays, validTime, err := automdm.ValidatePatchDeadlineSettings(days, tm)
	if err != nil {
		return nil, err
	}

	ctx = ctxdb.RequirePrimary(ctx, true)
	current, err := svc.ds.GetPatchDeadlineSettings(ctx, *teamID)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "get patch deadline settings")
	}
	if slices.Equal(current.Days, validDays) && current.Time == validTime {
		return current, nil
	}

	updated := automdm.PatchDeadlineSettings{TeamID: *teamID, Days: validDays, Time: validTime}
	if err := svc.ds.SetPatchDeadlineSettings(ctx, updated); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "set patch deadline settings")
	}

	teamName, err := svc.teamNameForActivity(ctx, *teamID)
	if err != nil {
		return nil, err
	}
	activity := automdm.ActivityTypeEditedPatchDeadlineSettings{
		TeamID:   ptr.UintOrNilIfZero(*teamID),
		TeamName: teamName,
		Days:     validDays,
		Time:     validTime,
	}
	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), activity); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for edited patch deadline settings")
	}
	return &updated, nil
}

// authorizePatchDeadlineSettings checks that teamID is present, authorized for action,
// and refers to an existing team.
func (svc *Service) authorizePatchDeadlineSettings(ctx context.Context, teamID *uint, action string) error {
	if teamID == nil {
		svc.authz.SkipAuthorization(ctx)
		return automdm.NewInvalidArgumentError("team_id", "team_id is required")
	}
	if err := svc.authz.Authorize(ctx, &automdm.PatchDeadlineSettings{TeamID: *teamID}, action); err != nil {
		return err
	}
	if *teamID != 0 {
		exists, err := svc.ds.TeamExists(ctx, *teamID)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "checking if team exists")
		}
		if !exists {
			return automdm.NewInvalidArgumentError("team_id", fmt.Sprintf("team %d does not exist", *teamID)).WithStatus(http.StatusNotFound)
		}
	}
	return nil
}
