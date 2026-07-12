// Pure projection for the "virtual switch" view (docs/features/topology.md
// §2: the switch/faceplate rendering of the same GET /topology graph the
// elk graph view renders). A Proxmox bridge *is* a virtual switch, so this
// re-shapes the flat nodes/edges list into one faceplate per bridge:
//
//   - uplinks   : bonds and physical NICs that are `port-of` the bridge
//                 (a bond expands to its `enslaved-by` member NICs, each
//                 carrying the LACP/MII "active" flag and, when known, the
//                 LLDP neighbor on the other end of the wire);
//   - vlans     : `.N` sub-interfaces `tagged-on` the bridge;
//   - ports     : guest NICs `attached-to` the bridge (access ports), plus
//                 the synthetic guest-group pill for a collapsed bridge
//                 (internal/topology/collapse.go) rendered as one "+N" port;
//   - vnets     : SDN VNets that `realize` onto the bridge.
//
// Deliberately framework-free (no React, no @xyflow/react) so it stays
// exhaustively Vitest-able without rendering — the same discipline
// projection.ts follows. SwitchView.tsx is the only consumer.
import type { EntityStatus, TopologyEdge, TopologyNode } from "../api/types";
import { isGuestGroupId } from "./projection";

// Edge/entity kind vocabularies mirror internal/inventory/link.go and the
// Kind constants exactly (this module reads the strings the backend emits,
// never invents its own).
const BRIDGE_KINDS = new Set(["bridge", "ovs-bridge"]);
const BOND_KINDS = new Set(["bond", "ovs-bond"]);

/** A physical port participating in an uplink: either a bond member NIC or a
 * bare NIC that is itself a bridge port. `active` is only meaningful for a
 * bond member (the LACP/MII slave state from the `enslaved-by` edge's
 * "active" badge); it is always true for a bare uplink NIC. */
export interface SwitchPortNic {
  ref: string;
  label: string;
  status: EntityStatus;
  active: boolean;
  /** The LLDP neighbor seen on this NIC (chassis name + remote port), when
   * lldpd has discovered one — undefined otherwise (docs/features/
   * topology.md §5's "no LLDP data → NICs only"). */
  neighbor?: { label: string; port?: string };
}

/** One uplink module on the faceplate: a bond (with its member NICs) or a
 * single directly-attached NIC (members has exactly that NIC). */
export interface SwitchUplink {
  ref: string;
  label: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
  /** For a bond: its member NICs. For a bare NIC uplink: a single entry for
   * the NIC itself, so the faceplate can render every uplink uniformly. */
  members: SwitchPortNic[];
}

/** One access port: a guest NIC, or the collapsed-guests "+N" pill. */
export interface SwitchAccessPort {
  ref: string;
  label: string;
  status: EntityStatus;
  badges: string[];
  vid?: number;
  vmid?: number;
  /** True for the synthetic guest-group pill (isGuestGroupId(ref)); `count`
   * is then how many guests it stands for (its click expands, reusing the
   * graph view's GuestGroupExpansion). */
  isGroup: boolean;
  count?: number;
}

export interface SwitchVlanIf {
  ref: string;
  label: string;
  status: EntityStatus;
  vid?: number;
}

export interface SwitchVnet {
  ref: string;
  label: string;
  status: EntityStatus;
  tag?: number;
}

/** One bridge, rendered as a virtual switch. */
export interface SwitchModel {
  ref: string;
  node: string;
  name: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
  uplinks: SwitchUplink[];
  vlans: SwitchVlanIf[];
  accessPorts: SwitchAccessPort[];
  vnets: SwitchVnet[];
}

/** All switches on one cluster node, plus any NICs/bonds on that node not
 * wired into a bridge — the faceplate view's equivalent of a NIC sitting
 * loose in the graph, surfaced so nothing silently disappears vs. the
 * graph view. */
export interface NodeSwitchGroup {
  node: string;
  switches: SwitchModel[];
  freePorts: SwitchUplink[];
}

