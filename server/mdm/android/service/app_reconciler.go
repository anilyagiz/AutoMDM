package service

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	"github.com/anilyagiz/automdm/v4/server/mdm/profiles"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"google.golang.org/api/androidmanagement/v1"
)

// androidAppList is the app list built for a host whose apps or AutoMDM agent
// changed, with the installs to save once it's sent.
type androidAppList struct {
	host                         *automdm.Host
	applicationsWithoutAgent     []*androidmanagement.ApplicationPolicy
	installsToUpsert             []automdm.HostAndroidSoftwareInstall
	installsToRemove             []automdm.HostAndroidSoftwareInstall
	setupAppIDsWithChangedConfig []string
	// set when the list is sent only to retry a failed request
	isFailedRequestRetry bool
}

func (r *profileReconciler) buildAppListsToSend(
	ctx context.Context,
	profileHostUUIDs map[string]struct{},
	batchSize int,
	policyBuiltAt time.Time,
) (map[string]*androidAppList, error) {
	hosts, err := r.DS.ListAndroidHostsOnHostPolicy(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list android hosts")
	}
	hostIDs := make([]uint, 0, len(hosts))
	for _, host := range hosts {
		hostIDs = append(hostIDs, host.ID)
	}
	appsInScopeByHostID, err := r.DS.ListAndroidAppsInScopeForHosts(ctx, hostIDs)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list android apps in scope for hosts")
	}
	setupAppsByHostID, err := r.DS.ListHostMDMAndroidVPPAppsFromSetupExperience(ctx, hostIDs)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list android setup experience apps")
	}
	installsByHostID, err := r.DS.ListHostAndroidSoftwareInstalls(ctx, hostIDs)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list host android software installs")
	}

	// Read the App Store app versions from the primary, after policyBuiltAt, so an edit the replica doesn't
	// have yet won't be saved as sent
	primaryCtx := ctxdb.RequirePrimary(ctx, true)
	// Read every team's versions with their configurations, so a host moved from another team will only get
	// a new app list when the configuration differs
	versionByID, err := r.DS.GetAndroidAppStoreAppVersionsByID(primaryCtx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "get android app store app versions")
	}

	appListsByHostUUID := make(map[string]*androidAppList)
	var appList *androidAppList
	var otherHostListCount int
	for _, host := range hosts {
		_, hasProfileChanges := profileHostUUIDs[host.UUID]
		// Build app lists for the hosts with profile changes, and for other hosts until the run sends to batchSize hosts
		if !hasProfileChanges && batchSize > 0 && len(profileHostUUIDs)+otherHostListCount >= batchSize {
			continue
		}
		appList, err = r.prepareHostAppList(ctx, host, appsInScopeByHostID[host.ID], installsByHostID[host.ID], setupAppsByHostID[host.ID],
			versionByID, policyBuiltAt)
		if err != nil {
			r.Logger.ErrorContext(ctx, "failed to prepare android app list for host", "host_id", host.ID, "err", err)
			continue
		}
		if appList == nil {
			continue
		}
		appListsByHostUUID[host.UUID] = appList
		if !hasProfileChanges {
			otherHostListCount++
		}
	}
	return appListsByHostUUID, nil
}

