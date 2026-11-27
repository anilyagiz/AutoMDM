import { useQuery, UseQueryOptions, UseQueryResult } from "react-query";

import { IPatchDeadlineSettings } from "interfaces/patch_deadline_settings";
import patchDeadlineSettingsAPI from "services/entities/patch_deadline_settings";

/** Fetches a team's patch deadline settings via React Query. Shared cache key
 * means multiple consumers on the same page (banner + Library settings) dedupe
 * through React Query instead of double-fetching. Stays idle while `automdmId`
 * is undefined. */
const usePatchDeadlineSettings = (
  automdmId: number | undefined,
  options?: Omit<
    UseQueryOptions<IPatchDeadlineSettings, Error>,
    "queryKey" | "queryFn"
  >
): UseQueryResult<IPatchDeadlineSettings, Error> =>
  useQuery<IPatchDeadlineSettings, Error>(
    ["patchDeadlineSettings", automdmId],
    () => patchDeadlineSettingsAPI.getSettings(automdmId as number),
    {
      refetchOnWindowFocus: false,
      ...options,
      enabled: automdmId !== undefined && (options?.enabled ?? true),
    }
  );

export default usePatchDeadlineSettings;
