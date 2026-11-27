import { AxiosResponse } from "axios";
import { Location } from "history";
import React, { useContext, useState } from "react";
import { useErrorHandler } from "react-error-boundary";
import { useQuery, useQueryClient } from "react-query";
import { InjectedRouter } from "react-router";

import BackButton from "components/BackButton";
import Button from "components/buttons/Button";
import Card from "components/Card";
import DataError from "components/DataError";
import MainContent from "components/MainContent";
import PageDescription from "components/PageDescription";
import PremiumFeatureMessage from "components/PremiumFeatureMessage";
import SidePanelPage from "components/SidePanelPage";
import Spinner from "components/Spinner";
import { notify } from "components/ToastNotification";
import { AppContext } from "context/app";
import { getErrorReason, hasStatusKey } from "interfaces/errors";
import { Platform, PLATFORM_DISPLAY_NAMES } from "interfaces/platform";
import { getPatchPolicyFlags } from "pages/SoftwarePage/components/forms/SoftwareDeploySelector";
import SoftwareIcon from "pages/SoftwarePage/components/icons/SoftwareIcon";
import PATHS from "router/paths";
import policiesAPI from "services/entities/policies";
import softwareAPI from "services/entities/software";
import { DEFAULT_USE_QUERY_OPTIONS } from "utilities/constants";
import { getPathWithQueryParams } from "utilities/url";

import TooltipWrapper from "../../../../../components/TooltipWrapper";

import AddAutoMDMAppSoftwareModal from "./AddAutoMDMAppSoftwareModal";
import AutoMDMAppDetailsForm from "./AutoMDMAppDetailsForm";
import { IAutoMDMMaintainedAppFormData } from "./AutoMDMAppDetailsForm/AutoMDMAppDetailsForm";
import AutoMDMAppDetailsModal from "./AutoMDMAppDetailsModal";
import { getErrorMessage } from "./helpers";

const baseClass = "automdm-maintained-app-details-page";

interface IAutoMDMAppSummaryProps {
  name: string;
  platform: string;
  version: string;
  onClickShowAppDetails: (event: MouseEvent) => void;
}

const AutoMDMAppSummary = ({
  name,
  platform,
  version,
  onClickShowAppDetails,
}: IAutoMDMAppSummaryProps) => {
  let versionElement = <>{version}</>;

  if (version === "latest") {
    versionElement = (
      <TooltipWrapper
        tipContent={
          <>
            To preview the version, select <strong>Show details</strong> and
            download {name} using the URL.
          </>
        }
      >
        Latest
      </TooltipWrapper>
    );
  }

  return (
    <Card className={`${baseClass}__automdm-app-summary`}>
      <div className={`${baseClass}__automdm-app-summary--left`}>
        <SoftwareIcon name={name} size="medium" />
        <div className={`${baseClass}__automdm-app-summary--details`}>
          <div className={`${baseClass}__automdm-app-summary--title`}>{name}</div>
          <div className={`${baseClass}__automdm-app-summary--info`}>
            <div
              className={`${baseClass}__automdm-app-summary--details--platform`}
            >
              {PLATFORM_DISPLAY_NAMES[platform as Platform]}
            </div>
            &bull;
            <div
              className={`${baseClass}__automdm-app-summary--details--version`}
            >
              {versionElement}
            </div>
          </div>
        </div>
      </div>
      <div className={`${baseClass}__automdm-app-summary--show-details`}>
        <Button variant="subdued" onClick={onClickShowAppDetails} icon="info">
          Show details
        </Button>
      </div>
    </Card>
  );
};

export interface IAutoMDMMaintainedAppDetailsQueryParams {
  team_id?: string;
}

interface IAutoMDMMaintainedAppDetailsRouteParams {
  id: string;
}

interface IAutoMDMMaintainedAppDetailsPageProps {
  location: Location<IAutoMDMMaintainedAppDetailsQueryParams>;
  router: InjectedRouter;
  routeParams: IAutoMDMMaintainedAppDetailsRouteParams;
}

/** This type includes the editable form data as well as the automdm maintained
 * app id */
export type IAddAutoMDMMaintainedData = IAutoMDMMaintainedAppFormData & {
  appId: number;
};

