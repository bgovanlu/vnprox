# Changelog

All notable user-facing changes to vnprox are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
vnprox uses semantic versioning; the SQLite schema migrates forward-only.

Versions up to v1.0 correspond to the milestones in `docs/roadmap.md`;
v2.0 is the milestone cut of the second arc in `docs/roadmap-next.md`
("Beyond the cluster"); v3.0 is the platform cut of the third arc in
`docs/roadmap-universal.md` ("The open platform"). Phase 3 ("Discovery &
true cluster") does not have its own version cut per the roadmap — its
functionality (peer clustering, LLDP discovery, drift detection, FDB
browsing) shipped as part of the v0.8 development cycle and is listed under
v0.8 below, alongside that release's SDN/IPAM work.

Precise dates are given for the v1.0.0, v2.0.0, and v3.0.0 release cuts;
earlier milestones predate this file and are dated only by year.

**Note on v1.1.0–v1.3.6:** these tags (2026-07-12 to 2026-07-15) were interim
development checkpoints cut while the v1.4 → v2.0 arc was in progress; they
don't have dedicated entries below because the `v2.0.0` tag wasn't applied
until the whole arc — including phases 13–15 of the *next* arc, see the note
on `[2.0.0]` below — had already merged onto the same branch. Their
functionality is folded into `[2.0.0]`.

## [Unreleased]

### Added

- **The map can now carry what you know, not just what is true.** "This uplink is temporary until
  the switch swap." "Vendor-managed, do not touch." That knowledge lived in a wiki that was wrong
  within a month, because it was not next to the thing it described. T-2806 puts it on the map:
  free-text notes pinned to an entity, and labelled regions drawn on the canvas. Both carry an
  author and a timestamp, both are shared with the whole team, and both appear in the config-doc
  export — which is where a note is most useful to a reader who cannot see the map at all.

  Three things about it are deliberate:

  - **A note can announce its own staleness.** Give a note an expiry and it stops being displayed
    when that moment passes. Expiry is computed on every read, not by a background job, so a daemon
    that was switched off for the week the expiry fell in cannot come back and show you a stale
    note. An expired note is *hidden*, never deleted — you can still list, read and unpin it.

  - **Deleting an entity does not delete what you wrote about it.** The note on a bridge you removed
    is kept and shown as orphaned, because it is frequently the only surviving record of *why* the
    bridge was removed. Nothing sweeps it away: not retention, not the expiry path, not collection.

  - **Regions survive your colleagues.** They live in their own shared table rather than inside the
    per-user layout blob that gets rewritten every time anyone drags a node, so a region persists
    across layout changes and view switches instead of being overwritten by whoever moved something
    last.

  None of this is a copy of Proxmox's config: a note's entity reference names a PVE object, but the
  row carries only the sentence a human typed, and a region corresponds to no PVE object at all.

- **The Hub has a registry to talk to — and it is a signed file, not a service.** vnprox shipped a
  registry *client* with nothing on the other end. T-2803 supplies the other end: the index format,
  the tooling that produces it (`vnproxctl hub publish|index|revoke|verify|keygen`), and the
  verification that makes it binding. The registry is a static directory — `index.json` next to an
  artifact tree — on object storage or GitHub Pages, exactly the posture the apt repository already
  uses. There is nothing new to run, and no new port.

  What the signature over the catalog buys you, given artifacts were already signed:

  - **A corrupted catalog gives you nothing, not part of something.** An index that is unsigned,
    truncated, edited, or signed by a key you did not pin fails as a whole; the Hub reports the
    registry as unavailable rather than showing you a catalog someone else edited. Set the
    registry's fingerprint in `[hub] index_signers` to turn this on.
  - **Revocation works when the network does not.** A withdrawn blueprint or plugin — one version,
    every version, or everything a compromised publisher key ever signed — is refused both in the
    catalog and at download, decided from the index you already fetched. No live revocation call
    exists to be blocked, spoofed, or simply unreachable at the moment it matters.
  - **It cannot make anything trusted.** The index signature is authority over the *catalog* only.
    An artifact from a signer you have not trusted still stops at the same explicit trust step it
    always did, and the capability scope a plugin declares is still shown to you before you confirm
    and still enforced after.

  Publishing is deliberately two-handed: a publisher signs their own artifact and opens a pull
  request; a reviewer — who does not hold the publisher's key — indexes it. Publishing the same
  artifact twice yields one entry and does not even rewrite the published file; publishing
  *different* bytes under a version that already exists is refused rather than swapped. The review
  checklist, the key handling, and the index-key rotation procedure are written down in
  `docs/hub-registry.md` before the first submission rather than after.

- **A change you made in the GUI can now be proposed to the repository it belongs in.** If your
  intent lives in git (`[gitsync]`), a change staged in vnprox was, until now, a change made outside
  your system of record: the cluster moved, the repository did not, and the next sync reported your
  own edit back to you as divergence. `POST /changesets/{id}/propose` closes the loop — it renders
  the changeset as a spec delta, commits it on a branch, pushes, and opens a pull request, on
  GitHub or GitLab. The changeset records the URL.

  What makes it trustworthy is what it refuses to do:

  - **vnprox opens the pull request and stops.** It never merges, gates, approves, or polls one —
    not as a route and not as a method anything here could call. Whatever happens to the request
    comes back through the ordinary sync, which opens a draft changeset you apply yourself.
  - **The branch means what the changeset meant, and that is checked before anything is written.**
    The proposed document must re-import to exactly your changeset's ops; one that would not is
    refused with the difference named. A changeset the spec cannot express — every delete, and
    every firewall/IPAM/QoS/WireGuard/raw-file op — is refused rather than approximated.
  - **A failed proposal leaves no orphan branch.** Either the branch and the request both exist, or
    neither does; a branch someone is already reviewing is never removed.
  - **Pushing needs its own credential.** `[gitsync] push_token_file` is separate from the
    read-only sync token, so syncing still never pushes, and a deployment that has not opted in
    never reads a write credential off disk. It appears in none of what gets written: not the
    branch name, the commit message, the pull-request body, the audit trail, or any error.

  The pull-request body carries the review context with the review: the spec diff, the per-op
  summary, and the blast radius — which nodes, carriers and guests the change would disturb, and
  whether it touches a management path.

- **An AI operator can now stage a change it cannot apply.** The MCP surface could already diagnose
  a problem in full — and then had to hand you a paragraph of instructions to type. Four new tools
  (`changesets.stage.bridge`, `changesets.stage.iface`, `changesets.stage.fwrule`,
  `changesets.stage.ipam`) close that gap from the safe side: each turns a request into exactly one
  op in a **draft** changeset and returns its id. You still review it and you still apply it.

  The boundary that makes this safe is not a rule anyone has to remember:

  - **No tool applies, confirms, approves, or deletes** — and one cannot be written. The only
    change-engine capability the MCP server holds is an interface with no such method, and a
    compile-time assertion fails the *build*, naming the offending method, if anyone widens it.
  - **Every op is checked against your policy rules before a draft exists.** A denied op is refused
    with the rule's id and description — feedback a model can act on — and leaves nothing behind. A
    policy that cannot be evaluated refuses the stage rather than skipping the check.
  - **Every staged changeset says who and what made it**: `origin: "mcp"`, the automation token, and
    now the tool's own name (`originTool`), all set once at creation and visible on every changeset
    response, so a reviewer sees which AI action produced the draft rather than only that one did.
  - **Budgeted**: staging is rate-limited per session and the number of open MCP drafts is capped;
    exceeding either is refused with a message naming the limit.

- **`vnproxctl verify` — the hardware-validation checklist, executed.** vnprox has always had a
  gap it stated openly: almost every behaviour it claims has only ever been tested against a mock
  Proxmox, because validating one on real hardware meant a person reading a checklist line, doing
  the thing, and writing down what happened. That does not scale, does not repeat, and — the part
  that actually mattered — could not be handed to a user who has a cluster and would like to help.

  `vnproxctl verify --suite=hardware` is that checklist as a command. Twenty-six checks, one per
  claim, each deciding its own verdict against your cluster and carrying the API response, command
  output or file contents that verdict rests on. It is read-only and safe on a production cluster.
  **If you have a Proxmox cluster, this is the single most useful thing you can send us.**

  Three properties keep the result honest, and each is enforced structurally rather than by
  convention:

  - **A skipped check is never a passing one.** A check that cannot run here says *why*, and names
    the hardware it would take. A run in which everything skipped exits non-zero and reports
    `0 passed` — because "we did not look" read as "we looked and it was fine" is how a validation
    figure becomes fiction.
  - **A verdict with no evidence is a malformed report**, which the command refuses to print. You
    can read the working and disagree with the verdict; that is the whole difference between this
    and a ticked box.
  - **It refuses to run against a mock Proxmox** unless `--allow-mock` is passed, naming the flag
    and saying what it costs. A green run against a mock is indistinguishable from a green run
    against real hardware, so the refusal is the default. A run made with the flag is stamped as
    such *in the report*, so the caveat travels with the document.

  Three suites: `hardware` (one real node, changes nothing), `multinode` (two or more nodes, or two
  clusters for federation; skips loudly with the count it saw otherwise), and `destructive`
  (`--i-understand` required — it interrupts applies, lets commit-confirm windows expire, and stops
  the active daemon). The destructive interlock is wiring, not policy: without the flag no write
  client is constructed at all.

  `--out` writes a signed, timestamped JSON artifact naming the vnprox version, PVE version, kernel
  and NIC models the run observed. Editing it — including merely re-indenting it — invalidates the
  signature. `vnproxctl verify --list` prints every check with the hardware it needs, so you can see
  what the suite will ask of your cluster before running any of it.

- **Policy-as-code guardrails: the change engine can now say no.** Until now the engine's
  guarantees were strong and *advisory* — it told you what would happen, but it refused only one
  thing, hard-coded: cutting a node's management path. Every other rule an organisation has ("no
  guest on VLAN 1", "a bridge carrying guests keeps two uplinks", "nobody touches `vmbr9` on the
  storage nodes") lived in a wiki and was enforced by whoever happened to review the change.

  You can now install a **declarative policy rule set** on the cluster. A rule is
  `{id, description, severity, match, assert}`, and `severity: deny` blocks the changeset at the
  validate stage — before any diff or plan is computed — with the rule's id and description in the
  error. `severity: warn` annotates the changeset instead and travels with it to the review
  screen without blocking anything.

  Policies are **data, not a scripting language**: `match` and `assert` are lists of
  `{field, op, value}` conditions over the ops and inventory the change engine already works in.
  There is no embedded expression interpreter and no new dependency — a deliberate limit, not an
  omission. Rules can assert over the changeset's *net effect* on the cluster (`target.guestCount`,
  `target.uplinkCount`, `target.vlanAware`), which is what lets "a bridge carrying guests keeps two
  uplinks" fire on a port removal that never mentions a guest.

  A rule that can never match anything is refused at load, not silently ignored, and a rule that
  has matched nothing for two weeks of real changesets is reported as probably-misconfigured. A
  policy file the daemon cannot parse is fatal at startup: vnproxd will not come up quietly
  enforcing a policy it could not read.

  `vnproxctl policy test --policy=f.yaml --changeset=<id>` evaluates a candidate rule set against a
  real changeset without staging anything, so a rule can be developed safely against the change it
  was written for; `vnproxctl policy lint` validates a document with no daemon at all, and
  `vnproxctl policy examples` prints a worked example set to start from. Rule sets are cluster-
  scoped, versioned in the store, and every change is audited (`policy.update`) with the full
  rule-set diff — both sides of every changed rule — so the audit entry alone reconstructs what
  changed. New routes: `GET`/`PUT /policies` and `POST /policies/test`.

  **The default policy set is empty, and an empty set changes nothing.**

