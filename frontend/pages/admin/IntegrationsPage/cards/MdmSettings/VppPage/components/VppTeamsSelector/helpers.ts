import { IMdmVppToken } from "interfaces/mdm";

export interface IVppTeamsValue {
  allAutoMDMs: boolean;
  automdmIds: number[];
}

export const valueFromToken = (token: IMdmVppToken): IVppTeamsValue => ({
  allAutoMDMs: token.teams?.length === 0,
  automdmIds: token.teams?.map((t) => t.team_id) ?? [],
});

/** The PATCH body's teams: [] means All teams, null means unassigned. */
export const teamsForApi = ({
  allAutoMDMs,
  automdmIds,
}: IVppTeamsValue): number[] | null => {
  if (allAutoMDMs) return [];
  return automdmIds.length ? automdmIds : null;
};
