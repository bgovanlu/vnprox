// SPDX-License-Identifier: Apache-2.0

// T-1307's guided diagnosis ladder: the "Diagnose" map/inspector action's
// verdict page (docs/api.md's "Diagnosis" section; POST /diagnose). Runs
// config check → simulator, live probe, guest interior, conntrack, and
// (only when explicitly escalated) packet capture against one target, and
// renders one advisory verdict — never an auto-applied fix. `suggestedFixRef`
// (when present) always opens the SAME changeset-drawer review flow
// FindingsStreamPanel's own "fix" button uses (POST /findings/{id}/fix) —
// this page has no fix-computation path of its own.
//
// The ladder itself runs server-side in one synchronous POST /diagnose call
// (no per-step streaming/WS event exists for it) — the "step-by-step
// progress view" below is therefore a simple "running" placeholder list
// while the request is in flight, then the real per-step result once it
// resolves, rather than true incremental live updates. A future card could
// add a streaming variant; this is a disclosed simplification, not a gap
// in the ladder's own contract (see planning/reports/T-1307.md).
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { useToast } from "../components/Toast";
import { useChangesetDrawerStore } from "../changesets/store";
import { FINDINGS_QUERY_KEY } from "../findings/queries";
import { fixFinding } from "../api/findings";
import { postDiagnose } from "../api/diagnose";
import type { DiagnoseConfidence, DiagnoseResult, DiagnoseStep, DiagnoseStepStatus } from "../api/types";

/** Registration order T-1307's card fixes for the ladder — used only to
 * render the "running…" placeholder list before a real result exists; the
 * actual rendered order once a result lands always follows the server's
 * own `steps` array. */
const STEP_ORDER = ["config-check", "live-probe", "guest-interior", "conntrack", "capture"] as const;

const STEP_LABELS: Record<string, string> = {
  "config-check": "Config check (simulator)",
  "live-probe": "Live probe (verify-live)",
  "guest-interior": "Guest interior",
  conntrack: "Conntrack",
  capture: "Capture",
};

const STATUS_LABEL: Record<DiagnoseStepStatus, string> = {
  ran: "Ran",
  skipped: "Skipped",
  error: "Error",
};

function statusColor(status: DiagnoseStepStatus): string {
  switch (status) {
    case "ran":
      return "text-emerald-600 dark:text-emerald-400";
    case "error":
      return "text-red-600 dark:text-red-400";
    default:
      return "text-slate-400";
  }
}

function statusIcon(status: DiagnoseStepStatus): string {
  switch (status) {
    case "ran":
      return "✓";
    case "error":
      return "✕";
    default:
      return "–";
  }
}

function StepRow({ step }: { step: DiagnoseStep }) {
  const [expanded, setExpanded] = useState(false);
  const hasDetail = step.detail !== undefined;

  return (
    <li
      className="rounded-md border border-slate-200 dark:border-slate-700"
      data-testid={`diagnose-step-${step.name}`}
      data-status={step.status}
    >
      <button
        type="button"
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm disabled:cursor-default"
        onClick={() => {
          if (hasDetail) setExpanded((v) => !v);
        }}
        aria-expanded={expanded}
        disabled={!hasDetail}
      >
        <span className="flex items-center gap-2">
          <span aria-hidden="true" className={statusColor(step.status)}>
            {statusIcon(step.status)}
          </span>
          <span className="font-medium">{STEP_LABELS[step.name] ?? step.name}</span>
          <span className="text-xs text-slate-500 dark:text-slate-400">{STATUS_LABEL[step.status]}</span>
        </span>
        {hasDetail && (
          <span className="text-xs text-slate-600 dark:text-slate-400">{expanded ? "Hide detail" : "Show detail"}</span>
        )}
      </button>
      <p className="px-3 pb-2 text-sm text-slate-600 dark:text-slate-300">{step.summary}</p>
      {expanded && hasDetail && (
        <pre className="overflow-x-auto border-t border-slate-200 bg-slate-50 px-3 py-2 text-xs dark:border-slate-700 dark:bg-slate-950">
          {JSON.stringify(step.detail, null, 2)}
        </pre>
      )}
    </li>
  );
}

function verdictClassName(confidence: DiagnoseConfidence): string {
  const base = "rounded-md border px-3 py-2";
  switch (confidence) {
    case "high":
      return `${base} border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200`;
    case "low":
      return `${base} border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200`;
    case "none":
      return `${base} border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300`;
    default:
      return `${base} border-slate-300 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300`;
  }
}

