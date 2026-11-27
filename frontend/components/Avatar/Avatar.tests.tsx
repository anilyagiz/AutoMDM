import { render, screen } from "@testing-library/react";
import React from "react";

import Avatar from "./Avatar";

describe("Avatar - component", () => {
  it("renders the user gravatar if provided", () => {
    render(
      <Avatar user={{ gravatar_url: "https://example.com/avatar.png" }} />
    );

    const avatar = screen.getByAltText("User avatar");
    expect(avatar).toBeVisible();
    expect(avatar).toHaveAttribute("src", "https://example.com/avatar.png");
  });

  it("renders the team avatar if useAutoMDMAvatar is `true`", () => {
    render(<Avatar useAutoMDMAvatar user={{}} />);
    expect(screen.getByTestId("automdm-avatar")).toBeVisible();
  });
});
