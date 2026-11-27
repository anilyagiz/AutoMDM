import { Meta, StoryObj } from "@storybook/react";
import { noop } from "lodash";
import React from "react";

import { AppContext, initialState } from "context/app";

import AutoMDMsDropdown from ".";

// Team names lifted from the Figma design so stories match reviewer visuals.
const TEAMS_FEW = [
  { id: -1, name: "All teams" },
  { id: 0, name: "Unassigned" },
  { id: 1, name: "Servers" },
  { id: 2, name: "Servers (canary)" },
  { id: 3, name: "Workstations" },
];

const TEAMS_MANY = [
  { id: -1, name: "All teams" },
  { id: 0, name: "Unassigned" },
  { id: 1, name: "Servers" },
  { id: 2, name: "Servers (canary)" },
  { id: 3, name: "Workstations" },
  { id: 4, name: "Testing & QA" },
  { id: 5, name: "Employee-issued mobile devices" },
  { id: 6, name: "Personal mobile devices" },
  { id: 7, name: "IT servers" },
  { id: 8, name: "TV media centers" },
  { id: 9, name: "Smart fridges" },
];

const TEAMS_SCROLLABLE = [
  ...TEAMS_MANY,
  { id: 10, name: "Company-owned wearables" },
  { id: 11, name: "CEO exception devices" },
  { id: 12, name: "Company-owned mobile devices" },
  { id: 13, name: "Contractor-owned laptops" },
  { id: 14, name: "Regional office desktops" },
  { id: 15, name: "Kiosk terminals" },
  { id: 16, name: "Retail POS systems" },
];

const withAppContext = (isGlobalAdmin: boolean) => (
  Story: React.ComponentType
) => (
  <AppContext.Provider value={{ ...initialState, isGlobalAdmin }}>
    {/* minHeight matches the menu's runtime maxHeight (715px) plus room for
        the trigger, so scrollable-list stories render the full open menu
        without clipping. */}
    <div style={{ minHeight: 780 }}>
      <Story />
    </div>
  </AppContext.Provider>
);

const meta: Meta<typeof AutoMDMsDropdown> = {
  title: "Components/AutoMDMsDropdown",
  component: AutoMDMsDropdown,
  args: {
    currentUserTeams: TEAMS_MANY,
    includeUnassigned: true,
    onChange: noop,
  },
};

export default meta;

type Story = StoryObj<typeof AutoMDMsDropdown>;

// ---------------------------------------------------------------------------
// Below the search threshold (<10 rows)
// ---------------------------------------------------------------------------

export const FewTeamsAsAdmin: Story = {
  name: "Few teams — global admin (no search, footer only)",
  args: { currentUserTeams: TEAMS_FEW },
  decorators: [withAppContext(true)],
};

export const FewTeamsAsNonAdmin: Story = {
  name: "Few teams — non-admin (no search, no footer)",
  args: { currentUserTeams: TEAMS_FEW },
  decorators: [withAppContext(false)],
};

// ---------------------------------------------------------------------------
// At the search threshold, still fits without scroll (10–14 rows)
// ---------------------------------------------------------------------------

export const ManyTeamsAsAdmin: Story = {
  name: "Many teams — global admin (search + footer, no scroll)",
  args: { currentUserTeams: TEAMS_MANY },
  decorators: [withAppContext(true)],
};

export const ManyTeamsAsNonAdmin: Story = {
  name: "Many teams — non-admin (search only, no scroll)",
  args: { currentUserTeams: TEAMS_MANY },
  decorators: [withAppContext(false)],
};

// ---------------------------------------------------------------------------
// Beyond the scroll threshold (15+ rows) — scroll-fade appears
// ---------------------------------------------------------------------------

export const ScrollableAsAdmin: Story = {
  name: "Scrollable list — global admin (search + fade + footer)",
  args: { currentUserTeams: TEAMS_SCROLLABLE },
  decorators: [withAppContext(true)],
};

export const ScrollableAsNonAdmin: Story = {
  name: "Scrollable list — non-admin (search + fade only)",
  args: { currentUserTeams: TEAMS_SCROLLABLE },
  decorators: [withAppContext(false)],
};

// ---------------------------------------------------------------------------
// Variants
// ---------------------------------------------------------------------------

export const AsFormField: Story = {
  name: "As form field (Save as new report modal)",
  args: {
    currentUserTeams: TEAMS_MANY,
    asFormField: true,
    includeAllAutoMDMs: false,
    selectedAutoMDMId: 1,
  },
  decorators: [withAppContext(true)],
};

export const Disabled: Story = {
  args: {
    currentUserTeams: TEAMS_MANY,
    isDisabled: true,
  },
  decorators: [withAppContext(true)],
};

export const LongTeamName: Story = {
  name: "Long team name (trigger + option truncation)",
  args: {
    currentUserTeams: [
      { id: -1, name: "All teams" },
      {
        id: 1,
        name:
          "Employee-issued mobile devices in the west-coast satellite offices",
      },
      { id: 2, name: "Workstations" },
      { id: 3, name: "Servers" },
    ],
    selectedAutoMDMId: 1,
  },
  decorators: [withAppContext(true)],
};
