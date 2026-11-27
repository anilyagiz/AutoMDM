import { toTeamRoles } from "./users";

describe("toTeamRoles", () => {
  it("keeps only the id and role of each team", () => {
    const teams = [
      {
        id: 3,
        role: "admin",
        name: "AutoMDM A",
        mdm: {
          windows_settings: { configuration_profiles: [] },
        },
      },
    ];

    expect(toTeamRoles(teams)).toEqual([{ id: 3, role: "admin" }]);
  });

  it("returns undefined when there are no teams", () => {
    expect(toTeamRoles(undefined)).toBeUndefined();
  });
});
