// A single custom React Flow node component covering every rendered entity
// kind (bridge, bond, physnic, vlan, sdn-zone/vnet, guest, guest-nic, and
// the synthetic guest-group pill), branching its look by `data.kind` rather
// than one component file per kind — deliberately, given the number of
// kinds (a dozen-plus inventory Kinds project onto only a handful of
// visually distinct shapes: a plain box, the SDN "cloud" look, and the
// guest-group pill) and that every one of them shares the same status-
// painting/dim/highlight/selection behavior (docs/features/topology.md §2).
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import clsx from "clsx";
import type { EntityStatus, SimVerdict, VerifyOutcome } from "../api/types";
import { entityAriaLabel } from "./a11yBridge";
import { useReducedMotion } from "../lib/useReducedMotion";

/** This node's role along a path-simulator overlay (T-504): "path" is any
 * hop on the traced route, "blocking" is the enforcement-point endpoint a
 * deny verdict stopped at, "missing" is the break point of an unreachable
 * verdict (docs/api.md's `Missing.atRef`). */
export type SimPathRole = "path" | "blocking" | "missing";

export interface EntityNodeData extends Record<string, unknown> {
  label: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
  dimmed: boolean;
  /** This node's band is rendering last-known data because a node-scoped
   * collector source is stale (docs/features/topology.md §5: "its band
   * renders greyed") — greyscale, distinct from `dimmed` (VLAN filter). */
  stale?: boolean;
  highlighted: boolean;
  isGuestGroup: boolean;
  collapsedCount?: number;
  /** Path simulator overlay (see toFlowElements.ts's `pathHighlight` param)
   * — undefined leaves this node's normal status/hover rendering alone. */
  simVerdict?: SimVerdict;
  simRole?: SimPathRole;
  /** T-806 "Verify live": set only on the probed source's own node, once a
   * live result has come back — a marker distinct from simVerdict/simRole
   * above (docs/features/firewall.md §5's "an observed-outcome marker
   * distinct from the simulated-verdict styling"). */
  verifyOutcome?: VerifyOutcome;
  verifyDiverges?: boolean;
}

export type EntityFlowNode = Node<EntityNodeData, "entity">;

// Status painting (docs/features/topology.md §2: "link down = red edge;
// degraded bond (missing slave) = amber; ... drift = dashed outline").
// "unknown" gets a neutral dashed treatment — it's a legitimate, common
// state for peer nodes' host-only fields (see api/types.ts's EntityStatus
// doc comment), not something to visually alarm on.
const STATUS_CLASSES: Record<EntityStatus, string> = {
  ok: "border-slate-300 dark:border-slate-600",
  down: "border-red-500 dark:border-red-500 ring-1 ring-red-500/40",
  degraded: "border-amber-500 dark:border-amber-500 ring-1 ring-amber-500/40",
  unknown: "border-slate-400 border-dashed dark:border-slate-500",
};

const KIND_ACCENT: Record<string, string> = {
  physnic: "bg-slate-50 dark:bg-slate-800",
  bond: "bg-sky-50 dark:bg-sky-950",
  "ovs-bond": "bg-sky-50 dark:bg-sky-950",
  bridge: "bg-indigo-50 dark:bg-indigo-950",
  "ovs-bridge": "bg-indigo-50 dark:bg-indigo-950",
  vlan: "bg-violet-50 dark:bg-violet-950",
  "sdn-zone": "bg-teal-50 dark:bg-teal-950",
  "sdn-vnet": "bg-teal-50 dark:bg-teal-950",
  "sdn-subnet": "bg-teal-50 dark:bg-teal-950",
  guest: "bg-emerald-50 dark:bg-emerald-950",
  "guest-nic": "bg-emerald-50 dark:bg-emerald-950",
  "guest-group": "bg-emerald-100 dark:bg-emerald-900",
  "lldp-neighbor": "bg-slate-100 dark:bg-slate-800",
};

// Path simulator verdict colors (T-504, docs/features/firewall.md §5):
// allow=emerald, deny=red, unreachable=amber, indeterminate=violet (a
// distinct fourth color — never squeezed into the allow/deny/unreachable
// palette, per the honesty contract's "never a pass/fail" requirement).
const SIM_RING_CLASS: Record<SimVerdict, string> = {
  allow: "ring-2 ring-emerald-500",
  deny: "ring-2 ring-red-500",
  unreachable: "ring-2 ring-amber-500",
  indeterminate: "ring-2 ring-violet-500",
};

