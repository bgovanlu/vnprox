// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-3004's analysis surfaces (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch in
// components").
//
// None of these routes has a WS event behind it — each is a fresh synthesis
// over the current inventory snapshot (SPOF, PBS) or a read of an app-owned
// table (QoS shapes, capacity aggregates), with no natural delta shape to
// push. "Live" therefore means polling on a deliberately slow interval: a
// SPOF score re-simulates every enumerated element's removal, which is far
// too much work to run at the conntrack explorer's 5s cadence.
import { useQuery } from "@tanstack/react-query";
import { fetchCapacityExport } from "../api/capacity";
import { fetchSpofScore } from "../api/failsim";
import { fetchPbsOverlay } from "../api/pbs";
import { fetchQosShapes } from "../api/qos";
import type { CapacityExport, CapacityKind, PbsOverlay, QosShape, SpofScore } from "../api/types";

/** SPOF is the most expensive read in this file (one simulated removal per
 * node, bond, uplink bridge, uplink NIC, LLDP-discovered switch and tunnel),
 * and a single point of failure is not a fast-moving fact. */
export const SPOF_REFETCH_MS = 60_000;

/** PBS and QoS are both config-derived reads that change only when an
 * operator changes something, so they poll on the same moderate cadence the
 * Edge cockpit uses. */
export const ANALYSIS_REFETCH_MS = 30_000;

export const SPOF_QUERY_KEY = ["failsim", "spof-score"] as const;
export const PBS_QUERY_KEY = ["pbs", "overlay"] as const;
export const QOS_SHAPES_QUERY_KEY = ["qos", "shapes"] as const;

export function useSpofScoreQuery() {
  return useQuery<SpofScore>({
    queryKey: SPOF_QUERY_KEY,
    queryFn: fetchSpofScore,
    refetchInterval: SPOF_REFETCH_MS,
  });
}

export function usePbsOverlayQuery() {
  return useQuery<PbsOverlay>({
    queryKey: PBS_QUERY_KEY,
    queryFn: fetchPbsOverlay,
    refetchInterval: ANALYSIS_REFETCH_MS,
  });
}

export function useQosShapesQuery() {
  return useQuery<QosShape[]>({
    queryKey: QOS_SHAPES_QUERY_KEY,
    queryFn: fetchQosShapes,
    refetchInterval: ANALYSIS_REFETCH_MS,
  });
}

export function capacityExportQueryKey(ref: string, kind: CapacityKind) {
  return ["capacity", "export", kind, ref] as const;
}

/** The JSON preview of what a CSV export would contain. Disabled until a
 * ref is picked — both `ref` and `kind` are required query parameters and
 * the daemon answers `400 validation_failed` without them, so there is no
 * "fetch everything" call to make here. Daily buckets do not move within a
 * session, so this one does not poll. */
export function useCapacityExportQuery(ref: string | undefined, kind: CapacityKind) {
  return useQuery<CapacityExport>({
    queryKey: capacityExportQueryKey(ref ?? "", kind),
    queryFn: () => fetchCapacityExport(ref ?? "", kind),
    enabled: Boolean(ref),
    staleTime: ANALYSIS_REFETCH_MS,
  });
}
