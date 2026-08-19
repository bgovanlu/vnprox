// One topic per routed screen in App.tsx. The coverage gate
// (coverage.test.ts) parses App.tsx for every `path="…"` literal and fails
// if any of them is missing from ROUTE_HELP, so this file and the router
// cannot drift apart silently.
import type { HelpTopic } from "../types";

export const PAGE_TOPICS: readonly HelpTopic[] = [
  {
    id: "login-page",
    title: "Signing in",
    surface: "page",
    summary:
      "vnprox has no accounts of its own — you sign in with the same Proxmox username, password, realm and second factor you use for the PVE web UI.",
    docRef: "docs/user-guide.md",
    keywords: ["login", "sign in", "password", "realm", "2fa", "totp", "oidc", "sso"],
    sections: [
      {
        heading: "Which credentials",
        body: "Exactly your Proxmox ones. Pick the realm (`pam`, `pve`, or whatever your cluster uses) from the dropdown; if your account has two-factor enabled in PVE, you'll be asked for the second factor here too. vnprox stores no password of its own and can't reset one.",
      },
      {
        heading: "Single sign-on",
        body: "If your administrator has enabled OIDC, a second sign-in button appears alongside the Proxmox form. Signing in through your identity provider establishes *who you are*; your Proxmox permissions still decide what you can do once you're in.",
      },
      {
        heading: "If sign-in fails",
        body: "An authentication failure names whether the credentials were rejected or the cluster was unreachable — those are different problems. A rejected login is a PVE decision, so it's worth confirming the same credentials work in the Proxmox UI before looking any further at vnprox.",
      },
    ],
    seeAlso: ["permissions", "dashboard-page"],
  },
  {
    id: "dashboard-page",
    title: "Home",
    surface: "page",
    summary:
      "The landing view: a tiled overview of your cluster's network health — open findings, drift, changesets in flight, top talkers, and traffic broken down by service class.",
    docRef: "docs/features/monitoring.md",
    keywords: ["home", "dashboard", "overview", "tiles", "status", "landing"],
    sections: [
      {
        heading: "What the tiles tell you",
        body: "Open findings by severity, current drift count, changesets awaiting confirmation or approval, management-path redundancy per node, top talkers over a recent window, and a per-service-class traffic breakdown (migration, backup, Ceph public and cluster, corosync, unclassified). Each tile links through to the view that explains it.",
      },
      {
        heading: "Reachable from a phone",
        body: "Home is one of the two screens deliberately usable at a narrow viewport — the other being Findings — because those are the two things you want when you're not at your desk. Everything else redirects to an explicit desktop-only message rather than rendering a cramped, half-working page.",
      },
      {
        heading: "Where to go from here",
        body: "If something is wrong, the fastest route is usually the findings stream, which carries the plain-English explanation and, where one is computable, a fix you can stage. For 'why can't A reach B', go straight to the path simulator.",
      },
    ],
    seeAlso: [
      "findings-stream",
      "tools-page",
      "topology-page",
      "service-class-traffic",
      "onboarding-walkthrough",
      "demo-mode",
    ],
  },
  {
    id: "topology-page",
    title: "Topology",
    surface: "page",
    summary:
      "The map: your cluster's network drawn as it actually is. It opens on Switch view — a faceplate rendering of your real gear — with a Graph view available for spatial work like drag-and-drop editing and path overlays.",
    docRef: "docs/features/topology.md",
    keywords: ["map", "topology", "diagram", "canvas", "switch view", "graph view", "layers"],
    sections: [
      {
        heading: "Switch view",
        body: "One appliance per Linux or OVS bridge, grouped per cluster node. The **uplink bay** shows the bridge's physical NICs and bonds with LACP/MII state and the LLDP neighbour on the far end of each wire — red means link down. The **VLAN strip** shows its sub-interfaces, the **guest access-port grid** gives one port per guest NIC (VMID as the port number), and the **VNet strip** shows any SDN VNets realized on that bridge. NICs and bonds wired into no bridge get their own unattached-ports panel.",
      },
      {
        heading: "Graph view",
        body: "The `Switch | Graph` toggle in the header switches to a pan/zoom node-link canvas with four layer bands — physical, L2, SDN, guests — stacked per node. Reach for it when you need drag-and-drop editing, the path-simulator overlay, traffic or latency paint modes, or hover-chain highlighting. The toggle is a per-session preference, not saved layout.",
      },
      {
        heading: "Things worth trying first",
        body: "**Hover** any VM in Graph view and its whole path to the physical switch lights up. Press `/` and type a VM name, MAC or IP to jump straight to it. Type a VLAN ID into the filter box to see exactly where that VLAN lives and where it's trunked — that works in both views. **Click** anything to open the inspector, which carries every detail, live status, and the raw config behind it.",
      },
      {
        heading: "Editing from the map",
        body: "In Graph view you can drag a NIC into a bond, or a guest NIC onto another bridge. Nothing applies — each drop becomes an operation in the change drawer, and goes through review and confirm like everything else.",
      },
      {
        heading: "Beyond the live view",
        body: "Pin a note or draw a region to record what the map can't show on its own — 'temporary until the switch swap'. Ask **History** for a point-in-time diff and click **Show on map** to ring every entity that changed. And from a changeset's review screen, **Preview the post-apply map** renders what applying it would actually produce, before you commit to it.",
      },
    ],
    seeAlso: [
      "topology-inspector",
      "topology-paint-modes",
      "spotlight-search",
      "change-drawer",
      "keyboard-and-palette",
      "map-annotations",
      "topology-point-in-time-diff",
      "post-apply-preview",
    ],
  },
  {
    id: "management-page",
    title: "Management",
    surface: "page",
    summary:
      "Per-node view of the interfaces that carry your management IP and corosync traffic — what vnprox protects, whether each path is redundant, and how to make it so if it isn't.",
    docRef: "docs/security.md",
    keywords: ["management", "mgmt", "corosync", "protected", "redundancy", "single path"],
    sections: [
      {
        heading: "What it shows",
        body: "For each node: the interface carrying its management address, the physical path underneath it (bridge → bond → NICs), the corosync rings, and whether the path is redundant — meaning at least two confirmed link-up physical NICs behind the interface holding the `mgmt` role.",
      },
      {
        heading: "Single-path nodes",
        body: "A node whose management path runs over one NIC raises a `mgmt_single_path` finding. Unlike most health checks this one isn't debounced, because it's a structural fact rather than a noisy counter: it fires the moment the path stops being redundant and clears the moment a second NIC comes up.",
      },
      {
        heading: "Fixing it safely",
        body: "The guided redundancy wizard builds the changeset for you — bonding a management uplink, or adding a slave to an existing management-path bond. It's interlock-clean by construction: every changeset it produces preserves the management address and its physical connectivity, so you're not fighting the safety interlock, you're working inside it.",
      },
    ],
    seeAlso: ["protected-interfaces", "mgmt-redundancy-wizard", "findings-stream"],
  },
  {
    id: "guests-page",
    title: "Guests",
    surface: "page",
    summary:
      "Every guest NIC in the cluster as a filterable list, with bulk operations — the fastest way to move a dozen VMs to another bridge or retag them onto a different VLAN.",
    docRef: "docs/features/change-management.md",
    keywords: ["guests", "vm", "container", "lxc", "nic", "reattach", "bulk", "vlan tag"],
    sections: [
      {
        heading: "What you can change",
        body: "Reattach a NIC to another bridge or VNet, change its VLAN tag, set a rate limit, toggle its firewall flag, and connect or disconnect the link. Each is an operation in the change drawer.",
      },
      {
        heading: "Bulk mode",
        body: "Filter by current bridge, VLAN or node, select the guests you want, and produce **one** changeset that moves all of them. Hotplug is attempted per guest and the result is reported per guest — so a guest that couldn't be hotplugged is named rather than quietly left behind.",
      },
      {
        heading: "How the writes happen",
        body: "Guest NIC operations go to the Proxmox API under your own login, the same call the PVE UI would make. That means your PVE permissions govern them, and it's why undoing them unattended needs the sealed revert ticket described under commit-confirm.",
      },
    ],
    seeAlso: ["guest-bulk-reattach", "change-drawer", "commit-confirm", "topology-page"],
  },
  {
    id: "sdn-page",
    title: "SDN",
    surface: "page",
    summary:
      "The SDN cockpit: zones, VNets and subnets as a navigable tree with per-node realization status, guided wizards for each zone type, and staged-versus-running shown as a real diff.",
    docRef: "docs/features/sdn.md",
    keywords: ["sdn", "zone", "vnet", "subnet", "vxlan", "evpn", "qinq", "vlan zone", "pending"],
    sections: [
      {
        heading: "Tree and detail",
        body: "Zones expand to VNets, VNets expand to subnets. Every level shows per-node realization status — applied, pending, or error — read from the PVE SDN status endpoints, so you can see that a zone is configured but hasn't landed on one node.",
      },
      {
        heading: "Pending is not a mystery flag",
        body: "Proxmox stages SDN edits until an apply. Rather than showing you an opaque 'pending' marker, vnprox surfaces staged-versus-running as a first-class diff: what's in the config, what's actually realized, and where they differ.",
      },
      {
        heading: "Guided zone wizards",
        body: "One wizard per zone type — Simple, VLAN, QinQ, VXLAN, EVPN — each explaining in plain English what the zone actually does, with a live preview pane drawing the resulting topology before anything is created. The VLAN wizard validates that the physical path really trunks the VIDs you picked; the VXLAN wizard does the MTU arithmetic for you.",
      },
      {
        heading: "DNS",
        body: "If your SDN uses the PowerDNS plugin, **SDN → DNS** shows your zones and records — both the configured truth and, where reachable, what PowerDNS is actually serving. Editing a record is an ordinary change that collects in the drawer and goes through review and confirm.",
      },
    ],
    seeAlso: ["sdn-zone-wizard", "validation-findings", "change-drawer", "ipam-page"],
  },
  {
    id: "firewall-page",
    title: "Firewall",
    surface: "page",
    summary:
      "The pve-firewall, made legible: datacenter, node and guest scopes as one hierarchy, with the effective evaluation order for any guest shown explicitly and every rule's origin labelled.",
    docRef: "docs/features/firewall.md",
    keywords: ["firewall", "rules", "pve-firewall", "security group", "ipset", "alias", "macro"],
    sections: [
      {
        heading: "Scopes and inheritance",
        body: "Three levels — Datacenter, Node, Guest — navigated as one hierarchy. For any guest, the resolved view shows the full evaluation order (cluster rules, security groups, guest rules, default policies) with each rule labelled by where it came from, so you're not reconstructing inheritance in your head.",
      },
      {
        heading: "The rule editor",
        body: "A table with drag-to-reorder (position matters), inline enable/disable, and a builder row covering direction, action, source and destination with autocomplete from aliases, ipsets, guest IPs and subnets, protocol and ports with service-name presets, interface, macro picker with expansion preview, log level and comment.",
      },
      {
        heading: "Objects with usage tracking",
        body: "Aliases, IPSets and security groups get first-class editors that tell you what references them — 'this alias is referenced by 9 rules'. Deleting a referenced object is blocked, with the reference list shown rather than a bare error.",
      },
      {
        heading: "The classic footgun, made visible",
        body: "If the datacenter firewall is switched off, none of these rules are active — and vnprox says so in a banner instead of letting you carefully build a ruleset that does nothing. Enabling or disabling the firewall at any scope comes with an explicit summary of what that will actually change.",
      },
    ],
    seeAlso: ["firewall-rule-effects", "path-simulator", "microseg-planner", "fwlog-viewer"],
  },
  {
    id: "ipam-page",
    title: "IPAM",
    surface: "page",
    summary:
      "The address plan Proxmox never showed you: subnets with utilization, a per-address list merging allocations with what's actually observed on the wire, and conflicts surfaced as findings.",
    docRef: "docs/features/ipam.md",
    keywords: ["ipam", "address", "subnet", "allocation", "reserve", "dhcp", "conflict", "ip"],
    sections: [
      {
        heading: "Where the data comes from",
        body: "Primarily the PVE IPAM API's allocations per SDN subnet, enriched with guest agent-reported addresses, DHCP leases, and ARP/IPv6 neighbour tables gathered cluster-wide. Each address carries a confidence label: `allocated`, `observed`, `both`, or `conflict`. Enrichment is never treated as authoritative — it's evidence, labelled as such.",
      },
      {
        heading: "The address list",
        body: "The selected subnet's occupied addresses as rows — IP, state, hostname, VMID, MAC, source — with contiguous free space collapsed into 'N addresses free' range rows. Because the view is proportional to actual usage rather than to the address space, the same list serves a /30 and a /16 without paging.",
      },
      {
        heading: "Conflicts are the point",
        body: "Duplicate IPs (two guests reporting the same address), observed-but-unallocated squatters, and allocated-but-dark stale records each become a health finding with a suggested resolution. These are the problems that are invisible until they bite.",
      },
      {
        heading: "Beyond the PVE-managed part",
        body: "Office LANs, upstream transit, colo ranges — address space Proxmox knows nothing about — can be recorded as **external subnets** so your plan is complete. They're plain records; they are never staged as PVE SDN changes. If you run NetBox or phpIPAM, the external sync gives you a dry-run preview before anything is written either way.",
      },
    ],
    seeAlso: ["ipam-address-list", "ipam-external-sync", "ipam-cross-cluster", "ipv6-dual-stack"],
  },
  {
    id: "flows-page",
    title: "Flows",
    surface: "page",
    summary:
      "The flow explorer: sampled network flows from sFlow, NetFlow or IPFIX, filterable and attributable to a service class and — where a Kubernetes cluster is registered — to a k8s service.",
    docRef: "docs/features/monitoring.md",
    keywords: ["flows", "sflow", "netflow", "ipfix", "traffic", "talkers", "sampling"],
    sections: [
      {
        heading: "Opt-in by design",
        body: "Flow ingestion is off by default and enabled per node. When on, records land in a bounded ring — a time window and a hard row cap, whichever prunes first. vnprox is deliberately not a long-term flow warehouse; export to real observability tooling if you need history.",
      },
      {
        heading: "Service-class attribution",
        body: "Each flow is tagged as migration, backup, Ceph public, Ceph cluster, corosync, or unclassified — derived purely from the record's own metadata (CIDR containment against registered network declarations, known addresses, VLAN). This is never payload inspection; vnprox is not an IDS.",
      },
      {
        heading: "Kubernetes attribution",
        body: "If you've registered a Kubernetes cluster, a `k8sService` column attributes source and destination addresses against its live overlay — exact Service ClusterIP first, then exact Pod IP, then pod-CIDR containment, never a wrong guess. An address matching nothing shows a dash; the row is never hidden.",
      },
      {
        heading: "What it's for",
        body: "Answering 'what is actually using this link', spotting a backup running over the wrong network, and feeding the microsegmentation planner a corpus of observed-good traffic to build a minimal firewall policy from.",
      },
    ],
    seeAlso: ["service-class-traffic", "microseg-planner", "conntrack-page", "topology-paint-modes"],
  },
  {
    id: "conntrack-page",
    title: "Conntrack",
    surface: "page",
    summary:
      "Host-local connection tracking: what's actually connected right now, per node, sampled from the kernel's own conntrack table rather than inferred from configuration.",
    docRef: "docs/features/monitoring.md",
    keywords: ["conntrack", "connections", "established", "nat", "sessions", "live"],
    sections: [
      {
        heading: "Live, not configured",
        body: "Everything else in vnprox tells you what your configuration says. This tells you what the kernel currently has open — established flows, their NAT translations, protocols and states. It's the natural companion to the path simulator, which evaluates configured state and says so.",
      },
      {
        heading: "Why it matters for firewall work",
        body: "Established-connection behaviour is exactly the nuance a static simulation has to caveat. When the simulator's verdict and reality disagree, conntrack is usually where the explanation is — an established flow that's permitted by state rather than by a rule.",
      },
    ],
    seeAlso: ["flows-page", "path-simulator", "firewall-page", "capture-panel"],
  },
  {
    id: "edge-page",
    title: "Edge & NAT",
    surface: "page",
    summary:
      "What this cluster exposes to and masks from the outside world: host masquerade and port-forward rules, static routes, SDN simple-zone SNAT, and WAN uplink health.",
    docRef: "docs/features/sdn.md",
    keywords: ["edge", "nat", "snat", "masquerade", "port forward", "route", "wan", "uplink"],
    sections: [
      {
        heading: "One outbound picture",
        body: "The edge layer gathers everything that decides how traffic leaves and enters: PVE host masquerade and port-forward rules, static routes, and the SNAT flag on each SDN simple zone. It never re-derives or shadows that data — it re-shapes the same reads into 'what does this cluster expose' terms.",
      },
      {
        heading: "Editing",
        body: "Masquerade rules, port forwards and static routes are edited through ordinary `nat.*` and `route.static.*` changeset operations — staged in the drawer, diffed, applied and confirmed like anything else. A simple zone's SNAT flag is read-only here; change it on the zone itself under SDN.",
      },
      {
        heading: "WAN health",
        body: "Where WAN uplinks are configured, rolling loss against external reference targets raises a `wan_degraded` finding. Its threshold is deliberately looser than the internal latency mesh's, because an ordinary WAN path's baseline jitter and loss are inherently higher than a LAN's.",
      },
    ],
    seeAlso: ["sdn-page", "findings-stream", "change-drawer"],
  },
  {
    id: "diagnose-page",
    title: "Diagnose",
    surface: "page",
    summary:
      "The guided diagnosis ladder: start from a symptom, and vnprox walks the layers in order — physical, L2, L3, firewall — naming what it ruled out and what it found.",
    docRef: "docs/features/topology.md",
    keywords: ["diagnose", "troubleshoot", "ladder", "why", "broken", "debug", "symptom"],
    sections: [
      {
        heading: "How it works",
        body: "Rather than dropping you into a pile of data, the ladder asks what's wrong and then checks in the order a network engineer would: is the link up, is the VLAN present and trunked along the whole path, is there an address and a route, does the firewall permit it. Each rung reports pass, fail, or 'couldn't determine' — never a silent skip.",
      },
      {
        heading: "It ends somewhere useful",
        body: "A completed diagnosis names the specific object at fault and links to it — the bond slave that's down, the VLAN that isn't trunked on one node's uplink, the rule that's dropping the traffic. Where a fix is computable, it can be staged as a changeset for you to review.",
      },
    ],
    seeAlso: ["path-simulator", "findings-stream", "topology-page", "conntrack-page"],
  },
  {
    id: "ports-page",
    title: "Ports",
    surface: "page",
    summary:
      "Physical port inventory from LLDP: which switch and which port is on the far end of every NIC, plus the mismatches between what your nodes think and what the switches report.",
    docRef: "docs/features/lldp-discovery.md",
    keywords: ["ports", "lldp", "switch", "neighbor", "cable", "patch", "physical"],
    sections: [
      {
        heading: "What LLDP gives you",
        body: "For every physical NIC that has a neighbour: the switch's system name, the port ID and description, and where the switch advertises it, the VLANs configured on that port. That's what turns the map from a diagram of your config into a diagram of your cabling.",
      },
      {
        heading: "If there's no data",
        body: "LLDP needs `lldpd` running on the nodes. vnprox offers to install and enable it during first-run setup, and that offer requires an explicit confirmation — it's a package installation on your hypervisors, so it's never done implicitly.",
      },
      {
        heading: "Mismatches",
        body: "A VLAN trunked on the node but not on the switch port, a node expecting a neighbour that's gone, two nodes' uplinks landing on the same switch when you thought they were redundant — these surface as findings rather than requiring you to read two inventories side by side.",
      },
    ],
    seeAlso: ["findings-stream", "topology-page", "switch-push"],
  },
  {
    id: "blueprints-page",
    title: "Blueprints",
    surface: "page",
    summary:
      "Reusable, parameterized network patterns: capture a working topology as a blueprint, then instantiate it on another node or cluster as one reviewable changeset.",
    docRef: "docs/features/blueprints.md",
    keywords: ["blueprint", "template", "pattern", "instantiate", "import", "export", "signed"],
    sections: [
      {
        heading: "What a blueprint is",
        body: "A declarative description of a network pattern — bridges, bonds, VLANs, SDN zones and their relationships — with parameters for the things that vary between installations. It describes intent, not a byte-for-byte copy of one node's files.",
      },
      {
        heading: "Instantiation is idempotent",
        body: "Applying a blueprint produces an ordinary changeset containing only the operations needed to reach the described state. Entities that already match are skipped, so re-running a blueprint against a cluster that already satisfies it yields a zero-operation changeset rendered as 'already up to date' — not a duplicate or a conflict.",
      },
      {
        heading: "Trust on import",
        body: "Imported blueprints are signature-verified against your trust store. An unsigned artifact, or one from a signer you haven't seen, requires you to explicitly say 'trust this' — there is no implicit trust path, including from the hub.",
      },
    ],
    seeAlso: ["blueprint-import", "hub-page", "change-drawer"],
  },
  {
    id: "hub-page",
    title: "Hub",
    surface: "page",
    summary:
      "An opt-in catalogue of signed blueprint bundles and SDK plugins you can browse and install, available once an administrator points vnprox at a registry.",
    docRef: "docs/user-guide.md",
    keywords: ["hub", "registry", "catalog", "marketplace", "install", "plugin", "bundle", "vetted"],
    sections: [
      {
        heading: "Nothing is installed on implicit trust",
        body: "Every install goes through the same trust gate a direct import does: the artifact's signature is verified against your trust store, and an unsigned or unfamiliar-signer artifact still requires an explicit 'trust this'. The hub is a place to find things, not an authority on them.",
      },
      {
        heading: "What the 'vetted' badge means",
        body: "That the registry recognizes the signer. It is informational only and never replaces your own trust decision — a vetted artifact from an unknown signer still stops at the same gate.",
      },
      {
        heading: "Installing a plugin",
        body: "You're shown the plugin's declared capability scope before you confirm. That scope is a ceiling: the plugin can never do more than it declared, and — like everything else in vnprox — it can stage a changeset but is never itself a way to apply one.",
      },
    ],
    seeAlso: ["plugins", "blueprints-page", "blueprint-import"],
  },
  {
    id: "history-page",
    title: "History",
    surface: "page",
    summary:
      "Your cluster's network history as a timeline: every applied change with its snapshot, a diff between any two points, and a restore path back to any of them.",
    docRef: "docs/features/change-management.md",
    keywords: ["history", "timeline", "snapshot", "diff", "restore", "time machine", "playback"],
    sections: [
      {
        heading: "The timeline",
        body: "Changes grouped by time, each showing who applied it, what it touched, and how it ended — committed, rolled back, or rolled back automatically at the confirm deadline. A changeset that failed keeps its failure step, so you can see exactly where the plan stopped.",
      },
      {
        heading: "Diffing two points",
        body: "Pick a **From** and a **To** point — any two snapshots, or 'vs live' — and switch between two views of what changed. **Topology** reads by entity: what was added, removed or changed, and whether a changeset explains it. **Files** is the older, literal unified diff of the raw interfaces text. This is the view for 'what did we actually do last Tuesday' and for 'this worked in March — what's different now'.",
      },
      {
        heading: "Restoring",
        body: "Restoring a snapshot doesn't reach behind the change engine. It builds a new changeset containing the operations that would return you to that state, and you review, apply and confirm it exactly as you would any change you'd built yourself.",
      },
    ],
    seeAlso: [
      "snapshots-time-machine",
      "snapshot-restore",
      "history-playback",
      "audit-page",
      "topology-point-in-time-diff",
    ],
  },
  {
    id: "incidents-page",
    title: "Incidents",
    surface: "page",
    summary:
      "One timeline over the window you are investigating: findings, changesets, diagnosis runs, captures, flows and your own notes, in one chronological column — plus what changed, and one artifact you can send to someone else.",
    docRef: "docs/features/monitoring.md",
    keywords: ["incident", "timeline", "outage", "postmortem", "correlate", "export", "annotation", "note"],
    sections: [
      {
        heading: "An incident is a view, not a mode",
        body: "Starting one changes nothing about what vnprox does — it collects no extra data and turns nothing on. It selects a window over history that was already being recorded. That is why you can open one **after** the fact, over a window that closed hours ago, and get exactly what you would have got had you started it at the time. Nobody has to remember to press a button before the network breaks.",
      },
      {
        heading: "What lands on the timeline",
        body: "Findings as they appear and clear, changesets staged or applied, diagnosis-ladder runs, captures started and stopped, sampled flows — and your own annotations, timestamped by *when the thing happened* rather than when you typed it. Everything is in one column in time order, because correlating five screens by hand is exactly what fails under pressure.",
      },
      {
        heading: "What it will not pretend to know",
        body: "A source that isn't collecting on this node says so rather than looking empty. Flows are capped and say when the cap bound. The point-in-time diff covers `/etc/network/interfaces` only; SDN objects are not diffed, and the timeline says so instead of implying they were checked. If vnprox has no snapshot old enough to cover your window, you get that message — naming the snapshots it does have — never a reassuring 'nothing changed'.",
      },
      {
        heading: "Closing, reopening, exporting",
        body: "Closing freezes the window; it deletes nothing, and reopening shows the same timeline. **Export** produces one archive: the timeline plus a support bundle, through the same redaction the support bundle already uses — safe to attach to a ticket or a forum post. You can export before closing; mid-incident is exactly when you are asking someone for help.",
      },
    ],
    seeAlso: ["history-page", "audit-page", "findings-stream", "diagnose-page", "topology-point-in-time-diff"],
  },
  {
    id: "audit-page",
    title: "Audit",
    surface: "page",
    summary:
      "Every action anyone took, merged across the cluster: who, when, what, and the result — filterable, expandable, and linked to the changeset and snapshots behind each row.",
    docRef: "docs/features/change-management.md",
    keywords: ["audit", "log", "who", "compliance", "trail", "actor", "history"],
    sections: [
      {
        heading: "What's recorded",
        body: "Applies, confirms, rollbacks, approvals, plugin installs and removals, external IPAM syncs with before-and-after per record, live probes into guests, and management-path acknowledgements. Anything that changes state or reaches outside vnprox leaves a row.",
      },
      {
        heading: "Telling humans from automation",
        body: "Each row names its actor. A changeset drafted by an AI operator is stamped with its origin and the token that created it, and every AI action writes its own row with an `mcp:` actor prefix — so 'did a person do this' is always answerable from the log rather than by inference.",
      },
      {
        heading: "Across clusters",
        body: "With federation configured, the audit view merges rows from every attached cluster newest-first, each tagged with its cluster, and shows the same explicit partial-results indicator you get elsewhere when one cluster is momentarily unreachable.",
      },
    ],
    seeAlso: ["history-page", "permissions", "federation", "mcp-ai-operators"],
  },
  {
    id: "tools-page",
    title: "Tools & Findings",
    surface: "page",
    summary:
      "The unified findings stream plus the power tools: path simulator, raw interfaces editor, firewall log viewer, MAC/FDB browser, and documentation export.",
    docRef: "docs/features/firewall.md",
    keywords: ["tools", "findings", "simulator", "raw editor", "export", "fdb", "mac"],
    sections: [
      {
        heading: "Findings first",
        body: "The stream merges every producer — health checks, drift, LLDP mismatches, IPAM conflicts, rogue-service detection, posture checks — into one source-tagged list with severity, a plain-English explanation, the affected objects as map links, and a fix where one is computable.",
      },
      {
        heading: "Available on a phone",
        body: "This is one of the two screens reachable at a narrow viewport, restricted there to the read-only findings view. The simulator, raw editor, MAC/FDB browser, firewall log viewer and doc export all need a desktop-sized screen and say so rather than rendering badly.",
      },
      {
        heading: "The escape hatch",
        body: "The raw interfaces editor gives you a full editor over a node's `/etc/network/interfaces` with syntax linting — and saving still produces a changeset that's diffed and commit-confirmed. It exists so power users stay inside the safety envelope instead of SSHing around it.",
      },
    ],
    seeAlso: ["findings-stream", "path-simulator", "raw-editor", "fwlog-viewer", "doc-export"],
  },
  {
    id: "changeset-review-page",
    title: "Changeset review",
    surface: "page",
    summary:
      "The screen you have to pass through before anything applies: a plain summary, the exact file diffs, the ordered plan, and the discussion thread — plus approval where your deployment requires it.",
    docRef: "docs/features/change-management.md",
    keywords: ["review", "apply", "diff", "plan", "approve", "comment", "changeset", "share"],
    sections: [
      {
        heading: "Four tabs",
        body: "**Summary** — the operations as human-readable cards. **File diff** — unified diffs of every file the change touches, per node, plus SDN config; deliberately literal, because operators reason in `/etc/network/interfaces` terms. **Plan** — the exact ordered steps: which API calls, which nodes reload, in what order. **Discussion** — comments on the whole changeset or on one operation.",
      },
      {
        heading: "Review as a team activity",
        body: "Comments are attributed and timestamped and survive validation and diffing. Deleting an operation deletes its comment explicitly and audibly rather than orphaning it. The changeset's URL is stable and shareable — send it to a colleague and they can read and comment on it, including from a phone. The link carries no credential of its own; they still need their own session.",
      },
      {
        heading: "Approval, where it's required",
        body: "If your deployment requires sign-off, an approver approves or rejects right here. That's an authorization decision made server-side on every apply attempt, not a UI affordance — an unapproved apply is refused whether it comes from the UI, the API, or the CLI. Editing the operations clears any prior approval, because a decision made about one set of operations must never authorize a different one.",
      },
      {
        heading: "Extra approvers for protected changes",
        body: "Some deployments require more than one approval for certain classes of change — anything touching firewall rules, SDN, a node's management path, or a policy tag your organisation defined. When a changeset falls into one of those classes, Apply stays disabled until enough **distinct people** have approved it, and the message under the button names the class and exactly how many more are needed. This is enforced by the server on every apply attempt, not by this screen — a request that skips the UI meets the identical refusal. An emergency break-glass override exists for when nobody else is available; it isn't a button on this screen, it requires a written reason, and it raises a finding that can't be acknowledged for 24 hours, so the override still gets reviewed by someone who wasn't in the room.",
      },
      {
        heading: "Before you hit Apply",
        body: "If the change touches a node's management path you'll be asked to type that node's name to acknowledge it, and the confirm window is raised to at least 180 seconds. The screen also tells you how much of your confirm window is covered by unattended revert for firewall and SDN operations, before you commit to it. **Preview the post-apply map**, on the blast-radius tab, shows what the map would look like once this changeset applies — before you commit to anything.",
      },
    ],
    seeAlso: [
      "change-drawer",
      "commit-confirm",
      "validation-findings",
      "scheduled-apply",
      "post-apply-preview",
      "presence-and-locks",
      "changeset-propose",
    ],
  },
  {
    id: "settings-page",
    title: "Settings",
    surface: "page",
    summary:
      "Your session, your effective capabilities, and the administrative surfaces: tokens, clusters, alert rules, plugins, tenants, switches, authentication and the hub.",
    docRef: "docs/user-guide.md",
    keywords: ["settings", "config", "admin", "tokens", "preferences", "capabilities"],
    sections: [
      {
        heading: "Your capabilities, spelled out",
        body: "Rather than making you discover your permissions by hitting walls, Settings lists what you can actually do in this deployment: view and change network, view and change SDN, view and change firewall, change guest NICs, view the audit log, capture packets. These come from your PVE ACLs.",
      },
      {
        heading: "Administrative surfaces",
        body: "Attached clusters, alert rules, automation and embed tokens, installed plugins with their declared capability scopes, tenants, registered switches, OIDC authentication, and the hub registry. Most of these are dormant until configured — an install that leaves them alone behaves as though they don't exist.",
      },
      {
        heading: "Things that are deliberately not here",
        body: "Read-only mode and the daemon's own configuration are set in the config file on the node, not from the UI. That's on purpose: a setting that governs whether the UI can write should not itself be writable from the UI.",
      },
    ],
    seeAlso: [
      "permissions",
      "read-only-mode",
      "settings-federation-page",
      "alert-rules-page",
      "tokens-and-embeds",
      "plugins",
      "tenants",
      "switch-push",
      "oidc-sso",
      "ha-pair",
      "certificates-page",
      "push-notifications",
      "installable-app-offline-shell",
    ],
  },
  {
    id: "alert-rules-page",
    title: "Alert rules",
    surface: "page",
    summary:
      "Which findings should reach you, and where: match on source, severity and check, and route to a webhook or to Proxmox's own notification system.",
    docRef: "docs/features/monitoring.md",
    keywords: ["alert", "rule", "notification", "webhook", "email", "routing", "escalation"],
    sections: [
      {
        heading: "Matching",
        body: "Rules select findings by source (health, drift, LLDP, IPAM, posture, rogue, WAN), by severity, and by specific check. Everything that ends up in the findings stream is routable — there's no separate, parallel alerting pipeline with its own set of events.",
      },
      {
        heading: "Delivery",
        body: "Webhook, or Proxmox's own notification system so alerts land wherever your PVE notifications already go. Delivery outcomes are visible, so a webhook that's been quietly failing is discoverable rather than assumed working.",
      },
      {
        heading: "Noise control comes from the checks",
        body: "Most health checks are hysteresis-debounced before they ever become findings — so alert rules don't need their own flap suppression. The exceptions are deliberate: structural facts and security signals like a suspected rogue DHCP server fire immediately, because debouncing them would be wrong.",
      },
    ],
    seeAlso: ["findings-stream", "settings-page", "dashboard-page"],
  },
  {
    id: "settings-federation-page",
    title: "Federated clusters",
    surface: "page",
    summary:
      "Attach other PVE clusters so one vnprox spans all of them — one map, one search, one audit log — without any cluster giving up ownership of its own configuration.",
    docRef: "docs/user-guide.md",
    keywords: ["federation", "clusters", "attach", "multi-cluster", "global", "peer"],
    sections: [
      {
        heading: "Attaching a cluster",
        body: "Name, API URL, and a read credential for that cluster. The credential is sealed at rest with the same encryption vnprox uses for Proxmox tickets, and is never displayed again after you save it.",
      },
      {
        heading: "What you get",
        body: "Once a **second** cluster is attached, the map gains a Global view at its outermost zoom — one capsule per cluster with its findings count, drift status, and an explicit indicator if it's momentarily unreachable. Search, the command palette and the audit log all span every attached cluster, grouped and namespaced by cluster.",
      },
      {
        heading: "What federation deliberately does not do",
        body: "It never changes another cluster's configuration for you. Each cluster remains the source of truth for its own network; a changeset always belongs to exactly one cluster and is rejected if an edit would reach across the boundary. Federation federates views and workflows, not ownership.",
      },
    ],
    seeAlso: ["federation", "cluster-awareness", "ipam-cross-cluster", "settings-page"],
  },
  {
    id: "certificates-page",
    title: "Certificates",
    surface: "page",
    summary:
      "Every TLS certificate your cluster's nodes present — to each other and to you — with expiry, the names each one covers, and whether it still chains to the cluster CA.",
    docRef: "docs/security.md",
    keywords: ["certificate", "tls", "ssl", "expiry", "san", "ca", "pve-ssl", "x509", "pem", "chain"],
    sections: [
      {
        heading: "Why this matters more on a multi-node cluster",
        body: "Everything vnprox does across nodes — applying a changeset, arming a distributed rollback timer, reading a peer's state — rides peer-API TLS, which is pinned to your cluster's own CA and **fails closed**. A certificate that has expired, that was issued by a different CA, or that doesn't cover the address its peers reach it at will take that node out of the cluster's peer mesh entirely. This screen is where you see that coming.",
      },
      {
        heading: "One node shows you the whole cluster",
        body: "`/etc/pve` is pmxcfs, Proxmox's own distributed filesystem, so every node's certificate is already present on every other node. vnprox reads them locally — no peer connection needed. That's deliberate: a certificate problem is exactly the thing that makes peers unreachable, so an inventory that needed the peer API to diagnose a peer-API failure would be useless at the only moment it mattered.",
      },
      {
        heading: "Problems come first",
        body: "The list at the top is what's actually wrong, each with the command that fixes it. **Expired** and **expiring** are self-explanatory. **Name mismatch** means the certificate covers neither the node's peer address nor its node name — pinned verification has nothing to check against. **CA mismatch** usually means a `pvecm updatecerts` that regenerated the CA without reissuing this node's certificate. **Weak key** flags anything below RSA-2048 or a SHA-1 signature.",
      },
      {
        heading: "Reading the names column",
        body: "These are the certificate's subject alternative names — the identities it can prove. A node dialled at an address that isn't in this list, and whose node name isn't either, cannot be authenticated. vnprox prefers to verify a peer by its node name where the certificate covers it, precisely because Proxmox does not reliably keep a node's current IP in there.",
      },
      {
        heading: "vnprox does not renew certificates",
        body: "That's Proxmox's job, and it does it well: `pvecm updatecerts -f` reissues a node's certificate from the cluster CA, and `pvenode acme cert order` drives ACME. Both restart `pveproxy`. Putting a hypervisor-restarting action behind a button here would add risk without adding capability — so each problem names the exact command instead. When the UI itself is unreachable, `vnproxctl certs` on the node shows you this same view.",
      },
    ],
    seeAlso: ["settings-page", "cluster-awareness", "findings-stream", "cli-escape-hatch"],
  },
  {
    id: "embed-map-page",
    title: "Embedded map",
    surface: "page",
    summary:
      "A read-only, live view of the network map for a wiki page or NOC screen, authenticated only by its own embed token — never by a logged-in browser session.",
    docRef: "docs/user-guide.md",
    keywords: ["embed", "iframe", "wiki", "noc", "screen", "share", "readonly", "token"],
    sections: [
      {
        heading: "Read-only by construction",
        body: "You cannot mint a write-capable embed, even as an administrator, and an embed never exceeds the permissions of whoever created it. It carries no navigation and no mutation controls at all — it isn't the app with the buttons hidden, it's a different, smaller surface.",
      },
      {
        heading: "How it authenticates",
        body: "Solely by the `token` in its URL. A visitor's existing vnprox session is never silently used to authenticate an embed, which is what stops an embedded frame on a shared page from quietly showing a viewer more than the embed's own token permits.",
      },
      {
        heading: "Creating one",
        body: "Mint an embed link under **Settings → Tokens → Embed**. Treat the resulting URL as the credential it is — anyone with the link sees what the token permits.",
      },
    ],
    seeAlso: ["tokens-and-embeds", "embed-dashboard-page", "embed-posture-page"],
  },
  {
    id: "embed-dashboard-page",
    title: "Embedded dashboard",
    surface: "page",
    summary:
      "The tiled health overview as a read-only embed for a wall display or wiki page, with the same token-only authentication as every other embed.",
    docRef: "docs/user-guide.md",
    keywords: ["embed", "dashboard", "noc", "wallboard", "screen", "readonly", "token"],
    sections: [
      {
        heading: "What it shows",
        body: "The same tiles as the Home view — open findings by severity, drift, changesets in flight, management redundancy — refreshed live, with no controls to click through to anything else.",
      },
      {
        heading: "Meant to be left up",
        body: "It's designed for a screen nobody is sitting at: no session to expire out from under it, no navigation to get lost in, and degraded rather than broken rendering if a node or cluster is momentarily unreachable.",
      },
    ],
    seeAlso: ["tokens-and-embeds", "embed-map-page", "dashboard-page"],
  },
  {
    id: "config-as-code-page",
    title: "Config as code",
    surface: "page",
    summary:
      "One screen for the three answers to 'what is this cluster's network': the declarative document (spec), the on-disk configuration (config), and the running kernel (live) — plus the two ways of resolving a disagreement between them.",
    docRef: "docs/api.md",
    keywords: [
      "gitops",
      "git",
      "spec",
      "declarative",
      "yaml",
      "terraform",
      "plan",
      "pin",
      "reconcile",
      "config as code",
      "intent",
    ],
    sections: [
      {
        heading: "Three positions, not two",
        body: "**Spec** is the declarative document — what the cluster is supposed to be, held in a git repository or pinned here. **Config** is `/etc/network/interfaces` as PVE reports it: what the cluster will be after the next reload. **Live** is the running kernel right now. Any two of them can agree while the third does not, and which pair agrees is what tells you where the problem actually is.",
      },
      {
        heading: "Nothing on this screen applies anything",
        body: "Every action here either stages a draft changeset you then take through the ordinary review screen, or moves the document. There is no apply, no confirm and no rollback on this page — resolving a spec disagreement uses exactly the same review-and-confirm flow as any other network change, because it is the same flow.",
      },
      {
        heading: "Two directions, never one button",
        body: "'Restore intent' moves the cluster to match the document. 'Adopt reality' moves the document to match the cluster. They have opposite blast radii — one changes your network, the other opens a pull request and touches nothing — so they are separate controls with separate confirmations, and the daemon audits them separately. There is deliberately no combined 'reconcile'.",
      },
    ],
    seeAlso: ["gitsync-status", "spec-pin", "spec-plan", "spec-reconciliation", "drift", "changeset-review-page"],
  },
  {
    id: "embed-posture-page",
    title: "Embedded posture report",
    surface: "page",
    summary:
      "The network security posture report as a read-only embed — the view to put in front of an auditor or on a compliance dashboard.",
    docRef: "docs/security.md",
    keywords: ["embed", "posture", "security", "compliance", "audit", "report", "readonly"],
    sections: [
      {
        heading: "What posture covers",
        body: "The security-shaped findings: segments without expected isolation, guests reachable from more places than intended, firewall scopes disabled, management paths without redundancy, and rogue-service detections. It's a report on state, not a scan — vnprox never probes your network to produce it.",
      },
      {
        heading: "Sharing it safely",
        body: "As with every embed, this is read-only by construction and authenticated only by its own token, so handing the link to someone outside your team grants exactly this view and nothing adjacent to it.",
      },
    ],
    seeAlso: ["tokens-and-embeds", "findings-stream", "embed-map-page"],
  },
  {
    id: "analysis-page",
    title: "Analysis",
    surface: "page",
    summary:
      "Five read-mostly analyses in one place: what breaks if something dies, where capacity is heading, what is shaped, where backup traffic actually flows, and what IPv6 is really doing on each segment.",
    docRef: "docs/features/monitoring.md",
    keywords: ["analysis", "spof", "resilience", "capacity", "qos", "shaping", "pbs", "backup", "ipv6"],
    sections: [
      {
        heading: "Judgements, not configuration",
        body: "Every panel here reads something vnprox already knows and turns it into a claim about the cluster — a single point of failure, a projected exhaustion, a shaped bridge, a backup path, an advertised prefix. That is why they share a screen rather than sitting on the pages whose entities they happen to name. Nothing on this screen probes your network to produce its answer.",
      },
      {
        heading: "One editable thing, and it still stages",
        body: "QoS shaping is the only panel here that changes anything, and it changes it the same way everything else in vnprox does: the edit becomes a `qos.shape.create`, `qos.shape.update` or `qos.shape.delete` operation in the change drawer, which you then review, apply and confirm. There is no QoS write route for it to take a shortcut through.",
      },
      {
        heading: "What is not here",
        body: "WAN and upstream health is the sixth analysis in this set and lives on the Edge screen instead — 'how does traffic leave' and 'does it get anywhere once it has' are one question, and the Edge cockpit already asks the first half. Capacity *forecasts* are not here either: a projected crossing arrives as an ordinary finding in the findings stream, where it can be acknowledged like any other.",
      },
    ],
    seeAlso: ["spof-score", "capacity-export", "qos-shaping", "pbs-awareness", "ipv6-segments", "wan-health"],
  },
  {
    id: "platform-panel-page",
    title: "Platform",
    surface: "page",
    summary:
      "Automation credentials, event delivery targets, installed extensions, and the daemon's own live self-check — the four administrative surfaces that used to be reachable only with curl.",
    docRef: "docs/api.md",
    keywords: ["platform", "token", "bearer", "webhook", "plugin", "doctor", "self-check", "automation", "settings"],
    sections: [
      {
        heading: "Four surfaces, four independent answers",
        body: "Tokens, webhooks, plugins and the self-check each read a different route family, and each degrades on its own. A daemon with no plugin registry wired still shows your tokens; a session without the audit capability still sees everything except the self-check. When a section cannot answer, it says which of the two reasons applies — you are not allowed to look, or this daemon does not carry that subsystem at all.",
      },
      {
        heading: "It never re-states a rule the daemon owns",
        body: "Whether a webhook destination is permitted, whether a scope may be granted, whether a plugin's manifest matches what its listing advertised — every one of those decisions is made by the daemon, and this screen renders the answer it gave, word for word, including the name of the configuration knob that would change it. Nothing here re-implements a policy check, because a second copy would drift from the one that actually decides.",
      },
      {
        heading: "Nothing is installed from here",
        body: "The plugin section manages the lifecycle of plugins that are already installed — enable, disable, uninstall. Installing goes through the Hub, where the signature check, the trust decision and the capability-scope agreement check live. Adding a second install path here would be a way around that gate, so there deliberately is not one.",
      },
    ],
    seeAlso: ["settings-page", "platform-tokens", "platform-webhooks", "platform-plugins", "platform-doctor-live", "tokens-and-embeds", "plugins"],
  },
  {
    id: "governance-page",
    title: "Governance",
    surface: "page",
    summary:
      "The rules a change is measured against, what the cluster can evidence about itself, who is allowed to see what, and when the periodic summary goes out — four administrative surfaces that had no screen at all.",
    docRef: "docs/features/change-management.md",
    keywords: ["governance", "policy", "compliance", "tenant", "digest", "rules", "approval", "audit"],
    sections: [
      {
        heading: "What is on this screen and what is not",
        body: "Policies, compliance profiles, tenants and the digest schedule live here. The two governance surfaces you actually meet while making a change do not: a policy `deny` verdict and the emergency break-glass override appear inside the review screen, where they block, rather than on an administration page you would have to think to visit. That is deliberate — a refusal an operator has to go looking for is a refusal they will not read.",
      },
      {
        heading: "Nothing here applies anything",
        body: "Replacing a rule set writes a document, the tenant controls write tenant rows, and the digest control writes a schedule. None of them touches the network, none of them goes near the change engine, and none of them is a way around the stage-validate-diff-apply-confirm path every real change still takes.",
      },
      {
        heading: "Every refusal shown here is the daemon's",
        body: "Whether a rule set parses, whether a cadence is workable, whether a tenant exists — the daemon decides each of those and this screen renders the answer it gave. There is no second, weaker copy of any of those checks in the browser, because a second copy drifts from the one that actually decides and then two different things are true at once.",
      },
    ],
    seeAlso: ["policies-panel", "compliance-panel", "tenants-panel", "digest-schedule-panel", "policy-verdict", "break-glass", "tenants"],
  },
];
