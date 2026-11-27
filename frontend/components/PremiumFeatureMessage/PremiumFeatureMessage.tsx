import classnames from "classnames";
import React from "react";

import CustomLink from "components/CustomLink";
import Icon from "components/Icon";

interface IPremiumFeatureMessage {
  className?: string;
}

const baseClass = "premium-feature-message-container";

const PremiumFeatureMessage = ({ className }: IPremiumFeatureMessage) => {
  const classes = classnames(baseClass, className);

  return (
    <div className={classes}>
      <div className="premium-feature-message">
        <Icon name="premium-feature" />
        <p>This feature is included in AutoMDM Premium.</p>
        <CustomLink
          url="https://automdm.com/upgrade"
          text="Learn more"
          newTab
        />
      </div>
    </div>
  );
};

export default PremiumFeatureMessage;
