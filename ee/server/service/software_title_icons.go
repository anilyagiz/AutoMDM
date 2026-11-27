package service

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/anilyagiz/automdm/v4/pkg/file"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

func (svc *Service) GetSoftwareTitleIcon(ctx context.Context, teamID uint, titleID uint) ([]byte, int64, string, error) {
	var err error
	if err = svc.authz.Authorize(ctx, &automdm.SoftwareTitleIcon{TeamID: teamID}, automdm.ActionRead); err != nil {
		return nil, 0, "", err
	}

	icon, err := svc.ds.GetSoftwareTitleIcon(ctx, teamID, titleID)
	if err != nil && !automdm.IsNotFound(err) {
		return nil, 0, "", ctxerr.Wrap(ctx, err, "getting software title icon")
	}
	if icon == nil {
		vppApp, err := svc.ds.GetVPPAppMetadataByTeamAndTitleID(ctx, &teamID, titleID)
		if vppApp != nil && vppApp.IconURL != nil {
			return nil, 0, "", &automdm.VPPIconAvailable{IconURL: *vppApp.IconURL}
		}

		return nil, 0, "", ctxerr.Wrap(ctx, err, "getting software title icon")
	}

	iconData, size, err := svc.softwareTitleIconStore.Get(ctx, icon.StorageID)
	if err != nil {
		return nil, 0, "", ctxerr.Wrap(ctx, err, "getting software title icon data")
	}
	defer iconData.Close()
	imageBytes, err := io.ReadAll(iconData)
	if err != nil {
		return nil, 0, "", ctxerr.Wrap(ctx, err, "reading icon data")
	}

	return imageBytes, size, icon.Filename, nil
}

func (svc *Service) UploadSoftwareTitleIcon(ctx context.Context, payload *automdm.UploadSoftwareTitleIconPayload) (automdm.SoftwareTitleIcon, error) {
	var err error
	if err = svc.authz.Authorize(ctx, &automdm.SoftwareTitleIcon{TeamID: payload.TeamID}, automdm.ActionWrite); err != nil {
		return automdm.SoftwareTitleIcon{}, err
	}
	var softwareInstaller *automdm.SoftwareInstaller
	var vppApp *automdm.VPPAppStoreApp
	var inHouseApp *automdm.SoftwareInstaller

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return automdm.SoftwareTitleIcon{}, automdm.ErrNoContext
	}
	user := vc.User

	softwareInstaller, err = svc.ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &payload.TeamID, payload.TitleID, false)
	if err != nil && !automdm.IsNotFound(err) {
		return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "getting software installer")
	}
	if softwareInstaller == nil {
		vppApp, err = svc.ds.GetVPPAppMetadataByTeamAndTitleID(ctx, &payload.TeamID, payload.TitleID)
		if err != nil && !automdm.IsNotFound(err) {
			return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "getting VPP app")
		}
		if vppApp == nil {
			inHouseApp, err = svc.ds.GetInHouseAppMetadataByTeamAndTitleID(ctx, &payload.TeamID, payload.TitleID)
			if err != nil && !automdm.IsNotFound(err) {
				return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "getting in-house app")
			}
		}
	}
	if softwareInstaller == nil && vppApp == nil && inHouseApp == nil {
		return automdm.SoftwareTitleIcon{}, &automdm.BadRequestError{Message: fmt.Sprintf("Software title has no software installer, VPP app, or in-house app: %d", payload.TitleID)}
	}

	icon, err := svc.ds.GetSoftwareTitleIcon(ctx, payload.TeamID, payload.TitleID)
	if err != nil && !automdm.IsNotFound(err) {
		return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "getting software title icon")
	}

	if payload.IconFile != nil {
		// get sha256 of icon file
		payload.StorageID, err = file.SHA256FromTempFileReader(payload.IconFile)
		if err != nil {
			return automdm.SoftwareTitleIcon{}, err
		}
	}

	if icon == nil || icon.StorageID != payload.StorageID {
		var exists bool

		hasAccess, err := userHasAccessToStorageID(ctx, svc, payload.TeamID, payload.StorageID)
		if err != nil {
			return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "checking access to existing software title icon")
		}

		if hasAccess {
			exists, err = svc.softwareTitleIconStore.Exists(ctx, payload.StorageID)
			if err != nil {
				return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "checking if software title icon exists")
			}
		}

		if !exists {
			if payload.IconFile != nil {
				if err := svc.softwareTitleIconStore.Put(ctx, payload.StorageID, payload.IconFile); err != nil {
					return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "storing icon")
				}
			} else {
				return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, &automdm.ConflictError{
					Message: fmt.Sprintf("software title icon with hash '%s' does not exist in the icon store", payload.StorageID),
				}, "icon bytes missing for metadata-only update")
			}
		}
	}

	softwareTitleIcon, err := svc.ds.CreateOrUpdateSoftwareTitleIcon(ctx, payload)
	if err != nil {
		return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "creating or updating software title icon")
	}

	// if anything on the icon has changed, we need to generate a new activity
	if icon == nil || icon.StorageID != softwareTitleIcon.StorageID || icon.Filename != softwareTitleIcon.Filename {
		iconUrl := softwareTitleIcon.IconUrl()
		activityDetailsForSoftwareTitleIcon, err := svc.ds.ActivityDetailsForSoftwareTitleIcon(ctxdb.RequirePrimary(ctx, true), payload.TeamID, payload.TitleID)
		if err != nil {
			return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "fetching software title icon activity details")
		}
		err = generateEditActivityForSoftwareTitleIcon(ctx, svc, user, iconUrl, activityDetailsForSoftwareTitleIcon)
		if err != nil {
			return automdm.SoftwareTitleIcon{}, ctxerr.Wrap(ctx, err, "generating edit activity for software title icon")
		}
	}

	return *softwareTitleIcon, nil
}

