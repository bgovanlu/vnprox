// Every explanatory string the five zone wizards show a user, collected in
// one file for copy review (T-403's task card: "copy review: every
// explanatory string reads for a non-networking-expert"). Nothing here
// assumes the reader knows what a VLAN, VTEP, or BGP session is — each
// wizard's `intro` and per-step `help` strings define the jargon the first
// time they use it, in plain language, per docs/features/sdn.md §2's own
// bar: "Each step explains *what this actually does* in plain English."
//
// Keep this file the single place new wizard copy gets added or edited —
// don't inline explanatory prose directly into a wizard component.

export const wizardStrings = {
  picker: {
    title: "Create a zone — guided setup",
    description:
      "Pick the kind of network you want. Each option walks you through the handful of settings it actually needs, explains what they do, and shows you a live preview before anything is created.",
  },

  common: {
    // Shown while the live preview pane is computing/empty.
    previewEmpty: "Fill in the fields on the left to see a preview of the network you're about to create.",
    previewLoading: "Updating preview…",
    memberNodesHelp: "Which cluster nodes should have this network. Leave blank for every node.",
    vnetAliasHelp: "A friendly name shown in the map and lists — purely cosmetic, doesn't affect anything.",
    subnetHeading: "Give VMs addresses (optional)",
    subnetSkipHelp:
      "You can skip this and add an address range later — a network works fine with no assigned IP range if the VMs on it will get addresses another way (e.g. a router elsewhere on the network).",
    cidrHelp: "The range of addresses VMs on this network can use, e.g. 10.10.0.0/24 (256 addresses).",
    gatewayHelp: "The address VMs use to reach anything outside this network — usually the first address in the range.",
    snatHelp:
      "Turn this on to let VMs on this private network reach the internet, disguised behind this node's own address (like a home router does for your devices). Turn it off if this network should stay fully isolated.",
    // T-701 acceptance criterion 1: the gateway is no longer a silently-
    // empty free-text field — an explicit choice between "has a gateway"
    // (pre-filled, editable) and "keep isolated" (no gateway, no SNAT).
    gatewayModeHasGateway: "This network has a gateway",
    gatewayModeIsolated: "Keep this network isolated — no gateway",
    gatewayModeIsolatedHelp:
      "No address will be set aside for a gateway. VMs on this network can only talk to each other (and anything else on the same broadcast domain) — there's no way out, which is exactly right for a network that should never reach anything outside itself.",
    snatDisabledNoGateway:
      "Set a gateway above first — SNAT disguises traffic behind this network's gateway, so there's nothing for it to hide behind without one.",
    // Zone-type-specific gateway framing, shown once a CIDR is entered
    // (docs/features/sdn.md §2's per-zone-type gateway semantics note).
    gatewayZoneCopy: {
      simple: "Optional unless you turn on SNAT below — a private network with no gateway is a perfectly normal, fully isolated setup.",
      vlan: "This gateway lives on your external router, not on anything vnprox or Proxmox creates here — vnprox just records it for DHCP and the IPAM grid.",
      qinq: "This gateway lives on your external router, not on anything vnprox or Proxmox creates here — vnprox just records it for DHCP and the IPAM grid.",
      vxlan: "This gateway lives on your external router, not on anything vnprox or Proxmox creates here — vnprox just records it for DHCP and the IPAM grid.",
      evpn: "This becomes the anycast gateway address realized on every node in this zone — strongly recommended. Leaving it unset doesn't fail creation, but routed traffic through this network will silently never arrive anywhere.",
    },
    evpnSnatNeedsExitNode:
      "SNAT on an EVPN network additionally needs at least one exit node (the previous step) — none are selected yet, so SNAT traffic would have nowhere to leave through.",
    finishButton: "Create draft",
    cancelButton: "Cancel",
    backButton: "Back",
    nextButton: "Next",
    draftNotice:
      "Nothing is created yet. Finishing this wizard adds the steps below to a changeset draft, which you review and apply separately — exactly like every other change in vnprox.",
  },

  simple: {
    title: "Simple network",
    intro:
      "A simple network gives every node its own private, isolated bridge with the same name. VMs plugged into it can talk to each other as long as they're on the same node — nothing outside vnprox needs to be reconfigured.",
    zoneStepHelp: "Name this network and choose which nodes should have it.",
    bridgeNameHelp:
      "Leave blank and Proxmox will name the bridge after this network automatically. Only set this if you need a specific bridge name.",
  },

  vlan: {
    title: "VLAN network",
    intro:
      "A VLAN network tags traffic with a VLAN ID (VID) on an existing trunk — a physical link your switch has already been configured to carry multiple networks over. VMs on this network are isolated from VMs on other VIDs, even though they share the same cable.",
    bridgeStepHelp:
      "Pick the existing VLAN-aware bridge that carries the trunk on every node, and the VLAN ID (VID) this network should use. Ask your network admin if you're not sure which bridge is the trunk.",
    vidHelp:
      "The VLAN ID (a number from 1-4094) your switch has been configured to carry for this network. Using a VID your switch doesn't actually trunk means traffic will silently never arrive — that's what the check below is for.",
    trunkCheckHeading: "Physical trunk check",
    trunkCheckExplain:
      "vnprox looked at what your switches report over LLDP (a protocol switches use to announce themselves) for the bridge's physical ports. This tells you whether the switch side is actually configured to carry the VID you picked.",
    trunkCheckNoData:
      "No LLDP data available for these ports yet, so this can't be checked automatically — double-check with your switch admin that the VID is trunked.",
    trunkCheckOk: "Every switch port vnprox can see reports this VID as trunked. Looks good.",
    trunkCheckWarning: (port: string, chassis: string, vid: number) =>
      `Switch "${chassis}", port ${port} does not report VLAN ${String(vid)} as trunked. Traffic on this network may never reach that node — check the switch's trunk configuration before creating this network.`,
  },

  qinq: {
    title: "QinQ network (double-tagged)",
    intro:
      "QinQ stacks two VLAN tags on the same traffic: an outer \"service\" tag your provider or core network uses to carry many customers over one link, and an inner \"customer\" tag that's yours alone. Think of it as putting an envelope (service tag) around an already-tagged letter (customer tag) — useful when you need more isolated networks than a single VLAN's tag range allows, or when a provider link only gives you one VID to work with.",
    serviceTagHelp:
      "The outer/service VLAN tag — normally assigned by whoever operates the trunk you're building on (e.g. your hosting provider). vnprox doesn't yet manage this tag directly in Proxmox's SDN configuration; use this field to see how it fits into the double-tag picture in the preview, then set it in Proxmox's own SDN zone settings if it isn't already configured.",
    customerTagHelp:
      "The inner/customer VLAN tag — this is the VID your own VMs actually see, and the one this network's VNet is created with.",
    illustrationHeading: "Double-tag illustration",
    illustrationExplain:
      "The preview shows how a packet gets wrapped: your network's traffic (customer tag) travels inside the outer service tag, and unwraps back to just the customer tag once it reaches your VMs.",
  },

  vxlan: {
    title: "VXLAN network (overlay)",
    intro:
      "VXLAN builds a private network that spans multiple nodes — even across different physical networks or routed links — by wrapping traffic inside regular IP packets between the nodes (\"tunneling\"). Nodes that participate are called peers; the tunnel between two peers is a VTEP.",
    peersStepHelp:
      "Choose which nodes participate in the tunnel mesh. vnprox suggests each node's own address automatically — adjust these if your VXLAN traffic should use a different address than the node's main one (e.g. a dedicated storage/overlay network).",
    peersAutoSuggestNote: "Suggested from each node's own network address — check these before continuing.",
    mtuHeading: "MTU — why it needs to be smaller",
    mtuExplain:
      "VXLAN adds its own wrapper around every packet, which takes up space. If this network's MTU (maximum packet size) is set too close to the underlying network's own MTU, the wrapped packets won't fit and get silently dropped or fragmented.",
    mtuMath: (underlay: number, overhead: number, safe: number) =>
      `${String(underlay)} (underlying network MTU) − ${String(overhead)} (VXLAN's wrapper overhead) = ${String(safe)} — the largest safe MTU for this network.`,
    mtuApplyFix: "Set this network's MTU to the safe value",
    mtuWarning: (mtu: number, safe: number) =>
      `${String(mtu)} leaves no room for VXLAN's wrapper — packets may be dropped or fragmented. Use ${String(safe)} or lower.`,
    mtuOk: "This MTU leaves enough room for VXLAN's wrapper.",
  },

  evpn: {
    title: "EVPN network (routed overlay)",
    intro:
      "EVPN is VXLAN's more advanced sibling: instead of every node just tunneling to every other node, a routing protocol called BGP tells each node exactly where every address lives, so traffic takes a direct path and can be routed between different EVPN networks. It needs a \"controller\" — a BGP process already configured in Proxmox — and one or more \"exit nodes\" that give this network a way out to the rest of your infrastructure.",
    controllerStepHelp:
      "Enter the ID of an EVPN controller already configured in Proxmox's own SDN → Controllers screen (vnprox doesn't create controllers itself yet). The ASN and peer fields below are for the preview only, so you can see the resulting BGP session graph before creating anything — configure the controller's real ASN/peers directly in Proxmox if you haven't already.",
    asnHelp: "The controller's BGP Autonomous System Number — a preview-only field to draw the session graph.",
    exitNodesHelp:
      "Which nodes act as this network's \"door\" to the rest of your infrastructure. Traffic leaving this overlay network routes out through one of these.",
    primaryExitHelp: "The preferred exit node — vnprox uses the first node in the list above as the primary exit.",
    routeTargetExplain:
      "Behind the scenes, EVPN uses \"route targets\" to decide which networks are allowed to exchange routes with each other — vnprox and Proxmox derive these automatically from your VRF/VNI settings, so there's nothing to configure here.",
    sessionGraphHeading: "BGP session graph",
    sessionGraphExplain:
      "Each line below is a BGP session this network's controller would establish with a peer once created — not a live status (nothing exists yet), just what the resulting mesh looks like.",
  },
} as const;
