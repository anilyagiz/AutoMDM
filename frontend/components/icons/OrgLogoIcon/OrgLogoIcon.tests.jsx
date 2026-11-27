import { render, screen } from "@testing-library/react";
import React from "react";

import automdmAvatar from "../../../assets/images/automdm-avatar-24x24@2x.png";

import OrgLogoIcon from "./OrgLogoIcon";

describe("OrgLogoIcon - component", () => {
  it("renders the AutoMDM Logo by default", () => {
    render(<OrgLogoIcon />);

    // expect(component.state("imageSrc")).toEqual(automdmAvatar);
    expect(screen.getByRole("img")).toHaveAttribute("src", automdmAvatar);
  });

  it("renders the image source when it is valid", () => {
    render(<OrgLogoIcon src="/assets/images/avatar.svg" />);

    expect(screen.getByRole("img")).toHaveAttribute(
      "src",
      "/assets/images/avatar.svg"
    );
  });
});