const SIM_MARKER_CLASS: Record<SimVerdict, string> = {
  allow: "bg-emerald-500",
  deny: "bg-red-500",
  unreachable: "bg-amber-500",
  indeterminate: "bg-violet-500",
};

const SIM_MARKER_LABEL: Record<SimPathRole, string> = {
  path: "on traced path",
  blocking: "blocking point",
  missing: "missing link",
};

// T-806 "Verify live": the observed-outcome marker is a *square* (the
// simulated-verdict marker above is a circle) so the two read as visually
// distinct styling even when they land on the same corner of the same
// node, per docs/features/firewall.md §5's requirement — never letting the
// live result's marker be mistaken for a restyled simulated one.
const VERIFY_MARKER_CLASS: Record<VerifyOutcome, string> = {
  reachable: "bg-emerald-500",
  unreachable: "bg-red-500",
  timeout: "bg-amber-500",
  error: "bg-slate-400",
};

const VERIFY_OUTCOME_LABEL: Record<VerifyOutcome, string> = {
  reachable: "reachable",
  unreachable: "unreachable",
  timeout: "timed out",
  error: "could not be attempted",
};

// T-702: distinct treatment for the management-path badge vocabulary
// (docs/features/topology.md §3) — "mgmt"/"corosync" mark the carrier
// itself, "mgmt-path" marks every physical entity behind it. Amber (not the
// plain grey every other badge renders as) so a glance at the map answers
// "which interface carries this node's management/corosync traffic, and
// what's physically behind it" without opening the inspector.
const MGMT_BADGE_LABEL: Record<string, string> = {
  mgmt: "management IP",
  corosync: "corosync link",
  "mgmt-path": "on the management path",
};

function isMgmtBadge(badge: string): boolean {
  return badge in MGMT_BADGE_LABEL;
}

