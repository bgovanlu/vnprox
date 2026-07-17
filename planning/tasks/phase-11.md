# Phase 11 — Network as code & automation

Goal: turn the change engine into a platform. Everything the UI can do — declare intent, stage,
schedule, subscribe, automate, share — becomes drivable outside the browser, with the exact same
staged→validate→diff→apply→confirm/rollback safety guarantees, because every path in this phase
terminates in an ordinary changeset. No card introduces a second mutation path and no card
introduces an interlock override: T-1103's unattended-apply exclusion for management-path changes
is enforced server-side with no config flag to bypass it, matching `docs/security.md`'s existing
"no override in UI" stance on the T-203 safety interlocks.

Dependency shape: T-1101 (the declarative spec) and T-1104 (event stream + tokens) are the two
independent foundations most other cards build on; T-1103 (scheduling) is independent of both and
can run in parallel — it builds on T-205/T-304's existing apply/rollback and per-node timer
machinery, not on the spec or token work. T-1102 depends only on T-1101. T-1105/T-1106/T-1107 are
the outward-facing surfaces and depend on T-1101 (and, for 1105/1106, T-1104's tokens, since
external/unattended callers need token auth rather than a browser session).

Origin: `docs/roadmap-next.md`'s Phase 11 section (v1.7). This phase's exit demo: a PR to a git
repo holding the cluster spec → CI calls `vnproxctl apply --plan` → review shows the diff → merge
applies during the night window → morning dashboard shows the committed changeset and a clean
drift report.

---

## T-1101 · Declarative cluster network spec: export, import, reconcile
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-603, T-201, T-205 · **context:** `docs/features/blueprints.md`, `docs/data-model.md` §3 §4, `docs/api.md` (Blueprints, Changesets), `docs/architecture.md` §4, `docs/roadmap-next.md` Phase 11

**Objective:** One versionable YAML document capturing cluster-wide network intent — bridges,
bonds, VLANs, SDN, firewall, IPAM ranges — as blueprints v2, cluster-scoped (unlike v1's
per-node-selector parameterized template). Export renders it from live state; import diffs the
spec against live state and generates an ordinary changeset draft — it never applies directly.
Stable field ordering makes `git diff` on two exports of an unchanged cluster empty, which is the
property that makes this GitOps-viable at all.

**Deliverables:**
- New `internal/spec` package: `Export(inventory.Graph snapshot, sdn/firewall/ipam views) Spec`
  and `Import(Spec, live) ([]change.Op, notInSpec []Ref)` — the diff logic mirrors
  `blueprint.Instantiate`'s absent→create/divergent→update/matching→noop pattern (`docs/data-model.md`
  §4), extended to cluster-wide entities rather than one blueprint's node-selected set. `Spec` has
  no embedded timestamp (a churning field would defeat the git-diff property); callers get
  freshness from commit metadata, not the document.
- New dependency: a YAML library (e.g. `gopkg.in/yaml.v3`) — flag this in the report per
  CLAUDE.md's "no new major dependencies without a note"; not in `docs/development.md`'s locked
  table today. Serialize with fixed struct-tag field order, not map iteration, so two exports of
  identical state are byte-identical.
- `GET /spec` (netRead-gated): the exported YAML, `specVersion: 1` header field.
- `POST /spec/import` (netWrite-gated, `{content}`): parses, diffs against live, calls
  `change.Service.Create` with the resulting ops → returns the draft changeset (same shape as
  `POST /changesets`). Entities present in live but absent from the spec are reported (not
  deleted) as a distinct `notInSpec` list in the response — no implicit prune.
- `docs/data-model.md` new §5 documenting the `Spec` schema; `docs/api.md` documents both routes.

**Acceptance criteria:**
1. Export against `single-node`/`three-node-vlan`/`evpn-lab` produces valid `specVersion: 1` YAML
   (golden-file schema test per fixture).
2. Round-trip property: `export(live)` → `import(spec)` against the same fixture → the returned
   changeset has zero ops, for all three fixtures.
