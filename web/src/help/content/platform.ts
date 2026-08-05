// Platform topics — the v2.0/v3.0 opt-ins. Every one of these is dormant
// until an administrator configures it, so the help has to answer "what
// would this do if I turned it on" as much as "how do I use it". The
// safety boundaries stated here are load-bearing and must match
// docs/user-guide.md §7–8 and docs/security.md exactly.
import type { HelpTopic } from "../types";

export const PLATFORM_TOPICS: readonly HelpTopic[] = [
  {
    id: "federation",
    title: "Federation — many clusters, one pane",
    surface: "concept",
    summary:
      "Attach other PVE clusters and vnprox spans all of them: one map, one search, one audit log. Ownership doesn't move — each cluster stays the source of truth for its own network.",
    docRef: "docs/user-guide.md",
    keywords: ["federation", "multi cluster", "global", "aggregate", "attach", "peer cluster"],
    sections: [
      {
        heading: "It appears when you use it",
        body: "If you run a single cluster and never attach a second, vnprox looks and behaves exactly as it did before federation existed. The Global map view only appears once a **second** cluster is attached.",
      },
      {
        heading: "One unreachable cluster doesn't cost you the view",
        body: "Each cluster's capsule degrades independently, with an explicit unreachable indicator, and every federated read reports partial results naming which cluster didn't answer. You never lose the whole picture because one site is down.",
      },
      {
        heading: "The boundary",
        body: "Federation never changes another cluster's configuration for you. A changeset always belongs to exactly one cluster and is rejected if an edit would reach across the boundary. It federates views and workflows, not ownership.",
      },
    ],
    seeAlso: ["settings-federation-page", "cluster-awareness", "ipam-cross-cluster", "wireguard-connect-clusters"],
  },
  {
    id: "mcp-ai-operators",
    title: "AI operators (MCP)",
    surface: "concept",
    summary:
      "vnprox can expose its read surfaces and a stage-only draft surface to an AI assistant that speaks the Model Context Protocol. An AI can read and can draft — it can never apply, confirm, or roll back.",
    docRef: "docs/user-guide.md",
    keywords: ["mcp", "ai", "llm", "assistant", "automation", "agent", "model context protocol"],
    sections: [
      {
        heading: "What it can reach",
        body: "Topology, findings, flows, IPAM, path simulation and diagnostics on the read side, plus the ability to *stage* a draft changeset. It's off by default and authenticated with a capability-scoped automation token you mint yourself.",
      },
      {
        heading: "The boundary is the whole point",
        body: "There is no MCP tool, and no combination of tools, that touches the apply path. A human — or your scheduled-confirm machinery — is always the one who commits. This isn't a policy the UI enforces; it's the absence of a capability.",
      },
      {
        heading: "You can always tell",
        body: "Every AI-drafted changeset is stamped with its origin and the token that created it, and every AI action writes its own audit row with an `mcp:` actor prefix. 'Did a person do this?' is answerable from the log, not by inference.",
      },
      {
        heading: "What it's actually good for",
        body: "The realistic case is triage: an on-call assistant reads an alert, runs the diagnosis ladder, and drafts a failover changeset that you review and confirm from your phone. The drafting is the labour; the judgement stays yours.",
      },
    ],
    seeAlso: ["tokens-and-embeds", "audit-page", "safety-model", "changeset-review-page"],
  },
  {
    id: "plugins",
    title: "Plugins",
    surface: "concept",
    summary:
      "Third parties can extend five surfaces — switch drivers, flow ingestors, finding packs, ingress discoverers and dashboard tiles — through a versioned SDK, inside a declared capability ceiling.",
    docRef: "docs/user-guide.md",
    keywords: ["plugin", "extension", "sdk", "third party", "install", "sandbox", "capability"],
    sections: [
      {
        heading: "The capability scope is a ceiling",
        body: "Each installed plugin declares what it needs, and you see that scope before you confirm the install. It can never do more than it declared — and like everything else, it can stage a changeset but is never itself a way to apply one.",
      },
      {
        heading: "Isolation",
        body: "Out-of-process plugins run as supervised, sandboxed subprocesses with no access to vnprox's database or files. If one crashes, its tile or its finding pack simply drops out; it doesn't take the daemon down with it.",
      },
      {
        heading: "Everything is audited",
        body: "Install, enable, disable and uninstall each write an audit row including the plugin's scope — so a capability that appeared in your deployment is traceable to when and by whom.",
      },
    ],
    seeAlso: ["hub-page", "settings-page", "audit-page", "safety-model"],
  },
  {
    id: "tenants",
    title: "Tenants and self-service",
    surface: "concept",
    summary:
      "On a shared cluster, a tenant sees only its own slice of the topology, findings and IPAM — and requests changes rather than making them, routed to an approver.",
    docRef: "docs/user-guide.md",
    keywords: ["tenant", "multi tenancy", "self service", "approver", "request", "scope", "delegation"],
    sections: [
      {
        heading: "Out of scope means invisible",
        body: "A tenant member's view is scoped to a specific set of guests, VLANs or subnets. Anything outside that scope isn't merely hidden: looking one up returns 'not found', never a permission error that would confirm it exists.",
      },
      {
        heading: "Request, approve, apply — three separate things",
        body: "A member requests a change, which becomes a request-changeset routed to their tenant's approver. An approver reviewing and approving it turns it into an ordinary draft; applying it is still a separate step through the usual review and confirm flow. Approval is not application.",
      },
      {
        heading: "Two guardrails",
        body: "A plain member can never approve, and an approver can never approve their own request. Both are enforced server-side rather than by the UI declining to render a button.",
      },
    ],
    seeAlso: ["permissions", "changeset-review-page", "settings-page"],
  },
  {
    id: "ha-pair",
    title: "High availability (active/standby)",
    surface: "concept",
    summary:
      "vnprox itself holds the timers that roll a bad change back, so it can be run as an active/standby pair. In-flight confirm deadlines survive a failover intact.",
    docRef: "docs/user-guide.md",
    keywords: ["ha", "high availability", "standby", "failover", "vip", "lease", "redundant"],
    sections: [
      {
        heading: "The property that matters",
        body: "If the active daemon dies mid-change, the standby takes over and re-arms the **same** rollback deadline — a change that would have auto-rolled-back at 12:03:30 still does, on the standby, at 12:03:30. Scheduled applies survive too.",
      },
      {
        heading: "Only one daemon ever drives a change",
        body: "A fenced lease guarantees it, even during a network partition. **Status → HA** shows each daemon's role, its lease term, and replication lag.",
      },
      {
        heading: "Upgrading a pair",
        body: "Upgrade the **standby first**, let it catch up, then upgrade the active — it hands over cleanly as its lease lapses. It's off by default; a single daemon needs none of this.",
      },
    ],
    seeAlso: ["commit-confirm", "settings-page", "cli-escape-hatch"],
  },
  {
    id: "switch-push",
    title: "Switch config push",
    surface: "concept",
    summary:
      "The one read-write step onto your physical switches, and deliberately the most guarded feature in the product. Off by default, and it must be enabled twice before any write is possible.",
    docRef: "docs/user-guide.md",
    keywords: ["switch", "push", "write", "physical", "vlan", "lacp", "port description", "risk"],
    sections: [
      {
        heading: "Enabled twice, then narrowly scoped",
        body: "Once at the daemon level, and again for each specific switch you register. Even then vnprox can push **only** to ports that LLDP confirms are facing your PVE nodes, and **only** VLAN membership, port descriptions and LACP settings — never a full-config push.",
      },
      {
        heading: "Read the residual risk before you enable it",
        body: "Unlike a node-side change, a switch that a bad push makes unreachable **cannot be rolled back remotely** — there is no vnprox agent living on the switch. If vnprox can't reach a switch to revert, it marks the changeset 'rollback incomplete — needs manual intervention' rather than pretending it rolled back.",
      },
      {
        heading: "The guardrails that do apply",
        body: "Management-path protection extends onto the uplink port carrying a node's management VLAN: a push that would strip it is hard-blocked with no override. Immediately before each write, vnprox re-checks that the port's neighbour is still the PVE node it expects — if a cable moved, the push aborts.",
      },
    ],
    seeAlso: ["ports-page", "protected-interfaces", "commit-confirm", "settings-page"],
  },
  {
    id: "tokens-and-embeds",
    title: "Automation tokens and embed links",
    surface: "concept",
    summary:
      "Capability-scoped tokens for automation, and read-only embed links for wikis and NOC screens. Neither can ever exceed the permissions of whoever created it.",
    docRef: "docs/user-guide.md",
    keywords: ["token", "api token", "embed", "automation", "scrape", "bearer", "share", "link"],
    sections: [
      {
        heading: "Automation tokens",
        body: "Scoped to specific capabilities and shown once at creation. They're what the MCP surface and any external automation authenticate with. A token never grants more than the person who minted it holds.",
      },
      {
        heading: "Embed links",
        body: "Read-only by construction — you cannot mint a write-capable embed even as an administrator. An embed authenticates **only** by its own token; a viewer's existing vnprox session is never silently used to authenticate one. Treat the URL as the credential it is.",
      },
      {
        heading: "The metrics scrape token is separate",
        body: "The Prometheus endpoint has its own bearer scrape token, with an optional source-CIDR allowlist, because a scraper can't carry a PVE-derived session at all. It's node-local: a cluster-wide view is your Prometheus's job, not the daemon's.",
      },
    ],
    seeAlso: ["settings-page", "embed-map-page", "mcp-ai-operators", "permissions"],
  },
  {
    id: "oidc-sso",
    title: "Single sign-on (OIDC)",
    surface: "concept",
    summary:
      "Log in through your identity provider alongside the normal Proxmox login, with group memberships mapping to a vnprox role — but your PVE permissions still decide what you can do.",
    docRef: "docs/user-guide.md",
    keywords: ["oidc", "sso", "single sign on", "identity", "idp", "saml", "groups", "login"],
    sections: [
      {
        heading: "What it changes",
        body: "How you authenticate, and nothing else. Your identity provider signs you in and your group memberships map to a vnprox role — useful when you're managing several clusters and don't want a separate PVE credential dance for each.",
      },
      {
        heading: "The boundary",
        body: "An OIDC role never grants more than your real PVE ACLs already allow. If there's no PVE linkage for a particular cluster, you can read it subject to that cluster's own rules, but you hold no write capability there from the OIDC role alone.",
      },
    ],
    seeAlso: ["permissions", "login-page", "settings-page"],
  },
  {
    id: "pbs-awareness",
    title: "Backup network awareness (PBS)",
    surface: "concept",
    summary:
      "Proxmox Backup Server hosts appear on the map with their interfaces, and a backup-path paint mode lights up the node-to-PBS traffic path. Entirely read-only.",
    docRef: "docs/user-guide.md",
    keywords: ["pbs", "backup", "proxmox backup server", "datastore", "path", "sizing"],
    sections: [
      {
        heading: "What it shows",
        body: "PBS hosts as first-class map entities, the backup path for nodes with a job targeting that storage, and a plain-English datastore-network sizing hint in the inspector based on your backup schedule and volume against the resolved link speed.",
      },
      {
        heading: "Read-only, with no credentials stored",
        body: "vnprox stores no PBS credentials and makes no changes here. It's awareness — the answer to 'is our backup traffic crossing the link we think it is', which is exactly the question the service-class flow breakdown also answers from the other direction.",
      },
    ],
    seeAlso: ["topology-paint-modes", "service-class-traffic", "flows-page"],
  },
];
