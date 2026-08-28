// SPDX-License-Identifier: Apache-2.0

// T-2601's policy-as-code guardrail over the wire: `GET /policies`,
// `PUT /policies`, `POST /policies/test` (docs/api.md §Policies).
//
// Three things about this family are easy to get wrong from the client side,
// so they are stated once here:
//
//   1. **A changeset's `deny` finding carries no structured rule id.** The
//      validate stage renders a violation as a plain `policy.violation`
//      Finding whose `message` embeds the rule id and the failed assertion as
//      prose (internal/change/policy_eval.go's policyFinding). There is no
//      `ruleId` field on the wire. So "which rule denied this, and what did it
//      assert" is answered by joining `POST /policies/test` (which rules the
//      installed set says the changeset violates) with `GET /policies` (each
//      rule's own `match`/`assert`) — never by parsing the message string.
//   2. **`POST /policies/test` with no `policy` means "the installed one"**
//      (internal/change.EvaluatePolicySet falls back to the stored set when
//      the supplied one is empty), and it stages, installs and mutates
//      nothing. It is a `netRead` route for that reason.
//   3. `severity` is a closed set server-side (`deny`|`warn`) but versioned
//      independently of this client, so it is typed as a plain string and
//      classified at runtime. A severity this build does not recognise must
//      render as unrecognised, never as the harmless one.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Finding, Op } from "./types";

/** One `{field, op, value}` triple in a rule's `match` or `assert`.
 * `value` is a JSON literal (or a list, for `in`/`notIn`), and is absent
 * entirely for the arity-none operators (`exists`/`notExists`). */
export interface PolicyCondition {
  field: string;
  op: string;
  value?: unknown;
}

/** One organisational rule. `assert` is optional and its absence is
 * meaningful: an assert-less rule means "the match itself is the violation"
 * (internal/change.PolicyRule's own doc comment), NOT a vacuously satisfied
 * assertion. */
export interface PolicyRule {
  id: string;
  description: string;
  severity: string;
  tags?: string[];
  match: PolicyCondition[];
  assert?: PolicyCondition[];
  /** T-4006: the IANA zone (e.g. "America/New_York") a rule's local-wall-
   * clock `time.*` facts (`time.weekday`, `time.minuteOfDay`, `time.date`,
   * `time.dayOfMonth`, `time.month`) are resolved in. Absent/empty unless
   * the rule uses one of those — a rule using only the zone-free `time.now`
   * needs none. */
  zone?: string;
}

/** A whole policy document. `rules` is `null` on the wire (never `[]`)
 * whenever a cluster has no installed policy set — internal/change.PolicySet's
 * Go zero value leaves its `Rules` slice nil, and a nil slice marshals to
 * JSON `null` (T-3204: found via a crash reproducing on every review dialog
 * and the Policies panel for a cluster with no configured policy — the
 * common/default case). Every reader of a *decoded* `PolicySet.rules` must
 * guard it (`?? []`); a `PolicySet` the CLIENT constructs to send in a PUT
 * body should still always populate a real array. */
export interface PolicySet {
  version: number;
  rules: PolicyRule[] | null;
}

/** One rule's runtime bookkeeping. `probablyMisconfigured` is a report, never
 * a refusal: the rule has been through enough evaluations over a long enough
 * window without ever matching an op. */
export interface PolicyRuleStatus {
  ruleId: string;
  firstSeenAt: number;
  lastMatchedAt: number;
  evalCount: number;
  matchCount: number;
  probablyMisconfigured: boolean;
}

/** `GET`/`PUT /policies`. `rules` here is the STATISTICS array, one entry per
 * rule id — the rules themselves live under `set.rules`. Both are optional on
 * the wire (`omitempty` server-side), and an absent array is an absent answer
 * rather than an empty rule set. */
export interface PolicyStatus {
  set: PolicySet;
  revision: number;
  updatedBy?: string;
  updatedAt?: number;
  rules?: PolicyRuleStatus[];
}

