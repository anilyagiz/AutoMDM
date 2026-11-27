package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/ptr"
)

func (svc *Service) CreateInitialUser(ctx context.Context, p automdm.UserPayload) (*automdm.User, error) {
	// skipauth: Only the initial user creation should be allowed to skip
	// authorization (because there is not yet a user context to check against).
	svc.authz.SkipAuthorization(ctx)

	setupRequired, err := svc.SetupRequired(ctx)
	if err != nil {
		return nil, err
	}
	if !setupRequired {
		return nil, ctxerr.Wrap(ctx, &automdm.ConflictError{Message: "a user already exists"})
	}

	// Initial user should be global admin with no explicit teams
	p.GlobalRole = ptr.String(automdm.RoleAdmin)
	p.Teams = nil

	return svc.newUser(ctx, p, svc.ds.NewInitialUser)
}

func (svc *Service) NewUser(ctx context.Context, p automdm.UserPayload) (*automdm.User, error) {
	return svc.newUser(ctx, p, svc.ds.NewUser)
}

func (svc *Service) newUser(ctx context.Context, p automdm.UserPayload, create func(context.Context, *automdm.User) (*automdm.User, error)) (*automdm.User, error) {
	licChecker, _ := license.FromContext(ctx)
	lic, _ := licChecker.(*automdm.LicenseInfo)
	if lic == nil {
		return nil, ctxerr.New(ctx, "license not found")
	}
	if err := automdm.ValidateUserRoles(true, p, *lic); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "validate role")
	}
	if !lic.IsPremium() {
		p.MFAEnabled = ptr.Bool(false)
	}

	user, err := p.User(svc.config.Auth.SaltKeySize, svc.config.Auth.BcryptCost)
	if err != nil {
		return nil, err
	}

	user, err = create(ctx, user)
	if err != nil {
		return nil, err
	}

	adminUser := authz.UserFromContext(ctx)
	if adminUser == nil {
		// In case of invites the user created herself.
		adminUser = user
	}
	if err := svc.NewActivity(
		ctx,
		adminUser,
		automdm.ActivityTypeCreatedUser{
			UserID:    user.ID,
			UserName:  user.Name,
			UserEmail: user.Email,
		},
	); err != nil {
		return nil, err
	}
	if err := automdm.LogRoleChangeActivities(ctx, svc, adminUser, nil, nil, user, p.JITProvisioned); err != nil {
		return nil, err
	}

	return user, nil
}

func (svc *Service) UserUnauthorized(ctx context.Context, id uint) (*automdm.User, error) {
	// Explicitly no authorization check. Should only be used by middleware.
	return svc.ds.UserByID(ctx, id)
}
