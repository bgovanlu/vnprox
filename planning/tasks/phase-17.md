# Phase 17 — The open platform (v3.0)

Goal: vnprox stops being only a product and becomes infrastructure. Every read surface it has ever
built (topology, findings, flows, simulations, diagnostics) and every safe way to act on that data
gets a programmable, extensible, multi-party surface — including for AI operators — with the change
engine as the one boundary that makes that sane. No card in this phase introduces a second mutation
path: AI-proposed changesets from the MCP server (T-1701), plugin-staged changesets (T-1702), and
tenant request-changesets (T-1703) are all ordinary changesets, staged→validated→diffed→applied→
confirmed/rolled-back exactly like every other mutation since T-205. This phase also carries the
arc's standing orchestration change (design §0): every merged task here receives an adversarial
Opus-class code review before any dependent task dispatches, additive to the heavyweight review
checkpoints named per card below (🔒 on T-1701, T-1702, T-1703, T-1704, and T-1707's pre-release
pass) — appropriate for a phase that opens an AI write-adjacent surface, a plugin execution boundary,
tenant isolation, and daemon failover in the same release.

Dependency shape: T-1701 (MCP), T-1702 (plugin SDK), T-1703 (multi-tenancy), and T-1704 (HA) are four
independent platform roots — each can start once its own specific dependencies land, not once the
others in this phase do. T-1702 and T-1704 depend only on already-shipped surfaces (T-1205's switch
driver interface, T-304/T-205's commit-confirm and apply machinery) and can begin very early relative
to the rest of the phase; T-1701 is the one root gated cross-phase, since it exposes the Phase 13
diagnosis ladder (T-1307) and the Phase 16 planner/simulator cores (T-1602, T-1604) and cannot start
before those land. T-1705 (hub) depends on T-1107 (already-specified blueprint bundles) and T-1702;
T-1706 (embeds) depends on T-1001/T-1104 (already-specified exporter/tokens) plus T-1607 (posture
score) and T-904 (dashboard). T-1707 is the arc-closing join, depending on all four platform roots.

Origin: `docs/roadmap-universal.md`'s Phase 17 section (v3.0) and the companion design document's
Phase 17 subsection (§1) and cross-cutting §§2/3/6/7. This phase's exit demo: an on-call AI assistant,
speaking MCP, triages a 3 a.m. alert — runs the Phase 13 diagnosis ladder, identifies a failed bond
slave, and stages the failover changeset; the on-call human confirms it from their phone using the
Phase 9 triage layout. The next morning, a tenant's VLAN request is approved from the same approval
queue, and the whole incident is visible on the NOC's embedded dashboard. v3.0 ships.

---

## T-1701 · MCP server & AI operator readiness ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-1104 (tokens/scopes), T-1307 (diagnosis ladder), T-503 (path simulator engine), T-1602 & T-1604 (planner/failure sims), T-205 (changeset staging) · **context:** `planning/tasks/phase-11.md` T-1104 (event stream & automation tokens, `api_tokens`/bearer middleware), `docs/architecture.md` §4 (change engine lifecycle), `docs/api.md` (Changesets, Auth sections), `docs/security.md` (Authentication/Authorization), `docs/roadmap-universal.md` Phase 13/16 sections (diagnosis ladder, planner, failure sim — the read surfaces this card wraps), `internal/change/`

**Objective:** A first-class MCP (Model Context Protocol) server exposing vnprox's **read** surfaces
(topology, findings, flows, IPAM, simulations, diagnostics ladders) and its **staging** surfaces
(draft changesets, run diagnostics, run simulations) — **never direct apply**. This card is the
arc's thesis applied to AI: capability through the change engine, not around it. Every MCP tool is
capability-scoped via T-1104 bearer tokens; AI-originated changesets are labeled in the audit trail;
a human (or T-1103's confirm machinery) remains the sole apply/confirm authority.

**Safety analysis (required section, T-703/T-1701-level rigor, cross-referenced by test names below):**
- **The tool registry is a fixed, enumerable allowlist**, never a generic "call any route" bridge —
  each tool maps 1:1 to one read or staging endpoint; there is no tool, parameter, or combination of
  tools that reaches `POST /changesets/{id}/apply|confirm|rollback`. This is verified by a registry
  enumeration test (AC1), not by convention.
- **Scoping cannot be escalated.** A tool is only exposed to a connected MCP session if the
  authenticating token's scopes (T-1104) cover it; the server derives exposure at connection time from
  the token, never from a client-asserted capability list.
- **AI origin is unerasable in audit.** Every changeset created via MCP carries `origin: "mcp"` and
  `originTokenId`; every MCP tool invocation writes its own audit row with actor `mcp:<token-name>` —
  a human reviewing `GET /audit` can always tell an AI-originated action from a UI-originated one.

**Deliverables:**
- New `internal/mcp` package: an MCP server (stdio + SSE transport) whose tool set is an explicit,
  documented allowlist — `topology.get`, `findings.list`, `flows.query`, `ipam.subnets.list`,
  `simulate.path` (T-503), `diagnose.run` (T-1307's ladder), `changesets.create` (draft only),
  `changesets.validate`, `changesets.diff` — and nothing else; no `apply`/`confirm`/`rollback`/
  `discard` tool is ever registered.
- Bearer-token auth reusing T-1104's middleware unchanged: an MCP session authenticates with a token
  carrying an `automation` scope plus whatever capability scopes the operator granted; the server's
  tool-registration step filters the exposed tool list to what the token's scopes actually cover.
- Changeset shape gains `origin` (`"ui"`\|`"mcp"`\|`"cli"`\|...) and `originTokenId?` fields
  (additive); audit entries for MCP actions use the `mcp:<token-name>` actor format.
- Mock MCP client (fixture family, new): a JSON-RPC test harness driving a running server instance
  against `internal/pvemock`, used by every acceptance test below.

**Acceptance criteria:**
1. Tool-registry enumeration test: the exact documented tool list exists and nothing else; a
   regression assertion fails loudly if any future change adds a tool matching `apply`/`confirm`/
   `rollback`/`discard` by name or by reachable code path.
2. A token scoped `{netRead, automation}` can call the read/simulate/diagnose tools; the same token's
   session never exposes `changesets.create` (which needs `netWrite`) — table test over scope
   combinations.
3. E2E against `single-node`: an MCP client with `{netRead, netWrite, automation}` runs
   `diagnose.run` then `changesets.create` → returns a draft changeset with `origin: "mcp"`, status
   stays `draft`; the mock client has no tool that could apply it.
4. `GET /audit` shows the MCP tool invocations and the resulting changeset's audit trail with actor
   `mcp:<token-name>`, visibly distinct from a UI-originated changeset in the same fixture run.
5. Revoking the token mid-session force-closes the MCP session within one server tick (same bound as
   T-1104 AC5's WS-subscription close).
6. Safety-analysis section present and cross-referenced by AC1 and AC4's test names.
7. `docs/api.md` gains an "MCP server" section (transport, auth, full tool table with required
   scopes); `docs/security.md` documents the stage-only boundary as this card's central invariant;
   `make check` green.

---

## T-1702 · Plugin SDK ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-1205 (switch driver interface), T-1002 (flow/telemetry ingestion), T-1406 (ingress discoverer), T-904 (dashboard tiles) · **context:** `planning/tasks/phase-12.md` T-1205 (`internal/switchdrv.SwitchDriver` interface, feature-flagged-dark pattern), `planning/tasks/phase-10.md` T-1002 (flow ingestion engine), T-904 in `planning/tasks/phase-9.md` (dashboard tiles), `docs/roadmap-universal.md` Phase 14 (ingress discoverer, becomes pluggable per this card), `docs/architecture.md` §2 §10 (package layout, API-stability precedent), `docs/development.md` (Go standards, dependency-addition rule)

**Objective:** Stable, versioned extension points for the surfaces third parties keep asking to
extend — switch drivers (beyond T-1205's OpenConfig/gNMI), flow/telemetry ingestors, finding packs,
ingress discoverers (Phase 14's T-1406 list becomes pluggable), and dashboard tiles. A versioned Go
plugin API plus an out-of-process gRPC option, capability-scoped, plugins declared in the audit
trail; existing built-ins migrate onto the same interfaces to prove them, not just document them.

**Safety analysis (required section, cross-referenced by test names below):**
- **A plugin can stage, never bypass.** The change-engine seam handed to plugin code exposes only
  `Create`/`Validate` from `internal/change.Service` — no `Apply`/`Confirm`/`Rollback` method is
  reachable from the plugin runtime, in-process or over gRPC, verified by an interface-surface test.
- **Capability scope is a ceiling, not a grant.** A plugin's declared capabilities are checked against
  the existing `internal/auth/caps.go` vocabulary — this card adds no new privilege beyond what
  change-engine ops already gate; a plugin cannot construct an op class its declared capabilities
  don't cover.
- **The out-of-process boundary is real but bounded.** A gRPC plugin process is spawned/supervised by
  vnproxd and never given direct DB or file access; its residual risk (unconstrained OS-level network
  egress from the plugin's own process) is stated plainly rather than engineered away, mirroring
  T-1205's stated-not-hidden residual-risk pattern for switch rollback.

**Deliverables:**
- New `internal/plugin` package: versioned Go interfaces per extension point — `SwitchDriver` (reused
  from T-1205's `internal/switchdrv`), `FlowIngestor`, `FindingProducer`, `IngressDiscoverer` (T-1406's
  interface), `DashboardTileProvider` — each frozen at v1 with the deprecation policy T-1707 writes
  into `docs/architecture.md` §10.
- Out-of-process option: a `.proto` mirroring each Go interface, plus an `internal/plugin/grpcshim`
  adapter so a plugin process implements the identical contract over gRPC.
- New app-store table `plugins` (id, name, extensionPoints `[]string`, capabilities `[]string`,
  transport `"in-process"`\|`"grpc"`, enabled, installedBy, installedAt).
- Plugin conformance harness + one sample plugin per extension point (fixture family, new): the same
  table-driven suite runs against both in-process and gRPC-shimmed samples.
- Built-in migration proof: T-1205's OpenConfig/gNMI driver re-registered as an in-process built-in
  plugin through this registry (T-1406's discoverers migrate additively once that card lands).

**Acceptance criteria:**
1. Conformance harness passes identically for in-process and gRPC transports, all five extension
   points — parity table test.
2. A plugin whose declared capabilities exclude `netWrite` cannot construct a `netWrite`-class op —
   rejected before reaching `internal/change` (table test).
3. Interface-surface test asserts the plugin-facing `change.Service` seam has no `Apply`/`Confirm`/
   `Rollback` method reachable — same style as T-1701 AC1.
4. T-1205's switch driver, re-registered through the plugin registry, produces output identical to
   its pre-migration direct-call form (golden comparison).
5. Plugin install/enable/disable/uninstall are audited (`plugin.install`/`enable`/`disable`/
   `uninstall`) with capabilities recorded; a fault-injection test kills a gRPC plugin mid-call and
   confirms its dashboard tile/finding pack degrades gracefully without crashing vnproxd.
6. Safety-analysis section present, cross-referenced by AC2 and AC3.
7. Report enumerates hardware-validation gaps (real vendor gNMI plugin behavior, real third-party
   process resource limits) into `planning/reports/needs-hardware-validation.md`; `docs/api.md`
   (plugin management routes), `docs/architecture.md` §10 (extension points + deprecation policy),
   `docs/security.md` (capability-scope model) updated; `make check` green.

---

## T-1703 · Multi-tenancy & self-service
**model:** sonnet-5 · **size:** L · **depends:** T-1201 (federation permission model), T-1207 (OIDC), T-1103 (scheduled/confirm machinery), T-1104 (scopes), T-1005 (alert routing) · **context:** `planning/tasks/phase-12.md` T-1201 (cluster registry), T-1207 (OIDC group→role mapping, mock OIDC provider), `planning/tasks/phase-11.md` T-1103 (scheduler/confirm machinery, injected `Clock` pattern), T-1104 (token scopes), `planning/tasks/phase-10.md` T-1005 (alert routing), `docs/security.md` (Authorization, `forceReadOnly` enforcement-point pattern), `docs/data-model.md` §2 (App store)

**Objective:** Delegated views and workflows on the federation-era permission model: a tenant sees
only their guests/VLANs/subnets and can **request** changes through request-changesets that route
to an approver, with scoped dashboards and alert routes. Approval chains reuse T-1103's scheduled/
confirm machinery; OIDC (T-1207) supplies identities. **This is a 🔒 review checkpoint** (design §3):
the sonnet-executed authz model composes already-frozen primitives rather than inventing a new core,
but cross-tenant leakage is exactly the failure mode a strong-model review targets before the next
dependent (T-1707) builds on it.

**Deliverables:**
- New app-store tables: `tenants` (id, name, createdBy, createdAt), `tenant_scopes` (tenantId,
  scopeRef — a `Ref` pattern naming the guests/VLANs/subnets/clusters visible to the tenant),
  `tenant_members` (tenantId, identity, role `"member"`\|`"approver"`).
- Server-side scoping middleware filtering every read route (`/topology`, `/findings`, `/ipam/*`,
  `/flows`) to the caller's `tenant_scopes` at the query layer — the same enforcement-point pattern
  `internal/auth.forceReadOnly` already uses, never a response-shaping filter a client could bypass;
  an out-of-scope direct ref lookup returns `404`, not `403` (existence isn't confirmed).
- New changeset status `requested` (additive to the existing lifecycle): `POST /changesets
  {tenantId}` creates a request-changeset, validated exactly like a draft but blocked from `apply`
  until an approver calls `POST /changesets/{id}/approve` (converts it to an ordinary `draft`); a
  tenant member can never approve their own tenant's request (server-side role check) — approval is
  not apply, an approver still applies through the ordinary changeset flow afterward.
- Approval routing reuses T-1103's notification plumbing: a pending request-changeset raises a
  routed notification to the tenant's approver group via T-1005's alert routing.
- `GET /dashboard?tenantId=` and per-tenant alert-route config, both filtered through the same
  `tenant_scopes` middleware; mock OIDC tenants (fixture family, extends T-1207's mock provider with
  tenant/group claims).

**Acceptance criteria:**
1. A tenant scoped to one VLAN sees only that VLAN's guests/subnets in `/topology`/`/findings`/
   `/ipam/subnets`; a direct ref lookup for an out-of-scope entity `404`s.
2. A tenant member cannot `POST /changesets/{id}/approve` on their own tenant's request (`403`); an
   approver in the same tenant can.
3. E2E against `three-node-vlan`: tenant requests a VLAN reservation → `status: requested` → approver
   notified via T-1005 alert routing (assert the routed event) → approve → converts to `draft` →
   approver applies through the ordinary flow — full trace asserted.
4. Cross-tenant leakage regression: two tenants with non-overlapping scopes, N randomized reads each,
   zero cross-tenant data ever appears in either response set.
5. OIDC group-claim → tenant-membership mapping test against the extended mock OIDC tenants fixture.
6. Scoped dashboard/alert-route test: a tenant's dashboard tile counts and alert routes reflect only
   their scope.
7. `docs/api.md` (tenant/request-changeset routes), `docs/data-model.md` (new tables),
   `docs/security.md` (tenant authz model, explicit server-side-enforcement statement) updated;
   `make check` green.

---

## T-1704 · vnproxd HA ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-304 (commit-confirm timers), T-1103 (scheduled applies), T-205 (change engine), T-1201 (registry, for standby awareness) · **context:** `planning/tasks/phase-3.md` T-304 (distributed rollback, per-node local timers), `planning/tasks/phase-2.md` T-205 (apply engine/commit-confirm), `planning/tasks/phase-11.md` T-1103 (scheduler, injected `Clock`, restart-safe re-arm), `planning/tasks/phase-12.md` T-1201 (cluster/federation registry), `docs/architecture.md` §4 §5 §7 (change engine, cluster model, storage), `docs/data-model.md` §2 (`changesets.confirm_deadline`)

**Objective:** Active/standby daemon with state replication and VIP-or-DNS failover, so the network
tool is not itself the single point of failure T-1604's failure simulator would flag. **Commit-
confirm timers and scheduled applies survive failover** — the hard requirement that makes this P0
rather than polish — built on the forward-only migration guarantee, with explicit split-brain
handling for in-flight commit-confirm timers.

**Safety analysis (required section, T-703-level rigor, cross-referenced by test names below):**
- **Deterministic arbitration.** A leader-lease record (`ha_lease`: holder, term, expiresAt),
  renewed on a short interval by the active node, is the sole source of truth for who may act; a
  standby promotes only after the lease expires past a fencing margin — never on a transient blip.
- **No double-apply, no dropped rollback.** `changesets.confirm_deadline` and T-1103's
  `changeset_schedules.windowStart/windowEnd` are already absolute unix timestamps (T-304/T-1103) —
  replicated verbatim; on promotion the new active re-arms every timer from the persisted deadline
  through T-304's *existing* re-arm code path, not a new one. Only the current lease-term holder ever
  executes a rollback/apply-completion decision; a losing side's timer checks the lease before acting
  and fires as a no-op otherwise.
- **A demoted former-active takes no action.** If a partition heals and the old active discovers a
  newer lease term already exists, it demotes immediately and performs zero rollback/apply actions in
  the interim — the newer term always wins, never a race.

**Deliverables:**
- New `internal/ha` package: an active/standby pair replicating changesets, `changeset_schedules`,
  `api_tokens`, and the audit log over a channel built on `internal/peer`'s existing TLS+HMAC
  transport (metrics rings excluded — bounded/ephemeral, not safety-critical to replicate).
- `ha_lease` app-store table and the fencing-lease arbitration logic above.
- VIP-or-DNS failover: `[ha] mode = "vip"|"dns"` — VIP mode triggers a pluggable external announce
  mechanism, DNS mode triggers a pluggable webhook; both are operationally-provided integration
  points vnproxd triggers, not new daemon dependencies (flag any new dependency per CLAUDE.md).
- `GET /ha/status`: role (active/standby), lease term, replication lag; a standby exceeding a
  configured lag threshold raises `ha_replication_degraded`.
- Two-daemon HA replication harness (fixture family, new): two vnproxd instances, an injected `Clock`
  (T-304/T-1103 pattern), and an injectable network-partition switch — deterministic failover/
  split-brain tests with no real sleeps or real VIP movement.

**Acceptance criteria:**
1. Harness test: active applies a changeset, arms confirm-deadline T+30; replication catches up;
   active is killed at T+10 → standby promotes after lease expiry and re-arms the *same* absolute
   deadline T+30; a confirm ack lands `committed`, a missing ack rolls back at T+30 exactly once.
2. Split-brain injection (partition without kill): standby's lease acquisition fails while active's
   lease is still valid; healing the partition before lease expiry leaves active still active, zero
   double-apply.
3. Split-brain injection (partition long enough to promote): standby promotes on genuine lease
   expiry; healing the partition afterward makes the old active detect the newer term and demote/
   no-op before any rollback action — exactly one rollback decision is ever executed.
4. A scheduled changeset's (T-1103) window/deadline survives a mid-window failover: the new active
   fires apply at the original absolute `windowStart`, never a time recomputed from promotion.
5. `GET /ha/status` reports role/lease/lag correctly; a lag-threshold breach raises
   `ha_replication_degraded`.
6. Safety-analysis section present, cross-referenced by AC1–AC3's test names.
7. Report enumerates hardware-validation gaps (real VIP/ARP failover timing, real DNS TTL
   propagation, real partition behavior beyond the injected-fault harness) into
   `planning/reports/needs-hardware-validation.md`; `docs/architecture.md` (HA topology),
   `docs/data-model.md` (`ha_lease` table), `docs/deployment.md` (VIP/DNS setup) updated;
   `make check` green.

---

## T-1705 · Blueprint & plugin hub
**model:** sonnet-5 · **size:** M · **depends:** T-1107 (signed blueprint bundles), T-1702 (plugin SDK) · **context:** `planning/tasks/phase-11.md` T-1107 (bundle envelope, Ed25519 signing, trust-store gate), T-1702 (this phase, plugin registry), `docs/features/blueprints.md` (§5 to be added by T-1107), `planning/tasks/phase-12.md` T-1204's docs-boundary precedent is not applicable here — cite instead T-1106's "contract not source" boundary (`planning/tasks/phase-11.md` T-1106)

**Objective:** An opt-in public registry client for T-1107's signed blueprint bundles and T-1702's
SDK plugins — browse, install, update, with signature verification and a vetted tier. **This repo's
deliverable is the client and the registry-index contract, not the registry service** — mirrors
T-1106's "the provider/collection source lives in a separate repo" boundary; state that explicitly
in the report.

**Deliverables:**
- New `internal/hub` package: an HTTP client for a documented `GET <registry>/index.json` contract
  (name, version, publisher, signature, `vetted: bool`) that this card specifies but does not host.
- `GET /hub/index?type=blueprint|plugin` (netRead-gated, proxies/caches the registry index);
  `POST /hub/install {type, id, version}` (netWrite+CSRF): for blueprints, downloads and runs the
  bundle through T-1107's *exact* existing import path (verify → trust-decision → the same
  `POST /blueprints/import` semantics, reused not duplicated); for plugins, downloads and registers
  through T-1702's *exact* plugin registry (capability-scoped install, reused not duplicated).
- Vetted tier: `[hub] vetted_signers` (fingerprint allowlist) distinct from T-1107's per-admin trust
  store — a "vetted" badge means "this fingerprint is in the hub's own recognized list," purely
  informational; it never bypasses T-1107's ordinary trust-decision gate.
- No new implicit-trust path: `POST /hub/install` on an unsigned or untrusted-signature entry returns
  T-1107's exact `unsigned`/`untrustedSignature` status and requires the same explicit
  `{trustUnsigned: true}`/`{trustNewKey: true}` step.
- UI: a Hub browse/install page reusing T-1107's trust-status dialog and T-1702's plugin-capability-
  review dialog rather than building new trust UX (Vitest + Testing Library).

**Acceptance criteria:**
1. `GET /hub/index` against a fixture registry-index double returns typed, filterable results.
2. Installing a signed-and-vetted blueprint completes via T-1107's exact import path — a call-site
   check confirms no duplicated verification logic exists.
3. Installing an unsigned bundle is rejected without `{trustUnsigned: true}` — identical status
   code/audit shape to T-1107's own unsigned-import test.
4. Installing a plugin registers it through T-1702's plugin registry with its declared capability set
   surfaced to the operator before confirm.
5. The "vetted" badge appears only for fixture entries whose signer fingerprint is in
   `[hub] vetted_signers`, and never skips the trust-decision step — a vetted-but-not-yet-trusted-by-
   this-installation entry still requires explicit trust (regression test).
6. Grep-verifiable: no registry hosting/serving code exists in this repo (mirrors T-1106 AC4); report
   states where the registry service is expected to live.
7. `docs/features/blueprints.md` and the plugin docs T-1702 adds both gain a Hub subsection;
   `docs/api.md` documents the new routes; `make check` green.

---

## T-1706 · Embeddable views & Grafana panels
**model:** sonnet-5 · **size:** M · **depends:** T-1001 (Prometheus exporter), T-1104 (event stream/tokens), T-1607 (posture report), T-904 (dashboard) · **context:** `planning/tasks/phase-10.md` T-1001 (Prometheus exporter), T-1104 in `planning/tasks/phase-11.md` (`api_tokens`, WS `"events"` topic), `docs/roadmap-universal.md` Phase 16 section (posture score & report, T-1607 — not yet an authored card), `planning/tasks/phase-9.md` T-904 (home dashboard)

**Objective:** Read-only, token-scoped embeds of the map, dashboards, and posture report for wikis/
NOC screens/status pages, plus Grafana panel plugins backed by T-1001's exporter and T-1104's event
stream. An embed token can never exceed its minting user's scope and carries no write surface.

**Deliverables:**
- Embed tokens: reuse T-1104's `api_tokens` model with an additive `embed: true` flag and a mandatory
  `embedScopes` subset restricted to read-only capabilities — minting an embed token with any write
  scope is rejected server-side (`400`) regardless of the minting user's own capabilities.
- `GET /embed/map?token=`, `GET /embed/dashboard?token=`, `GET /embed/posture?token=` (the last wired
  behind a capability check that lights up once T-1607 ships, declared not blocking): a read-only
  "shell" route variant reusing existing view components with every mutation entry point removed at
  the component level, not hidden via CSS.
- Grafana panel plugin (`web/grafana-panel/`, or a stated external-repo boundary per the T-1106/
  T-1705 "contract not source" pattern — the report must pick and state one): a metrics panel
  consuming T-1001's exporter and a live event-annotation panel consuming T-1104's `"events"` WS
  topic.
- Embed routes are a distinct middleware path from session-cookie routes — an embed route never
  accepts a session cookie in place of an embed token.
- Docs: `docs/api.md` new `/embed/*` routes; `docs/security.md` embed-token model (explicit read-only
  ceiling).

**Acceptance criteria:**
1. Minting an embed token with any write scope is rejected (`400`) regardless of the minting user's
   own capabilities — table test.
2. `GET /embed/map?token=` with a valid read-scoped token renders read-only (DOM assertion: zero
   changeset/edit affordances present); an expired/revoked token `401`s.
3. An embed token scoped narrower than its minting user (e.g. `netRead` only, minting user also has
   `netWrite`) never surfaces `sdnWrite`/`fwWrite`-gated data — ceiling regression test.
4. Grafana metrics panel renders against a fixture Prometheus scrape (T-1001); the event-annotation
   panel renders against a fixture T-1104 WS event stream — component-level test with mocked
   transport (no live Grafana instance in CI).
5. `/embed/posture` exists but returns a documented "not yet available" response until T-1607 ships
   (same wired-but-dark pattern the design doc uses elsewhere for cross-phase gates) — test asserts
   the dark-state response shape.
6. Regression: no embed route accepts a session cookie in place of an embed token.
7. `docs/api.md` + `docs/security.md` updated; `make check` green.

---

## T-1707 · v3.0 release: HA/genscale performance, platform-API freeze, docs, packaging, security & PVE-compat pass
**model:** sonnet-5 · **size:** L · **depends:** T-1701, T-1702, T-1703, T-1704 · **context:** `planning/tasks/phase-12.md` T-1208 (v2.0 release precedent: genscale profile, docs freeze, packaging/upgrade testing, security pass, release checklist), `docs/architecture.md` §10 (API-stability precedent declared at v1.7), `docs/performance.md`, `docs/deployment.md`, `docs/security.md` (threat-model-summary table), `docs/roadmap-universal.md` Compatibility & versioning section

**Objective:** The v3.0 cut — an HA + multi-cluster genscale performance pass; the **platform API
freeze** (plugin API, MCP surface, event stream become stable, documented interfaces with the same
deprecation policy the changeset API adopted at v1.7); a docs freeze covering this arc's features;
packaging/upgrade-path testing from the v2.x line; a security pass over the arc's new write/
credential surfaces; an HA failover soak; and PVE 10.x/11.x compatibility validation. This is a
hardening/freeze/release task only — no new features.

**Deliverables:**
- Genscale profile extension (extends T-1208's multi-cluster profile) adding HA-standby overhead:
  active/standby replication lag and failover-promotion latency stated as explicit outputs alongside
  the existing per-cluster/federation numbers.
- Platform API freeze: a versioned MCP tool manifest, plugin interface versions, and event-stream
  schema each get a frozen v1 contract page with the deprecation-policy statement, written as new
  rows into `docs/architecture.md` §10's decisions table.
- Docs freeze: user guide chapters for MCP/AI operators, plugin authoring, multi-tenancy, HA
  operations, and the hub — matching existing user-guide chapter structure/tone.
- Packaging/upgrade-path testing: an apt upgrade from the v2.x line exercised against a v2.x-schema
  DB fixture (forward-only migrations); an HA-pair standby-first-then-active upgrade sequence
  documented and smoke-tested against T-1704's two-daemon harness.
- Security pass: `docs/security.md`'s threat-model-summary table gains rows for capture files
  (Phase 13), WireGuard keys (Phase 14), the MCP write-adjacent surface, plugin capability grants,
  tenant credentials, and the HA replication channel — each new credential type confirmed encrypted
  at rest with a targeted test.
- HA failover soak: a repeated failover/recovery run (iteration count stated in the report, not
  wall-clock-bound) against T-1704's harness, asserting zero double-apply/dropped-rollback across all
  cycles.
- Release checklist mirroring T-1208's/T-607's structure, extended with platform-freeze gates
  (API-freeze doc review complete, HA soak green, tenant-isolation suite green).

**Acceptance criteria:**
1. The HA/multi-cluster genscale profile is defined (numbers stated) and checked in; a scale run
   meets a stated latency/memory/failover-promotion-time bound, recorded in the report.
2. A v2.x-schema SQLite fixture DB migrates cleanly to v3.0's schema (forward-only migration test);
   an HA-pair standby-first upgrade sequence is smoke-tested against T-1704's two-daemon harness.
3. Platform-freeze doc: MCP tool manifest, plugin interface versions, and event-stream schema each
   have a versioned, frozen contract page stating the deprecation policy — a reviewable checklist
   naming every frozen surface and nothing else.
4. `docs/security.md`'s threat-model table has new rows for every listed arc-17 (and carried-forward
   13/14) credential/write-adjacent surface; each new credential type has a targeted encrypted-at-rest
   test.
5. HA failover soak reports zero double-apply and zero dropped-rollback across the stated number of
   cycles.
6. User guide chapters exist for MCP/plugins/multi-tenancy/HA/hub, matching existing structure; a
   release checklist covering the platform-freeze gates is produced.
7. `make check` green across the full v3.0 surface; PVE 10.x/11.x compatibility notes updated per
   `docs/roadmap-universal.md`'s Compatibility & versioning section.

---

## Card-author notes

- **No design/roadmap conflicts identified.** The design document's Phase 17 subsection (§1) and
  `docs/roadmap-universal.md`'s Phase 17 section agree on scope, P0/P1 marking, task themes, and the
  arc-end release framing; no scope arbitration was needed.
- **Cross-arc reference corrected:** the design document cites `T-501 (path simulator)` as a
  dependency of `T-1701` (and, in its Phase 14/15/16 sections, of several other cards). The actual
  arc-1 card at that ID, `planning/tasks/phase-5.md` T-501, is *"Firewall read: rulesets, objects,
  resolved view"* — the path simulator engine is **T-503** (`planning/tasks/phase-5.md` T-503, ★).
  T-1701's `depends:` line above uses T-503, matching the identical correction applied in
  phase-14.md (T-1404), phase-15.md (T-1505), and phase-16.md (T-1604). `simulate.path` wraps
  T-503's `POST /simulate/path` endpoint.
- **Forward references to not-yet-authored cards:** T-1701 depends on T-1307 (Phase 13, guided
  diagnosis flows), T-1602/T-1604 (Phase 16, microsegmentation planner / failure-impact simulation);
  T-1702 depends on T-1406 (Phase 14, ingress discoverer); T-1706 depends on T-1607 (Phase 16,
  posture score). None of `planning/tasks/phase-13.md` through `phase-16.md` exist yet in this repo —
  those cards are grounded in `docs/roadmap-universal.md`'s own Phase 13/14/16 sections and the design
  document's corresponding subsections rather than in card files, since no such files could be cited.
  When those phases' task cards are authored, this file's `context:` lines referencing them by phase
  number should be revisited to cite the specific card IDs.
- **Doc sections that do not exist yet, cited as "to be added":** `docs/features/blueprints.md` §5
  (bundle format) is referenced by T-1705 as "to be added by T-1107" since T-1107 (which creates it)
  is itself not yet implemented in this codebase (only T-1101 has landed from Phase 11 per `git log`).
  This mirrors how `planning/tasks/phase-11.md`/`phase-12.md` already cite each other's not-yet-built
  surfaces.
