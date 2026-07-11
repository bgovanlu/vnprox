// Pure Op-construction helpers: every entity editor (BridgeEditor,
// BondEditor, VlanEditor, InterfaceEditor) and every map drag-drop
// interaction funnels through one of these instead of hand-building an
// `Op` object at the call site, so the wire shape (internal/change/
// params_*.go) is honored in exactly one place. Framework-free (no React
// import) so they're directly Vitest-able.
import type {
  BondCreateParams,
  BondUpdateParams,
  BridgeCreateParams,
  BridgeUpdateParams,
  GuestNicUpdateParams,
  IfaceUpdateParams,
  Op,
  SdnSubnetCreateParams,
  SdnSubnetUpdateParams,
  SdnVnetCreateParams,
  SdnVnetUpdateParams,
  SdnZoneCreateParams,
  SdnZoneUpdateParams,
  VidRange,
  VlanCreateParams,
  VlanUpdateParams,
} from "../api/types";

export interface BridgeFormValues {
  ports: string[];
  vlanAware: boolean;
  vids: VidRange[];
  addresses: string[];
  gateway: string;
  mtu: number;
  stp: boolean;
  comments: string;
}

export function buildBridgeCreateOp(target: string, form: BridgeFormValues): Op {
  const params: BridgeCreateParams = {
    ports: form.ports,
    vlanAware: form.vlanAware,
    vids: form.vlanAware ? form.vids : undefined,
    addresses: form.addresses.length > 0 ? form.addresses : undefined,
    gateway: form.gateway || undefined,
    mtu: form.mtu || undefined,
    stp: form.stp,
    comments: form.comments || undefined,
  };
  return { op: "bridge.create", target, params };
}

/** A partial bridge.update carrying only the fields that differ from
 * `initial` — port membership changes go through bridge.port.add/remove
 * instead (params_bridge.go's doc comment: a bridge.update can never
 * touch Ports), so `form.ports` is deliberately never read here. */
export function buildBridgeUpdateOp(target: string, initial: BridgeFormValues, form: BridgeFormValues): Op {
  const params: BridgeUpdateParams = {};
  if (form.vlanAware !== initial.vlanAware) params.vlanAware = form.vlanAware;
  if (JSON.stringify(form.vids) !== JSON.stringify(initial.vids)) params.vids = form.vids;
  if (JSON.stringify(form.addresses) !== JSON.stringify(initial.addresses)) params.addresses = form.addresses;
  if (form.gateway !== initial.gateway) params.gateway = form.gateway;
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  if (form.stp !== initial.stp) params.stp = form.stp;
  if (form.comments !== initial.comments) params.comments = form.comments;
  return { op: "bridge.update", target, params };
}

export function buildBridgeDeleteOp(target: string): Op {
  return { op: "bridge.delete", target, params: {} };
}

export function buildBridgePortAddOp(target: string, port: string): Op {
  return { op: "bridge.port.add", target, params: { port } };
}

export function buildBridgePortRemoveOp(target: string, port: string): Op {
  return { op: "bridge.port.remove", target, params: { port } };
}

/** One reattachment op per currently-attached guest NIC, for the bridge
 * delete-with-reattach flow (docs/features/change-management.md §5:
 * "deleting a bridge with attached guests requires choosing a reattachment
 * target ... generates the guest ops in the same changeset"). */
export function buildGuestReattachOps(guestNicRefs: string[], newBridgeOrVnet: string): Op[] {
  return guestNicRefs.map((ref) => buildGuestNicUpdateOp(ref, { bridgeOrVnet: newBridgeOrVnet }));
}

export interface BondFormValues {
  mode: string;
  slaves: string[];
  lacpRate: string;
  xmitHashPolicy: string;
  miimon: number;
  mtu: number;
  comments: string;
}

export function buildBondCreateOp(target: string, form: BondFormValues): Op {
  const params: BondCreateParams = {
    mode: form.mode,
    slaves: form.slaves,
    lacpRate: form.lacpRate || undefined,
    xmitHashPolicy: form.xmitHashPolicy || undefined,
    miimon: form.miimon || undefined,
    mtu: form.mtu || undefined,
    comments: form.comments || undefined,
  };
  return { op: "bond.create", target, params };
}

