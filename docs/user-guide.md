# User guide

This guide is written for the shipped product; during development it doubles as the UX specification — implementation agents build what this document describes.

New to vnprox? [`first-hour.md`](first-hour.md) is a shorter path through §1–§4 below for your
first session; come back here for the complete reference.

## 1. First login

Browse to `https://<any-node>:8007`. Log in with your **Proxmox credentials** (same username, password, realm, and second factor as the PVE UI — vnprox has no separate accounts).

On first login vnprox walks you through a short review:

1. **What we found** — your cluster's network, drawn. Nothing was changed; vnprox only read.
2. **Protected interfaces** — vnprox detected which interfaces carry each node's management IP and corosync traffic. Confirm these; vnprox will refuse changes that would cut them off.
3. **Physical discovery** — if `lldpd` isn't running, vnprox offers to enable it so the map can show real switch names and ports.
4. **Health findings** — anything inconsistent it noticed (MTU mismatches, half-applied configs, drift between nodes).

If you'd rather look before you touch, an administrator can set `read_only = true` in the config — the full UI works, editing is disabled.

## 2. Reading the map

The **Topology** view is home, and it lands on **Switch view** — a faceplate rendering of your real gear, one appliance per Linux/OVS bridge, grouped per cluster node:

- **Uplink bay**: the bridge's physical NICs/bonds, with LACP/MII state and the LLDP neighbor on the far end of each wire. Red = link down.
- **VLAN strip**: the bridge's VLAN sub-interfaces.
- **Guest access-port grid**: one port per guest NIC attached to the bridge (VMID as the port number, guest name, VLAN tag), collapsible per bridge.
- **VNet strip**: any SDN VNets realized on that bridge.
- **Unattached ports**: NICs/bonds not wired into any bridge surface in their own panel.

A `Switch | Graph` toggle in the header switches to the classic **Graph view** — a pan/zoom node-link canvas with four layer bands (Physical/L2/SDN/Guests) stacked per cluster node. Reach for Graph view when you need its spatial features: drag-and-drop editing, the path-simulator map overlay, traffic paint mode, or hover-chain highlight. The toggle is a per-session preference, not saved layout.

Things to try first:

- **Hover** any VM — its whole path to the physical switch lights up (Graph view).
- **Press `/`** and type a VM name, MAC, or IP — jump straight to it.
- Enter a **VLAN ID** in the filter box — see exactly where that VLAN lives and where it's trunked. Works in both views.
- **Click** anything — the inspector shows every detail, live status, and the raw config behind it.

## 3. Making changes safely

vnprox never applies anything as you click. Edits collect in the **change drawer** (bottom right):

1. Make edits anywhere — drag a NIC into a bond, edit a bridge, retag a guest NIC. Each becomes a line in the drawer.
2. Hit **Review & apply**. You'll see a plain summary, the exact file diffs per node (interfaces, and SDN config for SDN ops), and the ordered plan.
3. **Discuss it.** Leave a comment on the whole changeset or on one specific operation — useful when a colleague is reviewing your work, not just applying it yourself. If your deployment requires sign-off before apply, an approver **Approve**s or **Reject**s it right there; a review link ("Copy review link") lets you send it to someone else to look at, including from their phone.
4. Apply. A **countdown banner** appears (default 2 minutes). vnprox has applied your change — now it wants proof you still have connectivity.
5. Click **Confirm** — done. If you *can't* click (the change cut you off), vnprox automatically rolls everything back at the deadline. Reconnect and read what happened.

Every applied change is snapshotted. **Time machine** (History → Snapshots) lets you diff any two points and restore any of them — restores go through the same review flow.

### Common tasks (each has a wizard or guided form)

| Task | Where |
|---|---|
| Create a LACP bond from two NICs | Map → select NICs → "Create bond", or Node → Bonds → New |
| Make a bridge VLAN-aware and trunk 10–30 | Click bridge → Edit → VLAN aware toggle |
| Move 12 VMs to another bridge | Guests view → filter by bridge → select all → Reattach |
| Create an isolated test network on all nodes | SDN → New zone → Simple → wizard |
| Stretch a network across nodes with VXLAN | SDN → New zone → VXLAN → wizard (it does the MTU math) |
| Reserve an IP for a VM | IPAM → subnet → address list → "Reserve first free →" on a free range (or click an existing address) → Reserve |
| Allow only web traffic to a VM | Firewall → guest → builder row (macro: HTTP/HTTPS) |
| "Why can't VM A reach VM B?" | Tools → Path simulator — it names the blocking rule or missing link |

