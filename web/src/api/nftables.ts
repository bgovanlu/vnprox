// SPDX-License-Identifier: Apache-2.0

// Compiled-ruleset inspector API calls (T-3904, docs/api.md's "Compiled
// ruleset (nftables)" section; internal/api/nftables.go). Read-only — no
// mutation call exists in this file, on purpose: this page never stages,
// validates, or applies a firewall change, matching the permanent
// PVE-firewall-engine boundary docs/features.md documents.
import { apiFetch } from "./client";
import type { NftRulesetResponse } from "./types";

/** GET /firewall/compiled?node=: one node's compiled nftables ruleset.
 * node="" (or omitted) asks for the local node. */
export function fetchCompiledRuleset(node: string): Promise<NftRulesetResponse> {
  const qs = node ? `?node=${encodeURIComponent(node)}` : "";
  return apiFetch<NftRulesetResponse>(`/firewall/compiled${qs}`);
}
