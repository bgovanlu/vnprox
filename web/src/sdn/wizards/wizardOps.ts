// Turns each wizard's finished params into the changeset draft ops T-403's
// task card requires ("output = changeset draft, uses T-402's sdn.* ops,
// never a direct apply, no exceptions"). Every op is built through T-402's
// own opBuilders.ts functions — never a hand-built Op literal — so the
// wire shape (internal/change/params_sdn.go) stays honored in exactly the
// one place it always has been.
import type { Op } from "../../api/types";
import {
  buildSdnApplyOp,
  buildSdnSubnetCreateOp,
  buildSdnVnetCreateOp,
  buildSdnZoneCreateOp,
  type SdnSubnetFormValues,
  type SdnVnetFormValues,
  type SdnZoneFormValues,
} from "../../changesets/opBuilders";
import type { EvpnZoneParams, QinqZoneParams, SimpleZoneParams, VlanZoneParams, VxlanZoneParams } from "./previewEntities";

function zoneTarget(zoneId: string): string {
  return `sdn-zone::${zoneId}`;
}
function vnetTarget(zoneId: string, vnetId: string): string {
  return `sdn-vnet::${zoneId}/${vnetId}`;
}
function subnetTarget(cidr: string): string {
  return `sdn-subnet::${cidr}`;
}

const ZONE_FORM_DEFAULTS: Omit<SdnZoneFormValues, "type" | "bridge" | "nodes" | "vrfVxlan" | "mtu"> = {
  controller: "",
  exitNodes: [],
  peers: [],
};

function subnetOps(zoneId: string, vnetId: string, cidr: string, gateway: string, snat: boolean): Op[] {
  if (!cidr) return [];
  const form: SdnSubnetFormValues = {
    vnet: `${zoneId}/${vnetId}`,
    cidr,
    gateway,
    dnsZonePrefix: "",
    dhcpRanges: [],
    snat,
  };
  return [buildSdnSubnetCreateOp(subnetTarget(cidr), form)];
}

function vnetOp(zoneId: string, vnetId: string, alias: string, tag: number, vlanAware = false): Op {
  const form: SdnVnetFormValues = { zone: zoneId, alias, tag, vlanAware };
  return buildSdnVnetCreateOp(vnetTarget(zoneId, vnetId), form);
}

// --- 1. Simple ------------------------------------------------------------

export function buildSimpleZoneOps(p: SimpleZoneParams): Op[] {
  const zoneForm: SdnZoneFormValues = {
    ...ZONE_FORM_DEFAULTS,
    type: "simple",
    bridge: p.bridgeName,
    nodes: p.memberNodes,
    vrfVxlan: 0,
    mtu: 0,
  };
  return [
    buildSdnZoneCreateOp(zoneTarget(p.zoneId), zoneForm),
    vnetOp(p.zoneId, p.vnetId, p.vnetAlias, 0),
    ...subnetOps(p.zoneId, p.vnetId, p.subnetCidr, p.subnetGateway ?? "", p.snat ?? false),
    buildSdnApplyOp(),
  ];
}

// --- 2. VLAN ----------------------------------------------------------

export function buildVlanZoneOps(p: VlanZoneParams): Op[] {
  const zoneForm: SdnZoneFormValues = {
    ...ZONE_FORM_DEFAULTS,
    type: "vlan",
    bridge: p.bridgeName,
    nodes: p.memberNodes,
    vrfVxlan: 0,
    mtu: 0,
  };
  return [
    buildSdnZoneCreateOp(zoneTarget(p.zoneId), zoneForm),
    vnetOp(p.zoneId, p.vnetId, p.vnetAlias, p.vid),
    ...subnetOps(p.zoneId, p.vnetId, p.subnetCidr, p.subnetGateway ?? "", p.snat ?? false),
    buildSdnApplyOp(),
  ];
}

// --- 3. QinQ ------------------------------------------------------------

/** The customer VID becomes the VNet's own tag (the field this codebase's
 * op vocabulary actually has — see EvpnZoneWizard's sibling doc comment on
 * QinqZoneWizard.tsx for the flagged service-VID limitation: there is no
 * zone-level service-VLAN field in SdnZoneCreateParams yet). */
export function buildQinqZoneOps(p: QinqZoneParams): Op[] {
  const zoneForm: SdnZoneFormValues = {
    ...ZONE_FORM_DEFAULTS,
    type: "qinq",
    bridge: p.bridgeName,
    nodes: p.memberNodes,
    vrfVxlan: 0,
    mtu: 0,
  };
  return [
    buildSdnZoneCreateOp(zoneTarget(p.zoneId), zoneForm),
    vnetOp(p.zoneId, p.vnetId, p.vnetAlias, p.customerVid),
    ...subnetOps(p.zoneId, p.vnetId, p.subnetCidr, p.subnetGateway ?? "", p.snat ?? false),
    buildSdnApplyOp(),
  ];
}

// --- 4. VXLAN -----------------------------------------------------------

export function buildVxlanZoneOps(p: VxlanZoneParams): Op[] {
  const peers = p.memberNodes.map((n) => p.peers[n]).filter((addr): addr is string => Boolean(addr));
  const zoneForm: SdnZoneFormValues = {
    ...ZONE_FORM_DEFAULTS,
    type: "vxlan",
    bridge: "",
    nodes: p.memberNodes,
    peers,
    vrfVxlan: 0,
    mtu: p.mtu,
  };
  return [
    buildSdnZoneCreateOp(zoneTarget(p.zoneId), zoneForm),
    vnetOp(p.zoneId, p.vnetId, p.vnetAlias, p.vni),
    ...subnetOps(p.zoneId, p.vnetId, p.subnetCidr, p.subnetGateway ?? "", p.snat ?? false),
    buildSdnApplyOp(),
  ];
}

// --- 5. EVPN ------------------------------------------------------------

export function buildEvpnZoneOps(p: EvpnZoneParams): Op[] {
  const peers = p.peerAddresses.filter((addr) => addr.trim().length > 0);
  const zoneForm: SdnZoneFormValues = {
    controller: p.controller,
    exitNodes: p.exitNodes,
    peers,
    type: "evpn",
    bridge: "",
    nodes: p.memberNodes,
    vrfVxlan: p.vrfVxlan,
    mtu: 0,
  };
  return [
    buildSdnZoneCreateOp(zoneTarget(p.zoneId), zoneForm),
    vnetOp(p.zoneId, p.vnetId, p.vnetAlias, 0),
    ...subnetOps(p.zoneId, p.vnetId, p.subnetCidr, p.subnetGateway ?? "", p.snat ?? false),
    buildSdnApplyOp(),
  ];
}