export interface SwitchTopology {
  nodes: NodeSwitchGroup[];
}

/** Reads the numeric VLAN a "vid=N" / "tag=N" badge carries (the two badge
 * spellings project.go emits for a VLAN identity — see projection.ts's
 * badgeCarriesVlan). Returns undefined for any other badge shape (e.g. a
 * "vlans=10-20" trunk range, which is not a single access VID). */
function vidFromBadges(badges: string[]): number | undefined {
  for (const badge of badges) {
    const [key, value] = badge.split("=", 2);
    if ((key === "vid" || key === "tag") && value !== undefined) {
      const n = Number(value);
      if (Number.isFinite(n)) return n;
    }
  }
  return undefined;
}

/** A "port=Te1/0/1" badge's value (LLDP remote port), or undefined. */
function portFromBadges(badges: string[]): string | undefined {
  for (const badge of badges) {
    const [key, value] = badge.split("=", 2);
    if (key === "port" && value !== undefined) return value;
  }
  return undefined;
}

/** The VMID out of a "guest-nic:<node>:<vmid>/<key>" ref (best-effort — a
 * label like "app01/net0" carries the friendly name, this recovers the
 * numeric id for stable sort/display). undefined if the ref isn't shaped
 * like one. */
function vmidFromRef(ref: string): number | undefined {
  const firstColon = ref.indexOf(":");
  const secondColon = ref.indexOf(":", firstColon + 1);
  if (firstColon === -1 || secondColon === -1) return undefined;
  const tail = ref.slice(secondColon + 1);
  const slash = tail.indexOf("/");
  const vmidStr = slash === -1 ? tail : tail.slice(0, slash);
  const n = Number(vmidStr);
  return Number.isFinite(n) ? n : undefined;
}

function pushMap(map: Map<string, string[]>, key: string, value: string): void {
  const list = map.get(key);
  if (list) list.push(value);
  else map.set(key, [value]);
}

/**
 * Projects the flat topology graph into per-node switch faceplates.
 *
 * A single left-to-right pass indexes every edge by kind (nothing here is
 * recursive — the deepest structural walk is bridge → bond → member NIC →
 * LLDP neighbor, three fixed hops, so plain indexed lookups suffice), then a
 * second pass over the bridges assembles each faceplate. NICs enslaved to a
 * bond, or that are themselves a bridge port, are marked "consumed" so the
 * free-ports list is exactly the NICs/bonds the faceplates didn't already
 * account for.
 */
