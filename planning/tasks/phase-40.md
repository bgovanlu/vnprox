# Phase 40 — Operate at scale

**Arc:** *the core promise ("see and safely change your network") is proven on one operator's
screen; this phase is what lets a shop with existing tooling — Terraform, Ansible, a SIEM, a
change calendar — fit vnprox into how they already work, without weakening the one guarantee that
makes vnprox safe to adopt: every mutation still flows through `internal/change`.*

## Premise

Fifteen cards, T-4001–T-4015, drawn verbatim from `planning/roadmap-open-source.md`'s Phase 40
table — no items added, removed, or renumbered. Cards here are **stubs at Phase-37 fidelity**: a
2–4 sentence summary, 3–6 deliverable bullets naming real files/packages/mechanisms, and 2–4
checkable acceptance criteria. Each stub is expanded to full fidelity (detailed deliverables,
evidence obligations) by a sonnet agent immediately before dispatch, grounded in the code as it
exists *then* — per the roadmap's "Execution model" section, restated here rather than repeated
per card.

**The load-bearing fact every card in this phase inherits.** Two compile-time-enforced seams
already define "stage, never apply" in this codebase, and every integration below is required to
reuse one of them rather than invent a third:

- `internal/plugin.Stager` (`internal/plugin/stager.go`) exposes exactly `Create` and `Validate`
  against a `changeCreator` interface narrowed to those two methods — Apply/Confirm/Rollback are
  not in the interface the plugin seam holds, so a plugin binary *cannot* apply, not merely "is
  told not to". `frozen_interfaces_test.go` and `surface_test.go` guard the shape.
- `internal/mcp.ChangesetStager` (`internal/mcp/stageonly.go`) is the same pattern for the MCP
  surface: a `stageOnlyShape` type implementing exactly the allowed verbs is asserted to satisfy
  the interface at compile time — widening the interface to include Apply breaks the build, naming
  the offending method, before any test runs.

T-4001 (Terraform), T-4002 (Ansible), T-4003 (runbooks) are three more callers of this same
boundary: `apply` in a `.tf` plan, an Ansible playbook, or a runbook step means "stage a draft
changeset and stop" — never a new code path that reaches `Service.Apply`. A human (or the existing
confirm/scheduled-apply machinery) remains the sole apply authority, exactly as it has since T-205.

**Nothing in this phase adds an apply path anywhere** (roadmap decision D4: the change engine is
the sole mutation path). Where a card's roadmap description reads as if it might ("stage changesets
that apply"), the card below is the corrected, stage-only reading.

## Repo facts that correct the roadmap's Phase 40 assumptions

The roadmap was written from the feature inventory, not from re-reading the packages named. Three
items changed shape once the actual code was read; each is called out again in its own card so an
expanding agent does not silently re-scope back to the roadmap's original (wrong) premise:

1. **T-4005 (spec capture) is not missing — it ships today.** `internal/spec.Export` (see
   `internal/spec/doc.go`, `export.go`) already renders a byte-stable `Spec` from live inventory,
   and `GET /api/v1/spec` (`internal/api/spec.go`) already serves it as `specVersion:1` YAML. The
   "missing inverse of spec apply/plan" framing is wrong; the actual gap is that this only exists
   as a raw HTTP route — there is no `vnproxctl spec` subcommand at all (`cmd/vnproxctl` has no
   `spec*.go` file, unlike every other route family). T-4005 is rescoped below to close that CLI
   gap, not to build export logic that already exists.
2. **T-4015 (WireGuard as ordinary ops) is further along than "generalize the interconnect."**
   `internal/change/op.go` already defines `OpWgTunnelCreate`, `OpWgTunnelUpdate`,
   `OpWgTunnelDelete`, `OpWgPeerAdd`, `OpWgPeerRemove` as ordinary, federation-agnostic changeset
   ops — `apply_plan.go` and `preview.go` already apply and diff-preview them like any other op,
   and `GET /api/v1/wireguard/tunnels` is already a general (non-federation-namespaced) route. The
   op/apply/key-custody machinery is not a thing to build; the only federation-specific piece is
   the UI, which lives entirely in `web/src/wireguard/ConnectClustersWizard.tsx` and its wizard
   store. T-4015 is rescoped below to a general-purpose creation surface for ops that already work.
3. **T-4011 (CLI `--json`) is a partial-coverage gap, not a green-field one.** `parseOutputFormat`
   (`cmd/vnproxctl/output.go`) already backs `-o json` on doctor, rollback, backup, verify, bundle,
   hub, status, snapshots, telemetry, and remote — roughly a dozen subcommands already. The gap is
   the remaining subcommands (policy, gitsync, certs, apply, changesets-review, peertrust have no
   `-o`/`--json` flag at all) plus shell completions, which do not exist anywhere in the tree.

