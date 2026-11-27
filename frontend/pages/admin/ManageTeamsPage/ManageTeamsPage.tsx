import React, {
  useState,
  useCallback,
  useContext,
  useEffect,
  useMemo,
} from "react";
import { useErrorHandler } from "react-error-boundary";
import { useQuery } from "react-query";
import { InjectedRouter } from "react-router";

import Button from "components/buttons/Button";
import CustomLink from "components/CustomLink";
import TableDataError from "components/DataError";
import EmptyState from "components/EmptyState";
import PageDescription from "components/PageDescription";
import TableContainer from "components/TableContainer";
import TableCount from "components/TableContainer/TableCount";
import { notify } from "components/ToastNotification";
import TooltipWrapper from "components/TooltipWrapper";
import { AppContext } from "context/app";
import { ITeam as IAutoMDM } from "interfaces/team";
import teamsAPI, {
  ILoadTeamsResponse,
  ITeamFormData as IAutoMDMFormData,
} from "services/entities/teams";
import usersAPI, { IGetMeResponse } from "services/entities/users";
import { LEARN_MORE_ABOUT_BASE_LINK, PRIMO_TOOLTIP } from "utilities/constants";
import { getGitOpsModeTipContent } from "utilities/helpers";

import CreateTeamModal from "./components/CreateTeamModal";
import DeleteTeamModal from "./components/DeleteTeamModal";
import RenameTeamModal from "./components/RenameTeamModal";
import { generateTableHeaders, generateDataSet } from "./TeamTableConfig";
import { getTeamNameError } from "./helpers";

const baseClass = "manage-teams";
const noTeamsClass = "no-teams";

interface IManageTeamsPageProps {
  router: InjectedRouter;
  location: {
    pathname: string;
    query: { create_team?: string };
  };
}

