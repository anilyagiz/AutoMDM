import { rebuildQueryStringWithTeamId } from "hooks/useTeamIdParam";
import {
  APP_CONTEXT_ALL_TEAMS_ID,
  APP_CONTEXT_NO_TEAM_ID,
} from "interfaces/team";

import {
  getHostsReportColumns,
  STRIP_TEAM_SCOPED_FILTERS_ON_ALL_TEAMS,
} from "./helpers";

describe("STRIP_TEAM_SCOPED_FILTERS_ON_ALL_TEAMS", () => {
  const rebuild = (query: string, newTeamId: number) =>
    rebuildQueryStringWithTeamId(
      query,
      newTeamId,
      2,
      STRIP_TEAM_SCOPED_FILTERS_ON_ALL_TEAMS
    );

  it.each([
    "os_settings=pending",
    "apple_settings=failing",
    "os_settings_disk_encryption=verified",
    "macos_bootstrap_package=failed",
    "software_status=failed",
  ])("drops %s when switching to All teams", (filter) => {
    expect(
      rebuild(
        `?team_id=2&${filter}&query=mac&order_key=name`,
        APP_CONTEXT_ALL_TEAMS_ID
      )
    ).toBe("?query=mac&order_key=name");
  });

  it("keeps the filter when switching to another team", () => {
    expect(rebuild("?team_id=2&os_settings=pending", 3)).toBe(
      "?team_id=3&os_settings=pending"
    );
  });

  it("keeps the filter when switching to No team", () => {
    expect(
      rebuild("?team_id=2&os_settings=pending", APP_CONTEXT_NO_TEAM_ID)
    ).toBe("?team_id=0&os_settings=pending");
  });
});

describe("getHostsReportColumns", () => {
  it("exports every host field a multi-field column shows", () => {
    expect(
      getHostsReportColumns([
        "selection",
        "display_name",
        "os_version",
        "agent",
        "hardware_model",
      ])
    ).toEqual([
      "display_name",
      "os_name",
      "os_version",
      "orbit_version",
      "osquery_version",
      "hardware_model",
      "hardware_marketing_name",
    ]);
  });
});