func (svc *Service) DeleteSoftwareTitleIcon(ctx context.Context, teamID uint, titleID uint) error {
	var err error
	if err = svc.authz.Authorize(ctx, &automdm.SoftwareTitleIcon{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return automdm.ErrNoContext
	}
	user := vc.User
	activityDetailsForSoftwareTitleIcon, err := svc.ds.ActivityDetailsForSoftwareTitleIcon(ctxdb.RequirePrimary(ctx, true), teamID, titleID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "fetching software title icon activity details")
	}
	var iconUrl string
	if activityDetailsForSoftwareTitleIcon.VPPIconUrl != nil {
		iconUrl = *activityDetailsForSoftwareTitleIcon.VPPIconUrl
	}

	err = svc.ds.DeleteSoftwareTitleIcon(ctx, teamID, titleID)
	if err != nil && !automdm.IsNotFound(err) {
		return ctxerr.Wrap(ctx, err, "deleting software title icon")
	}

	// since delete is idempotent, we only want to generate an activity if the
	// software title icon was actually deleted. This error will be a not found error,
	// so if it exists, skip the activity generation
	if err == nil {
		err = generateEditActivityForSoftwareTitleIcon(ctx, svc, user, iconUrl, activityDetailsForSoftwareTitleIcon)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "generating edit activity for software title icon")
		}
	}

	return nil
}

func userHasAccessToStorageID(ctx context.Context, svc *Service, teamId uint, storageID string) (bool, error) {
	teamIds, err := svc.ds.GetTeamIdsForIconStorageId(ctx, storageID)
	if err != nil {
		return false, err
	}
	if slices.Contains(teamIds, teamId) {
		return true, nil
	}

	for _, tmID := range teamIds {
		if authErr := svc.authz.Authorize(ctx, &automdm.SoftwareTitleIcon{TeamID: tmID}, automdm.ActionWrite); authErr != nil {
			continue
		}

		return true, nil
	}

	return false, nil
}