export interface DiagnosisPageProps {
  /** Overrides the `ref` query param — lets a future embedding (e.g. an
   * inspector tab) drive this page's logic directly without a route
   * change. Falls back to `?ref=`, this feature's routed entry point
   * (`/diagnose?ref=...`, see pages/DiagnosePage.tsx). */
  targetRef?: string;
}

export function DiagnosisPage({ targetRef: targetRefProp }: DiagnosisPageProps = {}) {
  const [searchParams] = useSearchParams();
  const targetRef = targetRefProp ?? searchParams.get("ref") ?? "";
  const [escalate, setEscalate] = useState(false);
  const [result, setResult] = useState<DiagnoseResult | undefined>(undefined);
  const [fixingId, setFixingId] = useState<string | undefined>(undefined);
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const queryClient = useQueryClient();
  const { toast } = useToast();

  const diagnoseMutation = useMutation({
    mutationFn: () => postDiagnose({ targetRef, escalateToCapture: escalate }),
    onSuccess: (res: DiagnoseResult) => {
      setResult(res);
    },
    onError: () => {
      toast({ title: "Could not run diagnosis", variant: "error" });
    },
  });

  async function handleFix(id: string): Promise<void> {
    setFixingId(id);
    try {
      const changeset = await fixFinding(id);
      setActiveId(changeset.id);
      void queryClient.invalidateQueries({ queryKey: FINDINGS_QUERY_KEY });
      toast({ title: "Fixing changeset created", description: "Review it in the drawer before applying.", variant: "success" });
    } catch {
      toast({ title: "Could not create fixing changeset", variant: "error" });
    } finally {
      setFixingId(undefined);
    }
  }

  if (!targetRef) {
    return (
      <EmptyState
        title="No target selected"
        description="Open Diagnose from a guest or edge on the map, or from its inspector panel."
      />
    );
  }

  const suggestedFixRef = result?.verdict.suggestedFixRef;

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Diagnose"
        description={
          <>
            Runs config check (simulator) → live probe → guest interior → conntrack → capture against{" "}
            <code className="font-mono">{targetRef}</code> and produces one advisory verdict. Nothing here applies a
            fix automatically — a suggested fix always opens in the changeset drawer for your review first.
          </>
        }
      />

      <div className="flex items-center gap-3">
        <Button
          size="sm"
          onClick={() => {
            diagnoseMutation.mutate();
          }}
          disabled={diagnoseMutation.isPending}
        >
          {diagnoseMutation.isPending ? "Running…" : "Run diagnosis"}
        </Button>
        <label className="flex items-center gap-1.5 text-sm">
          <input
            type="checkbox"
            checked={escalate}
            onChange={(e) => {
              setEscalate(e.target.checked);
            }}
          />
          Escalate to packet capture
        </label>
      </div>
      <p className="text-xs text-slate-500 dark:text-slate-400">
        Packet capture requires the dedicated capture capability and reaches into the network — if this session
        doesn&apos;t hold it, the capture step is skipped with a stated reason rather than failing the whole run.
      </p>

      {diagnoseMutation.isPending && (
        <ul className="space-y-1" data-testid="diagnose-progress" aria-live="polite">
          {STEP_ORDER.map((name) => (
            <li key={name} className="text-sm text-slate-600 dark:text-slate-400">
              {STEP_LABELS[name]} — running…
            </li>
          ))}
        </ul>
      )}

      {result && (
        <div className="flex flex-col gap-3" data-testid="diagnose-result">
          <div role="status" className={verdictClassName(result.verdict.confidence)} data-testid="diagnose-verdict">
            <p className="text-sm font-medium">{result.verdict.summary}</p>
            <p className="text-xs uppercase tracking-wide opacity-80">Confidence: {result.verdict.confidence}</p>
            {suggestedFixRef && (
              <Button
                size="sm"
                variant="secondary"
                className="mt-2"
                disabled={fixingId === suggestedFixRef}
                onClick={() => {
                  void handleFix(suggestedFixRef);
                }}
              >
                {fixingId === suggestedFixRef ? "Creating…" : "Review suggested fix"}
              </Button>
            )}
          </div>

          <ul className="flex flex-col gap-2" data-testid="diagnose-steps">
            {result.steps.map((step) => (
              <StepRow key={step.name} step={step} />
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
