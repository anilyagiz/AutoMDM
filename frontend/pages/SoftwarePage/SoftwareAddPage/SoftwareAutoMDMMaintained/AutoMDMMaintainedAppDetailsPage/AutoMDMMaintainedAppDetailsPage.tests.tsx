import { screen, waitFor } from "@testing-library/react";
import React from "react";

import { createMockAutoMDMMaintainedAppDetails } from "__mocks__/softwareMock";
import policiesAPI from "services/entities/policies";
import softwareAPI from "services/entities/software";
import { getPatchDeadlineSettingsHandler } from "test/handlers/patch-deadline-settings-handlers";
import mockServer from "test/mock-server";
import { createCustomRenderer, createMockRouter } from "test/test-utils";

import AutoMDMMaintainedAppDetailsPage from "./AutoMDMMaintainedAppDetailsPage";

describe("AutoMDMMaintainedAppDetailsPage", () => {
  beforeEach(() => {
    jest.spyOn(softwareAPI, "getAutoMDMMaintainedApp").mockResolvedValue({
      automdm_maintained_app: createMockAutoMDMMaintainedAppDetails(),
    });
    jest
      .spyOn(softwareAPI, "addAutoMDMMaintainedApp")
      .mockResolvedValue({ software_title_id: 99 });
    jest.spyOn(policiesAPI, "create").mockResolvedValue({} as never);
  });

  afterEach(() => jest.restoreAllMocks());

  it("adds the FMA before creating its patch policy", async () => {
    const render = createCustomRenderer({
      withBackendMock: true,
      context: { app: { isPremiumTier: true } },
    });
    const router = createMockRouter();
    const { user } = render(
      <AutoMDMMaintainedAppDetailsPage
        location={
          ({ query: { team_id: "3" } } as unknown) as React.ComponentProps<
            typeof AutoMDMMaintainedAppDetailsPage
          >["location"]
        }
        router={router}
        routeParams={{ id: "1" }}
      />
    );

    await user.click(await screen.findByRole("checkbox", { name: "patch" }));
    await user.click(screen.getByRole("button", { name: "Add software" }));

    await waitFor(() => {
      expect(softwareAPI.addAutoMDMMaintainedApp).toHaveBeenCalledWith(
        3,
        expect.objectContaining({ patch: true, patchOption: "closed" })
      );
      expect(policiesAPI.create).toHaveBeenCalledWith({
        team_id: 3,
        type: "patch",
        software_title_id: 99,
        automation_type: "software",
        patch_when_closed: true,
        notify_before_patching: false,
        patch_deadline: false,
        continuous_automations_enabled: true,
      });
    });

    expect(
      (softwareAPI.addAutoMDMMaintainedApp as jest.Mock).mock
        .invocationCallOrder[0]
    ).toBeLessThan(
      (policiesAPI.create as jest.Mock).mock.invocationCallOrder[0]
    );
  });

  it("creates a deadline patch policy when Force patch is selected on macOS", async () => {
    mockServer.use(getPatchDeadlineSettingsHandler());
    const render = createCustomRenderer({
      withBackendMock: true,
      context: { app: { isPremiumTier: true } },
    });
    const { user } = render(
      <AutoMDMMaintainedAppDetailsPage
        location={
          ({ query: { team_id: "3" } } as unknown) as React.ComponentProps<
            typeof AutoMDMMaintainedAppDetailsPage
          >["location"]
        }
        router={createMockRouter()}
        routeParams={{ id: "1" }}
      />
    );

    await user.click(await screen.findByRole("checkbox", { name: "patch" }));
    await user.click(screen.getByRole("radio", { name: "Force patch" }));
    // Force patch on macOS preselects Deadline.
    expect(
      await screen.findByText(
        /Patch is done silently when app is closed or forced on/i
      )
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add software" }));

    await waitFor(() => {
      expect(policiesAPI.create).toHaveBeenCalledWith({
        team_id: 3,
        type: "patch",
        software_title_id: 99,
        automation_type: "software",
        patch_when_closed: false,
        notify_before_patching: false,
        patch_deadline: true,
        continuous_automations_enabled: true,
      });
    });
  });

  it("navigates to the added software when patch policy creation fails", async () => {
    (policiesAPI.create as jest.Mock).mockRejectedValueOnce(
      new Error("Patch failed")
    );
    const render = createCustomRenderer({
      withBackendMock: true,
      context: { app: { isPremiumTier: true } },
    });
    const router = createMockRouter();
    const { user } = render(
      <AutoMDMMaintainedAppDetailsPage
        location={
          ({ query: { team_id: "3" } } as unknown) as React.ComponentProps<
            typeof AutoMDMMaintainedAppDetailsPage
          >["location"]
        }
        router={router}
        routeParams={{ id: "1" }}
      />
    );

    await user.click(await screen.findByRole("checkbox", { name: "patch" }));
    await user.click(screen.getByRole("button", { name: "Add software" }));

    await waitFor(() => {
      expect(softwareAPI.addAutoMDMMaintainedApp).toHaveBeenCalledTimes(1);
      expect(policiesAPI.create).toHaveBeenCalledTimes(1);
      expect(router.push).toHaveBeenCalledWith(
        expect.stringMatching(/\/software\/titles\/99.*team_id=3/)
      );
    });
  });

  it("shows the server's message on the page when AutoMDM can't reach the AutoMDM-maintained app manifest", async () => {
    const reason =
      "AutoMDM couldn't fetch the manifest for Rectangle. Please try again.";
    (softwareAPI.getAutoMDMMaintainedApp as jest.Mock).mockRejectedValueOnce({
      status: 502,
      data: { message: reason, errors: [{ name: "base", reason }] },
    });
    const render = createCustomRenderer({
      withBackendMock: true,
      context: { app: { isPremiumTier: true } },
    });
    render(
      <AutoMDMMaintainedAppDetailsPage
        location={
          ({ query: { team_id: "3" } } as unknown) as React.ComponentProps<
            typeof AutoMDMMaintainedAppDetailsPage
          >["location"]
        }
        router={createMockRouter()}
        routeParams={{ id: "1" }}
      />
    );

    expect(await screen.findByText(reason)).toBeInTheDocument();
  });
});
