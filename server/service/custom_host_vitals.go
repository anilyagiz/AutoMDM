package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"golang.org/x/text/unicode/norm"
)

//////////////////////////////////////////////////////////////////////////////////
// List custom host vitals
//////////////////////////////////////////////////////////////////////////////////

func listCustomHostVitalsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ListCustomHostVitalsRequest)
	vitals, meta, count, err := svc.ListCustomHostVitals(ctx, req.ListOptions)
	return automdm.ListCustomHostVitalsResponse{
		CustomHostVitals: vitals,
		Meta:             meta,
		Count:            count,
		Err:              err,
	}, nil
}

func (svc *Service) ListCustomHostVitals(
	ctx context.Context,
	opts automdm.ListOptions,
) (customHostVitals []automdm.CustomHostVital, meta *automdm.PaginationMetadata, count int, err error) {
	if err := svc.authz.Authorize(ctx, &automdm.CustomHostVital{}, automdm.ActionRead); err != nil {
		return nil, nil, 0, err
	}

	// Always include pagination info.
	opts.IncludeMetadata = true
	if opts.OrderKey == "" {
		opts.OrderKey = "name"
		opts.OrderDirection = automdm.OrderAscending
	}

	customHostVitals, meta, count, err = svc.ds.ListCustomHostVitals(ctx, opts)
	if err != nil {
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "list custom host vitals")
	}
	return customHostVitals, meta, count, nil
}

//////////////////////////////////////////////////////////////////////////////////
// Create custom host vital
//////////////////////////////////////////////////////////////////////////////////

func createCustomHostVitalEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.CreateCustomHostVitalRequest)
	vital, err := svc.CreateCustomHostVital(ctx, req.Name)
	if err != nil {
		return automdm.CreateCustomHostVitalResponse{Err: err}, nil
	}
	return automdm.CreateCustomHostVitalResponse{CustomHostVital: vital}, nil
}

func (svc *Service) CreateCustomHostVital(ctx context.Context, name string) (*automdm.CustomHostVital, error) {
	if err := svc.authz.Authorize(ctx, &automdm.CustomHostVital{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	if err := automdm.ValidateCustomHostVitalName(name); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "validate custom host vital name")
	}

	vital, err := svc.ds.CreateCustomHostVital(ctx, name)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "creating custom host vital")
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeCreatedCustomHostVital{
			CustomHostVitalID:   vital.ID,
			CustomHostVitalName: vital.Name,
		},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for custom host vital creation")
	}

	return &vital, nil
}

//////////////////////////////////////////////////////////////////////////////////
// Update (rename) custom host vital
//////////////////////////////////////////////////////////////////////////////////

func updateCustomHostVitalEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.UpdateCustomHostVitalRequest)
	vital, err := svc.UpdateCustomHostVital(ctx, req.ID, req.Name)
	if err != nil {
		return automdm.UpdateCustomHostVitalResponse{Err: err}, nil
	}
	return automdm.UpdateCustomHostVitalResponse{CustomHostVital: vital}, nil
}

func (svc *Service) UpdateCustomHostVital(ctx context.Context, id uint, name string) (*automdm.CustomHostVital, error) {
	if err := svc.authz.Authorize(ctx, &automdm.CustomHostVital{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	if err := automdm.ValidateCustomHostVitalName(name); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "validate custom host vital name")
	}

	vital, err := svc.ds.UpdateCustomHostVital(ctx, id, name)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "updating custom host vital")
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeEditedCustomHostVital{
			CustomHostVitalID:   vital.ID,
			CustomHostVitalName: vital.Name,
		},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for custom host vital edit")
	}

	return &vital, nil
}

//////////////////////////////////////////////////////////////////////////////////
// Delete custom host vital
//////////////////////////////////////////////////////////////////////////////////

func deleteCustomHostVitalEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.DeleteCustomHostVitalRequest)
	err := svc.DeleteCustomHostVital(ctx, req.ID)
	return automdm.DeleteCustomHostVitalResponse{Err: err}, nil
}

func (svc *Service) DeleteCustomHostVital(ctx context.Context, id uint) error {
	if err := svc.authz.Authorize(ctx, &automdm.CustomHostVital{}, automdm.ActionWrite); err != nil {
		return err
	}

	name, err := svc.ds.DeleteCustomHostVital(ctx, id)
	if err != nil {
		if usedErr, ok := errors.AsType[*automdm.CustomHostVitalUsedError](err); ok {
			return ctxerr.Wrap(ctx, &automdm.ConflictError{
				Message: fmt.Sprintf("Couldn't delete. %s", usedErr.Error()),
			}, "delete custom host vital")
		}
		return ctxerr.Wrap(ctx, err, "delete custom host vital")
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeDeletedCustomHostVital{
			CustomHostVitalID:   id,
			CustomHostVitalName: name,
		},
	); err != nil {
		return ctxerr.Wrap(ctx, err, "create activity for custom host vital deletion")
	}

	return nil
}

