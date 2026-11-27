package authz

import (
	"context"
	"errors"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// GitOpsMDMCommandError replaces err, a failed MDM command write authorization, with an AutoMDM 5 migration message when the
// viewer has the GitOps role globally or on the team of teamID (nil for hosts in no team).
func GitOpsMDMCommandError(ctx context.Context, teamID *uint, err error) error {
	return GitOpsAutoMDMError(ctx, teamID, automdm.GitOpsMDMCommandErrorMessage, err)
}

// GitOpsHostListError is GitOpsMDMCommandError for the host list check made before an MDM command's target host is looked
// up, so GitOps users get the message without learning whether the host exists. A user who can't list hosts holds only
// GitOps roles, if any.
func GitOpsHostListError(ctx context.Context, err error) error {
	return GitOpsListError(ctx, automdm.GitOpsMDMCommandErrorMessage, err)
}

// GitOpsAutoMDMError replaces err, a failed authorization, with message when the viewer has the GitOps role globally or on
// the team of teamID (nil for hosts in no team).
func GitOpsAutoMDMError(ctx context.Context, teamID *uint, message string, err error) error {
	return gitOpsError(ctx, err, message, func(id uint) bool { return teamID != nil && id == *teamID })
}

// GitOpsListError is GitOpsAutoMDMError for the host list check made before a host is looked up, so GitOps users get the
// message without learning whether the host exists. A user who can't list hosts holds only GitOps roles, if any.
func GitOpsListError(ctx context.Context, message string, err error) error {
	return gitOpsError(ctx, err, message, func(uint) bool { return true })
}

func gitOpsError(ctx context.Context, err error, message string, matchesTeam func(teamID uint) bool) error {
	user := UserFromContext(ctx)
	if user == nil {
		return err
	}
	isGitOps := user.GlobalRole != nil && *user.GlobalRole == automdm.RoleGitOps
	for _, team := range user.Teams {
		if team.Role == automdm.RoleGitOps && matchesTeam(team.ID) {
			isGitOps = true
		}
	}
	if !isGitOps {
		return err
	}
	forbidden := &Forbidden{message: message, internal: err.Error(), subject: user}
	// Keep the denial's details for the log.
	if authzErr, ok := errors.AsType[*Forbidden](err); ok {
		forbidden.internal, forbidden.object, forbidden.action = authzErr.internal, authzErr.object, authzErr.action
	}
	return forbidden
}