- **Certificate management for the cluster.** Every cross-node thing vnprox does — applying a
  changeset, arming a distributed rollback timer, reading a peer's state — rides peer-API TLS,
  which is pinned to your cluster's own CA and fails closed. Until now there was no way to see
  those certificates, no warning before one expired, and one confirmed latent failure. There is
  now a **Settings → Certificates** screen, a `GET /certs` API, a `vnproxctl certs` command, and a
  `cert` family of findings covering expiry, name coverage, chain to the cluster CA, key strength,
  and missing or unreadable certificates. Each finding names the exact PVE command that fixes it.

  The inventory is cluster-wide from a **local** read: `/etc/pve` is pmxcfs, so every node's
  certificate is already on every node's disk. That is deliberate rather than incidental — a
  certificate problem is precisely what makes peers unreachable, so an inventory that needed the
  peer API to diagnose a peer-API failure would be useless at the only moment it mattered.
  `vnproxctl certs` reads the same data with the daemon down.

  Private keys live in the same pmxcfs directory as the certificates. The scanner only ever
  constructs paths from a fixed filename allowlist rather than listing a directory and filtering,
  and the certificate type carries no raw-bytes field at all — so "this cannot leak key material"
  is a property of the types, not of care taken at each call site.

- **Online help, on every screen.** vnprox previously had one in-app help surface: a dialog
  listing keyboard shortcuts. Everything else a user might need to know lived in `docs/` — on
  disk, in a git repo, on a machine they are probably not looking at while a commit-confirm
  countdown is running. There is now a help panel that answers "what is this, and what do I do
  here?" from wherever you are. Press **F1** or the **Help** button in the top bar for the screen
  you're on; click the **?** next to a panel heading for that specific surface. The panel carries
  full-text search across every topic, **See also** links between related ones, and Back to
  retrace. `?` still opens the keyboard-shortcut list, unchanged — two different questions, two
  different affordances.

  Coverage spans the concepts the UI assumes you hold (the change engine, commit-confirm,
  protected interfaces, drift, findings, permissions, read-only mode, cluster awareness), every
  routed screen, the panels and wizards inside them, and the v2.0/v3.0 opt-ins — federation, AI
  operators, plugins, tenants, HA, switch push, embeds, OIDC, PBS. Topics state the safety
  boundaries where they apply: that an AI operator can draft but never apply, that a switch push
  cannot be rolled back remotely, that unattended rollback covers interface changes always and
  firewall/SDN changes only for as long as your sealed PVE ticket lasts.

  Help content is bundled into the SPA rather than fetched, so it adds no API surface. Every topic
  cites the repo doc it was written from.