export function buildBondUpdateOp(target: string, initial: BondFormValues, form: BondFormValues): Op {
  const params: BondUpdateParams = {};
  if (form.mode !== initial.mode) params.mode = form.mode;
  if (JSON.stringify(form.slaves) !== JSON.stringify(initial.slaves)) params.slaves = form.slaves;
  if (form.lacpRate !== initial.lacpRate) params.lacpRate = form.lacpRate;
  if (form.xmitHashPolicy !== initial.xmitHashPolicy) params.xmitHashPolicy = form.xmitHashPolicy;
  if (form.miimon !== initial.miimon) params.miimon = form.miimon;
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  if (form.comments !== initial.comments) params.comments = form.comments;
  return { op: "bond.update", target, params };
}

export function buildBondDeleteOp(target: string): Op {
  return { op: "bond.delete", target, params: {} };
}

export interface VlanFormValues {
  parent: string;
  vid: number;
  addresses: string[];
  mtu: number;
}

export function buildVlanCreateOp(target: string, form: VlanFormValues): Op {
  const params: VlanCreateParams = {
    parent: form.parent,
    vid: form.vid,
    addresses: form.addresses.length > 0 ? form.addresses : undefined,
    mtu: form.mtu || undefined,
  };
  return { op: "vlan.create", target, params };
}

export function buildVlanUpdateOp(target: string, initial: VlanFormValues, form: VlanFormValues): Op {
  const params: VlanUpdateParams = {};
  if (JSON.stringify(form.addresses) !== JSON.stringify(initial.addresses)) params.addresses = form.addresses;
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  return { op: "vlan.update", target, params };
}

export function buildVlanDeleteOp(target: string): Op {
  return { op: "vlan.delete", target, params: {} };
}

export interface IfaceFormValues {
  mtu: number;
  comments: string;
  addresses: string[];
  gateway: string;
  autostart: boolean;
}

export function buildIfaceUpdateOp(target: string, initial: IfaceFormValues, form: IfaceFormValues): Op {
  const params: IfaceUpdateParams = {};
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  if (form.comments !== initial.comments) params.comments = form.comments;
  if (JSON.stringify(form.addresses) !== JSON.stringify(initial.addresses)) params.addresses = form.addresses;
  if (form.gateway !== initial.gateway) params.gateway = form.gateway;
  if (form.autostart !== initial.autostart) params.autostart = form.autostart;
  return { op: "iface.update", target, params };
}

// --- SDN zone/vnet/subnet (T-402, docs/features/sdn.md §1/§4) -------------

export interface SdnZoneFormValues {
  type: string;
  bridge: string;
  controller: string;
  nodes: string[];
  vrfVxlan: number;
  mtu: number;
}

/** target is `sdn-zone::<id>` (cluster-scoped: no node segment — Ref.Node
 * is empty for every sdn-* Kind, internal/inventory/entity.go's Ref doc
 * comment). */
export function buildSdnZoneCreateOp(target: string, form: SdnZoneFormValues): Op {
  const params: SdnZoneCreateParams = {
    type: form.type,
    bridge: form.bridge || undefined,
    nodes: form.nodes.length > 0 ? form.nodes : undefined,
    vrfVxlan: form.vrfVxlan || undefined,
    mtu: form.mtu || undefined,
  };
  return { op: "sdn.zone.create", target, params };
}

export function buildSdnZoneUpdateOp(target: string, initial: SdnZoneFormValues, form: SdnZoneFormValues): Op {
  const params: SdnZoneUpdateParams = {};
  if (form.bridge !== initial.bridge) params.bridge = form.bridge;
  if (form.controller !== initial.controller) params.controller = form.controller;
  if (JSON.stringify(form.nodes) !== JSON.stringify(initial.nodes)) params.nodes = form.nodes;
  if (form.vrfVxlan !== initial.vrfVxlan) params.vrfVxlan = form.vrfVxlan;
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  return { op: "sdn.zone.update", target, params };
}

