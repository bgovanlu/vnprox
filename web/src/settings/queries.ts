// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { fetchInstanceConfig } from "../api/config";

export const INSTANCE_CONFIG_QUERY_KEY = ["config"] as const;

/** The daemon's non-secret operational config (Settings page Instance
 * section). Rarely changes (it's fixed at daemon start), so it's cached
 * generously. */
export function useInstanceConfigQuery() {
  return useQuery({
    queryKey: INSTANCE_CONFIG_QUERY_KEY,
    queryFn: fetchInstanceConfig,
    staleTime: 5 * 60_000,
  });
}
