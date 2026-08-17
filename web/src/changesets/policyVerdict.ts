// T-3002: what the cluster's installed policy set says about THIS changeset,
// kept out of the component so the properties that matter are directly
// testable.
//
// Why this exists at all. When a `deny` rule fires, the change engine folds
// it into the changeset as an ordinary `policy.violation` Finding whose
// `message` is prose — `policy rule "no-flat-vlan": <description> (op
// iface.update: failed assertion params.vlan exists)`. There is no `ruleId`
// and no `assert` anywhere on that wire shape. Today's review screen renders
// that string in its generic "Blocking errors" list, which is exactly the
// "generic validation error" the card says is not good enough.
//
// So the rule id and its assertions are obtained from the daemon, structurally,
// by joining two routes it already serves:
//
//   POST /policies/test  {changesetId}   -> which INSTALLED rules this
//                                           changeset violates, by rule id
//   GET  /policies                       -> each installed rule's own
//                                           match / assert / severity
//
// Nothing here parses a finding message, and nothing here composes a reason
// of its own: every word rendered beside a rule comes from one of those two
// responses.
//
// Three unknowns are modelled rather than flattened, because each of them has
// a definite-looking neighbour it must not be confused with:
//
//   * a rule id in the evaluation that is absent from the installed set —
//     `assertKnown: false`. It is NOT the same as a rule with no assertions,
//     which means "the match itself is the violation" (internal/change's
//     PolicyRule doc comment) and is a stronger statement, not a weaker one.
//   * a severity outside {deny, warn} — classified `unknown`, never folded
//     into `warn`. The server's set is closed but versioned independently of
//     this client.
//   * "we could not ask" vs "nothing is installed" vs "this daemon has no
//     policy store" — three different states with three different messages.
import { ApiError } from "../api/client";
import type { Finding } from "../api/types";
import type { PolicyCondition, PolicyResult, PolicyRule, PolicyStatus } from "../api/policies";

/** A rule severity as this build understands it. `unknown` is a real member:
 * a value the daemon introduced later must render as unrecognised rather than
 * as the harmless one. */
export type VerdictSeverity = "deny" | "warn" | "unknown";

export function classifySeverity(raw: string): VerdictSeverity {
  if (raw === "deny") return "deny";
  if (raw === "warn") return "warn";
  return "unknown";
}

/** One rule that this changeset violates, with everything the daemon said
 * about it and nothing else. */
export interface RuleVerdict {
  ruleId: string;
  /** The rule's own description, from the evaluation. Empty when the rule
   * carries none — rendered as absent, never substituted for. */
  description: string;
  severity: VerdictSeverity;
  /** Verbatim `severity` string, so an unrecognised one can be shown as the
   * literal the daemon sent. */
  rawSeverity: string;
  /** The rule's assertions, from the installed set. */
  assert: readonly PolicyCondition[];
  /** False when the installed set has no rule with this id, so the
   * assertions could not be read at all. An empty `assert` with
   * `assertKnown: true` means the rule genuinely has none — the match itself
   * is the violation. */
  assertKnown: boolean;
  /** How many of the daemon's EVALUATED operations violated the rule.
   * Deliberately a count and not a list of the changeset's own ops: the
   * indices in `violatingOps` are into the expanded op list the evaluator ran
   * over (`expandRawReplaceOps`), which is not the changeset's `ops` array
   * whenever a raw-replace op is present. Indexing one with the other would
   * name the wrong operation. */
  violatingOpCount: number;
}

export type PolicyVerdict =
  | { kind: "loading" }
  /** We could not ask. Never "no policy applies" — telling an operator their
   * guardrails are silent at the moment they cannot be read is the failure
   * this whole arc keeps finding. */
  | { kind: "unreadable"; message: string }
  /** `503 policy_unavailable`: this daemon was built with no policy store.
   * Evaluation still runs (against an empty set), so this is "you cannot
   * administer what isn't wired", not a fail-open. */
  | { kind: "not-configured"; message: string }
  /** A policy store exists and holds no rules. */
  | { kind: "none-installed" }
  /** Rules are installed and none of them objects to this changeset. */
  | { kind: "clean"; ruleCount: number }
  | { kind: "violations"; rules: RuleVerdict[] };

