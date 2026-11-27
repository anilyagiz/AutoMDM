import { screen } from "@testing-library/react";
import React from "react";

import { renderWithSetup } from "test/test-utils";

import DeleteTeamModal from "./DeleteTeamModal";

describe("DeleteTeamModal", () => {
  const defaultProps = {
    name: "Workstations",
    isUpdatingAutoMDMs: false,
    onSubmit: jest.fn(),
    onCancel: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("calls onSubmit once when Delete is clicked", async () => {
    const { user } = renderWithSetup(<DeleteTeamModal {...defaultProps} />);

    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(defaultProps.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("calls onSubmit once when Enter is pressed", async () => {
    const { user } = renderWithSetup(<DeleteTeamModal {...defaultProps} />);

    await user.keyboard("{Enter}");

    expect(defaultProps.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("does not call onSubmit again on a second click while the team is being deleted", async () => {
    const { user, rerender } = renderWithSetup(
      <DeleteTeamModal {...defaultProps} />
    );

    await user.click(screen.getByRole("button", { name: "Delete" }));
    rerender(<DeleteTeamModal {...defaultProps} isUpdatingAutoMDMs />);
    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(defaultProps.onSubmit).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  });

  it("does not call onSubmit again on a second Enter while the team is being deleted", async () => {
    const { user, rerender } = renderWithSetup(
      <DeleteTeamModal {...defaultProps} />
    );

    await user.keyboard("{Enter}");
    rerender(<DeleteTeamModal {...defaultProps} isUpdatingAutoMDMs />);
    await user.keyboard("{Enter}");

    expect(defaultProps.onSubmit).toHaveBeenCalledTimes(1);
  });
});