3. A hand-edited spec (new VLAN bridge; a diverging MTU) imported against `three-node-vlan`
   produces exactly the expected create/update ops (golden ops), and the resulting changeset's
   status is `draft`/`validated` — never `applying` or `committed`.
4. Two exports of an unchanged cluster are byte-identical (test runs export twice with Go map
   iteration randomization in play and diffs the bytes).
5. An entity removed from the spec but still present live surfaces in `notInSpec`, not as a
   delete op (golden test against `evpn-lab`).
6. `docs/api.md` + `docs/data-model.md` updated with the schema and both routes; `make check`
   green.

---

## T-1102 · Pinned-spec drift mode
**model:** sonnet-5 · **size:** M · **depends:** T-1101, T-305 · **context:** `docs/features/topology.md` §6, `docs/api.md` (`GET /drift`, `GET /findings`), `internal/drift`, T-1101's spec diff engine

**Objective:** The drift checker learns a second reference: diff live state against a *pinned*
spec, distinct from and additional to the existing five cross-node-consistency families
(`docs/features/topology.md` §6). This is the GitOps reconciler half — spec-drift findings are
detection-only with a human-confirm fix, exactly like every other drift finding; pinning is
app-owned data, not a shadow copy of PVE config (it's the declared desired state, same status as
"pin nodes to blueprint" already flagged as P1 in `docs/features/blueprints.md` §2).

**Deliverables:**
- New migration: a `pinned_spec` table (content, pinned_by, pinned_at) — app-owned per
  `docs/architecture.md` §7.
- `GET /spec/pin` (current pin + metadata), `POST /spec/pin` (`{content}`, netWrite+CSRF, audited
  `spec.pin`), `DELETE /spec/pin` (audited `spec.unpin`).
