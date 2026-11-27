import React, { useContext, useMemo, useState } from "react";

import CustomLink from "components/CustomLink";
import Checkbox from "components/forms/fields/Checkbox";
import Radio from "components/forms/fields/Radio";
import SearchField from "components/forms/fields/SearchField";
import Tag from "components/Tag";
import TooltipWrapper from "components/TooltipWrapper";
import { AppContext } from "context/app";
import { APP_CONTEXT_ALL_TEAMS_ID, getTeamDisplayName } from "interfaces/team";

import { IVppTeamsValue } from "./helpers";

const baseClass = "vpp-teams-selector";

interface IVppTeamsSelectorProps {
  value: IVppTeamsValue;
  onChange: (value: IVppTeamsValue) => void;
  tooltipPosition: "top-start" | "bottom-start";
  /** Adds a "Learn more" link to the Teams tooltip. */
  tooltipLearnMoreUrl?: string;
  /** Shows the helper text about deleting apps from unchecked teams. */
  showUncheckHelpText?: boolean;
  disabled?: boolean;
}

const VppTeamsSelector = ({
  value,
  onChange,
  tooltipPosition,
  tooltipLearnMoreUrl,
  showUncheckHelpText = false,
  disabled = false,
}: IVppTeamsSelectorProps) => {
  const { availableTeams } = useContext(AppContext);
  const [searchQuery, setSearchQuery] = useState("");

  const teams = useMemo(
    () =>
      (availableTeams ?? [])
        .filter((t) => t.id !== APP_CONTEXT_ALL_TEAMS_ID)
        .map((t) => ({
          id: t.id,
          label: getTeamDisplayName({ team_id: t.id, name: t.name }),
        })),
    [availableTeams]
  );

  const filteredTeams = teams.filter((f) =>
    f.label.toLowerCase().includes(searchQuery.toLowerCase())
  );
  const selectedAutoMDMs = teams.filter((f) => value.automdmIds.includes(f.id));

  const onToggleAutoMDM = (id: number, checked: boolean) => {
    onChange({
      ...value,
      automdmIds: checked
        ? [...value.automdmIds, id]
        : value.automdmIds.filter((automdmId) => automdmId !== id),
    });
  };

  return (
    <div className={`${baseClass} form-field`}>
      {/* FormField's label tooltip isn't clickable, so the Learn more link
          inside it couldn't be reached. */}
      <span className="form-field__label" id={`${baseClass}-label`}>
        <TooltipWrapper
          position={tooltipPosition}
          clickable={!!tooltipLearnMoreUrl}
          tipContent={
            <>
              Choose <b>Custom</b> if you want your App Store apps from this VPP
              token to only be available to install on hosts in specific teams.
              {tooltipLearnMoreUrl && (
                <>
                  {" "}
                  <CustomLink
                    url={tooltipLearnMoreUrl}
                    text="Learn more"
                    newTab
                    variant="tooltip-link"
                  />
                </>
              )}
            </>
          }
        >
          Teams
        </TooltipWrapper>
      </span>
      <div
        className={`${baseClass}__radios`}
        role="radiogroup"
        aria-labelledby={`${baseClass}-label`}
      >
        <Radio
          className={`${baseClass}__radio-input`}
          label="All teams"
          id="vpp-teams-all-radio"
          name="vpp-teams-target"
          value="all"
          checked={value.allAutoMDMs}
          disabled={disabled}
          onChange={() => onChange({ ...value, allAutoMDMs: true })}
        />
        <Radio
          className={`${baseClass}__radio-input`}
          label="Custom"
          id="vpp-teams-custom-radio"
          name="vpp-teams-target"
          value="custom"
          checked={!value.allAutoMDMs}
          disabled={disabled}
          onChange={() => onChange({ ...value, allAutoMDMs: false })}
        />
      </div>
      {!value.allAutoMDMs && (
        <div className={`${baseClass}__custom`}>
          <SearchField
            placeholder="Search teams"
            defaultValue={searchQuery}
            onChange={setSearchQuery}
            disabled={disabled}
          />
          {selectedAutoMDMs.length > 0 && (
            <div className={`${baseClass}__selected-badges`}>
              {selectedAutoMDMs.map((f) => (
                <Tag
                  key={f.id}
                  type="dismissible"
                  size="small"
                  disabled={disabled}
                  dismissLabel={`Remove ${f.label}`}
                  onDismiss={() => onToggleAutoMDM(f.id, false)}
                >
                  {f.label}
                </Tag>
              ))}
            </div>
          )}
          <div className={`${baseClass}__checkboxes`}>
            {filteredTeams.map((f) => (
              <div className={`${baseClass}__team`} key={f.id}>
                <Checkbox
                  name={`vpp-automdm-${f.id}`}
                  ariaLabel={f.label}
                  value={value.automdmIds.includes(f.id)}
                  disabled={disabled}
                  onChange={({ value: checked }: { value: boolean }) =>
                    onToggleAutoMDM(f.id, checked)
                  }
                  parseTarget
                >
                  {f.label}
                </Checkbox>
              </div>
            ))}
          </div>
          {showUncheckHelpText && (
            <span className="form-field__help-text">
              If you uncheck a team, apps from this VPP token will be deleted
              from that team. Installed apps won&apos;t be uninstalled from
              hosts.
            </span>
          )}
        </div>
      )}
    </div>
  );
};

export default VppTeamsSelector;