export function buildSdnZoneDeleteOp(target: string): Op {
  return { op: "sdn.zone.delete", target, params: {} };
}

export interface SdnVnetFormValues {
  zone: string;
  alias: string;
  tag: number;
  vlanAware: boolean;
}

/** target is `sdn-vnet::<zone>/<vnetId>` — internal/change's own Ref.ID
 * convention for vnets (params_sdn.go's SdnVnetCreateParams doc comment). */
export function buildSdnVnetCreateOp(target: string, form: SdnVnetFormValues): Op {
  const params: SdnVnetCreateParams = {
    zone: form.zone,
    alias: form.alias || undefined,
    tag: form.tag || undefined,
    vlanAware: form.vlanAware,
  };
  return { op: "sdn.vnet.create", target, params };
}

export function buildSdnVnetUpdateOp(target: string, initial: SdnVnetFormValues, form: SdnVnetFormValues): Op {
  const params: SdnVnetUpdateParams = {};
  if (form.alias !== initial.alias) params.alias = form.alias;
  if (form.tag !== initial.tag) params.tag = form.tag;
  if (form.vlanAware !== initial.vlanAware) params.vlanAware = form.vlanAware;
  return { op: "sdn.vnet.update", target, params };
}

export function buildSdnVnetDeleteOp(target: string): Op {
  return { op: "sdn.vnet.delete", target, params: {} };
}

export interface SdnSubnetFormValues {
  vnet: string;
  cidr: string;
  gateway: string;
  dnsZonePrefix: string;
  dhcpRanges: string[];
  snat: boolean;
}

/** target is `sdn-subnet::<cidr>` — the subnet's id *is* its CIDR
 * (docs/data-model.md's SdnSubnet.ID doc comment). */
export function buildSdnSubnetCreateOp(target: string, form: SdnSubnetFormValues): Op {
  const params: SdnSubnetCreateParams = {
    vnet: form.vnet,
    cidr: form.cidr,
    gateway: form.gateway || undefined,
    dnsZonePrefix: form.dnsZonePrefix || undefined,
    dhcpRanges: form.dhcpRanges.length > 0 ? form.dhcpRanges : undefined,
    snat: form.snat,
  };
  return { op: "sdn.subnet.create", target, params };
}

export function buildSdnSubnetUpdateOp(target: string, initial: SdnSubnetFormValues, form: SdnSubnetFormValues): Op {
  const params: SdnSubnetUpdateParams = {};
  if (form.gateway !== initial.gateway) params.gateway = form.gateway;
  if (form.dnsZonePrefix !== initial.dnsZonePrefix) params.dnsZonePrefix = form.dnsZonePrefix;
  if (JSON.stringify(form.dhcpRanges) !== JSON.stringify(initial.dhcpRanges)) params.dhcpRanges = form.dhcpRanges;
  if (form.snat !== initial.snat) params.snat = form.snat;
  return { op: "sdn.subnet.update", target, params };
}

export function buildSdnSubnetDeleteOp(target: string): Op {
  return { op: "sdn.subnet.delete", target, params: {} };
}

/** The trailing `sdn.apply` step every SDN-carrying changeset needs
 * (docs/data-model.md §3: ordered last when present) — no target
 * (internal/change/op.go's `noTargetOps`). */
export function buildSdnApplyOp(): Op {
  return { op: "sdn.apply", target: undefined, params: {} };
}

export function buildGuestNicUpdateOp(target: string, params: GuestNicUpdateParams): Op {
  return { op: "guest.nic.update", target, params };
}

/** Bulk reattach/retag: one op per selected guest NIC ref, all sharing the
 * same param patch (docs/features/change-management.md §6: "select N
 * guests ... one changeset moving all, with per-guest hotplug attempted
 * and per-guest results reported"). */
export function buildBulkGuestNicOps(refs: string[], params: GuestNicUpdateParams): Op[] {
  return refs.map((ref) => buildGuestNicUpdateOp(ref, params));
}