func (r *profileReconciler) prepareHostAppList(
	ctx context.Context,
	host *automdm.Host,
	appsInList []automdm.VPPAppTeam,
	installByAppID map[string]*automdm.HostAndroidSoftwareInstall,
	setupApps []*automdm.HostAndroidVPPSoftwareInstall,
	versionByID map[uint]*automdm.VPPAppStoreApp,
	policyBuiltAt time.Time,
) (*androidAppList, error) {
	installTypeByAppID := make(map[string]string, len(appsInList)+len(setupApps))
	for _, app := range appsInList {
		installTypeByAppID[app.AdamID] = android.AppStatusAvailable
	}
	// Add pending and installed setup experience apps as PREINSTALLED, so replacing the list won't stop or
	// uninstall them. Add them even when labels leave the host out, because setup experience doesn't check labels yet.
	for _, setupApp := range setupApps {
		if _, inList := installTypeByAppID[setupApp.AdamID]; !inList {
			appsInList = append(appsInList, automdm.VPPAppTeam{AdamID: setupApp.AdamID, AppTeamID: setupApp.VPPAppTeamID})
		}
		installTypeByAppID[setupApp.AdamID] = android.AppStatusPreinstalled
	}
	// Add the AutoMDM agent as FORCE_INSTALLED, because every list includes it
	agentPackage := r.AndroidAgentConfig.Package
	if agentPackage != "" {
		installTypeByAppID[agentPackage] = android.AppStatusForceInstalled
	}

	var err error
	var needsSend bool
	var needsRemoval bool
	var needsRetry bool
	var lastFailedAt time.Time
	var installsToRemove []automdm.HostAndroidSoftwareInstall
	for appID, install := range installByAppID {
		// Track the last failed request, so the retry below can wait an hour after it
		if install.Status == automdm.AndroidSoftwareInstallStatusRequestFailed {
			needsRetry = true
			if install.UpdatedAt.After(lastFailedAt) {
				lastFailedAt = install.UpdatedAt
			}
		}
		if _, inList := installTypeByAppID[appID]; inList {
			continue
		}
		installsToRemove = append(installsToRemove, *install)
		// Send the removal in this run, unless its last request failed and the retry below will send it
		if install.Status != automdm.AndroidSoftwareInstallStatusRequestFailed {
			needsRemoval = true
		}
	}
	var installsWithNewVersion []automdm.HostAndroidSoftwareInstall
	changedAppIDs := make(map[string]struct{})
	var configChangedAppIDs []string
	// Compare each app with its last sent install, to find the changed apps and whether the host needs a new list
	for _, app := range appsInList {
		install, sent := installByAppID[app.AdamID]
		version := versionByID[app.AppTeamID]
		switch {
		case !sent:
			needsSend = true
		case install.NeedsResend, version != nil && version.ConfigurationUpdatedAt != nil && install.PolicyBuiltAt.Before(*version.ConfigurationUpdatedAt):
			needsSend = true
			configChangedAppIDs = append(configChangedAppIDs, app.AdamID)
		case !ptr.Equal(install.VPPAppTeamID, &app.AppTeamID):
			var sentVersion *automdm.VPPAppStoreApp
			if install.VPPAppTeamID != nil {
				sentVersion = versionByID[*install.VPPAppTeamID]
			}
			// Set only the install's version when the version in scope has the same configuration and install type,
			// because the app's entry in the policy doesn't change
			if sentVersion != nil && version != nil && bytes.Equal(sentVersion.Configuration, version.Configuration) &&
				(sentVersion.ConfigurationUpdatedAt == nil || sentVersion.ConfigurationUpdatedAt.Before(install.PolicyBuiltAt)) &&
				install.InstallType == installTypeByAppID[app.AdamID] {
				installWithNewVersion := *install
				installWithNewVersion.VPPAppTeamID = new(app.AppTeamID)
				installsWithNewVersion = append(installsWithNewVersion, installWithNewVersion)
			} else {
				needsSend = true
				configChangedAppIDs = append(configChangedAppIDs, app.AdamID)
			}
		case install.InstallType != installTypeByAppID[app.AdamID]:
			needsSend = true
		case install.Status == automdm.AndroidSoftwareInstallStatusRequestFailed:
			// Add the app with a failed request to the changed apps, so the retry below will set it back to pending
		case install.Status == automdm.AndroidSoftwareInstallStatusFailed:
			// Add the failed app to the changed apps, so it will be set back to pending
		default:
			continue
		}
		changedAppIDs[app.AdamID] = struct{}{}
	}
	if agentPackage != "" {
		agentInstall, agentSent := installByAppID[agentPackage]
		// Send a list to a host whose installs don't include the AutoMDM agent. It never got a list from AutoMDM,
		// so its policy can still have apps that older jobs didn't remove.
		if !agentSent {
			needsSend = true
		}
		// Send a list when the AutoMDM agent's configuration changed
		if agentSent && agentInstall.NeedsResend {
			needsSend = true
		}
	}

	var isFailedRequestRetry bool
	// Retry a failed request after 1 hour, like the App Store app configuration reconciler
	if !needsSend && needsRetry && time.Since(lastFailedAt) >= automdm.VPPAutomaticInstallWaitSeconds*time.Second {
		var retries int
		// Count retries per host, because a request sends the host's whole app list
		if r.RetryCounter != nil {
			retries, err = r.RetryCounter.CountAttempts(ctx, host.ID, 0)
			if err != nil {
				r.Logger.ErrorContext(ctx, "failed to count android app list retries", "host_id", host.ID, "err", err)
			}
		}
		// Retry at most twice until the count expires
		if retries < automdm.MaxVPPAutomaticInstallRetries {
			needsSend = true
			isFailedRequestRetry = true
		}
	}
	if !needsSend {
		r.appInstallsToUpsert = append(r.appInstallsToUpsert, installsWithNewVersion...)
		if needsRemoval {
			return &androidAppList{host: host, installsToRemove: installsToRemove}, nil
		}
		return nil, nil
	}

	substitutionHost := profiles.AndroidAppConfigSubstitutionHost{
		HostID:         host.ID,
		UUID:           host.UUID,
		HardwareSerial: host.HardwareSerial,
		Platform:       host.Platform,
	}
	configsByAppID := make(map[string][]byte, len(appsInList))
	var availableAppIDs []string
	var preinstalledAppIDs []string
	var installsToUpsert []automdm.HostAndroidSoftwareInstall
	var substituted []byte
	for _, app := range appsInList {
		version := versionByID[app.AppTeamID]
		// Check the host again on the next run, because its app version was added after the versions were read
		if version == nil {
			return nil, nil
		}
		_, changed := changedAppIDs[app.AdamID]
		install := automdm.HostAndroidSoftwareInstall{HostID: host.ID, AdamID: app.AdamID, VPPAppTeamID: new(app.AppTeamID), InstallType: installTypeByAppID[app.AdamID], Status: automdm.AndroidSoftwareInstallStatusPending, PolicyBuiltAt: policyBuiltAt}
		if len(version.Configuration) > 0 {
			substituted, err = profiles.SubstituteAutoMDMVarsAndVitalsInAndroidAppConfig(ctx, r.DS, version.Configuration, substitutionHost)
			if err != nil {
				if !profiles.IsUnresolvableAndroidAppConfigForHost(err) {
					return nil, ctxerr.Wrapf(ctx, err, "substitute automdm vars in android app config for app %s", app.AdamID)
				}
				r.Logger.WarnContext(ctx, "not sending android policy to host that can't supply a value referenced by an app config",
					"host_id", host.ID, "application_id", app.AdamID, "err", err)
				// Save the changed app as failed, so the next run will only try again once a change to that value flags it
				if changed {
					install.Status = automdm.AndroidSoftwareInstallStatusFailed
					r.appInstallsToUpsert = append(r.appInstallsToUpsert, install)
				}
				// Don't send the host's policy until it has every value its app configurations use, like for profiles
				return nil, nil
			}
			configsByAppID[app.AdamID] = substituted
		}
		// Save only the apps that changed. An unchanged app's entry in the new policy is the same as the one last sent.
		if changed {
			installsToUpsert = append(installsToUpsert, install)
		}
		if installTypeByAppID[app.AdamID] == android.AppStatusPreinstalled {
			preinstalledAppIDs = append(preinstalledAppIDs, app.AdamID)
			continue
		}
		availableAppIDs = append(availableAppIDs, app.AdamID)
	}
	appPolicies, err := profiles.BuildApplicationPoliciesWithConfig(ctx, availableAppIDs, configsByAppID, android.AppStatusAvailable)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "building application policies with config")
	}
	preinstalledAppPolicies, err := profiles.BuildApplicationPoliciesWithConfig(ctx, preinstalledAppIDs, configsByAppID, android.AppStatusPreinstalled)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "building preinstalled application policies with config")
	}

	if agentPackage != "" {
		installsToUpsert = append(installsToUpsert, automdm.HostAndroidSoftwareInstall{HostID: host.ID, AdamID: agentPackage, InstallType: android.AppStatusForceInstalled,
			Status: automdm.AndroidSoftwareInstallStatusPending, PolicyBuiltAt: policyBuiltAt})
	}
	var setupAppIDsWithChangedConfig []string
	for _, appID := range configChangedAppIDs {
		if installTypeByAppID[appID] == android.AppStatusPreinstalled {
			setupAppIDsWithChangedConfig = append(setupAppIDsWithChangedConfig, appID)
		}
	}

	// Add the removed apps back to the patch, so the removal sent after it will uninstall them
	for _, install := range installsToRemove {
		appPolicies = append(appPolicies, &androidmanagement.ApplicationPolicy{PackageName: install.AdamID, InstallType: install.InstallType})
	}

	return &androidAppList{
		host:                         host,
		applicationsWithoutAgent:     append(appPolicies, preinstalledAppPolicies...),
		installsToUpsert:             installsToUpsert,
		installsToRemove:             installsToRemove,
		setupAppIDsWithChangedConfig: setupAppIDsWithChangedConfig,
		isFailedRequestRetry:         isFailedRequestRetry,
	}, nil
}

