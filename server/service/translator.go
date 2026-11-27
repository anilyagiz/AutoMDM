package service

import (
	"context"
	"fmt"

	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
)

type translatorRequest struct {
	List []automdm.TranslatePayload `json:"list"`
}

type translatorResponse struct {
	List []automdm.TranslatePayload `json:"list"`
	Err  error                      `json:"error,omitempty"`
}

func (r translatorResponse) Error() error { return r.Err }

func translatorEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*translatorRequest)
	resp, err := svc.Translate(ctx, req.List)
	if err != nil {
		return translatorResponse{Err: err}, nil
	}
	return translatorResponse{List: resp}, nil
}

type translateFunc func(ctx context.Context, ds automdm.Datastore, identifier string) (uint, error)

func translateEmailToUserID(ctx context.Context, ds automdm.Datastore, identifier string) (uint, error) {
	user, err := ds.UserByEmail(ctx, identifier)
	if err != nil {
		return 0, err
	}
	return user.ID, nil
}

func translateLabelToID(ctx context.Context, ds automdm.Datastore, identifier string) (uint, error) {
	labelIDsByName, err := ds.AllLabelIDsByName(ctx, []string{identifier}, automdm.TeamFilter{User: authz.UserFromContext(ctx)})
	if err != nil {
		return 0, err
	}
	// Every row matched the one requested name, possibly as case variants stored on different teams.
	var matches []uint
	for _, ids := range labelIDsByName {
		matches = append(matches, ids...)
	}
	if len(matches) > 1 {
		return 0, ctxerr.Wrap(ctx, automdm.NewAmbiguousLabelNameError(identifier))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return 0, nil
}

func translateTeamToID(ctx context.Context, ds automdm.Datastore, identifier string) (uint, error) {
	team, err := ds.TeamByName(ctx, identifier)
	if err != nil {
		return 0, err
	}
	return team.ID, nil
}

func translateHostToID(ctx context.Context, ds automdm.Datastore, identifier string) (uint, error) {
	return ds.HostIDByIdentifier(ctx, identifier, automdm.HostIDByIdentifierOptions{})
}

func (svc *Service) Translate(ctx context.Context, payloads []automdm.TranslatePayload) ([]automdm.TranslatePayload, error) {
	if len(payloads) == 0 {
		// skip auth since there is no case in which this request will make sense with no payloads
		svc.authz.SkipAuthorization(ctx)
		return nil, badRequest("payloads must not be empty")
	}

	var finalPayload []automdm.TranslatePayload

	for _, payload := range payloads {
		var translateFunc translateFunc

		switch payload.Type {
		case automdm.TranslatorTypeUserEmail:
			if err := svc.authz.Authorize(ctx, &automdm.User{}, automdm.ActionRead); err != nil {
				return nil, err
			}
			translateFunc = translateEmailToUserID
		case automdm.TranslatorTypeLabel:
			if err := svc.authz.Authorize(ctx, &automdm.Label{}, automdm.ActionRead); err != nil {
				return nil, err
			}
			translateFunc = translateLabelToID
		case automdm.TranslatorTypeAutoMDM:
			if err := svc.authz.Authorize(ctx, &automdm.Team{}, automdm.ActionRead); err != nil {
				return nil, err
			}
			translateFunc = translateTeamToID
		case automdm.TranslatorTypeHost:
			if err := svc.authz.Authorize(ctx, &automdm.Host{}, automdm.ActionRead); err != nil {
				return nil, err
			}
			translateFunc = translateHostToID
		default:
			// if no supported payload type, this is bad regardless of authorization
			svc.authz.SkipAuthorization(ctx)
			if entry, ok := removednames.Find(removednames.KindTargetType, payload.Type, ""); ok {
				return nil, automdm.NewInvalidArgumentError("type", entry.Message())
			}
			return nil, endpointer.BadRequestErr(
				fmt.Sprintf("Type %s is unknown. ", payload.Type),
				automdm.NewErrorf(
					automdm.ErrNoUnknownTranslate,
					"Type %s is unknown.",
					payload.Type),
			)
		}

		id, err := translateFunc(ctx, svc.ds, payload.Payload.Identifier)
		if err != nil {
			return nil, err
		}
		payload.Payload.ID = id
		finalPayload = append(finalPayload, automdm.TranslatePayload{
			Type:    payload.Type,
			Payload: payload.Payload,
		})
	}

	return finalPayload, nil
}
