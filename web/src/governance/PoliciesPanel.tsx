// T-3002: the installed policy-as-code rule set — `GET`/`PUT /policies`.
//
// Before this panel, `vnproxctl policy` was the only client: a cluster could
// refuse an apply with `severity: deny` and an operator had no way to see
// which rules existed, let alone which of them had never matched anything.
//
// The editor is a JSON document rather than a per-rule form on purpose:
// `PUT /policies` replaces the document wholesale (there is no per-rule
// patch — "a rule set is reviewed and installed as a unit"), and a form that
// pretended otherwise would have to reconstruct the whole document from field
// state anyway. It is the same shape T-3001's spec panel uses for the same
// reason.
import { useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { classifySeverity, conditionText } from "../changesets/policyVerdict";
import { usePoliciesQuery } from "../changesets/governanceQueries";
import { useInstallPoliciesMutation } from "./queries";
import { ApiError } from "../api/client";
import type { PolicyRule, PolicyRuleStatus, PolicySet } from "../api/policies";

const SEVERITY_NOTE: Record<"deny" | "warn" | "unknown", string> = {
  deny: "blocks the apply",
  warn: "annotates the changeset",
  unknown: "an unrecognised severity — this build cannot say whether it blocks",
};

function parseDocument(raw: string): { set: PolicySet } | { error: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    return { error: err instanceof Error ? err.message : "the document is not valid JSON" };
  }
  if (typeof parsed !== "object" || parsed === null) {
    return { error: "the document must be a JSON object with a `rules` array" };
  }
  const obj: Record<string, unknown> = { ...(parsed as Record<string, unknown>) };
  const rules = obj.rules;
  if (!Array.isArray(rules)) {
    return { error: "the document must carry a `rules` array" };
  }
  const version = typeof obj.version === "number" ? obj.version : 1;
  // Deliberately not validated any further here. The server validates the
  // whole document before writing anything and answers `400 validation_failed`
  // with `details: {file, ruleId, field}` naming exactly what is wrong — a
  // second, weaker validator in the browser would only produce a different
  // opinion for the operator to reconcile.
  return { set: { version, rules: rules as PolicyRule[] } };
}

function StatRow({ stat }: { stat: PolicyRuleStatus }) {
  return (
    <p className="text-xs text-slate-500 dark:text-slate-400">
      {stat.evalCount === 0
        ? "Never evaluated yet."
        : `Evaluated ${String(stat.evalCount)} time${stat.evalCount === 1 ? "" : "s"}, matched ${String(stat.matchCount)}.`}
      {stat.probablyMisconfigured && (
        <span className="ml-1 font-medium text-amber-700 dark:text-amber-300">
          The daemon reports this rule has never matched an op over a long enough window to be worth checking — it
          guards nothing as written. That is a report, never a refusal.
        </span>
      )}
    </p>
  );
}