const AutoMDMMaintainedAppDetailsPage = ({
  location,
  router,
  routeParams,
}: IAutoMDMMaintainedAppDetailsPageProps) => {
  const teamId = location.query.team_id;
  const appId = parseInt(routeParams.id, 10);
  if (isNaN(appId)) {
    router.push(PATHS.SOFTWARE_ADD_AUTOMDM_MAINTAINED);
  }

  const queryClient = useQueryClient();

  const handlePageError = useErrorHandler();
  const { isPremiumTier } = useContext(AppContext);

  const [
    showAddAutoMDMAppSoftwareModal,
    setShowAddAutoMDMAppSoftwareModal,
  ] = useState(false);
  const [showAppDetailsModal, setShowAppDetailsModal] = useState(false);

  const {
    data: automdmApp,
    isLoading: isLoadingAutoMDMApp,
    isError: isErrorAutoMDMApp,
    error: errorAutoMDMApp,
  } = useQuery(
    ["automdm-maintained-app", appId],
    () => softwareAPI.getAutoMDMMaintainedApp(appId, teamId),
    {
      ...DEFAULT_USE_QUERY_OPTIONS,
      enabled: isPremiumTier,
      retry: false,
      select: (res) => res.automdm_maintained_app,
      onError: (error) => {
        // Show the server's message on the page when AutoMDM can't reach the AutoMDM-maintained apps manifest
        if (hasStatusKey(error) && error.status === 502) {
          return;
        }
        handlePageError(error);
      },
    }
  );

  const onClickShowAppDetails = () => {
    setShowAppDetailsModal(true);
  };

  const backToAddSoftwareUrl = getPathWithQueryParams(
    PATHS.SOFTWARE_ADD_AUTOMDM_MAINTAINED,
    { team_id: teamId }
  );

  const onCancel = () => {
    router.push(backToAddSoftwareUrl);
  };

  const onSubmit = async (formData: IAutoMDMMaintainedAppFormData) => {
    // this should not happen but we need to handle the type correctly
    if (!teamId) return;

    setShowAddAutoMDMAppSoftwareModal(true);

    // Refresh the software caches and land on the new title's details. Shared
    // by the success path and the partial-success path (title added, but the
    // patch policy failed) so the two can't drift apart.
    const refreshAndGoToTitle = (titleId: number) => {
      queryClient.invalidateQueries({
        queryKey: [{ scope: "software-titles" }],
      });
      queryClient.invalidateQueries({
        queryKey: [{ scope: "software-library" }],
      });
      queryClient.invalidateQueries({
        queryKey: [{ scope: "automdm-maintained-apps" }],
      });
      router.push(
        getPathWithQueryParams(
          PATHS.SOFTWARE_TITLE_DETAILS(titleId.toString()),
          { team_id: teamId }
        )
      );
    };

    let softwareFmaTitleId: number | undefined;
    try {
      const response = await softwareAPI.addAutoMDMMaintainedApp(
        parseInt(teamId, 10),
        {
          ...formData,
          appId,
        }
      );
      const addedSoftwareTitleId = response.software_title_id;
      softwareFmaTitleId = addedSoftwareTitleId;

      if (formData.patch) {
        await policiesAPI.create({
          team_id: parseInt(teamId, 10),
          type: "patch",
          software_title_id: addedSoftwareTitleId,
          ...(formData.patchOption !== "manual" && {
            automation_type: "software",
          }),
          ...getPatchPolicyFlags(
            formData.patchOption,
            formData.endUserExperience
          ),
        });
      }

      refreshAndGoToTitle(addedSoftwareTitleId);

      notify.success(
        <>
          <b>{automdmApp?.name}</b> successfully added.
        </>
      );
    } catch (error) {
      const ae = (typeof error === "object" ? error : {}) as AxiosResponse;

      if (softwareFmaTitleId) {
        refreshAndGoToTitle(softwareFmaTitleId);
        notify.error(
          "Software was added, but the deployment settings couldn't be saved. Try again from Actions > Deploy.",
          { response: error }
        );
      } else {
        notify.error(getErrorMessage(ae), { response: error });
      }
    }

    setShowAddAutoMDMAppSoftwareModal(false);
  };

  const renderContent = () => {
    if (!isPremiumTier) {
      return <PremiumFeatureMessage />;
    }

    if (isLoadingAutoMDMApp) {
      return <Spinner />;
    }

    if (isErrorAutoMDMApp) {
      return (
        <DataError
          verticalPaddingSize="pad-xxxlarge"
          description={getErrorReason(errorAutoMDMApp) || undefined}
          excludeIssueLink
        />
      );
    }

    if (automdmApp) {
      return (
        <>
          <BackButton
            text="Back to add software"
            path={backToAddSoftwareUrl}
            className={`${baseClass}__back-to-add-software`}
          />
          <h1>{automdmApp.name}</h1>
          <PageDescription content="Add software to your library." />
          <div className={`${baseClass}__page-content`}>
            <AutoMDMAppSummary
              name={automdmApp.name}
              platform={automdmApp.platform}
              version={automdmApp.version}
              onClickShowAppDetails={onClickShowAppDetails}
            />
            <AutoMDMAppDetailsForm
              categories={automdmApp.categories}
              defaultInstallScript={automdmApp.install_script}
              defaultPostInstallScript={automdmApp.post_install_script}
              defaultUninstallScript={automdmApp.uninstall_script}
              teamId={teamId}
              onCancel={onCancel}
              onSubmit={onSubmit}
              softwareTitleId={automdmApp.software_title_id}
              platform={automdmApp.platform}
            />
          </div>
        </>
      );
    }

    return null;
  };

  return (
    <SidePanelPage>
      <MainContent className={baseClass}>
        <>{renderContent()}</>
        {showAddAutoMDMAppSoftwareModal && <AddAutoMDMAppSoftwareModal />}
        {showAppDetailsModal && automdmApp && (
          <AutoMDMAppDetailsModal
            name={automdmApp.name}
            platform={automdmApp.platform}
            version={automdmApp.version}
            slug={automdmApp.slug}
            url={automdmApp.url}
            onCancel={() => setShowAppDetailsModal(false)}
          />
        )}
      </MainContent>
    </SidePanelPage>
  );
};

export default AutoMDMMaintainedAppDetailsPage;
