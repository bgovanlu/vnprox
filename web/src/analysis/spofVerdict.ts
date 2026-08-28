// SPDX-License-Identifier: Apache-2.0

// The SPOF verdict: how one `internal/failsim.Impact` reads to an operator.
//
// Framework-free so it is directly Vitest-able, and separate from the panel
// because this is the load-bearing judgement in the whole surface. The rule
// it enforces is the house style stated in planning/tasks/phase-30.md's
// phase invariants — **a skipped or unknown state never renders as a healthy
// one** — expressed as the same four-verdict vocabulary the path simulator
// already uses (allow / deny / unreachable / indeterminate).
//
// The mapping, and why each arm is what it is:
//
//   critical      quorum, a management path, or a Ceph network is at risk.
//   degraded      guests lose their uplink, or SDN segments are stranded.
//   no-impact     ONLY when severity is "none" AND notEvaluated is empty —
//                 "none" is documented as "breaks nothing this simulator can
//                 see, and every dimension was actually evaluated", so an
//                 empty notEvaluated is a precondition, not a nicety.
//   indeterminate everything else: severity "info" (nothing known-broken but
//                 at least one dimension unassessed), and any severity value
//                 this client does not recognise.
//
// A `critical`/`degraded` verdict with a non-empty notEvaluated stays
// critical/degraded — the known breakage is real — but the panel still
// renders the unevaluated dimensions, because the verdict is then a floor on
// the damage rather than a measurement of it. `spofVerdictIsPartial` is that
// distinction.
import type { FailsimImpact } from "../api/types";

export type SpofVerdict = "critical" | "degraded" | "no-impact" | "indeterminate";

/** The four known `internal/failsim` severities. Anything else on the wire
 * is treated as unknown rather than coerced into one of these. */
const KNOWN_SEVERITIES = new Set(["none", "info", "warning", "critical"]);

/** Human-readable name for each of the four `notEvaluated` dimension codes
 * (docs/api.md's Failure-impact simulation section). An unrecognised code
 * renders verbatim rather than being dropped — a dimension nobody can name
 * is still a dimension nobody evaluated. */
const DIMENSION_LABELS: Readonly<Record<string, string>> = {
  quorum: "corosync quorum",
  ceph: "Ceph networks",
  tunnels: "WireGuard tunnels",
  "guest-connectivity": "guest connectivity",
};

export function spofVerdict(impact: FailsimImpact): SpofVerdict {
  if (!KNOWN_SEVERITIES.has(impact.severity)) {
    return "indeterminate";
  }
  switch (impact.severity) {
    case "critical":
      return "critical";
    case "warning":
      return "degraded";
    case "none":
      // "none" already implies an empty notEvaluated server-side; the check
      // is here anyway so a future producer that emits both cannot quietly
      // turn an unknown into a clean bill of health.
      return impact.notEvaluated.length === 0 ? "no-impact" : "indeterminate";
    default:
      // "info": nothing known-broken, at least one dimension unassessed.
      return "indeterminate";
  }
}

/** True when the verdict names real damage but at least one dimension was
 * not assessed — i.e. the verdict is a floor, not a measurement. */
export function spofVerdictIsPartial(impact: FailsimImpact): boolean {
  const verdict = spofVerdict(impact);
  return (verdict === "critical" || verdict === "degraded") && impact.notEvaluated.length > 0;
}

export const SPOF_VERDICT_LABEL: Readonly<Record<SpofVerdict, string>> = {
  critical: "Critical",
  degraded: "Degrades connectivity",
  "no-impact": "No known impact",
  indeterminate: "Indeterminate",
};

/** Why the verdict says what it says, in one sentence. The indeterminate
 * arm never claims safety. */
export function spofVerdictExplanation(impact: FailsimImpact): string {
  switch (spofVerdict(impact)) {
    case "critical":
      return "Removing this element puts quorum, a management path, or a Ceph network at risk.";
    case "degraded":
      return "Removing this element leaves guests without an uplink, or strands SDN segments.";
    case "no-impact":
      return "Every dimension was evaluated and none of them breaks when this element is removed.";
    default:
      return impact.notEvaluated.length > 0
        ? `Not enough information to decide: ${describeDimensions(impact.notEvaluated)} could not be evaluated.`
        : "Not enough information to decide: the daemon reported a result this version does not recognise.";
  }
}

/** Renders `notEvaluated` codes as a readable list. */
export function describeDimensions(codes: readonly string[]): string {
  return codes.map((c) => DIMENSION_LABELS[c] ?? c).join(", ");
}

/** Every entity ref this impact names — the map links the panel renders.
 * Deduped and ordered disconnected-guests, stranded-VLANs, so the two
 * categories stay distinguishable by position as well as by label.
 * `mgmtPathLoss` is deliberately excluded: it is a list of node *names*, not
 * Ref strings, so it cannot be linked to the map the same way. */
export function spofAffectedRefs(impact: FailsimImpact): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const ref of [...impact.disconnectedGuests, ...impact.strandedVlans]) {
    if (!seen.has(ref)) {
      seen.add(ref);
      out.push(ref);
    }
  }
  return out;
}
