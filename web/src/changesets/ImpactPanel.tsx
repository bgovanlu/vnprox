// T-2404's blast-radius panel: what an operator would NOTICE if this
// changeset were applied.
//
// It sits next to the diff on the review & apply screen because that is the
// moment the question is asked. The diff says what changes; this says who
// notices.
//
// Every verdict rendered here carries the server's own reason, never a phrase
// invented on this side. That matters more than it looks: an impact panel that
// over-claims with no explanation trains people to skip it, and a skipped
// warning is the same as an absent one. If the server cannot explain a verdict
// it does not produce one — `reason` is never empty in the API shape.
import clsx from "clsx";
import { HelpAnchor } from "../help/HelpAnchor";
import type { ChangesetImpact, DisruptionClass } from "../api/types";

const DISRUPTION_LABEL: Record<DisruptionClass, string> = {
  none: "No disruption",
  brief: "Brief interruption",
  outage: "Outage",
};

const DISRUPTION_CLASSES: Record<DisruptionClass, string> = {
  none: "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200",
  brief: "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200",
  outage: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200",
};

export interface ImpactPanelProps {
  impact?: ChangesetImpact;
  loading?: boolean;
  error?: boolean;
}

export function ImpactPanel({ impact, loading, error }: ImpactPanelProps) {
  if (loading) {
    return <p className="text-xs text-slate-600 dark:text-slate-400">Computing impact…</p>;
  }
  // A failure says so rather than rendering an empty panel. "We could not work
  // out the blast radius" and "the blast radius is nothing" must never look the
  // same.
  if (error || !impact) {
    return (
      <p className="text-xs text-red-600 dark:text-red-400">
        Could not compute the impact of this changeset. Do not read this as &ldquo;no impact&rdquo;.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-3" aria-label="Blast radius">
      <div className={clsx("rounded-md border p-3 text-sm", DISRUPTION_CLASSES[impact.disruption])}>
        <p className="flex items-center gap-1.5 font-semibold">
          {DISRUPTION_LABEL[impact.disruption]}
          <HelpAnchor topic="changeset-impact" />
        </p>
        <p className="mt-1 text-xs">
          {impact.nodes.length} node{impact.nodes.length === 1 ? "" : "s"}
          {impact.nodes.length > 0 ? ` (${impact.nodes.join(", ")})` : ""}
          {" · "}
          {impact.guests.length} guest{impact.guests.length === 1 ? "" : "s"} affected
        </p>
        {impact.touchesMgmtPath && (
          <p className="mt-1 text-xs font-medium">This changeset touches a management path.</p>
        )}
      </div>

      {impact.guests.length > 0 && (
        <section>
          <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">Guests affected</h3>
          <ul className="space-y-1 text-xs">
            {impact.guests.map((g) => (
              <li key={`${g.ref}:${g.nic}`} className="rounded border border-slate-200 px-2 py-1 dark:border-slate-700">
                <span className="font-medium">{g.name || g.ref}</span>
                {g.vmid > 0 && <span className="text-slate-500 dark:text-slate-400"> ({g.vmid})</span>}
                <span className="text-slate-500 dark:text-slate-400">
                  {" "}
                  on {g.node} — {g.nic} via {g.carrier}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section>
        <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">Per operation</h3>
        <ul className="space-y-1 text-xs">
          {impact.ops.map((o, i) => (
            <li key={o.opId ?? `${o.op}:${String(i)}`} className="rounded border border-slate-200 px-2 py-1 dark:border-slate-700">
              <span
                className={clsx(
                  "mr-1.5 rounded px-1 py-0.5 text-[10px] uppercase",
                  DISRUPTION_CLASSES[o.disruption],
                )}
              >
                {o.disruption}
              </span>
              <span className="font-mono">{o.op}</span>
              {o.target && <span className="text-slate-500 dark:text-slate-400"> {o.target}</span>}
              {/* The server's own reason, verbatim. */}
              <span className="block text-slate-600 dark:text-slate-300">{o.reason}</span>
            </li>
          ))}
          {impact.ops.length === 0 && <li className="text-slate-600 dark:text-slate-400">No operations.</li>}
        </ul>
      </section>
    </div>
  );
}
