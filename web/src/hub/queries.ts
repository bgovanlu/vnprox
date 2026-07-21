// TanStack Query hooks for the Blueprint & plugin hub (T-1705). One hook per
// API call; the install mutation invalidates the installed-plugins/blueprints
// lists a successful install affects.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchHubIndex, installHubItem } from "../api/hub";
import { BLUEPRINTS_QUERY_KEY } from "../blueprints/queries";
import type { HubEntryType, HubInstallRequest } from "../api/types";

export const hubIndexQueryKey = (type: HubEntryType) => ["hub", "index", type] as const;

export function useHubIndexQuery(type: HubEntryType) {
  return useQuery({
    queryKey: hubIndexQueryKey(type),
    queryFn: () => fetchHubIndex(type),
    staleTime: 30_000,
  });
}

export function useHubInstallMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: HubInstallRequest) => installHubItem(req),
    onSuccess: (resp) => {
      if (resp.type === "blueprint" && resp.status === "imported") {
        void queryClient.invalidateQueries({ queryKey: BLUEPRINTS_QUERY_KEY });
      }
      if (resp.type === "plugin" && resp.status === "installed") {
        void queryClient.invalidateQueries({ queryKey: ["plugins"] });
      }
    },
  });
}