## 4. The escape hatches

- **Raw editor**: Tools → Raw interfaces editor → pick a node. Full editor with linting; saving still goes through review + rollback protection. (Corrected, T-607: previously said "Node → Advanced," which is not where this lives in the shipped UI — it's under Tools with a node-select dropdown, not a per-node "Advanced" tab.)
- **CLI on the node**: `vnproxctl` can list/restore snapshots and trigger rollbacks even when the UI is unreachable (`docs/deployment.md` §Troubleshooting).
- **vnprox down?** Your network keeps working — Proxmox owns the config; vnprox is only a (very good) way of editing it.

## 5. Permissions

You see and can do exactly what your Proxmox permissions allow. Read-only PVE users get a read-only vnprox; users without SDN privileges see the SDN cockpit disabled with a tooltip naming the missing privilege. Everything anyone changes is in the **Audit** view.

## 6. Keyboard reference

`/` search · `1–4` toggle layers · `f` VLAN filter · `g` then `t/s/f/i` go to Topology/SDN/Firewall/IPAM · `⌘K`/`Ctrl+K` command palette · `?` full list · `F1` help for the current screen.

**Command palette (`⌘K`/`Ctrl+K`)**: one dialog, reachable from any page, merging the same fuzzy entity search `/` opens with every action the current page(s) have registered — "edit vmbr0", "new VLAN zone", "open drafts", "simulate path from <entity>", and more as pages add their own verbs. Arrow keys move through the merged list; Enter/click runs the highlighted entry. On the topology map itself, arrow keys also move focus between entities (roving focus, in on-screen left-to-right/top-to-bottom order) once an entity has focus; Enter activates the focused one exactly like a click.

## 6a. Online help

Every screen in vnprox explains itself, without leaving the browser.

- **`F1`, or the `Help` button** in the top bar, opens the help panel on whatever screen you're looking at — what it's for, how to read it, and what the safe way to change something here is.
- **The `?` next to a panel heading** opens help for that specific surface, not the whole page. The path simulator's four verdicts, what microsegmentation's "cannot-determine" bucket means, what unattended rollback actually covers — each is one click from where you're standing.
- **Search** inside the panel covers every topic's full text, not just titles. **See also** links move between related topics, and **Back** returns.
- **`?`** still opens the keyboard-shortcut list, unchanged. Two different questions, two different affordances.

Help topics cover the concepts the UI assumes you hold (the change engine, commit-confirm, protected interfaces, drift, permissions), every routed screen, the panels and wizards inside them, and the v2.0/v3.0 opt-ins — federation, AI operators, plugins, tenants, HA, switch push, embeds.

Coverage is enforced, not asserted: `web/src/help/coverage.test.ts` derives the screen inventory from `App.tsx` and `NavRail.tsx` themselves, so a route that ships without help fails the build. See `docs/development.md`.

## 7. Beyond one cluster (v2.0)

Everything above works the same whether you run one cluster or ten. The features in this section only appear when you use them — if you run a single cluster and never attach a second, vnprox looks and behaves exactly as it did before v2.0.

### 7.1 Federation — many clusters, one pane

An administrator attaches other PVE clusters under **Settings → Clusters** (name, API URL, and a read credential for that cluster). Each cluster's credential is sealed at rest with the same encryption vnprox uses for Proxmox tickets, and is never shown again.

Once a **second** cluster is attached, the map gains a **Global** view at its outermost zoom: one capsule per cluster showing its name, open-findings count, drift status, and a greyed-out "unreachable" indicator if a cluster is momentarily unreachable (its capsule degrades; the others are unaffected — you never lose the whole view because one cluster is down). Click a capsule to drill into that cluster's ordinary topology, unchanged from the single-cluster experience.

Search and the command palette (`/`, `⌘K`) span every attached cluster: results are grouped by cluster and namespaced, and the palette gains a **"switch to cluster X"** action to change context. The **Audit** view merges rows across clusters newest-first, each tagged with its cluster (with the same "partial results" indicator you already know from unreachable peers).

**What federation does *not* do:** it never changes another cluster's config for you. Each cluster stays the source of truth for its own network; a changeset always belongs to exactly one cluster and is rejected if an edit would reach across the boundary. Federation federates *views and workflows*, not ownership.

### 7.2 Cross-cluster IPAM & external subnets

**IPAM → Cross-cluster** surfaces any subnet (or overlapping CIDR) allocated in two attached clusters as a **conflict finding** naming both clusters — the "we used 10.20.0.0/24 in two places" problem is finally visible.

You can also record **external subnets** — office LANs, upstream transit, colo ranges that PVE itself doesn't manage — as first-class IPAM records (**IPAM → External subnets → Add**), so your address plan is complete, not just the PVE-managed part. External subnets are plain records; they are never staged as PVE SDN changes.

If you run NetBox or phpIPAM, the bridge is now **two-way**. **IPAM → External sync → Preview** shows a dry run of what would change on either side (additions, removals, conflicts) and writes nothing. Only **Apply** (with an explicit confirm) performs the sync, and every write is recorded in the audit log with before/after. A disagreement between vnprox and the external system on a specific address shows up as a finding, not a silent overwrite.

### 7.3 DNS management

If your SDN uses the DNS plugin (PowerDNS), **SDN → DNS** now shows your zones and records — both the authoritative config and, where reachable, what PowerDNS is actually serving (the same config-vs-live duality you see for DHCP reservations vs. leases). Guests whose IP matches a DNS record get a **name badge** on the map.

Editing a record or zone is an ordinary change: it collects in the change drawer and goes through the same review → apply → confirm flow as any SDN edit (deleting a zone that still has records makes you remove the records in the same change). Nothing here is a separate, un-audited mechanism.

### 7.4 Switch config push (guarded, opt-in)

This is the one read-write step onto your physical switches, and it is deliberately the most guarded feature in the product. It is **off by default** and must be enabled twice: once at the daemon level, and again for each specific switch you register (**Settings → Switches**). Until both are on, no switch write is possible.

When enabled, vnprox can push **only** to switch ports that LLDP confirms are facing your PVE nodes, and **only** VLAN membership, port descriptions, and LACP settings — never a full-config push. Every push is an ordinary changeset with a diff and a confirm step, and immediately before each write vnprox re-checks that the port's neighbor is still the PVE node it expects (if a cable moved, the push aborts).

Read the residual-risk note before you enable it: unlike a node-side change, a switch that a bad push makes unreachable **cannot be rolled back remotely** — there is no vnprox agent living on the switch. vnprox extends its management-path guardrails onto the uplink port carrying a node's management VLAN (a push that would strip it is hard-blocked, no override), and if it can't reach a switch to revert a change, it marks that changeset "rollback incomplete — needs manual intervention" rather than pretending it rolled back.

### 7.5 Backup network awareness (PBS)

Proxmox Backup Server hosts now appear on the map with their interfaces, and the **Backup path** paint mode lights up the node → PBS traffic path for nodes with a backup job targeting that storage. The inspector adds a plain-English datastore-network sizing hint (based on your backup schedule/volume and the resolved link speed). This is entirely read-only — vnprox stores no PBS credentials and makes no changes here.

### 7.6 Single sign-on (OIDC)

For larger, multi-cluster setups, an administrator can enable **OIDC login** alongside the normal Proxmox login (**Settings → Authentication**). You log in through your identity provider, and your group memberships map to a vnprox role.

Note the boundary: OIDC signs you in to vnprox, but your **Proxmox permissions still decide what you can do in each cluster**. An OIDC role never grants more than your real PVE ACLs allow, and if there's no PVE linkage for a cluster, you can read it (subject to that cluster's rules) but hold no write capability there from the OIDC role alone.

