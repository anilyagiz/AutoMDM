import createMockUser from "__mocks__/userMock";

import { getDefaultTeam, preferredOrLowestIdAutoMDM } from "./useTeamIdParam";

describe("preferredOrLowestIdAutoMDM", () => {
  it('returns "Workstations" when present', () => {
    const teams = [
      { id: 1, name: "Alpha" },
      { id: 2, name: "Workstations" },
      { id: 3, name: "Zebra" },
    ];
    expect(preferredOrLowestIdAutoMDM(teams)).toEqual({
      id: 2,
      name: "Workstations",
    });
  });

  it('returns "💻 Workstations" (emoji variant) when present', () => {
    const teams = [
      { id: 1, name: "Alpha" },
      { id: 3, name: "💻 Workstations" },
    ];
    expect(preferredOrLowestIdAutoMDM(teams)).toEqual({
      id: 3,
      name: "💻 Workstations",
    });
  });

  it("prefers plain over emoji when both exist (first match wins)", () => {
    const teams = [
      { id: 5, name: "Workstations" },
      { id: 2, name: "💻 Workstations" },
    ];
    expect(preferredOrLowestIdAutoMDM(teams)).toEqual({
      id: 5,
      name: "Workstations",
    });
  });

  it("matches case-insensitively", () => {
    const teams = [
      { id: 1, name: "Alpha" },
      { id: 4, name: "WORKSTATIONS" },
    ];
    expect(preferredOrLowestIdAutoMDM(teams)).toEqual({
      id: 4,
      name: "WORKSTATIONS",
    });
  });

  it("falls back to lowest ID when no Workstations team exists", () => {
    const teams = [
      { id: 10, name: "Zebra" },
      { id: 3, name: "Charlie" },
      { id: 7, name: "Mike" },
    ];
    expect(preferredOrLowestIdAutoMDM(teams)).toEqual({
      id: 3,
      name: "Charlie",
    });
  });

  it("returns undefined for an empty array", () => {
    expect(preferredOrLowestIdAutoMDM([])).toBeUndefined();
  });
});

describe("getDefaultTeam", () => {
  it("picks the lowest-ID admin team, not the alphabetically first one", () => {
    // Sorted by name, as getUserTeams returns them.
    const teams = [
      { id: 310, name: "Alpha", role: "admin" as const },
      { id: 5, name: "Beta", role: "maintainer" as const },
      { id: 12, name: "Zeta", role: "admin" as const },
    ];
    const currentUser = createMockUser({ global_role: null, teams });
    expect(
      getDefaultTeam({
        currentUser,
        userTeams: teams,
        includeAllTeams: false,
        includeNoTeam: false,
        isPrimoMode: false,
      })?.id
    ).toBe(12);
  });
});
