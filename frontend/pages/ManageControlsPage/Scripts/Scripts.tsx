import React from "react";
import { InjectedRouter, Params } from "react-router/lib/Router";

import CustomLink from "components/CustomLink";
import PageDescription from "components/PageDescription";
import Spinner from "components/Spinner";
import SideNav from "pages/admin/components/SideNav";
import { AUTOMDM_WEBSITE_URL } from "utilities/constants";

import useScriptNavItems from "./ScriptsNavItems";

const baseClass = "scripts";

export interface ScriptsLocation {
  search: string;
  pathname: string;
  query: {
    team_id?: string;
    status?: string;
    page?: string;
    add_script?: string;
  };
}
interface IScriptsProps {
  params: Params;
  router: InjectedRouter;
  location: ScriptsLocation;
  // Undefined until the URL's team id resolves to an available team.
  // Gate team-scoped queries on this being defined — anything fired during
  // that window targets the wrong team.
  teamIdForApi?: number;
}

const Scripts = ({ router, location, params, teamIdForApi }: IScriptsProps) => {
  const { section } = params;

  const SCRIPTS_NAV_ITEMS = useScriptNavItems(teamIdForApi);

  const DEFAULT_SCRIPTS_SECTION = SCRIPTS_NAV_ITEMS[0];

  const currentFormSection =
    SCRIPTS_NAV_ITEMS.find((item) => item.urlSection === section) ??
    DEFAULT_SCRIPTS_SECTION;

  // Redirect to the default section if the URL section is not in the filtered list
  if (
    section &&
    currentFormSection === DEFAULT_SCRIPTS_SECTION &&
    section !== DEFAULT_SCRIPTS_SECTION.urlSection
  ) {
    router.replace(DEFAULT_SCRIPTS_SECTION.path);
    return null;
  }

  const CurrentCard = currentFormSection.Card;

  // Wait for the team id to resolve before mounting children — they fire
  // team-scoped queries eagerly.
  if (teamIdForApi === undefined) {
    return <Spinner />;
  }

  return (
    <div className={baseClass}>
      <PageDescription
        variant="tab-panel"
        content={
          <>
            Change configuration and remediate issues on macOS, Windows, and
            Linux hosts.{" "}
            <CustomLink
              text="Learn more"
              url={`${AUTOMDM_WEBSITE_URL}/docs/using-automdm/scripts`}
              newTab
            />
          </>
        }
      />
      <SideNav
        className={`${baseClass}__side-nav`}
        navItems={SCRIPTS_NAV_ITEMS}
        activeItem={currentFormSection.urlSection}
        CurrentCard={
          <CurrentCard
            key={teamIdForApi}
            teamId={teamIdForApi}
            router={router}
            location={location}
          />
        }
      />
    </div>
  );
};

export default Scripts;
