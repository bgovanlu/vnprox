# Phase 6 — Operations & 1.0

Goal: live traffic on the map, the health stream, blueprints, hardening, packaging, release. Milestone: **v1.0**.

---

## T-601 · Metrics: live rates, map traffic mode, history
**model:** sonnet-5 · **size:** M · **depends:** T-303 · **context:** `docs/features/monitoring.md` §1–2 (the spec), `docs/api.md` (metrics, WS)

**Objective:** The monitoring surface: 5s sampling, WS live rates, traffic-painted map, 24h history.

**Deliverables:** `internal/metrics`: sampler (local + peers), daemon-side rate computation, 24h ring persistence with 30s downsampling + pruning (T-003 table), per-slave bond breakdown; `GET /metrics/live|history` + `metrics.sample` WS per API doc (subscription-scoped — only subscribed refs stream); UI: inspector sparklines + counters (rx/tx bps/pps, errors, drops), history charts (rate, errors over time — recharts), map "traffic" paint mode (edge thickness/heat by utilization % of link speed) per spec, bond member balance view; guest netin/netout from PVE for top talkers (P1, per spec §3: per-bridge guest ranking, 5m/1h windows).

**Acceptance criteria:**
1. Fixture counter progression → correct rates (incl. counter-wrap handling, table-tested) and correct utilization % against link speed.
2. Traffic mode: fixture with one hot bond → visibly distinct heat; slave imbalance visible in the bond view.
3. History: 24h retention verified in a time-travel test; downsampling correct (golden).
4. WS streams only subscribed refs; 200 subscribed entities sustained without backpressure collapse (load test).
5. DB growth bounded: 24h at scale-target entity count stays under a documented size ceiling (measured in report).

---

## T-602 · Health checks & findings stream
**model:** sonnet-5 · **size:** M · **depends:** T-305, T-601 · **context:** `docs/features/monitoring.md` §5 (the spec — check list is binding)

**Objective:** One findings stream unifying drift, LLDP mismatch, IPAM conflicts, and the monitoring-driven checks.

