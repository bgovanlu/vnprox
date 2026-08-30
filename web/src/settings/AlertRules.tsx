// SPDX-License-Identifier: Apache-2.0

// T-1005's alert routing Settings page: CRUD over `alert_rules` (route
// findings/drift transitions to a webhook target) plus the delivery log
// (`GET /alert-deliveries`). Mirrors web/src/blueprints/BlueprintsPage.tsx's
// list+detail-panel layout — a list of rules on the left, the
// create/edit form for the selected rule on the right, the delivery log
// (optionally filtered to the selected rule) below.
import { useState } from "react";
import clsx from "clsx";
import type { AlertRule, AlertSourceFilterValue, AlertTargetKind, FindingSource, Severity } from "../api/types";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useToast } from "../components/Toast";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Tooltip } from "../components/Tooltip";
import { Button } from "../components/Button";
import {
  useAlertDeliveriesQuery,
  useAlertRulesQuery,
  useCreateAlertRuleMutation,
  useDeleteAlertRuleMutation,
  useTestAlertRuleMutation,
  useUpdateAlertRuleMutation,
} from "./alertRulesQueries";

const TARGET_KINDS: AlertTargetKind[] = ["generic", "gotify", "ntfy", "slack"];

// Debt sweep "found during the sweep, not yet carded" (2026-08-19): this used
// to be a 5-of-17 literal array (`["drift", "lldp", "ipam", "health",
// "probe"]`), so an operator could never route an alert on the other 12
// finding sources — the same defect `T-3004-followup-01` fixed in
// FindingsStreamPanel.tsx's `SOURCE_LABELS`, applied here.  Keyed off
// `Record<FindingSource, string>` (not a plain array) so that adding an
// 18th `internal/findings.Source` constant without adding its entry here is
// a `tsc` error, not a silently-missing checkbox — mirrors
// web/src/findings/FindingsStreamPanel.tsx's `SOURCE_LABELS` doc comment.
const SOURCE_LABELS: Record<FindingSource, string> = {
  drift: "Drift",
  lldp: "LLDP",
  ipam: "IPAM",
  health: "Health",
  probe: "Verify live",
  wireguard: "WireGuard",
  wan: "WAN",
  flow: "Flow",
  k8s: "Kubernetes",
  rogue: "Rogue",
  capacity: "Capacity",
  baseline: "Baseline",
  federation: "Federation",
  peer: "Peer",
  store: "Store",
  cert: "Certificates",
  gitsync: "Git sync",
};
const SOURCE_VALUES = Object.keys(SOURCE_LABELS) as AlertSourceFilterValue[];
const SEVERITY_VALUES: Severity[] = ["error", "warning", "info"];

interface FormState {
  name: string;
  enabled: boolean;
  sourceFilter: AlertSourceFilterValue[];
  severityFilter: Severity[];
  targetKind: AlertTargetKind;
  targetUrl: string;
  targetSecret: string;
  /** T-2407 delivery scheduling. Held as strings because they are text
   * inputs; converted at submit. */
  quietStart: string;
  quietEnd: string;
  quietTz: string;
  digestWindowMin: string;
  bypassQuietHoursOnError: boolean;
  /** Whether the user touched the secret field this session — controls
   * whether an empty value means "leave unchanged" (untouched, editing an
   * existing rule) or "no secret" (touched-and-cleared, or a brand-new
   * rule). */
  secretTouched: boolean;
}

const EMPTY_FORM: FormState = {
  name: "",
  enabled: true,
  sourceFilter: [],
  severityFilter: [],
  targetKind: "generic",
  targetUrl: "",
  targetSecret: "",
  quietStart: "",
  quietEnd: "",
  quietTz: "",
  digestWindowMin: "",
  bypassQuietHoursOnError: true,
  secretTouched: false,
};

