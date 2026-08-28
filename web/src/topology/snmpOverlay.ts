// SPDX-License-Identifier: Apache-2.0

// SNMP switch-counter map-edge annotation (docs/api.md's "Switch counters
// (SNMP)" section, T-4013): resolves GET /snmp/counters results to the
// canvas-edge badges a "Switch counters" layer paints, mirroring
// mtuOverlay.ts's computeMTUOverlayEdges precedent — a pure mapping
// function, kept separate from any canvasDraw.ts draw function so the
// resolution logic is unit-testable without a real CanvasRenderingContext2D.
//
// Unlike computeMTUOverlayEdges (which resolves a node-to-node path via a
// PVE node *name* lookup), each SNMPCounterResult identifies a physical
// local-NIC<->switch-port edge by (chassisId, localIface) — the same
// identity internal/ifcounters.Service keys its in-memory results by. This
// module deliberately takes that resolution as an injected callback
// (edgeIdForPort) rather than hard-coding a lookup against the canvas's own
// switch/NIC node-id scheme: which map node currently represents an
// LLDP-discovered switch chassis is a decision the topology rendering layer
// owns (see web/src/topology/EntityEdge.tsx / TopologyCanvasV2.tsx for how
// other LLDP-derived edges resolve their endpoints), and this task
// deliberately did not touch those files — see this task's own report for
// why (concurrent edits to the same rendering pipeline).

import type { SNMPCounterResult, SNMPCounterState } from "../api/types";

/** One GET /snmp/counters reading, resolved to the canvas-edge badge a
 * "Switch counters" layer would draw at the edge midpoint — the same
 * division of labor MTUOverlayBadge/LatencyOverlayEdge already establish. */
export interface SNMPCounterOverlayBadge {
  id: string;
  from: string;
  to: string;
  state: SNMPCounterState;
  inErrors?: number;
  outErrors?: number;
  inDiscards?: number;
  outDiscards?: number;
  operUp?: boolean;
  at: number;
}

/** Resolves results to their on-canvas edge endpoints via edgeIdForPort —
 * a result whose (chassisId, localIface) the caller's resolver cannot place
 * on the current canvas produces no badge, never a placeholder (the same
 * "no result yet shows no badge, not a stale/zero value" contract
 * computeMTUOverlayEdges documents). Every result produces a badge
 * regardless of `state` — the honest not_configured/unreachable/no_counters/
 * ok split (SNMPCounterState's doc comment) is a rendering decision for the
 * badge itself (e.g. a muted/grey badge for not_configured vs. a colored
 * one for ok), not a filter applied here. */
export function computeSNMPCounterOverlayEdges(
  results: readonly SNMPCounterResult[],
  edgeIdForPort: (chassisId: string, localIface: string) => { from: string; to: string } | undefined,
): SNMPCounterOverlayBadge[] {
  const out: SNMPCounterOverlayBadge[] = [];
  for (const r of results) {
    const endpoints = edgeIdForPort(r.chassisId, r.localIface);
    if (!endpoints || endpoints.from === endpoints.to) continue;
    out.push({
      id: `${r.chassisId}|${r.localIface}`,
      from: endpoints.from,
      to: endpoints.to,
      state: r.state,
      inErrors: r.inErrors,
      outErrors: r.outErrors,
      inDiscards: r.inDiscards,
      outDiscards: r.outDiscards,
      operUp: r.operUp,
      at: r.at,
    });
  }
  return out.sort((a, b) => a.id.localeCompare(b.id));
}

/** Formats a badge's label text for the `ok` state — "ERR 5 / DISC 2",
 * naming both counter families so it's never mistaken for a bare
 * utilization number; a caller renders honestly-worded text for the other
 * three states instead (e.g. "not configured", "unreachable"). */
export function formatSNMPCounterBadgeLabel(badge: SNMPCounterOverlayBadge): string {
  const errs = (badge.inErrors ?? 0) + (badge.outErrors ?? 0);
  const discards = (badge.inDiscards ?? 0) + (badge.outDiscards ?? 0);
  return `ERR ${String(errs)} / DISC ${String(discards)}`;
}
