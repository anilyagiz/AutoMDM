import sendRequest from "services";

import policiesAPI from "./policies";

jest.mock("services", () => ({
  __esModule: true,
  default: jest.fn(),
}));

const mockSendRequest = sendRequest as jest.MockedFunction<typeof sendRequest>;

describe("policiesAPI", () => {
  beforeEach(() => {
    mockSendRequest.mockReset();
    mockSendRequest.mockResolvedValue({});
  });

  it("forwards both flags when creating a patch policy", async () => {
    await policiesAPI.create({
      team_id: 1,
      type: "patch",
      software_title_id: 10,
      automation_type: "software",
      patch_when_closed: true,
      continuous_automations_enabled: true,
    });

    expect(mockSendRequest).toHaveBeenCalledWith(
      "POST",
      "/latest/automdm/policies",
      expect.objectContaining({
        team_id: 1,
        type: "patch",
        software_title_id: 10,
        automation_type: "software",
        patch_when_closed: true,
        continuous_automations_enabled: true,
      })
    );
  });

  it("sends Unassigned as team_id 0 and leaves it out for All teams", async () => {
    await policiesAPI.create({ team_id: 0, name: "Unassigned" });
    await policiesAPI.create({ name: "All teams" });

    expect(mockSendRequest.mock.calls[0][2]).toMatchObject({ team_id: 0 });
    expect(mockSendRequest.mock.calls[1][2]).toHaveProperty(
      "team_id",
      undefined
    );
  });

  it("retains false flag values when updating a patch policy, without team_id", async () => {
    await policiesAPI.update(22, {
      team_id: 1,
      patch_when_closed: false,
      continuous_automations_enabled: false,
    });

    expect(mockSendRequest).toHaveBeenCalledWith(
      "PATCH",
      "/latest/automdm/policies/22",
      expect.objectContaining({
        patch_when_closed: false,
        continuous_automations_enabled: false,
      })
    );
    expect(mockSendRequest.mock.calls[0][2]).not.toHaveProperty("team_id");
  });

  it("sends automation_type on update, null to remove the automation", async () => {
    await policiesAPI.update(22, { automation_type: null });

    expect(mockSendRequest).toHaveBeenCalledWith(
      "PATCH",
      "/latest/automdm/policies/22",
      expect.objectContaining({ automation_type: null })
    );
  });

  it("deletes policies from any scope in one request", async () => {
    await policiesAPI.destroy([1, 2]);

    expect(mockSendRequest).toHaveBeenCalledWith(
      "POST",
      "/latest/automdm/policies/delete",
      { ids: [1, 2] }
    );
  });

  it("lists a team's policies with the combined filters", async () => {
    await policiesAPI.loadAll({
      automdmId: 0,
      mergeInherited: true,
      page: 1,
      perPage: 20,
      query: "disk",
      automationType: "webhook_or_ticket",
      platform: "darwin",
    });

    expect(mockSendRequest).toHaveBeenCalledWith(
      "GET",
      "/latest/automdm/policies?page=1&per_page=20&order_key=name&order_direction=asc&team_id=0&merge_inherited=true&query=disk&automation_type=webhook_or_ticket&platform=darwin"
    );
  });

  it("counts All teams policies without a query string when unfiltered", async () => {
    await policiesAPI.getCount({});

    expect(mockSendRequest).toHaveBeenCalledWith(
      "GET",
      "/latest/automdm/policies/count"
    );
  });

  it("counts All teams policies of a type without team_id", async () => {
    await policiesAPI.getCount({ type: "patch" });

    expect(mockSendRequest).toHaveBeenCalledWith(
      "GET",
      "/latest/automdm/policies/count?type=patch"
    );
  });
});