func (r *profileReconciler) sendHostApps(ctx context.Context, appListsByHostUUID map[string]*androidAppList) error {
	appLists := slices.SortedFunc(maps.Values(appListsByHostUUID), func(a *androidAppList, b *androidAppList) int {
		return cmp.Compare(a.host.ID, b.host.ID)
	})
	for _, appList := range appLists {
		if r.appSendsStoppedByQuota {
			return nil
		}

		hostUUID := appList.host.UUID
		policyName := fmt.Sprintf("%s/policies/%s", r.Enterprise.Name(), hostUUID)
		if len(appList.installsToUpsert) == 0 {
			err := r.removeAppsAndRecordAppListRequest(ctx, appList, &android.MDMAndroidPolicyRequest{RequestName: policyName, PolicyID: hostUUID}, true)
			if err != nil {
				return err
			}
			continue
		}

		applications, err := r.buildApplicationsWithAgent(ctx, appList)
		if err != nil {
			r.Logger.ErrorContext(ctx, "failed to build android app list for host", "host_id", appList.host.ID, "err", err)
			continue
		}
		policy := &androidmanagement.Policy{Applications: applications}
		policyReq, skip, err := r.patchPolicy(ctx, hostUUID, policyName, policy, nil, androidmgmt.PoliciesPatchOpts{OnlyUpdateApps: true})
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "patch app list for host %s", hostUUID)
		}
		err = r.removeAppsAndRecordAppListRequest(ctx, appList, policyReq, skip)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *profileReconciler) buildApplicationsWithAgent(
	ctx context.Context,
	appList *androidAppList,
) ([]*androidmanagement.ApplicationPolicy, error) {
	agentSvc := r.agentService()
	agentPolicy, err := agentSvc.BuildAutoMDMAgentApplicationPolicy(ctx, appList.host.UUID)
	// Fail instead of building the list without the AutoMDM agent, because replacing the list would remove the agent
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "build automdm agent application policy")
	}
	applications := slices.Clone(appList.applicationsWithoutAgent)
	if agentPolicy != nil {
		applications = append(applications, agentPolicy)
	}
	return applications, nil
}

