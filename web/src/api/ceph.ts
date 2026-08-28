// SPDX-License-Identifier: Apache-2.0

// Ceph network awareness API calls (docs/api.md's Ceph section, T-1503;
// internal/api/ceph.go's GET /ceph/status). Read-only, mirroring
// api/mtuprobe.ts's single-fetch shape.
import { apiFetch } from "./client";
import type { CephOverlay } from "./types";

/** GET /ceph/status — the map-layer projection: public/cluster network
 * CIDRs plus per-node/per-OSD physical bond attribution. A cluster with no
 * Ceph installed at all resolves to `{nodes: [], osds: []}`, never an
 * error. */
export function fetchCephStatus(): Promise<CephOverlay> {
  return apiFetch<CephOverlay>("/ceph/status");
}
