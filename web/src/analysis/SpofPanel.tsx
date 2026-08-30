// SPDX-License-Identifier: Apache-2.0

// Failure simulation: "what breaks if this NIC / bond / switch dies?"
// (GET /failsim/spof-score, T-1604).
//
// Read-only forever. The route removes an entity from a *copy* of the live
// inventory snapshot and recomputes connectivity — it never induces a
// failure and there is no `failsim.*` changeset op, so this panel has no
// action of any kind beyond navigating to the entities it names.
//
// The verdict logic lives in spofVerdict.ts and is unit-tested there. The
// one property this component must not lose: an impact the simulator could
// not decide renders as **Indeterminate**, never as "no impact" — see that
// module's doc comment for the mapping and phase-30's invariant for why.
import clsx from "clsx";
import { useNavigate } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import type { FailsimImpact, SpofEntry } from "../api/types";
import { blastRadiusRequestFromFailsimImpact } from "../topology/blastRadiusFocus";
import { useTopologyStore } from "../topology/store";
import { MapLink } from "./MapLink";
import { useSpofScoreQuery } from "./analysisQueries";
import {
  SPOF_VERDICT_LABEL,
  describeDimensions,
  spofAffectedRefs,
  spofVerdict,
  spofVerdictExplanation,
  spofVerdictIsPartial,
  type SpofVerdict,
} from "./spofVerdict";

/** Indeterminate borrows the path simulator's violet (ResultPanel.tsx's
 * VERDICT_BANNER_CLASS) on purpose: the two surfaces mean the same thing by
 * it, and an operator who has learned one should not have to learn the
 * other. */
const VERDICT_CLASS: Readonly<Record<SpofVerdict, string>> = {
  critical: "border-red-300 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-100",
  degraded: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100",
  "no-impact":
    "border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-100",
  indeterminate:
    "border-violet-300 bg-violet-50 text-violet-900 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-100",
};

/** Ordering: worst first, and indeterminate immediately after critical
 * rather than last — an impact nobody could evaluate is not a mild one. */
const VERDICT_RANK: Readonly<Record<SpofVerdict, number>> = {
  critical: 0,
  indeterminate: 1,
  degraded: 2,
  "no-impact": 3,
};

function sortEntries(entries: readonly SpofEntry[]): SpofEntry[] {
  return [...entries].sort(
    (a, b) => VERDICT_RANK[spofVerdict(a.impact)] - VERDICT_RANK[spofVerdict(b.impact)] || a.ref.localeCompare(b.ref),
  );
}