## 8. The open platform (v3.0)

v3.0 opens vnprox up as infrastructure other tools build on — an AI-operator surface, an extension SDK, delegated multi-tenant views, a redundant daemon pair, and a plugin/blueprint hub. Every one of these is opt-in and dormant until you configure it: an install that leaves them alone behaves exactly as it did in v2.x. The single rule that makes all of them safe is unchanged — **nothing here is a new way to change your network.** AI proposals, plugins, and tenant requests all produce ordinary changesets that a human still reviews and applies through the same change drawer, with the same diff, confirm, and auto-rollback you already use.

### 8.1 AI operators (MCP)

vnprox can expose its **read** surfaces (topology, findings, flows, IPAM, path simulation, diagnostics) and a **stage-only** draft-changeset surface to an AI assistant that speaks the Model Context Protocol — for example, an on-call assistant that triages an alert, runs the diagnosis ladder, and *drafts* a failover changeset for you to confirm from your phone. It is **off by default** (`[mcp] enabled` in the daemon config) and authenticated with a capability-scoped automation token you mint under **Settings → Tokens** (the same tokens automation already uses).

The boundary is the whole point: **an AI can read and can draft, but can never apply, confirm, or roll back** — there is no MCP tool, and no combination of tools, that touches the apply path. A human (or your scheduled-confirm machinery) is always the one who commits. Every AI-drafted changeset is stamped `origin: mcp` with the token that created it, and every AI action writes its own audit row with an `mcp:<token-name>` actor — so in the audit log you can always tell an AI-originated change from one a person made.

