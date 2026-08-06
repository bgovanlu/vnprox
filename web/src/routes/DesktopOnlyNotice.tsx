// T-909: the explicit affordance every desktop-only page shows on a narrow
// viewport instead of a broken/cramped attempt at the full UI (the task
// card's own wording). Deliberately actionable, not just "not available
// here" — it names what the visitor CAN still do from this device (per
// docs/architecture.md's product-safety framing, silently hiding or
// half-rendering controls is worse than telling the operator where to go).
import { Link } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";

export interface DesktopOnlyNoticeProps {
  /** The page's own name, for the heading ("Topology needs a larger screen."). */
  pageLabel: string;
  /** Extra context on why (optional — most pages don't need it beyond the
   * generic copy; a few, like a wizard, are more useful naming the specific
   * capability). */
  detail?: string;
}

export function DesktopOnlyNotice({ pageLabel, detail }: DesktopOnlyNoticeProps) {
  return (
    <EmptyState
      title={`${pageLabel} needs a larger screen`}
      description={
        detail ??
        `Editing, wizards, and other ${pageLabel.toLowerCase()} controls need more room than this device gives them. ` +
          "Open vnprox on a desktop or a wider window to use this page."
      }
      action={
        <div className="flex flex-wrap items-center justify-center gap-3 text-sm">
          <Link to="/" className="font-medium text-accent-700 underline dark:text-accent-400">
            Go to Dashboard
          </Link>
          <Link to="/tools" className="font-medium text-accent-700 underline dark:text-accent-400">
            Go to Findings
          </Link>
          <span className="text-slate-500 dark:text-slate-400">
            A pending changeset&apos;s confirm/roll back controls still work from here.
          </span>
        </div>
      }
    />
  );
}
