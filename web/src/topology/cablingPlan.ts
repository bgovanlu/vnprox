// SPDX-License-Identifier: Apache-2.0

// T-3907: the physical cabling plan — a rendering/print-layout task over
// data that already exists. `internal/host/lldp.go` already parses real
// switch-side LLDP data, and switchModel.ts's `buildSwitchModel` already
// joins every physical NIC in the topology (bridge/bond members AND free
// NICs) against its LLDP neighbor, when one was discovered
// (SwitchPortNic.neighbor). This module does no new derivation of that
// join — it only FLATTENS SwitchTopology's per-bridge/per-free-port
// structure into one row per physical NIC, grouped by node, which is what
// a cabling plan actually wants to show (SwitchTopology is grouped by
// virtual switch, which is the wrong axis for "what's plugged into this
// node").
//
// The one thing this module insists on that the faceplate view does not:
// every physnic (or phys-group pill) SwitchTopology knows about becomes a
// row, whether or not it has a neighbor. A NIC with no LLDP neighbor is
// exactly as real a cabling-plan entry as one with a neighbor — an
// unmanaged switch, a directly-attached host, or LLDP disabled on the far
// end are all common and unremarkable — so it is never silently absent,
// only explicitly marked `linkState: "unknown"`. Nothing here calls that
// state "not connected"; the NIC's own `status`/`badges` (link up/down)
// already answer that question independently.
import type { EntityStatus } from "../api/types";
import type { NodeSwitchGroup, SwitchPortNic, SwitchTopology, SwitchUplink } from "./switchModel";

/** The three-way state a NIC's far end can be in — deliberately not a
 * boolean, so "no LLDP neighbor" (`unknown`) can never be confused with
 * "this NIC is one of several collapsed into a phys-group pill and hasn't
 * been individually resolved" (`grouped`), which needs a different
 * operator action (expand the group) rather than "check the far end". */
export type CablingLinkState = "discovered" | "unknown" | "grouped";

export interface CablingRow {
  nicRef: string;
  nicLabel: string;
  status: EntityStatus;
  /** The virtual switch (bridge) this NIC backs, when it is currently
   * wired into one — undefined for a NIC not attached to any bridge
   * (switchModel.ts's "free port"). */
  bridgeName?: string;
  /** The bond this NIC is a member of, when it is a bond member — absent
   * for a bare NIC uplink (there is no bond to name). */
  bondLabel?: string;
  mediaPort?: string;
  speedMbps?: number;
  duplex?: string;
  linkState: CablingLinkState;
  /** Only meaningful (and only ever set) when linkState is "discovered". */
  farEndSwitch?: string;
  farEndPort?: string;
  /** True for a T-1907 collapsed phys-group pill standing in for several
   * real NICs — see CablingLinkState's "grouped" doc comment. */
  isGroup: boolean;
  groupCount?: number;
}

export interface CablingNodeGroup {
  node: string;
  rows: CablingRow[];
}

export interface CablingPlan {
  nodes: CablingNodeGroup[];
}

/** Kind vocabulary for "this uplink is a bond" — mirrors switchModel.ts's
 * own private BOND_KINDS (internal/inventory/link.go's Kind constants);
 * duplicated here rather than exported from switchModel.ts because it is a
 * one-line, stable-vocabulary check, not a structural dependency. */
function isBondKind(kind: string): boolean {
  return kind === "bond" || kind === "ovs-bond";
}

function rowFromMember(member: SwitchPortNic, bridgeName: string | undefined, bondLabel: string | undefined): CablingRow {
  const isGroup = member.isGroup === true;
  const hasNeighbor = member.neighbor !== undefined;
  const linkState: CablingLinkState = isGroup ? "grouped" : hasNeighbor ? "discovered" : "unknown";
  return {
    nicRef: member.ref,
    nicLabel: member.label,
    status: member.status,
    bridgeName,
    bondLabel,
    mediaPort: member.mediaPort,
    speedMbps: member.speedMbps,
    duplex: member.duplex,
    linkState,
    farEndSwitch: linkState === "discovered" ? member.neighbor?.label : undefined,
    farEndPort: linkState === "discovered" ? member.neighbor?.port : undefined,
    isGroup,
    groupCount: isGroup ? member.count : undefined,
  };
}

function rowsFromUplink(uplink: SwitchUplink, bridgeName: string | undefined): CablingRow[] {
  const bondLabel = isBondKind(uplink.kind) ? uplink.label : undefined;
  return uplink.members.map((member) => rowFromMember(member, bridgeName, bondLabel));
}