### 8.2 Plugins (extension SDK)

Third parties can extend five surfaces — switch drivers, flow/telemetry ingestors, finding packs, ingress discoverers, and dashboard tiles — through a stable, versioned SDK. An administrator sees installed plugins under **Settings → Plugins**, each showing the extension points it implements and the **capability scope** it declared. That scope is a *ceiling*: a plugin can only ever do what its declared capabilities already allow, and — like everything else — a plugin can *stage* a changeset for you to apply but is never itself a way to apply one. Out-of-process plugins run as supervised, sandboxed subprocesses with no access to vnprox's database or files; if one crashes, its tile or finding pack simply drops out rather than taking the daemon down. Install, enable, disable, and uninstall are all audited with the plugin's scope.

### 8.3 Multi-tenancy & self-service

For shared clusters, an administrator can define **tenants** (**Settings → Tenants**) scoped to a specific set of guests, VLANs, or subnets, with members and approvers. A tenant member sees only their own slice of the topology, findings, and IPAM — everything outside their scope is not just hidden but genuinely invisible (a lookup of something out of scope returns "not found," never confirming it exists).

A member doesn't edit directly; they **request** a change, which becomes a request-changeset routed to their tenant's approver (via your normal alert routing). An approver reviews and approves it — which turns it into an ordinary draft — and then applies it through the usual review → confirm flow. Two guardrails hold: a plain member can never approve, and an approver can never approve their **own** request. Approval isn't the same as applying — the ordinary confirm/rollback safety still gates the real change.

### 8.4 High availability (active/standby)

Because vnprox itself holds the safety timers that roll a bad change back, you can run it as an **active/standby pair** so the tool isn't a single point of failure. It's **off by default**; a single daemon needs none of this. An administrator adds an `[ha]` section to both daemons' config (see `docs/deployment.md`), picks a failover mechanism (a VIP-move script or a DNS-repoint webhook — you provide the mechanism, vnprox just triggers it on promotion), and sets one of the pair to bootstrap.

The important property: **in-flight commit-confirm timers and scheduled applies survive a failover.** If the active dies mid-change, the standby takes over and re-arms the *same* rollback deadline — so a change that would have auto-rolled-back at 12:03:30 still does, on the standby, at 12:03:30. A fenced lease guarantees only one daemon ever drives a change, even during a network partition. **Status → HA** shows each daemon's role, lease term, and replication lag. When you upgrade an HA pair, upgrade the **standby first**, let it catch up, then upgrade the active (it hands over cleanly as its lease lapses).

### 8.5 The hub

