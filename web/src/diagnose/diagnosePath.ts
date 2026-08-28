// SPDX-License-Identifier: Apache-2.0

// Shared "is this entity diagnosable, and what URL opens its Diagnose
// page" helper — reused by both of T-1307's map entry points
// (topology/TopologyPage.tsx's right-click menu and
// topology/InspectorPanel.tsx's header action), mirroring
// conntrack/urlState.ts + topology/TopologyPage.tsx's own
// CONNTRACK_ENTITY_KINDS precedent for a shared entity-kind allowlist.
//
// Broader than isTraceableEntityKind (guest-nic only, simulator/traceLink.ts)
// since POST /diagnose's own target resolution (internal/api/diagnose.go's
// resolveDiagnoseTarget) accepts a guest-nic, a bare guest, or any edge
// kind (bridge/bond/vlan/SDN VNet) — an edge target simply gets the
// guest-specific steps skipped rather than being rejected.
const DIAGNOSABLE_ENTITY_KINDS = new Set([
  "guest-nic",
  "guest",
  "bridge",
  "ovs-bridge",
  "bond",
  "ovs-bond",
  "vlan",
  "sdn-vnet",
]);

export function isDiagnosableEntityKind(kind: string): boolean {
  return DIAGNOSABLE_ENTITY_KINDS.has(kind);
}

/** Builds the `/diagnose?ref=...` URL for ref — undefined for a
 * non-diagnosable kind (mirrors traceLink.ts's `undefined`-when-
 * inapplicable convention, so callers can `if (path) navigate(path)`
 * uniformly). */
export function diagnosePath(kind: string, ref: string): string | undefined {
  if (!isDiagnosableEntityKind(kind)) return undefined;
  return `/diagnose?ref=${encodeURIComponent(ref)}`;
}
