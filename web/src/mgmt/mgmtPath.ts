// SPDX-License-Identifier: Apache-2.0

// Shared plain-English description of a management path's redundancy, used
// by both the topology inspector's "Management path" section and the
// dedicated Management page (issue #1). `path` is the resolved physical
// interfaces behind a carrier (ManagementPathRef.path); `redundant` is the
// server's ≥2-link-up verdict.
export function describeMgmtPathRedundancy(path: string[], redundant: boolean): string {
  if (redundant) {
    return `Redundant: this path has ${String(path.length)} physical interfaces behind it — losing any one still leaves connectivity.`;
  }
  if (path.length === 0) {
    return "Not redundant: no physical interface is resolved behind this carrier yet.";
  }
  if (path.length === 1) {
    return `Not redundant: ${path[0] ?? ""} is the only physical interface behind this carrier — if it fails, this path goes down with it.`;
  }
  return "Not redundant: fewer than two of the physical interfaces behind this carrier are confirmed link-up.";
}
