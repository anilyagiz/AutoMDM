import { screen, waitFor } from "@testing-library/react";
import React, { useState } from "react";

import { createMockTeamSummary } from "__mocks__/teamMock";
import { createCustomRenderer } from "test/test-utils";

import { IVppTeamsValue } from "./helpers";
import VppTeamsSelector from "./VppTeamsSelector";

const HELP_TEXT = /If you uncheck a team, apps from this VPP token will be/;

const availableTeams = [
  createMockTeamSummary({ id: -1, name: "All teams" }),
  createMockTeamSummary({ id: 1, name: "Workstations" }),
  createMockTeamSummary({ id: 2, name: "Personal mobile devices" }),
  createMockTeamSummary({ id: 0, name: "Unassigned" }),
];

const render = createCustomRenderer({
  context: { app: { availableTeams } },
});

const Harness = ({
  initial,
  showUncheckHelpText,
  onChangeSpy,
}: {
  initial: IVppTeamsValue;
  showUncheckHelpText?: boolean;
  onChangeSpy?: (value: IVppTeamsValue) => void;
}) => {
  const [value, setValue] = useState(initial);
  return (
    <VppTeamsSelector
      value={value}
      onChange={(v) => {
        onChangeSpy?.(v);
        setValue(v);
      }}
      tooltipPosition="top-start"
      showUncheckHelpText={showUncheckHelpText}
    />
  );
};

describe("VppTeamsSelector", () => {
  it("shows only the radios under All teams", () => {
    render(<Harness initial={{ allAutoMDMs: true, automdmIds: [] }} />);

    expect(screen.getByRole("radio", { name: "All teams" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Custom" })).not.toBeChecked();
    expect(screen.queryByPlaceholderText("Search teams")).toBeNull();
    expect(screen.queryByRole("checkbox")).toBeNull();
  });

  it("lists every team plus Unassigned under Custom, and not the All teams entry", () => {
    render(<Harness initial={{ allAutoMDMs: false, automdmIds: [] }} />);

    expect(screen.getByRole("radio", { name: "Custom" })).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Workstations" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Personal mobile devices" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Unassigned" })
    ).toBeInTheDocument();
    expect(screen.getAllByRole("checkbox")).toHaveLength(3);
  });

  it("switching to Custom and back to All teams keeps the checks", async () => {
    const { user } = render(
      <Harness initial={{ allAutoMDMs: false, automdmIds: [1] }} />
    );

    await user.click(screen.getByRole("radio", { name: "All teams" }));
    expect(screen.queryByRole("checkbox")).toBeNull();

    await user.click(screen.getByRole("radio", { name: "Custom" }));
    expect(
      screen.getByRole("checkbox", { name: "Workstations" })
    ).toBeChecked();
  });

  it("filters the checklist by name, ignoring case", async () => {
    const { user } = render(
      <Harness initial={{ allAutoMDMs: false, automdmIds: [] }} />
    );

    await user.type(screen.getByPlaceholderText("Search teams"), "MOB");

    await waitFor(() => {
      expect(
        screen.queryByRole("checkbox", { name: "Workstations" })
      ).toBeNull();
    });
    expect(
      screen.getByRole("checkbox", { name: "Personal mobile devices" })
    ).toBeInTheDocument();
  });

  it("checking a team adds a pill and reports the new ids", async () => {
    const onChangeSpy = jest.fn();
    const { user } = render(
      <Harness
        initial={{ allAutoMDMs: false, automdmIds: [] }}
        onChangeSpy={onChangeSpy}
      />
    );

    await user.click(screen.getByRole("checkbox", { name: "Workstations" }));

    expect(onChangeSpy).toHaveBeenLastCalledWith({
      allAutoMDMs: false,
      automdmIds: [1],
    });
    expect(
      screen.getByRole("button", { name: "Remove Workstations" })
    ).toBeInTheDocument();
  });

  it("removing a pill unchecks its team", async () => {
    const { user } = render(
      <Harness initial={{ allAutoMDMs: false, automdmIds: [1, 2] }} />
    );

    await user.click(
      screen.getByRole("button", { name: "Remove Workstations" })
    );

    expect(
      screen.queryByRole("button", { name: "Remove Workstations" })
    ).toBeNull();
    expect(
      screen.getByRole("checkbox", { name: "Workstations" })
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Personal mobile devices" })
    ).toBeChecked();
  });

  it("shows the uncheck help text under Custom, not under All teams", async () => {
    const { user } = render(
      <Harness
        initial={{ allAutoMDMs: false, automdmIds: [] }}
        showUncheckHelpText
      />
    );
    expect(screen.getByText(HELP_TEXT)).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "All teams" }));
    expect(screen.queryByText(HELP_TEXT)).toBeNull();
  });

  it("hides the uncheck help text without showUncheckHelpText", () => {
    render(<Harness initial={{ allAutoMDMs: false, automdmIds: [] }} />);
    expect(screen.queryByText(HELP_TEXT)).toBeNull();
  });

  it("keeps the search text and the filter when toggling All teams and back to Custom", async () => {
    const { user } = render(
      <Harness initial={{ allAutoMDMs: false, automdmIds: [] }} />
    );

    await user.type(screen.getByPlaceholderText("Search teams"), "MOB");
    await waitFor(() => {
      expect(
        screen.queryByRole("checkbox", { name: "Workstations" })
      ).toBeNull();
    });

    await user.click(screen.getByRole("radio", { name: "All teams" }));
    await user.click(screen.getByRole("radio", { name: "Custom" }));

    expect(screen.getByPlaceholderText("Search teams")).toHaveValue("MOB");
    expect(screen.queryByRole("checkbox", { name: "Workstations" })).toBeNull();
    expect(
      screen.getByRole("checkbox", { name: "Personal mobile devices" })
    ).toBeInTheDocument();
  });

  it("shows the Teams tooltip copy on hover", async () => {
    const { user } = render(
      <Harness initial={{ allAutoMDMs: true, automdmIds: [] }} />
    );

    await user.hover(screen.getByText("Teams"));

    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip.textContent).toBe(
      "Choose Custom if you want your App Store apps from this VPP token to only be available to install on hosts in specific teams."
    );
  });
});
