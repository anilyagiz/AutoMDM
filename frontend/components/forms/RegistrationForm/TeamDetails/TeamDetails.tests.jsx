import { render, screen } from "@testing-library/react";
import React from "react";

import TeamDetails from "components/forms/RegistrationForm/TeamDetails";
import { renderWithSetup } from "test/test-utils";
import INVALID_SERVER_URL_MESSAGE from "utilities/error_messages";

describe("TeamDetails - form", () => {
  const handleSubmitSpy = jest.fn();
  it("renders", () => {
    render(<TeamDetails handleSubmit={handleSubmitSpy} />);

    expect(
      screen.getByRole("textbox", { name: "AutoMDM web address" })
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeInTheDocument();
  });

  it("validates the presence of the automdm web address field", async () => {
    const { user } = renderWithSetup(
      <TeamDetails handleSubmit={handleSubmitSpy} currentPage />
    );

    await user.click(screen.getByRole("button", { name: "Next" }));

    expect(handleSubmitSpy).not.toHaveBeenCalled();
    expect(
      screen.getByText("AutoMDM web address must be completed")
    ).toBeInTheDocument();
  });

  it("validates the AutoMDM server URL field starts with 'https://' or 'http://'", async () => {
    const { user } = renderWithSetup(
      <TeamDetails handleSubmit={handleSubmitSpy} currentPage />
    );

    const inputField = screen.getByRole("textbox", {
      name: "AutoMDM web address",
    });
    const nextButton = screen.getByRole("button", { name: "Next" });

    await user.type(inputField, "gnar.AutoMDM.co");
    await user.click(nextButton);

    expect(handleSubmitSpy).not.toHaveBeenCalled();
    expect(screen.getByText(INVALID_SERVER_URL_MESSAGE)).toBeInTheDocument();

    await user.type(inputField, "localhost:8080");
    await user.click(nextButton);

    expect(handleSubmitSpy).not.toHaveBeenCalled();
    expect(screen.getByText(INVALID_SERVER_URL_MESSAGE)).toBeInTheDocument();
  });

  it("submits the form with valid https link", async () => {
    const { user } = renderWithSetup(
      <TeamDetails handleSubmit={handleSubmitSpy} currentPage />
    );
    // when
    await user.type(
      screen.getByRole("textbox", { name: "AutoMDM web address" }),
      "https://gnar.AutoMDM.co"
    );
    await user.click(screen.getByRole("button", { name: "Next" }));
    // then
    expect(handleSubmitSpy).toHaveBeenCalledWith({
      server_url: "https://gnar.AutoMDM.co",
    });
  });
  it("submits the form with valid http link", async () => {
    const { user } = renderWithSetup(
      <TeamDetails handleSubmit={handleSubmitSpy} currentPage />
    );
    // when
    await user.type(
      screen.getByRole("textbox", { name: "AutoMDM web address" }),
      "http://localhost:8080"
    );
    await user.click(screen.getByRole("button", { name: "Next" }));
    // then
    expect(handleSubmitSpy).toHaveBeenCalledWith({
      server_url: "http://localhost:8080",
    });
  });
});