**Deliverables:** Check engine running every documented §5 check (error/drop thresholds, bond slave down, MTU path mismatch, STP topology bursts, bridge without carrier uplink, dnsmasq/frr service down, stale `interfaces.new`); unified findings model (merging the T-305/T-302/T-405 producers — refactor them onto one interface if they diverged), severity + plain-English explanation + affected refs + remediation (fixing changeset where computable, docs link otherwise) per spec; findings UI finalized (stream view, map badges, nav count) on the T-305 component; PVE notification-system hooks (P1: webhook/email on severity ≥ threshold, using PVE's notification targets).

**Acceptance criteria:**
1. Every §5 check has a triggering fixture and a golden finding (explanation strings reviewed for the plain-English bar).
2. All finding producers emit through one stream: filter by source/severity/node works across drift+lldp+ipam+health uniformly.
3. Threshold checks don't flap on noisy fixtures (hysteresis verified).
4. A computable remediation (bond slave down → no; MTU mismatch → yes) round-trips: finding → fixing changeset → applied → finding clears.
5. Notification hook fires once per finding transition (not per cycle) on a threshold fixture.

---

## T-603 · Blueprints
**model:** sonnet-5 · **size:** L · **depends:** T-205 (T-402 for SDN entities in starters) · **context:** `docs/features/blueprints.md` §1–2 (the spec)

**Objective:** Parameterized topology templates: author, capture, instantiate idempotently, plus the five starters.

**Deliverables:** Versioned blueprint JSON format (`blueprintVersion: 1`, entities + `{{param}}` placeholders + node-expansion selectors per spec) with schema validation; instantiation engine: params → expansion → **idempotent** diff against inventory (matching entities skipped, divergent → update ops, absent → create ops, each labeled in the changeset diff per spec) → draft; capture-from-node ("blueprint-ify", parameterizing addresses per spec); the five documented starters as bundled read-only blueprints, each with description + preview diagram (rendered from its entities via the wizard-preview machinery); UI: blueprint list/detail/param form (IPAM-aware address suggestions via T-405's picker), import/export files; `GET/POST /blueprints`, instantiate endpoint per API doc.

**Acceptance criteria:**
1. Each starter instantiates against a bare fixture → golden changeset; against an already-conforming fixture → zero-op changeset (idempotency both ways).
2. Divergent re-instantiation produces update-labeled ops only for the divergent fields (golden).
3. Capture of the three-node fixture's node → re-instantiating it on a bare node yields an equivalent network (round-trip test at inventory level).
4. Param validation: bad CIDR/VID rejected at the form; address params get next-free suggestions.
5. Export → import → instantiate on a second test daemon works (file portability).

---

## T-604 · Security hardening pass
**model:** sonnet-5 · **size:** M · **depends:** T-301 (touches everything; run late, worktree-isolate) · **context:** `docs/security.md` (every section is a checklist item here)

**Objective:** Verify and tighten every documented security property; reduce privileges where possible.

**Deliverables:** Systematic audit: each security-doc claim gets an automated test or a checked-and-documented manual verification (auth flows, cookie/CSRF properties, header set, peer-API rejection matrix, rate limits, encrypted-at-rest, audit append-only, interlocks) — a `docs/security-verification.md` mapping claim → evidence; privilege reduction attempt: capability-bounded systemd unit (which caps ifreload/netlink actually need — measure, apply, document; if root remains required for specific ops, isolate them behind an internal boundary and document why); dependency audit (`govulncheck`, `npm audit`) clean or triaged; fuzz targets inventory extended (auth token parse, peer HMAC, interfaces parser, lldp/frr/lease parsers — all fuzzed in CI at least briefly); secrets handling sweep (no secrets in logs — grep-audit test on log output of a full E2E run); CSP tightened to the minimum that still works (verified by the Playwright suite).

**Acceptance criteria:**
1. `security-verification.md` covers 100% of security-doc claims, each with evidence link (test name or procedure).
2. Unit file drops at least: `ProtectKernelModules`, `ProtectKernelTunables` (except needed sysctls), `RestrictAddressFamilies` scoped, `SystemCallFilter` profile applied — or each exception documented with the syscall/capability that forced it.
3. Zero high/critical findings from govulncheck + npm audit (or pinned-with-justification list).
4. Log-secrecy test passes (full E2E run's logs contain no ticket/secret/password material — automated grep with allowlist).
5. All fuzz targets run in CI (short) and a 10-min soak locally, clean (report the soak).

---

## T-605 · Onboarding walkthrough & config doc export
**model:** sonnet-5 · **size:** M · **depends:** T-107 (walkthrough final also wants T-302/T-305 present) · **context:** `docs/features/blueprints.md` §3–4 (the spec), `docs/user-guide.md` §1

**Objective:** The first-login experience per the user guide, and the as-built documentation export.

**Deliverables:** First-login walkthrough exactly per user-guide §1 (found-summary, protected-interface confirmation writing `protected.json` via T-203's API, LLDP offer, health findings review), dismissible/resumable, never blocks navigation; read-only-mode UX polish (every write affordance disabled-with-tooltip — audit sweep of all UI from prior tasks, fix stragglers); config doc export per spec §4: Markdown + standalone HTML with embedded topology SVG, per-node interface tables, VLAN matrix, SDN inventory, firewall summaries, LLDP wiring table, timestamp; export endpoint + UI (Tools → Export documentation).

**Acceptance criteria:**
1. Fresh-DB first login on the brownfield fixture runs the full walkthrough; `protected.json` written with confirmed interfaces; skipping and resuming works.
2. Walkthrough correctly detects and pre-fills management + corosync interfaces from the fixture.
3. Export golden test on the three-node fixture: Markdown structure + every documented section present; HTML renders standalone (no external requests — CSP-style check); SVG topology matches the map.
4. Read-only sweep: automated crawl (Playwright) in read-only mode finds zero enabled mutating controls.

---

## T-606 · Packaging final: installer, apt repo, upgrades
**model:** sonnet-5 · **size:** M · **depends:** T-006 (finalize after features stabilize) · **context:** `docs/deployment.md` (the spec — every documented behavior ships here)

**Objective:** Production-grade install/upgrade/uninstall exactly as the deployment guide documents.

**Deliverables:** `install.sh` completed: cluster detection + node-list rollout via SSH (or per-node instructions fallback), PVE token creation (`vnprox@pve!daemon` with auditor role — create the role if absent), cluster secret bootstrap, lldpd option, port-conflict flow, first-login checklist output; `vnprox-setup` interactive + `--answers` non-interactive parity; apt repository tooling (`release.yml` publishes signed packages; repo layout + signing key docs); upgrade path: migration tests from each prior tagged schema, mixed-version refusal behavior verified in the multi-daemon harness; uninstall/purge per doc incl. last-node cleanup prompt; `vnproxctl` completed (`status` rich output per doc, snapshots, rollback-now — verifying T-206's daemon-independent restore); container-based test matrix in CI: install/upgrade/remove/purge on Debian 12 + 13 images.

**Acceptance criteria:**
1. CI matrix green: fresh install → serve → upgrade from previous tag → still serves with data intact → remove/purge semantics per doc, on both Debian bases.
2. Cluster install simulation (3 containers, SSH between them): one-command rollout completes; same-port enforcement works; secret replicated (simulated pmxcfs path).
3. Port-conflict path: PBS package present → prompt → alternate port → all nodes configured consistently.
4. `--answers` file produces identical results to the interactive flow (diff of resulting system state).
5. Token/role creation idempotent (re-run installer → no duplicates, no errors).

---

## T-607 · Performance, E2E suite, release
**model:** sonnet-5 · **size:** M · **depends:** T-601, T-604 (the release gate — runs last) · **context:** `docs/features/topology.md` §4 (scale targets), `docs/development.md` (Playwright, CI), `docs/roadmap.md` (v1.0)

**Objective:** Prove the scale targets, ship the E2E suite, cut v1.0.

**Deliverables:** Scale fixture (`scale-lab.yaml`: 8 nodes × 6 NICs, 4 bridges/node, 300 guests, 40 VNets per topology spec §4) + performance harness: API latencies (topology, simulate, validate) and frontend metrics (initial render, pan/zoom frame times via Playwright tracing) measured and recorded in `docs/performance.md` with pass/fail against targets; progressive-disclosure behavior verified at scale (collapse defaults, filter prompt at the element cap); Playwright E2E suite covering the user-guide §3 task table end-to-end against pvemock (each documented common task is a test); soak test: 24h daemon run against a churn fixture (scripted state changes) — memory/goroutine/DB-growth flat; release checklist executed: docs freeze pass (every doc reflects shipped behavior — discrepancy list resolved or documented), CHANGELOG, version stamping, `release.yml` dry run, tag v1.0.0.

**Acceptance criteria:**
1. Every topology-spec §4 target met and recorded (or a flagged, justified exception with a follow-up issue).
2. E2E suite: all user-guide §3 tasks pass in CI (<15 min total).
3. Soak: RSS and goroutine count flat over 24h (graphs in report); DB within T-601's ceiling.
4. Docs-vs-behavior audit produces zero unresolved discrepancies.
5. `make deb` artifact from the release workflow installs and passes the T-606 matrix — the actual v1.0 candidate.
