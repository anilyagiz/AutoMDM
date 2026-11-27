import { screen, waitFor, within } from "@testing-library/react";
import { UserEvent } from "@testing-library/user-event";
import { http, HttpResponse, RequestHandler } from "msw";
import React from "react";
import { QueryClient, QueryClientProvider } from "react-query";

import createMockConfig from "__mocks__/configMock";
import createMockPolicy from "__mocks__/policyMock";
import createMockUser from "__mocks__/userMock";
import { notify } from "components/ToastNotification";
import mockServer from "test/mock-server";
import {
  createCustomRenderer,
  createMockRouter,
  baseUrl,
} from "test/test-utils";

import ManagePoliciesPage from "./ManagePoliciesPage";

const AUTOMDM_WITH_POLICIES_ID = 1;
const AUTOMDM_WITHOUT_POLICIES_ID = 2;

const getConfigHandler = () =>
  http.get(baseUrl("/config"), () => HttpResponse.json(createMockConfig()));

const getAutoMDMHandler = () =>
  http.get(baseUrl("/teams/:id"), ({ params }) => {
    const automdmId = Number(params.id);
    const team = {
      ...createMockConfig(),
      id: automdmId,
      name: `AutoMDM ${automdmId}`,
    };
    return HttpResponse.json({ team });
  });

const getTeamId = (request: Request) => {
  const automdmId = new URL(request.url).searchParams.get("team_id");
  return automdmId === null ? undefined : Number(automdmId);
};

const getTeamPoliciesHandler = () =>
  http.get(baseUrl("/policies"), ({ request }) => {
    const automdmId = getTeamId(request);
    if (automdmId === undefined) {
      return HttpResponse.json({ policies: [] });
    }
    const policies =
      automdmId === AUTOMDM_WITH_POLICIES_ID
        ? [
            createMockPolicy({
              id: 1,
              name: "Software policy",
              team_id: automdmId,
              install_software: { name: "Zoom", software_title_id: 1 },
            }),
          ]
        : [];
    return HttpResponse.json({ policies });
  });

const getTeamPoliciesCountHandler = () =>
  http.get(baseUrl("/policies/count"), ({ request }) => {
    const automdmId = getTeamId(request);
    return HttpResponse.json({
      count: automdmId === AUTOMDM_WITH_POLICIES_ID ? 1 : 0,
    });
  });

const getAutomationFilterControl = (): HTMLElement => {
  const control = document.querySelector(
    ".manage-policies-page__filter-automation-dropdown .react-select__control"
  );
  if (!control) {
    throw new Error("Automations filter control not found");
  }
  return control as HTMLElement;
};

const setupHandlers = () => {
  mockServer.use(
    getConfigHandler(),
    getAutoMDMHandler(),
    getTeamPoliciesHandler(),
    getTeamPoliciesCountHandler()
  );
};

