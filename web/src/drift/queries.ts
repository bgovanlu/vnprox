// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-305's drift findings: the list fetch, the
// "create fixing changeset" mutation, and the WS bridge that keeps the
// list fresh on `drift.changed` — the same "targeted invalidation on a WS
// event" pattern topology/queries.ts's useTopologyWsBridge established.
import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { adoptReality, fetchAdoption, fetchDrift, fixDriftFinding, restoreIntent } from "../api/drift";
import { fetchGitSyncStatus } from "../api/gitsync";
import { fetchSpec, fetchSpecPin, importSpec, pinSpec, unpinSpec } from "../api/spec";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { DriftChangedEvent } from "../api/types";

export const DRIFT_QUERY_KEY = ["drift"] as const;
export const GITSYNC_STATUS_QUERY_KEY = ["gitsync", "status"] as const;
export const SPEC_PIN_QUERY_KEY = ["spec", "pin"] as const;

/** Keyed under DRIFT_QUERY_KEY's prefix on purpose: a `drift.changed` push
 * invalidates the findings list and every finding's adoption link together,
 * since a finding that stopped existing has no adoption to show either. */
export function adoptionQueryKey(findingId: string): readonly [string, string, string] {
  return ["drift", findingId, "adoption"];
}

export function useDriftQuery() {
  return useQuery({
    queryKey: DRIFT_QUERY_KEY,
    queryFn: fetchDrift,
    staleTime: 15_000,
  });
}

export function useFixDriftMutation() {
  return useMutation({
    mutationFn: (id: string) => fixDriftFinding(id),
  });
}

// ---------------------------------------------------------------------------
// T-3001's config-as-code cockpit: the git sync's status, the pinned
// document, the import plan, and the two reconciliation actions.
//
// Every mutation below either stages a draft changeset the operator still
// reviews, or moves the DOCUMENT. None of them applies anything — there is no
// apply path in this feature module, by construction: nothing here imports the
// changeset apply/confirm calls at all.
// ---------------------------------------------------------------------------

/** GET /gitsync/status. Polled rather than pushed: the sync runs on its own
 * timer (`pollIntervalSeconds`) and emits no WS event of its own, so a screen
 * left open would otherwise show a fetch from whenever it was opened. */
export function useGitSyncStatusQuery() {
  return useQuery({
    queryKey: GITSYNC_STATUS_QUERY_KEY,
    queryFn: fetchGitSyncStatus,
    refetchInterval: 30_000,
  });
}

/** GET /spec/pin — the pinned document, or `{pinned: false}`. */
export function useSpecPinQuery() {
  return useQuery({
    queryKey: SPEC_PIN_QUERY_KEY,
    queryFn: fetchSpecPin,
    staleTime: 15_000,
  });
}

/** GET /spec — the live cluster rendered as a document. A mutation rather
 * than a query because it is an explicit "show me this now" action, and
 * because holding a whole cluster's YAML in the query cache for a screen that
 * mostly does not need it buys nothing. */
export function useExportSpecMutation() {
  return useMutation({ mutationFn: () => fetchSpec() });
}

/** POST /spec/pin. Invalidates the pin so the panel re-reads what the daemon
 * actually stored rather than trusting the body it just sent. */
export function usePinSpecMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (content: string) => pinSpec(content),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SPEC_PIN_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: DRIFT_QUERY_KEY });
    },
  });
}

/** DELETE /spec/pin. */
export function useUnpinSpecMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => unpinSpec(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SPEC_PIN_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: DRIFT_QUERY_KEY });
    },
  });
}

/** POST /spec/import — the plan. Note this *creates a draft changeset*, empty
 * plan included (internal/api/spec.go), which is why it is a mutation and why
 * the panel says so before the operator clicks. */
export function useImportSpecMutation() {
  return useMutation({ mutationFn: (content: string) => importSpec(content) });
}

/** POST /drift/{id}/restore-intent — stages a draft that moves the CLUSTER. */
export function useRestoreIntentMutation() {
  return useMutation({ mutationFn: (id: string) => restoreIntent(id) });
}

/** POST /drift/{id}/adopt-reality — opens a pull request that moves the
 * DOCUMENT. Deliberately a separate hook from the one above: the two have
 * opposite blast radii and share no call site, no confirmation and no
 * pending flag. */
export function useAdoptRealityMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => adoptReality(id),
    onSuccess: (_proposal, id) => {
      void queryClient.invalidateQueries({ queryKey: adoptionQueryKey(id) });
    },
  });
}

/** GET /drift/{id}/adoption — the pull request this finding was already
 * adopted as (`null` when the daemon answers its documented 404). */
export function useAdoptionQuery(findingId: string) {
  return useQuery({
    queryKey: adoptionQueryKey(findingId),
    queryFn: () => fetchAdoption(findingId),
    staleTime: 30_000,
  });
}

/** Runtime guard for the `drift.changed` payload (docs/api.md: `{count}`).
 * See topology/queries.ts's isTopologyDeltaEvent for why the narrowed type
 * is the `WsServerEvent &` intersection rather than plain DriftChangedEvent.
 * Exported for direct unit testing. */
export function isDriftChangedEvent(evt: WsServerEvent): evt is WsServerEvent & DriftChangedEvent {
  return evt.event === "drift.changed" && typeof evt.count === "number";
}

/** Applies one drift.changed event to the query cache: a targeted refetch
 * of the drift list (docs/api.md's WS section's own convention — see
 * applyTopologyDelta's doc comment for the same reasoning). Exported
 * standalone so it's directly unit-testable against a plain QueryClient. */
export function applyDriftChanged(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: DRIFT_QUERY_KEY });
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/** Subscribes to the `/api/ws` "drift" topic for the component's lifetime
 * and invalidates the drift query on every `drift.changed` push. Pass
 * `client` in tests to inject a client wired to a real `ws` test server
 * instead of the shared browser singleton (see topology/queries.ts's
 * useTopologyWsBridge, the same pattern). Subscribing to "drift" is
 * additive to whatever other topics (e.g. "topology") other components
 * already subscribe to on the same shared connection — WsClient unions
 * every active subscriber's topics into one `{"subscribe":[...]}` message
 * (see api/ws.ts's currentTopics), so this never clobbers them. */
export function useDriftWsBridge(client?: WsClient): void {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["drift"], (evt) => {
      if (isDriftChangedEvent(evt)) {
        applyDriftChanged(queryClientRef.current);
      }
    });
    return unsubscribe;
  }, [client]);
}