func generateEditActivityForSoftwareTitleIcon(ctx context.Context, svc *Service, user *automdm.User, iconUrl string, activityDetailsForSoftwareTitleIcon automdm.DetailsForSoftwareIconActivity) error {
	if activityDetailsForSoftwareTitleIcon.AdamID != nil {
		// Audit-log docs promise "Default version" when the admin didn't name
		// the version. Fallback mirrors vpp.go:487 and vpp.go:1007.
		versionName := activityDetailsForSoftwareTitleIcon.VersionName
		if versionName == "" {
			versionName = automdm.DefaultAppStoreAppVersionName
		}
		vppToken, err := automdm.ActivityVPPTokenByID(ctx, svc.ds, activityDetailsForSoftwareTitleIcon.VPPTokenID)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "getting vpp token for software title icon activity")
		}
		if err := svc.NewActivity(ctx, user, automdm.ActivityEditedAppStoreApp{
			VPPToken:            vppToken,
			SoftwareTitle:       activityDetailsForSoftwareTitleIcon.SoftwareTitle,
			SoftwareDisplayName: activityDetailsForSoftwareTitleIcon.SoftwareDisplayName,
			SoftwareTitleID:     activityDetailsForSoftwareTitleIcon.SoftwareTitleID,
			AppStoreID:          *activityDetailsForSoftwareTitleIcon.AdamID,
			TeamName:            activityDetailsForSoftwareTitleIcon.TeamName,
			TeamID:              &activityDetailsForSoftwareTitleIcon.TeamID,
			Platform:            *activityDetailsForSoftwareTitleIcon.Platform,
			SelfService:         activityDetailsForSoftwareTitleIcon.SelfService,
			SoftwareIconURL:     &iconUrl,
			LabelsIncludeAny:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAny,
			LabelsExcludeAny:    activityDetailsForSoftwareTitleIcon.LabelsExcludeAny,
			LabelsIncludeAll:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAll,
			VersionName:         versionName,
		}); err != nil {
			return ctxerr.Wrap(ctx, err, "creating activity for software title icon")
		}

		return nil
	}

	if activityDetailsForSoftwareTitleIcon.SoftwareInstallerID != nil {
		if err := svc.NewActivity(ctx, user, automdm.ActivityTypeEditedSoftware{
			SoftwareTitle:       activityDetailsForSoftwareTitleIcon.SoftwareTitle,
			SoftwareDisplayName: activityDetailsForSoftwareTitleIcon.SoftwareDisplayName,
			SoftwarePackage:     activityDetailsForSoftwareTitleIcon.Filename,
			TeamName:            activityDetailsForSoftwareTitleIcon.TeamName,
			TeamID:              &activityDetailsForSoftwareTitleIcon.TeamID,
			SelfService:         activityDetailsForSoftwareTitleIcon.SelfService,
			SoftwareIconURL:     &iconUrl,
			LabelsIncludeAny:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAny,
			LabelsExcludeAny:    activityDetailsForSoftwareTitleIcon.LabelsExcludeAny,
			LabelsIncludeAll:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAll,
			SoftwareTitleID:     activityDetailsForSoftwareTitleIcon.SoftwareTitleID,
		}); err != nil {
			return ctxerr.Wrap(ctx, err, "creating activity for software title icon")
		}

		return nil
	}

	if activityDetailsForSoftwareTitleIcon.InHouseAppID != nil {
		if err := svc.NewActivity(ctx, user, automdm.ActivityTypeEditedSoftware{
			SoftwareTitle:       activityDetailsForSoftwareTitleIcon.SoftwareTitle,
			SoftwareDisplayName: activityDetailsForSoftwareTitleIcon.SoftwareDisplayName,
			SoftwarePackage:     activityDetailsForSoftwareTitleIcon.Filename,
			TeamName:            activityDetailsForSoftwareTitleIcon.TeamName,
			TeamID:              &activityDetailsForSoftwareTitleIcon.TeamID,
			SelfService:         activityDetailsForSoftwareTitleIcon.SelfService,
			SoftwareIconURL:     &iconUrl,
			LabelsIncludeAny:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAny,
			LabelsExcludeAny:    activityDetailsForSoftwareTitleIcon.LabelsExcludeAny,
			LabelsIncludeAll:    activityDetailsForSoftwareTitleIcon.LabelsIncludeAll,
			SoftwareTitleID:     activityDetailsForSoftwareTitleIcon.SoftwareTitleID,
		}); err != nil {
			return ctxerr.Wrap(ctx, err, "creating activity for software title icon")
		}

		return nil
	}

	return ctxerr.New(ctx, "no software installer, VPP app, or in-house app found for software title icon")
}
