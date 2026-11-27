import { Command } from "cmdk";
import React from "react";

import { ITeamSummary } from "interfaces/team";

import HighlightedLabel from "./HighlightedLabel";

const baseClass = "command-palette";

interface ITeamPickerProps {
  availableTeams?: ITeamSummary[];
  currentTeam?: ITeamSummary;
  search: string;
  onSelect: (automdmId: number) => void;
}

const TeamPicker = ({
  availableTeams,
  currentTeam,
  search,
  onSelect,
}: ITeamPickerProps): JSX.Element => {
  return (
    <>
      {/* cmdk's Command.Empty auto-renders when every Item is filtered out.
          Mirrors the root palette's "No results found." so the picker doesn't
          go blank on a typo. CommandPalette suppresses its own Command.Empty
          on picker pages — pickers own their empty copy. */}
      <Command.Empty className={`${baseClass}__empty`}>
        {search ? `No teams match "${search}".` : "No teams found."}
      </Command.Empty>
      <Command.Group className={`${baseClass}__group`}>
        {availableTeams?.map((team) => {
          const isSelected = team.id === currentTeam?.id;
          return (
            <Command.Item
              key={`team-${team.id}`}
              value={team.name}
              onSelect={() => onSelect(team.id)}
              className={`${baseClass}__item`}
            >
              <span
                className={`${baseClass}__item-label${
                  isSelected ? ` ${baseClass}__item-label--selected` : ""
                }`}
              >
                <HighlightedLabel text={team.name} query={search} />
              </span>
            </Command.Item>
          );
        })}
      </Command.Group>
    </>
  );
};

export default TeamPicker;
