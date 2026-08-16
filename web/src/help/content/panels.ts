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
  {
    id: "canary-apply",
    title: "Canary apply and the rollout hold",
    surface: "panel",
    summary:
      "Apply a multi-node changeset to a few nodes first, hold there while you look at them, and only then apply the rest — with the commit-confirm window covering the whole sequence rather than each stage.",
    docRef: "docs/features/change-management.md",
    keywords: ["canary", "staged apply", "rollout", "hold", "continue", "gate", "auto-rollback", "phased"],
    sections: [
      {
        heading: "All at once, or a canary first",
        body: "**All nodes at once** is the default and is what apply has always done: every affected node changes together. **Canary** applies only the nodes you pick, then pauses. The pause is recorded on the server, not in your browser — reload the page, open another tab, or restart the daemon and the same hold is still there, with the same nodes on each side of it.",
      },
      {
        heading: "What the gate is waiting for",
        body: "A **manual** gate waits for you to press Continue after looking at the canary nodes. An **automatic** gate promotes at the hold deadline only when the canary nodes are healthy and no new error-severity finding is attributable to them; a hold that no findings cycle completed inside is reported un-clean rather than promoted on silence. Either way, aborting restores exactly the nodes that were applied and never contacts the ones that were not.",
      },
      {
        heading: "The window covers the whole sequence",
        body: "The commit-confirm deadline is set once, when the sequence starts, and every hold deadline is clamped to it. A hold still pending when the window elapses rolls back everything applied so far — a stalled canary cannot hold the cluster open. That is also why a hold must be shorter than the window: a hold that fills it leaves no time to apply the remaining nodes.",
      },
      {
        heading: "Auto-rollback on a new error finding",
        body: "Off by default, and orthogonal to the fan-out: it governs the commit-confirm **window**, not which nodes are applied or in what order. When armed, a new error-severity finding attributable to something the changeset touched, arriving inside the window, rolls it back immediately instead of waiting the window out. Pre-existing findings never trigger it, and warnings never trigger it at all. During a canary hold it aborts the sequence rather than rolling back a plan half of which was never applied. Leaving the box unticked asks for the cluster default (`auto_rollback_on_error`, itself off) rather than asserting off — the browser cannot read that setting. It is an in-memory guard: a daemon restart drops it, and the commit-confirm timer remains the safety net either way.",
      },
      {
        heading: "When canary is not offered",
        body: "A changeset touching fewer than two nodes has no second stage to hold before, and a plan carrying cluster-scope steps that must run before every per-node step cannot be split in either direction without violating the plan's own ordering. In both cases the option is disabled with the reason stated — vnprox refuses the strategy before taking a snapshot or mutating anything, leaving the changeset exactly where it was.",
      },
      {
        heading: "Reading the rollout view",
        body: "While a rollout is held, each affected node is shown as applied, not contacted, or unknown. **Unknown is a real answer**: it means the server did not place that node on either side of the hold, and it is never quietly drawn as one of the other two. A node reported as unknown should be checked directly before you continue or abort.",
      },
    ],
    seeAlso: ["changeset-review-page", "commit-confirm", "safety-model", "findings-stream", "scheduled-apply"],
  },
  {
    id: "gitsync-status",
    title: "Git spec sync",
    surface: "panel",
    summary:
      "What the git repository holding your cluster's declared intent is doing: which remote, which ref, which document, when it last fetched, and — the part that matters on a bad day — what went wrong.",
    docRef: "docs/api.md",
    keywords: ["gitsync", "git", "gitops", "repo", "remote", "ref", "branch", "sync", "signed", "commit"],
    sections: [
      {
        heading: "Off is an answer, not an error",
        body: "With no `[gitsync]` section in the daemon's configuration, nothing is fetched and no endpoint is contacted — the panel says so plainly and offers no controls. That state is deliberately rendered differently from a sync that is configured and failing, and differently again from vnprox being unable to read the status at all. Those are three different situations and only one of them needs your attention right now.",
      },
      {
        heading: "A failing sync says why",
        body: "A bad ref, an unreachable remote and an authentication failure are different problems, so the panel shows the daemon's own error text rather than a generic 'sync failed'. It also keeps showing the details from the last cycle that got far enough to produce them, since a failed cycle stages nothing and tells you nothing new about the plan.",
      },
      {
        heading: "The sync stages, never applies",
        body: "When the repository and the cluster disagree, vnprox opens one draft changeset and stops — it never applies, never pushes, never merges and never decides the document wins. There is only ever one open sync draft: a second detected divergence updates it rather than piling up drafts. The link here takes you to that draft in the ordinary review screen.",
      },
      {
        heading: "Signature verification",
        body: "With signed commits required, a commit whose signature this daemon cannot verify locally is refused and nothing is staged — an unsigned commit, an unsupported key algorithm and a signer missing from the allowed-signers file are all refusals. The last verified signer is shown when signatures are required, and explicitly as not applicable when they are not.",
      },
    ],
    seeAlso: ["config-as-code-page", "spec-pin", "spec-reconciliation", "changeset-review-page"],
  },
  {
    id: "spec-pin",
    title: "The spec document",
    surface: "panel",
    summary:
      "The declarative YAML document describing this cluster's L2 and SDN intent — rendered from live state, pasted in, or pinned so the drift checks have something to compare against.",
    docRef: "docs/api.md",
    keywords: ["spec", "pin", "pinned", "yaml", "document", "declarative", "export", "intent"],
    sections: [
      {
        heading: "Rendering the live cluster",
        body: "'Render the live cluster' exports what the cluster currently is as a `specVersion: 1` document. The export is byte-stable — two exports of an unchanged cluster are byte-identical, which is what makes a `git diff` against it empty when nothing has changed. It reads only; producing it changes nothing.",
      },
      {
        heading: "What pinning does and does not do",
        body: "Pinning stores that document as the declared desired state, so the drift checks compare live state against it every cycle. It applies nothing by itself, and it is app-owned data — never a shadow copy of PVE's configuration. Re-pinning replaces the previous document in place; you do not need to unpin first. Clearing the pin removes the comparison, not anything on the cluster.",
      },
      {
        heading: "Pin or git, or both",
        body: "The spec position can come from the pin or from the git sync. With neither, there is no spec position at all, and the reconciliation panel says so rather than reporting agreement — a question nobody asked is not a clean bill of health.",
      },
    ],
    seeAlso: ["config-as-code-page", "spec-plan", "gitsync-status", "blueprints-page"],
  },
  {
    id: "spec-plan",
    title: "Planning a spec against live state",
    surface: "panel",
    summary:
      "Diffs the working document against the cluster as it is and shows what it would take to make them agree — the drift-detection primitive the automation contract specifies for a terraform-plan-shaped check.",
    docRef: "docs/api.md",
    keywords: ["plan", "import", "diff", "dry run", "terraform", "spec", "notinspec", "preview"],
    sections: [
      {
        heading: "A plan stages a draft",
        body: "There is no read-only plan route: the plan is the response to a spec import, and that import creates a draft changeset for its result — every time, including when the result is empty. The button says so. Nothing is applied, and a draft you only wanted as a question can be discarded from the review screen.",
      },
      {
        heading: "Two different facts",
        body: "**Operations** are what the document says has to change about the cluster. **Not in the document** are entities the cluster has that the document never mentions. Both are always reported, including when both are zero, because they answer different questions — 'nothing to change' and 'nothing undeclared' are separate statements and a clean plan is both of them.",
      },
      {
        heading: "Nothing is ever pruned",
        body: "An entity absent from the document is reported and left alone. Spec import has no prune path and emits no delete operations, so importing a partial document can never quietly remove the bridges it happens not to mention.",
      },
    ],
    seeAlso: ["config-as-code-page", "spec-pin", "changeset-review-page", "validation-findings"],
  },
  {
    id: "spec-reconciliation",
    title: "Restoring intent and adopting reality",
    surface: "panel",
    summary:
      "When spec, config and live disagree about one entity, these are the two ways out — one moves the cluster to match the document, the other moves the document to match the cluster.",
    docRef: "docs/api.md",
    keywords: [
      "reconcile",
      "restore intent",
      "adopt reality",
      "three-way",
      "spec",
      "config",
      "live",
      "pull request",
      "propose",
    ],
    sections: [
      {
        heading: "All three pairs, always",
        body: "Each finding shows spec-vs-config, config-vs-live and spec-vs-live — including the pairs that agree, because which pair agrees is what identifies the odd position out. A field a position never reported is shown as not reported, never as an empty value: 'we don't know' and 'it is blank' are different, and collapsing them would invent divergence.",
      },
      {
        heading: "Restore intent",
        body: "Stages a draft changeset bringing the cluster back to what the document declares. The operations are computed by the daemon from the finding itself and are never sent from the browser. It stages and stops — validating, applying and confirming it are your own separate steps in the ordinary review screen, with the usual blast radius and commit-confirm window.",
      },
      {
        heading: "Adopt reality",
        body: "Rewrites the document to describe the entity as the cluster currently has it, as a pull request on the spec repository. It changes nothing about the cluster. vnprox opens the request and stops: it never merges, approves or polls one. It needs a write-capable spec repository configured; without one, the daemon refuses with that reason rather than failing vaguely.",
      },
      {
        heading: "Offered only when it would do something",
        body: "Each action appears only if performing it would produce a non-empty result. A finding offering neither is ordinary and honest — a divergence that exists only between the file and the kernel is real, and no spec commit resolves it. Neither action is ever taken automatically, at any severity.",
      },
    ],
    seeAlso: ["config-as-code-page", "gitsync-status", "drift", "changeset-review-page", "safety-model"],
  },
  {
    id: "spof-score",
    title: "Failure simulation and the SPOF score",
    surface: "panel",
    summary:
      "Answers 'what breaks if this NIC, bond or switch dies?' by removing each element from a copy of the current inventory and recomputing connectivity — and says so plainly when it cannot decide.",
    docRef: "docs/api.md",
    keywords: ["spof", "failure", "simulation", "resilience", "single point of failure", "quorum", "score"],
    sections: [
      {
        heading: "Simulated, never induced",
        body: "The simulation removes an entity from a *copy* of the live inventory snapshot. It never takes anything down, never writes, and never stores a result — every verdict is recomputed fresh from the current snapshot each time you look. There is no changeset operation anywhere in vnprox that a failure simulation can produce.",
      },
      {
        heading: "Four verdicts, and one of them is 'indeterminate'",
        body: "Critical means quorum, a management path or a Ceph network is at risk. Degrades means guests lose an uplink or SDN segments are stranded. No known impact is claimed only when every dimension was actually evaluated. Anything else — including a dimension the simulator had no model for — reads as **indeterminate**, which is not a mild verdict: it means nobody knows, and it sorts immediately after critical for that reason.",
      },
      {
        heading: "Why a dimension goes unevaluated",
        body: "Quorum needs a corosync configuration whose ring addresses resolve to real interfaces. Ceph needs a Ceph read model that declares networks. Tunnels need a WireGuard model. Guest connectivity needs every guest's NIC attachment to resolve. Where one of those is missing, that dimension is named as not evaluated rather than reported as safe — a distinction the entire panel is built around.",
      },
      {
        heading: "The score",
        body: "One hundred minus a weight per single point of failure, floored at zero, so fewer and lower-impact single points of failure score higher. It is a summary of the list below it, not an independent measurement, and an empty list genuinely means none were found — purely redundant elements are excluded before the list is built.",
      },
    ],
    seeAlso: ["analysis-page", "path-simulator", "protected-interfaces", "topology-page"],
  },
  {
    id: "capacity-export",
    title: "Capacity history export",
    surface: "panel",
    summary:
      "Downloads one link's or one IPAM pool's daily utilization history as CSV or JSON, bounded to the retention window the daemon is configured to keep.",
    docRef: "docs/features/monitoring.md",
    keywords: ["capacity", "export", "csv", "history", "utilization", "retention", "forecast", "trend"],
    sections: [
      {
        heading: "History, not forecast",
        body: "This is the raw daily series behind the forecasts, not the forecast itself. A projected capacity crossing reaches you as an ordinary finding in the findings stream, where it can be acknowledged, filtered and alerted on like any other — so there is deliberately no second forecast screen here to keep in sync with it.",
      },
      {
        heading: "What has history at all",
        body: "Links are rolled up only for physical NICs with a negotiated speed: without one there is no percentage to record. Bonds are not rolled up individually. Pools are rolled up per subnet with a nonzero size. Only entities the rollup actually writes are offered, because an empty export from an entity that was never collected reads exactly like an entity with no traffic, and they are not the same thing.",
      },
      {
        heading: "The retention bound",
        body: "The daemon clamps every export to its configured `[capacity] aggregate_retention_days`, computed from its own clock, so buckets older than that are absent even in the gap between prune ticks. That configured value is not exposed to the browser, so this panel names the setting and its default rather than claiming to know yours — an export that silently stops at a boundary nobody stated is a trap.",
      },
      {
        heading: "One bucket per complete day",
        body: "The rollup writes one bucket per complete UTC day, so an entity discovered today has none yet and that is not a fault. Buckets are stamped at midnight UTC and rendered in UTC here, because showing them in your own timezone would move a bucket onto the wrong calendar day west of Greenwich.",
      },
    ],
    seeAlso: ["analysis-page", "findings-stream", "ipam-page", "topology-inspector"],
  },
  {
    id: "qos-shaping",
    title: "QoS shaping",
    surface: "panel",
    summary:
      "Every traffic shape vnprox has applied to a bridge, and the three edits you can make to them — each of which becomes an ordinary reviewable changeset rather than a direct write.",
    docRef: "docs/api.md",
    keywords: ["qos", "shaping", "rate", "ceiling", "htb", "tc", "bandwidth", "priority"],
    sections: [
      {
        heading: "Applied, not observed",
        body: "The list is read from vnprox's own store of shapes it has applied, not from a live `tc` dump. So a shape here is one vnprox put there. A rate limit somebody applied by hand outside vnprox will not appear, which is the honest reading of what this store can know — and it is also why the map's shaping badge draws from the same source.",
      },
      {
        heading: "Editing stages a changeset",
        body: "There is no QoS write route. Creating, changing or removing a shape builds a `qos.shape.create`, `qos.shape.update` or `qos.shape.delete` operation and lands it in the change drawer, where it goes through the same validate, diff, apply and confirm path as an interface edit. Nothing on this panel touches a shape directly, and a staged edit is not applied until you apply it.",
      },
      {
        heading: "Re-scoping is remove-and-recreate",
        body: "Changing a shape's rate, ceiling or priority is an edit. Changing which traffic it selects — its match CIDR or VLAN — is not: that is a different shape, and it is expressed as two visible operations, a delete and a create. The edit form therefore does not offer the match fields, so the operation list always says what actually happened.",
      },
    ],
    seeAlso: ["analysis-page", "change-drawer", "topology-page", "safety-model"],
  },
  {
    id: "ipv6-segments",
    title: "IPv6 segments",
    surface: "panel",
    summary:
      "What each segment is actually advertising — router advertisements, their prefixes, and whether a DHCPv6 server answered — observed live per node rather than read from configuration.",
    docRef: "docs/features/sdn.md",
    keywords: ["ipv6", "ra", "router advertisement", "slaac", "dhcpv6", "dual stack", "prefix", "segment"],
    sections: [
      {
        heading: "An observation, not a configuration read",
        body: "Each entry comes from an actual router-advertisement solicitation on that interface, on that node. That makes it the ground truth for 'is v6 really working on this segment', and it is why the read is slow and polls infrequently: it is a live measurement across every bridge and VLAN interface in the cluster.",
      },
      {
        heading: "An empty table means none were seen",
        body: "An interface appears here only when an advertisement was actually observed, so nothing listed means nothing was seen — it does not mean IPv6 is disabled, and it does not mean no subnet is configured. For the same reason, a VLAN or VNet that is still v4-only has no row at all, which is the normal starting point for a dual-stack rollout rather than a problem.",
      },
      {
        heading: "A node that did not answer",
        body: "The cluster fan-out tolerates individual node failures: one node's read failing never blanks the rest. When that happens the panel names the nodes that did not answer alongside the rows it did get, so a missing segment is visibly missing rather than quietly absent.",
      },
      {
        heading: "Implied is not observed",
        body: "A DHCPv6 server the advertisement's managed flag implies and a DHCPv6 server actually seen answering are shown differently on purpose. The first is what the router claims should exist; the second is what does. Collapsing them would turn a misconfiguration into a clean reading.",
      },
    ],
    seeAlso: ["analysis-page", "ipv6-planning", "sdn-page", "ipam-page"],
  },
  {
    id: "wan-health",
    title: "WAN and upstream health",
    surface: "panel",
    summary:
      "Continuous reachability, latency and loss against reference hosts you name, per uplink — the evidence for 'it is the ISP, not the cluster'.",
    docRef: "docs/api.md",
    keywords: ["wan", "uplink", "isp", "upstream", "latency", "loss", "availability", "multi-wan"],
    sections: [
      {
        heading: "The verdict is computed where both halves are visible",
        body: "'Likely your ISP' is a deliberately narrow claim: it fires only when a WAN uplink is degraded **and** every other finding in the cluster is below warning severity. Without that second half the verdict is the plainer 'WAN degraded' — the same upstream signal without the confirmation that nothing else is wrong. The daemon computes this, because it is the only place that can see both the probe results and the findings stream at once.",
      },
      {
        heading: "Per uplink, never blended",
        body: "Multiple uplinks on one node are always reported independently. An uplink whose configured target has produced no reading yet has no entry at all rather than a stale placeholder, so an empty row is never mistaken for a healthy one. This node probes only its own targets; the cluster-wide picture is the union of each node's own view.",
      },
      {
        heading: "Naming a target",
        body: "A reference host must be an IP address or a DNS name. Targets are dialed by a root-owned prober, so anything option-shaped or shell-hostile is refused outright — and the refusal names the value it rejected instead of failing vaguely. Saving targets replaces the whole set for this node rather than patching it, so removing one means saving the rest.",
      },
      {
        heading: "Diagnosis, not failover",
        body: "Nothing here switches an uplink or reroutes anything. There is no changeset operation for WAN failover anywhere in vnprox, and the probe loop calls no mutating route. What this panel gives you is the evidence to take to whoever owns the link.",
      },
    ],
    seeAlso: ["analysis-page", "edge-page", "findings-stream", "path-simulator"],
  },
  {
    id: "platform-tokens",
    title: "Automation tokens: stored scope and effective scope",
    surface: "panel",
    summary:
      "Mint a capability-scoped bearer token without curl, see when each one expires, and see whether this deployment is currently narrowing what a token can actually do.",
    docRef: "docs/api.md",
    keywords: ["token", "bearer", "mint", "scope", "expiry", "read only", "revoke", "automation", "curl"],
    sections: [
      {
        heading: "A token never exceeds you",
        body: "Every scope you can tick is one you already hold on at least one node; the rest are disabled and say why. The daemon re-checks this when it mints, so a token can never carry a capability its creator did not have at the moment of creation. The one exception is `automation`, which is not derived from any Proxmox privilege at all — holding a vnprox session is the whole bound on granting it.",
      },
      {
        heading: "Expiry is now the default, not the exception",
        body: "A token minted with nothing said about expiry lives 90 days. You can pin a different instant, or opt out of expiry entirely, but opting out is an explicit choice rather than what happens when you say nothing. Tokens minted before expiry existed have none and keep working — expiry is never applied backwards — so 'never expires' in the list is a real state, not a missing value.",
      },
      {
        heading: "Stored scope is not always effective scope",
        body: "In a read-only deployment the daemon removes every write scope from a token on every request, whatever the token's stored scope says. The list shows both, and names the scopes being removed, because a token that looks write-capable and is not is precisely the confusion this panel exists to end. Until the instance configuration has loaded, the effective scope is reported as unknown rather than assumed unchanged.",
      },
      {
        heading: "Shown once",
        body: "The raw bearer value crosses the wire exactly once, in the response to the mint request. It is stored only as a hash, so no route can ever hand it back. If you lose it, revoke the token and mint another; revoking also force-closes any live event subscription that token had authenticated.",
      },
    ],
    seeAlso: ["platform-panel-page", "tokens-and-embeds", "read-only-mode", "permissions"],
  },
  {
    id: "platform-webhooks",
    title: "Webhooks and the destination policy",
    surface: "panel",
    summary:
      "Delivery targets for vnprox's event stream, and — more usefully — the reason the daemon gives when it refuses one of them.",
    docRef: "docs/security.md",
    keywords: ["webhook", "delivery", "hmac", "ssrf", "private address", "metadata", "loopback", "signature"],
    sections: [
      {
        heading: "Why a destination gets refused",
        body: "The daemon runs as root on a hypervisor with reach into your management network, so an unconstrained webhook target is a way to make it fetch things on an attacker's behalf. Public HTTPS destinations are permitted by default; plain HTTP, loopback, RFC1918, and link-local addresses — which includes the cloud metadata address — are not. Each refusal names the configuration knob that would permit it, and this panel shows that message unedited.",
      },
      {
        heading: "Refused twice, on purpose",
        body: "The address class is checked when you register a target, and checked again against the address actually being connected to at every single delivery. A hostname that resolves publicly today and to loopback tomorrow is refused at the socket rather than at the URL, so re-pointing DNS after registration does not get past the guard.",
      },
      {
        heading: "This screen may not be able to reach it",
        body: "The webhook routes require the automation capability, and that capability is never granted by logging in — it exists only on a bearer token minted with it in scope. A browser session therefore gets a refusal here rather than a list, and the panel shows that refusal instead of an empty table, because 'you may not look' and 'there is nothing here' are different facts. Mint an automation-scoped token in the section above and use it from a client that can send it.",
      },
      {
        heading: "Delivery health is not assumed",
        body: "A registration that has never had an event match it shows as never attempted, not as healthy: a zero failure count before any attempt says nothing. Three consecutive failed delivery sequences raise a finding in the ordinary findings stream, and a subsequent success clears it on the next cycle.",
      },
    ],
    seeAlso: ["platform-panel-page", "findings-stream", "alert-rules-page", "tokens-and-embeds"],
  },
  {
    id: "platform-plugins",
    title: "Installed plugins and their capability ceiling",
    surface: "panel",
    summary:
      "What extensions this deployment is carrying, exactly what each one is permitted to touch, and how to stop or remove one — but never how to install one.",
    docRef: "docs/api.md",
    keywords: ["plugin", "extension", "capability", "enable", "disable", "uninstall", "sdk", "ceiling"],
    sections: [
      {
        heading: "The capability list is a ceiling",
        body: "What a plugin declares is the maximum it may ever reach: which internal seams it can read and which classes of change-engine operation it may stage. It is not a description of what the plugin usually does. A plugin can put a change into the drawer for you to review, and is never itself a way to apply one — that boundary holds regardless of what it declared.",
      },
      {
        heading: "Installing happens somewhere else, deliberately",
        body: "There is no install control here, and adding one would be a way around the trust gate. Installation goes through the Hub, which verifies the artifact's signature against the same trust store blueprint bundles use, refuses outright when the downloaded manifest declares different capabilities than the catalog listing you read before consenting, and only then registers it — where the scope is validated a second time, independently.",
      },
      {
        heading: "Disable is not uninstall",
        body: "Disabling stops dispatch to a plugin's extension points while leaving it installed, which is the right first move when you suspect one is misbehaving. Uninstalling additionally tears down any supervised subprocess it was running. Both transitions, like installing, write an audit row recording the plugin's capability scope, so a capability that appeared in your deployment is traceable to when and by whom.",
      },
    ],
    seeAlso: ["platform-panel-page", "plugins", "hub-page", "audit-page"],
  },
  {
    id: "platform-doctor-live",
    title: "The daemon's live self-check",
    surface: "panel",
    summary:
      "The four health checks that need a credential only the running daemon holds, reported as four distinct verdicts — and a skipped check is never one of the passing ones.",
    docRef: "docs/deployment.md",
    keywords: ["doctor", "self check", "live", "diagnostics", "skip", "pve token", "clock skew", "peer secret"],
    sections: [
      {
        heading: "Four checks, not ten",
        body: "The command-line doctor runs ten checks; this route runs the four that cannot be answered without the daemon's own credentials — whether Proxmox is reachable, whether its token holds the privileges vnprox uses, whether every node agrees on the cluster secret, and whether the clock is close enough to a reference. The other six observe the machine the command line is standing on, and the daemon deliberately does not answer them on its behalf.",
      },
      {
        heading: "Skipped is not passed",
        body: "A skipped check means the daemon could not look, and it says why. It is counted separately, styled separately, and worded separately from a pass, because a wall of skips under a 'nothing failed' summary reads as success to a tired operator. If nothing passed, the verdict says so outright rather than reporting a clean run. Two of these four still skip by design on an ordinary deployment.",
      },
      {
        heading: "Warned is not failed either",
        body: "A warning means the check ran and found something degraded but working — an optional privilege missing, a clock a little further out than ideal. It does not make the run a failure, and it is deliberately not folded into either neighbouring verdict. Every warning and every failure carries a remediation line; that is the difference between a diagnostic and a complaint.",
      },
      {
        heading: "Not being allowed to look",
        body: "Reading this section needs the audit capability, the same one the audit log and the support bundle need, because the result says which privileges the configured Proxmox token holds. If your session lacks it you get an explicit refusal here — which is emphatically not a report that the checks failed.",
      },
    ],
    seeAlso: ["platform-panel-page", "permissions", "audit-page", "safety-model"],
  },
  {
    id: "policies-panel",
    title: "Policy as code",
    surface: "panel",
    summary:
      "The cluster's declarative rule set: which rules are installed, what each one matches and asserts, whether it blocks or only annotates, and how often it has actually fired.",
    docRef: "docs/features/change-management.md",
    keywords: ["policy", "policy as code", "rules", "deny", "warn", "assert", "match", "guardrail"],
    sections: [
      {
        heading: "What a rule is",
        body: "A rule matches operations by their fields and asserts something that must hold for every operation it matched. A `deny` rule blocks the apply inside the change engine's validate stage; a `warn` rule annotates the changeset and blocks nothing. A rule with no assertions at all is not a rule that always passes — it means the match itself is the violation, which is how you express \"never touch this\" with nothing to assert beyond the operation's existence.",
      },
      {
        heading: "A rule that never matches is reported",
        body: "The daemon tracks how many evaluations each rule has been through and how many operations it has matched. A rule that has been evaluated enough times over a long enough window without ever matching anything is flagged as probably misconfigured — because a rule guarding nothing looks exactly like a rule guarding something until the day you need it. It is a report and never a refusal: a rule you know is simply rare stays installed and keeps working.",
      },
      {
        heading: "Installing is wholesale",
        body: "There is no per-rule edit. A rule set is reviewed and installed as a unit, so the editor takes the whole document and the daemon validates all of it before writing any of it — a malformed set stores nothing and audits nothing, and the refusal names the offending rule and field. Re-installing an identical set is a no-op with no new revision. A successful install audits the full body of every rule added, removed or changed.",
      },
    ],
    seeAlso: ["governance-page", "policy-verdict", "validation-findings", "change-drawer"],
  },
  {
    id: "policy-verdict",
    title: "Why policy refused this change",
    surface: "panel",
    summary:
      "Inside the review screen: which installed rule objects to this changeset, at what severity, and exactly which assertions had to hold — rather than one more line in a list of validation errors.",
    docRef: "docs/features/change-management.md",
    keywords: ["policy", "deny", "refused", "blocked", "rule", "assert", "violation", "review"],
    sections: [
      {
        heading: "Where the rule id comes from",
        body: "The change engine folds a violation into the changeset as a message, not as structured data — there is no rule id anywhere on that wire shape. So this panel asks the daemon two questions instead of reading the message: which installed rules this changeset violates, and what each of those rules asserts. Everything you read beside a rule came from one of those two answers. Nothing here composes a reason of its own.",
      },
      {
        heading: "Three unknowns that are not the same as anything else",
        body: "If the rule set could not be read, this says so rather than reporting that nothing applies — being unable to see your guardrails is not the same as not having any. If a violated rule is no longer in the installed set, its assertions are reported as unreadable rather than as absent, because a rule with genuinely no assertions means something stronger, not weaker. And a severity this build does not recognise is shown as unrecognised and treated as blocking, never quietly folded into `warn`.",
      },
      {
        heading: "It counts operations, it does not name them",
        body: "A rule reports how many of the daemon's evaluated operations violated it, not which of your changeset's operations they were. The evaluator runs over an expanded operation list — a single raw-replace operation becomes several — so the positions it reports are not positions in the list on your screen. Naming an operation from them would name the wrong one.",
      },
    ],
    seeAlso: ["policies-panel", "changeset-review-page", "validation-findings", "governance-page"],
  },
  {
    id: "break-glass",
    title: "Emergency break-glass",
    surface: "panel",
    summary:
      "The reasoned override of the two-person rule, for when the second approver genuinely cannot be reached — audited under its own action and raising a finding nobody can clear for a day.",
    docRef: "docs/features/change-management.md",
    keywords: ["break glass", "breakglass", "override", "emergency", "two person", "approval", "audit"],
    sections: [
      {
        heading: "What it actually overrides",
        body: "The distinct-approver count, and nothing else. Validation still runs, the review-approval requirement still applies, peer compatibility is still checked, and the apply is still refused if any of those refuses. If your change is blocked by a policy `deny` or by a validation error, break-glass will not move it — that is not what it is for.",
      },
      {
        heading: "What it costs you",
        body: "An audit entry under its own action, `change.breakglass`, naming you, the changeset and the reason you typed — deliberately not a result value on the apply, so an auditor filtering for overrides finds it without knowing which apply outcomes imply one. And an error-severity finding that nobody, including you, can acknowledge for twenty-four hours: it is meant to be reviewed by someone who was not in the room when it was taken.",
      },
      {
        heading: "Why it takes three clicks",
        body: "Opening the panel shows you the consequences; a separate control acknowledges them; only then does the reason field and the record button exist. A written reason is required, and the daemon refuses an override without one rather than relying on the form to insist. An override you could take by accident, or without having read what it does, would be worse than the refusal it replaces.",
      },
      {
        heading: "It does not survive an edit",
        body: "The override is pinned to the operations it was taken for. Edit the draft afterwards and it no longer applies — the apply is refused again and a fresh override has to be taken, with a fresh reason and a fresh audit entry. That is what stops an override taken for one emergency from quietly authorising a different change later.",
      },
    ],
    seeAlso: ["changeset-review-page", "policy-verdict", "safety-model", "audit-page"],
  },
  {
    id: "compliance-panel",
    title: "Compliance profiles",
    surface: "panel",
    summary:
      "What this cluster can evidence about itself, control by control, mapped from findings, posture factors and policy rules — with three distinct ways of saying \"not a pass\".",
    docRef: "docs/api.md",
    keywords: ["compliance", "control", "evidence", "unmapped", "not evaluated", "profile", "audit", "report"],
    sections: [
      {
        heading: "This is not a certification surface",
        body: "No report here asserts compliance with CIS, PCI-DSS, HIPAA, SOC 2, ISO 27001 or any other published framework, and no profile vnprox ships is named after one. Each report carries the profile's own notice saying what it does not claim, and a standing test fails the build if a shipped profile names a framework. What a profile is is a declarative mapping from control ids onto evidence vnprox already produces.",
      },
      {
        heading: "Four statuses, exactly one of which is a pass",
        body: "`pass` means every mapped evidence item was evaluated and satisfied. `fail` means at least one was evaluated and is not satisfied. `not_evaluated` means the control has a mapping but something in it could not be assessed — absence of evidence is not evidence of compliance. `unmapped` means the control has no mapped evidence at all and vnprox observes nothing that speaks to it, so it says so instead of passing it. An acknowledged finding still fails its control: acknowledgement is triage, not remediation.",
      },
      {
        heading: "Unmapped checks are reported, not ignored",
        body: "Every check this build can emit that no control in the profile maps is listed, along with where that list was computed from. Adding a check to vnprox without mapping it therefore degrades no control silently — the gap shows up as a gap. The same reasoning applies to the counts above the table: they are recounted from the controls on screen, so a status this build does not model cannot hide inside a summary that still adds up.",
      },
    ],
    seeAlso: ["governance-page", "findings-stream", "policies-panel", "doc-export"],
  },
  {
    id: "tenants-panel",
    title: "Tenant administration",
    surface: "panel",
    summary:
      "Creating tenants, declaring what each one may see, and managing its members and approvers — the administrative side of the delegated, server-side-scoped views.",
    docRef: "docs/api.md",
    keywords: ["tenant", "scope", "member", "approver", "multi-tenant", "delegation", "admin"],
    sections: [
      {
        heading: "Scope is a list of refs, typed rather than picked",
        body: "A tenant's scope is inventory Ref strings — a guest, a subnet, or a coarser VLAN or VNet that is expanded to its members live at read time. You type them; this screen deliberately offers no guest picker, because enumerating the cluster's inventory to fill a dropdown is a read this administration screen has no business making and would be the exact leak the server-side scoping exists to prevent.",
      },
      {
        heading: "The list and the detail answer different questions",
        body: "Listing tenants tells you which exist. It tells you nothing about their scopes or their members — the list route reports both as empty without consulting the store, so an empty list there is an absent answer wearing an empty one's clothes. Select a tenant to read either. This screen never concludes \"no members\" from a list it knows did not look.",
      },
      {
        heading: "There is no request or approve control here",
        body: "Tenancy has no self-service request or approval route. A member requests a change by staging a changeset against the tenant; an approver of that tenant converts it to an ordinary draft from the changeset itself, and then drives the usual review flow — approval is not apply. Nothing on this screen approves anything, and an approver may never approve their own request.",
      },
      {
        heading: "Out of scope is reported as not found",
        body: "Asking for something a session may not see answers \"no such thing\" rather than \"you may not\" — existence is not confirmed to someone who is not allowed to know. That means a not-found answer here does not distinguish \"it does not exist\" from \"it is not yours\", and that ambiguity is the point rather than a rough edge.",
      },
    ],
    seeAlso: ["governance-page", "tenants", "permissions", "read-only-mode"],
  },
  {
    id: "digest-schedule-panel",
    title: "Digest schedule",
    surface: "panel",
    summary:
      "How often the periodic summary goes out, which alert rules it draws from, and what the last run actually did — the half of scheduled digests that used to need a SQLite client.",
    docRef: "docs/api.md",
    keywords: ["digest", "schedule", "summary", "cadence", "weekly", "quiet", "alert rules", "delivery"],
    sections: [
      {
        heading: "No cadence is not a cadence",
        body: "A schedule nobody has ever written reports a cadence of zero, and this panel says exactly that rather than showing \"every 0 seconds\" or quietly displaying \"weekly\". The runner does substitute a weekly default when it finds a stored cadence of zero, but only for an enabled schedule and only at tick time — that substitution is not recorded anywhere, so if you want a cadence that is written down, write one.",
      },
      {
        heading: "Rule ids are a filter, not an address book",
        body: "A digest carries no delivery target of its own. It is handed to the same alert-rule delivery path everything else uses, so quiet hours defer it, coalescing applies, failures retry with the same backoff, and every attempt shows up in the delivery log. Naming rule ids narrows which rules' targets receive it; naming none means every rule, which is the no-filter convention used throughout — never \"no recipients\".",
      },
      {
        heading: "Enabled needs a workable cadence; disabled does not",
        body: "An enabled schedule must be at least an hour, because a digest is a summary of a period and a period shorter than that produces a report with nothing in it plus a delivery attempt per tick. A disabled schedule may carry any cadence including none, because disabling is how you silence a digest without losing the cadence you chose. The daemon enforces both; this form only saves you discovering it as an error.",
      },
      {
        heading: "The quiet form",
        body: "A digest covering a period with nothing to report renders as a single line, under a stated size — no sections, no tables, no \"none observed\" filler. Deltas are measured against the previous digest rather than an arbitrary window, so consecutive digests abut exactly, and a first-ever digest states that it has no baseline instead of rendering a delta against zero.",
      },
    ],
    seeAlso: ["governance-page", "alert-rules-page", "findings-stream", "doc-export"],
  },
];
