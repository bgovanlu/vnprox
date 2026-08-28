// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for the Platform panel's four route families
// (T-3003). Server state only ever reaches a component through these — no
// component in this feature calls `apiFetch` directly (docs/development.md's
// TypeScript standards).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchTokens, mintToken, revokeToken } from "../api/tokens";
import { createWebhook, deleteWebhook, fetchWebhooks } from "../api/webhooks";
import { disablePlugin, enablePlugin, fetchPlugins, uninstallPlugin } from "../api/plugins";
import { fetchDoctorLive } from "../api/doctor";
import type { ApiTokenCreateRequest, WebhookCreateRequest } from "../api/types";

export const TOKENS_QUERY_KEY = ["tokens"] as const;
export const WEBHOOKS_QUERY_KEY = ["webhooks"] as const;
/** Deliberately `["plugins"]`: `web/src/hub/queries.ts`'s install mutation
 * already invalidates exactly this key on a successful plugin install, and
 * has had no consumer since T-1705. Matching it means installing from the Hub
 * refreshes this panel's list for free rather than leaving it stale. */
export const PLUGINS_QUERY_KEY = ["plugins"] as const;
export const DOCTOR_LIVE_QUERY_KEY = ["doctor", "live"] as const;

/** Every query below sets `retry: false`. These routes fail for *structural*
 * reasons an operator needs to see immediately — a 403 because the session
 * lacks the capability, a 404 because the subsystem is not wired — and
 * retrying a structural refusal three times only delays the explanation. */
const NO_RETRY = { retry: false } as const;

export function useTokensQuery() {
  return useQuery({ queryKey: TOKENS_QUERY_KEY, queryFn: fetchTokens, ...NO_RETRY });
}

export function useMintTokenMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: ApiTokenCreateRequest) => mintToken(req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TOKENS_QUERY_KEY });
    },
  });
}

export function useRevokeTokenMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => revokeToken(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TOKENS_QUERY_KEY });
    },
  });
}

export function useWebhooksQuery() {
  return useQuery({ queryKey: WEBHOOKS_QUERY_KEY, queryFn: fetchWebhooks, ...NO_RETRY });
}

export function useCreateWebhookMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: WebhookCreateRequest) => createWebhook(req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: WEBHOOKS_QUERY_KEY });
    },
  });
}

export function useDeleteWebhookMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteWebhook(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: WEBHOOKS_QUERY_KEY });
    },
  });
}

export function usePluginsQuery() {
  return useQuery({ queryKey: PLUGINS_QUERY_KEY, queryFn: fetchPlugins, ...NO_RETRY });
}

/** The three lifecycle verbs share one mutation shape; `action` picks which
 * route runs. Kept as one hook rather than three so the invalidation and the
 * error surface cannot drift apart between them. */
export type PluginLifecycleAction = "enable" | "disable" | "uninstall";

const PLUGIN_ACTIONS: Record<PluginLifecycleAction, (id: string) => Promise<void>> = {
  enable: enablePlugin,
  disable: disablePlugin,
  uninstall: uninstallPlugin,
};

export function usePluginLifecycleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: PluginLifecycleAction }) => PLUGIN_ACTIONS[action](id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PLUGINS_QUERY_KEY });
    },
  });
}

/** `GET /doctor/live` runs real probes against PVE and every peer on each
 * call, so it is never background-refetched: it runs when the panel mounts
 * and when an operator asks for it again. */
export function useDoctorLiveQuery() {
  return useQuery({
    queryKey: DOCTOR_LIVE_QUERY_KEY,
    queryFn: fetchDoctorLive,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    ...NO_RETRY,
  });
}
