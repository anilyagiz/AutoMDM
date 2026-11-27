import { APP_CONTEXT_NO_TEAM_ID, ITeamSummary } from "interfaces/team";

/**
 * Returns the trailing automdm-context phrase used in picker empty states.
 * Mirrors the convention from EmptyVulnerabilitiesTable elsewhere in the
 * codebase:
 *   - Real team (id > 0)        → " in Engineering"
 *   - Unassigned (id === 0)     → " in this team"
 *   - All teams (id === -1)    → "" (no suffix; context is global)
 *   - Undefined currentTeam     → "" (defensive)
 *
 * Used with copy like `\`No reports found${getTeamSuffix(currentTeam)}.\``.
 */
const getTeamSuffix = (currentTeam?: ITeamSummary): string => {
  if (!currentTeam) return "";
  if (currentTeam.id > 0) return ` in ${currentTeam.name}`;
  if (currentTeam.id === APP_CONTEXT_NO_TEAM_ID) return " in this team";
  return "";
};

export default getTeamSuffix;
