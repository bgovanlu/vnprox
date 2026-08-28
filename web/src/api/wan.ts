// SPDX-License-Identifier: Apache-2.0

// WAN & upstream health API calls (docs/api.md's WAN & upstream health
// section, T-1405; internal/api/wan.go).
//
// Node-local scope, deliberately: GET/PUT /wan/targets carry no {node} path
// param and act on the *requesting* node's own store, so the cluster-wide
// picture is the union of every node's own GET /wan/status. That is the
// same documented gap GET /latmesh/heatmap and GET /mtuprobe/results carry;
// the response's own `node` field is what says which node answered.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { WanStatus, WanTarget, WanTargetsView } from "./types";

/** GET /wan/status — per-uplink availability/latency/loss plus the daemon's
 * own verdict and summary sentence. */
export function fetchWanStatus(): Promise<WanStatus> {
  return apiFetch<WanStatus>("/wan/status");
}

/** GET /wan/targets — this node's own configured reference targets. */
export function fetchWanTargets(): Promise<WanTargetsView> {
  return apiFetch<WanTargetsView>("/wan/targets");
}

/** PUT /wan/targets — a full-set replace, never a partial patch. The daemon
 * rejects a host that is neither an IP literal nor an RFC 1123 hostname
 * (T-2905's validWANHost) with `400 validation_failed`; the caller surfaces
 * that refusal by name rather than retrying or sanitising it here. */
export function replaceWanTargets(targets: WanTarget[]): Promise<WanTargetsView> {
  return apiFetch<WanTargetsView>("/wan/targets", {
    method: "PUT",
    json: { targets },
    csrfToken: readCsrfCookie(),
  });
}
