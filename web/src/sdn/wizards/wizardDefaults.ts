// Click-through defaults for the guided Simple-zone flow: a user can accept
// every step as-is and deploy a working, isolated SDN network without typing
// anything (the "just click Next" path the SDN empty-state promises). Every
// value stays editable — these are starting points, not constraints. The
// other four zone types (VLAN/QinQ/VXLAN/EVPN) each still need a genuinely
// environment-specific value (a VLAN tag, VXLAN peers, an EVPN ASN), which
// can't be guessed safely, so full click-through is a Simple-zone affordance.
import { useEffect, useRef } from "react";
import type { SubnetStepValue } from "./SubnetStep";

/** Default zone name — a friendly, memorable id that passes SDN name rules. */
export const DEFAULT_ZONE_ID = "homelab";

/** Default VNet name. */
export const DEFAULT_VNET_ID = "vnet1";

/** Default underlying bridge for VLAN/QinQ zones — Proxmox's conventional
 * default bridge name. Editable; the wizard preview and validators flag it if
 * it isn't VLAN-aware. */
export const DEFAULT_BRIDGE = "vmbr0";

/** Default VLAN tag for the VLAN wizard. */
export const DEFAULT_VLAN_TAG = 100;

/** Default QinQ service (outer, S-VLAN) and customer (inner, C-VLAN) tags. */
export const DEFAULT_QINQ_SERVICE_VID = 100;
export const DEFAULT_QINQ_CUSTOMER_VID = 200;

/** Default VXLAN/EVPN VNI (also the L2 identifier the VNet advertises). */
export const DEFAULT_VNI = 100;

/** Default EVPN controller id + ASN (preview-only; the zone references an
 * existing controller by id). */
export const DEFAULT_EVPN_CONTROLLER = "evpn1";
export const DEFAULT_EVPN_ASN = 65000;

/** Default subnet: a private range unlikely to collide with a typical
 * home/lab LAN. An isolated SDN vnet lives on its own bridge, so accepting
 * this yields a self-contained network the user can attach guests to
 * immediately; the gateway is the range's first usable address. */
export const defaultSubnetStepValue: SubnetStepValue = {
  cidr: "10.10.10.0/24",
  gateway: "10.10.10.1",
  isolated: false,
  snat: false,
};

/** useSelectAllNodesOnce checks every cluster node as soon as the node list
 * first loads, so a zone wizard opens with all nodes selected — the sensible
 * default for a cluster-wide zone. It fires once and only while the selection
 * is still untouched, so it never fights a manual change (deselecting the
 * last node does not re-trigger it). */
export function useSelectAllNodesOnce(
  clusterNodes: string[],
  selected: string[],
  setSelected: (nodes: string[]) => void,
): void {
  const applied = useRef(false);
  useEffect(() => {
    if (applied.current || clusterNodes.length === 0 || selected.length > 0) return;
    applied.current = true;
    setSelected(clusterNodes);
  }, [clusterNodes, selected, setSelected]);
}
