import clsx from "clsx";
import { HelpAnchor } from "../help/HelpAnchor";

// T-804: the bond inspector's live LACP section. `fields` is an
// EntityDetail's generic `Record<string, unknown>` (see fields.ts) —
// internal/topology's Detail() marshals the resolved *inventory.Bond
// straight through (detail.go's entityFields), so `SlaveDetail` arrives as
// an array of internal/inventory.BondSlaveState's own JSON shape (capitalized
// Go field names, no json tags — see InspectorPanel.test.tsx's captured
// fixture for the established precedent). The frontend can't take that
// shape on faith across the wire, hence the runtime narrowing below
// (mirrors isFDBRows in InspectorPanel.tsx).

/** One inventory.BondSlaveState row as it arrives over the wire. */
export interface BondSlaveDetailRow {
  Name: string;
  MIIStatus: string;
  PermHWAddr: string;
  LinkFailureCount: number;
  Active: boolean;
  ActorSystemID: string;
  ActorSystemPriority: number;
  ActorKey: number;
  ActorSynchronized: boolean;
  ActorCollecting: boolean;
  ActorDistributing: boolean;
  PartnerSystemID: string;
  PartnerSystemPriority: number;
  PartnerKey: number;
  LACPDetailSet: boolean;
}

function isBondSlaveDetailRows(v: unknown): v is BondSlaveDetailRow[] {
  return (
    Array.isArray(v) &&
    v.every((row) => typeof row === "object" && row !== null && "Name" in row && "MIIStatus" in row)
  );
}

/** A slave is "negotiated" once its actor state has reached all three LACP
 * bits — the same synchronized+collecting+distributing test
 * health_lacpmismatch.go's `lacpMismatchReason` applies server-side. This
 * is a purely visual mirror of that logic (the health check, not this
 * component, is the authoritative source for the `lacp_partner_mismatch`
 * finding). */
function isNegotiated(s: BondSlaveDetailRow): boolean {
  return s.ActorSynchronized && s.ActorCollecting && s.ActorDistributing;
}

function StateDot({ label, ok }: { label: string; ok: boolean }) {
  return (
    <span className="inline-flex items-center gap-1">
      <span
        aria-hidden="true"
        className={clsx("inline-block h-1.5 w-1.5 rounded-full", ok ? "bg-emerald-500" : "bg-red-500")}
      />
      <span className={ok ? "text-slate-500 dark:text-slate-400" : "text-red-700 dark:text-red-400"}>{label}</span>
    </span>
  );
}

/** Renders the bond inspector's live LACP section: actor/partner system
 * ID/key, and a per-slave synchronized/collecting/distributing indicator —
 * visually distinct (red vs. green) between a mismatched and a
 * negotiated-correctly bond (T-804 acceptance criterion 4). */
export function BondLacpSection({ fields }: { fields: Record<string, unknown> }) {
  const allSlaves = isBondSlaveDetailRows(fields.SlaveDetail) ? fields.SlaveDetail : [];
  const mode = typeof fields.Mode === "string" ? fields.Mode : "";
  const slaves = allSlaves.filter((s) => s.LACPDetailSet);

  if (allSlaves.length === 0) {
    return <p className="text-xs text-slate-600 dark:text-slate-400">No slave detail reported for this bond yet.</p>;
  }

  if (slaves.length === 0) {
    return (
      <p className="text-xs text-slate-600 dark:text-slate-400">
        No 802.3ad LACP negotiation detail available{mode ? ` (bond mode: ${mode})` : ""} — this bond may not be
        running LACP, or the kernel/driver on this node doesn&apos;t report actor/partner PDU detail.
      </p>
    );
  }

  // Split-brain: slaves disagree on which partner system/key they're
  // aggregating with — the same signal the lacp_partner_mismatch health
  // check raises server-side.
  const partnerKeys = new Set(slaves.map((s) => `${s.PartnerSystemID}/${String(s.PartnerKey)}`));
  const splitBrain = partnerKeys.size > 1;
  const anyNotNegotiated = slaves.some((s) => !isNegotiated(s));
  const mismatched = splitBrain || anyNotNegotiated;

  return (
    <div className="space-y-3 text-xs">
      <p className="flex items-center gap-1.5 font-medium text-slate-600 dark:text-slate-300">
        Live LACP state
        <HelpAnchor topic="bond-lacp-state" />
      </p>
      <div
        role="status"
        className={clsx(
          "rounded border p-2",
          mismatched
            ? "border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-950 dark:text-red-200"
            : "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-200",
        )}
      >
        {splitBrain
          ? "LACP split-brain: slaves are aggregating with different partner systems."
          : mismatched
            ? "LACP not fully negotiated on every slave — see detail below."
            : "LACP negotiated correctly on every slave."}
      </div>
      <ul className="space-y-2">
        {slaves.map((s) => {
          const ok = isNegotiated(s);
          return (
            <li key={s.Name} className="rounded border border-slate-200 p-2 dark:border-slate-700">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-slate-700 dark:text-slate-200">{s.Name}</span>
                <span
                  className={clsx(
                    "rounded px-1.5 py-0.5 text-[10px] font-medium",
                    ok
                      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900 dark:text-emerald-300"
                      : "bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300",
                  )}
                >
                  {ok ? "negotiated" : "not negotiated"}
                </span>
              </div>
              <dl className="mt-1.5 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-slate-500 dark:text-slate-400">
                <dt>Actor system</dt>
                <dd className="text-slate-700 dark:text-slate-200">
                  {s.ActorSystemID} (priority {s.ActorSystemPriority}, key {s.ActorKey})
                </dd>
                <dt>Partner system</dt>
                <dd className="text-slate-700 dark:text-slate-200">
                  {s.PartnerSystemID} (priority {s.PartnerSystemPriority}, key {s.PartnerKey})
                </dd>
                <dt>State</dt>
                <dd className="flex flex-wrap gap-2">
                  <StateDot label="Synchronized" ok={s.ActorSynchronized} />
                  <StateDot label="Collecting" ok={s.ActorCollecting} />
                  <StateDot label="Distributing" ok={s.ActorDistributing} />
                </dd>
              </dl>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
