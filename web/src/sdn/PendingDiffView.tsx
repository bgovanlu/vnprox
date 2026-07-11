// Renders docs/features/sdn.md §1's "staged-vs-running as a first-class
// diff" for one zone/vnet/subnet — the pending-state diff view T-401
// acceptance criterion 2 asks for ("Fixture with staged-but-unapplied SDN
// change -> pending diff renders exactly the staged delta").
import type { SdnPendingDiff } from "../api/types";
import { formatDiffValue } from "./tree";

const STATE_LABEL: Record<SdnPendingDiff["state"], string> = {
  new: "Staged — created, not yet applied",
  changed: "Staged — changed, not yet applied",
  deleted: "Staged — marked for deletion, not yet applied",
};

export function PendingDiffView({ diff }: { diff: SdnPendingDiff }) {
  const fields = diff.changedFields ?? [];
  return (
    <div
      role="status"
      className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-700 dark:bg-amber-950"
    >
      <p className="font-medium text-amber-800 dark:text-amber-200">{STATE_LABEL[diff.state]}</p>
      {diff.state === "changed" && fields.length > 0 && (
        <table className="mt-2 w-full text-xs">
          <thead>
            <tr className="text-left text-amber-700 dark:text-amber-300">
              <th className="pr-3 font-medium">Field</th>
              <th className="pr-3 font-medium">Running</th>
              <th className="font-medium">Staged</th>
            </tr>
          </thead>
          <tbody>
            {fields.map((field) => (
              <tr key={field}>
                <td className="py-0.5 pr-3 font-mono">{field}</td>
                <td className="py-0.5 pr-3 text-amber-900/70 dark:text-amber-300/70">
                  {formatDiffValue(diff.running?.[field])}
                </td>
                <td className="py-0.5 font-semibold text-amber-900 dark:text-amber-100">
                  {formatDiffValue(diff.staged?.[field])}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

/** The inline "in sync" indicator shown instead of PendingDiffView when an
 * entity has no staged edit at all (AC2: "applied state shows 'in sync'"). */
export function InSyncBadge() {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border border-emerald-300 bg-emerald-50 px-2 py-1 text-xs font-medium text-emerald-700 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">
      In sync
    </span>
  );
}
