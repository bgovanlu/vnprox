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
  /** OVS-only: the OVS bridge this bond attaches to (BondCreateParams.bridge).
   * Ignored for a plain "bond:..." target. */
  ovsBridge?: string;
}

/** target's kind prefix ("ovs-bond:" vs "bond:") decides whether this is an
 * OVS bond create — see BridgeEditor/BondEditor's kind selector, which
 * constructs target accordingly. */
export function buildBondCreateOp(target: string, form: BondFormValues): Op {
  const params: BondCreateParams = {
    mode: form.mode,
    slaves: form.slaves,
    lacpRate: form.lacpRate || undefined,
    xmitHashPolicy: form.xmitHashPolicy || undefined,
    miimon: form.miimon || undefined,
    mtu: form.mtu || undefined,
    comments: form.comments || undefined,
    bridge: target.startsWith("ovs-bond:") ? (form.ovsBridge ?? undefined) : undefined,
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
  /** True for an OVS Int Port instead of a plain 802.1q sub-interface (see
   * VlanCreateParams.ovs). Drives whether `trunks` is sent. */
  ovs?: boolean;
  /** OVS-only trunk VLAN ranges. */
  trunks?: VidRange[];
}

export function buildVlanCreateOp(target: string, form: VlanFormValues): Op {
  const params: VlanCreateParams = {
    parent: form.parent,
    vid: form.vid,
    addresses: form.addresses.length > 0 ? form.addresses : undefined,
    mtu: form.mtu || undefined,
    ovs: form.ovs ?? undefined,
    trunks: form.ovs && form.trunks && form.trunks.length > 0 ? form.trunks : undefined,
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