- **The coverage claim is enforced, not asserted.** `web/src/help/coverage.test.ts` derives the
  screen inventory by parsing `App.tsx` and `NavRail.tsx` for the routes they actually declare,
  then requires each to resolve to a substantial topic. It also rejects stale mappings for deleted
  routes, `<HelpAnchor>` values that resolve to nothing, unresolvable `seeAlso` ids, orphaned
  topics unreachable from any screen, `docRef`s naming a file that doesn't exist, and content below
  a quality floor. Adding a route without help is a failing test naming the path. Each source parse
  asserts a floor on what it found and that a known sentinel is among the results, so a regex that
  stops matching after a refactor fails loudly instead of certifying full coverage of an empty set.

### Fixed

- **Pinned peer TLS no longer fails against a certificate that doesn't list the node's IP**
  (`T-1906-bug-01`, found on real hardware). Peers are dialled by IP, and Proxmox does not
  necessarily regenerate a node's `pve-ssl.pem` when its management address changes — the node
  this was found on carried a **stale** IP SAN (`192.168.100.99`) for a machine now at
  `192.168.1.9`. Pinned verification against the dial address therefore failed closed on a
  correctly configured cluster: correct components, no operator error, **every peer down at once
  on upgrade**.

  vnprox now verifies a peer against its PVE node name where the certificate covers it, while
  still dialling the IP and still pinning the cluster CA — what `curl --resolve` does. No network
  round trip is needed, because pmxcfs already holds every node's certificate locally. Three
  properties are held by adversarial tests: the CA pin is untouched (a certificate from another CA
  is still rejected), candidate names come from PVE's authoritative node name rather than from the
  presented certificate, and an FQDN candidate must be rooted at the node name — so node A's
  certificate cannot authenticate node B even though the same CA issued both. Where nothing is
  covered, the handshake still fails closed, but `cert_san_mismatch` is now raised at **startup**,
  before the first peer call, instead of surfacing as an opaque handshake error later.

- **Closing a dialog no longer drops keyboard focus to nowhere.** Radix restores focus to a
  `DialogTrigger`; the help panel is opened programmatically and has none, so closing it would have
  left focus on `<body>`. The panel now returns focus to whatever opened it — the top-bar button,
  or the specific `?` anchor you clicked.

## [3.0.4] - 2026-07-30

Closes T-1407's two deferred follow-ups (see `planning/reports/T-1407-followups.md`).
No schema change (stays 32).

### Added

- **Name the cluster on the other side of a tunnel.** The "connect two clusters" wizard's
  *Other side* step gains an optional **Federated cluster** field. Picking one of your
  attached clusters tags the tunnel's peer as that cluster, inside the very same changeset
  that creates the tunnel — no extra step, and nothing is linked unless you actually apply
  the changeset. That tag is what makes the cluster count as reachable over the tunnel, so
  a tunnel outage shows up as one finding naming the cluster instead of every cross-cluster
  view reporting it separately as unreachable. The field is disabled with an explanation
  when no clusters are attached.

### Changed

