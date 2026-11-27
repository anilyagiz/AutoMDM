import sendRequest from "services";

import mdmAndroidAPI from "./mdm_android";

jest.mock("services", () => ({
  __esModule: true,
  default: jest.fn(),
}));

const mockSendRequest = sendRequest as jest.MockedFunction<typeof sendRequest>;

const ZERO_TOUCH_PATH =
  "/latest/automdm/android_enterprise/zero_touch_configuration";

describe("mdmAndroidAPI.getZeroTouchConfiguration", () => {
  beforeEach(() => {
    mockSendRequest.mockReset();
    mockSendRequest.mockResolvedValue({});
  });

  it.each([
    ["no team", undefined, ZERO_TOUCH_PATH],
    ["Unassigned (0)", 0, ZERO_TOUCH_PATH],
    ["a team", 7, `${ZERO_TOUCH_PATH}?team_id=7`],
  ])("requests %s", async (_, automdmId, expectedPath) => {
    await mdmAndroidAPI.getZeroTouchConfiguration(automdmId);
    expect(mockSendRequest).toHaveBeenCalledWith("GET", expectedPath);
  });
});
