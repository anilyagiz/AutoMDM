import React from "react";

import Button from "components/buttons/Button";
import { TargetType } from "components/TargetLabelSelector";

const baseClass = "profile-bottom-bar";

interface IProfileBottomBarProps {
  targetType: TargetType;
  automdmName?: string;
  /** Omit where targets can't be edited, like AutoMDM Free. */
  onEditTargets?: () => void;
  editTargetsDisabled?: boolean;
  submitButton: React.ReactNode;
}

const ProfileBottomBar = ({
  targetType,
  automdmName,
  onEditTargets,
  editTargetsDisabled = false,
  submitButton,
}: IProfileBottomBarProps) => (
  <div className={baseClass}>
    <span className={`${baseClass}__applies-to`}>
      Applies to{" "}
      <b>{targetType === "Custom" ? "hosts in custom targets" : "all hosts"}</b>
      {automdmName && (
        <>
          {" "}
          for <b>{automdmName}</b>
        </>
      )}
      .
    </span>
    <div className={`${baseClass}__actions`}>
      {onEditTargets && (
        <Button
          variant="secondary"
          icon="pencil"
          onClick={onEditTargets}
          disabled={editTargetsDisabled}
        >
          Edit targets
        </Button>
      )}
      {submitButton}
    </div>
  </div>
);

export default ProfileBottomBar;
