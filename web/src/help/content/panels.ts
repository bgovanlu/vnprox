// Panel, dialog and wizard topics — the surfaces inside a screen that carry
// their own vocabulary or their own risk. Each is reachable from the screen
// it belongs to via <HelpAnchor topic="…">, and the coverage gate requires
// every anchor in the tree to resolve to one of these.
import type { HelpTopic } from "../types";

export const PANEL_TOPICS: readonly HelpTopic[] = [
  {
    id: "topology-inspector",
    title: "The inspector",
    surface: "panel",
    summary:
      "Click anything on the map and the inspector shows everything vnprox knows about it: configuration, live state, health, the raw config behind it, and — for guests — what the guest itself reports.",
    docRef: "docs/features/topology.md",
    keywords: ["inspector", "detail", "properties", "selection", "sidebar"],
    sections: [
      {
        heading: "Configured and live, side by side",
        body: "For a bond you get the mode, MII status, active slave and per-slave link and failure counts read from the kernel, alongside the configuration that produced them. For an 802.3ad bond you also get the LACP actor and partner system IDs, keys and per-slave port state — which is what turns 'the bond is up but slow' into 'these two slaves are talking to different partners'.",
      },
      {
        heading: "Live rates and history",
        body: "Selecting an entity shows current rx/tx bits and packets per second, errors and drops, with sparklines over a rolling 24-hour window at 30-second resolution. Nothing longer is kept — export to real observability tooling if you need history.",
      },
      {
        heading: "The guest interior tab",
        body: "For a guest, an opt-in read-only tab shows the guest's *own* view of its network: interfaces, addresses, routes, DNS, listening sockets, gateway reachability. It's off by default and reads inside the guest each time you open it. Every address the guest claims is cross-checked against IPAM rather than taken at face value — a self-report is a claim, not a fact.",
      },
    ],
    seeAlso: ["topology-page", "topology-paint-modes", "ipam-page", "map-annotations"],
  },
  {
    id: "topology-paint-modes",
    title: "Paint modes",
    surface: "panel",
    summary:
      "Overlays that colour the map by something other than structure: current traffic, measured latency, a simulated path, or the backup path to a PBS host.",
    docRef: "docs/features/monitoring.md",
    keywords: ["paint", "overlay", "heatmap", "traffic", "latency", "utilization", "colour"],
    sections: [
      {
        heading: "Traffic",
        body: "Edge thickness and heat by current utilization, sampled every five seconds from the kernel's own interface statistics. Bond member balance is shown per slave, which makes an LACP hash imbalance visible at a glance rather than requiring you to compare counters.",
      },
      {
        heading: "Latency",
        body: "A separate overlay colouring each node-to-node link by its rolling round-trip time and loss, from continuous low-rate probes across the fabrics vnprox can identify. Its colour scale never shares a value with traffic mode's, so the two read as clearly distinct layers. A bridge carrying both IPv4 and IPv6 is probed on each family independently — a v6-only degradation can't hide behind a healthy v4 number.",
      },
      {
        heading: "Path and backup overlays",
        body: "The path simulator draws its hop-by-hop result directly on the map. Where Proxmox Backup Server hosts are known, a backup-path mode lights up the node-to-PBS traffic path for nodes with a job targeting that storage.",
      },
    ],
    seeAlso: ["topology-page", "path-simulator", "flows-page", "pbs-awareness"],
  },
  {
    id: "spotlight-search",
    title: "Search",
    surface: "dialog",
    summary:
      "Press `/` from anywhere and type a VM name, MAC, IP, VMID or comment. Selecting a result reveals that entity on the map.",
    docRef: "docs/user-guide.md",
    keywords: ["search", "spotlight", "find", "jump", "locate", "mac", "vmid"],
    sections: [
      {
        heading: "What it searches",
        body: "Fuzzy matching across names, MAC addresses, IP addresses, VMIDs and comments, over the whole inventory — not just what's currently drawn or currently filtered.",
      },
      {
        heading: "Across clusters",
        body: "With federation configured, results span every attached cluster, grouped and namespaced by cluster so a duplicate VM name in two places is unambiguous rather than confusing.",
      },
    ],
    seeAlso: ["keyboard-and-palette", "topology-page"],
  },
  {
    id: "mgmt-redundancy-wizard",
    title: "Management redundancy wizard",
    surface: "dialog",
    summary:
      "Guided flow for giving a node's management path a second physical NIC — bonding a single uplink, or adding a slave to an existing management-path bond.",
    docRef: "docs/security.md",
    keywords: ["redundancy", "wizard", "bond", "management", "mgmt", "single path", "uplink"],
    sections: [
      {
        heading: "Why it exists",
        body: "Restructuring the interface that carries a node's management address is precisely what the safety interlock forbids you from doing carelessly. This wizard is the sanctioned path: every changeset it produces preserves the management IP value and its physical connectivity in net effect, so it's interlock-clean by construction rather than by override.",
      },
      {
        heading: "What it produces",
        body: "An ordinary changeset. You review the diff and the plan, apply it, and confirm it — with the raised 180-second window and the typed node-name acknowledgement that any management-path change gets.",
      },
      {
        heading: "What's still out of scope",
        body: "Re-addressing a management IP. Changing which address a node is managed on is a decision with consequences vnprox cannot roll back for you, so it stays a deliberate manual operation.",
      },
    ],
    seeAlso: ["protected-interfaces", "management-page", "commit-confirm"],
  },
  {
    id: "guest-bulk-reattach",
    title: "Bulk reattach",
    surface: "dialog",
    summary:
      "Move many guest NICs at once: filter to the set you want, pick the target bridge or VNet, and get one changeset covering all of them with per-guest results.",
    docRef: "docs/features/change-management.md",
    keywords: ["bulk", "reattach", "move", "migrate", "batch", "guests", "retag"],
    sections: [
      {
        heading: "One change, not N",
        body: "Selecting twenty guests produces a single changeset with twenty operations — one review, one plan, one confirm window. That matters for a maintenance window: twenty separate changes means twenty separate rollback deadlines to babysit.",
      },
      {
        heading: "Per-guest outcomes",
        body: "Hotplug is attempted per guest and reported per guest. A guest that couldn't be hotplugged — because its OS or its configuration doesn't support it — is named explicitly, so you know which ones need a reboot to pick up the change rather than assuming they all took.",
      },
    ],
    seeAlso: ["guests-page", "change-drawer", "commit-confirm"],
  },
  {
    id: "sdn-zone-wizard",
    title: "Zone wizards",
    surface: "dialog",
    summary:
      "One wizard per SDN zone type, each explaining in plain English what the zone actually does, with a live preview drawing the resulting topology before anything is created.",
    docRef: "docs/features/sdn.md",
    keywords: ["zone", "wizard", "simple", "vlan", "qinq", "vxlan", "evpn", "sdn", "new zone"],
    sections: [
      {
        heading: "Simple",
        body: "An isolated bridge per node, with optional SNAT — 'a private network that exists on every node; VMs on it can talk to each other on the same node'. The natural starting point for an isolated test network.",
      },
      {
        heading: "VLAN and QinQ",
        body: "The VLAN wizard picks the VLAN-aware bridge and validates that the physical path actually trunks the VIDs you chose, cross-checking LLDP VLAN information where the switch advertises it. QinQ adds a service VLAN and inner range, with a double-tag illustration so the encapsulation is visible rather than assumed.",
      },
      {
        heading: "VXLAN and EVPN",
        body: "These stretch a network across nodes, and the wizard does the MTU arithmetic — VXLAN's encapsulation overhead against your underlay path MTU — rather than leaving you to discover the shortfall as mysterious packet loss later. A configured MTU that leaves no headroom raises a finding even after apply, if the underlay degrades.",
      },
    ],
    seeAlso: ["sdn-page", "validation-findings", "change-drawer"],
  },
  {
    id: "firewall-rule-effects",
    title: "Rule effects preview",
    surface: "panel",
    summary:
      "Before you apply a security-group rule, see which guests it will actually match — computed from inventory rather than left to inference.",
    docRef: "docs/features/firewall.md",
    keywords: ["effects", "preview", "matching", "guests", "security group", "impact"],
    sections: [
      {
        heading: "What it computes",
        body: "For a rule in a security group, the set of guests that group currently reaches, resolved through the group's references. That answers 'how wide is this blast radius' before you find out empirically.",
      },
      {
        heading: "What it doesn't compute",
        body: "IP-level effects. The preview works at guest granularity; it does not enumerate every address a rule would match. That's a stated limit rather than a silent approximation — for address-level reasoning, use the path simulator on a specific source and destination.",
      },
    ],
    seeAlso: ["firewall-page", "path-simulator", "microseg-planner"],
  },
  {
    id: "path-simulator",
    title: "Path simulator",
    surface: "panel",
    summary:
      "Answers 'can A reach B on this protocol and port — and if not, what stops it?' by evaluating your configured state hop by hop, and naming the exact blocking rule or missing link.",
    docRef: "docs/features/firewall.md",
    keywords: ["simulate", "path", "reachability", "why", "blocked", "connectivity", "trace"],
    sections: [
      {
        heading: "What it evaluates",
        body: "L2 adjacency along the path — same bridge or VNet, VLAN tag compatibility at every hop — then L3 (subnet membership, gateways, SNAT), then firewall evaluation in PVE's real order at the guest-scope enforcement points the path crosses, including macro expansion and ipset/alias resolution.",
      },
      {
        heading: "Four verdicts, including an honest one",
        body: "**Allow**, **deny**, **unreachable**, and **indeterminate** — the fourth being a path it cannot fully evaluate. A deny names the exact blocking rule and deep-links to its editor; an unreachable names the missing link ('VLAN 30 is not trunked on bond0 of node pve2'). Node- and cluster-scope host-chain rules are disclosed as off-path rather than evaluated, as a stated caveat — not a silent gap.",
      },
      {
        heading: "It simulates configuration, not packets",
        body: "Results are labelled simulated, and conntrack-dependent nuances like established flows and NAT reflection are listed as caveats when they're relevant. This honesty is the feature: a simulator that guesses is worse than none.",
      },
      {
        heading: "Verify live",
        body: "Where the source is a QEMU guest with a running guest agent, **Verify live** runs the same source-destination-protocol-port tuple as a real probe inside that guest and shows the observed outcome *alongside* the simulated verdict — never framed as one correcting the other. Because it reaches into a guest it's audited, unlike a plain simulation. If the two disagree, that divergence is recorded as a finding that survives a restart and deep-links back with the tuple pre-filled.",
      },
    ],
    seeAlso: ["tools-page", "firewall-page", "conntrack-page", "diagnose-page"],
  },
  {
    id: "microseg-planner",
    title: "Microsegmentation planner",
    surface: "panel",
    summary:
      "Proposes the minimal firewall policy that preserves a guest's observed-good traffic — 'these N rules cover 30 days of traffic' — then makes you dry-run it before it can be staged.",
    docRef: "docs/features/firewall.md",
    keywords: ["microsegmentation", "microseg", "zero trust", "policy", "propose", "dry run", "least privilege"],
    sections: [
      {
        heading: "Propose",
        body: "Coverage is stated as the exact fraction of observed-good bytes the rules cover, and is never rounded up to 'everything': 99.53% reads 99.53%, with the deliberately-uncovered long-tail flow count shown as its own number. Flows the baseline detector flagged as anomalous are excluded from what counts as observed-good, so a transient compromise inside the training window can't legitimize itself into an allow rule.",
      },
      {
        heading: "Dry-run, in four honest buckets",
        body: "**Would-have-blocked** — observed-good flows this policy would have dropped. Zero is the goal, and the section is always present showing zero rows when empty, so 'checked, none' is never confused with 'not checked'. **Cannot-determine** — flows the evaluator could not prove stay permitted; these are never folded into 'would-allow' and are treated as potential breaks. **Would-allow** and **ungoverned** are the reassuring buckets, collapsed below.",
      },
      {
        heading: "Staging",
        body: "The proposal becomes ordinary firewall rule operations in the change drawer — there is no bespoke apply path here. Staging stays disabled until the current proposal has been dry-run at least once, and re-proposing invalidates that: nobody stages a policy nobody has dry-run.",
      },
    ],
    seeAlso: ["firewall-page", "flows-page", "change-drawer"],
  },
  {
    id: "fwlog-viewer",
    title: "Firewall log viewer",
    surface: "panel",
    summary:
      "Tails and parses per-node pve-firewall logs, filterable by guest, direction and action, with best-effort correlation back to the rule that most likely matched.",
    docRef: "docs/features/firewall.md",
    keywords: ["log", "firewall log", "tail", "dropped", "blocked", "packets", "syslog"],
    sections: [
      {
        heading: "Reading the log",
        body: "Lines are parsed into structured rows — guest, direction, action, addresses, ports — so you can filter rather than grep. Live tail follows new lines as they arrive.",
      },
      {
        heading: "Rule correlation is heuristic",
        body: "Real pve-firewall log lines do **not** embed a rule position or reference. Correlation is done by matching a line against the resolved ruleset on direction, action and guest — a best-effort inference, not something PVE told us. Where a line can't be confidently attributed, it isn't, rather than being attributed to a plausible-looking rule.",
      },
    ],
    seeAlso: ["firewall-page", "path-simulator", "conntrack-page"],
  },
  {
    id: "raw-editor",
    title: "Raw interfaces editor",
    surface: "panel",
    summary:
      "A full editor over a node's `/etc/network/interfaces`, with syntax linting — and saving still produces a diffed, commit-confirmed changeset.",
    docRef: "docs/features/change-management.md",
    keywords: ["raw", "editor", "interfaces", "escape hatch", "text", "manual", "power user"],
    sections: [
      {
        heading: "Why it's here",
        body: "Some things are faster to express as text, and some configurations the form editors don't cover. Rather than have you SSH to the node and edit around every safety mechanism the product has, the raw editor keeps you inside the envelope: linting as you type, the same validators run against the parsed result, and a diff before anything is written.",
      },
      {
        heading: "It's still a changeset",
        body: "Saving creates a changeset whose single operation replaces the file. It's diffed, validated, applied and commit-confirmed exactly like a change you built with the visual editors — including the automatic rollback if you cut yourself off.",
      },
      {
        heading: "Pick the node explicitly",
        body: "This editor is per-node and takes the node from a dropdown; it doesn't infer one from context. That's deliberate — editing the wrong node's interfaces file is precisely the mistake you don't want to make quickly.",
      },
    ],
    seeAlso: ["tools-page", "change-drawer", "commit-confirm", "validation-findings"],
  },
  {
    id: "doc-export",
    title: "Documentation export",
    surface: "panel",
    summary:
      "Generates a written description of your cluster's network — topology, addressing, VLANs, SDN, firewall posture — as Markdown or HTML you can hand to an auditor or paste into a wiki.",
    docRef: "docs/features/topology.md",
    keywords: ["export", "documentation", "report", "markdown", "html", "handover", "audit"],
    sections: [
      {
        heading: "What it contains",
        body: "The current state, described: nodes and their interfaces, bridges and bonds with their members, VLANs and where they're trunked, SDN zones and subnets, addressing, and the open findings at the time of export.",
      },
      {
        heading: "A snapshot, not a live link",
        body: "The export is generated from state at the moment you ask for it and is dated accordingly. It's for handover, audit and change-record purposes; for a live view someone else can watch, mint a read-only embed instead.",
      },
    ],
    seeAlso: ["tools-page", "tokens-and-embeds", "audit-page"],
  },
  {
    id: "ipam-address-list",
    title: "The address list",
    surface: "panel",
    summary:
      "A subnet's occupied addresses as rows, with free space collapsed into range rows — so the same view serves a /30 and a /16 without paging.",
    docRef: "docs/features/ipam.md",
    keywords: ["addresses", "list", "grid", "free", "used", "reserve", "release", "utilization"],
    sections: [
      {
        heading: "States",
        body: "Each row is `allocated`, `reserved`, `observed-unallocated`, `gateway`, or `conflict`, with the source that produced the claim. An observed-unallocated address is a squatter — something is using it that IPAM has no record of; an allocated-but-dark record is the reverse.",
      },
      {
        heading: "Reserving and releasing",
        body: "Reserve and release go through the change engine as `ipam.alloc.*` operations, so they're staged, reviewed and confirmed like any other change rather than taking effect as you click.",
      },
      {
        heading: "Finding a free address",
        body: "The next-free picker is available on address fields that support it, so you don't have to eyeball the range rows. Per-subnet CSV export is available for feeding an external system or a spreadsheet.",
      },
    ],
    seeAlso: ["ipam-page", "change-drawer", "ipam-external-sync"],
  },
  {
    id: "ipam-external-sync",
    title: "External IPAM sync",
    surface: "panel",
    summary:
      "Two-way bridge to NetBox or phpIPAM: preview shows exactly what would change on either side and writes nothing; only an explicit confirmed apply performs the sync.",
    docRef: "docs/features/ipam.md",
    keywords: ["netbox", "phpipam", "sync", "external", "preview", "dry run", "bidirectional"],
    sections: [
      {
        heading: "Preview first, always",
        body: "The preview is a pure dry-run. It classifies each difference as an **add** (vnprox has it, the external system doesn't), a **remove** (the external system has it, vnprox no longer allocates it), or a **conflict** (both hold the address but disagree about its hostname). Conflicts are never auto-written in either direction.",
      },
      {
        heading: "Why this isn't a changeset",
        body: "The change engine owns Proxmox network configuration. An external IPAM system has no PVE object to diff, no file to snapshot, and no node-local daemon that could roll a remote write back — routing it through the change engine would be a category error. But 'outside the change engine' does not mean unstaged or unaudited: preview is the stage-and-diff step, a confirmed apply is the explicit confirm step, and every write lands an audit row with before and after.",
      },
      {
        heading: "Disagreements become findings",
        body: "A conflict on a specific address, and pending drift between the two systems, flow into the findings stream rather than being silently resolved by whichever side wrote last.",
      },
    ],
    seeAlso: ["ipam-page", "findings-stream", "audit-page"],
  },
  {
    id: "ipam-cross-cluster",
    title: "Cross-cluster IPAM",
    surface: "panel",
    summary:
      "Surfaces the same or an overlapping CIDR allocated in two attached clusters — the 'we used 10.20.0.0/24 in two places' problem, which no single cluster's own view can see.",
    docRef: "docs/features/ipam.md",
    keywords: ["cross cluster", "overlap", "duplicate", "cidr", "conflict", "federation", "ipam"],
    sections: [
      {
        heading: "What it detects",
        body: "Duplicate and overlapping subnets across every attached cluster, reported as a conflict naming both clusters. It's a latent addressing hazard that only becomes visible when something federates the view — which is exactly what this is.",
      },
      {
        heading: "When a cluster is down",
        body: "One unreachable cluster is isolated the same way every other fan-out isolates a down peer: you get the reachable clusters' conflicts plus an explicit note of which cluster didn't answer, rather than an empty or misleading all-clear.",
      },
    ],
    seeAlso: ["ipam-page", "federation", "settings-federation-page"],
  },
  {
    id: "ipv6-planning",
    title: "IPv6 planning grid and dual-stack wizard",
    surface: "panel",
    summary:
      "Takes a delegated prefix, enumerates its /64 blocks, and proposes one per currently v4-only network — a read-only planning aid, with a wizard to turn a proposal into a real subnet.",
    docRef: "docs/features/ipam.md",
    keywords: ["ipv6", "v6", "prefix", "dual stack", "slaac", "ra", "delegation", "/64"],
    sections: [
      {
        heading: "The grid is a plan, not a write",
        body: "Given something like a /56 from your ISP, the grid enumerates its /64-aligned blocks — the atomic unit for PVE SDN and for nearly every real v6 addressing plan — and proposes one per v4-only VLAN or VNet, aligned in ascending order for a deterministic, reviewable proposal. Blocks already occupied by a configured subnet render as allocated. Nothing here writes.",
      },
      {
        heading: "The dual-stack wizard",
        body: "Turning a proposed block into a real subnet goes through an ordinary `sdn.subnet.create` changeset. The wizard is idempotent: re-running it against a VNet that already has the requested v6 subnet yields a zero-operation changeset rendered as 'already up to date', not a duplicate.",
      },
      {
        heading: "What vnprox doesn't do here",
        body: "Prefix delegation from an upstream device vnprox doesn't manage is visibility-only — observed through router advertisements, never requested or configured by vnprox. Router-advertisement parameters beyond addressing (the M and O flags, DHCPv6 ranges) aren't controllable because PVE SDN's own subnet model has no such fields yet.",
      },
    ],
    seeAlso: ["ipam-page", "sdn-page", "sdn-zone-wizard"],
  },
  {
    id: "service-class-traffic",
    title: "Service-class traffic",
    surface: "panel",
    summary:
      "Breaks recent traffic down by what it's for — migration, backup, Ceph public, Ceph cluster, corosync — using flow metadata only, never payload inspection.",
    docRef: "docs/features/monitoring.md",
    keywords: ["service class", "migration", "backup", "ceph", "corosync", "attribution", "classification"],
    sections: [
      {
        heading: "How classification works",
        body: "Each flow is matched against registered network declarations — CIDR containment, known exact addresses, VLAN. Corosync's configured ring addresses and Ceph's public and cluster CIDRs are registered from real configuration. Nothing here inspects packet contents; vnprox is not an intrusion detection system.",
      },
      {
        heading: "Traffic on the wrong network",
        body: "The main thing this catches: backup or migration traffic riding the network you meant to keep for corosync, or Ceph replication crossing a link that wasn't sized for it. That raises its own finding rather than requiring you to notice a shape in a graph.",
      },
    ],
    seeAlso: ["flows-page", "dashboard-page", "findings-stream"],
  },
  {
    id: "blueprint-import",
    title: "Importing and instantiating a blueprint",
    surface: "dialog",
    summary:
      "Import verifies the artifact's signature against your trust store before anything else; instantiation produces an ordinary changeset containing only what's actually missing.",
    docRef: "docs/features/blueprints.md",
    keywords: ["import", "blueprint", "signature", "trust", "instantiate", "parameters", "apply"],
    sections: [
      {
        heading: "The trust gate",
        body: "An unsigned artifact, or one signed by a key you haven't trusted, stops here and requires an explicit decision from you. This is the same gate whether the artifact came from a file, a URL, or the hub — there is no path that installs on implicit trust.",
      },
      {
        heading: "Parameters",
        body: "A blueprint declares what varies between installations — node names, VLAN IDs, address ranges. You fill those in and see the resulting operations before anything is staged.",
      },
      {
        heading: "Idempotent by construction",
        body: "Instantiation stages only the operations needed to reach the described state. Anything that already matches is skipped, so running a blueprint twice is safe and the second run reads 'already up to date' rather than producing a conflicting draft.",
      },
    ],
    seeAlso: ["blueprints-page", "hub-page", "change-drawer"],
  },
  {
    id: "snapshot-restore",
    title: "Restoring a snapshot",
    surface: "dialog",
    summary:
      "Builds a new changeset containing the operations that would return your cluster to a chosen point in its history — reviewed, applied and confirmed like any other change.",
    docRef: "docs/features/change-management.md",
    keywords: ["restore", "snapshot", "revert", "rollback", "undo", "time machine"],
    sections: [
      {
        heading: "Restore is not a special path",
        body: "It doesn't reach behind the change engine or write files directly. You get a diff, a plan, a confirm window and automatic rollback — because restoring an old configuration can cut you off exactly as easily as applying a new one can.",
      },
      {
        heading: "Rolling back something you already confirmed",
        body: "For 7 days after a changeset commits, a manual rollback is offered directly from its history entry. Like a restore, it creates a new restoring changeset through the normal flow.",
      },
      {
        heading: "Read the diff",
        body: "The cluster has moved since the snapshot was taken. The diff shows what restoring would actually change *now*, which is not the same as undoing the one change you're thinking of — everything applied since is in scope too.",
      },
    ],
    seeAlso: ["snapshots-time-machine", "history-page", "commit-confirm"],
  },
  {
    id: "history-playback",
    title: "History playback",
    surface: "panel",
    summary:
      "Scrub through your cluster's recent network history and watch the map change — useful for 'when did this start' questions that a static diff can't answer.",
    docRef: "docs/features/change-management.md",
    keywords: ["playback", "scrub", "timeline", "replay", "animate", "when"],
    sections: [
      {
        heading: "What you're watching",
        body: "The map rendered at each point in the retained window, with changes and findings appearing where they occurred. Sampled flow data, where it's being collected, rides along the same scrubber.",
      },
      {
        heading: "Bounded on purpose",
        body: "Playback covers the retained window, not all history. vnprox keeps short-horizon operational data deliberately — it's a network tool that shows you recent state, not a time-series warehouse.",
      },
    ],
    seeAlso: ["history-page", "snapshots-time-machine", "flows-page"],
  },
  {
    id: "scheduled-apply",
    title: "Scheduled apply and maintenance windows",
    surface: "panel",
    summary:
      "Stage a change now and have it apply inside a future window, with the whole apply, confirm and rollback machinery running unchanged when it fires.",
    docRef: "docs/features/change-management.md",
    keywords: ["schedule", "maintenance", "window", "later", "deferred", "cron", "unattended"],
    sections: [
      {
        heading: "Re-validated at fire time",
        body: "The scheduler re-runs validation and recomputes whether the change touches a management path *fresh* at the window's start, never trusting the values from when you scheduled it. A change that was safe last Tuesday may not be safe tonight.",
      },
      {
        heading: "Management-path changes cannot be scheduled",
        body: "At all, unconditionally, server-side. A change that could cut a node off needs a human watching the clock — that's the entire premise of commit-confirm, and scheduling it away would defeat it.",
      },
      {
        heading: "Confirming a change that fired while you were asleep",
        body: "Two ways: the normal UI confirm, or a single-use, changeset-scoped signed callback token issued once when you scheduled it — the webhook path, for automation with no browser session. The rollback deadline is the earlier of your confirm window and the end of the maintenance window.",
      },
    ],
    seeAlso: ["changeset-review-page", "commit-confirm", "change-drawer"],
  },
  {
    id: "capture-panel",
    title: "Packet capture",
    surface: "panel",
    summary:
      "Short, bounded packet captures on a chosen interface with a guided BPF filter builder — for the questions no amount of configuration reading will answer.",
    docRef: "docs/features/monitoring.md",
    keywords: ["capture", "pcap", "tcpdump", "bpf", "sniff", "packets", "wireshark"],
    sections: [
      {
        heading: "Bounded by design",
        body: "Captures have a size and time limit and are taken on a named interface. This is a diagnostic reach-in, not a monitoring mode you leave running — and it requires the capture capability, which is a separate permission from ordinary network read.",
      },
      {
        heading: "The filter builder",
        body: "BPF syntax is easy to get subtly wrong in a way that silently captures nothing. The builder composes a filter from hosts, ports, protocols and VLANs and shows you the resulting expression, so you can check it before you start rather than after you've missed the event.",
      },
      {
        heading: "It's audited",
        body: "Capturing packets means reading traffic that isn't yours to read by default. Every capture writes an audit row naming who ran it, where, and with what filter.",
      },
    ],
    seeAlso: ["conntrack-page", "flows-page", "permissions", "audit-page"],
  },
  {
    id: "wireguard-connect-clusters",
    title: "Connect clusters over WireGuard",
    surface: "dialog",
    summary:
      "Guided setup for a WireGuard tunnel between clusters, so federation traffic and cross-cluster reads have a path that doesn't depend on both clusters sharing a network.",
    docRef: "docs/features/sdn.md",
    keywords: ["wireguard", "wg", "tunnel", "vpn", "connect", "federation", "peer", "link"],
    sections: [
      {
        heading: "What the wizard does",
        body: "Collects the endpoints and allowed address ranges, generates the key material, and stages the interface configuration on both ends as ordinary changeset operations — so a tunnel comes up through the same review and confirm flow as any other network change.",
      },
      {
        heading: "Tunnel changes revert unattended",
        body: "WireGuard operations are undone by the daemon itself, so a tunnel change that cuts you off rolls back at the confirm deadline without needing your session — unlike firewall and SDN operations, which need the sealed revert ticket.",
      },
      {
        heading: "The tunnel and the cluster link are separate things",
        body: "Bringing up a tunnel doesn't attach a cluster, and attaching a cluster doesn't require a tunnel. Where a federated cluster is reachable over a tunnel this wizard created, that linkage is recorded so you can see which transport a cluster is actually using.",
      },
    ],
    seeAlso: ["settings-federation-page", "federation", "commit-confirm"],
  },
  {
    id: "onboarding-walkthrough",
    title: "First-run setup",
    surface: "dialog",
    summary:
      "The short review vnprox walks you through on first login: what it found, which interfaces to protect, whether to enable physical discovery, and what's already inconsistent.",
    docRef: "docs/user-guide.md",
    keywords: ["onboarding", "first run", "setup", "walkthrough", "wizard", "getting started"],
    sections: [
      {
        heading: "Four steps, none of which change anything",
        body: "**What we found** — your cluster's network, drawn; nothing was changed, vnprox only read. **Protected interfaces** — confirm which interfaces carry each node's management IP and corosync traffic. **Physical discovery** — if `lldpd` isn't running, vnprox offers to enable it so the map can show real switch names and ports. **Health findings** — anything inconsistent it noticed.",
      },
      {
        heading: "The one thing that asks permission",
        body: "Enabling `lldpd` installs and starts a service on your hypervisors. That's the only step that would change anything, and it requires an explicit confirmation — declining it costs you switch-port detail on the map and nothing else.",
      },
      {
        heading: "Confirming protected interfaces matters",
        body: "This is what the safety interlocks are built on. Confirming the wrong interface protects something that doesn't need it while leaving the real management path exposed, so it's worth reading rather than clicking through.",
      },
    ],
    seeAlso: ["protected-interfaces", "ports-page", "findings-stream", "safety-model"],
  },
  {
    id: "in-app-assistant",
    title: "The in-app assistant",
    surface: "dialog",
    summary:
      "A drawer that asks vnprox's own read-only tools your question — using your session and your permissions — and can draft a change for review. It never applies one, and until you point it at a model backend it does nothing at all.",
    docRef: "docs/security.md",
    keywords: ["assistant", "ai", "ask", "mcp", "chat", "drawer", "citations", "question"],
    sections: [
      {
        heading: "No backend, no requests",
        body: "vnprox ships no model and no credential for one. The panel does nothing until you fill in an endpoint, a model name, and an optional API key under **Backend settings** — kept in memory for this browser tab only, never written to storage and never sent to vnprox itself. Your question and the tool results go straight from your browser to the backend you configured; vnproxd never sees them.",
      },
      {
        heading: "Every answer is cited, or it isn't shown",
        body: "The assistant runs the same read tools vnprox exposes over MCP against the local daemon, under your own session — a capability you lack is unreachable through it exactly as it is everywhere else, and your account's exact restrictions are named above the question box. An answer is rendered only if it cites something those tools actually returned; a reply that cites nothing, or cites something the tools didn't produce, is withheld rather than shown with a caveat.",
      },
      {
        heading: "It can draft, never apply",
        body: "When the answer implies a change, a **Proposed change** panel appears with a **Stage for review** button. Staging opens an ordinary draft changeset — tagged as the assistant's — and hands off to the normal review screen. There is no apply, confirm or rollback control anywhere in this panel; the component that renders it holds nothing capable of reaching one.",
      },
    ],
    seeAlso: ["mcp-ai-operators", "change-drawer", "changeset-review-page"],
  },
  {
    id: "map-annotations",
    title: "Notes and regions on the map",
    surface: "panel",
    summary:
      "Pin a free-text note to any entity, or draw a labelled region on the canvas — a shared team scratchpad for the things the map itself can't show, like 'temporary until the switch swap'.",
    docRef: "docs/features/topology.md",
    keywords: ["annotation", "note", "sticky note", "region", "label", "pin", "comment", "scratchpad"],
    sections: [
      {
        heading: "Notes",
        body: "Pinned to one entity from the inspector's **Notes** tab, and shown there and as a small marker on that entity on the Graph-view canvas. Every note carries who pinned it and when, and can carry an expiry — 7, 30 or 90 days, or never — so a temporary note announces its own staleness instead of quietly becoming permanent. Expiry is checked on every read, never left to the browser's own clock.",
      },
      {
        heading: "Regions",
        body: "Labelled rectangles you draw on the Graph-view canvas to mark an area — a rack, a cage, a set of nodes under maintenance. They hold their position under pan and zoom and survive layout changes and view switches, because they're stored separately from your personal saved layout rather than inside it.",
      },
      {
        heading: "Shared, and never lost",
        body: "Every `netRead`-capable user sees the same notes and regions, and anyone can unpin any note — this is a team scratchpad, not private data. A note on an entity that's later removed is kept and marked **Entity no longer exists** rather than dropped, because it's often the only record of why the entity was removed. Notes and region labels both appear in the documentation export, and the text is always rendered escaped, never as HTML.",
      },
    ],
    seeAlso: ["topology-page", "topology-inspector", "doc-export"],
  },
  {
    id: "presence-and-locks",
    title: "Presence and draft locks",
    surface: "panel",
    summary:
      "Staging a change takes an advisory lock on the entities it touches, and the drawer shows who else is looking at a changeset — so two people editing the same bridge find out about each other instead of silently overwriting each other's work.",
    docRef: "docs/api.md",
    keywords: ["presence", "lock", "advisory lock", "collision", "who else", "viewing", "take over the lock"],
    sections: [
      {
        heading: "Advisory means it never blocks you",
        body: "A lock warns; it doesn't refuse. If you stage or edit a draft that touches an entity someone else already has a draft open against, your change is saved exactly the same — the drawer just tells you who else has a claim, and lets you proceed deliberately. Apply never consults a lock, and can't: a lock prevents an accidental change, never an emergency one.",
      },
      {
        heading: "Taking over",
        body: "**Take over the lock** doesn't touch your changeset's operations at all — it re-submits the same ones with the claim transferred to you, so the other operator is the one warned next time. Every takeover is recorded in the audit log, naming who held the lock before.",
      },
      {
        heading: "Presence",
        body: "While you have a changeset open, the drawer shows a quiet 'N other people are viewing this' line — nothing renders when you're alone with it. Presence comes from who's currently connected rather than anything stored, and it clears the moment their session ends.",
      },
      {
        heading: "Locks release themselves",
        body: "A lock expires on a timeout, releases the moment its holder's session ends (closing a laptop, not a button click), and disappears entirely once its draft is discarded. There's no separate 'unlock' action to remember.",
      },
    ],
    seeAlso: ["change-drawer", "changeset-review-page", "audit-page"],
  },
  {
    id: "topology-point-in-time-diff",
    title: "Point-in-time topology diff",
    surface: "panel",
    summary:
      "Compares your cluster at two points in time — a snapshot, a timestamp, or right now — and shows exactly what changed, entity by entity, naming the changeset responsible or flagging a change nothing explains.",
    docRef: "docs/api.md",
    keywords: ["diff", "topology diff", "point in time", "compare", "unattributed", "out of band", "what changed"],
    sections: [
      {
        heading: "Where to start it",
        body: "From **History**, pick a **From** and a **To** point (or 'vs live') and switch the **Diff** toggle to **Topology** — as opposed to **Files**, the older, literal unified diff of the raw `/etc/network/interfaces` text. **Show on map** opens the same range as an overlay on the Topology page, ringing every changed entity: a dashed ring for a change a changeset explains, a thicker solid one for a change nothing does.",
      },
      {
        heading: "The unattributed count is the point",
        body: "Every difference names whether a changeset explains it. One that doesn't is a change made outside vnprox — someone SSHed in and edited the file, or ran an `ip` command — which is exactly the class of change the drift checker exists to catch. That count is called out on its own, in both the History panel and the map overlay's status line, rather than folded into an undifferentiated 'N changes'.",
      },
      {
        heading: "What's compared, stated rather than assumed",
        body: "The diff covers each node's `/etc/network/interfaces` — bridges, bonds, VLANs, addressing. SDN zones and VNets aren't diffed yet. A node captured at only one end of the range is named as such rather than having every interface on it reported as created or deleted.",
      },
      {
        heading: "An honest error beats an empty diff",
        body: "Asking for a range vnprox has no snapshot old enough to cover returns a stated error naming the snapshots that do exist — never a diff that quietly comes back empty and reads as 'nothing changed'.",
      },
    ],
    seeAlso: ["history-page", "topology-page", "drift", "incidents-page"],
  },
  {
    id: "post-apply-preview",
    title: "Post-apply preview",
    surface: "panel",
    summary:
      "See what the map would look like after a staged changeset applies — before you click Apply — with every added, removed and changed entity marked on a distinct preview mode of the map.",
    docRef: "docs/features/change-management.md",
    keywords: ["preview", "post-apply", "post apply", "what will change", "projection", "before apply"],
    sections: [
      {
        heading: "Where to find it",
        body: "**Preview the post-apply map**, on a changeset's **Blast radius** tab, opens the Topology page in preview mode. A deleted entity is drawn back onto the map, struck through rather than simply missing — an absence is exactly the kind of change nobody notices, so it's shown, never omitted.",
      },
      {
        heading: "Best-effort, and it says so",
        body: "The projection folds the changeset's operations into the live inventory in memory — nothing is written anywhere, and nothing here touches the store or PVE. Some operations have no representation in the entity graph at all (a raw interfaces-file edit, a firewall, QoS, NAT or WireGuard change): those are listed by name with a reason as **unprojectable**, in the map's own status line, rather than silently left out of the picture.",
      },
      {
        heading: "No preview for a changeset that can't apply",
        body: "A changeset with blocking validation findings has no post-apply state to show, so the preview is refused rather than rendering a map no real sequence of events could produce — the live map is shown instead, with a message naming why.",
      },
    ],
    seeAlso: ["changeset-review-page", "topology-page", "validation-findings"],
  },
];