/** True when at least one violated rule blocks the apply — a `deny`, or a
 * severity this build cannot classify (which must be treated as at least as
 * serious as the strictest one it knows, never as a warning). */
export function verdictBlocks(verdict: PolicyVerdict): boolean {
  return verdict.kind === "violations" && verdict.rules.some((r) => r.severity !== "warn");
}

function ruleById(status: PolicyStatus | undefined): Map<string, PolicyRule> {
  const map = new Map<string, PolicyRule>();
  for (const rule of status?.set.rules ?? []) {
    map.set(rule.id, rule);
  }
  return map;
}

function messageOf(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message !== "") return error.message;
  return fallback;
}

export interface PolicyVerdictInput {
  status: PolicyStatus | undefined;
  statusError: unknown;
  result: PolicyResult | undefined;
  resultError: unknown;
  isLoading: boolean;
}

/** Classifies one changeset's policy standing from the two reads.
 *
 * Order matters: an error on either read wins over a stale-but-present body
 * from the other, because a set we could not refresh is not evidence of what
 * is currently installed. */
export function policyVerdict(input: PolicyVerdictInput): PolicyVerdict {
  const { status, statusError, result, resultError, isLoading } = input;

  for (const err of [statusError, resultError]) {
    if (err === null || err === undefined) continue;
    if (err instanceof ApiError && err.code === "policy_unavailable") {
      return { kind: "not-configured", message: err.message };
    }
    return { kind: "unreadable", message: messageOf(err, "the policy read failed") };
  }

  if (isLoading || status === undefined || result === undefined) {
    return { kind: "loading" };
  }

  // status.set.rules is a Go nil slice ([]PolicyRule) whenever a cluster has
  // no installed policy set (internal/change.PolicySet's documented "zero
  // value is a valid, empty set" — internal/change/policy_service.go's
  // PolicyStatus never re-initializes it) — that marshals as JSON `null`,
  // not `[]` (T-3204: found via a crash in ReviewApplyScreen, "Cannot read
  // properties of null (reading 'length')", on every changeset review for a
  // cluster with no configured policy — the common case). `result.rules ??
  // []` below already guards the analogous field; this is the same
  // established convention, applied here too.
  const installed = status.set.rules ?? [];
  if (installed.length === 0) {
    return { kind: "none-installed" };
  }

  const byId = ruleById(status);
  const violated: RuleVerdict[] = [];
  for (const rr of result.rules ?? []) {
    const violating = rr.violatingOps ?? [];
    if (violating.length === 0) continue;
    const installedRule = byId.get(rr.ruleId);
    violated.push({
      ruleId: rr.ruleId,
      description: rr.description,
      severity: classifySeverity(rr.severity),
      rawSeverity: rr.severity,
      assert: installedRule?.assert ?? [],
      assertKnown: installedRule !== undefined,
      violatingOpCount: violating.length,
    });
  }

  if (violated.length === 0) {
    return { kind: "clean", ruleCount: installed.length };
  }
  // Deny first, then anything unrecognised, then warn: the operator reads the
  // thing that is stopping them before the thing that is not.
  const rank: Record<VerdictSeverity, number> = { deny: 0, unknown: 1, warn: 2 };
  violated.sort((a, b) => rank[a.severity] - rank[b.severity]);
  return { kind: "violations", rules: violated };
}

/** One condition rendered as the daemon expresses it: `field op value`.
 * `exists`/`notExists` carry no value and must not render an empty one. */
export function conditionText(cond: PolicyCondition): string {
  if (cond.value === undefined || cond.value === null) {
    return `${cond.field} ${cond.op}`;
  }
  return `${cond.field} ${cond.op} ${JSON.stringify(cond.value)}`;
}

/** The `policy.violation` findings already on the changeset — the daemon's
 * own words about the same refusal, shown beside the rule so the panel adds
 * structure without replacing the message the engine actually produced. */
export function policyFindings(findings: readonly Finding[]): Finding[] {
  return findings.filter((f) => f.code === "policy.violation" || f.code === "policy.invalid");
}