The **Hub** (browse under **Settings → Hub**, available once an administrator points `[hub] registry_url` at a registry) is an opt-in catalog of signed blueprint bundles and SDK plugins you can browse and install. Every install goes through the same trust gate a direct import does: the artifact's signature is verified against your trust store, and an unsigned or unfamiliar-signer artifact still requires you to explicitly say "trust this" — the hub never installs anything on implicit trust. A **"vetted"** badge means the registry recognizes the signer, but it's informational only: it never replaces your own trust decision. Installing a plugin shows you its declared capability scope before you confirm.

### 8.6 Embeddable views

You can put a read-only, live view of the map, a dashboard, or the posture report on a wiki page or NOC screen. Mint an **embed link** under **Settings → Tokens → Embed**: it is read-only by construction (you cannot mint a write-capable embed, even as an admin), never exceeds your own permissions, and authenticates only by its own token — a logged-in browser session is never silently used to authenticate an embed. There are also Grafana panels backed by vnprox's metrics exporter and event stream for teams that live in Grafana.

## 9. Guardrails and config as code (Arc 5)

The features below shipped in phases 25–27 and answer a different question than v3.0's platform features above: not "what can vnprox connect to" but "can the change engine say no, and can your cluster's intent live somewhere other than vnprox's own store." **Several of these are configured through `vnproxctl` or the API rather than a settings screen** — that is stated explicitly per feature below rather than left for you to discover by looking for a button that isn't there.

### 9.1 Policy-as-code guardrails

An administrator can install a **declarative policy rule set** — organisational rules like "no guest on VLAN 1" or "a bridge carrying guests keeps two uplinks" — that the change engine evaluates at the **validate** stage, before a violating changeset ever reaches diff. A rule is data (`{id, description, severity, match, assert}`), not a script: there is no expression language to learn. `severity: deny` blocks the changeset outright, naming the rule; `severity: warn` annotates it and rides along to the review screen without blocking anything. The default rule set is empty and changes nothing until you add one.

There is no policy editor screen yet: author rules as YAML, validate them with `vnproxctl policy lint`, try one against a real changeset without staging anything with `vnproxctl policy test --policy=f.yaml --changeset=<id>`, and install them via `PUT /policies` or a `[changesets] policy_file` on the daemon. A `deny` violation shows up in the ordinary changeset review screen as a blocking finding, the same place every other validation error appears.

### 9.2 Canary apply, auto-rollback, and the two-person rule

Three apply-time guardrails, each off by default and each additive to the ordinary stage → validate → diff → apply → confirm/rollback flow — none of them is a second way to change your network.

- **Canary / staged apply.** An apply can be split into a canary stage (a named subset of nodes) and a hold before the rest, instead of touching every affected node at once. The changeset pauses in a resumable state during the hold — neither applied nor rolled back — and either a person confirms it should continue (`gate: manual`) or it promotes automatically once the canary nodes show no new error-severity findings (`gate: auto`). There is no canary wizard in the changeset drawer today: this is configured by sending an `applyStrategy` object on `POST /changesets/{id}/apply` (see `docs/api.md`) — reachable from a direct API call, not yet from `vnproxctl` or a form in the UI.
- **Finding-triggered auto-rollback.** With `autoRollbackOnError` set (per apply, or as a cluster default), a **new** error-severity finding on something the changeset touched rolls it back immediately instead of waiting out the commit-confirm window — closing the gap between "the operator lost their connection" (which commit-confirm already handles) and "the change was wrong and the operator is still staring at the screen." Like canary apply, this is set on the apply request; there is no toggle in the drawer yet.
- **The two-person rule and break-glass.** An administrator can declare that certain classes of change (a glob like `fw.*`, the reserved `mgmtPath`, or a tagged policy rule) require sign-off from **two distinct people** before they can be applied — enforced server-side, so a direct API call is refused exactly like the UI would be. The review screen **does** show you when a changeset is blocked this way and by how much. For the 3am emergency with nobody else awake, `POST /changesets/{id}/break-glass` (a written reason required) overrides the approver count alone, is audited under its own action, and raises a finding that cannot be acknowledged for 24 hours — so the override gets reviewed by someone who wasn't in the room. There is no break-glass button in the UI; it is an API/CLI escape hatch by design.

### 9.3 Post-apply topology preview

