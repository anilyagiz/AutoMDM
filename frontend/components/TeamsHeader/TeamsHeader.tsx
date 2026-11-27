import React from "react";

import AutoMDMsDropdown from "components/AutoMDMsDropdown";
import { ITeamSummary } from "interfaces/team";

interface ITeamsHeader {
  isOnGlobalTeam?: boolean;
  currentTeamId?: number;
  userTeams?: ITeamSummary[];
  onTeamChange: (teamId: number) => void;
}

const TeamsHeader = ({
  isOnGlobalTeam,
  currentTeamId,
  userTeams = [],
  onTeamChange,
}: ITeamsHeader) => {
  if (userTeams) {
    if (userTeams.length > 1 || isOnGlobalTeam) {
      return (
        <AutoMDMsDropdown
          currentUserTeams={userTeams}
          selectedAutoMDMId={currentTeamId}
          onChange={onTeamChange}
          includeUnassigned
        />
      );
    }
    if (userTeams.length === 1 && !isOnGlobalTeam) {
      return <h1>{userTeams[0].name}</h1>;
    }
  }
  return <></>;
};

export default TeamsHeader;