export function buildSwitchModel(nodes: TopologyNode[], edges: TopologyEdge[]): SwitchTopology {
  const byId = new Map<string, TopologyNode>(nodes.map((n) => [n.id, n]));

  // Edge indices, all keyed by the *bridge*/*bond* side so a faceplate can
  // fan out to its parts in one lookup.
  const uplinksOfBridge = new Map<string, string[]>(); // bridge -> [bond|physnic] via port-of
  const membersOfBond = new Map<string, string[]>(); // bond -> [physnic] via enslaved-by
  const vlansOfBridge = new Map<string, string[]>(); // bridge -> [vlan] via tagged-on
  const portsOfBridge = new Map<string, string[]>(); // bridge -> [guest-nic|guest-group] via attached-to
  const vnetsOfBridge = new Map<string, string[]>(); // bridge -> [sdn-vnet] via realizes
  const neighborOfNic = new Map<string, { label: string; port?: string }>(); // physnic -> lldp neighbor
  const activeMemberEdge = new Map<string, boolean>(); // "physnic->bond" -> active flag
  const realizeTagOf = new Map<string, number>(); // "vnet->bridge" -> tag (edge carries the per-node tag)

  for (const e of edges) {
    switch (e.kind) {
      case "port-of":
        pushMap(uplinksOfBridge, e.to, e.from);
        break;
      case "enslaved-by":
        pushMap(membersOfBond, e.to, e.from);
        activeMemberEdge.set(`${e.from}->${e.to}`, e.badges.includes("active"));
        break;
      case "tagged-on":
        pushMap(vlansOfBridge, e.to, e.from);
        break;
      case "attached-to":
        pushMap(portsOfBridge, e.to, e.from);
        break;
      case "realizes": {
        pushMap(vnetsOfBridge, e.to, e.from);
        const tag = vidFromBadges(e.badges);
        if (tag !== undefined) realizeTagOf.set(`${e.from}->${e.to}`, tag);
        break;
      }
      case "lldp-adjacent": {
        const neighbor = byId.get(e.to);
        if (neighbor) neighborOfNic.set(e.from, { label: neighbor.label, port: portFromBadges(neighbor.badges) });
        break;
      }
      default:
        // lldp-port/vtep-peer/etc. have no faceplate slot; the graph view
        // still renders them. Ignored here on purpose.
        break;
    }
  }

  const consumed = new Set<string>(); // NIC/bond refs already shown inside a faceplate

  function nicPort(ref: string, active: boolean): SwitchPortNic {
    consumed.add(ref);
    const n = byId.get(ref);
    return {
      ref,
      label: n?.label ?? ref,
      status: n?.status ?? "unknown",
      active,
      neighbor: neighborOfNic.get(ref),
    };
  }

  function uplinkFrom(ref: string): SwitchUplink | undefined {
    const n = byId.get(ref);
    if (!n) return undefined;
    consumed.add(ref);
    if (BOND_KINDS.has(n.kind)) {
      const members = (membersOfBond.get(ref) ?? [])
        .map((m) => nicPort(m, activeMemberEdge.get(`${m}->${ref}`) ?? false))
        .sort((a, b) => a.label.localeCompare(b.label));
      return { ref, label: n.label, kind: n.kind, status: n.status, badges: n.badges, members };
    }
    // A bare NIC uplink: render it as a one-member uplink so the faceplate
    // treats every uplink uniformly (active is always true for a NIC that is
    // itself the bridge port — there is no slave-state concept).
    return { ref, label: n.label, kind: n.kind, status: n.status, badges: n.badges, members: [nicPort(ref, true)] };
  }

  const bridges = nodes.filter((n) => BRIDGE_KINDS.has(n.kind));
  const switchesByNode = new Map<string, SwitchModel[]>();

  for (const bridge of bridges) {
    const uplinks = (uplinksOfBridge.get(bridge.id) ?? [])
      .map(uplinkFrom)
      .filter((u): u is SwitchUplink => u !== undefined)
      // bonds before bare NICs, then by label — a stable, switch-like order.
      .sort((a, b) => {
        const rank = (u: SwitchUplink) => (BOND_KINDS.has(u.kind) ? 0 : 1);
        return rank(a) - rank(b) || a.label.localeCompare(b.label);
      });

    const vlans: SwitchVlanIf[] = (vlansOfBridge.get(bridge.id) ?? [])
      .map((ref) => byId.get(ref))
      .filter((n): n is TopologyNode => n !== undefined)
      .map((n) => ({ ref: n.id, label: n.label, status: n.status, vid: vidFromBadges(n.badges) }))
      .sort((a, b) => (a.vid ?? 0) - (b.vid ?? 0) || a.label.localeCompare(b.label));

    const accessPorts: SwitchAccessPort[] = (portsOfBridge.get(bridge.id) ?? [])
      .map((ref): SwitchAccessPort | undefined => {
        if (isGuestGroupId(ref)) {
          const n = byId.get(ref);
          return {
            ref,
            label: n?.label ?? "guests",
            status: n?.status ?? "unknown",
            badges: n?.badges ?? [],
            isGroup: true,
            count: n?.collapsedCount,
          };
        }
        const n = byId.get(ref);
        if (!n) return undefined;
        return {
          ref,
          label: n.label,
          status: n.status,
          badges: n.badges,
          vid: vidFromBadges(n.badges),
          vmid: vmidFromRef(ref),
          isGroup: false,
        };
      })
      .filter((p): p is SwitchAccessPort => p !== undefined)
      .sort((a, b) => {
        // Real guests first (by VMID), the collapsed "+N" pill last.
        if (a.isGroup !== b.isGroup) return a.isGroup ? 1 : -1;
        return (a.vmid ?? 0) - (b.vmid ?? 0) || a.label.localeCompare(b.label);
      });

    const vnets: SwitchVnet[] = (vnetsOfBridge.get(bridge.id) ?? [])
      .map((ref) => byId.get(ref))
      .filter((n): n is TopologyNode => n !== undefined)
      .map((n) => ({
        ref: n.id,
        label: n.label,
        status: n.status,
        tag: realizeTagOf.get(`${n.id}->${bridge.id}`) ?? vidFromBadges(n.badges),
      }))
      .sort((a, b) => (a.tag ?? 0) - (b.tag ?? 0) || a.label.localeCompare(b.label));

    const model: SwitchModel = {
      ref: bridge.id,
      node: bridge.nodeGroup,
      name: bridge.label,
      kind: bridge.kind,
      status: bridge.status,
      badges: bridge.badges,
      uplinks,
      vlans,
      accessPorts,
      vnets,
    };
    const list = switchesByNode.get(bridge.nodeGroup);
    if (list) list.push(model);
    else switchesByNode.set(bridge.nodeGroup, [model]);
  }

  // Free ports: bonds and NICs that no faceplate consumed. A NIC enslaved to
  // a bond, or that is itself a bridge port, was consumed above; what remains
  // is genuinely unattached. Bonds not port-of any bridge are surfaced too
  // (their member NICs get consumed as we build them).
  const freeByNode = new Map<string, SwitchUplink[]>();
  for (const n of nodes) {
    if (consumed.has(n.id)) continue;
    if (!BOND_KINDS.has(n.kind) && n.kind !== "physnic") continue;
    const uplink = uplinkFrom(n.id);
    if (!uplink) continue;
    const list = freeByNode.get(n.nodeGroup);
    if (list) list.push(uplink);
    else freeByNode.set(n.nodeGroup, [uplink]);
  }

  const nodeNames = Array.from(new Set([...switchesByNode.keys(), ...freeByNode.keys()])).sort();
  const result: NodeSwitchGroup[] = nodeNames.map((node) => ({
    node,
    switches: (switchesByNode.get(node) ?? []).sort((a, b) => a.name.localeCompare(b.name)),
    freePorts: (freeByNode.get(node) ?? []).sort((a, b) => a.label.localeCompare(b.label)),
  }));

  return { nodes: result };
}

