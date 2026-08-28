// SPDX-License-Identifier: Apache-2.0

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
import type { EntityStatus, FindingBadge, SimVerdict, VerifyOutcome } from "../api/types";
import { entityAriaLabel } from "./a11yBridge";
import { useReducedMotion } from "../lib/useReducedMotion";
import {
  findingBadgeClass,
  findingChipText,
  findingDetailText,
  hasOpenFinding,
  isMgmtBadge,
  MGMT_BADGE_CLASS,
  MGMT_BADGE_LABEL,
  parseFindingBadge,
  shouldPulse,
} from "./findingBadges";
import { PortJack } from "./PortBody";
import { jackKindForEntity, speedMarking } from "./portMedia";
import { STP_ROOT_BADGE, stpBadgeLabel } from "./stpOverlay";

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
  /** T-3501, additive to badges: one entry per open finding naming this
   * entity, carrying its own check/detail text — see TopologyNode.findings'
   * doc comment (api/types.ts). Absent on entities/fixtures that predate
   * this field; every helper in findingBadges.ts treats that the same as
   * an empty list. */
  findings?: FindingBadge[];
  dimmed: boolean;
  /** This node's band is rendering last-known data because a node-scoped
   * collector source is stale (docs/features/topology.md §5: "its band
   * renders greyed") — greyscale, distinct from `dimmed` (VLAN filter). */
  stale?: boolean;
  highlighted: boolean;
  isGuestGroup: boolean;
  /** T-1907: true for a synthetic "phys-group:<node>" per-node physical-
   * layer summary pill — reuses the exact same pill rendering as
   * isGuestGroup (see `isPill` below) rather than a second look. Optional
   * (unlike isGuestGroup) so every pre-existing EntityNodeData literal
   * across the codebase — none of which know about phys-group pills —
   * keeps compiling unchanged; falsy/undefined reads exactly like `false`. */
  isPhysGroup?: boolean;
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
  /** T-3505, mirrors TopologyNode.mediaPort/speedMbps (physnic nodes only —
   * see api/types.ts's doc comment): plumbed through so this DOM renderer
   * and canvasDraw.ts's v2 renderer draw the same port jack/speed marking
   * SwitchFaceplate.tsx already does, off the same two facts, rather than
   * the graph silently not knowing what the switch faceplate now says about
   * the identical entity. Absent on every other kind. */
  mediaPort?: string;
  speedMbps?: number;
}

export type EntityFlowNode = Node<EntityNodeData, "entity">;

// Status painting (docs/features/topology.md §2: "link down = red edge;
// degraded bond (missing slave) = amber; ... drift = dashed outline").
// "unknown" gets a neutral dashed treatment — it's a legitimate, common
// state for peer nodes' host-only fields (see api/types.ts's EntityStatus
// doc comment), not something to visually alarm on.
const STATUS_CLASSES: Record<EntityStatus, string> = {
  ok: "border-slate-300 dark:border-slate-600",
  down: "border-status-critical ring-1 ring-status-critical/40",
  degraded: "border-status-degraded ring-1 ring-status-degraded/40",
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
  "phys-group": "bg-slate-200 dark:bg-slate-700",
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
  reachable: "bg-status-ok",
  unreachable: "bg-status-critical",
  timeout: "bg-status-degraded",
  error: "bg-status-unknown",
};

const VERIFY_OUTCOME_LABEL: Record<VerifyOutcome, string> = {
  reachable: "reachable",
  unreachable: "unreachable",
  timeout: "timed out",
  error: "could not be attempted",
};

// T-1505: the shaping-active badge (docs/api.md's GET /topology badge
// vocabulary — reuses T-901's plain badges[] convention, additive to
// whatever mgmt/drift badges are already present) gets its own distinct
// (blue) treatment, the same "a glance at the map answers the question"
// rationale MGMT_BADGE_LABEL's amber treatment (findingBadges.ts) documents
// — here, "which bridge is currently rate-limited."
const QOS_SHAPED_BADGE = "qos-shaped";
const QOS_SHAPED_LABEL = "carries an applied QoS shape";

function isQosShapedBadge(badge: string): boolean {
  return badge === QOS_SHAPED_BADGE;
}