- **A cluster's tunnel linkage now has one answer, not two.** The peer-level tag above and
  the cluster-level `wgTunnelId` recorded the same fact from opposite ends and could
  disagree. Every read now resolves a single effective linkage: an explicitly-set
  `wgTunnelId` always wins, otherwise it is derived from a tagged peer. `GET`/`PUT
  /federation/clusters` report the new read-only `wgTunnelSource` (`"explicit"` or
  `"peer"`) saying which applied. One behaviour change worth noting: clearing `wgTunnelId`
  no longer necessarily unlinks a cluster — if a tagged peer still exists the cluster stays
  linked, now sourced `"peer"`. Removing a peer-derived link means retiring or retagging
  that peer through an ordinary WireGuard changeset.

### Fixed

- `docs/roadmap-universal.md` still described T-1407 as unimplemented and open, three
  releases after it shipped.

## [3.0.3] - 2026-07-22

Completes phase 14 (`docs/roadmap-universal.md`): T-1407, the one card from that phase
that shipped no code in v2.0.0/v3.0.0/v3.0.1/v3.0.2 despite its six siblings landing
around it (found by a docs/plans-vs-implementation audit; see
`planning/reports/T-1407.md`). Schema 32 (forward-only migration from 31; a v2.0.0+
install upgrades in place).

### Added

- **Tunnel-aware federation transport.** An attached federation cluster can now declare
  itself reachable only via a specific WireGuard tunnel this daemon manages
  (`PUT /federation/clusters/{id}`'s new optional `wgTunnelId`). When that tunnel's live
  handshake goes stale, the cluster is excluded from the global topology, audit, and
  cross-cluster IPAM-conflict reads — but, unlike an ordinary unreachable cluster, it no
  longer raises three redundant per-surface `partial`/`failedClusters` flags for the same
  root cause. In their place, one named finding — `tunnel_down_peer_unreachable`
  (`source: "federation"`) — points at the down tunnel. No auto-remediation: the only
  action is a link to the tunnel's own changeset editor; a human fixes it through an
  ordinary `wg.*` change.

## [3.0.2] - 2026-07-22

Packaging patch — no code or schema change (schema stays 31; a v3.0.0/v3.0.1
install upgrades in place). Completes the `ProtectSystem=strict` fix begun in
v3.0.1, which covered only the first-boot key writes.

### Fixed

- **WireGuard apply fails `read-only file system` on a hardened host.**
  Applying a WireGuard changeset op writes each tunnel's wg-quick config under
  `/etc/wireguard` (`cmd/vnproxd/wireguard.go`'s `hostWGGateway` — `MkdirAll`
  `0700` + `WriteFile` `0600`), but the hardened unit's `ProtectSystem=strict`
  left `/etc/wireguard` read-only, so every WireGuard apply on a hardened node
  failed — the same crash class as the v3.0.1 keys bug, missed because that
  fix only added `/etc/vnprox/keys`. `ReadWritePaths` now also includes
  `/etc/wireguard`, and `postinst` creates that directory (`0700 root:root`,
  wireguard-tools' own convention) so the sandbox bind target always exists
  even on nodes without `wireguard-tools` installed. The rest of `/etc` stays
  read-only. The cluster secret's fallback generate-if-absent write to
  `/etc/pve/priv/vnprox/` is deliberately not added: `/etc/pve` is a pmxcfs
  FUSE mount that `vnprox-setup` pre-seeds, and whether `ProtectSystem=strict`
  even makes it read-only is unconfirmed — tracked in
  `planning/reports/needs-hardware-validation.md`.

## [3.0.1] - 2026-07-21

Packaging patch — no code or schema change (schema stays 31; a v3.0.0
install upgrades in place).

### Fixed

- **Service crash-loop on first upgrade to the v1.4→v3.0 key-generating
  releases.** The hardened systemd unit's `ProtectSystem=strict` made
  `/etc/vnprox` read-only, but the daemon generates first-run secrets under
  `/etc/vnprox/keys/` on startup — the Prometheus metrics scrape token
  (`metrics.key`) and the blueprint signing key (`blueprint-signing.key`) —
  which `postinst` does not pre-seed (only `session.key` is). On a node
  upgrading from a release that predates those keys, the first boot failed
  with `open /etc/vnprox/keys/…: read-only file system` and the service
  restart-looped. `ReadWritePaths` now includes `/etc/vnprox/keys` so the
  daemon can create its own first-run secrets (still `0600 root:root`; the
  rest of `/etc` stays read-only). Found on real hardware upgrading
  1.3.6 → 3.0.0; no sandboxed or fresh-`vnprox-setup` test caught it because
  setup had already created the key files.

## [3.0.0] - 2026-07-21

The platform release — the cut where vnprox stops being only a product and
becomes infrastructure other tools build on. v3.0 caps the v2.0 → v3.0 arc:
deep-sight diagnostics, edge/WireGuard, and Kubernetes/Ceph/QoS visibility
(phases 13–15) shipped earlier, under the `[2.0.0]` cut below — see that
entry's note. This release adds phase 16 (flow baselining, microsegmentation
planning, failure-impact simulation, rogue-service detection, capacity
forecasting, and a posture score) plus phase 17: an AI-operator MCP surface,
a plugin SDK, multi-tenancy, daemon HA, a blueprint/plugin hub, and
embeddable views. Target platforms: Proxmox VE 8.2+ and 9.x; **PVE 10.x and
11.x** are the forward compatibility targets for this arc (see
`docs/deployment.md`, flagged needs-hardware-validation until validated on
real hardware).

**Every new surface stages through the one change engine.** AI-proposed
changesets (MCP), plugin-staged changesets, and tenant request-changesets
are all ordinary changesets — staged, validated, diffed, applied, and
confirmed/rolled-back exactly like every mutation since v0.5. No card in
this arc introduced a second mutation path, and this release **freezes** the
new programmable surfaces as stable, versioned compatibility contracts. DB
migrations remain forward-only; a v2.x install upgrades in place.

### Added

- **Flow baselining, microsegmentation, and failure simulation.** Automatic
  per-guest traffic baselines with anomaly findings when observed flow
  deviates from the learned norm; a microsegmentation planner that proposes
  least-privilege firewall rules from observed flow history with a
  dry-run/review step before any rule is staged; a failure-impact simulator
  that answers "what breaks if this bridge/bond/link goes down" before it
  happens; rogue-service detection (unexpected listeners, unauthorized DHCP/
  DNS servers on the network); capacity forecasting for VLANs, subnets, and
  link utilization trending toward exhaustion; and a cluster-wide posture
  score rolling up firewall coverage, drift, and findings into one number
  with a point-in-time report.
- **MCP server for AI operators (read + stage only).** A first-class Model
  Context Protocol server exposes vnprox's read surfaces (topology, findings,
  flows, IPAM, path simulation, diagnostics) and lets an AI operator *stage*
  a draft changeset — and nothing else. No apply/confirm/rollback verb is
  reachable through MCP by any tool or combination; a human remains the sole
  apply authority. Every AI-originated changeset is unerasably labelled
  `origin: "mcp"` and every tool call is audited with an `mcp:<token-name>`
  actor, so an operator can always tell an AI action from a human one. Off by
  default (`[mcp] enabled`), authenticated with a capability-scoped
  automation token.