## Cards

### T-4001 · Terraform/OpenTofu provider
**model:** sonnet-5 · **size:** L · **depends:** —

A Terraform provider exposing vnprox inventory as read-only data sources and network entities
(bridges, bonds, VLANs, SDN zones/vnets, firewall rules) as resources whose `terraform apply`
stages a draft changeset and stops — the provider never calls anything past `Create`/`Validate`.
This is the phase's biggest adoption lever and the card that fixes the stage-only contract's exact
shape for T-4002 and T-4003 to reuse.

**Deliverables**
- A sibling Go module (its own `go.mod`, e.g. `terraform-provider-vnprox/`) built on
  `terraform-plugin-framework`, outside `cmd/vnproxd`'s build graph entirely — the same structural
  isolation `internal/hubreg/sigstoreverify` uses to keep sigstore-go out of the daemon (commit
  `34c11588`), except here the isolation is a genuine separate module rather than an unimported
  subpackage, because a Terraform provider is its own compiled binary, not a library `vnproxctl`
  ever links.
- Every resource's `Create`/`Update`/`Delete` maps to exactly one `change.Op` batch staged via the
  vnprox HTTP API (`POST /changesets`, `POST /changesets/{id}/validate`) — never
  `POST /changesets/{id}/apply`. `terraform apply` produces a **draft, validated changeset**;
  making it live is a vnprox review action, documented as such in the provider's own docs.
  `terraform plan` maps to `GET /changesets/{id}/diff`.
- Data sources for topology/inventory reads (bridges, bonds, SDN objects, firewall rulesets) via
  the existing read routes — no new backend endpoints for reads.