func (r *profileReconciler) removeAppsAndRecordAppListRequest(
	ctx context.Context,
	appList *androidAppList,
	policyRequest *android.MDMAndroidPolicyRequest,
	skip bool,
) error {
	host := appList.host

	if (skip || policyRequest.StatusCode == http.StatusOK) && len(appList.installsToRemove) > 0 {
		policyName := policyRequest.RequestName
		removedPackageNames := make([]string, 0, len(appList.installsToRemove))
		for _, install := range appList.installsToRemove {
			removedPackageNames = append(removedPackageNames, install.AdamID)
		}

		removeRequest, err := newAndroidPolicyRemoveApplicationsRequest(host.UUID, policyName, removedPackageNames)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "prepare remove policy applications request")
		}

		removeResponse, removeErr := r.Client.EnterprisesPoliciesRemovePolicyApplications(ctx, policyName, removedPackageNames)
		removeNotModified, err := recordAndroidRequestResult(ctx, r.DS, removeRequest, removeResponse, nil, removeErr)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "record remove policy applications request")
		}

		switch {
		case removeRequest.StatusCode == http.StatusTooManyRequests:
			r.appSendsStoppedByQuota = true
			appList.installsToRemove = nil
			appList.isFailedRequestRetry = false
		case !removeNotModified && removeRequest.StatusCode != http.StatusOK:
			// Set the removed apps to request failed instead of deleting them, so the retry will send their removal again
			for _, install := range appList.installsToRemove {
				install.Status = automdm.AndroidSoftwareInstallStatusRequestFailed
				appList.installsToUpsert = append(appList.installsToUpsert, install)
			}
			appList.installsToRemove = nil
		}
	}

	// Stop sending app lists on a quota error and don't save this list, so the
	// next run will send it again without waiting to retry
	if policyRequest.StatusCode == http.StatusTooManyRequests {
		r.appSendsStoppedByQuota = true
		return nil
	}

	// Count a retry only once its request is sent, because the batch size can delay the host to the next run
	if appList.isFailedRequestRetry && r.RetryCounter != nil {
		_, err := r.RetryCounter.RecordAttempt(ctx, host.ID, 0, automdm.VPPAutomaticInstallRetryTimeout)
		if err != nil {
			r.Logger.ErrorContext(ctx, "failed to record android app list retry", "host_id", host.ID, "err", err)
		}
	}

	// Save the apps of a not modified response as pending with no policy version, because the policy already has them
	if !skip && policyRequest.StatusCode != http.StatusOK {
		// Set the changed apps and the agent to request failed, so the next run will wait before sending again
		for _, install := range appList.installsToUpsert {
			if install.Status == automdm.AndroidSoftwareInstallStatusPending {
				install.Status = automdm.AndroidSoftwareInstallStatusRequestFailed
			}
			r.appInstallsToUpsert = append(r.appInstallsToUpsert, install)
		}
		// Set the removed apps to request failed instead of deleting them, because their removal wasn't sent either
		for _, install := range appList.installsToRemove {
			install.Status = automdm.AndroidSoftwareInstallStatusRequestFailed
			r.appInstallsToUpsert = append(r.appInstallsToUpsert, install)
		}
		return nil
	}

	for _, install := range appList.installsToUpsert {
		if install.Status == automdm.AndroidSoftwareInstallStatusPending && policyRequest.PolicyVersion.Valid {
			install.InPolicyVersion = &policyRequest.PolicyVersion.V
		}
		r.appInstallsToUpsert = append(r.appInstallsToUpsert, install)
	}
	r.appInstallsToRemove = append(r.appInstallsToRemove, appList.installsToRemove...)
	r.appSentHostIDs = append(r.appSentHostIDs, host.ID)

	// Set the setup experience install of an app whose configuration changed back to pending in the new policy
	// version, so the next status report will verify it with the new configuration applied
	if policyRequest.PolicyVersion.Valid {
		for _, appID := range appList.setupAppIDsWithChangedConfig {
			err := r.DS.SetAndroidAppInstallPendingApplyConfig(ctx, host.UUID, appID, policyRequest.PolicyVersion.V)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "set android setup experience install pending apply config")
			}
		}
	}
	return nil
}