- **Plugin SDK.** Stable, versioned extension points third parties can
  implement — switch drivers, flow/telemetry ingestors, finding packs,
  ingress discoverers, and dashboard tiles — with an in-process and a
  supervised out-of-process option, each plugin declaring a capability scope
  that is a server-enforced *ceiling*, never a grant. A plugin can stage a
  changeset for a human to apply but is never itself a mutation path. Install/
  enable/disable/uninstall are all audited with the recorded scope.
- **Multi-tenancy & self-service.** Delegated, server-side-scoped views: a
  tenant sees only its own guests/VLANs/subnets and *requests* changes
  through request-changesets that route to an approver, with scoped
  dashboards and alert routes. Scoping is enforced at the data-access layer
  (an out-of-scope lookup is a `404`, never confirming existence); a member
  can never approve, and an approver can never approve their own request.
- **vnproxd high availability (active/standby).** An optional active/standby
  daemon pair with state replication and VIP-or-DNS failover, so the network
  tool is not itself a single point of failure. Commit-confirm timers and
  scheduled applies survive failover — re-armed to their original absolute
  deadlines — governed by a fenced single-writer lease with explicit
  split-brain handling. Off by default; a single-daemon install is unchanged.
- **Blueprint & plugin hub.** An opt-in client for a public registry of
  signed blueprint bundles and SDK plugins — browse and install with
  Ed25519 signature verification, a per-installation trust decision, and an
  informational "vetted" tier that never substitutes for that decision.
- **Embeddable views & Grafana panels.** Read-only, token-scoped embeds of
  the map, dashboards, and posture report for wikis/NOC screens, plus Grafana
  panels backed by the Prometheus exporter and the event stream. An embed
  token is hard-restricted to read-only scopes at mint and can never exceed
  its minting user.

### Changed

- **Platform API freeze.** The MCP tool surface, the plugin SDK interfaces,
  and the WebSocket `"events"` stream schema are now stable, documented
  compatibility contracts with the same deprecation policy the changeset API
  adopted at v1.7 (additive-only within a version; a breaking change mints a
  new version, keeping the old accepted for ≥1 minor release). See
  `docs/architecture.md` §13.
- Compatibility target advanced to Proxmox VE 10.x and 11.x for this arc
  (8.2+/9.x still supported); real-hardware validation is tracked as a
  needs-hardware-validation item.

### Security

- Every new credential/write-adjacent surface across the v2.0 → v3.0 arc is
  covered in `docs/security.md`'s threat-model summary: packet-capture files
  (Phase 13), WireGuard tunnel keys (Phase 14), the MCP AI-operator
  write-adjacent surface, plugin capability grants and the out-of-process
  plugin boundary, tenant credentials/isolation, and the HA replication
  channel. Every new at-rest credential class is sealed with the single
  AES-256-GCM session key vnprox already uses, never a second cipher or key,
  and each has a targeted encrypted-at-rest test.

## [2.0.0] - 2026-07-20

The multi-cluster release — the cut where a vnprox instance is no longer
1:1 with a Proxmox cluster. v2.0 caps the v1.4 → v2.0 arc (federation,
cross-cluster IPAM, DNS management, guarded switch push, PBS awareness, and
OIDC SSO). Target platforms: Proxmox VE 8.2+ and 9.x; PVE 10.x is the
forward compatibility target (see `docs/deployment.md`, flagged
needs-hardware-validation until validated on real hardware).

**Note:** this tag was applied after phases 13–15 of the *next* arc
(`docs/roadmap-universal.md`) had already merged onto the same branch, so
this release also includes deep-sight diagnostics (Phase 13), edge/WireGuard
networking (Phase 14, excluding T-1407 — see below), and Kubernetes/Ceph/QoS
workload visibility (Phase 15) — listed under "Added" below alongside the
v1.4 → v2.0 federation work they shipped together with. Phase 14's T-1407
("tunnel-aware federation transport") was **not** implemented and is not
included; it remains open, tracked as P1 in `docs/roadmap-universal.md`.

**Federation is additive, not a fork:** a v1.x single-cluster install that
upgrades with zero clusters attached keeps serving its existing
single-cluster experience unchanged — the global cluster view only appears
once a second cluster is attached, and DNS/switch-push/OIDC stay dormant
until explicitly configured. DB migrations remain forward-only.

### Added

- **Multi-cluster federation.** Attach any number of PVE clusters to one
  designated primary and see them all on one screen: a global topology with
  per-cluster capsules and drill-down into each cluster's ordinary view, a
  global search and command palette spanning clusters, per-cluster
  changesets with a merged cluster-wide audit trail, and per-cluster failure
  isolation — an unreachable cluster is greyed out and flagged as a partial
  result, never blanking or erroring the whole view. Config ownership stays
  strictly per-cluster; there is no cross-cluster mutation.