function toFormState(rule: AlertRule): FormState {
  return {
    name: rule.name,
    enabled: rule.enabled,
    sourceFilter: rule.sourceFilter ?? [],
    severityFilter: rule.severityFilter ?? [],
    targetKind: rule.targetKind,
    targetUrl: rule.targetUrl,
    targetSecret: "",
    quietStart: rule.quietStart ?? "",
    quietEnd: rule.quietEnd ?? "",
    quietTz: rule.quietTz ?? "",
    digestWindowMin: rule.digestWindowSec > 0 ? String(Math.round(rule.digestWindowSec / 60)) : "",
    bypassQuietHoursOnError: rule.bypassQuietHoursOnError,
    secretTouched: false,
  };
}

/** Client-side mirror of internal/api/alertrules.go's validateAlertRuleRequest
 * — the server is the source of truth, this just avoids a round trip for
 * the obvious cases and gives the form's Save button something to disable
 * on. */
function validate(form: FormState): string | undefined {
  if (!form.name.trim()) return "Name is required.";
  try {
    const u = new URL(form.targetUrl);
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      return "Target URL must be http:// or https://.";
    }
  } catch {
    return "Target URL must be an absolute http(s) URL.";
  }
  // T-2407. Mirrors internal/findings' QuietHours.Validate: a window is either
  // fully set or fully unset, and a zero-length one is refused rather than
  // guessed at.
  const hasStart = form.quietStart.trim() !== "";
  const hasEnd = form.quietEnd.trim() !== "";
  if (hasStart !== hasEnd) return "Quiet hours needs both a start and an end.";
  if (hasStart && hasEnd) {
    if (!HHMM.test(form.quietStart.trim()) || !HHMM.test(form.quietEnd.trim())) {
      return "Quiet hours must be HH:MM (24-hour).";
    }
    if (form.quietStart.trim() === form.quietEnd.trim()) {
      return "Quiet hours start and end cannot be the same time.";
    }
  }
  if (form.digestWindowMin.trim() !== "") {
    const mins = Number(form.digestWindowMin);
    if (!Number.isFinite(mins) || mins < 0 || !Number.isInteger(mins)) {
      return "Digest window must be a whole number of minutes.";
    }
    if (mins > 24 * 60) return "Digest window must be at most 1440 minutes (24h).";
  }
  return undefined;
}

/** 24-hour HH:MM, both parts two digits — the same shape
 * internal/findings/quiethours.go's parseClock accepts. */
const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/;

function toggleValue<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
}

function FilterCheckboxGroup<T extends string>({
  label,
  options,
  selected,
  onChange,
  disabled,
  labels,
}: {
  label: string;
  options: T[];
  selected: T[];
  onChange: (next: T[]) => void;
  disabled?: boolean;
  /** Display label per option; falls back to the raw value when omitted
   * (severity's three values are self-explanatory as-is). */
  labels?: Record<T, string>;
}) {
  return (
    <fieldset className="flex flex-col gap-1">
      <legend className="text-xs font-medium text-fg-muted">{label}</legend>
      <div className="flex flex-wrap gap-2">
        {options.map((opt) => (
          <label key={opt} className="flex items-center gap-1 text-xs text-fg-muted">
            <input
              type="checkbox"
              disabled={disabled}
              checked={selected.includes(opt)}
              onChange={() => {
                onChange(toggleValue(selected, opt));
              }}
            />
            {labels ? labels[opt] : opt}
          </label>
        ))}
      </div>
      <p className="text-[11px] text-fg-muted">None selected matches every value.</p>
    </fieldset>
  );
}

