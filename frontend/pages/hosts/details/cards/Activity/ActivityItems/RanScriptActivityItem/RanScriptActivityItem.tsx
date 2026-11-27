import React from "react";

import ActivityItem from "components/ActivityItem";
import { formatScriptNameForActivityItem } from "utilities/helpers";

import { IHostActivityItemComponentPropsWithShowDetails } from "../../ActivityConfig";

const baseClass = "ran-script-activity-item";

const RanScriptActivityItem = ({
  tab,
  activity,
  onShowDetails,
  onCancel,
  isSoloActivity,
  hideCancel,
}: IHostActivityItemComponentPropsWithShowDetails) => {
  let ranScriptPrefix = tab === "past" ? "ran" : "told AutoMDM to run";
  if (tab !== "past" && activity.automdm_initiated) {
    ranScriptPrefix = "will run";
  }

  return (
    <ActivityItem
      className={baseClass}
      activity={activity}
      onShowDetails={onShowDetails}
      onCancel={onCancel}
      isSoloActivity={isSoloActivity}
      hideCancel={hideCancel}
    >
      <b>{activity.actor_full_name ?? "AutoMDM"}</b>
      <>
        {" "}
        {ranScriptPrefix}{" "}
        {formatScriptNameForActivityItem(activity.details?.script_name)} on this
        host
        {activity.details?.from_setup_experience
          ? " during setup experience"
          : ""}
        .
      </>
    </ActivityItem>
  );
};

export default RanScriptActivityItem;