- **Cross-cluster IPAM and external subnets.** The same or an overlapping
  subnet allocated in two attached clusters now surfaces as a conflict
  finding. Non-PVE subnets (office LANs, upstream transit, colo ranges) are
  first-class IPAM records you can add and manage directly. The
  NetBox/phpIPAM bridge is upgraded from read-merge to **bidirectional
  sync**, with a dry-run preview that never writes and an explicit-confirm
  apply step — every sync write is audited with before/after per record.
- **DNS management.** Surface and edit PVE SDN's DNS plugin (PowerDNS):
  zone and record visibility, guest names shown as badges on the map, and
  record edits staged as ordinary changesets through the same SDN
  apply flow as zones/VNets/subnets.
- **Guarded switch config push.** The read-write step beyond LLDP discovery:
  driver-based (OpenConfig/gNMI) pushes scoped strictly to switch ports
  facing your PVE nodes (VLAN membership, port descriptions, LACP), each one
  an ordinary changeset with validate/diff/confirm and the management-path
  interlocks extended onto the uplink port. Per-switch, explicit opt-in;
  ships dark (feature-flagged off) until you enable it for a specific switch,
  with a plainly-stated residual risk that a switch made unreachable by a
  push cannot be remotely reverted.
- **PBS network awareness.** Proxmox Backup Server hosts appear on the map
  with their interfaces, the backup traffic path (node → PBS) is
  highlighted, and the inspector shows datastore-network sizing hints.
  Entirely read-only — no new write actions, no PBS credentials stored.
- **OIDC SSO.** Log in via OpenID Connect (authorization-code + PKCE)
  alongside the existing Proxmox ticket bridge, for federated deployments
  where per-cluster PVE credentials stop scaling, with group→role mapping.
  OIDC authenticates you to vnprox; your Proxmox permissions still gate every
  cluster-scoped action per cluster, and an OIDC role can never grant a
  capability your real PVE ACLs don't already allow.
- **Distributed packet capture, with a permission model and in-browser
  decode.** Capture on any node/interface, build filters with a guided BPF
  builder, decode packets in the browser, and download a `.pcap` — gated by
  a dedicated capability so capture access is granted independently of
  general admin rights.
- **Latency & loss mesh, guest network interior inspector, and conntrack/NAT
  table explorer.** A continuously-probed latency/loss heatmap across the
  cluster; a live view into a guest's own interfaces/routes/DNS via the
  guest agent; and a searchable connection-tracking/NAT table explorer per
  node.
- **Path MTU prober and guided diagnosis flows.** Active end-to-end MTU
  discovery feeding the existing `vxlan_underlay_mtu` finding, and
  guided, honesty-contract-preserving diagnosis flows that walk a user from
  symptom ("can't reach X") through the capture/latency/conntrack/interior
  tools above to a root cause.
- **WireGuard tunnel engine.** First-class WireGuard tunnels — key
  generation and custody, changeset-integrated apply (`wg.*` ops through the
  ordinary stage/validate/diff/apply/confirm flow), map edges for
  tunnel-connected clusters, and a "connect two clusters" wizard.
- **Edge & NAT cockpit, IPv6 enablement, WAN/upstream health, and ingress
  visibility.** A dedicated view for edge routing/NAT configuration; a
  guided IPv6 enablement suite; upstream/WAN link health monitoring; and
  visibility into ingress paths reaching the cluster from outside.
- **Kubernetes overlay mapping (read-only, CNI-aware) and Ceph network
  awareness.** A read-only Kubernetes overlay layer on the topology map with
  service-flow attribution, and Ceph's own network topology (public/cluster
  networks, OSD/mon placement) surfaced without new credentials — both
  follow the "read the owning system's own knowledge, zero new write
  surface" pattern PBS awareness established in this same release.
- **Service-network attribution, QoS & traffic shaping, SR-IOV lifecycle,
  and a migration network planner.** Flow records classified against known
  services (including Ceph/PBS/K8s traffic); `qos.*` changeset ops for
  traffic shaping; real SR-IOV VF lifecycle management; and a planner that
  recommends the least-disruptive network path/timing for a live migration
  using the latency mesh's data.

### Changed

- Compatibility target advanced to Proxmox VE 9.x and 10.x for this arc
  (8.2+ still supported); PVE 10.x validation on real hardware is tracked as
  a needs-hardware-validation item.

### Security

- Every new v2.0 credential class — the per-cluster registry credential
  (`clusters.credential_enc`), the OIDC client secret and mapped PVE
  credentials (`oidc_pve_links.credential_enc`), and switch-driver
  credentials (`switches.credentials_enc`) — is sealed at rest with the same
  single AES-256-GCM session key vnprox already uses for Proxmox tickets, and
  is never returned by any API response, log line, or audit entry. Each has a
  targeted encrypted-at-rest test.
- The threat-model summary gains rows for the arc's new surfaces
  (cluster-registry credential theft, a rogue or compromised attached
  cluster, switch-driver credential theft/errant push, OIDC token forgery)
  with stated mitigations.

## [1.0.0] - 2026-07-12

The 1.0 release: operations, hardening, and release polish on top of
everything below. Target platforms: Proxmox VE 8.2+ and 9.x.

### Added

- Live traffic visualization on the topology map: edge thickness and color
  reflect real-time link utilization, with a dedicated "Traffic" view mode
  and per-bond slave balance shown in the inspector.
- 24-hour traffic history with rate and error charts in the entity
  inspector (sparklines plus a full history chart).
- A unified health and findings stream that brings together drift
  detection, LLDP/switch mismatches, IPAM conflicts, and new health checks
  (interface error/drop rate thresholds, bond slave down, bridge without a
  carrier uplink, MTU mismatches, STP topology instability, stale
  unreloaded network changes, dnsmasq/FRR service health) — each finding
  has a plain-English explanation, affected entities, and, where possible,
  a one-click fixing change.
- Optional notifications (email/webhook, via your existing Proxmox
  notification targets) when a finding's severity crosses a threshold.
