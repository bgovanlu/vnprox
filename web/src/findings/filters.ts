// Pure filter-composition logic for the unified findings stream (AC2:
// "filter by source/severity/node works across drift+lldp+ipam+health
// uniformly"). Kept separate from any React component so it's directly
// unit-testable without rendering anything, and so FindingsStreamPanel's
// filter UI stays a thin wrapper around this single source of truth
// (the server's own GET /findings?source=&severity=&node= applies the
// identical semantics — see internal/api/findings.go's handleFindings —
// so this client-side filter is a snappy, no-round-trip mirror of the same
// contract, not a second one).
import type { FindingSource, Severity, StreamFinding } from "../api/types";

export interface FindingsFilterState {
  source: FindingSource | "";
  severity: Severity | "";
  node: string;
}

export const EMPTY_FILTER: FindingsFilterState = { source: "", severity: "", node: "" };

/** Applies filter to findings: every set field is an AND-combined exact
 * match (source/severity against the finding's own field, node against
 * membership in the finding's `nodes` array) — an empty/unset filter field
 * matches everything, mirroring the server's "omitted query param = no
 * filter" contract exactly. */
export function filterFindings(findings: StreamFinding[], filter: FindingsFilterState): StreamFinding[] {
  return findings.filter((f) => {
    if (filter.source && f.source !== filter.source) return false;
    if (filter.severity && f.severity !== filter.severity) return false;
    if (filter.node && !f.nodes.includes(filter.node)) return false;
    return true;
  });
}

/** Every distinct node named across findings, sorted — the candidate list
 * for the node filter's <select>. */
export function nodesIn(findings: StreamFinding[]): string[] {
  const set = new Set<string>();
  for (const f of findings) {
    for (const n of f.nodes) set.add(n);
  }
  return Array.from(set).sort((a, b) => a.localeCompare(b));
}
