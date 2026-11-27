import { uniqueId } from "lodash";
import React from "react";
import ReactTooltip, { Place } from "react-tooltip";

import TooltipWrapper from "components/TooltipWrapper";
import { INITIAL_AUTOMDM_DATE } from "utilities/constants";
import { humanLastSeen, internationalTimeFormat } from "utilities/helpers";

interface IHumanTimeDiffWithDateTip {
  timeString: string;
  cutoffBeforeAutoMDMLaunch?: boolean;
  tooltipPosition?: Place;
  /** Content for a tooltip on the "Never" value, explaining to the caller's
   * audience why the date is missing. Ignored unless the value renders as
   * "Never". */
  neverTooltip?: React.ReactNode;
}

/** Returns "Unavailable" if date is empty string or "Unavailable"
 * Returns "Invalid date" if date is invalid
 * Returns "Never" if cutoffBeforeAutoMDMLaunch is true and date is before the
 * initial launch of AutoMDM */
export const HumanTimeDiffWithDateTip = ({
  timeString,
  cutoffBeforeAutoMDMLaunch = false,
  tooltipPosition = "top",
  neverTooltip,
}: IHumanTimeDiffWithDateTip): JSX.Element => {
  const id = uniqueId();

  if (timeString === "Unavailable" || timeString === "") {
    return <span>Unavailable</span>;
  }

  // There are cases where dates are set in AutoMDM to be the "zero date" which
  // serves as an indicator that a particular date isn't set.
  if (cutoffBeforeAutoMDMLaunch && timeString < INITIAL_AUTOMDM_DATE) {
    if (!neverTooltip) {
      return <span>Never</span>;
    }
    return (
      <TooltipWrapper
        tipContent={neverTooltip}
        position={tooltipPosition}
        showArrow
      >
        Never
      </TooltipWrapper>
    );
  }

  try {
    return (
      <>
        <span className="date-tooltip" data-tip data-for={`tooltip-${id}`}>
          {humanLastSeen(timeString)}
        </span>
        <ReactTooltip
          className="date-tooltip-text"
          place={tooltipPosition}
          type="dark"
          effect="solid"
          id={`tooltip-${id}`}
          backgroundColor="var(--tooltip-bg)"
        >
          {internationalTimeFormat(new Date(timeString))}
        </ReactTooltip>
      </>
    );
  } catch (e) {
    if (e instanceof RangeError) {
      return <span>Invalid date</span>;
    }
    return <span>Unavailable</span>;
  }
};

/** Returns a HumanTimeDiffWithDateTip configured to return "Never" in the case
 * that the timeString is before the launch date of AutoMDM */
export const HumanTimeDiffWithTeamLaunchCutoff = ({
  timeString,
  tooltipPosition = "top",
  neverTooltip,
}: IHumanTimeDiffWithDateTip): JSX.Element => {
  return (
    <HumanTimeDiffWithDateTip
      timeString={timeString}
      tooltipPosition={tooltipPosition}
      neverTooltip={neverTooltip}
      cutoffBeforeAutoMDMLaunch
    />
  );
};