const ManageTeamsPage = ({
  router,
  location,
}: IManageTeamsPageProps): JSX.Element => {
  const {
    currentTeam,
    setCurrentTeam,
    setCurrentUser,
    setAvailableTeams,
    setUserSettings,
    config,
  } = useContext(AppContext);

  const [isUpdatingAutoMDMs, setIsUpdatingAutoMDMs] = useState(false);
  const [showCreateTeamModal, setShowCreateTeamModal] = useState(false);

  // Mirror the gate used by the in-page "Add team" button:
  // Primo mode and GitOps mode both disable creation.
  const isCreateTeamDisabled =
    !!config?.partnerships?.enable_primo ||
    !!(config?.gitops?.gitops_mode_enabled && config?.gitops?.repository_url);

  // Open the create modal when arriving with ?create_team=1 (e.g., from
  // the command palette). Gate on the same predicate as the in-page
  // button — the param alone must not bypass Primo/GitOps restrictions.
  // Wait for config to load before evaluating so the deep link can't
  // slip through while isCreateTeamDisabled is still a stale false.
  useEffect(() => {
    if (location.query.create_team !== "1") return;
    if (!config) return;
    if (!isCreateTeamDisabled) {
      setShowCreateTeamModal(true);
    }
    const rest = { ...location.query };
    delete rest.create_team;
    router.replace({ pathname: location.pathname, query: rest });
  }, [
    location.query,
    location.pathname,
    router,
    isCreateTeamDisabled,
    config,
  ]);
  const [showDeleteTeamModal, setShowDeleteTeamModal] = useState(false);
  const [showRenameTeamModal, setShowRenameTeamModal] = useState(false);
  const [automdmEditing, setTeamEditing] = useState<IAutoMDM>();
  const [backendValidators, setBackendValidators] = useState<{
    [key: string]: string;
  }>({});
  const handlePageError = useErrorHandler();

  const { refetch: refetchMe } = useQuery(["me"], () => usersAPI.me(), {
    enabled: false,
    onSuccess: ({ user, available_teams, settings }: IGetMeResponse) => {
      setCurrentUser(user);
      setAvailableTeams(user, available_teams);
      setUserSettings(settings);
    },
  });

  const {
    data: teams,
    isFetching: isFetchingTeams,
    error: loadingAutoMDMsError,
    refetch: refetchAutoMDMs,
  } = useQuery<ILoadTeamsResponse, Error, IAutoMDM[]>(
    ["teams"],
    () => teamsAPI.loadAll(),
    {
      select: (data: ILoadTeamsResponse) => data.teams,
      onError: (error) => handlePageError(error),
    }
  );

  // TODO: Cleanup useCallbacks, add missing dependencies, use state setter functions, e.g.,
  // `setShowCreateTeamModal((prevState) => !prevState)`, instead of including state
  // variables as dependencies for toggles, etc.

  const toggleCreateTeamModal = useCallback(() => {
    setShowCreateTeamModal(!showCreateTeamModal);
    setBackendValidators({});
  }, [showCreateTeamModal, setShowCreateTeamModal, setBackendValidators]);

  const toggleDeleteTeamModal = useCallback(
    (team?: IAutoMDM) => {
      setShowDeleteTeamModal(!showDeleteTeamModal);
      team ? setTeamEditing(team) : setTeamEditing(undefined);
    },
    [showDeleteTeamModal, setShowDeleteTeamModal, setTeamEditing]
  );

  const toggleRenameTeamModal = useCallback(
    (team?: IAutoMDM) => {
      setShowRenameTeamModal(!showRenameTeamModal);
      setBackendValidators({});
      team ? setTeamEditing(team) : setTeamEditing(undefined);
    },
    [
      showRenameTeamModal,
      setShowRenameTeamModal,
      setTeamEditing,
      setBackendValidators,
    ]
  );

  const onCreateSubmit = useCallback(
    (formData: IAutoMDMFormData) => {
      setIsUpdatingAutoMDMs(true);
      teamsAPI
        .create(formData)
        .then(() => {
          notify.success(`Successfully created ${formData.name}.`);
          setBackendValidators({});
          toggleCreateTeamModal();
          refetchMe();
          refetchAutoMDMs();
        })
        .catch((createError: unknown) => {
          const nameError = getTeamNameError(createError, formData.name);
          if (nameError) {
            setBackendValidators({ name: nameError });
          } else {
            notify.error("Could not create team. Please try again.", {
              response: createError,
            });
            toggleCreateTeamModal();
          }
        })
        .finally(() => {
          setIsUpdatingAutoMDMs(false);
        });
    },
    [toggleCreateTeamModal, refetchMe, refetchAutoMDMs]
  );

  const onDeleteSubmit = useCallback(() => {
    if (automdmEditing) {
      setIsUpdatingAutoMDMs(true);
      teamsAPI
        .destroy(automdmEditing.id)
        .then(() => {
          notify.success(`Successfully deleted ${automdmEditing.name}.`);
          if (currentTeam?.id === automdmEditing.id) {
            setCurrentTeam(undefined);
          }
        })
        .catch(() => {
          notify.error(
            `Could not delete ${automdmEditing.name}. Please try again.`
          );
        })
        .finally(() => {
          setIsUpdatingAutoMDMs(false);
          refetchMe();
          refetchAutoMDMs();
          toggleDeleteTeamModal();
        });
    }
  }, [
    currentTeam,
    automdmEditing,
    refetchMe,
    refetchAutoMDMs,
    setCurrentTeam,
    toggleDeleteTeamModal,
  ]);

  const onRenameSubmit = useCallback(
    (formData: IAutoMDMFormData) => {
      if (formData.name === automdmEditing?.name) {
        toggleRenameTeamModal();
      } else if (automdmEditing) {
        setIsUpdatingAutoMDMs(true);
        teamsAPI
          .update(formData, automdmEditing.id)
          .then(() => {
            notify.success(
              `Successfully updated team name to ${formData.name}.`
            );
            setBackendValidators({});
            toggleRenameTeamModal();
            refetchAutoMDMs();
          })
          .catch((updateError: unknown) => {
            const nameError = getTeamNameError(updateError, formData.name);
            if (nameError) {
              setBackendValidators({ name: nameError });
            } else {
              notify.error(
                `Could not rename ${automdmEditing.name}. Please try again.`,
                { response: updateError }
              );
            }
          })
          .finally(() => {
            setIsUpdatingAutoMDMs(false);
          });
      }
    },
    [automdmEditing, toggleRenameTeamModal, refetchAutoMDMs]
  );

  const onActionSelection = useCallback(
    (action: string, team: IAutoMDM): void => {
      switch (action) {
        case "rename":
          toggleRenameTeamModal(team);
          break;
        case "delete":
          toggleDeleteTeamModal(team);
          break;
        default:
      }
    },
    [toggleRenameTeamModal, toggleDeleteTeamModal]
  );

  const tableHeaders = useMemo(() => generateTableHeaders(onActionSelection), [
    onActionSelection,
  ]);
  const tableData = useMemo(() => (teams ? generateDataSet(teams) : []), [
    teams,
  ]);

  const renderTeamCount = useCallback(() => {
    return <TableCount name="teams" count={teams?.length} />;
  }, [teams]);

  const disabledPrimaryActionTooltip = (() => {
    if (!isCreateTeamDisabled) return null;
    if (config?.partnerships?.enable_primo) return PRIMO_TOOLTIP;
    if (config?.gitops?.repository_url) {
      return getGitOpsModeTipContent(config.gitops.repository_url);
    }
    return null;
  })();

  return (
    <div className={baseClass}>
      <PageDescription
        content={
          <>
            Use teams to group hosts together with their own controls, reports,
            and policies.{" "}
            <CustomLink
              text="Learn more"
              url={`${LEARN_MORE_ABOUT_BASE_LINK}/teams`}
              newTab
            />
          </>
        }
      />
      {loadingAutoMDMsError ? (
        <TableDataError />
      ) : (
        <TableContainer
          columnConfigs={tableHeaders}
          data={tableData}
          isLoading={isFetchingTeams}
          defaultSortHeader="name"
          defaultSortDirection="asc"
          actionButton={{
            name: "create team",
            buttonText: "Add team",
            variant: "default",
            onClick: toggleCreateTeamModal,
            hideButton: false,
            disabledTooltipContent: disabledPrimaryActionTooltip,
          }}
          resultsTitle="teams"
          emptyComponent={() => {
            const rawButton = (
              <Button
                disabled={!!disabledPrimaryActionTooltip}
                onClick={toggleCreateTeamModal}
                className={`${noTeamsClass}__create-button`}
              >
                Add team
              </Button>
            );
            const primaryButton = disabledPrimaryActionTooltip ? (
              <TooltipWrapper
                tipContent={disabledPrimaryActionTooltip}
                position="top"
                underline={false}
                showArrow
                tipOffset={8}
              >
                {rawButton}
              </TooltipWrapper>
            ) : (
              rawButton
            );
            return (
              <EmptyState
                header="No teams yet"
                info="Add a team to add hosts and assign users."
                primaryButton={primaryButton}
              />
            );
          }}
          showMarkAllPages={false}
          isAllPagesSelected={false}
          isClientSidePagination
          renderCount={renderTeamCount}
        />
      )}
      {showCreateTeamModal && (
        <CreateTeamModal
          onCancel={toggleCreateTeamModal}
          onSubmit={onCreateSubmit}
          backendValidators={backendValidators}
          isUpdatingAutoMDMs={isUpdatingAutoMDMs}
        />
      )}
      {showDeleteTeamModal && (
        <DeleteTeamModal
          onCancel={toggleDeleteTeamModal}
          onSubmit={onDeleteSubmit}
          name={automdmEditing?.name || ""}
          isUpdatingAutoMDMs={isUpdatingAutoMDMs}
        />
      )}
      {showRenameTeamModal && (
        <RenameTeamModal
          onCancel={toggleRenameTeamModal}
          onSubmit={onRenameSubmit}
          defaultName={automdmEditing?.name || ""}
          backendValidators={backendValidators}
          isUpdatingAutoMDMs={isUpdatingAutoMDMs}
        />
      )}
    </div>
  );
};

export default ManageTeamsPage;