export function PoliciesPanel() {
  const query = usePoliciesQuery();
  const install = useInstallPoliciesMutation();
  const [draft, setDraft] = useState<string | undefined>(undefined);
  const [parseError, setParseError] = useState<string | undefined>(undefined);

  const status = query.data;
  const statsById = new Map<string, PolicyRuleStatus>((status?.rules ?? []).map((s) => [s.ruleId, s]));

  const notConfigured = query.error instanceof ApiError && query.error.code === "policy_unavailable";
  const unreadable = query.error !== null && !notConfigured;

  const documentText =
    draft ?? (status === undefined ? "" : JSON.stringify({ version: status.set.version, rules: status.set.rules }, null, 2));

  function handleInstall(): void {
    const parsed = parseDocument(documentText);
    if ("error" in parsed) {
      setParseError(parsed.error);
      return;
    }
    setParseError(undefined);
    install.mutate(parsed.set, {
      onSuccess: () => {
        setDraft(undefined);
      },
    });
  }

  return (
    <section aria-label="Policy rules" data-testid="policies-panel" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold">Policy as code</h2>
        <HelpAnchor topic="policies-panel" />
      </div>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        The cluster's declarative rule set. A rule matches operations and asserts something about them; a `deny` rule
        blocks the apply inside the change engine's validate stage, and a `warn` rule annotates the changeset. Nothing
        here is enforced by this screen — it is where the rules are read and replaced.
      </p>

      {query.isLoading && <p className="text-sm text-slate-500 dark:text-slate-400">Reading the installed rule set…</p>}

      {notConfigured && (
        <p className="text-sm text-slate-600 dark:text-slate-300">
          This daemon has no policy store wired, so there is no rule set to administer. Changesets still validate
          against an empty set, which produces no policy findings — this is not policy being bypassed. The daemon said:{" "}
          {query.error instanceof Error ? query.error.message : ""}
        </p>
      )}

      {unreadable && (
        <p className="text-sm text-slate-700 dark:text-slate-200" role="status">
          The rule set could not be read, so what is installed is unknown — not that nothing is. The daemon said:{" "}
          {query.error instanceof Error ? query.error.message : "the read failed"}
        </p>
      )}

      {status !== undefined && (
        <>
          <p className="text-xs text-slate-500 dark:text-slate-400">
            Store revision {String(status.revision)}
            {status.revision === 0 && " (nothing has ever been installed)"}
            {status.updatedBy !== undefined && status.updatedBy !== "" && ` · last installed by ${status.updatedBy}`}
            {status.updatedAt !== undefined && status.updatedAt !== 0
              ? ` on ${new Date(status.updatedAt * 1000).toLocaleString()}`
              : ""}
          </p>

          {status.set.rules.length === 0 ? (
            <p className="text-sm text-slate-500 dark:text-slate-400">
              No rules are installed, so policy-as-code guards nothing on this cluster today.
            </p>
          ) : (
            <ul className="flex flex-col gap-2" data-testid="policy-rule-list">
              {status.set.rules.map((rule) => {
                const severity = classifySeverity(rule.severity);
                const stat = statsById.get(rule.id);
                return (
                  <li key={rule.id} className="rounded-md border border-slate-200 p-3 dark:border-slate-800">
                    <p className="text-sm font-medium">
                      <span className="font-mono">{rule.id}</span>{" "}
                      <span className="font-normal text-slate-500 dark:text-slate-400">
                        — {rule.severity}: {SEVERITY_NOTE[severity]}
                      </span>
                    </p>
                    {rule.description !== "" && (
                      <p className="mt-0.5 text-sm text-slate-600 dark:text-slate-300">{rule.description}</p>
                    )}
                    {rule.tags !== undefined && rule.tags.length > 0 && (
                      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                        Tags: {rule.tags.join(", ")} — a protected class may be declared against any of these.
                      </p>
                    )}
                    <div className="mt-1 grid gap-2 sm:grid-cols-2">
                      <div>
                        <p className="text-xs font-medium">Matches operations where</p>
                        <ul className="mt-0.5 list-disc pl-4 font-mono text-xs">
                          {rule.match.map((cond, i) => (
                            <li key={i}>{conditionText(cond)}</li>
                          ))}
                        </ul>
                      </div>
                      <div>
                        <p className="text-xs font-medium">Asserts</p>
                        {rule.assert !== undefined && rule.assert.length > 0 ? (
                          <ul className="mt-0.5 list-disc pl-4 font-mono text-xs">
                            {rule.assert.map((cond, i) => (
                              <li key={i}>{conditionText(cond)}</li>
                            ))}
                          </ul>
                        ) : (
                          <p className="mt-0.5 text-xs text-slate-600 dark:text-slate-300">
                            Nothing — for this rule the match itself is the violation.
                          </p>
                        )}
                      </div>
                    </div>
                    {stat === undefined ? (
                      <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                        The daemon reported no statistics for this rule, so how often it has matched is unknown.
                      </p>
                    ) : (
                      <StatRow stat={stat} />
                    )}
                  </li>
                );
              })}
            </ul>
          )}

          <details className="rounded-md border border-slate-200 p-3 dark:border-slate-800">
            <summary className="cursor-pointer text-sm font-medium">Replace the rule set</summary>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              A whole document, installed as a unit — there is no per-rule patch. The daemon validates it before
              anything is written: a malformed set is refused and nothing is stored and nothing is audited.
              Re-installing an identical set is a no-op, with no new revision and no audit entry.
            </p>
            <textarea
              value={documentText}
              onChange={(e) => {
                setDraft(e.target.value);
              }}
              rows={12}
              aria-label="Policy document"
              spellCheck={false}
              className="mt-2 w-full rounded border border-slate-300 p-2 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
            />
            {parseError !== undefined && (
              <p className="mt-1 text-xs text-red-700 dark:text-red-300" role="alert">
                {parseError}
              </p>
            )}
            {install.error !== null && (
              <p className="mt-1 text-xs text-red-700 dark:text-red-300" role="alert" data-testid="policy-install-error">
                {install.error.message}
              </p>
            )}
            <div className="mt-2 flex gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setDraft(undefined);
                  setParseError(undefined);
                }}
              >
                Revert to installed
              </Button>
              <Button variant="primary" size="sm" disabled={install.isPending} onClick={handleInstall}>
                Install this rule set
              </Button>
            </div>
          </details>
        </>
      )}
    </section>
  );
}
