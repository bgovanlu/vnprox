// SPDX-License-Identifier: Apache-2.0

// T-3002: the policy-as-code verdict, rendered where it blocks.
//
// Everything on screen here comes from `GET /policies` or
// `POST /policies/test` — the rule id, its description, its severity, its
// assertions — plus the change engine's own `policy.*` finding messages,
// quoted verbatim. This panel composes no reason of its own and parses no
// message to obtain one; see policyVerdict.ts for why that constraint shapes
// the data model as much as the copy.
import type { PolicyVerdict, RuleVerdict } from "./policyVerdict";
import { conditionText } from "./policyVerdict";
import { HelpAnchor } from "../help/HelpAnchor";
import type { Finding } from "../api/types";

export interface PolicyVerdictPanelProps {
  verdict: PolicyVerdict;
  /** The `policy.violation` / `policy.invalid` findings already carried on the
   * changeset — the change engine's own words about the same refusal. */
  findings: readonly Finding[];
}

const SEVERITY_LABEL: Record<RuleVerdict["severity"], string> = {
  deny: "deny — blocks apply",
  warn: "warn — annotates only",
  unknown: "unrecognised severity",
};

function RuleCard({ rule }: { rule: RuleVerdict }) {
  const blocking = rule.severity !== "warn";
  return (
    <li
      className={
        blocking
          ? "rounded border border-red-300 p-2 dark:border-red-700"
          : "rounded border border-amber-300 p-2 dark:border-amber-700"
      }
      data-testid={`policy-rule-${rule.ruleId}`}
    >
      <p className="font-medium">
        <span className="font-mono">{rule.ruleId}</span>{" "}
        <span className="font-normal">
          (
          {rule.severity === "unknown"
            ? `${SEVERITY_LABEL.unknown}: ${rule.rawSeverity}`
            : SEVERITY_LABEL[rule.severity]}
          )
        </span>
      </p>
      {rule.description !== "" && <p className="mt-0.5">{rule.description}</p>}
      <p className="mt-0.5">
        Violated by {rule.violatingOpCount} of the operations the daemon evaluated for this changeset.
      </p>

      {/* The three cases are genuinely different and are never collapsed:
          assertions we read, a rule that has none (its match IS the
          violation), and a rule we could not read at all. */}
      {rule.assertKnown && rule.assert.length > 0 && (
        <div className="mt-1">
          <p className="font-medium">Its assertions, all of which had to hold:</p>
          <ul className="mt-0.5 list-disc pl-4 font-mono">
            {rule.assert.map((cond, i) => (
              <li key={i}>{conditionText(cond)}</li>
            ))}
          </ul>
        </div>
      )}
      {rule.assertKnown && rule.assert.length === 0 && (
        <p className="mt-1">
          This rule asserts nothing: matching it is itself the violation, so there is no condition to satisfy — the
          operation has to change, or the rule does.
        </p>
      )}
      {!rule.assertKnown && (
        <p className="mt-1" data-testid={`policy-assert-unknown-${rule.ruleId}`}>
          Its assertions could not be read: the installed rule set has no rule with this id, so it may have been
          replaced since this changeset was evaluated. That is not the same as a rule with no assertions.
        </p>
      )}
    </li>
  );
}

export function PolicyVerdictPanel({ verdict, findings }: PolicyVerdictPanelProps) {
  return (
    <section
      className="mt-3 rounded-md border border-border p-3 text-xs"
      aria-label="Policy verdict"
      data-testid="policy-verdict-panel"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-medium text-fg-subtle">Policy</h3>
        <HelpAnchor topic="policy-verdict" />
      </div>

      {verdict.kind === "loading" && (
        <p className="mt-1 text-fg-subtle">Asking the cluster's policy set about this change…</p>
      )}

      {verdict.kind === "unreadable" && (
        <p className="mt-1 text-fg-body" role="status">
          The policy set could not be read, so what it says about this change is unknown — not that nothing applies.
          The daemon said: {verdict.message}
        </p>
      )}

      {verdict.kind === "not-configured" && (
        <p className="mt-1 text-fg-subtle">
          This daemon has no policy store wired, so there is no rule set to administer. Changesets are still validated
          against an empty set, which produces no policy findings. The daemon said: {verdict.message}
        </p>
      )}

      {verdict.kind === "none-installed" && (
        <p className="mt-1 text-fg-subtle">
          A policy store is wired and no rules are installed, so nothing in this changeset is guarded by
          policy-as-code.
        </p>
      )}

      {verdict.kind === "clean" && (
        <p className="mt-1 text-fg-subtle">
          All {verdict.ruleCount} installed rule{verdict.ruleCount === 1 ? "" : "s"} were evaluated against this
          changeset and none of them objects to it.
        </p>
      )}

      {verdict.kind === "violations" && (
        <div className="mt-1 text-fg-body">
          <p className="font-medium">
            {verdict.rules.some((r) => r.severity !== "warn")
              ? "Refused by the cluster's installed policy:"
              : "Flagged by the cluster's installed policy:"}
          </p>
          <ul className="mt-1 space-y-2">
            {verdict.rules.map((rule) => (
              <RuleCard key={rule.ruleId} rule={rule} />
            ))}
          </ul>
        </div>
      )}

      {findings.length > 0 && (
        <div className="mt-2 text-fg-body">
          <p className="font-medium">What the change engine itself reported:</p>
          <ul className="mt-0.5 list-disc pl-4">
            {findings.map((f, i) => (
              <li key={i}>
                {f.message}
                {f.ref !== undefined && f.ref !== "" && <span className="ml-1 font-mono">({f.ref})</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
