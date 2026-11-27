package service

import (
	"context"
	"errors"

	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/ptr"
)

// GetSSOUser is the premium implementation of svc.GetSSOUser, it allows to
// create users during the SSO flow the first time they log in if
// config.SSOSettings.EnableJITProvisioning is `true`
func (svc *Service) GetSSOUser(ctx context.Context, auth automdm.Auth) (*automdm.User, error) {
	config, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting app config")
	}

	// despite the fact that svc.NewUser will also validate the
	// email, we do it here to avoid hitting the database early if
	// the email happens to be invalid.
	if err := automdm.ValidateEmail(auth.UserID()); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "validating SSO response email")
	}

	user, err := svc.Service.GetSSOUser(ctx, auth)
	var nfe automdm.NotFoundError
	switch {
	case err == nil:
		// If the user exists, we want to update the user roles from the attributes received
		// in the SAMLResponse.

		// If JIT provisioning is disabled, then AutoMDM does not attempt to change
		// the role of the existing user.
		if config.SSOSettings == nil || !config.SSOSettings.EnableJITProvisioning {
			return user, nil
		}

		// Load custom roles from SSO attributes.
		ssoRolesInfo, coercedAttrs, ignoredAttrs, err := automdm.RolesFromSSOAttributes(auth.AssertionAttributes())
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "invalid SSO attributes")
		}
		if len(coercedAttrs) > 0 {
			svc.logger.WarnContext(ctx,
				"SAML role attribute(s) coerced to null due to empty value",
				"attributes", coercedAttrs,
			)
		}
		if len(ignoredAttrs) > 0 {
			svc.logger.WarnContext(ctx, removedSAMLAttributeMessage(ignoredAttrs), "attributes", ignoredAttrs)
		}
		if !ssoRolesInfo.IsSet() {
			// If role attributes were not set, then there's nothing to do here.
			return user, nil
		}

		newGlobalRole, newTeamsRoles, err := svc.userRolesFromSSOAttributes(ctx, ssoRolesInfo)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "user roles from SSO attributes")
		}
		oldGlobalRole := user.GlobalRole
		oldTeamsRoles := user.Teams

		// rolesChanged assumes that there cannot be multiple role entries for the same team,
		// which is ok because the "old" values comes from the database and the "new" values
		// come from automdm.RolesFromSSOAttributes which already checks for duplicates.
		if !rolesChanged(oldGlobalRole, oldTeamsRoles, newGlobalRole, newTeamsRoles) {
			// Roles haven't changed, so nothing to do.
			return user, nil
		}

		user.GlobalRole = newGlobalRole
		user.Teams = newTeamsRoles

		err = svc.ds.SaveUser(ctx, user) // TODO see if we can use TeamLite through this workflow
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "save user")
		}
		if err := automdm.LogRoleChangeActivities(ctx, svc, user, oldGlobalRole, oldTeamsRoles, user, true); err != nil {
			return nil, ctxerr.Wrap(ctx, err, "log activities for role change")
		}
		return user, nil
	case errors.As(err, &nfe):
		if config.SSOSettings == nil || !config.SSOSettings.EnableJITProvisioning {
			return nil, err
		}
	default:
		return nil, err
	}

	displayName := auth.UserDisplayName()
	if displayName == "" {
		displayName = auth.UserID()
	}

	var (
		globalRole *string
		teamRoles  []automdm.UserTeam
	)
	// Attempt to retrieve user roles from SAML custom attributes.
	ssoRolesInfo, coercedAttrs, ignoredAttrs, err := automdm.RolesFromSSOAttributes(auth.AssertionAttributes())
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "invalid SSO attributes")
	}
	if len(coercedAttrs) > 0 {
		svc.logger.WarnContext(ctx,
			"SAML role attribute(s) coerced to null due to empty value",
			"attributes", coercedAttrs,
		)
	}
	if len(ignoredAttrs) > 0 {
		svc.logger.WarnContext(ctx, removedSAMLAttributeMessage(ignoredAttrs), "attributes", ignoredAttrs)
	}
	if ssoRolesInfo.IsSet() {
		globalRole, teamRoles, err = svc.userRolesFromSSOAttributes(ctx, ssoRolesInfo)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "user roles from SSO attributes")
		}
	} else {
		// If no roles are set in the SSO attributes, default to setting user as a global observer.
		globalRole = ptr.String(automdm.RoleObserver)
	}

	user, err = svc.Service.NewUser(ctx, automdm.UserPayload{
		Name:           &displayName,
		Email:          new(auth.UserID()),
		SSOEnabled:     new(true),
		GlobalRole:     globalRole,
		Teams:          &teamRoles,
		JITProvisioned: true,
	})
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "creating new SSO user")
	}
	if err := svc.NewActivity(
		ctx,
		user,
		automdm.ActivityTypeUserAddedBySSO{},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for SSO user creation")
	}
	return user, nil
}

// rolesChanged checks whether there was any change between the old and new roles.
//
// rolesChanged assumes that there cannot be multiple role entries for the same team.
func rolesChanged(oldGlobal *string, oldTeams []automdm.UserTeam, newGlobal *string, newTeams []automdm.UserTeam) bool {
	if (newGlobal != nil && (oldGlobal == nil || *oldGlobal != *newGlobal)) || (newGlobal == nil && oldGlobal != nil) {
		return true
	}
	if len(oldTeams) != len(newTeams) {
		return true
	}
	oldTeamsMap := make(map[uint]automdm.UserTeam, len(oldTeams))
	for _, oldTeam := range oldTeams {
		oldTeamsMap[oldTeam.Team.ID] = oldTeam
	}
	for _, newTeam := range newTeams {
		oldTeam, ok := oldTeamsMap[newTeam.Team.ID]
		if !ok {
			return true
		}
		if oldTeam.Role != newTeam.Role {
			return true
		}
	}
	return false
}

// userRolesFromSSOAttributes returns `globalRole` and `teamRoles` ready to be assigned
// to an `automdm.User` struct fields `GlobalRole` and `Teams` respectively.
func (svc *Service) userRolesFromSSOAttributes(ctx context.Context, ssoRolesInfo automdm.SSORolesInfo) (globalRole *string, teamsRoles []automdm.UserTeam, err error) {
	for _, teamRole := range ssoRolesInfo.Teams {
		team, err := svc.ds.TeamWithExtras(ctx, teamRole.ID)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "invalid team")
		}
		teamsRoles = append(teamsRoles, automdm.UserTeam{
			Team: *team,
			Role: teamRole.Role,
		})
	}

	return ssoRolesInfo.Global, teamsRoles, nil
}

// removedSAMLAttributeMessage is the warning for SAML attributes that AutoMDM 5 removed and the server ignores.
func removedSAMLAttributeMessage(attrs []string) string {
	entry, _ := removednames.FindSAMLAttribute(attrs[0])
	return entry.Message()
}