function rowsForGroup(group: NodeSwitchGroup): CablingRow[] {
  const rows: CablingRow[] = [];
  for (const sw of group.switches) {
    for (const uplink of sw.uplinks) {
      rows.push(...rowsFromUplink(uplink, sw.name));
    }
  }
  for (const uplink of group.freePorts) {
    rows.push(...rowsFromUplink(uplink, undefined));
  }
  rows.sort((a, b) => a.nicLabel.localeCompare(b.nicLabel));
  return rows;
}

/** Flattens SwitchTopology (grouped by virtual switch) into a cabling plan
 * (grouped by node, one row per physical NIC). Pure — no fetch, no DOM. */
export function buildCablingPlan(topology: SwitchTopology): CablingPlan {
  return {
    nodes: topology.nodes
      .map((group) => ({ node: group.node, rows: rowsForGroup(group) }))
      .filter((g) => g.rows.length > 0),
  };
}

/** Total NIC rows across every node — the empty-state predicate. */
export function cablingPlanRowCount(plan: CablingPlan): number {
  return plan.nodes.reduce((sum, g) => sum + g.rows.length, 0);
}

/** Count of rows whose far end is not discovered (excludes "grouped" rows,
 * which are a display artifact of collapsing, not a real unknown link) —
 * the summary line the view's header renders. */
export function cablingPlanUnknownCount(plan: CablingPlan): number {
  let n = 0;
  for (const g of plan.nodes) {
    for (const row of g.rows) {
      if (row.linkState === "unknown") n += 1;
    }
  }
  return n;
}

const PORT_W = 108;
const PORT_H = 46;
const PORT_GAP = 10;
const ROW_GAP = 34;
const NODE_HEADER_H = 22;
const PADDING = 16;

/** One drawn NIC box in the diagram layout — positions only, no colour: the
 * caller (CablingPlanView) renders this against Tailwind `fill-*`/`stroke-*`
 * classes (dark: variants included) so the diagram picks up the app's own
 * theme-aware, already-AA-checked color tokens instead of a second,
 * hand-picked palette baked into this pure module. */
export interface CablingDiagramPort {
  ref: string;
  label: string;
  linkState: CablingLinkState;
  /** Pre-formatted single line for the far-end sub-label — "sw · port" when
   * discovered, "N NICs grouped" when grouped, "Not discovered" when
   * unknown. Never blank: this is the exact detail this diagram exists to
   * never silently drop. */
  farEndLabel: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface CablingDiagramNodeRow {
  node: string;
  labelX: number;
  labelY: number;
  ports: CablingDiagramPort[];
}

export interface CablingDiagramLayout {
  width: number;
  height: number;
  rows: CablingDiagramNodeRow[];
}

function farEndLabelFor(row: CablingRow): string {
  if (row.linkState === "discovered") {
    return `${row.farEndSwitch ?? "?"} ${row.farEndPort ?? ""}`.trim();
  }
  if (row.linkState === "grouped") {
    return `${String(row.groupCount ?? "N")} NICs grouped`;
  }
  return "Not discovered";
}

/**
 * Computes a self-contained "patch panel" layout: one row per cluster node,
 * one small port box per physical NIC, positioned left-to-right — pure
 * geometry and pre-formatted labels, no color and no DOM. Deliberately NOT
 * a rack elevation — LLDP names a far end's identity and port, never a
 * U-position, a cable length, or a colour, and CLAUDE.md is explicit that
 * this feature must not invent a rack model pretending to know what it
 * cannot (see planning/tasks/phase-39.md's T-3907 card). This is an
 * enhancement over the table (CablingPlanView's table is the accessible
 * source of truth); the view renders this layout `aria-hidden`.
 */
export function computeCablingDiagramLayout(plan: CablingPlan): CablingDiagramLayout {
  let maxCols = 0;
  for (const g of plan.nodes) maxCols = Math.max(maxCols, g.rows.length);
  const width = PADDING * 2 + Math.max(1, maxCols) * (PORT_W + PORT_GAP) - (maxCols > 0 ? PORT_GAP : 0);
  const rowH = NODE_HEADER_H + PORT_H + ROW_GAP;
  const height = PADDING * 2 + Math.max(1, plan.nodes.length) * rowH;

  const rows: CablingDiagramNodeRow[] = plan.nodes.map((group, rowIndex) => {
    const y = PADDING + rowIndex * rowH;
    const ports: CablingDiagramPort[] = group.rows.map((row, colIndex) => ({
      ref: row.nicRef,
      label: row.nicLabel,
      linkState: row.linkState,
      farEndLabel: farEndLabelFor(row),
      x: PADDING + colIndex * (PORT_W + PORT_GAP),
      y: y + NODE_HEADER_H,
      width: PORT_W,
      height: PORT_H,
    }));
    return { node: group.node, labelX: PADDING, labelY: y + 14, ports };
  });

  return { width, height, rows };
}