export function AlertRules() {
  const { data: rulesData, isLoading, error, refetch } = useAlertRulesQuery();
  const { data: session } = useSession();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const { toast } = useToast();

  const createMutation = useCreateAlertRuleMutation();
  const updateMutation = useUpdateAlertRuleMutation();
  const deleteMutation = useDeleteAlertRuleMutation();
  const testMutation = useTestAlertRuleMutation();
  const { data: deliveriesData } = useAlertDeliveriesQuery({ ruleId: selectedId });

  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  const items = rulesData?.items ?? [];
  const selected = items.find((r) => r.id === selectedId);
  const editing = creating || selected !== undefined;

  function startCreate(): void {
    setCreating(true);
    setSelectedId(undefined);
    setForm(EMPTY_FORM);
  }

  function selectRule(rule: AlertRule): void {
    setCreating(false);
    setSelectedId(rule.id);
    setForm(toFormState(rule));
  }

  function cancelEdit(): void {
    setCreating(false);
    setForm(EMPTY_FORM);
  }

  const validationError = editing ? validate(form) : undefined;

  async function handleSave(): Promise<void> {
    if (validationError) return;
    const req = {
      name: form.name.trim(),
      enabled: form.enabled,
      sourceFilter: form.sourceFilter.length > 0 ? form.sourceFilter : undefined,
      severityFilter: form.severityFilter.length > 0 ? form.severityFilter : undefined,
      targetKind: form.targetKind,
      targetUrl: form.targetUrl.trim(),
      ...(form.secretTouched ? { targetSecret: form.targetSecret } : {}),
      quietStart: form.quietStart.trim() || undefined,
      quietEnd: form.quietEnd.trim() || undefined,
      quietTz: form.quietTz.trim() || undefined,
      digestWindowSec: form.digestWindowMin.trim() === "" ? 0 : Number(form.digestWindowMin) * 60,
      bypassQuietHoursOnError: form.bypassQuietHoursOnError,
    };
    try {
      if (creating) {
        const created = await createMutation.mutateAsync(req);
        setCreating(false);
        setSelectedId(created.id);
        setForm(toFormState(created));
        toast({ title: "Alert rule created", description: created.name, variant: "success" });
      } else if (selected) {
        const updated = await updateMutation.mutateAsync({ id: selected.id, req });
        setForm(toFormState(updated));
        toast({ title: "Alert rule saved", description: updated.name, variant: "success" });
      }
    } catch {
      toast({ title: "Could not save alert rule", variant: "error" });
    }
  }

  async function handleDelete(rule: AlertRule): Promise<void> {
    try {
      await deleteMutation.mutateAsync(rule.id);
      if (selectedId === rule.id) {
        setSelectedId(undefined);
        setForm(EMPTY_FORM);
      }
      toast({ title: "Alert rule deleted", variant: "success" });
    } catch {
      toast({ title: "Could not delete alert rule", variant: "error" });
    }
  }

  async function handleTest(rule: AlertRule): Promise<void> {
    try {
      const result = await testMutation.mutateAsync(rule.id);
      if (result.status === "delivered") {
        toast({ title: "Test delivered", description: rule.name, variant: "success" });
      } else {
        toast({ title: "Test delivery failed", description: result.error ?? rule.name, variant: "error" });
      }
    } catch {
      toast({ title: "Could not send test alert", variant: "error" });
    }
  }

  if (isLoading) {
    return <EmptyState title="Loading…" description="Fetching alert rules." />;
  }
  if (error) {
    return (
      <EmptyState
        variant="failed"
        title="Could not load alert rules"
        description="Check your connection and try again."
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-hidden p-4">
      <PageHeader
        title="Alert rules"
        description="Route findings and drift transitions to a webhook (generic JSON, Gotify, ntfy, or Slack), independent of PVE's
          own notification targets."
      />

      <div className="flex flex-1 gap-4 overflow-hidden">
        <div className="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-fg-body">Rules</h2>
            <Tooltip content={writeDisabledReason}>
              <span>
                <Button size="sm" variant="secondary" disabled={!canWrite} onClick={startCreate}>
                  New rule
                </Button>
              </span>
            </Tooltip>
          </div>

          {items.length === 0 ? (
            <p className="text-sm text-fg-muted">No alert rules configured yet.</p>
          ) : (
            <ul className="flex flex-col gap-1" data-testid="alert-rule-list">
              {items.map((rule) => (
                <li key={rule.id}>
                  <button
                    type="button"
                    className={clsx(
                      "flex w-full flex-col items-start rounded-md px-2 py-1.5 text-left text-sm",
                      rule.id === selectedId
                        ? "bg-accent-soft text-accent-fg"
                        : "hover:bg-slate-100 dark:hover:bg-slate-800",
                    )}
                    onClick={() => {
                      selectRule(rule);
                    }}
                  >
                    <span className="font-medium">{rule.name}</span>
                    <span className="text-[10px] uppercase tracking-wide text-fg-muted">
                      {rule.targetKind} · {rule.enabled ? "enabled" : "disabled"}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="flex-1 overflow-y-auto">
          {!editing ? (
            <EmptyState title="Select a rule" description="Pick one from the list, or create a new one." />
          ) : (
            <form
              className="flex max-w-xl flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                void handleSave();
              }}
            >
              <div>
                <label htmlFor="alert-rule-name" className="text-xs font-medium text-fg-muted">
                  Name
                </label>
                <input
                  id="alert-rule-name"
                  type="text"
                  className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                  value={form.name}
                  onChange={(e) => {
                    setForm({ ...form, name: e.target.value });
                  }}
                />
              </div>

              <label className="flex items-center gap-2 text-sm text-fg-body">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => {
                    setForm({ ...form, enabled: e.target.checked });
                  }}
                />
                Enabled
              </label>

              <FilterCheckboxGroup
                label="Source filter"
                options={SOURCE_VALUES}
                selected={form.sourceFilter}
                onChange={(next) => {
                  setForm({ ...form, sourceFilter: next });
                }}
                labels={SOURCE_LABELS}
              />
              <FilterCheckboxGroup
                label="Severity filter"
                options={SEVERITY_VALUES}
                selected={form.severityFilter}
                onChange={(next) => {
                  setForm({ ...form, severityFilter: next });
                }}
              />

              <fieldset className="rounded-md border border-border p-2">
                <legend className="px-1 text-xs font-medium text-fg-muted">Delivery schedule</legend>
                <p className="mb-2 text-xs text-fg-muted">
                  Quiet hours defer deliveries — they are never dropped, and go out when the window ends. A digest window
                  coalesces everything arriving inside it into one message.
                </p>
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label htmlFor="alert-rule-quiet-start" className="text-xs font-medium text-fg-muted">
                      Quiet from (HH:MM)
                    </label>
                    <input
                      id="alert-rule-quiet-start"
                      type="text"
                      placeholder="22:00"
                      className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                      value={form.quietStart}
                      onChange={(e) => {
                        setForm({ ...form, quietStart: e.target.value });
                      }}
                    />
                  </div>
                  <div>
                    <label htmlFor="alert-rule-quiet-end" className="text-xs font-medium text-fg-muted">
                      Quiet until (HH:MM)
                    </label>
                    <input
                      id="alert-rule-quiet-end"
                      type="text"
                      placeholder="06:00"
                      className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                      value={form.quietEnd}
                      onChange={(e) => {
                        setForm({ ...form, quietEnd: e.target.value });
                      }}
                    />
                  </div>
                  <div>
                    <label htmlFor="alert-rule-quiet-tz" className="text-xs font-medium text-fg-muted">
                      Time zone
                    </label>
                    <input
                      id="alert-rule-quiet-tz"
                      type="text"
                      placeholder="the daemon's own"
                      className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                      value={form.quietTz}
                      onChange={(e) => {
                        setForm({ ...form, quietTz: e.target.value });
                      }}
                    />
                  </div>
                  <div>
                    <label htmlFor="alert-rule-digest" className="text-xs font-medium text-fg-muted">
                      Digest window (minutes)
                    </label>
                    <input
                      id="alert-rule-digest"
                      type="text"
                      placeholder="0 — send each"
                      className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                      value={form.digestWindowMin}
                      onChange={(e) => {
                        setForm({ ...form, digestWindowMin: e.target.value });
                      }}
                    />
                  </div>
                </div>
                <label className="mt-2 flex items-center gap-2 text-sm text-fg-body">
                  <input
                    type="checkbox"
                    checked={form.bypassQuietHoursOnError}
                    onChange={(e) => {
                      setForm({ ...form, bypassQuietHoursOnError: e.target.checked });
                    }}
                  />
                  Deliver <span className="font-medium">error</span>-severity findings during quiet hours anyway
                </label>
              </fieldset>

              <div>
                <label htmlFor="alert-rule-target-kind" className="text-xs font-medium text-fg-muted">
                  Target kind
                </label>
                <select
                  id="alert-rule-target-kind"
                  className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                  value={form.targetKind}
                  onChange={(e) => {
                    setForm({ ...form, targetKind: e.target.value as AlertTargetKind });
                  }}
                >
                  {TARGET_KINDS.map((k) => (
                    <option key={k} value={k}>
                      {k}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label htmlFor="alert-rule-target-url" className="text-xs font-medium text-fg-muted">
                  Target URL
                </label>
                <input
                  id="alert-rule-target-url"
                  type="text"
                  placeholder="https://…"
                  className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                  value={form.targetUrl}
                  onChange={(e) => {
                    setForm({ ...form, targetUrl: e.target.value });
                  }}
                />
              </div>

              {form.targetKind !== "slack" && (
                <div>
                  <label htmlFor="alert-rule-target-secret" className="text-xs font-medium text-fg-muted">
                    Target secret {selected?.hasSecret && !form.secretTouched ? "(configured — leave blank to keep)" : ""}
                  </label>
                  <input
                    id="alert-rule-target-secret"
                    type="password"
                    className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
                    value={form.targetSecret}
                    onChange={(e) => {
                      setForm({ ...form, targetSecret: e.target.value, secretTouched: true });
                    }}
                  />
                </div>
              )}

              {validationError && <p className="text-xs text-red-600 dark:text-red-400">{validationError}</p>}

              <div className="flex flex-wrap gap-2">
                <Tooltip content={writeDisabledReason}>
                  <span>
                    <Button type="submit" variant="primary" disabled={!canWrite || !!validationError}>
                      {creating ? "Create" : "Save"}
                    </Button>
                  </span>
                </Tooltip>
                <Button type="button" variant="secondary" onClick={cancelEdit}>
                  Cancel
                </Button>
                {selected && (
                  <>
                    <Tooltip content={writeDisabledReason}>
                      <span>
                        <Button
                          type="button"
                          variant="secondary"
                          disabled={!canWrite || testMutation.isPending}
                          onClick={() => {
                            void handleTest(selected);
                          }}
                        >
                          Test
                        </Button>
                      </span>
                    </Tooltip>
                    <Tooltip content={writeDisabledReason}>
                      <span>
                        <Button
                          type="button"
                          variant="destructive"
                          disabled={!canWrite}
                          onClick={() => {
                            void handleDelete(selected);
                          }}
                        >
                          Delete
                        </Button>
                      </span>
                    </Tooltip>
                  </>
                )}
              </div>
            </form>
          )}
        </div>
      </div>

      <section className="shrink-0 border-t border-border pt-3">
        <h2 className="mb-2 text-sm font-semibold text-fg-body">
          Delivery log{selected ? ` — ${selected.name}` : ""}
        </h2>
        {!deliveriesData || deliveriesData.items.length === 0 ? (
          <p className="text-sm text-fg-muted">No deliveries logged yet.</p>
        ) : (
          <div className="max-h-48 overflow-y-auto">
            <table className="w-full text-left text-xs" data-testid="delivery-log">
              <thead className="text-fg-muted">
                <tr>
                  <th className="py-1 pr-2">At</th>
                  <th className="py-1 pr-2">Rule</th>
                  <th className="py-1 pr-2">Attempt</th>
                  <th className="py-1 pr-2">Status</th>
                  <th className="py-1 pr-2">Error</th>
                </tr>
              </thead>
              <tbody>
                {deliveriesData.items.map((d) => (
                  <tr key={d.id} className="border-t border-slate-100 dark:border-slate-800">
                    <td className="py-1 pr-2">{new Date(d.at * 1000).toLocaleString()}</td>
                    <td className="py-1 pr-2">{d.ruleId}</td>
                    <td className="py-1 pr-2">{d.attempt}</td>
                    <td className="py-1 pr-2">{d.status}</td>
                    <td className="py-1 pr-2 text-red-600 dark:text-red-400">{d.error ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
