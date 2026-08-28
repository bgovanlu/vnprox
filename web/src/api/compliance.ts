// SPDX-License-Identifier: Apache-2.0

// T-2706's compliance profiles: `GET /compliance` and
// `GET /compliance/{profile}` (docs/api.md §"Compliance profiles & evidence
// export"). Both are read-only; a report is a derived artifact, never staged,
// applied or persisted.
//
// The load-bearing part of this contract is the control `status` vocabulary,
// and it is deliberately NOT typed as a union here. Four values exist today
// (`pass`|`fail`|`not_evaluated`|`unmapped`), exactly one of which is a pass,
// and a client that models them as a closed union renders a fifth one as
// `undefined` — which is how a status nobody modelled becomes a definite-
// looking one. `complianceStatus.ts` classifies the string at runtime and
// carries an explicit `unknown` case instead.
import { apiFetch } from "./client";

/** `GET /compliance`'s items. `unmappedControls` is the count of controls the
 * profile itself maps no evidence onto — a property of the profile, not of
 * this cluster's state. */
export interface ComplianceProfileSummary {
  id: string;
  title: string;
  version: string;
  description?: string;
  notice: string;
  controlCount: number;
  mappedChecks: number;
  unmappedControls: number;
}

/** One evidence item behind a control. `status` is `satisfied`|`unsatisfied`|
 * `not_evaluated` server-side, typed as a plain string for the same reason
 * the control status is. */
export interface ComplianceEvidence {
  kind: string;
  name: string;
  status: string;
  detail: string;
  note?: string;
  refs?: string[];
}

/** One evaluated control. `evidence` is OMITTED (not empty) for a control the
 * profile maps nothing onto — which is the `unmapped` case, and the reason
 * `unmappedReason` exists to be rendered beside it. */
export interface ComplianceControl {
  id: string;
  title: string;
  statement: string;
  status: string;
  evidence?: ComplianceEvidence[];
  unmappedReason?: string;
}

export interface ComplianceSummary {
  pass: number;
  fail: number;
  notEvaluated: number;
  unmapped: number;
  total: number;
}

/** `GET /compliance/{profile}`. `caveats` is never empty for a historical
 * (`asOf`) report; `unmappedChecks` names every check this build can emit
 * that no control maps, with `checkUniverse` stating where that list came
 * from. */
export interface ComplianceReport {
  productVersion: string;
  profileId: string;
  profileTitle: string;
  profileVersion: string;
  notice: string;
  generatedAt: number;
  asOf?: number;
  caveats?: string[];
  summary: ComplianceSummary;
  controls: ComplianceControl[];
  unmappedChecks?: string[];
  checkUniverse: string;
}

/** GET /compliance — `netRead`. */
export function fetchComplianceProfiles(): Promise<{ items: ComplianceProfileSummary[] }> {
  return apiFetch<{ items: ComplianceProfileSummary[] }>("/compliance");
}

/** GET /compliance/{profile} — `netRead`. An unknown profile is `404
 * not_found` carrying `details.availableProfiles`; a date outside the
 * retention window is `400 outside_retention_window` rather than a thinner
 * report, and the caller must surface the daemon's own refusal. */
export function fetchComplianceReport(profile: string): Promise<ComplianceReport> {
  return apiFetch<ComplianceReport>(`/compliance/${encodeURIComponent(profile)}`);
}