- `internal/drift` gains a `spec_drift` check family (source `drift`, `check: "spec_drift"`),
  computed each drift cycle by running T-1101's diff engine against the pinned spec — a distinct
  `check` value from the five cross-node families so consumers can filter GitOps-vs-cluster-drift
  independently (`docs/api.md`'s `GET /drift` finding shape documents the sixth `check` value).
- `fixable: true` on spec-drift findings: `POST /drift/{id}/fix` returns a draft changeset
  reconciling live state to the pinned spec (never auto-applied).

**Acceptance criteria:**
1. Pin a spec captured from `three-node-vlan` → `GET /drift` immediately after has no
   `spec_drift` findings.
2. Mutate live state (fixture-injected bridge MTU change) without touching the pin → next drift
   cycle raises exactly one `spec_drift` finding naming the entity, with a `check` value distinct
   from `bridge_divergence`/`mtu_consistency` even though the same entity could independently
   trigger those too.
3. `POST /drift/{id}/fix` on the `spec_drift` finding returns a draft changeset (golden ops)
   reconciling to the pinned spec; status is `draft`, not applied.
4. Unpinning clears all `spec_drift` findings on the next cycle; pin/unpin are audited
   (`spec.pin`/`spec.unpin`) with the acting user recorded.
5. Regression: the five existing cross-node drift families still fire independently against
   `messy-brownfield`, unaffected by pin state.
6. `docs/api.md` + `docs/features/topology.md` §6 updated; `make check` green.

---

## T-1103 · Scheduled changesets & maintenance windows
**model:** sonnet-5 · **size:** L · **depends:** T-205, T-304 · **context:** `docs/features/change-management.md` §4, `docs/architecture.md` §4, `docs/security.md` (Safety interlocks), `internal/change/localtimer.go`, `planning/tasks/phase-7.md` T-703 (`touchesMgmtPath`), `docs/api.md` (Changesets)

**Objective:** Stage now, apply inside a window, with the existing commit-confirm/rollback
machinery (T-205/T-304) making unattended apply safe. This card is a **review checkpoint**: its
safety-analysis section (below) must be present and cross-referenced by test names before the card
counts as done. `touchesMgmtPath` changesets are excluded from unattended apply, server-side,
unconditionally — there is no config flag or API parameter that overrides this, matching
`docs/security.md`'s "no override in UI" for the underlying T-203 interlock.

**Scope note (flagged):** the roadmap text says ack arrives via "webhook/UI/CLI", but neither
scoped automation tokens (T-1104) nor `vnproxctl`'s HTTP mode (T-1105) exist yet at this card's
declared dependencies (T-205, T-304 only) — and T-1104 does not depend on T-1103, so it cannot be
assumed either. This card therefore ships UI ack (existing session-cookie `POST
.../confirm`) plus a single-use, changeset-scoped signed callback token (HMAC over changeset id +
deadline, minted at schedule time, delivered once in the schedule response — same construction
style as the peer API's HMAC, not a general credential) as the webhook path. CLI ack is explicitly
deferred to T-1105.

**Deliverables:**
- New `changeset_schedules` table (changeset_id, window_start, window_end, confirm_timeout_sec,
  missed_window_policy, callback_token_hash, created_by, created_at, cancelled_at).
- `POST /changesets/{id}/schedule` (`{windowStart, windowEnd, confirmTimeoutSec?,
  missedWindowPolicy?}`, netWrite+CSRF): rejects `422 mgmt_path_unattended_forbidden` when
  `touchesMgmtPath` is true, `422` when the changeset carries error-severity findings or
  `windowStart >= windowEnd`. `DELETE .../schedule` cancels (audited `changeset.schedule_cancel`).
- A supervised scheduler goroutine (Go standards: owned, has a shutdown path) driven by an
  injected `Clock` interface (`Now()`) so window/deadline tests need no sleeps. At `windowStart`:
  re-validates (state may have moved since scheduling — abort + audit + finding on new blocking
  findings, mirroring `docs/features/change-management.md` §2's "runs again immediately before
  apply"), recomputes `touchesMgmtPath` (abort if now true), then applies and arms confirm with
  deadline `min(windowStart+confirmTimeoutSec, windowEnd)` using T-304's existing per-node local
  timer machinery unchanged — this card decides *when to start*, T-205/T-304 own everything after.
- Daemon-restart-safe: schedules persist and re-arm on startup, same pattern as T-304's
  confirm-deadline re-arm.
- `missedWindowPolicy`: `skip` (default — marks `schedule_missed`, audits, raises a finding) or
  `applyImmediately` (applies on restart with a fresh window, still `touchesMgmtPath`-checked).
- **Safety analysis** (required section in the card/report): (1) daemon down mid-window — no
  partial apply is possible since apply only starts at `windowStart`; missed-window policy governs
  on restart. (2) peer unreachable at deadline — T-304's per-node local timers mean each node
  rolls back independently on its own clock; no cross-node dependency for the rollback decision.
  (3) clock skew — deadlines are absolute unix timestamps computed once by the coordinator and
  pushed via the existing peer arm-timer call before any apply step begins (unchanged ordering
  from T-304); each node's own clock governs its own rollback. (4) spec/state changed between
  schedule and fire time — `touchesMgmtPath` and validation are recomputed at fire time, never
  trusted from schedule time.

**Acceptance criteria:**
1. Injected-clock test: schedule against `single-node` (window T+10..T+70, confirm 30s) →
   advancing to T+10 fires apply, confirm deadline lands at T+40; callback-token ack before T+40 →
   `committed`; no ack → rolled back at T+40, pre-state byte-identical.
2. Scheduling a `touchesMgmtPath` changeset against `three-node-vlan` → `422
   mgmt_path_unattended_forbidden`, changeset unchanged; no override path exists anywhere in the
   API or schema (documented, not just tested).
3. Restart-safety: schedule, reconstruct the scheduler from the same store (simulating restart),
   advance the clock past `windowStart` → apply still fires from the persisted row.
4. Missed-window: advance the fake clock past `windowEnd` before the scheduler ever runs → `skip`
   marks `schedule_missed`, audits, raises a finding; `applyImmediately` applies immediately with a
   freshly computed `touchesMgmtPath` check (table test, both policies).
5. `DELETE .../schedule` before `windowStart` prevents apply (audited `changeset.schedule_cancel`);
   scheduler skips the original firing time.
6. Re-validation at fire time: mutate `three-node-vlan` between schedule and `windowStart` to
   introduce a new blocking finding → apply aborts at fire time, no steps executed, audited.
7. `make check` green.

---

## T-1104 · Event stream & automation tokens
**model:** sonnet-5 · **size:** M · **depends:** T-105, T-206 · **context:** `docs/api.md` (WS `/api/ws`, `GET /audit`), `internal/auth/caps.go`, `docs/security.md` (Authentication/Authorization), `docs/architecture.md` §6

**Objective:** An authenticated WS + webhook firehose of audit, changeset lifecycle, drift, and
finding events, plus scoped API tokens for automation — vnprox-local and capability-scoped,
explicitly distinct from the PVE ticket bridge (`docs/security.md`: "No vnprox-local accounts" is
about *login*; a token is a delegated, scoped credential a logged-in user mints, not a second
authentication path).

**Deliverables:**
- New `api_tokens` table (id, name, token_hash, scopes_json, created_by, created_at,
  last_used_at, revoked_at). Scopes are drawn from the exact capability set `internal/auth/caps.go`
  already defines, plus a new `automation` scope gating the event/webhook routes below — no new
  privilege surface beyond that one addition.
- Bearer-token middleware (`Authorization: Bearer <token>`) alongside the existing session-cookie
  path, converging on the same capability-derived request context; bearer requests skip CSRF (not
  cookie-based) but stay rate-limited and are audited per use.
- `POST /tokens {name, scopes}` → one-time-reveal token value (same pattern as PVE API tokens);
  `GET /tokens` (list, no secret); `DELETE /tokens/{id}` (revoke). A token's scopes can never
  exceed the creating user's own derived capabilities at creation time. Audited (`token.create`/
  `token.revoke`).
- New WS topic `"events"` (`automation` scope required): a superset envelope over the existing
  `changeset.status`/`drift.changed`/`findings.changed` producers (reused, not duplicated) plus a
  new `audit.appended` event, all in the existing flat-JSON envelope convention.
- Webhook registration: `POST /webhooks {url, events, secret}`, `GET /webhooks`, `DELETE
  /webhooks/{id}` — vnproxd POSTs the same event envelope with an `X-VNPROX-SIGNATURE` HMAC-SHA256
  header (mirrors the peer API's HMAC construction), retried with backoff; N consecutive failures
  raise a `webhook_unhealthy` finding.
- `docs/api.md`: Tokens/Webhooks route tables, the `events` topic, `audit.appended` shape.
  `docs/security.md`: token model added to Authentication, explicitly distinguished from the PVE
  ticket bridge.

**Acceptance criteria:**
1. A token with `{netRead, automation}` scopes subscribes to `"events"` and reads `/topology`;
   a `POST /changesets` with the same token 403s (no `netWrite`).
2. A user without `sdnWrite` cannot mint a token scoped `sdnWrite` (400/403, table test).
3. E2E against `single-node`: apply a changeset over the normal API while a token-authed `"events"`
   subscriber observes `changeset.status` transitions then `audit.appended` for the same changeset,
   in order.
4. Webhook: register a target, trigger a drift finding → receives one signed POST; a tampered body
   fails signature verification (in-test).
5. Revoking a token rejects its next request (401) and force-closes its open WS subscriptions
   within one server tick.
6. A webhook target with N consecutive failures raises `webhook_unhealthy`; recovery clears it.
7. `docs/api.md` + `docs/security.md` updated; `make check` green.

---

## T-1105 · vnproxctl parity
**model:** sonnet-5 · **size:** M · **depends:** T-1101, T-1104 · **context:** `docs/api.md`, `cmd/vnproxctl/*.go` (existing daemon-independent commands), T-1104's token auth

**Objective:** CLI parity with the UI's read and changeset surfaces, plus `vnproxctl apply
spec.yaml --plan/--apply` for the GitOps flow. `-o json` everywhere; token-authed; CI-suitable
exit codes.

**Scope note (flagged):** `vnproxctl`'s existing commands (`status`, `snapshots`, `rollback-now`)
are deliberately daemon-independent, direct-SQLite disaster-recovery tools (per
`cmd/vnproxctl/main.go`'s own doc comment) — this is a real naming collision with a new
HTTP-backed `snapshots list`. This card must pick and document one resolution (e.g. the new
HTTP-backed commands live under a `vnproxctl remote <subcommand>` namespace, leaving the existing
top-level names untouched) rather than silently overloading a name whose current meaning is
"works when the daemon is down."

**Deliverables:**
- New HTTP-backed command family, namespaced per the scope note, requiring the daemon up and
  `--token <token>` / `VNPROX_TOKEN` (T-1104 bearer tokens only — no PVE username/password flow
  from the CLI).
- Read commands: topology, changesets (list/get/diff), findings, drift, audit — mirroring
  `docs/api.md`'s routes.
- Changeset lifecycle commands: create/validate/apply/confirm/rollback/discard, over the same
  routes T-205 already exposes.
- `vnproxctl apply spec.yaml --plan`: `POST /spec/import`, prints the diff, exits a documented
  "changes pending" code if non-empty, `0` if clean (Terraform-`plan`-style convention).
  `--apply`: additionally applies the resulting changeset, polls to `committed` within a
  `--timeout`, auto-confirms non-interactively; a changeset stuck past `--timeout` exits a distinct
  timeout code rather than hanging.
- `-o json` on every command in this family (and retrofitted onto the existing three commands,
  since the card says "every command").
- Documented exit-code table (success / usage / validation-or-plan-pending / auth / network)
  stable enough for CI to branch on.

**Acceptance criteria:**
1. `vnproxctl remote changesets list -o json` against a running daemon with a valid token returns
   JSON matching `GET /changesets`; with no token, fails fast with the documented auth exit code
   and no daemon call attempted.
2. `vnproxctl apply spec.yaml --plan` against `three-node-vlan` with a spec adding one bridge
   prints the diff and exits the "changes pending" code; a spec matching live exactly exits 0.
3. `vnproxctl apply spec.yaml --apply` end-to-end against `single-node`: create → apply → poll →
   auto-confirm → exit 0 on `committed`; a changeset stuck in `awaiting_confirm` past `--timeout`
   exits the timeout code (tested with a short timeout, no real sleeps).
4. Every new command supports `-o json` (table test asserting the flag is wired on each).
5. The naming-collision resolution is documented and the existing three commands' behavior/output
   is unchanged (regression test).
6. A revoked token gets the documented auth exit code on the next call.
7. `make check` green; report states any UI surfaces intentionally excluded from CLI parity.

---

## T-1106 · Terraform provider + Ansible collection
**model:** sonnet-5 · **size:** L · **depends:** T-1101, T-1104 · **context:** `docs/api.md`, `docs/architecture.md` §10 (changeset API declared stable at v1.7), T-1101's spec routes, T-1104's tokens

**Objective:** Thin shims over the changeset API — plan = validate+diff, apply = apply+confirm.
**This repo's deliverable is the stable API contract and a conformance test suite, not the
provider or collection source** — those are separate, publishable repos this card does not create.
State that boundary explicitly in the report.

**Deliverables:**
- Decide and document the drift-detection story external tooling needs for `terraform plan`
  (which route, what "no changes" looks like) — likely `POST /spec/import`'s empty-ops response or
  `GET /findings?source=drift`; the card must pick one and specify it, not leave it implicit.
- API-token auth (T-1104) as the only documented/supported auth path for external
  providers/collections — no PVE ticket flow exposed to them.
- A contract-test suite in this repo (e.g. `internal/apicontract/`) with golden request/response
  fixtures for every route external tooling depends on: token-authed changeset
  create→validate→apply→confirm, a validation-error path, an apply-then-rollback path, and
  `POST /spec/import` idempotency. This repo's `make check` runs them against `internal/pvemock`
  so a handler regression breaks CI here, not silently downstream in an external repo.
- `docs/api.md` new "Automation contract" section naming exactly the routes external
  providers/collections depend on (a minimal, intentional list) plus the stability/versioning note
  from `docs/architecture.md` §10.
- Explicit non-deliverable, stated in the report: no `terraform-provider-vnprox` or
  `ansible-collection-vnprox` source lands in this repo.

**Acceptance criteria:**
1. Contract suite covers the four flows above against both `single-node` and `three-node-vlan`.
2. Golden fixtures are generated from/verified against the real handlers (not hand-written
   independently) — a deliberate handler schema break fails the suite.
3. The "Automation contract" doc section is a reviewable, minimal checklist (names every
   contract-tested route and nothing else).
4. No provider/collection source exists in this repo (grep-verifiable); report names where the
   external repos are expected to live.
5. The `terraform plan` drift-detection route/semantics are specified in the doc, not implicit.
6. `make check` green.

---

## T-1107 · Blueprint sharing bundles
**model:** sonnet-5 · **size:** M · **depends:** T-1101 · **context:** `docs/features/blueprints.md`, `docs/data-model.md` §4, `docs/security.md` (key handling conventions), `docs/api.md` (Blueprints)

**Objective:** Signed, parameterized blueprint bundles exportable/importable across
installations — the community layer on top of blueprints v2. Signature verification on import
with a clear trust UX; unsigned imports allowed only behind an explicit "I trust this file" step,
never silently.

**Deliverables:**
- Bundle envelope wrapping an existing `Blueprint` (`docs/data-model.md` §4):
  `{bundleVersion: 1, blueprint: Blueprint, signature?: {alg, publicKeyFingerprint, sig}}`.
- Ed25519 signing key generated at first use (`/etc/vnprox/keys/blueprint-signing.key`,
  root:root 0600 — same handling convention as `docs/security.md`'s session key), public key
  exportable so a receiving admin can pin it.
- `GET /blueprints/{id}/bundle?sign=` produces the (optionally signed) bundle for download.
- `POST /blueprints/import`: verifies any signature against a trust store
  (`/etc/vnprox/keys/trusted-signers/`, admin-managed). An unsigned bundle, or one signed by an
  untrusted key, is not imported by default — the response reports a distinct status
  (`unsigned`/`untrustedSignature`) and import proceeds only when the request explicitly sets
  `{trustUnsigned: true}` or `{trustNewKey: true}`. Both paths audited as `blueprint.import` with
  the trust decision recorded.
- `GET/POST/DELETE /blueprint-signers`: manage trusted public keys (netWrite+CSRF, audited).
- UI: import dialog surfaces signature status (verified/trusted, signed-but-untrusted,
  unsigned) with the explicit trust step gating the import action; Vitest + Testing Library
  covers all three states.
- `docs/features/blueprints.md` new §5 (bundle format, signature algorithm, trust-store
  location); `docs/api.md` documents the new routes.

**Acceptance criteria:**
1. A signed export's signature verifies against its own exported public key (round-trip test).
2. Import from an already-trusted signer imports immediately, no prompt, audited `trusted: true`.
3. Import from an unknown signer is rejected without `{trustNewKey: true}`; succeeds with it, or
   after the key is separately added via `POST /blueprint-signers` — both paths tested and
   audited.
4. Import of an unsigned bundle is rejected by default; succeeds only with
   `{trustUnsigned: true}`, audited distinctly from a verified-signature import.
5. Tampering bundle content after signing invalidates the signature (`untrustedSignature`/
   `invalidSignature`, distinct from `unsigned`) — negative test.
6. Frontend test covers all three dialog states and that the trust step is required before the
   import action enables for the latter two.
7. `docs/features/blueprints.md` + `docs/api.md` updated; `make check` green.
