import { omit } from "lodash";
import { useQuery } from "react-query";

import mdmApi, {
  IGetProfilesApiParams,
  IMdmProfilesResponse,
} from "services/entities/mdm";

const PROFILES_PAGE_SIZE = 1000;

interface IUseProfilesArgs {
  automdmId: number;
  enabled: boolean;
}

type IUseProfilesQueryKey = IGetProfilesApiParams & { scope: "profiles" };

const useProfiles = ({ automdmId, enabled }: IUseProfilesArgs) =>
  useQuery<
    IMdmProfilesResponse,
    Error,
    IMdmProfilesResponse,
    [IUseProfilesQueryKey]
  >(
    [
      {
        scope: "profiles",
        page: 0,
        per_page: PROFILES_PAGE_SIZE,
        team_id: automdmId,
      },
    ],
    ({ queryKey: [key] }) => mdmApi.getProfiles(omit(key, "scope")),
    { enabled, staleTime: 30_000 }
  );

export default useProfiles;