//////////////////////////////////////////////////////////////////////////////////
// Set host custom host vital value
//////////////////////////////////////////////////////////////////////////////////

func setHostCustomHostVitalValueEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.SetHostCustomHostVitalValueRequest)
	err := svc.SetHostCustomHostVitalValue(ctx, req.HostID, req.ID, req.Value)
	return automdm.SetHostCustomHostVitalValueResponse{Err: err}, nil
}

func (svc *Service) SetHostCustomHostVitalValue(ctx context.Context, hostID uint, vitalID uint, value string) error {
	// Authorize against the host so team-scoped roles are enforced (host-write pattern).
	if err := svc.authz.Authorize(ctx, &automdm.Host{}, automdm.ActionList); err != nil {
		return err
	}

	host, err := svc.ds.HostLite(ctx, hostID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "find host for setting custom host vital value")
	}

	if err := svc.authz.Authorize(ctx, &automdm.HostCustomHostVitalValue{TeamID: host.TeamID}, automdm.ActionWrite); err != nil {
		return err
	}

	vital, err := svc.customHostVitalByID(ctx, vitalID)
	if err != nil {
		return err
	}

	if err := svc.ds.SetHostCustomHostVitalValue(ctx, hostID, vitalID, value); err != nil {
		return ctxerr.Wrap(ctx, err, "set host custom host vital value")
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeEditedCustomHostVitalValue{
			HostID:              hostID,
			HostDisplayName:     host.DisplayName(),
			CustomHostVitalID:   vitalID,
			CustomHostVitalName: vital.Name,
		},
	); err != nil {
		return ctxerr.Wrap(ctx, err, "create activity for custom host vital value edit")
	}

	return nil
}

func (svc *Service) customHostVitalByID(ctx context.Context, id uint) (*automdm.CustomHostVital, error) {
	vitals, err := svc.ds.GetCustomHostVitals(ctx, []uint{id})
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "get custom host vital by id")
	}
	if len(vitals) == 0 {
		return nil, ctxerr.Wrap(ctx, common_mysql.NotFound("CustomHostVital").WithID(id))
	}
	return &vitals[0], nil
}

//////////////////////////////////////////////////////////////////////////////////
// Upsert custom host vitals (spec)
//////////////////////////////////////////////////////////////////////////////////

func upsertCustomHostVitalsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.UpsertCustomHostVitalsRequest)
	err := svc.UpsertCustomHostVitals(ctx, req.CustomHostVitals, req.DryRun)
	return automdm.UpsertCustomHostVitalsResponse{Err: err}, nil
}

func (svc *Service) UpsertCustomHostVitals(ctx context.Context, customHostVitals []automdm.CustomHostVital, dryRun bool) error {
	if err := svc.authz.Authorize(ctx, &automdm.CustomHostVital{}, automdm.ActionWrite); err != nil {
		return err
	}

	// Names are unique in the database under the utf8mb4_unicode_ci collation
	// (case-insensitive), so dedupe on that same basis rather than exact string
	// equality -- otherwise two names differing only by case would pass this
	// check and then fail as a raw DB duplicate-key error at insert time.
	seen := make(map[string]string, len(customHostVitals)) // collation key -> original name
	for _, vital := range customHostVitals {
		if err := automdm.ValidateCustomHostVitalName(vital.Name); err != nil {
			return ctxerr.Wrap(ctx, err, "validate custom host vital name")
		}
		key := norm.NFC.String(strings.ToLower(vital.Name))
		if prev, ok := seen[key]; ok {
			return ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("custom_host_vitals",
				fmt.Sprintf("duplicate custom host vital names: %q and %q must differ by more than letter case", prev, vital.Name)))
		}
		seen[key] = vital.Name
	}

	if dryRun {
		return nil
	}

	created, deleted, err := svc.ds.UpsertCustomHostVitals(ctx, customHostVitals)
	if err != nil {
		if usedErr, ok := errors.AsType[*automdm.CustomHostVitalUsedError](err); ok {
			return ctxerr.Wrap(ctx, &automdm.ConflictError{
				Message: fmt.Sprintf("Couldn't delete. %s", usedErr.Error()),
			}, "upsert custom host vitals")
		}
		return ctxerr.Wrap(ctx, err, "upsert custom host vitals")
	}

	user := authz.UserFromContext(ctx)
	for _, vital := range created {
		if err := svc.NewActivity(
			ctx,
			user,
			automdm.ActivityTypeCreatedCustomHostVital{
				CustomHostVitalID:   vital.ID,
				CustomHostVitalName: vital.Name,
			},
		); err != nil {
			return ctxerr.Wrap(ctx, err, "create activity for custom host vital creation")
		}
	}
	for _, vital := range deleted {
		if err := svc.NewActivity(
			ctx,
			user,
			automdm.ActivityTypeDeletedCustomHostVital{
				CustomHostVitalID:   vital.ID,
				CustomHostVitalName: vital.Name,
			},
		); err != nil {
			return ctxerr.Wrap(ctx, err, "create activity for custom host vital deletion")
		}
	}

	return nil
}
