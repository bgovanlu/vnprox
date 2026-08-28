// SPDX-License-Identifier: Apache-2.0

// T-4205: the typed entity-kind -> pictogram lookup, so a caller (a future
// EntityNode.tsx/table-row/empty-state adoption card) can render a glyph
// with `PICTOGRAMS[kind]` instead of a per-call-site switch statement.
//
// PictogramKind is a curated set, not a 1:1 mirror of every string
// internal/inventory/ref.go's Kind enum defines: it covers the roadmap
// card's named minimum (bridge, bond, VLAN, VXLAN, fabric, zone, WireGuard
// peer, physical NIC) plus the "confirm each against the code" list
// (node/host, guest/VM, vnet, subnet, gateway, switch, port, route,
// firewall rule/group — see each glyph module's doc comment for where it
// was confirmed), plus one kind the card didn't name but EntityNode.tsx's
// own `KIND_ACCENT` map showed up as real and separately rendered:
// guest-nic. It deliberately excludes the model's more niche app-owned/
// infra Kinds not named by the card (qos-shape, nat-rule, vf, ceph-osd,
// pbs-host, sdn-controller, sdn-ipam, sdn-dns-zone/record, tc-mirror) —
// extending this map for one of those is a small, additive change for
// whichever later card adopts it, following the same pattern.
//
// Every value here that corresponds to a real internal/inventory Kind uses
// that Kind's exact string (so `PICTOGRAMS[topologyNode.kind]` works
// without translation); the handful of concepts with no Kind of their own
// (vxlan, gateway, switch, port, firewall-group) use a plain descriptive
// string instead, documented per entry below.
import { BondIcon, BridgeIcon, OvsBondIcon, OvsBridgeIcon, VlanIcon } from "./glyphs.linklayer";
import { GuestIcon, GuestNicIcon, NodeIcon, PhysNicIcon, PortIcon, SwitchIcon } from "./glyphs.hosts";
import { FabricIcon, GatewayIcon, LldpNeighborIcon, SubnetIcon, VnetIcon, VxlanIcon, ZoneIcon } from "./glyphs.sdn";
import { FirewallGroupIcon, FirewallRuleIcon, RouteIcon, WgPeerIcon, WgTunnelIcon } from "./glyphs.security";
import { UnknownPictogram } from "./UnknownPictogram";
import type { PictogramProps } from "./Icon";
import type { ReactElement } from "react";

export type PictogramKind =
  // internal/inventory/ref.go Kind strings, verbatim:
  | "node"
  | "physnic"
  | "bond"
  | "ovs-bond"
  | "bridge"
  | "ovs-bridge"
  | "vlan"
  | "sdn-zone"
  | "sdn-vnet"
  | "sdn-subnet"
  | "sdn-fabric"
  | "guest"
  | "guest-nic"
  | "lldp-neighbor"
  | "wg-tunnel"
  | "wg-peer"
  | "static-route"
  | "fw-ruleset"
  // Real, code-confirmed concepts with no inventory Kind of their own:
  | "vxlan" // SdnZone.type === "vxlan" (api/types.ts)
  | "gateway" // SdnSubnet.gateway et al. (api/types.ts)
  | "switch" // physical switch device (internal/switchdrv, SwitchFaceplate.tsx)
  | "port" // a bare port/jack (PortBody.tsx's PortJack, PortsPage.tsx)
  | "firewall-group"; // ObjectUsageView.kind === "group" (api/types.ts)

export type PictogramComponent = (props: PictogramProps) => ReactElement;

/** Every declared `PictogramKind` maps to a component — enforced by
 * `Record<PictogramKind, ...>` below (a kind added to the union without an
 * entry here fails `tsc`) and re-asserted at the value level by
 * Pictogram.test.tsx (so a future refactor that quietly drops an entry from
 * this literal, without touching the type, still fails a test rather than
 * silently losing a glyph). */
export const PICTOGRAMS: Record<PictogramKind, PictogramComponent> = {
  node: NodeIcon,
  physnic: PhysNicIcon,
  bond: BondIcon,
  "ovs-bond": OvsBondIcon,
  bridge: BridgeIcon,
  "ovs-bridge": OvsBridgeIcon,
  vlan: VlanIcon,
  vxlan: VxlanIcon,
  "sdn-zone": ZoneIcon,
  "sdn-vnet": VnetIcon,
  "sdn-subnet": SubnetIcon,
  "sdn-fabric": FabricIcon,
  gateway: GatewayIcon,
  guest: GuestIcon,
  "guest-nic": GuestNicIcon,
  "lldp-neighbor": LldpNeighborIcon,
  "wg-tunnel": WgTunnelIcon,
  "wg-peer": WgPeerIcon,
  "static-route": RouteIcon,
  "fw-ruleset": FirewallRuleIcon,
  "firewall-group": FirewallGroupIcon,
  switch: SwitchIcon,
  port: PortIcon,
};

/** Every kind this registry declares, in the fixed order above — the
 * iteration surface Pictogram.test.tsx renders across, and a convenience
 * for any future gallery/legend UI. */
export const PICTOGRAM_KINDS = Object.keys(PICTOGRAMS) as PictogramKind[];

/** Resolves a `kind` string (as read off a live TopologyNode/EntityDetail,
 * which carries `kind` as a plain `string` per docs/api.md — server-
 * controlled and open-ended, not narrowed to `PictogramKind` at the API
 * boundary) to its pictogram component, falling back to
 * `UnknownPictogram` for anything this registry doesn't cover. This is the
 * one intended entry point for a non-literal, run-time `kind` value;
 * `PICTOGRAMS[kind]` directly is fine only when `kind` is already known to
 * be a `PictogramKind` (e.g. iterating `PICTOGRAM_KINDS`). */
export function getPictogram(kind: string): PictogramComponent {
  return Object.hasOwn(PICTOGRAMS, kind) ? PICTOGRAMS[kind as PictogramKind] : UnknownPictogram;
}
