// Polls T-1501's Kubernetes read routes while the "Kubernetes" map layer
// (or the flow explorer's k8sService attribution — k8sFlowAttribution.ts)
// needs them. Mirrors web/src/wireguard/wgTunnelsQuery.ts's shape: no WS
// push exists for this data, so a plain refetch-interval poll is the whole
// freshness mechanism.
import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { fetchK8sClusters, fetchK8sOverlay } from "../../api/k8s";
import type { K8sCluster, K8sOverlay } from "../../api/types";

export const k8sClustersKey = ["k8s-clusters"] as const;
export const k8sOverlayKey = (clusterId: string) => ["k8s-overlay", clusterId] as const;

/** GET /k8s/{clusterId}/overlay is a live poll against the cluster's own
 * API server (docs/api.md's Kubernetes section) — 30s balances staying
 * reasonably fresh against not hammering a real cluster's apiserver, the
 * same interval useWireGuardTunnelsQuery uses for its own live-status
 * poll. */
const OVERLAY_REFETCH_MS = 30_000;

export function useK8sClustersQuery(enabled: boolean): { data: K8sCluster[] | undefined; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: k8sClustersKey,
    queryFn: fetchK8sClusters,
    enabled,
    staleTime: 15_000,
  });
  return { data, isLoading };
}

/** Fans GET /k8s/{clusterId}/overlay out across every currently-registered
 * cluster — the Kubernetes layer (and flow attribution) draws every
 * registered cluster's overlay at once, mirroring how the WireGuard layer
 * draws every tunnel this node can see rather than requiring a picker.
 *
 * `overlays` is memoized on each result's own `dataUpdatedAt`, not left as
 * a fresh array built on every call — `useQueries` itself returns a new
 * array instance on every render regardless of whether any result's data
 * actually changed (web/src/guests/queries.ts's `useAllGuestNicsQuery` hit
 * this exact pitfall first: "keying off its serialized data instead avoids
 * an infinite recompute loop while still reacting to actual data
 * changes"). Without this, `overlays`' changing identity defeats every
 * downstream `useMemo` in TopologyPage.tsx's k8s overlay pipeline
 * (`k8sOverlay` -> `extraNodes`/`extraEdges` -> `elements`), forcing a
 * fresh recompute — and therefore a fresh object identity handed to the v2
 * canvas/elk layout — on every single render even while this layer is
 * disabled, which measurably destabilized flows.spec.ts's own
 * elk-layout-timing-sensitive assertions in practice (found while
 * verifying this task's e2e suite doesn't regress others' — see this
 * task's completion report). */
export function useK8sOverlaysQuery(
  clusters: readonly K8sCluster[] | undefined,
  enabled: boolean,
): { overlays: K8sOverlay[]; isLoading: boolean } {
  const ids = clusters?.map((c) => c.id) ?? [];
  const results = useQueries({
    queries: ids.map((id) => ({
      queryKey: k8sOverlayKey(id),
      queryFn: () => fetchK8sOverlay(id),
      enabled,
      staleTime: 10_000,
      refetchInterval: enabled ? OVERLAY_REFETCH_MS : false,
    })),
  });

  const dataFingerprint = results.map((r) => `${String(r.dataUpdatedAt)}:${r.data ? "1" : "0"}`).join(",");
  const overlays = useMemo(
    () => results.map((r) => r.data).filter((data): data is K8sOverlay => data !== undefined),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- dataFingerprint is the intentional, stable proxy for results' actual content (see doc comment above)
    [dataFingerprint],
  );
  const isLoading = enabled && results.some((r) => r.isLoading);
  return { overlays, isLoading };
}
