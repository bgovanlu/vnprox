// SPDX-License-Identifier: Apache-2.0

// Concept topics — the cross-cutting ideas the rest of the UI assumes you
// already hold. Sourced from docs/architecture.md §4, docs/security.md, and
// docs/features/change-management.md; each topic names its source in
// `docRef` so a reviewer can check the prose against the spec.
import type { HelpTopic } from "../types";

export const CONCEPT_TOPICS: readonly HelpTopic[] = [
  {
    id: "safety-model",
    title: "How vnprox keeps you from cutting yourself off",
    surface: "concept",
    summary:
      "Nothing you click changes your network. Edits collect as a draft, get validated and diffed, and only apply when you say so — and even then vnprox undoes them unless you prove you still have connectivity.",
    docRef: "docs/architecture.md",
    keywords: ["safety", "interlock", "guardrail", "rollback", "change engine"],
    sections: [
      {
        heading: "Every change follows the same five steps",
        body: "**Stage** an edit anywhere in the UI and it becomes a line in the change drawer. **Validate** runs on every draft change and again immediately before apply. **Diff** shows you the exact file changes and the ordered plan. **Apply** executes it. **Confirm** is you telling vnprox you're still connected — and if you don't, it rolls back. There is no path around this sequence: the same engine serves the UI, the API, the CLI, plugins, and AI operators.",
      },
      {
        heading: "Interlocks you cannot click through",
        body: "Validation hard-blocks changes that would take out a node's management IP interface, a corosync link, or a bridge with running guests attached. These are errors, not warnings — there is no override checkbox in the UI for them. Advisory findings (a bond without a sensible hash policy, a bridge with no description) are warnings, and those you can apply past with an explicit acknowledgement.",
      },
      {
        heading: "Proxmox stays the source of truth",
        body: "vnprox does not keep its own authoritative copy of your network config. It reads Proxmox, shows you what's there, and writes back through the same PVE API and files you'd edit by hand. If vnprox is down, your network is unaffected — you've lost a very good editor, not your configuration.",
      },
    ],
    seeAlso: ["change-drawer", "commit-confirm", "protected-interfaces", "validation-findings"],
  },
  {
    id: "change-drawer",
    title: "The change drawer",
    surface: "concept",
    summary:
      "The persistent drawer in the bottom-right corner collects every edit you make anywhere in the app into one reviewable changeset, and holds it there until you apply or discard it.",
    docRef: "docs/features/change-management.md",
    keywords: ["draft", "changeset", "staging", "pending", "ops"],
    sections: [
      {
        heading: "What lands here",
        body: "Map drag-and-drops, form edits, firewall rule changes, guest NIC retags — all of them. Each becomes an operation with a human-readable summary. You can reorder operations, remove individual ones, and watch live validation status update as you go. Editing never mutates anything until you apply.",
      },
      {
        heading: "Parking work for later",
        body: "You can keep multiple named drafts and switch between them. That makes it practical to build up a maintenance-window change over days without holding it in your head, or to park a half-finished idea while you deal with something urgent.",
      },
      {
        heading: "Discarding is free",
        body: "**Discard** throws the draft away and touches nothing on your cluster, because nothing was ever written. A draft carries no risk — the risk starts at apply, which is why apply has its own review screen.",
      },
    ],
    seeAlso: ["safety-model", "changeset-review-page", "validation-findings"],
  },
  {
    id: "commit-confirm",
    title: "Commit-confirm and automatic rollback",
    surface: "concept",
    summary:
      "After a change applies, a countdown banner appears. Click Confirm and the change sticks. Say nothing — because the change cut you off — and vnprox restores the previous configuration by itself.",
    docRef: "docs/features/change-management.md",
    keywords: ["countdown", "confirm", "rollback", "timeout", "auto-revert", "lockout"],
    sections: [
      {
        heading: "Why it works",
        body: "Confirmation requires an authenticated round-trip to the daemon. That is the whole trick: if your change broke the path between you and the cluster, you physically cannot confirm, and the deadline passes. Rollback then happens server-side, without you. The default window is **120 seconds**, configurable between 30 and 600.",
      },
      {
        heading: "Changes to a management path get a longer leash",
        body: "If a changeset touches a node's resolved management path, the review screen makes you type the node's name to acknowledge it, and the confirm window defaults to — and cannot be set below — **180 seconds**. That's additive ceremony on top of the normal flow, not a way to override the safety interlock behind it.",
      },
      {
        heading: "What unattended rollback covers, and what needs your session",
        body: "Interface-file, WireGuard, QoS and switch-port changes are undone by the daemon itself and always revert unattended. Firewall and SDN changes are written to Proxmox under **your** login, so undoing them without you present needs a copy of your session: vnprox seals your PVE ticket into the changeset for the confirm window, uses it only to undo that one changeset, and destroys it the moment you confirm or it rolls back. A PVE session lasts about two hours, so a long window opened late in a session can be only partly covered — the apply response tells you exactly how much of it is.",
      },
      {
        heading: "If a node drops mid-apply",
        body: "Remaining steps abort, already-completed nodes roll back, and the unreachable node's own daemon rolls back locally at its deadline. Each node arms its own timer when its step starts, so safety never depends on one node being able to reach another.",
      },
    ],
    seeAlso: ["safety-model", "snapshots-time-machine", "changeset-review-page", "cli-escape-hatch"],
  },
  {
    id: "protected-interfaces",
    title: "Protected interfaces",
    surface: "concept",
    summary:
      "vnprox works out which interfaces carry each node's management IP and corosync traffic, and refuses changes that would cut them. You confirm that detection once, during first-run setup.",
    docRef: "docs/security.md",
    keywords: ["management", "corosync", "mgmt", "interlock", "lockout", "protected"],
    sections: [
      {
        heading: "What gets protected",
        body: "The interface holding each node's management address, every interface carrying a corosync ring, and — transitively — the physical path underneath them: the bond a management bridge sits on, the NICs enslaved to that bond. A change anywhere along that path is caught, not just a change to the named interface.",
      },
      {
        heading: "There is no override button",
        body: "Unlike advisory warnings, a protected-interface violation is a hard error with no UI checkbox to click past. Re-addressing a management IP is deliberately out of scope. If you genuinely need to restructure a management path, the guided redundancy wizard builds a changeset that is interlock-clean by construction — it preserves the management address and its physical connectivity at every step.",
      },
      {
        heading: "Keeping detection current",
        body: "Hardware changes. If vnprox's picture of a node's management path stops matching reality, it prompts you to re-confirm rather than silently protecting the wrong interface — a stale protection is worse than none, because it protects something that no longer matters while leaving the real path exposed.",
      },
    ],
    seeAlso: ["safety-model", "management-page", "mgmt-redundancy-wizard"],
  },
  {
    id: "snapshots-time-machine",
    title: "Snapshots and the time machine",
    surface: "concept",
    summary:
      "Every applied change is snapshotted before it runs. The History view lets you diff any two points in your cluster's network history and restore any of them.",
    docRef: "docs/features/change-management.md",
    keywords: ["snapshot", "history", "restore", "time machine", "revert", "backup"],
    sections: [
      {
        heading: "Taken before, not after",
        body: "The snapshot is captured before the plan executes, which is what makes rollback possible at all — the rollback path restores those exact pre-change files on the affected nodes and re-runs the interface reload and SDN apply as needed.",
      },
      {
        heading: "Restoring is an ordinary change",
        body: "Choosing to restore a snapshot doesn't bypass anything. It builds a new changeset containing the operations that would get you back to that state, and that changeset goes through the same validate → diff → apply → confirm flow as any other. You review it and you confirm it.",
      },
      {
        heading: "Rolling back a change you already confirmed",
        body: "Manual rollback of a committed changeset is offered for **7 days** after it applied. Like a restore, it creates a new restoring changeset through the normal flow rather than reaching behind the engine.",
      },
    ],
    seeAlso: ["commit-confirm", "history-page", "snapshot-restore"],
  },
  {
    id: "validation-findings",
    title: "Validation: errors, warnings, and fixes",
    surface: "concept",
    summary:
      "Every draft is checked by five classes of validator, in order. Errors block apply outright; warnings need an explicit acknowledgement; many findings carry a machine-applicable fix you can accept with one click.",
    docRef: "docs/features/change-management.md",
    keywords: ["validate", "error", "warning", "finding", "fix", "blocked"],
    sections: [
      {
        heading: "The five classes",
        body: "**Schema** checks types and ranges — VLAN IDs 1–4094, MTU 576–9216, bond mode enums, CIDR syntax. **Referential** checks that targets exist and that you haven't created duplicate enslavements, name collisions, VID overlaps on a trunk, or address overlaps. **Safety** is the interlock layer. **Cross-node consistency** folds the change's projected effect across every node in the cluster. **Advisory** raises style and health warnings.",
      },
      {
        heading: "Why cross-node checks are errors, not warnings",
        body: "The same comparison the background drift checker runs against live state is run here against what your state *would become*. Divergence in a same-named bridge, an MTU asymmetry, or an SDN zone whose realizing bridge is missing on one member node are all blocking errors — a changeset that would leave the cluster inconsistent shouldn't apply without you seeing it first, even though the equivalent live-state finding only warns.",
      },
      {
        heading: "Validation runs twice",
        body: "Once as you edit, so you get feedback immediately, and again immediately before apply — because the cluster may have moved underneath your draft since you built it.",
      },
    ],
    seeAlso: ["change-drawer", "changeset-review-page", "findings-stream", "drift"],
  },
  {
    id: "drift",
    title: "Drift",
    surface: "concept",
    summary:
      "Drift is when your cluster's live network state stops matching what its configuration says, or when nodes that should agree with each other don't. vnprox checks continuously and raises findings.",
    docRef: "docs/features/topology.md",
    keywords: ["drift", "inconsistent", "divergence", "out of sync", "half-applied"],
    sections: [
      {
        heading: "Two kinds",
        body: "**Config-vs-live** drift: the file says one thing, the running kernel says another — usually a half-applied change, a manual `ip` command someone ran, or a reload that didn't take. **Node-vs-node** drift: a bridge named the same on three nodes has a different MTU, a different VLAN-aware setting, or a different VID set on one of them.",
      },
      {
        heading: "What to do about it",
        body: "Drift findings appear in the findings stream with the node and object named. Many carry a fix that stages the corrective operations into a changeset for you — so resolving drift goes through the same review and confirm flow as any other change, rather than being a silent background correction.",
      },
    ],
    seeAlso: ["findings-stream", "validation-findings", "topology-page"],
  },
  {
    id: "permissions",
    title: "What you're allowed to do",
    surface: "concept",
    summary:
      "vnprox has no accounts of its own. You log in with Proxmox credentials and you can see and change exactly what your Proxmox permissions allow — nothing more.",
    docRef: "docs/user-guide.md",
    keywords: ["permissions", "acl", "privileges", "capability", "role", "disabled", "greyed out"],
    sections: [
      {
        heading: "Capabilities come from PVE",
        body: "Your PVE ACLs are translated into capabilities: view and change network, view and change SDN, view and change firewall, change guest NICs, view the audit log, capture packets. A read-only PVE user gets a read-only vnprox, with the full UI still browsable.",
      },
      {
        heading: "When something is greyed out",
        body: "A disabled control carries a tooltip naming the specific privilege you're missing, rather than failing silently or at submit time. If the SDN cockpit is disabled for you, hover it and you'll see which PVE privilege would enable it.",
      },
      {
        heading: "Single sign-on doesn't change this",
        body: "If your administrator has enabled OIDC login, your identity provider signs you in — but your Proxmox permissions still decide what you can do. An OIDC role never grants more than your real PVE ACLs already allow.",
      },
    ],
    seeAlso: ["settings-page", "read-only-mode", "audit-page", "tenants"],
  },
  {
    id: "read-only-mode",
    title: "Read-only mode",
    surface: "concept",
    summary:
      "An administrator can run vnprox with editing disabled entirely. The full UI works and every view is browsable; nothing can be staged or applied.",
    docRef: "docs/deployment.md",
    keywords: ["read only", "readonly", "look but don't touch", "safe mode", "evaluation"],
    sections: [
      {
        heading: "Why you'd use it",
        body: "It's the natural way to evaluate vnprox on a production cluster: you get the map, the findings, the flow explorer and the path simulator with zero write capability anywhere in the daemon, so there is nothing to get wrong while you're deciding whether to trust it.",
      },
      {
        heading: "How to tell",
        body: "Editing affordances are disabled throughout rather than hidden, so the shape of the product stays legible. This is a daemon-level setting (`read_only = true` in the config), not a per-user one — a full administrator sees the same read-only UI as everyone else.",
      },
    ],
    seeAlso: ["permissions", "settings-page"],
  },
  {
    id: "cluster-awareness",
    title: "Working across a cluster",
    surface: "concept",
    summary:
      "Every view and every change in vnprox is cluster-aware. You talk to whichever node you happened to browse to, and it acts on behalf of the whole cluster.",
    docRef: "docs/architecture.md",
    keywords: ["cluster", "node", "peer", "quorum", "multi-node"],
    sections: [
      {
        heading: "One pane, many nodes",
        body: "The map, the findings stream, the audit log and the change engine all span the cluster. Applying a change that touches three nodes produces one changeset with one plan, ordered across those nodes — not three changes you have to coordinate.",
      },
      {
        heading: "When a node is unreachable",
        body: "Views degrade rather than fail: you get the data from the nodes that answered, plus an explicit partial-results indicator naming what's missing. You never lose the whole picture because one node is down.",
      },
      {
        heading: "Beyond one cluster",
        body: "If your administrator has attached other PVE clusters, the same principle extends outward — but ownership does not. A changeset always belongs to exactly one cluster and is rejected if an edit would reach across the boundary.",
      },
    ],
    seeAlso: ["federation", "topology-page", "settings-federation-page"],
  },
  {
    id: "findings-stream",
    title: "Findings",
    surface: "concept",
    summary:
      "A finding is something vnprox noticed that you probably want to know about: a health problem, a drift, an LLDP inconsistency, a security posture issue. They're gathered into one stream, source-tagged.",
    docRef: "docs/features/monitoring.md",
    keywords: ["finding", "health", "alert", "issue", "problem", "warning"],
    sections: [
      {
        heading: "What raises one",
        body: "Health checks (MTU mismatches, a bond whose LACP partner doesn't agree, a link that keeps flapping), the drift checker, LLDP discovery inconsistencies, and posture checks. Each finding names the object and node it concerns and carries a severity.",
      },
      {
        heading: "Findings that fix themselves — with your permission",
        body: "Many findings carry a machine-applicable fix. Accepting one stages the corrective operations into a changeset; it does not apply anything. You still review the diff and confirm, exactly as if you'd built the change by hand.",
      },
      {
        heading: "Debouncing",
        body: "Health checks use hysteresis, so a link that bounces once doesn't generate a finding and a finding doesn't disappear the instant a metric brushes back over the line. What you see is a condition that persisted, not a sample.",
      },
    ],
    seeAlso: ["tools-page", "drift", "dashboard-page", "alert-rules-page"],
  },
  {
    id: "keyboard-and-palette",
    title: "Keyboard shortcuts and the command palette",
    surface: "reference",
    summary:
      "vnprox is built to be driven from the keyboard: single keys for the map, a two-key chord for navigation, and a command palette that merges entity search with every action the current page has registered.",
    docRef: "docs/user-guide.md",
    keywords: ["keyboard", "shortcut", "hotkey", "palette", "cmd-k", "ctrl-k", "search"],
    sections: [
      {
        heading: "The bindings",
        body: "`/` opens search · `1`–`4` toggle the physical, L2, SDN and guest layers · `f` filters by VLAN · `g` then `t`, `s`, `f` or `i` jumps to Topology, SDN, Firewall or IPAM · `⌘K` / `Ctrl+K` opens the command palette · `?` shows the full, live list · `F1` opens help for whatever screen you're on.",
      },
      {
        heading: "The command palette",
        body: "One dialog, reachable from any page, merging the same fuzzy entity search `/` opens with every action the currently-mounted pages have registered — 'edit vmbr0', 'new VLAN zone', 'open drafts', 'simulate path from…'. Arrow keys move through the merged list, Enter runs the highlighted entry.",
      },
      {
        heading: "Moving around the map itself",
        body: "Once an entity on the topology canvas has focus, arrow keys move focus between entities in on-screen reading order, and Enter activates the focused one exactly as a click would. The `?` dialog always reflects what's actually wired up right now, including the actions the current page contributes.",
      },
    ],
    seeAlso: ["topology-page", "spotlight-search"],
  },
  {
    id: "cli-escape-hatch",
    title: "When the UI isn't available",
    surface: "reference",
    summary:
      "vnproxctl runs on the node itself and can list and restore snapshots, inspect changesets, and trigger rollbacks — including when the web UI is unreachable, which is exactly when you'll want it.",
    docRef: "docs/deployment.md",
    keywords: ["vnproxctl", "cli", "console", "ssh", "emergency", "recovery", "stuck"],
    sections: [
      {
        heading: "What it can do",
        body: "`vnproxctl status` reports the daemon's health, `vnproxctl snapshots` lists what's restorable, `vnproxctl rollback` triggers a rollback, and `vnproxctl backup` and `vnproxctl support-bundle` produce artifacts for archiving or for sending to support. Run `vnproxctl --help` on the node for the full list.",
      },
      {
        heading: "You usually don't need it",
        body: "If a change cut you off, the automatic rollback at the confirm deadline has already handled it — reconnect and read what happened. Reach for the CLI when the daemon is up but the UI can't reach it, or when you want to script something.",
      },
      {
        heading: "And if vnprox itself is down",
        body: "Your network keeps working. Proxmox owns the configuration; vnprox is a way of editing it, not a component of the data path.",
      },
    ],
    seeAlso: ["commit-confirm", "snapshots-time-machine", "safety-model"],
  },
  {
    id: "demo-mode",
    title: "Demo mode",
    surface: "concept",
    summary:
      "vnproxd --demo runs the whole product against a synthetic cluster built into the binary — no Proxmox, no network access — and every write reports what it would have done instead of doing it.",
    docRef: "docs/features/demo-mode.md",
    keywords: ["demo", "demo mode", "evaluate", "trial", "sandbox", "synthetic", "would have"],
    sections: [
      {
        heading: "How to tell you're in one",
        body: "A persistent amber banner across the top of every screen, which doesn't dismiss and doesn't auto-hide — it's meant to still be there when someone else walks up to the screen. Log in with the demo fixture's own built-in credentials (`root` / `vnprox-mock`, realm `pam`); there's no separate demo account system to set up.",
      },
      {
        heading: "Nothing you do here is real",
        body: "Every mutating request — staging, applying, confirming — is accepted and reports what it would have done, and touches nothing: no PVE endpoint is dialled (there is none to dial), and the synthetic cluster's own state doesn't change either. You can go through an entire apply-and-confirm flow safely, to see exactly what it feels like before you point vnprox at a real cluster.",
      },
      {
        heading: "It shows a real fixture, not your machine",
        body: "The topology, findings, drift and flows you see are a realistic multi-node cluster built into vnprox for exactly this purpose — never the interfaces of the computer the demo happens to be running on. A demo instance also can't be pointed at a real Proxmox endpoint, and a real deployment can't be switched into demo mode; each direction is refused outright.",
      },
    ],
    seeAlso: ["dashboard-page", "safety-model", "onboarding-walkthrough"],
  },
];