- A guard test (mirroring `cmd/vnproxd/sigstoreguard_test.go`'s `go list -deps` pattern) proving
  `cmd/vnproxd`'s and `cmd/vnproxctl`'s dependency graphs never pick up
  `terraform-plugin-framework` or `terraform-plugin-go`.

**Acceptance criteria**
1. A `terraform apply` against a running vnproxd (mock or `vnprox-dev`) creates a `status: draft`
   or `status: validated` changeset visible in `GET /changesets/{id}` — never `status: applied`.
2. `go list -deps ./cmd/...` from the main module contains zero `terraform-plugin-*` packages.
3. Provider acceptance tests run against `internal/pvemock`-backed vnproxd, table-driven per
   resource type, matching this repo's existing test convention.

### T-4002 · Ansible collection
**model:** sonnet-5 · **size:** L · **depends:** T-4001 (reuses its stage-only wire contract)

An Ansible collection whose modules mirror T-4001's stage-only contract — `state: present/absent`
maps to a staged, validated draft changeset, never an apply — plus a dynamic inventory plugin built
from live topology, so `ansible-inventory` can enumerate vnprox-managed nodes/bridges/bonds without
a second source of truth.

**Deliverables**
- A collection tree (`ansible/vnprox/` or a sibling repo-subdir, structurally separate from the Go
  module the same way T-4001's provider is — Python/YAML has no Go dependency-graph exposure to
  guard, but the same "lives outside vnproxd's build" principle applies) with modules for the same
  entity set T-4001 covers (bridges, bonds, VLANs, SDN objects, firewall rules), each calling the
  vnprox HTTP API's stage/validate routes only.
- A dynamic inventory plugin (`vnprox.vnprox.topology_inventory` or similar) sourced from
  `GET /api/v1/topology` / inventory read routes, grouped by node/cluster.
- Idempotency: a module run against state that already matches vnprox's live inventory reports
  `changed: false` without staging an empty changeset — mirrors the diff-before-stage discipline
  `internal/spec.Import` already uses (absent→create / divergent→update / matching→noop).
- Molecule or equivalent integration tests against a mock vnproxd (`internal/pvemock`-backed),
  matching T-4001's test convention rather than inventing a second one.

**Acceptance criteria**
1. A playbook run with `state: present` on a new bridge stages a draft changeset and reports
   `changed: true`; a second run against unchanged live state reports `changed: false` and stages
   nothing.
2. No module in the collection calls an apply/confirm/rollback route — grep-checkable against the
   collection's own route-constant list, documented as a lint rule in its CI.
3. `ansible-inventory --list` against a live vnproxd/pvemock returns node/bridge/bond groups that
   match `GET /api/v1/topology`'s content.

### T-4003 · Runbooks
**model:** sonnet-5 · **size:** L · **depends:** —

Parameterized sequences of read-checks and changeset-op templates attached to finding types —
"prepare remediation for this finding" runs the checks, renders the templated ops, and stages a
draft changeset, then stops. The MCP/plugin write boundary already defines the contract; a runbook
is a third caller of the identical `Create`+`Validate` pair, not a new mutation path.

**Deliverables**
- A `internal/runbook` package: a `Runbook` type (ordered steps: read-check | op-template),
  parameter binding from a finding's own fields (`internal/findings` types), and a `Prepare` method
  returning the same `(change.Op, error)` shape the blueprint/spec adapters already produce —
  reusing `internal/blueprint`'s absent→create/divergent→update pattern rather than inventing a
  fourth op-drafting convention (spec import, blueprint instantiate, drift fix are the other three).
- `Prepare` calls only `changeCreator`-shaped methods (`Create`, `Validate`) — enforced the same
  way `internal/plugin/stager.go` enforces it: a narrowed interface, plus a compile-time assertion
  test alongside `internal/mcp/frozen_payload_test.go`'s pattern.
- A library of built-in runbooks for the highest-value finding classes (starting with whatever
  `internal/findings` currently has the most `health_*.go` checks for — read the current set at
  expansion time rather than guessing here) plus a documented schema for authoring new ones.
- `POST /findings/{id}/runbooks/{name}/prepare` (or equivalent route, named per `docs/api.md`
  convention at expansion time) returning the staged draft changeset's id.

**Acceptance criteria**
1. Running a runbook against a finding produces a `status: draft` changeset whose ops match the
   runbook's template, parameterized from that finding's actual data — never a `status: applied`
   or `status: confirmed` one.
2. A compile-time or reflection test proves `internal/runbook`'s change-engine seam has no
   Apply/Confirm/Rollback method, matching `internal/mcp/stageonly.go`'s guarantee.
3. At least one runbook round-trips through `internal/pvemock`: finding fires, runbook prepares,
   the resulting changeset validates clean.

### T-4004 · PVE upgrade advisor
**model:** sonnet-5 · **size:** M · **depends:** —

A checkable catalog of known network-affecting breaks between PVE versions — the
conntrack-procfs class (`planning/tasks/T-3711-conntrack-procfs.md`,
`planning/reports/evidence/T-3711-conntrack-netlink-lab-2026-08-25.txt`) as the first cataloged
entry — surfaced as a preflight an operator runs before upgrading a node, instead of relying on
someone remembering it happened last time.

**Deliverables**
- An `internal/upgradeadvisor` (or similar) package: a versioned catalog of
  `{fromVersionRange, toVersionRange, affects, check, remediation}` entries, starting with the
  conntrack procfs→netlink break as entry one, each entry backed by an evidence file the same way
  T-3711's fix is.
- A read-only check runner that evaluates the catalog against a node's current live config
  (existing `internal/host`/`internal/collect` reads — no new PVE writes), reporting which catalog
  entries would fire if that node upgraded to a named target version.
- A `vnproxctl doctor`-style surface or a new `vnproxctl upgrade-check` subcommand (decide which at
  expansion time, matching whichever existing command family it fits best) plus a UI surface if the
  finding-stream pattern fits better than a standalone command.

**Acceptance criteria**
1. Run against a node whose config matches T-3711's break class, the advisor flags it by name with
   a link to the remediation, and the catalog is data (a file/table), not a hardcoded `if` chain,
   so a second entry doesn't require restructuring the first.
2. The check is provably read-only: no `internal/change` op, no `pvesh` write, in the check path.
3. A table-driven test proves at least two catalog entries (conntrack-procfs plus one more found at
   expansion time) fire correctly against fixture "before" state and stay silent against "after".

### T-4005 · Spec capture CLI
**model:** sonnet-5 · **size:** M · **depends:** —

`internal/spec.Export` and `GET /api/v1/spec` already generate the declarative cluster spec from
live state (see "Repo facts" above) — the backend half of this card shipped under T-1101. The
actual gap is that there is no `vnproxctl spec` command at all: a config-as-code adopter today has
to hand-curl `GET /api/v1/spec` and knows nothing of `POST /spec/import`, `/spec/pin`, or
`internal/gitsync`'s propose flow without reading API docs. This card is the CLI on-ramp, not new
export logic.

**Deliverables**
- `cmd/vnproxctl/speccmd.go`: `vnproxctl spec export [-o file]` (calls `GET /spec`, writes the YAML
  — the direct missing counterpart to `vnproxctl backup`/`vnproxctl snapshots`'s existing
  file-output pattern), `vnproxctl spec import <file>` (calls `POST /spec/import`, prints the
  resulting draft changeset id and `notInSpec` list — never auto-applies), and `vnproxctl spec pin`
  / `spec unpin` wrapping the existing pin routes.
- `-o json` support via the existing `parseOutputFormat` (`cmd/vnproxctl/output.go`) — this command
  should not add a fourth output-flag convention.
- Docs: a short "config-as-code quickstart" section wherever `docs/development.md` or
  `docs/features.md` currently documents `/spec` only as an HTTP route.

**Acceptance criteria**
1. `vnproxctl spec export` against a running vnproxd/pvemock writes byte-identical output to two
   consecutive runs against unchanged state (spec.Export's own byte-stability guarantee, now
   reachable from the CLI).
2. `vnproxctl spec import` on a modified spec file stages a draft changeset and never calls an
   apply route — verified against a test double the same way other `vnproxctl` command tests do.
3. Command is documented in whatever `vnproxctl --help` index this repo already maintains.

### T-4006 · Freeze windows
**model:** sonnet-5 · **size:** M · **depends:** —

Time-based deny/warn rules in policy-as-code (`internal/change/policy.go`, T-2601): "no changeset
may apply between these hours/dates" as a declarative rule, evaluated at the same validate stage
every other policy rule already runs at — no second enforcement point. Pairs with the scheduled-
apply machinery that already exists (`internal/change/schedule.go`, T-1103) rather than duplicating
its window concept.

**Deliverables**
- Extend `policy_eval.go`'s closed `factFields` vocabulary with the fact a freeze rule needs
  (current time / the changeset's intended apply time) so a freeze window is expressible as an
  ordinary `PolicyRule` with `PolicyDeny`/`PolicyWarn` severity — not a new rule-type parallel to
  the existing `match`/`assert` shape (`policy.go`'s own design constraint: declarative data, not a
  scripting language; keep this rule the same kind of statement as `protected.go`'s hard-coded one).
- A change-calendar view (`web/src/`) listing declared freeze windows and any changesets scheduled
  to fire inside one, reusing `internal/change/schedule.go`'s existing `Schedule`/`WindowStart`/
  `WindowEnd` types for the "what's scheduled" half rather than inventing a parallel schedule model.
- Distinguish "freeze blocks staging/apply" (a policy deny at validate) from "freeze blocks the
  scheduled fire time" (checked at `fireSchedule`, `schedule.go`) — both paths must see the same
  freeze-window data, from one source.

**Acceptance criteria**
1. A changeset validated during a declared freeze window carries a `PolicyDeny` finding naming the
   freeze rule; validated outside the window, it does not.
2. A changeset already scheduled to fire inside a freeze window declared *after* scheduling is
   caught at fire time too, not only at the original schedule-time validate.
3. The change-calendar view renders at least one declared freeze window and one scheduled
   changeset on the same timeline.

### T-4007 · Node maintenance mode
**model:** sonnet-5 · **size:** S · **depends:** T-4006

Suppress findings/alerts for a node during a declared maintenance window, tied to the same calendar
T-4006 introduces. Suppressions are visible (never a silent mute) and expire on their own — no
"forgot to turn it back on" failure mode.

**Deliverables**
- A `maintenance_windows` concept (store table + `internal/findings` integration) scoped to a
  node/cluster and a `{start, end}` pair, reusing T-4006's calendar data model rather than a
  second one — a maintenance window and a freeze window are both "declared time ranges" the same
  calendar view should render together.
- `internal/findings/engine.go`'s evaluation path checks active maintenance windows before
  publishing a new finding for a suppressed node/scope; suppressed findings are marked
  `suppressed: true` in the API response, not omitted — an operator can still see what would have
  fired.
- Auto-expiry: a window past its `end` stops suppressing without any manual action, verified the
  same way `internal/capture`'s auto-purge sweep is (a tick-cadence check, not a cron the operator
  must remember).

**Acceptance criteria**
1. A finding that would fire for a node inside a declared maintenance window is marked
   `suppressed: true`, not silently dropped — visible via `GET /findings`.
2. The same finding condition outside the window fires normally (unsuppressed).
3. A window's suppression stops automatically the instant its `end` passes, with a test asserting
   this without requiring a manual "end maintenance" action.

### T-4008 · Policy packs on the hub
**model:** sonnet-5 · **size:** M · **depends:** DNS for hosted reach (owner decision, per roadmap)

Shareable policy-as-code bundles (T-2601 rule sets) as a new registry artifact type, additive to
the existing signed hub index format — the same signing/revocation story T-1107's blueprints
already use, extended to a second `EntryType` rather than a parallel mechanism.

**Deliverables**
- `internal/hub/hub.go`: add `TypePolicyPack EntryType = "policyPack"` alongside the existing
  `TypeBlueprint`/`TypePlugin`, and extend `ValidType` — purely additive, no existing entry type's
  behavior changes.
- `internal/hubreg/index.go`: a policy-pack entry validates and signs/verifies through the same
  path blueprint entries already use (the `unknown type %q` error path at `index.go:322` is the
  existing extension point); revocation (`index.go:345`'s same check) covers policy packs
  identically to blueprints/plugins.
- `vnproxctl hub` gains `policy-pack push/pull/list` subcommands mirroring the existing
  `hubcmd.go` blueprint/plugin verbs — same `-o json` convention (`parseOutputFormat`), same
  sigstore verification path in `internal/hubreg/sigstoreverify` (imported only by `vnproxctl`,
  never by `vnproxd` — the `TestVnproxdDoesNotImportSigstore` guard in
  `cmd/vnproxd/sigstoreguard_test.go` must still pass unchanged).
- A policy pack installs as one or more `PolicyRule`s (`internal/change/policy.go`) via the
  existing versioned policy store (`policy_service.go`) — installing a pack is a policy-set update,
  not a new apply-adjacent mechanism.

**Acceptance criteria**
1. `go list -deps ./cmd/vnproxd` still contains zero `sigstore` packages after this card — proving
   the new artifact type didn't reopen the T-3709 daemon/CLI split.
2. A policy pack pushed to a mock/local hub round-trips through pull + local install, ending with
   its rules present in `GET /policies` (or equivalent) exactly as a hand-authored `PolicyRule` set
   would.
3. Revoking a policy pack's index entry behaves identically to revoking a blueprint (same test
   shape as the existing blueprint revocation test, parameterized over `EntryType`).

### T-4009 · Air-gapped bundle
**model:** sonnet-5 · **size:** M · **depends:** —

An offline install bundle plus `vnproxctl hub mirror` for clusters with no outbound network — the
demo daemon (`--demo`) already proves the product runs with zero external calls; this card packages
that same no-network property as a deliberate offline install path rather than an accident of demo
mode.

**Deliverables**
- A `vnproxctl hub mirror <dir>` command that pulls the full hub index plus every referenced
  artifact (blueprints, plugins, and — once T-4008 lands — policy packs) into a local directory,
  reusing `internal/hubreg`'s existing fetch/verify path rather than a second downloader.
- A bundle format (tarball or directory) containing the `.deb`, the mirrored hub index, and
  whatever `docs/development.md`'s toolchain-pinning section already documents as needed for an
  offline Go build (it flags `GOTOOLCHAIN` auto-download failing air-gapped today — this card is
  partly what makes that documented limitation actually addressed).
- `vnproxd`/`vnproxctl` pointed at a local mirror directory instead of the hosted hub URL — a
  config value, not a code fork of the hub client.

**Acceptance criteria**
1. `vnproxctl hub mirror` against a reachable hub, then a `vnproxctl hub pull` pointed at the local
   mirror with the network disabled, succeeds and produces byte-identical artifacts to a direct
   pull.
2. A documented install sequence, exercised end-to-end at least once against `internal/pvemock`
   (or a disposable lab per `docs/development.md`'s existing disposable-lab section) with outbound
   network blocked, completes without any call that would 404 offline.
3. Sigstore verification (`vnproxctl`-side, per T-3709) still runs against mirrored artifacts —
   air-gapped does not mean unverified.

### T-4010 · `vnproxctl watch`
**model:** sonnet-5 · **size:** M · **depends:** —

A live terminal UI over the existing WS `"events"` topic (`internal/topology/hub.go`, T-1104,
frozen at D10) — findings, applies, and drift as they happen, from a shell. This card **reads** the
frozen envelope; it must not add a field or change a message shape, because D10 (`docs/architecture.
md` §13, "the WebSocket `"events"` stream schema... become[s] stable, documented compatibility
contracts") governs it, and additive-only within that freeze is the only move available.

**Deliverables**
- `cmd/vnproxctl/watchcmd.go`: connects to the existing `"events"` topic exactly as the web UI's WS
  client does (same auth/session handshake `internal/auth/caps.go`'s "gates the WS `events` topic"
  comment documents), and renders a scrolling/paginated TUI (a small, stdlib-adjacent TUI approach —
  flag any new TUI dependency per CLAUDE.md's stdlib-first rule if one is chosen at expansion time).
- Filtering by event kind (findings / changeset applies / drift), matching whatever kinds
  `topology.Hub`'s existing envelope already carries — read the current message shapes at
  expansion time rather than assuming a set here.
- Graceful reconnect on WS drop, matching the web client's own reconnect behavior if one exists
  (check `web/src/` at expansion time) rather than inventing different reconnect semantics for the
  CLI.

**Acceptance criteria**
1. `vnproxctl watch` against a live vnproxd renders a real applied changeset and a real fired
   finding as they occur, sourced only from the existing `"events"` topic — no new WS topic, no new
   HTTP polling loop duplicating what the topic already delivers.
2. No field is added to the `"events"` envelope; a diff of `internal/topology/hub.go`'s wire types
   before/after this card is empty.
3. The command degrades cleanly (clear error, non-zero exit) when the `"events"` topic is refused
   for the connecting identity's scope — the existing fail-closed behavior `hub.go` already
   documents, not a new permission model.

### T-4011 · CLI machine surface
**model:** sonnet-5 · **size:** M · **depends:** —

`-o json` on every remaining `vnproxctl` subcommand (per "Repo facts" above, roughly a dozen
commands already have it via `parseOutputFormat`; policy, gitsync, certs, apply, changesets-review,
and peertrust do not) with documented stable schemas, plus shell completions — neither of which
exist anywhere in the tree today.

**Deliverables**
- Extend `parseOutputFormat`'s `-o json` convention (`cmd/vnproxctl/output.go`) to every remaining
  subcommand that currently lacks it (`policycmd.go`, `gitsynccmd.go`, `certscmd.go`, `applycmd.go`,
  `changesets_review_test.go`'s command, `peertrust.go` — confirm the exact list at expansion time
  against the tree as it then stands).
- One documented, versioned JSON schema per command's output — a `docs/cli-json.md` or similar,
  analogous to how `docs/api.md` documents HTTP response shapes, since scripting adopters need the
  same drift guarantee an HTTP client gets.
- Shell completion generation (bash/zsh at minimum) for `vnproxctl`'s flag set, generated from the
  existing `flag`-based command definitions rather than hand-maintained per shell.

**Acceptance criteria**
1. Every `vnproxctl` subcommand accepts `-o json` (or documents why it structurally cannot — e.g. a
   purely interactive command) and produces valid JSON parseable without scraping table output.
2. A golden-file test per command's JSON output, matching the convention the dozen already-JSON
   commands use, so a future field rename is caught the same way `docs/api.md` drift is caught
   elsewhere in this repo.
3. `vnproxctl completion bash` (and zsh) produces a sourceable completion script exercised by at
   least one test invoking it and checking it parses as valid shell.

### T-4012 · Audit/finding SIEM export
**model:** sonnet-5 · **size:** M · **depends:** —

Structured streaming export (syslog/JSONL) of the audit log and findings stream. vnprox stays
not-a-warehouse (a permanent scope boundary per `docs/features.md` / this roadmap's header) by
shipping events **out** to a SIEM the operator already runs, not by storing more history itself.

**Deliverables**
- An export sink abstraction (`internal/audit` or a new `internal/siemexport` package) with a
  syslog (RFC 5424) writer and a JSONL-to-file/socket writer, fed from the same audit-row and
  findings-publish points `internal/topology.Hub`'s `"events"` topic already taps — a second
  consumer of an existing fan-in, not a new audit-capture path.
- Config-gated (off by default, per the product's opt-in convention elsewhere — telemetry, MCP)
  destination configuration: syslog host:port or a JSONL output path/socket.
- Backpressure/bounding: if the sink is unreachable, buffering is capped and old-events-dropped
  rather than unbounded queue growth — the same "bounded, not a warehouse" property
  `internal/capture`'s retention sweep and `internal/metrics`' ring both already enforce elsewhere
  in this codebase; reuse that pattern's shape rather than inventing an unbounded queue.
- Documented field mapping from vnprox's audit-row/finding shape to the exported syslog/JSON
  fields, so a SIEM's parser can be written once.

**Acceptance criteria**
1. With the sink enabled, every audit row and every published finding appears in the export stream
   within a bounded delay, verified against a test syslog/JSONL receiver.
2. With the sink unreachable, vnprox's own audit log and findings stream are unaffected — export
   failure never blocks or loses the primary record.
3. The export buffer has a hard cap (size or count) enforced in code, with a test proving it drops
   rather than grows unbounded under sustained sink unavailability.

### T-4013 · Read-only SNMP switch counters
**model:** sonnet-5 · **size:** L · **depends:** —

Port errors/discards/utilization from LLDP-discovered switches (`internal/host/lldp.go`), painted
on map edges. Read-only end to end — `internal/switchdrv`'s guarded-push boundary (its
`ErrNeighborMismatch` re-verification before every write, `driver.go`) is untouched; this card adds
a read path that never calls `SetPortConfig` or anything in `switchdrv` at all.

**Deliverables**
- A minimal SNMPv2c GET/GET-BULK client scoped to IF-MIB counters
  (`ifInErrors`/`ifOutErrors`/`ifInDiscards`/`ifOutDiscards`/`ifHCInOctets`/`ifHCOutOctets`) only —
  **flag the library choice explicitly in the task report**: `docs/development.md`'s locked tech
  stack has no SNMP entry today, so a third-party client (e.g. `gosnmp`) is a new dependency
  requiring the justification note CLAUDE.md's stdlib-first rule calls for; the stdlib-first
  alternative is a hand-rolled BER/ASN.1 encoder scoped to exactly the OIDs above (bounded enough
  to be feasible, unlike a general SNMP library) — decide and justify at expansion time, do not
  default to the third-party option without that note.
- A poller keyed off `internal/host`'s existing LLDP-discovered neighbor set — only switches
  vnprox already knows about via LLDP are polled, no separate switch-discovery mechanism.
- Map-edge rendering of the counters (`web/src/`, wherever topology edges already render, e.g.
  alongside `internal/switchdrv/openconfig.go`'s existing port-config read surface if one is
  already exposed to the frontend).

**Acceptance criteria**
1. Counters render on a map edge only for switches with a live LLDP neighbor relationship — no SNMP
   poll target invented from any other source.
2. A grep-checkable proof that this card's code path never calls `switchdrv.SwitchDriver.
   SetPortConfig` or any other write method — read path and guarded-push path share no call edge.
3. SNMP poll failures degrade to "no data" on the edge, not a blocking error anywhere else in the
   map render.

### T-4014 · SPAN/mirror session manager
**model:** sonnet-5 · **size:** M · **depends:** —

`tc mirred` mirror sessions staged through the change engine — bounded and audited in the same
*spirit* as packet capture (`internal/capture`'s hard caps + audit-row pattern), but staged
*differently*: capture is a direct capability-gated action (`auth.CapCapture`, no changeset op
exists for it — confirmed absent from `internal/change/op.go`), while a mirror session is an
**ordinary changeset op** (`tc.mirror.create`/`tc.mirror.delete`, following the `<domain>.<action>`
naming `op.go` already uses for `bond.*`/`bridge.*`/`wg.*`), because a mirror session is a
standing config change to a node's `tc` rules, not a bounded time-limited capture run.

**Deliverables**
- New `OpType`s in `internal/change/op.go` (`tc.mirror.create`, `tc.mirror.update`,
  `tc.mirror.delete`) plus their apply-step implementation (`internal/change/apply_*.go`, following
  whichever existing apply-step file pattern the SDN/bond ops use) — mirror-session config is
  captured pre-image and rolled back on the existing rollback path exactly like every other op.
- Server-enforced caps on mirror sessions borrowed from `internal/capture`'s clamp pattern
  (`Coordinator.clampCaps`): a maximum concurrent-session count and a maximum mirrored-bandwidth
  ceiling per node, enforced at validate/apply time, not merely documented.
- An audit row per create/delete (mirrors `capture.start`/`capture.stop`'s convention) recording
  actor, source port(s), and destination.
- Validation that a mirror destination is never the management interface/path (reusing
  `internal/change`'s existing management-path protection, `protected.go`'s pattern, rather than a
  new check with different semantics).

**Acceptance criteria**
1. Creating a mirror session produces an ordinary changeset that stages, validates, diffs, and
   rolls back exactly like a bond/bridge op — no bespoke lifecycle.
2. Exceeding the configured concurrent-session or bandwidth cap is rejected at validate time with a
   named blocking finding, not silently clamped or allowed through to apply.
3. A mirror session targeting the management interface is rejected the same way an ordinary
   mgmt-path-cutting op already is, verified against the existing protected-path test suite's
   pattern.

### T-4015 · First-class WireGuard tunnels
**model:** sonnet-5 · **size:** M · **depends:** —

Managed WireGuard links as a general-purpose creation surface, beyond the federation interconnect.
Per "Repo facts" above, the change-engine half of this is **already built**: `OpWgTunnelCreate`/
`OpWgPeerAdd`/etc. are ordinary, federation-agnostic ops (`internal/change/op.go`), already applied
and preview-diffed (`apply_plan.go`, `preview.go`), and `GET /api/v1/wireguard/tunnels` is already
a general route. The only federation-specific piece is the UI
(`web/src/wireguard/ConnectClustersWizard.tsx` and its wizard store) — this card is a general
tunnel-creation surface for ops that already work end to end, not new backend machinery.

**Deliverables**
- A general (non-federation-scoped) WireGuard tunnel creation UI in `web/src/wireguard/` — a
  standalone panel/wizard alongside the existing `ConnectClustersWizard`, reusing its op-drafting
  logic (`wizardOps.ts`) rather than duplicating it, generalized to accept an arbitrary remote
  endpoint instead of assuming "the other end is a vnprox-federated cluster."
- A `vnproxctl` surface (new subcommand or extending an existing one — decide at expansion time)
  for scripted tunnel creation, since a general-purpose tunnel is exactly the kind of thing an
  IaC-adjacent operator wants from a CLI, not only a wizard.
- Confirm at expansion time whether `validate_crosscluster.go`'s federation-cluster-scoping checks
  (`codeCrossClusterRef`) incorrectly reject a non-federation tunnel's ops today — if so, that is
  the one real backend fix this card needs; if the checks are already scoped only to
  federation-tagged ops, no backend change is needed at all.

**Acceptance criteria**
1. A WireGuard tunnel created through the new general surface produces the same `OpWgTunnelCreate`/
   `OpWgPeerAdd` op shapes a federation-wizard-created tunnel does — one op vocabulary, two entry
   points.
2. Creating a non-federation tunnel does not trip any federation-cluster-scoping validation that
   assumes every WireGuard op belongs to a federated cluster relationship.
3. The private key custody guarantee (`internal/wireguard`'s doc.go: generated on the owning node,
   sealed before touching the store, never logged/printed) holds identically through the new
   surface — same code path, not a reimplementation.

## Sequencing

```
T-4001 (Terraform) ─────► T-4002 (Ansible, reuses T-4001's stage-only wire contract)
                    ─────► T-4003 (Runbooks, same MCP/plugin write boundary — no code dependency
                                     on T-4001/T-4002, but shares the contract they establish)

T-4004 (upgrade advisor) ─┐
T-4005 (spec capture CLI) ┼── parallel anytime, no cross-dependency
T-4011 (CLI --json)      ─┘

T-4006 (freeze windows) ──► T-4007 (maintenance mode, same calendar)

T-4008 (policy packs) ── blocked on DNS for hosted reach (owner decision, unchanged from roadmap)

T-4009 (air-gapped bundle) ── independent; benefits from T-4008 existing but does not require it

T-4010 (watch TUI) ──┐
T-4012 (SIEM export) ─┼── parallel, both read the same "events"/audit fan-in from different angles
                      ┘

T-4013 (SNMP counters) ─┐
T-4014 (mirror sessions) ┼── parallel, late — no shared dependency with each other or T-4001–T-4012
T-4015 (WG tunnels)     ─┘
```

Matches the roadmap's own sequencing note: T-4001 first because its stage-only contract shapes
T-4002/T-4003; T-4004/T-4005/T-4011 parallel anytime; T-4006 → T-4007; T-4008 after DNS;
T-4013–T-4015 parallel late.

## Explicitly not in this phase

- **Any apply path outside `internal/change`.** Every integration in this phase — Terraform,
  Ansible, runbooks, mirror sessions, WireGuard tunnels — stages a draft changeset and stops.
  `terraform apply`, `ansible-playbook`, and a prepared runbook are all names for "create +
  validate," never "create + validate + apply."
- **A metrics/log warehouse.** T-4012 ships audit/finding events *out* to a SIEM the operator
  already runs; it does not grow vnprox's own retention.
- **Any write through `internal/switchdrv` beyond its existing guarded push.** T-4013 is read-only;
  it does not touch `SetPortConfig` or the neighbor-reverification path.
- **General switch management.** T-4013 is counters on map edges, not a switch config surface.
- **A second SNMP use beyond the counters named in T-4013's card** — no SNMP write, no SNMP-based
  config discovery beyond IF-MIB counters.
- **Hosted-service groundwork for T-4008 beyond what DNS unblocks.** The owner's DNS decision
  (recorded in the roadmap's Wave 0 "owner decisions already pending") gates the hosted half; this
  phase does not attempt to route around that decision.
