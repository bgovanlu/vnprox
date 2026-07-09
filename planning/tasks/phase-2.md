# Phase 2 — Change engine & core editing

Goal: safe writes. The changeset lifecycle end-to-end, then the editors that feed it. Milestone: **v0.5 beta**. T-205 is the most safety-critical task in the project.

---

## T-201 · Changeset model, store, draft API
**model:** sonnet-5 · **size:** M · **depends:** T-103, T-003 · **context:** `docs/data-model.md` §3 (op vocabulary), `docs/api.md` (changesets), `docs/features/change-management.md` §1

**Objective:** Changesets as data: the op type system, persistence, and draft CRUD API.

**Deliverables:** `internal/change`: `Op` tagged union covering the full documented v1 vocabulary (typed params per op, JSON (de)serialization with strict unknown-field rejection); changeset aggregate with the documented status machine (illegal transitions return typed errors); store wiring (T-003 changesets table); API: list/create/get/update-draft/delete-draft per API doc (validate/apply/etc. registered but `501` until T-202/T-205); WS `changeset.status` events on every transition; audit entries on create/discard.

**Acceptance criteria:**
1. Every documented op round-trips JSON; unknown op type or unknown param field → 400 `validation_failed` with the offending path.
2. Status machine table-tested: every (state × action) pair asserted allowed/denied.
3. Draft CRUD API integration-tested incl. capability gating (netWrite required to create).
4. Two parked drafts by different users coexist and list correctly.

---

## T-202 · Validation framework + schema/referential validators
**model:** sonnet-5 · **size:** L · **depends:** T-201 · **context:** `docs/features/change-management.md` §2 (the spec), `docs/api.md` (finding shape)

**Objective:** The layered validator pipeline and its first two classes, with machine-applicable fixes.

**Deliverables:** Validator framework: ordered classes, findings per the API shape (severity/code/message/ref/fix), short-circuit on schema errors, validation against an inventory snapshot (injected — enables pure tests); schema validators for every op (ranges/enums/syntax per spec); referential validators per spec (existence, duplicate enslavement, name collisions, VID overlap, address overlap) evaluated against the snapshot *plus* earlier ops in the same changeset (an op may reference an entity a prior op creates); `fix` patches where computable (e.g. MTU clamp suggestion); `POST /changesets/{id}/validate` + auto-validation on draft mutation; advisory-class validators from spec item 5.

**Acceptance criteria:**
1. Golden validation suites: ≥40 table cases across ops covering each validator with exact expected findings (code + ref).
2. Intra-changeset references validate (create bond0 then enslave to it in one changeset → no error; reversed order → error).
3. Every emitted `fix` patch, when applied to the draft, revalidates clean (property test over the fix corpus).
4. Validation of a 100-op changeset completes <200ms (benchmark).

---

## T-203 · Safety interlocks & protected interfaces
**model:** sonnet-5 · **size:** S · **depends:** T-202 · **context:** `docs/security.md` (Safety interlocks), `docs/features/blueprints.md` §3 (onboarding seed), `docs/features/change-management.md` §2 class 3

**Objective:** The hard-error validator class that keeps users from sawing off the branch: management IP, corosync links, guest-bearing bridge deletion.

**Deliverables:** Detection of protected interfaces per node (management IP from PVE node config + corosync links from `/etc/pve/corosync.conf`), persisted/overridden via `/etc/pve/vnprox/protected.json` (the onboarding-confirmed set; minimal API to read/update it, gated on netWrite, audited); interlock validators: any op chain whose net effect removes/re-addresses a protected interface's connectivity or deletes/detaches its bridge path → error `protected_interface`; bridge deletion with attached running guests → error with the attachment list unless the same changeset reattaches every one; `allow_dangerous_ops` config flag downgrade-to-warning behavior (flag read at validation time, its use audited).

**Acceptance criteria:**
1. Table tests: deleting vmbr0 carrying the mgmt IP → error; re-addressing it → error; deleting an unrelated bridge → no interlock finding.
2. Chain analysis: moving the mgmt IP to a new bridge *and* deleting the old one in one changeset validates clean (connectivity preserved).
3. Guest-bearing bridge deletion errors list every attached guest; adding reattach ops for all of them clears the error.
4. With `allow_dangerous_ops=true`, the same cases are warnings and an audit entry records the override.

