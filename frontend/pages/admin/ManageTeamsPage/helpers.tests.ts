import {
  TEAM_NAME_CONFLICT_ERROR,
  TEAM_NAME_NO_LETTER_OR_NUMBER_ERROR,
  getTeamNameError,
} from "./helpers";

const response = (status: number, name: string, reason: string) => ({
  status,
  data: { message: "error", errors: [{ name, reason }] },
});

describe("getTeamNameError", () => {
  it("returns the conflict error for a 409, whatever the reason says", () => {
    [
      '"work-stations" is too similar to the team name "Workstations". Please try another name.',
      'A team named "Workstations" already exists. Team names must differ by more than letter case.',
      "something else entirely",
    ].forEach((reason) => {
      expect(
        getTeamNameError(response(409, "base", reason), "work-stations")
      ).toBe(TEAM_NAME_CONFLICT_ERROR);
    });
    expect(getTeamNameError({ status: 409 }, "work-stations")).toBe(
      TEAM_NAME_CONFLICT_ERROR
    );
  });

  it("returns the letter-or-number error for that 422", () => {
    expect(
      getTeamNameError(
        response(422, "name", "must include at least one letter or number"),
        "🚀"
      )
    ).toBe(TEAM_NAME_NO_LETTER_OR_NUMBER_ERROR);
  });

  it("returns the reserved-name error with the name the user typed", () => {
    expect(
      getTeamNameError(
        response(422, "name", '"unassigned" is a reserved team name'),
        "unassigned"
      )
    ).toBe('"unassigned" is a reserved team name. Please try another name.');
    expect(
      getTeamNameError(
        response(422, "name", '"All teams" is a reserved team name'),
        "All teams"
      )
    ).toBe('"All teams" is a reserved team name. Please try another name.');
  });

  it("returns the too-similar error naming the reserved name", () => {
    expect(
      getTeamNameError(
        response(
          422,
          "name",
          '"Un-assigned" is too similar to the reserved name "Unassigned"'
        ),
        "Un-assigned"
      )
    ).toBe(
      'Name is too similar to the reserved name "Unassigned". Please try another name.'
    );
  });

  it("returns undefined for other errors", () => {
    expect(
      getTeamNameError(response(422, "name", "may not be empty"), "")
    ).toBeUndefined();
    expect(
      getTeamNameError(
        response(422, "name", "may not exceed 255 characters"),
        "a"
      )
    ).toBeUndefined();
    expect(
      getTeamNameError(
        response(422, "secrets", "must include at least one letter or number"),
        "a"
      )
    ).toBeUndefined();
    expect(
      getTeamNameError(
        response(500, "name", "must include at least one letter or number"),
        "a"
      )
    ).toBeUndefined();
    expect(
      getTeamNameError(response(403, "base", "forbidden"), "a")
    ).toBeUndefined();
    expect(getTeamNameError({ status: 422, data: {} }, "a")).toBeUndefined();
  });

  it("returns undefined for a 422 whose body isn't an AutoMDM error", () => {
    [
      "<html>Unprocessable</html>",
      { errors: "must include at least one letter or number" },
      { errors: [null, "name", { name: "name" }, { name: "name", reason: 1 }] },
      null,
    ].forEach((data) => {
      expect(getTeamNameError({ status: 422, data }, "a")).toBeUndefined();
    });
    expect(getTeamNameError("Network Error", "a")).toBeUndefined();
    expect(getTeamNameError(undefined, "a")).toBeUndefined();
  });
});
