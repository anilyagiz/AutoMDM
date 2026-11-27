import { screen } from "@testing-library/react";
import { Command } from "cmdk";
import React from "react";

import { createCustomRenderer } from "test/test-utils";

import TeamPicker from "./TeamPicker";

// cmdk uses scrollIntoView which JSDOM doesn't implement
Element.prototype.scrollIntoView = jest.fn();

const renderInCommand = createCustomRenderer();
const renderPicker = (
  component: React.ReactElement
): ReturnType<typeof renderInCommand> =>
  renderInCommand(<Command>{component}</Command>);

// cmdk drives filtering off its own internal search state (mutated via
// Command.Input). Rendering a Command.Input here lets a test type into
// cmdk and exercise Command.Empty / row visibility the way the real
// palette does.
const renderPickerWithInput = (
  component: React.ReactElement
): ReturnType<typeof renderInCommand> =>
  renderInCommand(
    <Command>
      <Command.Input aria-label="search" />
      {component}
    </Command>
  );

describe("TeamPicker", () => {
  const availableTeams = [
    { id: -1, name: "All teams" },
    { id: 0, name: "No team" },
    { id: 1, name: "Engineering" },
    { id: 2, name: "Sales" },
  ];

  it("renders every team in availableTeams", () => {
    renderPicker(
      <TeamPicker
        availableTeams={availableTeams}
        currentTeam={availableTeams[2]}
        search=""
        onSelect={jest.fn()}
      />
    );

    expect(screen.getByText("All teams")).toBeInTheDocument();
    expect(screen.getByText("No team")).toBeInTheDocument();
    expect(screen.getByText("Engineering")).toBeInTheDocument();
    expect(screen.getByText("Sales")).toBeInTheDocument();
  });

  it("marks the current team with the selected modifier class", () => {
    renderPicker(
      <TeamPicker
        availableTeams={availableTeams}
        currentTeam={availableTeams[2]}
        search=""
        onSelect={jest.fn()}
      />
    );

    const engineering = screen.getByText("Engineering");
    expect(engineering.className).toMatch(/__item-label--selected/);

    const sales = screen.getByText("Sales");
    expect(sales.className).not.toMatch(/__item-label--selected/);
  });

  it("calls onSelect with the team id when an item is clicked", async () => {
    const onSelect = jest.fn();
    const { user } = renderPicker(
      <TeamPicker
        availableTeams={availableTeams}
        currentTeam={availableTeams[2]}
        search=""
        onSelect={onSelect}
      />
    );

    await user.click(screen.getByText("Sales"));
    expect(onSelect).toHaveBeenCalledWith(2);
  });

  it("renders empty (no items) when availableTeams is undefined", () => {
    renderPicker(<TeamPicker search="" onSelect={jest.fn()} />);
    expect(screen.queryByText("Engineering")).not.toBeInTheDocument();
  });

  it("shows the contextual empty state when search matches no team", async () => {
    const { user } = renderPickerWithInput(
      <TeamPicker
        availableTeams={availableTeams}
        currentTeam={availableTeams[2]}
        search="zzz-nope"
        onSelect={jest.fn()}
      />
    );

    // cmdk filters off its own input value. Typing here mirrors what the
    // real palette does when the user types into Command.Input.
    await user.type(screen.getByLabelText("search"), "zzz-nope");

    expect(screen.getByText('No teams match "zzz-nope".')).toBeInTheDocument();
    expect(screen.queryByText("Engineering")).not.toBeInTheDocument();
  });

  it("shows the no-search empty copy when availableTeams is empty", () => {
    renderPicker(
      <TeamPicker availableTeams={[]} search="" onSelect={jest.fn()} />
    );

    expect(screen.getByText("No teams found.")).toBeInTheDocument();
  });
});
