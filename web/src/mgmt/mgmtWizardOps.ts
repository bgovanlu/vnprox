// SPDX-License-Identifier: Apache-2.0

// Pure Op-construction helpers for T-703's management-redundancy wizard —
// the frontend counterpart of internal/change/mgmtwizard_test.go's golden
// ops. Every flow is "interlock-clean by construction" (docs/security.md's
// Safety interlocks; T-702-analysis.md's "Reconciling wizard-edits-mgmt
// with interlock-blocks-mgmt"): the management IP *value* and its physical
// connectivity survive the changeset's net effect, so T-203's net-effect
// interlock — armed unchanged behind the wizard — validates them clean.
// There is no interlock override anywhere here; re-addressing the mgmt IP
// is out of scope by construction (the builders never emit a new address).
//
// Framework-free (no React) so they are directly Vitest-able against the
// same JSON internal/change decodes.
import type { Op } from "../api/types";

/** A parsed inventory Ref triplet ("kind:node:id"). */
export interface ParsedRef {
  kind: string;
  node: string;
  id: string;
}

/** Parses a "kind:node:id" ref. `id` may itself contain colons is NOT a
 * case here — every interfaces-namespace ref (physnic/bond/bridge/vlan) has
 * a colon-free id — so a simple 3-way split is correct and total. Returns
 * undefined for a malformed ref rather than throwing, so callers computing
 * candidate sets degrade gracefully. */
export function parseRef(ref: string): ParsedRef | undefined {
  const parts = ref.split(":");
  if (parts.length !== 3) return undefined;
  return { kind: parts[0] ?? "", node: parts[1] ?? "", id: parts[2] ?? "" };
}

/** Bond mode choices the wizard offers, with the plain-English guidance
 * T-703's card calls for (default active-backup when LLDP can't confirm a
 * LACP peer; explicit "your switch must be configured for LACP first" for
 * 802.3ad). Kept here (not strings.ts) because the value is load-bearing —
 * it becomes the bond-mode op field. */
export type BondMode = "active-backup" | "802.3ad";

export interface BondUplinkInput {
  node: string;
  /** The management bridge carrier ref ("bridge:<node>:vmbr0"). */
  bridgeRef: string;
  /** The single physical NIC currently wired into the bridge (from the
   * mgmt path) — becomes the bond's first slave. */
  currentPortNic: string;
  /** The chosen second NIC to add for redundancy. */
  candidateNic: string;
  /** New bond's name (e.g. "bond0"). */
  bondName: string;
  mode: BondMode;
}

/** Flow A — "bond the management uplink": migrate the mgmt bridge's single
 * physical port into a fresh two-slave bond, preserving the bridge's
 * address by never touching it (net effect: address stays on vmbr0, final
 * port count 1 → validates clean). Order matters: remove the bare NIC
 * port, create the bond enslaving it + the candidate, then add the bond as
 * the bridge port. */
export function buildBondUplinkOps(input: BondUplinkInput): Op[] {
  const { node, bridgeRef, currentPortNic, candidateNic, bondName, mode } = input;
  const bondTarget = `bond:${node}:${bondName}`;
  const params: Record<string, unknown> = {
    mode,
    slaves: [currentPortNic, candidateNic],
    miimon: 100,
  };
  if (mode === "802.3ad") {
    // LACP defaults that match real-PVE guidance for a mgmt bond; the
    // wizard's copy warns the switch must be configured for LACP first.
    params.lacpRate = "fast";
    params.xmitHashPolicy = "layer3+4";
  }
  return [
    { op: "bridge.port.remove", target: bridgeRef, params: { port: currentPortNic } },
    { op: "bond.create", target: bondTarget, params: params },
    { op: "bridge.port.add", target: bridgeRef, params: { port: bondName } },
  ];
}

export interface BondSlaveUpdateInput {
  /** The existing mgmt-path bond's ref ("bond:<node>:bond0"). */
  bondRef: string;
  /** The bond's complete new slave list (add or replace). Must keep at
   * least the existing slaves' connectivity — the wizard only ever appends
   * a candidate or swaps one out for another, never empties the list. */
  slaves: string[];
}

/** Flow B — "add/replace a slave in an existing mgmt-path bond": a single
 * bond.update. Net effect never removes the bond or its address's path, so
 * it validates clean. */
export function buildBondSlaveUpdateOps(input: BondSlaveUpdateInput): Op[] {
  return [{ op: "bond.update", target: input.bondRef, params: { slaves: input.slaves } }];
}

export interface DedicatedVlanInput {
  node: string;
  /** The current carrier ref losing the address — a bridge or an existing
   * VLAN sub-interface ("bridge:<node>:vmbr0" / "vlan:<node>:vmbr0.30"). */
  oldCarrierRef: string;
  /** The parent bridge the new VLAN sub-interface rides on. */
  parentBridge: string;
  /** The new VLAN id. */
  vid: number;
  /** The new sub-interface's name (e.g. "vmbr0.40"). */
  vlanName: string;
  /** The node's *existing* management address(es), carried over verbatim —
   * this is what keeps the mgmt IP value unchanged (interlock-clean). */
  addresses: string[];
  /** The node's existing default gateway, carried over. */
  gateway: string;
  mtu?: number;
}

/** Flow C — "dedicated management VLAN interface": create a VLAN
 * sub-interface carrying the node's *existing* mgmt address + gateway, and
 * strip the address+route from the old carrier in the same changeset. Order
 * matters — remove first, create second — so the address-overlap
 * referential check never sees both carriers hold the address at once,
 * while the net effect preserves it (interlock-clean). */
export function buildDedicatedVlanOps(input: DedicatedVlanInput): Op[] {
  const { node, oldCarrierRef, parentBridge, vid, vlanName, addresses, gateway, mtu } = input;
  const vlanTarget = `vlan:${node}:${vlanName}`;
  const createParams: Record<string, unknown> = { parent: parentBridge, vid, addresses };
  if (gateway) createParams.gateway = gateway;
  if (mtu) createParams.mtu = mtu;
  return [
    { op: "iface.update", target: oldCarrierRef, params: { removeAddress: true, removeGateway: true } },
    { op: "vlan.create", target: vlanTarget, params: createParams },
  ];
}

/** Computes the next free "bondN" / "vmbrN.<vid>"-free name given the names
 * already present on a node — a small deterministic helper the wizard uses
 * to propose a default that won't collide. */
export function nextFreeBondName(existingIfaceNames: string[]): string {
  const taken = new Set(existingIfaceNames);
  for (let i = 0; i < 100; i++) {
    const name = `bond${String(i)}`;
    if (!taken.has(name)) return name;
  }
  return "bond0";
}
