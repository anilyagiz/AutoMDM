import { screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import React from "react";

import createMockConfig from "__mocks__/configMock";
import createMockTeam from "__mocks__/teamMock";
import createMockUser from "__mocks__/userMock";
import { notify } from "components/ToastNotification";
import mockServer from "test/mock-server";
import {
  createCustomRenderer,
  baseUrl,
  createMockRouter,
} from "test/test-utils";

import ManageTeamsPage from "./ManageTeamsPage";

jest.mock("components/ToastNotification", () => ({
  notify: {
    success: jest.fn(),
    error: jest.fn(),
    batch: jest.fn(),
    dismiss: jest.fn(),
  },
}));

const CONFLICT_ERROR =
  "Name is too similar to another team name. Please try another name.";
const NO_LETTER_OR_NUMBER_ERROR =
  "Team name must include at least one letter or number.";

const conflictResponse = () =>
  HttpResponse.json(
    {
      message: "Conflict",
      errors: [
        {
          name: "base",
          reason:
            '"work-stations" is too similar to the team name "Workstations". Please try another name.',
        },
      ],
    },
    { status: 409 }
  );

const invalidNameResponse = (reason: string) =>
  HttpResponse.json(
    { message: "Validation Failed", errors: [{ name: "name", reason }] },
    { status: 422 }
  );

const serverErrorResponse = () =>
  HttpResponse.json(
    { message: "error", errors: [{ name: "base", reason: "boom" }] },
    { status: 500 }
  );

const renderPage = () => {
  mockServer.use(
    http.get(baseUrl("/me"), () =>
      HttpResponse.json({ user: createMockUser({ global_role: "admin" }) })
    ),
    http.get(baseUrl("/teams"), () =>
      HttpResponse.json({
        teams: [createMockTeam({ id: 1, name: "Workstations" })],
      })
    )
  );
  const render = createCustomRenderer({
    withBackendMock: true,
    context: {
      app: {
        isGlobalAdmin: true,
        isPremiumTier: true,
        currentUser: createMockUser({ global_role: "admin" }),
        config: createMockConfig(),
        setAvailableTeams: jest.fn(),
        setCurrentTeam: jest.fn(),
        setCurrentUser: jest.fn(),
        setUserSettings: jest.fn(),
      },
    },
  });
  return render(
    <ManageTeamsPage
      router={createMockRouter()}
      location={{ pathname: "/settings/teams", query: {} }}
    />
  );
};

describe("ManageTeamsPage", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe("creating a team", () => {
    const submitCreate = async (name: string) => {
      const { user } = renderPage();
      await user.click(
        await screen.findByRole("button", { name: "Add team" })
      );
      await user.type(screen.getByLabelText("Team name"), name);
      await user.click(screen.getByRole("button", { name: "Create" }));
    };

    it("shows the conflict error under the name field and keeps the modal open on a 409", async () => {
      mockServer.use(http.post(baseUrl("/teams"), conflictResponse));

      await submitCreate("work-stations");

      expect(await screen.findByText(CONFLICT_ERROR)).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Create" })
      ).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("shows the letter-or-number error under the name field", async () => {
      mockServer.use(
        http.post(baseUrl("/teams"), () =>
          invalidNameResponse("must include at least one letter or number")
        )
      );

      await submitCreate("🚀");

      expect(
        await screen.findByText(NO_LETTER_OR_NUMBER_ERROR)
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Create" })
      ).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("shows the reserved-name error under the name field", async () => {
      mockServer.use(
        http.post(baseUrl("/teams"), () =>
          invalidNameResponse('"Unassigned" is a reserved team name')
        )
      );

      await submitCreate("Unassigned");

      expect(
        await screen.findByText(
          '"Unassigned" is a reserved team name. Please try another name.'
        )
      ).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("shows a toast and closes the modal for other errors", async () => {
      mockServer.use(http.post(baseUrl("/teams"), serverErrorResponse));

      await submitCreate("Servers");

      await waitFor(() => {
        expect(notify.error).toHaveBeenCalledWith(
          "Could not create team. Please try again.",
          expect.anything()
        );
      });
      expect(
        screen.queryByRole("button", { name: "Create" })
      ).not.toBeInTheDocument();
    });
  });

  describe("renaming a team", () => {
    const submitRename = async (name: string) => {
      const { user } = renderPage();
      await user.click(await screen.findByText("Actions"));
      await user.click(await screen.findByText("Rename"));
      const nameInput = screen.getByLabelText("Team name");
      await user.clear(nameInput);
      await user.type(nameInput, name);
      await user.click(screen.getByRole("button", { name: "Save" }));
    };

    it("shows the conflict error under the name field on a 409, with no toast", async () => {
      mockServer.use(http.patch(baseUrl("/teams/1"), conflictResponse));

      await submitRename("Laptops!");

      expect(await screen.findByText(CONFLICT_ERROR)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("shows the letter-or-number error under the name field", async () => {
      mockServer.use(
        http.patch(baseUrl("/teams/1"), () =>
          invalidNameResponse("must include at least one letter or number")
        )
      );

      await submitRename("---");

      expect(
        await screen.findByText(NO_LETTER_OR_NUMBER_ERROR)
      ).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("shows the reserved-name error under the name field", async () => {
      mockServer.use(
        http.patch(baseUrl("/teams/1"), () =>
          invalidNameResponse('"All teams" is a reserved team name')
        )
      );

      await submitRename("All teams");

      expect(
        await screen.findByText(
          '"All teams" is a reserved team name. Please try another name.'
        )
      ).toBeInTheDocument();
      expect(notify.error).not.toHaveBeenCalled();
    });

    it("sends a rename to a variant of the team's own name", async () => {
      const patchSpy = jest.fn();
      mockServer.use(
        http.patch(baseUrl("/teams/1"), async ({ request }) => {
          patchSpy(await request.json());
          return HttpResponse.json({
            team: createMockTeam({ id: 1, name: "workstations" }),
          });
        })
      );

      await submitRename("workstations");

      await waitFor(() => {
        expect(notify.success).toHaveBeenCalledWith(
          "Successfully updated team name to workstations."
        );
      });
      expect(patchSpy).toHaveBeenCalledWith({ name: "workstations" });
      expect(
        screen.queryByRole("button", { name: "Save" })
      ).not.toBeInTheDocument();
    });

    it("shows a toast for other errors", async () => {
      mockServer.use(http.patch(baseUrl("/teams/1"), serverErrorResponse));

      await submitRename("Servers");

      await waitFor(() => {
        expect(notify.error).toHaveBeenCalledWith(
          "Could not rename Workstations. Please try again.",
          expect.anything()
        );
      });
    });
  });
});