// (helpers below are exported for direct testing / reuse by SwitchView)

/** Whether a switch model carries `vid` anywhere (uplink trunk badge, a
 * VLAN sub-if, an access port's tag, or a realized VNet's tag) — the switch
 * view's equivalent of projection.ts's computeVlanMatch, used to dim
 * whole faceplates that don't touch the filtered VLAN. Exported for reuse
 * and direct testing. */
export function switchCarriesVlan(sw: SwitchModel, vid: number): boolean {
  if (sw.vlans.some((v) => v.vid === vid)) return true;
  if (sw.accessPorts.some((p) => p.vid === vid)) return true;
  if (sw.vnets.some((v) => v.tag === vid)) return true;
  // A trunk uplink whose badge names the VID (e.g. "vlans=10-20") — reuse
  // the same token parse the graph view uses.
  return sw.uplinks.some((u) => u.badges.some((b) => badgeTrunksVlan(b, vid)));
}

/** "vlans=10-20,30" trunk-range membership test (mirrors projection.ts). */
function badgeTrunksVlan(badge: string, vid: number): boolean {
  const [key, value] = badge.split("=", 2);
  if (key !== "vlans" || value === undefined) return false;
  return value.split(",").some((range) => {
    const [loStr, hiStr] = range.split("-", 2);
    const lo = Number(loStr);
    const hi = hiStr === undefined ? lo : Number(hiStr);
    return Number.isFinite(lo) && Number.isFinite(hi) && vid >= lo && vid <= hi;
  });
}
