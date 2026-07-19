// Ceph network overlay (docs/features/topology.md §1's ninth, client-only
// map layer, T-1503): paints `ceph-public`/`ceph-cluster` badges onto the
// existing bond/PhysNic map nodes a node's Ceph traffic rides
// (`GET /ceph/status`'s `publicRidingOn`/`clusterRidingOn`) rather than
// introducing new map nodes — mirroring mtuOverlay.ts's "pure mapping
// function, kept separate from canvas draw code so it's unit-testable
// without a real CanvasRenderingContext2D" precedent, and LayerToggleBar's
// established optional-pair-prop convention for a layer this simple (no
// synthetic endpoint node, unlike WireGuard's).

import type { CephNodeAttribution, CephOSD, CephOverlay } from "../api/types";

export type CephBadgeKind = "ceph-public" | "ceph-cluster";

/** One badge to paint onto an existing bond/PhysNic map node — `nodeId` is
 * that entity's own `inventory.Ref` string (already the map's node id
 * convention, `docs/features/topology.md` §3), so no id-resolution step is
 * needed the way `computeMTUOverlayEdges`' `nodeIdForName` requires for
 * node-name-keyed link endpoints. */
export interface CephBadge {
  nodeId: string;
  kind: CephBadgeKind;
}

/** Resolves overlay's per-node `publicRidingOn`/`clusterRidingOn` refs to
 * the badge set the map layer paints — a node with an unresolved carrier
 * (undeclared CIDR, or an ambiguous/no path) simply contributes no badge
 * for that network, never a guess. A bond/NIC riding *both* networks (the
 * `ceph_single_nic` footgun's own shape) gets both badges on the same
 * node id — never merged into one, since the two are visually and
 * semantically distinct network identities. Deduplicated and sorted by
 * (nodeId, kind) for deterministic rendering. */
export function computeCephBadges(overlay: CephOverlay): CephBadge[] {
  const seen = new Set<string>();
  const out: CephBadge[] = [];
  const add = (nodeId: string | undefined, kind: CephBadgeKind) => {
    if (!nodeId) return;
    const key = `${nodeId}|${kind}`;
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ nodeId, kind });
  };
  for (const na of overlay.nodes) {
    add(na.publicRidingOn, "ceph-public");
    add(na.clusterRidingOn, "ceph-cluster");
  }
  out.sort((a, b) => (a.nodeId === b.nodeId ? a.kind.localeCompare(b.kind) : a.nodeId.localeCompare(b.nodeId)));
  return out;
}

/** Every OSD hosted on node, sorted by id — the inspector panel's "which
 * OSDs ride which bonds" listing for a selected node
 * (`docs/features/topology.md` §1's ninth-layer note). */
export function osdsForNode(overlay: CephOverlay, node: string): CephOSD[] {
  return overlay.osds.filter((o) => o.node === node).sort((a, b) => a.id - b.id);
}

/** Looks up a single node's resolved attribution by name, or undefined if
 * this node hosts no OSDs at all (Overlay.Nodes only ever contains
 * OSD-hosting nodes — internal/ceph.Project's own contract). */
export function attributionForNode(overlay: CephOverlay, node: string): CephNodeAttribution | undefined {
  return overlay.nodes.find((na) => na.node === node);
}