export function EntityNode({ id, data, selected }: NodeProps<EntityFlowNode>) {
  const isPill = data.isGuestGroup || data.isPhysGroup;
  const simVerdict = data.simVerdict;
  const simRole = data.simRole;
  const verifyOutcome = data.verifyOutcome;
  // T-905: `prefers-reduced-motion: reduce` disables the drift "pulse" and
  // the plain opacity/color transition below, falling back to the same
  // static dashed-border/badge treatment minus the animation.
  const reducedMotion = useReducedMotion();
  // T-3501: the dashed-outline affordance stays source-agnostic (any open
  // finding earns it, exactly as the old bare "drift" badge did), but the
  // pulse is now gated on severity — see shouldPulse's doc comment for why
  // an entity whose only signal is the legacy fallback badge still pulses.
  const hasFinding = hasOpenFinding(data.badges);
  const pulseWorthy = shouldPulse(data.badges);
  const jackKind = jackKindForEntity(data.kind, data.mediaPort);
  const speedLabel = speedMarking(data.speedMbps);
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
        //
        // Staleness no longer carries an opacity at all — `grayscale` below
        // is the whole treatment, matching SwitchFaceplate (see the longer
        // note there). `opacity` fades a node's TEXT along with its chrome:
        // at 0.6 this node's kind badge measured 4.30:1 (axe: fg #798098 on
        // #17193e) against a 4.5:1 AA floor, so a node was hardest to read
        // exactly when it was reporting that its data had stopped
        // refreshing. The VLAN filter's `dimmed` keeps its opacity — that is
        // a deliberate "you filtered this out" de-emphasis the user just
        // asked for, not a health signal about the node.
        data.dimmed && !data.highlighted ? "opacity-25" : "opacity-100",
        data.stale && "grayscale",
        data.highlighted && "ring-2 ring-blue-500",
        selected && "outline outline-2 outline-offset-1 outline-blue-600",
        // Open finding = dashed outline (docs/features/topology.md §2),
        // additive to (not replacing) the status-driven border color above
        // — a "down"/"degraded" node can also carry an open finding. T-905:
        // pulses (via Tailwind's built-in `animate-pulse`) when motion is
        // allowed AND the finding's severity warrants it (T-3501 —
        // shouldPulse reserves motion for "error"), static dashed border
        // otherwise — the "unconfirmed-changeset pulse, drift dash"
        // reduced-motion case this task's card names explicitly never
        // collapses to no treatment at all, only to the static one.
        hasFinding && "border-dashed",
        hasFinding && pulseWorthy && !reducedMotion && "animate-pulse",
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
          // Contrast here is measured against the *node's own tint*, not the
          // page background: entity nodes carry per-kind and per-state
          // background tints, and this badge sits on top of them. Both halves
          // of the usual muted pairing fail against those tints —
          // `dark:text-slate-500` measured 1.84:1 and `dark:text-slate-400`
          // 3.7-4.4:1, either side of WCAG AA's 4.5:1 but neither above it
          // (T-2108). So this uses a step darker in light mode and lighter in
          // dark mode than muted text elsewhere, which is the only way to
          // clear 4.5:1 across every tint a node can take.
          <span className="shrink-0 text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
            {data.kind}
          </span>
        )}
      </div>
      {jackKind && (
        // T-3505: the same drawn jack SwitchFaceplate.tsx's PortCell shows
        // (PortBody.tsx's <PortJack>, reused verbatim rather than
        // reimplemented) — a physnic's real copper/fibre/unknown socket, or
        // a guest-nic's dashed virtual one — plus the negotiated speed
        // where a physnic reported one. The Switch view already draws this
        // for the identical entity (T-3503); the graph previously said
        // nothing about it, which is the "must not contradict" gap this
        // task closes.
        <div className="flex items-center gap-1">
          <PortJack kind={jackKind} status={data.status} />
          {speedLabel && (
            <span className="text-[9px] font-semibold uppercase leading-none tracking-wider text-slate-600 dark:text-slate-300">
              {speedLabel}
            </span>
          )}
        </div>
      )}
      {data.badges.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {data.badges.map((b) => {
            // T-3501: the legacy bare "drift" badge stays on the wire for
            // back-compat (see findingBadges.ts's doc comment) but is never
            // rendered as its own chip any more — a "finding:" token (below)
            // or, failing that, nothing, replaces the literal word "drift"
            // this chip used to print regardless of what actually fired.
            if (b === "drift") return null;
            const parsed = parseFindingBadge(b);
            if (parsed) {
              return (
                <span
                  key={b}
                  title={findingDetailText(parsed, data.findings)}
                  className={clsx("rounded px-1 py-0.5 text-[10px] font-medium", findingBadgeClass(parsed.severity))}
                >
                  {findingChipText(parsed)}
                </span>
              );
            }
            // T-3901: the elected root bridge of an STP-enabled L2 domain —
            // "the first question in any L2 loop hunt" gets its own
            // distinct (indigo) treatment, the same "answer the question at
            // a glance" rationale MGMT_BADGE_CLASS/QOS_SHAPED_BADGE's
            // colors already document, using a color neither of those (nor
            // status/finding-severity) already claims.
            if (b === STP_ROOT_BADGE) {
              return (
                <span
                  key={b}
                  title="This bridge is the elected STP root of its L2 domain."
                  className="rounded bg-indigo-200 px-1 py-0.5 text-[10px] font-medium text-indigo-900 dark:bg-indigo-900 dark:text-indigo-100"
                >
                  {stpBadgeLabel(b)}
                </span>
              );
            }
            return (
              <span
                key={b}
                title={isMgmtBadge(b) ? MGMT_BADGE_LABEL[b] : isQosShapedBadge(b) ? QOS_SHAPED_LABEL : undefined}
                className={clsx(
                  "rounded px-1 py-0.5 text-[10px]",
                  isMgmtBadge(b)
                    ? MGMT_BADGE_CLASS
                    : isQosShapedBadge(b)
                      ? "bg-blue-200 text-blue-800 dark:bg-blue-900/60 dark:text-blue-200"
                      // dark:text-slate-200, not -300. At 10px these badges need
                      // the full 4.5:1 (they are below the 18.66px large-text
                      // threshold), and slate-300 over a translucent
                      // slate-700/70 sitting on a tinted node measured 4.35–4.39
                      // — under, but only just, and only on some tints, so the
                      // axe gate caught it on one run and not the two before it
                      // (T-2108). slate-200 clears it on every tint with margin
                      // rather than landing on the threshold again.
                      : "bg-slate-200 text-slate-600 dark:bg-slate-700 dark:text-slate-200",
                )}
              >
                {b}
              </span>
            );
          })}
        </div>
      )}
      {isPill && (
        <span className="text-[10px] text-fg-subtle">click to expand</span>
      )}
    </div>
  );
}