---

## T-204 · interfaces(5) writer & differ
**model:** sonnet-5 · **size:** M · **depends:** T-102 · **context:** `docs/data-model.md` §3 (op→file semantics), `docs/features/change-management.md` §3

**Objective:** Turn ops into minimal edits of the interfaces AST, and render human-reviewable diffs.

**Deliverables:** In `internal/host` (or `internal/change/ifaces`): AST mutators for every node-file-affecting op (bridge/bond/vlan/iface groups) producing **minimal** edits (untouched stanzas byte-identical, comments preserved, vnprox-created stanzas carry a `# managed by vnprox (changeset <id>)` comment); deterministic stanza ordering rules for new entities; unified-diff renderer (per file, per node) + structured op summaries for the API `diff` endpoint shape; `GET /changesets/{id}/diff` wired (plan tab arrives with T-205).

**Acceptance criteria:**
1. Golden tests: each op type applied to each relevant corpus file (T-102's testdata) produces exactly the expected file (byte-level golden files).
2. Property test: apply op → parse result → inventory-level effect matches the op's intent, for randomized valid ops.
3. Untouched-stanza byte-identity asserted across the corpus.
4. Diff endpoint returns correct unified diffs for a multi-node draft (three-node fixture).

---

## T-205 · Apply engine: planner, executor, commit-confirm, rollback ★
**model:** **strong (Opus/Fable-class)** · **size:** L · **depends:** T-203, T-204 · **context:** `docs/architecture.md` §4 (the spec), `docs/features/change-management.md` §4, `docs/api.md` (apply/confirm/rollback), `docs/data-model.md` (plan/apply_log/snapshots)

**Objective:** The safety core: ordered plans, execution against PVE, the commit-confirm window with daemon-side auto-rollback, and failure recovery. **Nothing here may be approximated.** Single-node scope (multi-node local timers are T-304; this task must still structure per-node steps for it).

**Deliverables:** Planner: ops → ordered typed steps per architecture §4 (cluster-API steps, per-node stage-file steps, per-node ifreload steps, sdn.apply last), rendered into `plan_json` pre-apply; executor: pre-state snapshot (files per data model) → step execution with per-step logging (`apply_log_json`), PVE steps via the *user's* ticket, file steps via host writer + PVE network reload; commit-confirm: deadline persisted (`confirm_deadline`), timer armed daemon-side, **re-armed from DB on daemon restart**; confirm/rollback endpoints; auto- and manual rollback: restore pre-snapshot files + inverse API steps in reverse order + reload, attributed `system:rollback`; cluster-wide apply lock (advisory single-applier); mid-apply step failure → abort remaining, roll back completed; `RefreshNow` after every terminal state; audit everything.

**Acceptance criteria:**
1. End-to-end against pvemock `single-node`: draft → validate → diff → apply → WS status stream (`applying`→`awaiting_confirm`) → confirm → `committed`; fixture state verifiably changed.
2. No-confirm path: deadline passes → state restored byte-identically to pre-snapshot, status `rolled_back`, audit trail complete. **Timer survives restart:** kill the daemon mid-window, restart, rollback still fires on schedule.
3. Injected step failure (pvemock failure flag) at each step position of a 5-step plan → completed steps rolled back, changeset `failed`, apply log pinpoints the step; system converges to pre-state (property: post-rollback inventory == pre-apply inventory) for every position.
4. Second apply while one is `applying`/`awaiting_confirm` → 409 `changeset_locked`.
5. Rollback of a committed changeset within retention creates a correct restoring draft (diff shown is the inverse).
6. ≥90% coverage in `internal/change`; race detector clean; a written test-plan section in the report enumerating what was and wasn't provable against mocks (honesty contract — the reviewer needs the residual-risk list for hardware validation).

---

## T-206 · Snapshots, time machine, audit UI
**model:** sonnet-5 · **size:** M · **depends:** T-205 · **context:** `docs/api.md` (snapshots), `docs/features/change-management.md` §4 §8, `docs/user-guide.md` §3

**Objective:** History as a product surface: snapshot browsing/diff/restore and the audit view.

**Deliverables:** Snapshot API per doc (list paginated, manual create, detail, diff between any two or vs. live, restore→draft); zstd blob storage with hash dedup (identical file content stored once); retention job (config: keep N days, default 90, committed-changeset snapshots pinned 7d minimum per spec); History UI: snapshot timeline grouped by changeset, side-by-side/unified diff viewer, restore flow into the changeset drawer; Audit UI per spec §8 (merged view is single-node until T-303 — design the query for fan-out); `vnproxctl snapshots list|restore` + `rollback-now` subcommands (local, root-only, the documented recovery path).

**Acceptance criteria:**
1. Apply 3 changesets against pvemock → timeline shows pre/post snapshots; diff between any two renders correctly; `to=live` reflects current state.
2. Restore of snapshot N-2 produces a draft whose diff exactly reverses the two later changesets (golden).
3. Dedup verified (same file across snapshots → one blob row); retention prunes per policy in a time-travel test.
4. `vnproxctl snapshots restore` works with the daemon stopped except for its DB (document the mechanism: direct DB + file write + ifreload exec) — this is the disaster path and must not depend on the HTTP API.
5. Audit UI filters (user/date/result/target) work; every T-205 lifecycle event appears.

---

## T-207 · Editing UI: changeset drawer, entity editors, guest NICs
**model:** sonnet-5 · **size:** L · **depends:** T-107, T-201 (uses T-202/T-205 as available) · **context:** `docs/features/change-management.md` §1 §5 §6 (the spec), `docs/user-guide.md` §3

**Objective:** The write-side UX: the drawer, the four entity editors, map drag-edits, guest NIC ops with bulk mode, and the apply/confirm flow.

**Deliverables:** ChangesetDrawer per spec §1 (accumulate, reorder, remove, park/resume named drafts, live validation badges); Review & apply screen: Summary/File diff/Plan tabs, warnings-checkbox gate, apply → countdown banner (WS-driven, survives page reload) → confirm/rollback; editors per spec §5 with inline plain-English help (bridge incl. VLAN-aware VID editor + delete-with-reattach flow; bond incl. candidate link states + post-apply LACP partner view; VLAN; interface); map drag-drop edits per topology spec §2 (NIC→bond/bridge, guest-NIC edge retarget) generating drafted ops with snap-back on validation failure; guest NIC list view + bulk reattach per spec §6; all mutations exclusively through changeset APIs.

**Acceptance criteria:**
1. Scripted walkthrough against pvemock `three-node-vlan` (documented, Playwright where feasible): create bond from two NICs via drag → edit bridge to VLAN-aware → bulk-move 5 guests → review (all three tabs correct) → apply → countdown → confirm.
2. Validation errors render on the drawer line *and* the originating form field; a `fix` patch offers one-click apply.
3. Countdown banner survives reload mid-window (state from `/changesets/{id}` + WS resubscribe); rollback outcome renders clearly on reconnect.
4. Read-only-capability user sees disabled editing affordances with explanatory tooltips everywhere (spot-check matrix).
5. Vitest on drawer state machine + op-summary rendering; `tsc`/eslint clean.

---

## T-208 · Raw interfaces editor
**model:** sonnet-5 · **size:** S · **depends:** T-207, T-204 · **context:** `docs/features/change-management.md` §7

**Objective:** The power-user escape hatch that stays inside the safety envelope.

**Deliverables:** Monaco-based editor (lazy-loaded chunk) per node: current file, interfaces(5) syntax highlighting (define a Monarch grammar) + linting via the T-102 parser surfaced as editor markers; save → changeset with single `iface.raw.replace` op (add op type: params = node + full content; differ shows full-file diff; validators run against the parsed result — reuse T-202 pipeline on the parsed entity delta); conflict guard: file hash captured at open, mismatch on save → reload prompt.

**Acceptance criteria:**
1. Syntax errors underline with line-precise messages as you type (parser in a worker or debounced server round-trip — implementer's choice, latency <300ms).
2. Saving a file that deletes the management bridge → interlock error surfaces in the editor flow (T-203 integration).
3. Hash-conflict path verified (edit fixture file server-side mid-session).
4. Monaco loads only when the editor opens (bundle-size assertion in build).
