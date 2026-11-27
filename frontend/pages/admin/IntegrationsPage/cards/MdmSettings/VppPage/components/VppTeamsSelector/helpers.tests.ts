import { createMockVppToken } from "__mocks__/mdmMock";
import { IMdmVppToken } from "interfaces/mdm";

import { teamsForApi, valueFromToken } from "./helpers";

const token = (teams: IMdmVppToken["teams"]): IMdmVppToken =>
  createMockVppToken({ teams });

describe("VppTeamsSelector helpers", () => {
  describe("valueFromToken", () => {
    it("maps an empty teams array to All teams", () => {
      expect(valueFromToken(token([]))).toEqual({
        allAutoMDMs: true,
        automdmIds: [],
      });
    });

    it("maps null teams to Custom with no teams", () => {
      expect(valueFromToken(token(null))).toEqual({
        allAutoMDMs: false,
        automdmIds: [],
      });
    });

    it("maps teams to Custom with team ids, including Unassigned", () => {
      expect(
        valueFromToken(
          token([
            { team_id: 0, name: "No team" },
            { team_id: 2, name: "Pirates" },
          ])
        )
      ).toEqual({ allAutoMDMs: false, automdmIds: [0, 2] });
    });
  });

  describe("teamsForApi", () => {
    it("returns [] for All teams, even with remembered team ids", () => {
      expect(teamsForApi({ allAutoMDMs: true, automdmIds: [1, 2] })).toEqual([]);
    });

    it("returns null for Custom with no teams", () => {
      expect(teamsForApi({ allAutoMDMs: false, automdmIds: [] })).toBeNull();
    });

    it("returns the ids for Custom with teams", () => {
      expect(teamsForApi({ allAutoMDMs: false, automdmIds: [0, 3] })).toEqual([
        0,
        3,
      ]);
    });
  });
});