describe("ManagePoliciesPage - automations filter", () => {
  const renderPage = (automdmId: string, automationType?: string) => {
    setupHandlers();

    const render = createCustomRenderer({
      withBackendMock: true,
      context: {
        app: {
          currentUser: createMockUser({ global_role: "admin" }),
          isGlobalAdmin: true,
          isOnGlobalTeam: true,
          isPremiumTier: true,
          availableTeams: [
            { id: -1, name: "All teams" },
            { id: 0, name: "Unassigned" },
            { id: AUTOMDM_WITH_POLICIES_ID, name: "AutoMDM with policies" },
            { id: AUTOMDM_WITHOUT_POLICIES_ID, name: "AutoMDM without policies" },
          ],
          config: createMockConfig(),
          setCurrentTeam: jest.fn(),
          setFilteredPoliciesPath: jest.fn(),
          setConfig: jest.fn(),
        },
      },
    });

    const query: Record<string, string> = { team_id: automdmId };
    if (automationType) {
      query.automation_type = automationType;
    }

    return render(
      <ManagePoliciesPage
        router={createMockRouter()}
        location={{
          action: "PUSH",
          hash: "",
          key: "",
          pathname: "/policies/manage",
          query,
          search: `?${new URLSearchParams(query).toString()}`,
        }}
      />
    );
  };

  it("keeps the automations filter dropdown visible and enabled for a team with no policies when a filter is present in the URL", async () => {
    renderPage(AUTOMDM_WITHOUT_POLICIES_ID.toString(), "software");

    await waitFor(() => {
      expect(
        screen.getByText("No policies match the current filters.")
      ).toBeInTheDocument();
    });

    // Per the fix for #44624: when a filter is present in the query params,
    // the automations filter dropdown must remain visible and enabled (and
    // reflect the selected filter) even though this team has zero policies.
    expect(screen.getByText("Software")).toBeInTheDocument();
    expect(getAutomationFilterControl()).not.toHaveClass(
      "react-select__control--is-disabled"
    );
  });

  it("offers software/scripts/conditional access automation types for the 'Unassigned' team, but not calendar", async () => {
    const { user } = renderPage("0", "software");

    await waitFor(() => {
      expect(
        screen.getByText("No policies match the current filters.")
      ).toBeInTheDocument();
    });

    // The selected filter persists (sticky), matching what's in the URL.
    expect(screen.getByText("Software")).toBeInTheDocument();

    // "Unassigned" is a real, policy-bearing team (unlike "All teams", which
    // is restricted to webhook/ticket-only automations) -- it should offer
    // every automation type EXCEPT calendar events, which
    // PolicyAutomationsFields hardcodes as automdm-only (never available for
    // "All teams" or "Unassigned").
    await user.click(getAutomationFilterControl());

    expect(screen.getByText("Scripts")).toBeInTheDocument();
    expect(screen.getByText("Conditional access")).toBeInTheDocument();
    expect(screen.queryByText("Calendar")).not.toBeInTheDocument();
  });

  it("rejects an automation_type=calendar query param for the 'Unassigned' team, since calendar isn't a valid option there", async () => {
    renderPage("0", "calendar");

    await waitFor(() => {
      expect(
        screen.getByText("No policies for this team")
      ).toBeInTheDocument();
    });

    // "calendar" isn't a valid filter for "Unassigned", so it must not stick
    // as the selected value -- the filter should fall back to its default.
    expect(screen.getByText("All automations")).toBeInTheDocument();
    expect(screen.queryByText("Calendar")).not.toBeInTheDocument();
  });
});