/** One rule's outcome over one changeset. `matchedOps`/`violatingOps` are
 * indices into the ops the evaluation ran over, and both are omitted when
 * empty. */
export interface PolicyRuleResult {
  ruleId: string;
  description: string;
  severity: string;
  tags?: string[];
  matchedOps?: number[];
  violatingOps?: number[];
}

/** `POST /policies/test`'s response. */
export interface PolicyResult {
  findings?: Finding[];
  rules?: PolicyRuleResult[];
}

/** `POST /policies/test`'s body. Exactly one of `changesetId`/`ops` is
 * required — the server answers `400 validation_failed` for both or neither. */
export interface PolicyTestRequest {
  policy?: PolicySet;
  changesetId?: string;
  ops?: Op[];
}

/** GET /policies — `netRead`. Answers `503 policy_unavailable` on a daemon
 * built without a policy store; that is "you cannot administer what isn't
 * wired", never a fail-open on enforcement. */
export function fetchPolicies(): Promise<PolicyStatus> {
  return apiFetch<PolicyStatus>("/policies");
}

/** PUT /policies — `netWrite` + CSRF. Replaces the document wholesale; there
 * is no per-rule patch. A malformed set is refused `400 validation_failed`
 * with `details: {file, ruleId, field}` and nothing is stored or audited. */
export function putPolicies(set: PolicySet): Promise<PolicyStatus> {
  return apiFetch<PolicyStatus>("/policies", {
    method: "PUT",
    json: { version: set.version, rules: set.rules },
    csrfToken: readCsrfCookie(),
  });
}

/** POST /policies/test — `netRead`, stages nothing. Omit `policy` to evaluate
 * the INSTALLED set, which is what the review screen's deny panel does. */
export function testPolicy(req: PolicyTestRequest): Promise<PolicyResult> {
  return apiFetch<PolicyResult>("/policies/test", { method: "POST", json: req });
}

// --- T-4006: freeze windows & the change calendar --------------------------

/** One declared freeze window, rendered for the calendar (`GET /calendar`'s
 * `freezeWindows` entries) — the server's own best-effort reading of a
 * freeze-tagged `PolicyRule`'s `match` conditions (internal/change/
 * freeze_calendar.go). `recognized: false` means the rule used a `time.*`
 * shape this renderer does not understand; only `ruleId`/`description`/
 * `severity` are meaningful then, and the calendar must fall back to
 * listing it by description rather than drawing a box for it — never guess
 * a shape it cannot back up. THE POLICY RULE ITSELF, evaluated at validate
 * time, is still the sole enforcement; this is a read of that same data,
 * never a second opinion about it. */
export interface FreezeWindowView {
  ruleId: string;
  description: string;
  severity: string;
  zone?: string;
  recognized: boolean;
  weekdays?: string[];
  daysOfMonth?: number[];
  months?: number[];
  minuteOfDayStart?: number;
  minuteOfDayEnd?: number;
  epochStart?: number;
  epochEnd?: number;
}

/** One scheduled changeset (T-1103's `Schedule`, `internal/change/
 * schedule.go`), as `GET /calendar` renders it. Never carries a callback
 * token — that is delivered once, at schedule-creation time, by
 * `POST /changesets/{id}/schedule`'s own response, not here. */
export interface Schedule {
  changesetId: string;
  windowStart: number;
  windowEnd: number;
  confirmTimeoutSec: number;
  missedWindowPolicy: string;
  status: string;
  createdBy: string;
  createdAt: number;
  firedAt?: number;
  cancelledAt?: number;
}

/** `GET /calendar`'s whole response: every declared freeze window alongside
 * every currently-PENDING scheduled changeset — the one place an operator
 * sees why a staged apply would be refused, or why a schedule they already
 * set is about to be, before either happens. */
export interface CalendarView {
  freezeWindows: FreezeWindowView[];
  schedules: Schedule[] | null;
}

/** GET /calendar — `netRead`. */
export function fetchCalendar(): Promise<CalendarView> {
  return apiFetch<CalendarView>("/calendar");
}
