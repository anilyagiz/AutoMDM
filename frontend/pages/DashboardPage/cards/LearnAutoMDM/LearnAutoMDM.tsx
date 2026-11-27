import React from "react";

import CustomLink from "components/CustomLink";

const baseClass = "learn-automdm";

const LearnAutoMDM = (): JSX.Element => {
  return (
    <div className={baseClass}>
      <p>
        Want to explore AutoMDM&apos;s features? Learn how to ask questions about
        your device using reports.
      </p>
      <CustomLink
        className={`${baseClass}__action-button`}
        url="https://automdm.com/docs/using-automdm/learn-how-to-use-automdm"
        text="Learn how to use AutoMDM"
        newTab
      />
    </div>
  );
};

export default LearnAutoMDM;