- Blueprints: reusable, parameterized network templates you can save,
  import, and export as files. Five ready-made starters are included
  (single-NIC homelab, dual-NIC management+trunk, LACP bond with a storage
  VLAN, VXLAN overlay, and EVPN datacenter). Re-applying a blueprint is
  idempotent — already-matching parts of your network are left alone, only
  the differences are staged. You can also "capture" an existing node's
  configuration as a new blueprint to replicate it elsewhere.
- A guided first-login walkthrough: see a summary of what vnprox found on
  your cluster, confirm (or correct) which interfaces are protected as
  management/cluster links, optionally install LLDP for switch discovery,
  and review your initial health findings — dismissible and resumable, and
  it never blocks normal use.
- Config documentation export: generate a point-in-time Markdown or
  standalone HTML report of your entire network — per-node interface
  tables, VLAN matrix, SDN inventory, firewall summary, LLDP wiring, and an
  embedded topology diagram — for change records or audits.
- Optional, server-enforced read-only mode: when enabled, no write actions
  are possible through vnprox for anyone, regardless of their Proxmox
  privileges.
- Production-grade packaging: a signed apt repository, a one-command
  cluster installer that rolls the package out to every cluster node over
  SSH (or prints manual per-node steps if SSH isn't available), automatic
  provisioning of vnprox's own read-only Proxmox API token and role, and a
  tested upgrade path that preserves your data and config across versions.
  `apt remove` keeps your config and data; `apt purge` removes them
  (prompting before touching cluster-shared state on the last node).
- `vnproxctl status` now reports collector health, Proxmox API
  reachability, and peer reachability/version compatibility all in one
  place, for quick troubleshooting.
- A multi-node change now refuses to start at all if a peer is running an
  incompatible version, rather than risking a partially-applied change.

### Changed

- Tightened the browser Content-Security-Policy to the minimum the app
  actually needs (no inline scripts, no third-party connections, no
  frames/objects/workers beyond what the app uses).
- The daemon's systemd service now runs with additional Linux hardening
  (restricted address families, a syscall allow-list) to reduce its attack
  surface further.
- Session identifiers are now truncated in log output rather than logged
  in full.

### Fixed

- The SQLite database file could be created world-readable under some
  system umask settings; it's now always created (and corrected on
  upgrade) with strict, owner-only permissions.
- A guest-selection bug in the firewall UI that could send the wrong
  reference and fail to load a guest's rules.
- An EVPN zone created via the guided wizard or the plain editor could
  silently omit its BGP controller reference, leaving the zone
  non-functional.
- A cluster-installer parsing bug could mistake the coordinating node's own
  status line for another node's name during multi-node rollout.
- SDN zone/VNet/subnet creation, editing, and deletion (both the guided
  wizards and the plain editors) could be permanently disabled for every
  user, including full-privilege admins, on any real multi-node cluster —
  a capability-resolution bug that made the entire SDN cockpit's write
  path non-functional outside of single-node test setups. Found and fixed
  during v1.0 release verification.
- Several SDN and firewall edit/delete controls (zone/VNet/subnet editors,
  per-rule delete/enable/reorder, shared object deletion) were missing
  read-only-mode capability gating entirely, so a read-only or
  under-privileged session could see them enabled (the underlying write
  would still be rejected server-side — no privilege escalation was
  possible, but the controls should have been disabled with an
  explanatory tooltip).
- `vnproxctl snapshots list/restore` and `rollback-now` — the documented
  "works even when the daemon/UI is unreachable" disaster-recovery path —
  incorrectly required a resolvable Proxmox TLS certificate to run at
  all, which these commands never actually need. Fixed so they work on
  any host, with or without a working Proxmox VE certificate.

### Security

- Completed a full security-hardening review: every claim in the security
  documentation is now backed by an automated test or a documented
  verification procedure.
- Zero known high/critical vulnerabilities in Go and npm dependencies,
  checked continuously in CI.
- Extended fuzz testing to cover every parser that handles data from
  outside vnprox's own control (interface files, LLDP, FRR/BGP output,
  DHCP leases, firewall logs, peer authentication).

## [0.9.0] - 2026

Firewall management and the path simulator.

### Added

- Full visual firewall management across datacenter, node, and guest
  scopes: rule tables, aliases, IP sets, and security groups.
- A "resolved rules" view for any guest showing the exact effective rule
  evaluation order Proxmox's firewall applies, with each rule labeled by
  where it came from (cluster, security group, or the guest itself).
- Clear warning banners whenever a firewall scope is disabled, so a
  disabled datacenter or guest firewall is never a silent surprise.
- Drag-to-reorder rule editing, inline enable/disable, and a rule builder
  with autocomplete for aliases, IP sets, and service macros (with an
  expansion preview showing exactly what a macro matches).
- Safety guard on deleting a shared alias, IP set, or security group: if
  it's still referenced by any rule, the delete is blocked and every
  referencing rule is listed.
- Rule effects preview: for a group-referencing rule, see exactly which
  guests it applies to before you apply it.
