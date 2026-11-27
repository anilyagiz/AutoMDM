import React from "react";

import Button from "components/buttons/Button";
import { SUPPORT_LINK } from "utilities/constants";

// @ts-ignore
import illustration from "../../../assets/images/404.png";

const baseClass = "automdm-404";

const AutoMDM404 = () => (
  <>
    <img className={`${baseClass}__illustration`} src={illustration} alt="" />
    <div className="error-page__details">
      <h1 className={`${baseClass}__title`}>
        404: We can&apos;t find that page!
      </h1>
      <p className={`${baseClass}__description`}>
        The page you are looking for has either moved, or doesn&apos;t exist.
      </p>
      <Button
        variant="secondary"
        icon="external-link"
        iconPosition="right"
        onClick={() =>
          window.open(SUPPORT_LINK, "_blank", "noopener,noreferrer")
        }
      >
        Get help with AutoMDM
      </Button>
    </div>
  </>
);

export default AutoMDM404;
