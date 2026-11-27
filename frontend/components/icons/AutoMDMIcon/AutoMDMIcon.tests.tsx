import { render } from "@testing-library/react";
import React from "react";

import AutoMDMIcon from "./AutoMDMIcon";

describe("AutoMDMIcon - component", () => {
  it("renders", () => {
    const { container } = render(<AutoMDMIcon name="success-check" />);
    expect(
      container.querySelector(".automdmicon-success-check")
    ).toBeInTheDocument();
  });
});
