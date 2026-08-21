// T-3501 AC5: findings whose Refs are empty (health/service_down for a bare
// service name like dnsmasq/frr on the reference node — nothing to name)
// have nothing to paint on the map. They must not be invisible, so this
// banner is their home: rendered once in TopologyPage.tsx, above both the
// Switch and Graph views, so the two views can never contradict each other
// about it (there is only one component, not a per-view rendering). Mirrors
// StalenessBanner.tsx's shape/placement exactly — same pattern, different
// data.
import type { Remediation, UnrefFinding } from "../api/types";
import { OperationalActionButton } from "../findings/OperationalActionButton";
import { remediationAction, type RemediationContext } from "../findings/remediation";
import { findingBadgeClass, findingChipText, worstFindingSeverity } from "./findingBadges";
import { remedyActionKey, unrefFindingKey } from "./unrefFindingKeys";

export interface UnrefFindingsBannerProps {
  findings: readonly UnrefFinding[] | undefined;
  /** Phase 36: how this surface resolves a producer-declared remedy. Absent
   * = the pre-Phase-36 rendering, with no actions offered at all. The
   * resolver fails closed on its own if `runOperational` is missing, so a
   * caller that has no confirmation dialog cannot accidentally expose a
   * mutating button by forgetting a flag. */
  remediationCtx?: RemediationContext;
  /** The id of the finding whose action is currently running, if any. */
  pendingId?: string;
  /** Per-finding outcome text from the last attempt, keyed by finding id.
   * Kept on screen rather than toasted — a failure has to stay legible next
   * to the button that produced it. */
  results?: Readonly<Record<string, string>>;
}

export function UnrefFindingsBanner({ findings, remediationCtx, pendingId, results }: UnrefFindingsBannerProps) {
  if (!findings || findings.length === 0) return null;
  const worst = worstFindingSeverity(findings.map((f) => `finding:${f.source}:${f.severity}`)) ?? "info";
  return (
    <div
      role="status"
      className={`rounded-md border px-3 py-2 text-xs ${
        worst === "error"
          ? "border-red-300 dark:border-red-700"
          : worst === "warning"
            ? "border-amber-300 dark:border-amber-700"
            : "border-slate-300 dark:border-slate-700"
      } bg-slate-50 dark:bg-slate-900`}
    >
      <p className="font-medium text-slate-700 dark:text-slate-200">
        {findings.length === 1
          ? "1 finding is not tied to any map entity:"
          : `${String(findings.length)} findings are not tied to any map entity:`}
      </p>
      {/* Height-capped and scrolled, not free-growing. This banner is a
          sibling of the map container, which is the `min-h-0 flex-1`
          leftover of a fixed-height (`h-full`) flex column — so every pixel
          this list takes comes straight out of the map. Measured on the
          scale-lab fixture (8 nodes, 300 guests) on 2026-08-20, an uncapped
          list rendered 385px tall and cut the map from ~524px to 139px, 17%
          of the page; a few more findings and `flex-1` resolves to 0, at
          which point the map is not small but *gone* (Playwright reports the
          canvas as `hidden` — see planning/reports/T-2505-followup-01.md).
          A banner about findings must never be able to displace the thing
          the findings are about. */}
      {/* tabIndex + a name because the `max-h`/`overflow-y-auto` above make
          this a scrollable region, and a scrollable region a keyboard user
          cannot reach is content they cannot read at all (axe
          `scrollable-region-focusable`, WCAG 2.1.1). The cap is deliberate —
          see the comment above — so the scroll is not going away, which
          means the keyboard affordance has to be here. */}
      <ul
        className="mt-1 max-h-28 space-y-0.5 overflow-y-auto"
        tabIndex={0}
        aria-label="Findings with no matching entity"
      >
        {findings.map((f) => {
          const key = unrefFindingKey(f);
          const actionKey = remedyActionKey(f.remedy);
          const resolved = remediationCtx ? remediationAction(f.remedy, remediationCtx) : undefined;
          const result = actionKey === undefined ? undefined : results?.[actionKey];
          return (
          <li key={key} className="flex flex-wrap items-center gap-1.5">
            <span
              className={`rounded px-1 py-0.5 text-[10px] font-medium ${findingBadgeClass(f.severity)}`}
            >
              {findingChipText({ source: f.source, severity: f.severity })}
            </span>
            <span className="text-slate-600 dark:text-slate-300">{f.detail || f.check}</span>
            {f.nodes.length > 0 && (
              <span className="text-slate-500 dark:text-slate-400">
                (on {f.nodes.join(", ")})
              </span>
            )}
            {/* T-3604: the remedy the producer declared — "Start dnsmasq"
                on the node it names. Mutating, so it is the confirmed
                tier: OperationalActionButton owns the dialog, and the
                resolver has already refused to hand us anything at all
                without netWrite. */}
            {resolved && (
              <OperationalActionButton
                label={resolved.label}
                title={`${resolved.label}?`}
                description={describeStart(f.remedy)}
                confirmLabel="Start"
                pending={actionKey !== undefined && pendingId === actionKey}
                onConfirm={resolved.onClick}
              />
            )}
            {result !== undefined && result !== "" && (
              <span className="text-red-700 dark:text-red-300">— {result}</span>
            )}
          </li>
          );
        })}
      </ul>
    </div>
  );
}

/** The confirmation copy for a service start. Names the unit and the node,
 * and says what the action is NOT: this starts the service now and does not
 * enable it at boot, which is a different decision the operator still owns.
 * An operator who reads "start" and gets "start and enable" has been
 * misled, however convenient the extra step would have been. */
function describeStart(remedy: Remediation | undefined): string {
  const service = remedy?.params?.service ?? "the service";
  const node = remedy?.params?.node ?? "the node";
  return (
    `vnprox will run "systemctl start ${service}" on node ${node}. ` +
    "This starts it now; it does not enable it at boot, and it changes no network configuration — " +
    "no changeset is staged. The attempt is written to the audit log whether it succeeds or fails."
  );
}