Before you click apply, you can see what the map will look like afterwards: the changeset drawer's preview renders the live map with the changeset's ops folded in, added/removed/modified entities marked distinctly. It is best-effort and says so — an op whose effect can't be projected (a raw interfaces-file edit, most firewall/QoS ops) is listed by name rather than silently dropped from the picture.

### 9.4 Git-backed spec sync

If your infrastructure already lives in a git repository, point vnprox at it (`[gitsync]` in the daemon config — off by default) and it polls one file at one ref, compares it against the live cluster, and opens a **draft changeset** when they disagree. It never applies anything itself — the sync's only path into the change engine is a create/update call, nothing that could confirm or roll back. `vnproxctl gitsync status` and `GET /gitsync/status` show the last fetched commit, the last plan, and why a draft exists, if one does; there is no dedicated settings screen for this today.

### 9.5 Changeset → pull request

The other direction: a changeset you staged in vnprox can be proposed back to the spec repository as a pull request (`POST /changesets/{id}/propose`, GitHub or GitLab). vnprox commits the spec delta, pushes, and opens the request — and stops there. It never merges, approves, or polls one; whatever happens to the PR comes back through the ordinary git sync above. This is API/CLI-only today; there is no "propose" button in the changeset drawer yet.

### 9.6 Drift-to-git reconciliation

With a spec synced from git, a drift finding can name a third position alongside "what PVE reports" and "what's actually running": what the spec says should be there. Two explicit actions answer "which of these is right" — **adopt reality** (propose a spec commit describing the cluster as it is) and **restore intent** (stage a changeset bringing the cluster back to what the spec says) — and neither ever fires on its own, at any severity. Both are reached through the API today (`POST /drift/{id}/adopt-reality`, `POST /drift/{id}/restore-intent`); the Findings/Drift screens report the three-way divergence but don't yet render buttons for the two actions.

### 9.7 AI operators can now stage, not just read (MCP)

§8.1's MCP surface gained four **typed staging tools** — `changesets.stage.bridge`, `.iface`, `.fwrule`, `.ipam` — so an AI operator can propose a concrete change as a draft changeset instead of dictating one to you in a chat message. The boundary from §8.1 is unchanged and, if anything, stronger: no MCP tool can apply, confirm, approve, or delete, and that is now enforced at compile time in the daemon binary, not merely by convention. Every staged op is checked against your policy rules (§9.1) before a draft even exists — a denied op comes back to the model with the rule's id and description, which is feedback it can act on. Every AI-staged changeset now also names the specific tool that staged it, visible on the changeset and in the audit log, so a reviewer knows not just that an AI acted but which action it took.

### 9.8 Compliance profiles and evidence export

`GET /compliance/{profile}` maps your findings, policies, and posture factors onto named controls and reports each as `pass`, a named failing check, or — the load-bearing case — `unmapped`, which can never render as `pass` in any output. One general-purpose profile ships; it is a mapping format, not a certification claim against any published standard. `GET /export/compliance/{profile}?format=md|html|json` renders it as a timestamped, reproducible document. This is API-only today — there is no compliance screen in the UI.

### 9.9 Advisory locks and presence

When you have a draft open against an entity, other operators staging against the same entity see a warning naming who holds it and can proceed deliberately (the override is audited) — this is **advisory**, never a block on applying. The changeset drawer also shows who else is currently looking at the same changeset or entity, via **PresenceIndicator** — nothing renders when you're the only one there, so it stays quiet on every single-operator screen. Presence and locks are per-node today: two operators logged in to different nodes of the same cluster don't yet see each other.

## 10. Adoption (Arc 5)

Phase 28's cards are aimed at two audiences: people evaluating vnprox who have never run it, and teams already running it who need to operate at more than one person at a time.

### 10.1 Demo mode

`vnproxd --demo` runs the whole product against a synthetic, built-in three-node cluster — no Proxmox VE, no outbound network. It's the fastest way to see vnprox before pointing it at a real cluster: log in with the fixture's own demo credentials, and every screen renders populated data. A persistent banner and a distinct accent colour make it unmistakable, and every mutating request answers "this is what I would have done" without touching anything. A demo daemon refuses to be configured against a real PVE endpoint, in both directions — see `docs/features/demo-mode.md`.

