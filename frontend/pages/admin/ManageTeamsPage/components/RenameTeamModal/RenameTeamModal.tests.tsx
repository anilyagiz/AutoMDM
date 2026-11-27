import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import React from "react";

import { createCustomRenderer } from "test/test-utils";

import RenameTeamModal from "./RenameTeamModal";

describe("RenameTeamModal", () => {
  const defaultProps = {
    onCancel: jest.fn(),
    onSubmit: jest.fn(),
    defaultName: "Existing Team Name",
    backendValidators: {},
    isUpdatingAutoMDMs: false,
  };

  const render = createCustomRenderer();

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("renders the modal with the save button enabled for the default name", () => {
    render(<RenameTeamModal {...defaultProps} />);

    const saveButton = screen.getByRole("button", { name: "Save" });
    expect(saveButton).toBeEnabled();
  });

  it("disables the save button when the name is cleared", async () => {
    render(<RenameTeamModal {...defaultProps} />);

    const nameInput = screen.getByLabelText("Team name");
    await userEvent.clear(nameInput);

    const saveButton = screen.getByRole("button", { name: "Save" });
    expect(saveButton).toBeDisabled();
  });

  it("disables the save button when only spaces are entered", async () => {
    render(<RenameTeamModal {...defaultProps} />);

    const nameInput = screen.getByLabelText("Team name");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "     ");

    const saveButton = screen.getByRole("button", { name: "Save" });
    expect(saveButton).toBeDisabled();
  });

  it("calls onSubmit with trimmed name", async () => {
    render(<RenameTeamModal {...defaultProps} />);

    const nameInput = screen.getByLabelText("Team name");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "  New Name  ");

    const saveButton = screen.getByRole("button", { name: "Save" });
    await userEvent.click(saveButton);

    expect(defaultProps.onSubmit).toHaveBeenCalledWith({ name: "New Name" });
  });

  it("caps the team name input at 255 characters (matches DB varchar(255))", () => {
    render(<RenameTeamModal {...defaultProps} />);

    const nameInput = screen.getByLabelText("Team name") as HTMLInputElement;
    expect(nameInput.maxLength).toBe(255);
  });

  it("does not call onSubmit when name is whitespace-only", async () => {
    render(<RenameTeamModal {...defaultProps} />);

    const nameInput = screen.getByLabelText("Team name");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "     ");

    const saveButton = screen.getByRole("button", { name: "Save" });
    expect(saveButton).toBeDisabled();
    expect(defaultProps.onSubmit).not.toHaveBeenCalled();
  });
});