describe("ManagePoliciesPage - team's own policies", () => {
  const INHERITED_ONLY_TEAM_ID = 3;
  const MIXED_TEAM_ID = 4;

  const inheritedPolicies = [
    createMockPolicy({ id: 10, name: "Inherited one", team_id: null }),
    createMockPolicy({ id: 11, name: "Inherited two", team_id: null }),
  ];
  const ownPolicies: Record<number, ReturnType<typeof createMockPolicy>[]> = {
    0: [createMockPolicy({ id: 30, name: "Unassigned one", team_id: 0 })],
    [INHERITED_ONLY_TEAM_ID]: [],
    [MIXED_TEAM_ID]: [
      createMockPolicy({
        id: 20,
        name: "Own policy",
        team_id: MIXED_TEAM_ID,
      }),
    ],
  };

  // The count has to differ with merge_inherited, or a page that used the
  // merged count wouldn't be caught.
  let deletedIds = new Set<number>();
  beforeEach(() => {
    deletedIds = new Set();
  });

  const policiesFor = (request: Request) => {
    const params = new URL(request.url).searchParams;
    if (!params.has("team_id")) {
      return inheritedPolicies.filter((policy) => !deletedIds.has(policy.id));
    }
    const own = ownPolicies[Number(params.get("team_id"))] ?? [];
    return (params.get("merge_inherited") === "true"
      ? [...own, ...inheritedPolicies]
      : own
    ).filter((policy) => !deletedIds.has(policy.id));
  };

  /** `automdmId` undefined is "All teams". `handlers` override the defaults.
   *  `keepCache` keeps inactive queries cached like the app does; the test
   *  renderer drops them at once. */
  const renderPage = (
    automdmId: number | undefined,
    isPrimo = false,
    handlers: RequestHandler[] = [],
    keepCache = false
  ) => {
    const config = createMockConfig({
      partnerships: { enable_primo: isPrimo },
    });
    mockServer.use(
      // The first matching handler wins, so overrides go first.
      ...handlers,
      http.get(baseUrl("/config"), () => HttpResponse.json(config)),
      getAutoMDMHandler(),
      http.get(baseUrl("/policies"), ({ request }) =>
        HttpResponse.json({ policies: policiesFor(request) })
      ),
      http.get(baseUrl("/policies/count"), ({ request }) =>
        HttpResponse.json({ count: policiesFor(request).length })
      ),
      http.post(baseUrl("/policies/delete"), async ({ request }) => {
        const { ids } = (await request.json()) as { ids: number[] };
        ids.forEach((id) => deletedIds.add(id));
        return HttpResponse.json({ deleted: ids });
      })
    );

    const render = createCustomRenderer({
      withBackendMock: true,
      context: {
        app: {
          currentUser: createMockUser({ global_role: "admin" }),
          isGlobalAdmin: true,
          isOnGlobalTeam: true,
          isPremiumTier: true,
          availableTeams: [
            { id: -1, name: "All teams" },
            { id: 0, name: "Unassigned" },
            { id: INHERITED_ONLY_TEAM_ID, name: "Inherited only" },
            { id: MIXED_TEAM_ID, name: "Mixed" },
          ],
          config,
          setCurrentTeam: jest.fn(),
          setFilteredPoliciesPath: jest.fn(),
          setConfig: jest.fn(),
        },
      },
    });

    const router = createMockRouter();
    const cacheClient = keepCache
      ? new QueryClient({ defaultOptions: { queries: { retry: false } } })
      : undefined;
    const page = (id: number | undefined) => {
      const query: Record<string, string> =
        id === undefined ? {} : { team_id: id.toString() };
      const element = (
        <ManagePoliciesPage
          router={router}
          location={{
            action: "PUSH",
            hash: "",
            key: "",
            pathname: "/policies/manage",
            query,
            search: `?${new URLSearchParams(query).toString()}`,
          }}
        />
      );
      return cacheClient ? (
        <QueryClientProvider client={cacheClient}>
          {element}
        </QueryClientProvider>
      ) : (
        element
      );
    };
    const result = render(page(automdmId));
    /** Changes the team in the URL without onTeamChange, like browser Back. */
    const showAutoMDM = (id: number | undefined) => result.rerender(page(id));
    return { ...result, showAutoMDM };
  };

  const rowCheckbox = async (name: string) =>
    within(
      (await screen.findAllByText(name))[0].closest("tr") as HTMLElement
    ).getByRole("checkbox");

  const confirmDelete = async (user: UserEvent) => {
    await user.click(screen.getByRole("button", { name: /Delete/ }));
    await screen.findByText("Delete policies");
    // The modal's Delete renders after the table's.
    const deleteButtons = screen.getAllByRole("button", { name: "Delete" });
    await user.click(deleteButtons[deleteButtons.length - 1]);
  };

  const ownCountRequest = (automdmId: number) => (request: Request) => {
    const params = new URL(request.url).searchParams;
    return (
      params.get("team_id") === String(automdmId) &&
      params.get("merge_inherited") !== "true"
    );
  };

  it("disables Manage automations and delete for a team with only inherited policies", async () => {
    renderPage(INHERITED_ONLY_TEAM_ID);

    await screen.findAllByText("Inherited one");
    // The button starts disabled, so wait for the merged count; a page using
    // it would enable the button once it arrives.
    await screen.findByText(/2 policies/);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Manage automations/i })
      ).toBeDisabled()
    );
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
  });

  it("enables Manage automations and delete for a team with its own policies", async () => {
    renderPage(MIXED_TEAM_ID);

    await screen.findAllByText("Own policy");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Manage automations/i })
      ).toBeEnabled()
    );
    expect(screen.queryAllByRole("checkbox").length).toBeGreaterThan(0);
  });

  it("still lets Primo mode delete inherited policies", async () => {
    const deleteRequests: number[][] = [];
    // Primo mode has no teams; its policies view is "Unassigned".
    const { user } = renderPage(0, true, [
      http.post(baseUrl("/policies/delete"), async ({ request }) => {
        const { ids } = (await request.json()) as { ids: number[] };
        deleteRequests.push(ids);
        ids.forEach((id) => deletedIds.add(id));
        return HttpResponse.json({ deleted: ids });
      }),
    ]);

    await user.click(await rowCheckbox("Inherited one"));
    await user.click(await rowCheckbox("Unassigned one"));
    await confirmDelete(user);

    await waitFor(() =>
      expect(screen.queryByText("Unassigned one")).not.toBeInTheDocument()
    );
    expect(screen.queryAllByText("Inherited one")).toHaveLength(0);
    expect(deleteRequests).toEqual([[10, 30]]);
  });

  it("refreshes and clears the selection when a delete fails partway", async () => {
    const notifyErrorSpy = jest.spyOn(notify, "error");
    const { user } = renderPage(0, true, [
      // The inherited policy is deleted before the request fails.
      http.post(baseUrl("/policies/delete"), () => {
        deletedIds.add(10);
        return HttpResponse.json({ message: "failed" }, { status: 500 });
      }),
    ]);

    await user.click(await rowCheckbox("Inherited one"));
    await user.click(await rowCheckbox("Unassigned one"));
    await confirmDelete(user);

    await waitFor(() =>
      expect(screen.queryByText("Inherited one")).not.toBeInTheDocument()
    );
    expect(await rowCheckbox("Unassigned one")).not.toBeChecked();
    expect(notifyErrorSpy).toHaveBeenCalledWith(
      expect.stringContaining("Some may have been deleted"),
      expect.anything()
    );
    notifyErrorSpy.mockRestore();
  });

  it("closes the delete confirmation when the team changes", async () => {
    const { user, showAutoMDM } = renderPage(MIXED_TEAM_ID);

    await user.click(await rowCheckbox("Own policy"));
    await user.click(screen.getByRole("button", { name: /Delete/ }));
    await screen.findByText("Delete policies");

    showAutoMDM(INHERITED_ONLY_TEAM_ID);

    await waitFor(() =>
      expect(screen.queryByText("Delete policies")).not.toBeInTheDocument()
    );
  });

  it("doesn't use the previous team's count while the new one loads", async () => {
    const { showAutoMDM } = renderPage(MIXED_TEAM_ID, false, [
      http.get(baseUrl("/policies/count"), ({ request }) =>
        ownCountRequest(INHERITED_ONLY_TEAM_ID)(request)
          ? new Promise<never>(() => undefined)
          : undefined
      ),
    ]);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Manage automations/i })
      ).toBeEnabled()
    );

    showAutoMDM(INHERITED_ONLY_TEAM_ID);

    await screen.findByText(/2 policies/);
    expect(
      screen.getByRole("button", { name: /Manage automations/i })
    ).toBeDisabled();
  });

  it("keeps the team's own policies actionable when their count fails", async () => {
    renderPage(MIXED_TEAM_ID, false, [
      http.get(baseUrl("/policies/count"), ({ request }) =>
        ownCountRequest(MIXED_TEAM_ID)(request)
          ? HttpResponse.json({ message: "failed" }, { status: 500 })
          : undefined
      ),
    ]);

    await screen.findAllByText("Own policy");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Manage automations/i })
      ).toBeEnabled()
    );
  });

  it("doesn't show a deleted policy from another team's cached list", async () => {
    const { user, showAutoMDM } = renderPage(MIXED_TEAM_ID, false, [], true);
    await screen.findAllByText("Inherited one");

    showAutoMDM(undefined);
    await user.click(await rowCheckbox("Inherited one"));
    await confirmDelete(user);
    await waitFor(() =>
      expect(screen.queryByText("Inherited one")).not.toBeInTheDocument()
    );

    showAutoMDM(MIXED_TEAM_ID);

    expect(screen.queryByText("Inherited one")).not.toBeInTheDocument();
    await screen.findAllByText("Own policy");
    expect(screen.queryByText("Inherited one")).not.toBeInTheDocument();
  });
});
