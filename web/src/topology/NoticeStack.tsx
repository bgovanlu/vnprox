// SPDX-License-Identifier: Apache-2.0

// T-4304: one row for N page-level notices, instead of N stacked banners.
//
// Measured off the visual gate's first graph-view capture at 1400x900: the
// staleness banner took 150px, the untied-findings banner 149px, the LLDP
// notice 48px — 39% of the viewport — and the topology canvas got 235px, or
// 26%. The product is a visual networking tool and its visual was the
// smallest region on its own page. Worse, the three banners appear precisely
// when a cluster has problems, which is when an operator most needs the map.
//
// Nothing bounded the stack, either: three is what today's fixture happens to
// produce, and the layout would have kept growing downward with a fourth.
//
// So the rule this component encodes is: **one notice renders as itself; two
// or more collapse to a summary row.** The single-notice case is deliberately
// untouched — a lone banner is not a stack, and wrapping it in a disclosure
// would make the common case worse to fix the uncommon one.
import { useId, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Badge } from "../components/Badge";
import type { StatusTone } from "../components/statusTone";

export interface Notice {
  readonly id: string;
  /** Whether this notice applies right now. Computed by the caller from the
   * same data the banner itself checks — the banners each return null when
   * inapplicable, and React gives no way to ask a child whether it rendered,
   * so the predicate is lifted rather than inferred. */
  readonly active: boolean;
  readonly tone: StatusTone;
  /** Two or three words for the collapsed row. Not the banner's own headline:
   * this has to survive sitting beside two others on one line. */
  readonly label: string;
  readonly body: ReactNode;
}

export interface NoticeStackProps {
  readonly notices: readonly Notice[];
  /** Names the region for screen readers, e.g. "Topology notices". */
  readonly label: string;
}

/** Written out per tone rather than composed as `border-status-${tone}`.
 * Tailwind v4 resolves utilities by scanning source TEXT, so an interpolated
 * class name is never emitted into the stylesheet — the row would render with
 * no border and no wash, and every test asserting on `className` would still
 * pass because the string is right and the CSS is missing. */
const ROW_TONE: Record<StatusTone, string> = {
  ok: "border-status-ok bg-status-ok-soft",
  degraded: "border-status-degraded bg-status-degraded-soft",
  critical: "border-status-critical bg-status-critical-soft",
  info: "border-status-info bg-status-info-soft",
  unknown: "border-status-unknown bg-status-unknown-soft",
};

export function NoticeStack({ notices, label }: NoticeStackProps) {
  const [expanded, setExpanded] = useState(false);
  const regionId = useId();
  const active = notices.filter((n) => n.active);

  if (active.length === 0) return null;

  // One notice is not a stack. Render it exactly as it rendered before this
  // component existed — same DOM, same spacing, no disclosure.
  if (active.length === 1) {
    return <div className="print:hidden flex flex-col gap-2">{active[0]?.body}</div>;
  }

  const Chevron = expanded ? ChevronDown : ChevronRight;
  // The most severe tone present drives the summary row's own treatment, so a
  // collapsed row cannot make a critical condition look routine.
  const severity: StatusTone[] = ["critical", "degraded", "unknown", "info", "ok"];
  const worst = severity.find((t) => active.some((n) => n.tone === t)) ?? "info";

  return (
    <section aria-label={label} className="print:hidden">
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={regionId}
        onClick={() => {
          setExpanded((e) => !e);
        }}
        className={`flex w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm transition-colors hover:bg-surface-sunken ${ROW_TONE[worst]}`}
      >
        <Chevron size={16} className="shrink-0 text-fg-muted" aria-hidden="true" />
        <span className="font-medium text-fg-body">
          {active.length} notices
        </span>
        <span className="flex flex-wrap items-center gap-1.5">
          {active.map((n) => (
            <Badge key={n.id} status={n.tone} size="sm">
              {n.label}
            </Badge>
          ))}
        </span>
        <span className="ml-auto shrink-0 text-xs text-fg-subtle">{expanded ? "Hide" : "Show"}</span>
      </button>
      {/* Rendered only when open. Keeping the bodies mounted-but-hidden would
          leave their live regions announcing to screen readers from behind a
          collapsed row, which is worse than the stacking this replaces. */}
      {expanded && (
        <div id={regionId} className="mt-2 flex flex-col gap-2">
          {active.map((n) => (
            <div key={n.id}>{n.body}</div>
          ))}
        </div>
      )}
    </section>
  );
}
