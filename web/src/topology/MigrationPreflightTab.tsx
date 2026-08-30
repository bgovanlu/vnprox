// SPDX-License-Identifier: Apache-2.0

// T-1507's migration pre-flight inspector tab: lets an operator, before
// triggering a live migration/evacuation in PVE itself, check whether the
// migration network has enough headroom for this guest's RAM size — the
// "don't saturate the corosync link on a Friday-night evacuation" check
// (docs/api.md's Migration planner section). Purely advisory and
// read-only: this tab has no "migrate" button of its own and never calls
// anything but `POST /migration/preflight` — vnprox never triggers or
// manages the actual migration, that stays a PVE action.
import { useState } from "react";
import { Button } from "../components/Button";
import { useMigrationPreflightMutation } from "./migrationQueries";
import type { MigrationAssessment, MigrationVerdict } from "../api/types";

export interface MigrationPreflightTabProps {
  /** The guest's own Ref string (guest:node:vmid). */
  entityRef: string;
}

const VERDICT_LABEL: Record<MigrationVerdict, string> = {
  ok: "OK — ample headroom",
  tight: "Tight — worth a second look",
  insufficient: "Insufficient — do not evacuate onto this link unattended",
};

function verdictClassName(verdict: MigrationVerdict): string {
  const base = "rounded-md border px-3 py-2";
  switch (verdict) {
    case "ok":
      return `${base} border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200`;
    case "tight":
      return `${base} border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200`;
    case "insufficient":
      return `${base} border-red-300 bg-red-50 text-red-900 dark:border-red-800 dark:bg-red-950/40 dark:text-red-200`;
  }
}

function formatTransferSec(sec: number): string {
  if (sec < 0) return "unknown (no headroom to estimate from)";
  if (sec < 60) return `~${sec.toFixed(1)}s`;
  return `~${(sec / 60).toFixed(1)}min`;
}

function AssessmentResult({ result }: { result: MigrationAssessment }) {
  return (
    <div className="flex flex-col gap-2" data-testid="migration-preflight-result">
      <div role="status" className={verdictClassName(result.verdict)} data-testid="migration-preflight-verdict">
        <p className="text-sm font-medium">{VERDICT_LABEL[result.verdict]}</p>
        <p className="text-xs opacity-80">
          Headroom: {result.headroomMbps} Mbps · Estimated transfer: {formatTransferSec(result.estimatedTransferSec)}
        </p>
      </div>
      {result.bestEffort && (
        <p className="text-xs text-fg-subtle">
          Best-effort estimate: no live guest instrumentation backs this arc's dirty-page-rate figure.
        </p>
      )}
      {result.caveats.length > 0 && (
        <ul className="flex flex-col gap-1 text-xs text-fg-subtle" data-testid="migration-preflight-caveats">
          {result.caveats.map((c, i) => (
            <li key={i}>· {c}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function MigrationPreflightTab({ entityRef }: MigrationPreflightTabProps) {
  const [targetNode, setTargetNode] = useState("");
  const preflight = useMigrationPreflightMutation();

  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-xs text-fg-subtle">
        Advisory only: checks bandwidth headroom on the migration network against this guest&apos;s configured RAM
        size before you trigger a live migration or evacuation in PVE. Nothing here starts, stops, or manages a
        migration — vnprox never does that.
      </p>

      <div className="flex items-center gap-2">
        <label htmlFor="migration-preflight-target" className="text-xs text-fg-subtle">
          Target node
        </label>
        <input
          id="migration-preflight-target"
          type="text"
          className="w-32 rounded border border-border-strong bg-white px-2 py-1 text-xs dark:bg-slate-900"
          value={targetNode}
          placeholder="e.g. pve2"
          onChange={(e) => {
            setTargetNode(e.target.value);
          }}
        />
        <Button
          size="sm"
          disabled={targetNode.trim() === "" || preflight.isPending}
          onClick={() => {
            preflight.mutate({ guest: entityRef, targetNode: targetNode.trim() });
          }}
        >
          {preflight.isPending ? "Checking…" : "Check headroom"}
        </Button>
      </div>

      {preflight.isError && (
        <p className="text-xs text-red-600 dark:text-red-400" role="alert">
          Could not run the pre-flight check.
        </p>
      )}

      {preflight.data && <AssessmentResult result={preflight.data} />}
    </div>
  );
}