export function EntityNode({ id, data, selected }: NodeProps<EntityFlowNode>) {
  const isPill = data.isGuestGroup;
  const simVerdict = data.simVerdict;
  const simRole = data.simRole;
  const verifyOutcome = data.verifyOutcome;
  // T-905: `prefers-reduced-motion: reduce` disables the drift "pulse" and
  // the plain opacity/color transition below, falling back to the same
  // static dashed-border/badge treatment minus the animation.
  const reducedMotion = useReducedMotion();
  const drifting = data.badges.includes("drift");
  return (
    <div
      role="button"
      tabIndex={0}
      // T-903: roving arrow-key focus (src/keyboard/useRovingFocus.ts)
      // reads this attribute to find every focusable entity in the Graph
      // view and to focus/activate them by id — the same id React Flow's
      // own onNodeClick already reports, so keyboard activation (Enter)
      // and a pointer click always resolve to the identical entity.
      data-entity-ref={id}
      // Kept as the plain label (not the richer entityAriaLabel format
      // T-905 gives canvas v2's a11y proxies): topology.spec.ts/scale.spec
      // .ts/perf.spec.ts (T-607/T-901) all locate this node via
      // `getByRole("button", { name: "vmbr0", exact: true })` — an exact
      // match a richer label would break. The kind/status/badge detail is
      // instead exposed via `aria-describedby` below (a real, standard
      // accessible-description channel, additive to the name), so this
      // stays both WCAG-improved and backward-compatible with the locked
      // exact-name query every one of those specs already relies on.
      aria-label={data.label}
      aria-describedby={`${id}-a11y-desc`}
      className={clsx(
        "relative flex flex-col gap-1 border px-3 py-2 text-xs shadow-sm",
        reducedMotion ? "transition-none" : "transition-opacity",
        "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
        isPill ? "rounded-full text-center" : "rounded-md",
        KIND_ACCENT[data.kind] ?? "bg-white dark:bg-slate-900",
        STATUS_CLASSES[data.status],
        // One opacity class per node (never two competing ones, whose CSS
        // order would decide): the VLAN filter's dim wins over staleness.
        data.dimmed && !data.highlighted ? "opacity-25" : data.stale ? "opacity-60" : "opacity-100",
        data.stale && "grayscale",
        data.highlighted && "ring-2 ring-blue-500",
        selected && "outline outline-2 outline-offset-1 outline-blue-600",
        // drift = dashed outline (docs/features/topology.md §2), additive
        // to (not replacing) the status-driven border color above — a
        // "down"/"degraded" node can also carry an open drift finding.
        // T-905: pulses (via Tailwind's built-in `animate-pulse`) when
        // motion is allowed, static dashed border otherwise — the
        // "unconfirmed-changeset pulse, drift dash" reduced-motion case
        // this task's card names explicitly.
        drifting && "border-dashed",
        drifting && !reducedMotion && "animate-pulse",
        // Path simulator overlay (T-504) wins visually over the plain hover
        // ring above (a simulated trace is a more deliberate, rarer action
        // than a passive hover) and marks the missing-link break with a
        // dashed border so it reads as "broken", not just "highlighted".
        simVerdict && SIM_RING_CLASS[simVerdict],
        simVerdict && simRole === "missing" && "border-dashed border-2",
      )}
      style={{ minWidth: 140 }}
    >
      <Handle type="target" position={Position.Top} className="opacity-0" />
      <Handle type="source" position={Position.Bottom} className="opacity-0" />
      {/* T-905 AC4: the full kind/status/badge description (mgmt/corosync/
          mgmt-path spelled out, drift called out) — visually hidden,
          wired via aria-describedby above so it's announced alongside the
          plain-name aria-label without changing that name (see this
          node's aria-label doc comment for why the name itself stays
          plain). Reuses entityAriaLabel — the same text canvas v2's a11y
          proxies expose as their own aria-label — so both renderers
          describe the same entity identically to a screen reader. */}
      <span id={`${id}-a11y-desc`} className="sr-only">
        {entityAriaLabel(data)}
      </span>
      {simVerdict && simRole && (
        <span
          role="img"
          aria-label={SIM_MARKER_LABEL[simRole]}
          title={SIM_MARKER_LABEL[simRole]}
          className={clsx(
            "absolute -right-1.5 -top-1.5 h-3 w-3 rounded-full border-2 border-white dark:border-slate-900",
            SIM_MARKER_CLASS[simVerdict],
          )}
        />
      )}
      {verifyOutcome && (
        <span
          role="img"
          aria-label={`observed: ${VERIFY_OUTCOME_LABEL[verifyOutcome]}`}
          title={`Live probe observed: ${VERIFY_OUTCOME_LABEL[verifyOutcome]}`}
          className={clsx(
            "absolute -bottom-1.5 -left-1.5 h-3 w-3 rounded-sm border-2 border-white dark:border-slate-900",
            VERIFY_MARKER_CLASS[verifyOutcome],
          )}
        />
      )}
      {data.verifyDiverges && (
        <span
          role="img"
          aria-label="verify live diverges"
          title="The live probe disagrees with the simulated verdict."
          className="absolute -bottom-1.5 -right-1.5 rounded bg-fuchsia-600 px-1 py-0.5 text-[9px] font-semibold uppercase leading-none text-white"
        >
          Diverges
        </span>
      )}
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium text-slate-800 dark:text-slate-100">{data.label}</span>
        {!isPill && (
          <span className="shrink-0 text-[10px] uppercase tracking-wide text-slate-400 dark:text-slate-500">
            {data.kind}
          </span>
        )}
      </div>
      {data.badges.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {data.badges.map((b) => (
            <span
              key={b}
              title={isMgmtBadge(b) ? MGMT_BADGE_LABEL[b] : undefined}
              className={clsx(
                "rounded px-1 py-0.5 text-[10px]",
                isMgmtBadge(b)
                  ? "bg-amber-200/70 text-amber-800 dark:bg-amber-900/60 dark:text-amber-200"
                  : "bg-slate-200/70 text-slate-600 dark:bg-slate-700/70 dark:text-slate-300",
              )}
            >
              {b}
            </span>
          ))}
        </div>
      )}
      {isPill && (
        <span className="text-[10px] text-slate-500 dark:text-slate-400">click to expand</span>
      )}
    </div>
  );
}