### 10.2 Hosted read-only demo and guided tour

`--public-demo` (which requires `--demo`) adds a read-only edge in front of the daemon: every request that isn't `GET`/`HEAD`/`OPTIONS` is refused, including login — each visitor gets their own in-memory session and scratch state, invisible to every other visitor, with per-visitor rate and storage caps. A scripted tour walks a visitor through six of the datasheet's lead features. **There is no hosted instance of this today** — everything above is built and tested, but nobody has deployed it at a public URL; see `docs/features/demo-mode.md`'s "Known gaps" if you're considering standing one up yourself.

### 10.3 The hub, publisher side

§8.5's Hub client now has something real to talk to: `vnproxctl hub publish|index|revoke|verify|keygen` lets a publisher sign and submit a blueprint or plugin, a reviewer (who doesn't hold the publisher's key) index it, and either revoke it later — all against a static, signed `index.json`, not a running service. Revocation is checked from the already-fetched signed index, so it works even when the network doesn't. As with §8.5, none of this widens what the client already trusted: an unsigned or unfamiliar-signer artifact still needs your explicit say-so. See `docs/hub-registry.md` — which also states plainly that no hosted registry exists yet, matching the demo instance above.

### 10.4 Incident mode

**Incidents** (top-level nav) assembles one timeline from everything vnprox already records — findings appearing and clearing, changesets staged or applied, diagnosis-ladder runs, captures, recent flows, and the point-in-time topology diff ("what's different about this cluster compared to Tuesday," `docs/features/topology.md` §6) — over a window you choose. It is a *view*: opening one starts no new collection and copies no data, so you can open an incident retroactively over a window that already passed and see exactly what a live one would have shown. Add your own timestamped annotations as you investigate, and "close and export" produces one artifact — the timeline plus a secret-redacted support bundle — through the same redaction path `vnproxctl support-bundle` uses.

### 10.5 Map annotation layer

Pin a free-text note to any entity on the map, or draw a labelled region that corresponds to no PVE object at all — "this uplink is temporary until the switch swap," "vendor-managed, do not touch." Give a note an expiry and it stops being shown once that moment passes (it's still there if you ask for it explicitly); delete the entity it's pinned to and the note is kept and marked orphaned rather than silently dropped, because it may be the only surviving record of why. Notes appear in the config-doc export, which is where they're most useful to a reader who can't see the live map.

### 10.6 In-app assistant

A right-hand panel that runs the same read tools §9.7's MCP surface exposes — topology, findings, flows, IPAM, path simulation, diagnosis — against your own daemon, using your own session, so it can never see or do anything you couldn't reach yourself. Every answer cites the tool results it came from; an answer with no citation simply isn't shown. It has no model backend configured by default — you point it at one yourself — and prompts and answers never pass through vnproxd, so they never appear in a log or a support bundle. Its one write is staging a changeset (tagged `[assistant]`), which then goes through the ordinary review screen exactly like every other draft.

### 10.7 Scheduled digest reports

A weekly (or whatever cadence you set) push of posture score, capacity projections, unresolved drift, and findings opened/closed in the period — delivered through the same alert-rule targets §7 already uses, so it respects quiet hours and retries like any other alert. A digest covering a quiet period is one line, not a padded report nobody reads. Configure the schedule and its recipient rules with `GET`/`PUT /digest/schedule`; there is no settings screen for it yet.

### 10.8 Hardware validation and compatibility telemetry

If you have a real Proxmox cluster, `vnproxctl verify --suite=hardware` is the single most useful thing you can run: it works through the same hardware-validation checklist vnprox has always disclosed as incomplete, as a read-only command, and writes a signed report naming exactly what passed, what failed, and what it couldn't check and why. It refuses to run against a mock endpoint unless you pass `--allow-mock`, so a green run can't happen by accident.

`vnproxctl telemetry` lets you opt in to sharing which checks pass on which PVE version, kernel, and NIC — building the compatibility matrix from real clusters instead of one dev box. It is off by default, contacts no endpoint until you configure one, and `vnproxctl telemetry preview` prints the **exact bytes** that would be sent, from the same buffer `send` transmits — not a description of them.