- The path simulator: ask "can this VM reach that VM (or IP, or the
  internet) on this port?" and get a real, statically-computed answer —
  allowed, blocked, unreachable, or an honest "couldn't determine" — with
  the full hop-by-hop path, the specific rule that blocked it (one click
  takes you to that rule's editor), and a complete list of caveats. The
  simulator is built to never give a confidently wrong answer.
- Trace-path from the topology map: right-click any guest to trace a path
  to another guest, an IP address, or "the outside world"; the result
  highlights the path on the map with its verdict.
- Simulation results are shareable via URL.
- A cluster-wide firewall log viewer with rule correlation (which rule
  produced a given log line, where determinable) and protection against
  log storms overwhelming the browser.

## [0.8.0] - 2026

True multi-node clustering, physical-layer discovery, and the SDN/IPAM
cockpit. (Includes Phase 3's cluster/discovery work, which the roadmap
does not cut as a separate release.)

### Added — cluster & discovery

- Genuine multi-node clustering: every view and every edit works the same
  way whether the node involved is local or a cluster peer.
- Physical switch discovery via LLDP: real switches now appear on the
  topology map with port-level wiring to your hosts.
- A VLAN cross-check that compares your bridge/bond VLAN configuration
  against what your switches actually advertise over LLDP, and flags
  mismatches.
- A dedicated ports table (with CSV export) showing every LLDP-discovered
  physical link.
- Automatic drift detection: vnprox continuously checks for configuration
  inconsistencies across your cluster — mismatched same-named bridges,
  MTU mismatches along a path or across nodes, SDN configuration that
  doesn't match zone membership, staged-but-never-applied network changes,
  and live state that's drifted from declared configuration — with a
  one-click fixing change wherever the fix is unambiguous.
- Distributed rollback safety for multi-node changes: each affected node
  arms its own independent safety timer, so no single node's rollback
  protection depends on the others staying reachable.
- A cluster-wide MAC/FDB browser: look up any MAC address (or part of one)
  and see which bridge and port it's on, and which guest owns it.
- Cluster-wide audit log and snapshot history, with a clear "partial
  results" indicator if a peer is temporarily unreachable — never a silent
  gap.

### Added — SDN & IPAM

- An SDN cockpit: browse your zones, VNets, and subnets as a tree with
  per-node health/apply status, and see exactly what a staged-but-
  unapplied SDN change will do before you apply it.
- A topology map overlay for SDN — VNet planes across the bridges that
  realize them, and a VXLAN/EVPN tunnel mesh with MTU annotations.
- Guided setup wizards for all five SDN zone types (Simple/NAT, VLAN,
  QinQ, VXLAN, EVPN), each with plain-English explanations and a live
  preview of the resulting topology as you fill in the form.
- The VLAN zone wizard cross-checks your chosen VLAN ID against what LLDP
  says your switch port actually trunks, and warns you if it's missing.
- The VXLAN wizard shows the MTU math explicitly (underlay MTU minus
  overhead) with a one-click "use the safe value" fix.
- EVPN/BGP observability: a peering matrix, per-session detail (prefixes,
  uptime, last error), a VNI list, exit-node health, and detection of
  flapping BGP sessions.
- Visual IPAM: color-coded allocation grids for your subnets showing
  confidence (allocated via Proxmox IPAM, observed via guest agent, both,
  or conflicting), automatic conflict detection (duplicate IPs, observed-
  but-unallocated addresses, allocations that don't match any known
  guest) with suggested resolutions, and a "next free address" picker
  usable directly from the bridge editor.
- DHCP range management on SDN subnets, plus a live leases view correlated
  to your guests by MAC address — reservations and allocations are one
  and the same record, shown consistently in both the IPAM grid and the
  DHCP view.
- First-class Open vSwitch support: OVS bridges, bonds, and internal ports
  are visualized, edited, and validated with the same safety checks as
  Linux bridging.

## [0.5.0] - 2026

Beta: the change engine and core network editing. This is the first
release where vnprox can actually modify your network, not just show it
to you.

### Added

- Safe network editing end to end: every change is staged as a draft,
  validated, shown as a diff, applied, and requires explicit confirmation
  — with automatic rollback if you never confirm.
- A changeset drawer: accumulate multiple pending edits, reorder them,
  and park a named draft to resume later.
- Live, plain-English validation as you edit, with one-click fixes for
  common problems (e.g. an MTU or VLAN ID out of range).
- A review screen before every apply, with three views of exactly what
  will happen: a human-readable summary, the literal file diff, and the
  execution plan.
- A commit-confirm safety window after every apply: a visible countdown
  during which you must confirm the change worked; if you don't (for
  example, because the change broke your connection to vnprox), it's
  automatically rolled back.
- Protected-interface guardrails: vnprox detects your management IP and
  cluster (corosync) links during setup and blocks any change that would
  cut them off, unless you explicitly override it.
- Deleting a bridge that still has running VMs/containers attached is
  blocked unless you also move those guests in the same change.
- Full editors for bridges (including VLAN-aware bridges with a VID range
  editor), bonds, VLAN sub-interfaces, and plain interfaces, each with
  inline plain-English help.
- Drag-and-drop editing directly on the topology map — drag a NIC onto a
  bond or bridge, or retarget a guest's network connection.
- Bulk guest network reattachment: move many VMs/containers to a new
  bridge in a single operation.
- A raw interfaces-file editor for advanced users, with syntax
  highlighting and live linting, protected by the same safety checks as
  the guided editors.
- Full change history: every applied change is snapshotted; browse,
  diff against any other point in time or against live state, and
  restore.
- An audit log recording every action taken through vnprox.
- `vnproxctl` command-line recovery tools that work even if the daemon
  itself is down (direct snapshot restore and emergency rollback).

## [0.1.0] - 2026

Private preview: read-only visibility.

### Added

- Installable as a Proxmox VE add-on via a `.deb` package with a
  systemd service.
- Log in with your existing Proxmox VE credentials — no separate account
  or password to manage.
- A live, auto-laid-out network topology map of your whole cluster:
  physical NICs, bonds, bridges, VLANs, and guest network connections.
- Four togglable map layers (physical / L2 / SDN / guests) so you can
  focus on the level of detail you need.
- Cluster-wide search by name, MAC address, IP address, or VM/container
  ID, with a keyboard shortcut to jump straight to it.
- Click any element on the map for full detail: its normalized fields,
  live status, and raw underlying configuration.
- Real-time updates: the map reflects changes on your cluster within one
  polling cycle, no manual refresh needed.
- Works from any single node's vnprox instance — the whole cluster's
  network is visible regardless of which node you're connected to.
- Dark and light themes, and keyboard shortcuts for layer toggles, VLAN
  filtering, and search.
- Read-only by design at this stage, and permission-aware: what you can
  see mirrors your existing Proxmox VE permissions, and vnprox never
  modifies your network configuration.
