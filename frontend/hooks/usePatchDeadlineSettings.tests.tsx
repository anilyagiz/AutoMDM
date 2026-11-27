import { renderHook, waitFor } from "@testing-library/react";
import React from "react";
import { QueryClient, QueryClientProvider } from "react-query";

import { getPatchDeadlineSettingsHandler } from "test/handlers/patch-deadline-settings-handlers";
import mockServer from "test/mock-server";

import usePatchDeadlineSettings from "./usePatchDeadlineSettings";

const buildWrapper = () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
};

describe("usePatchDeadlineSettings", () => {
  it("fetches the new team's settings when automdmId changes", async () => {
    mockServer.use(getPatchDeadlineSettingsHandler());
    const { result, rerender } = renderHook(
      ({ automdmId }: { automdmId: number }) => usePatchDeadlineSettings(automdmId),
      { wrapper: buildWrapper(), initialProps: { automdmId: 1 } }
    );

    await waitFor(() => expect(result.current.data?.team_id).toBe(1));

    rerender({ automdmId: 2 });

    await waitFor(() => expect(result.current.data?.team_id).toBe(2));
  });

  it("stays idle without an automdmId", () => {
    const { result } = renderHook(() => usePatchDeadlineSettings(undefined), {
      wrapper: buildWrapper(),
    });

    expect(result.current.isIdle).toBe(true);
  });
});