export function SpofPanel() {
  const { data, isLoading, error, refetch } = useSpofScoreQuery();

  return (
    <section aria-labelledby="spof-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="spof-heading" className="flex items-center gap-2 text-lg font-semibold">
          Failure simulation
          <HelpAnchor topic="spof-score" />
        </h2>
        <p className="text-sm text-fg-muted">
          What breaks if one element dies. Each entry is a simulated removal against a copy of the current inventory —
          nothing here induces a failure, changes anything, or is stored. An impact that could not be decided says so
          rather than reporting a clean result.
        </p>
      </div>

      {isLoading && <p className="text-sm text-fg-muted">Simulating…</p>}
      {error && (
        <EmptyState
          icon="node"
          variant="failed"
          title="Could not compute the SPOF score"
          description="The daemon could not build a simulation over the current inventory snapshot. Try again in a moment."
          density="compact"
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !error && data && (
        <>
          <ResilienceScore score={data.score} spofCount={data.entries.length} generatedAt={data.generatedAt} />
          {data.entries.length === 0 ? (
            <EmptyState
              icon="node"
              variant="empty"
              title="No single points of failure found"
              description="Every element the simulator enumerated is redundant enough that removing it breaks nothing it could see. Elements with a known impact of zero are excluded server-side, so this is a result, not an empty read."
              density="compact"
            />
          ) : (
            <ul className="flex flex-col gap-2" aria-label="Single points of failure">
              {sortEntries(data.entries).map((entry) => (
                <SpofEntryCard key={entry.ref} entry={entry} />
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function ResilienceScore({
  score,
  spofCount,
  generatedAt,
}: {
  score: number;
  spofCount: number;
  generatedAt: string;
}) {
  return (
    <div className="flex flex-wrap items-baseline gap-3 rounded-md border border-border bg-white px-3 py-2 dark:bg-slate-900">
      <span className="text-2xl font-semibold tabular-nums">{score}</span>
      <span className="text-xs uppercase tracking-wide text-fg-muted">resilience score</span>
      <span className="text-sm text-fg-muted">
        {spofCount} single point{spofCount === 1 ? "" : "s"} of failure
      </span>
      <span className="ml-auto text-xs text-fg-muted">Simulated {generatedAt}</span>
    </div>
  );
}

function SpofEntryCard({ entry }: { entry: SpofEntry }) {
  const verdict = spofVerdict(entry.impact);
  const affected = spofAffectedRefs(entry.impact);
  const navigate = useNavigate();
  const setBlastRadiusRequest = useTopologyStore((s) => s.setBlastRadiusRequest);
  return (
    <li className="rounded-lg border border-border bg-white p-3 dark:bg-slate-900">
      <div className="flex flex-wrap items-center gap-2">
        <MapLink entityRef={entry.ref} />
        <span
          data-testid="spof-verdict"
          className={clsx("rounded border px-2 py-0.5 text-xs font-semibold", VERDICT_CLASS[verdict])}
        >
          {SPOF_VERDICT_LABEL[verdict]}
        </span>
        {spofVerdictIsPartial(entry.impact) && (
          <span className="rounded border border-violet-300 bg-violet-50 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-900 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-100">
            Partly unevaluated
          </span>
        )}
        {/* T-3912: collapses the topology map to this simulated removal's
         * blast radius — the target plus every disconnected guest/stranded
         * VLAN/lost management path, and the map path connecting them. */}
        <Button
          size="sm"
          variant="secondary"
          className="ml-auto"
          onClick={() => {
            setBlastRadiusRequest(blastRadiusRequestFromFailsimImpact(entry.impact));
            void navigate("/topology");
          }}
        >
          Show blast radius
        </Button>
      </div>
      <p className="mt-1 text-sm text-fg-muted">{spofVerdictExplanation(entry.impact)}</p>

      {affected.length > 0 && (
        <div className="mt-2">
          <h3 className="text-xs font-medium uppercase tracking-wide text-fg-muted">
            Affected entities
          </h3>
          <ul className="mt-1 flex flex-wrap gap-x-3 gap-y-1">
            {affected.map((ref) => (
              <li key={ref}>
                <MapLink entityRef={ref} />
              </li>
            ))}
          </ul>
        </div>
      )}

      <RiskFlags impact={entry.impact} />
      <NotEvaluatedList impact={entry.impact} />
    </li>
  );
}

/** Quorum/Ceph/management-path risk. Each is shown only when the dimension
 * was actually evaluated: a `quorumRisk: false` alongside `quorum` in
 * notEvaluated means "not checked", not "safe", so the flag is suppressed
 * there and the dimension shows up in the unevaluated list instead. */
function RiskFlags({ impact }: { impact: FailsimImpact }) {
  const unevaluated = new Set(impact.notEvaluated);
  const flags: string[] = [];
  if (impact.quorumRisk && !unevaluated.has("quorum")) flags.push("Puts corosync quorum at risk");
  if (impact.cephRisk && !unevaluated.has("ceph")) flags.push("Isolates a Ceph network");
  if (impact.mgmtPathLoss.length > 0) {
    flags.push(`Loses the management path to ${impact.mgmtPathLoss.join(", ")}`);
  }
  if (flags.length === 0) return null;
  return (
    <ul className="mt-2 flex flex-col gap-0.5 text-xs text-red-800 dark:text-red-300">
      {flags.map((f) => (
        <li key={f}>{f}</li>
      ))}
    </ul>
  );
}

function NotEvaluatedList({ impact }: { impact: FailsimImpact }) {
  if (impact.notEvaluated.length === 0) return null;
  return (
    <p data-testid="spof-not-evaluated" className="mt-2 text-xs text-violet-800 dark:text-violet-300">
      Not evaluated: {describeDimensions(impact.notEvaluated)}. Treat each as unknown — the simulator had no model for
      it, so it reported nothing rather than guessing.
    </p>
  );
}
