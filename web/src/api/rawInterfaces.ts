// T-208 raw editor API calls (docs/api.md "Raw interfaces editor"). These
// are the only two routes the raw Monaco editor needs beyond the ordinary
// changesets endpoints (api/changesets.ts) it saves through.
import { apiFetch } from "./client";

/** GET /nodes/{node}/interfaces/raw response: the current live file plus
 * its sha256 — the editor's "open" call and the hash-conflict guard's
 * baseline. */
export interface RawInterfacesFile {
  node: string;
  content: string;
  sha256: string;
}

export function getRawInterfaces(node: string): Promise<RawInterfacesFile> {
  return apiFetch<RawInterfacesFile>(`/nodes/${encodeURIComponent(node)}/interfaces/raw`);
}

/** One interfaces(5) syntax diagnostic (line-precise, 1-based — matches
 * Monaco's own marker line numbering). */
export interface LintMarker {
  line: number;
  message: string;
}

export interface LintResult {
  errors: LintMarker[];
}

/** POST /interfaces/lint — a pure syntax check, debounced by the editor as
 * the user types (AC1: "syntax errors underline with line-precise messages
 * as you type", latency budget <300ms). No CSRF token: it mutates nothing
 * server-side. */
export function lintInterfaces(content: string): Promise<LintResult> {
  return apiFetch<LintResult>("/interfaces/lint", { method: "POST", json: { content } });
}
