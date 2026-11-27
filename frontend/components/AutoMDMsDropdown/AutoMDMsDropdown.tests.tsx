import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { noop } from "lodash";
import React from "react";

import createMockConfig from "__mocks__/configMock";
import { APP_CONTEXT_NO_TEAM_ID } from "interfaces/team";
// TODO: Replace renderWithAppContext with createCustomRenderer (inherited
// from pre-rename TeamsDropdown.tests.tsx).
import { renderWithAppContext } from "test/test-utils";

import AutoMDMsDropdown from "./AutoMDMsDropdown";

const mockPush = jest.fn();
jest.mock("react-router", () => ({
  browserHistory: {
    push: (...args: unknown[]) => mockPush(...args),
  },
}));

// The visible trigger is a real AutoMDM <Button>; getByRole("button") finds it
// unambiguously (react-select's hidden control has no role="button").
const getTrigger = (name: RegExp) =>
  screen.getByRole("button", { name, hidden: false });

describe("AutoMDMsDropdown - component", () => {
  const USER_TEAMS = [
    { id: -1, name: "All teams" },
    { id: 1, name: "AutoMDM 1" },
    { id: 2, name: "AutoMDM 2" },
  ];

  beforeEach(() => {
    mockPush.mockClear();
  });

  it("renders the given selected team from selectedAutoMDMId", () => {
    render(
      <AutoMDMsDropdown
        currentUserTeams={USER_TEAMS}
        selectedAutoMDMId={1}
        onChange={noop}
      />
    );

    expect(getTrigger(/AutoMDM 1/)).toBeInTheDocument();
  });

  it("renders the first team option when includeAllAutoMDMs is false and when no selectedAutoMDMId is given", () => {
    render(
      <AutoMDMsDropdown
        currentUserTeams={USER_TEAMS}
        includeAllAutoMDMs={false}
        onChange={noop}
      />
    );

    expect(getTrigger(/AutoMDM 1/)).toBeInTheDocument();
  });

  it("renders 'All teams' when no selectedAutoMDMId is given", () => {
    render(<AutoMDMsDropdown currentUserTeams={USER_TEAMS} onChange={noop} />);

    expect(getTrigger(/All teams/)).toBeInTheDocument();
  });

  it("renders the first team when the current-user list has no 'All teams' row and no selectedAutoMDMId is given", () => {
    const withoutAllTeams = USER_TEAMS.filter(
      (t) => t.id > APP_CONTEXT_NO_TEAM_ID
    );
    render(
      <AutoMDMsDropdown currentUserTeams={withoutAllTeams} onChange={noop} />
    );

    expect(getTrigger(/AutoMDM 1/)).toBeInTheDocument();
  });

  it("clicking a team option updates the selected team", async () => {
    const user = userEvent.setup();

    const TestHarness = () => {
      const [selectedAutoMDMId, setSelectedTeamId] = React.useState(1);

      return (
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={selectedAutoMDMId}
          onChange={setSelectedTeamId}
        />
      );
    };

    render(<TestHarness />);

    // Starts on AutoMDM 1
    expect(getTrigger(/AutoMDM 1/)).toBeInTheDocument();

    // Open menu and click AutoMDM 2
    await user.click(getTrigger(/AutoMDM 1/));
    await user.click(screen.getByText("AutoMDM 2"));

    // Selection propagated through onChange -> selectedAutoMDMId
    expect(getTrigger(/AutoMDM 2/)).toBeInTheDocument();

    // Menu should close after selection
    expect(screen.queryByText("AutoMDM 1")).not.toBeInTheDocument();
  });

  describe("in-menu search", () => {
    // Search only appears once the list has 10+ rows.
    const MANY_TEAMS = [
      { id: -1, name: "All teams" },
      { id: 1, name: "AutoMDM 1" },
      { id: 2, name: "AutoMDM 2" },
      { id: 3, name: "AutoMDM 3" },
      { id: 4, name: "AutoMDM 4" },
      { id: 5, name: "AutoMDM 5" },
      { id: 6, name: "AutoMDM 6" },
      { id: 7, name: "AutoMDM 7" },
      { id: 8, name: "AutoMDM 8" },
      { id: 9, name: "AutoMDM 9" },
    ];

    it("hides the search input when there are fewer than 10 rows", async () => {
      const user = userEvent.setup();
      render(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(
        screen.queryByPlaceholderText("Search teams")
      ).not.toBeInTheDocument();
    });

    it("renders a search input when there are 10 or more rows", async () => {
      const user = userEvent.setup();
      render(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(screen.getByPlaceholderText("Search teams")).toBeInTheDocument();
    });

    it("filters options by the search query", async () => {
      const user = userEvent.setup();
      render(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />
      );

      await user.click(getTrigger(/AutoMDM 1/));
      fireEvent.change(screen.getByPlaceholderText("Search teams"), {
        target: { value: "AutoMDM 2" },
      });

      // The trigger button also contains "AutoMDM 1"; scope option lookups to
      // react-select's option class so the trigger doesn't count.
      const optionLabels = Array.from(
        document.querySelectorAll(".team-dropdown__option")
      ).map((o) => o.textContent);
      expect(optionLabels).toContain("AutoMDM 2");
      expect(optionLabels).not.toContain("All teams");
    });

    it("shows the empty-state message when nothing matches", async () => {
      const user = userEvent.setup();
      render(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />
      );

      await user.click(getTrigger(/AutoMDM 1/));
      fireEvent.change(screen.getByPlaceholderText("Search teams"), {
        target: { value: "nothing-matches-this" },
      });

      expect(screen.getByText("No matching teams")).toBeInTheDocument();
    });

    it("click outside the wrapper closes the menu and clears the search query", async () => {
      const user = userEvent.setup();
      render(
        <div>
          <button type="button">outside</button>
          <AutoMDMsDropdown
            currentUserTeams={MANY_TEAMS}
            selectedAutoMDMId={1}
            onChange={noop}
          />
        </div>
      );

      await user.click(getTrigger(/AutoMDM 1/));
      fireEvent.change(screen.getByPlaceholderText("Search teams"), {
        target: { value: "AutoMDM 2" },
      });
      expect(screen.getByPlaceholderText("Search teams")).toHaveValue(
        "AutoMDM 2"
      );

      // Click on an element outside the dropdown wrapper.
      fireEvent.mouseDown(screen.getByRole("button", { name: /outside/i }));

      // Menu closes.
      expect(
        screen.queryByPlaceholderText("Search teams")
      ).not.toBeInTheDocument();

      // Reopen — the search input should be empty, not stuck on "AutoMDM 2".
      await user.click(getTrigger(/AutoMDM 1/));
      expect(screen.getByPlaceholderText("Search teams")).toHaveValue("");
    });

    it("Escape on the search input closes the menu via the forwardNavKey bridge", async () => {
      const user = userEvent.setup();
      render(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(screen.getByPlaceholderText("Search teams")).toBeInTheDocument();

      // Escape hits the search input's onKeyDown, gets forwarded to
      // react-select's hidden input, which closes the menu. If the bridge
      // ever regresses, the search input stays mounted.
      fireEvent.keyDown(screen.getByPlaceholderText("Search teams"), {
        key: "Escape",
      });

      expect(
        screen.queryByPlaceholderText("Search teams")
      ).not.toBeInTheDocument();
    });
  });

  describe("Add team button", () => {
    const MANY_TEAMS = [
      { id: -1, name: "All teams" },
      { id: 1, name: "AutoMDM 1" },
      { id: 2, name: "AutoMDM 2" },
      { id: 3, name: "AutoMDM 3" },
      { id: 4, name: "AutoMDM 4" },
      { id: 5, name: "AutoMDM 5" },
      { id: 6, name: "AutoMDM 6" },
      { id: 7, name: "AutoMDM 7" },
      { id: 8, name: "AutoMDM 8" },
      { id: 9, name: "AutoMDM 9" },
    ];

    it("does not render for non-global-admin users", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />,
        { contextValue: { isGlobalAdmin: false } }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(
        screen.queryByRole("button", { name: /add team/i })
      ).not.toBeInTheDocument();
    });

    it("renders as a labeled footer for global admins when the list is short", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />,
        { contextValue: { isGlobalAdmin: true } }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      const addButton = screen.getByRole("button", { name: /add team/i });
      expect(addButton).toHaveTextContent("Add team");

      await user.click(addButton);
      expect(mockPush).toHaveBeenCalledWith("/settings/teams?create_team=1");
    });

    it("renders the same labeled footer for global admins when the list is long", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />,
        { contextValue: { isGlobalAdmin: true } }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      const addButton = screen.getByRole("button", { name: /add team/i });
      expect(addButton).toHaveTextContent("Add team");

      await user.click(addButton);
      expect(mockPush).toHaveBeenCalledWith("/settings/teams?create_team=1");
    });

    it("Enter on Add team navigates without also selecting a highlighted team", async () => {
      // Regression guard for the addAutoMDMKeyDown handler: without
      // stopPropagation on Enter/Space, the keydown would bubble to
      // SelectContainer, react-select would treat it as "select the
      // highlighted option," and onChange would fire in parallel with the
      // Add team navigation.
      //
      // In a real browser Enter on a focused <button> fires keydown then a
      // native click; jsdom doesn't dispatch that click, so we simulate both.
      const onChange = jest.fn();
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={MANY_TEAMS}
          selectedAutoMDMId={1}
          onChange={onChange}
        />,
        { contextValue: { isGlobalAdmin: true } }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      const addButton = screen.getByRole("button", { name: /add team/i });
      fireEvent.keyDown(addButton, { key: "Enter" });
      fireEvent.click(addButton);

      // Navigation fired.
      expect(mockPush).toHaveBeenCalledWith("/settings/teams?create_team=1");
      // No parallel team selection.
      expect(onChange).not.toHaveBeenCalled();
    });

    it("hides the button for global admins when GitOps mode is enabled", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />,
        {
          contextValue: {
            isGlobalAdmin: true,
            config: createMockConfig({
              gitops: {
                gitops_mode_enabled: true,
                repository_url: "https://github.com/anilyagiz/automdm",
                exceptions: { labels: false, software: false, secrets: true },
              },
            }),
          },
        }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(
        screen.queryByRole("button", { name: /add team/i })
      ).not.toBeInTheDocument();
    });

    it("hides the button for global admins when rendered as a form field", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
          asFormField
        />,
        { contextValue: { isGlobalAdmin: true } }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(
        screen.queryByRole("button", { name: /add team/i })
      ).not.toBeInTheDocument();
    });

    it("hides the button for global admins when Primo mode is enabled", async () => {
      const user = userEvent.setup();
      renderWithAppContext(
        <AutoMDMsDropdown
          currentUserTeams={USER_TEAMS}
          selectedAutoMDMId={1}
          onChange={noop}
        />,
        {
          contextValue: {
            isGlobalAdmin: true,
            config: createMockConfig({
              partnerships: { enable_primo: true },
            }),
          },
        }
      );

      await user.click(getTrigger(/AutoMDM 1/));
      expect(
        screen.queryByRole("button", { name: /add team/i })
      ).not.toBeInTheDocument();
    });
  });
});
