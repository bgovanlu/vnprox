# Phase 10 — Flows & observability

Theme: *from counters to conversations.* v1 shows throughput (who is busy); this phase shows
flows (who talks to whom) and pushes vnprox's signals into the observability stack the rest of
the infrastructure already uses. Every task here is read/observe/notify — nothing in this phase
adds a new write surface, so `internal/change` is untouched by every card below. T-1002 is the
long pole: it is the dependency for both the map-painting work (T-1003) and host-local sampling
(T-1004), so land it first; T-1001, T-1005, and T-1006 are independent of T-1002 and of each
other and can run in parallel once their own dependencies (already shipped in Phases 6/5) are
available. Per `docs/roadmap-next.md`'s carried-forward invariants: vnprox never becomes a
long-term flow/metric warehouse — every storage deliverable below states its bound (window +
hard cap) and its prune path; export to Prometheus/a real TSDB is the answer for anything longer.

---

## T-1001 · Prometheus exporter
**model:** sonnet-5 · **size:** S · **depends:** T-601, T-602 · **context:** `docs/features/monitoring.md` §1 §2 §4 §5, `docs/security.md` (Authentication, Authorization, Transport), `docs/api.md` (Auth, Metrics), `internal/metrics/sampler.go`, `internal/findings/engine.go`

**Objective:** `GET /metrics` in Prometheus text exposition format, exporting data the sampler and
findings engine already compute — no new collection logic, this is an export surface only, per
`docs/features/monitoring.md` §4's P2 note (promoted here). Because Prometheus scrapers cannot
carry a session cookie or CSRF header, this route needs its own auth story that doesn't weaken
`docs/security.md`'s model.

**Deliverables:**
- `internal/api/metrics_exporter.go`: `GET /metrics` renders `internal/metrics.Sampler`'s current
  per-ref counters (not pre-computed rates — Prometheus does its own `rate()`), `internal/findings`'s
  open-finding counts by severity, `internal/drift`'s open-finding count, and current changeset
  counts by status, as OpenMetrics-compatible text.
- Metric names (documented as a table in `docs/api.md`'s new Metrics-exporter subsection):
  `vnprox_iface_rx_bytes_total`, `vnprox_iface_tx_bytes_total`, `vnprox_iface_rx_packets_total`,
  `vnprox_iface_tx_packets_total`, `vnprox_iface_rx_errors_total`, `vnprox_iface_tx_errors_total`,
  `vnprox_iface_rx_dropped_total`, `vnprox_iface_tx_dropped_total` (all labeled `ref`, `node`,
  `kind`); `vnprox_findings_open{severity="error|warning|info"}` (gauge); `vnprox_drift_open`
  (gauge); `vnprox_changesets{status="draft|applying|awaiting_confirm|committed|rolled_back|failed"}`
  (gauge); `vnprox_build_info{version}` (gauge, always `1`).
- Auth: a new **scrape token** (random 256-bit value, generated at install alongside the session
  key per `docs/security.md`'s Authentication section, stored `root:root 0600` at
  `/etc/vnprox/keys/metrics.key`), checked via `Authorization: Bearer <token>` with
  `crypto/subtle.ConstantTimeCompare` — no PVE ticket, no session cookie, so scraping doesn't
  require a logged-in browser. Additive optional CIDR allowlist (`[metrics] allow_from` in
  `vnprox.toml`, same shape as an nginx-style allowlist) checked before the token. Both gates are
  documented as new subsections in `docs/security.md`'s Authentication/Transport sections
  (flagged as this task's addition, not a silent change) and `docs/api.md`'s Auth section.
- `GET /config`'s Settings-page payload gains a `metricsEnabled` bool (no secret exposed, matching
  the existing "deliberately excludes every secret" contract).

**Acceptance criteria:**
1. `GET /metrics` with a valid `Authorization: Bearer` token against the `three-node-vlan` fixture
   returns 200 text/plain with at least one `vnprox_iface_rx_bytes_total` series per observed NIC
   and correct `vnprox_findings_open` counts matching a concurrently-seeded findings fixture; a
   missing/invalid token returns 401 with the standard `{"error":...}` envelope (this route is the
   one exception to the cookie/CSRF convention, so its error path is tested explicitly).
2. `allow_from` configured to exclude the test client's source IP returns 403 even with a valid
   token; unset `allow_from` (default) allows any source.
3. Output is valid OpenMetrics text (parsed back with `prometheus/common/expfmt` in the test only —
   not a runtime dependency of `vnproxd` itself, so this does not count as a new production
   third-party dependency).
4. `docs/api.md` and `docs/security.md` updated with the new route, metric table, and auth story;
   `make check` green.

---

## T-1002 · Flow ingestion engine
**model:** sonnet-5 · **size:** L · **depends:** T-303, T-003 · **context:** `docs/architecture.md` §3 §5 §7, `docs/data-model.md` §2, `docs/api.md` (Peer API, WebSocket), `docs/features/monitoring.md` §3, `internal/peer/`, `internal/store/`

**Objective:** Ingest sFlow v5, NetFlow v5/v9, and IPFIX into a bounded, node-local ring store,
with cluster fan-in so any node's UI can query flows observed anywhere in the cluster — the same
peer-API pattern `internal/collect`'s host poller and `GET /audit`/`GET /snapshots` already use.
This is the foundation T-1003 (map painting) and T-1004 (host-local sampling) both build on.

**Deliverables:**
- `internal/flow/` package: `internal/flow/sflow.go`, `netflow5.go`, `netflow9.go`, `ipfix.go` —
  **stdlib-only wire decoding** (`encoding/binary` over a `net.UDPConn`, no third-party flow
  library) per CLAUDE.md's dependency rule; NetFlow v9/IPFIX are template-based, so the decoder
  maintains a per-exporter, per-source-id template cache (evicted on exporter timeout) exactly
  like a real collector must. Each decoder normalizes into one `flow.Record`:
  `{at, node, srcIp, dstIp, srcPort, dstPort, proto, bytes, packets, vlan?, srcRef?, dstRef?,
  ingressIfIndex?, egressIfIndex?, source: "sflow"|"netflow5"|"netflow9"|"ipfix"}` — `srcRef`/
  `dstRef` are resolved against the inventory graph (guest NIC / bridge Ref) where the IP matches
  a known guest or subnet, left unset otherwise (never guessed).
- `internal/flow/listener.go`: per-node UDP listeners (configurable ports, default 6343/2055/4739),
  off by default, opt-in per node (`[flows] sflow_enabled`/`netflow_enabled`/`ipfix_enabled` in
  `vnprox.toml`) — matches T-1004's opt-in convention.
- `internal/flow/store.go`: bounded ring store in SQLite (`flow_samples` table, new migration
  `internal/store/migrations/NNNN_flows.sql`), same retention philosophy as `metric_samples`
  (§2 of `docs/data-model.md`): a configurable window (`[flows] retention_minutes`, default 60)
  **and** a hard row cap (default 2,000,000 rows) — whichever is smaller prunes first, on the same
  tick cadence pattern the metrics ring uses. No long-term warehouse: this is explicit in the
  package doc comment.
- `GET /flows?guest=&vlan=&subnet=&port=&protocol=&fromTs=&toTs=&limit=&cursor=` (documented in a
  new `docs/api.md` Flows section, additive): paginated query over the local node's ring, every
  filter optional and ANDed (mirrors `GET /audit`'s convention — an unrecognized filter value
  matches nothing, never a 400).
- Cluster fan-in: `GET /api/peer/flows` (same query params, peer-local-only response, no
  `partial`/`failedNodes`) added to the Peer API table in `docs/api.md`; `GET /flows` fans out to
  every reachable peer and merges, following the `GET /audit`/`GET /snapshots` `partial`/
  `failedNodes` envelope exactly.
- WS event `flow.batch` (`{entries: [flow.Record], droppedTotal}`), same rate-capped/append
  contract as `firewall.log.batch`; new subscribe topic `"flows"`.
- Testdata: `testdata/flows/` — captured/hand-built raw UDP datagram fixtures per protocol
  (`sflow5_basic.bin`, `netflow5_basic.bin`, `netflow9_template.bin` + `netflow9_data.bin` — v9
  needs a template before a data record decodes, so these are a paired sequence — `ipfix_basic.bin`),
  plus truncated/malformed variants per decoder for defensive-skip tests (mirrors
  `internal/fwlog.ParseAll`'s and `internal/host.ParseDHCPLeases`'s "skip and count, never fail
  the whole read" convention). A `testdata/clusters/flow-lab.yaml` pvemock fixture adds guest
  NICs/subnets the fixtures' IPs resolve against for `srcRef`/`dstRef` end-to-end tests.

**Acceptance criteria:**
1. Golden decode tests: each `testdata/flows/*.bin` fixture decodes to an exact expected
   `[]flow.Record` (table-driven, one test per protocol); the v9 pair decodes only after the
   template datagram is fed first, and a data datagram fed with no prior template is dropped
   (counted, not errored) with a logged reason.
2. Malformed/truncated fixtures never panic and never block the listener goroutine (fuzz-style
   loop over all fixtures with random truncation, ≥1000 iterations, matching the CI `FuzzParse`
   convention's spirit).
3. `GET /flows` against `flow-lab` with two nodes seeded with different fixture records: querying
   from either node returns the union (peer fan-in), with `?guest=`/`?vlan=`/`?subnet=`/`?port=`/
   `?protocol=` each independently narrowing the result set correctly (table test per filter).
4. Ring store bound: seeding beyond `retention_minutes` prunes the oldest rows on the next tick;
   seeding beyond the hard cap prunes down to it regardless of age; both asserted by row count.
5. `flow.batch` WS push fires on ingestion against a subscribed test client; `droppedTotal`
   increments under a synthetic storm exceeding the rate cap.
6. `docs/api.md` (Flows section + Peer API + WS table) and `docs/data-model.md` (§2 `flow_samples`)
   updated; `make check` green.

---

## T-1003 · Flow explorer + map flow painting
**model:** sonnet-5 · **size:** L · **depends:** T-1002, T-902 · **context:** `docs/features/topology.md` §1 §2 §3, `docs/api.md` (Flows, WebSocket), `web/src/topology/`, `web/src/api/ws.ts`

**Objective:** Turn ingested flows into two UI surfaces: a filterable/sortable/aggregatable
explorer table, and animated, weighted edges layered on the T-902 v2 map renderer — the topology
map becomes a live traffic diagram, not just a throughput heat map. Read-only throughout; no
mutation.

**Deliverables:**
- `web/src/flows/` (new directory): `FlowExplorer.tsx` — table with filter controls matching
  `GET /flows`'s query params (guest/VLAN/subnet/port/protocol), sort by bytes/packets/recency,
  and a conversation aggregation mode (group by src/dst pair, summing bytes/packets over the
  visible window) per the roadmap's "filter/sort/aggregate by conversation" requirement;
  `flowsQueries.ts` (TanStack Query hooks over `GET /flows`, following `topology/queries.ts`'s
  existing pattern) and a WS bridge subscribing to the `"flows"` topic, applying `flow.batch`
  entries into the query cache exactly like `web/src/api/ws.ts` already does for
  `firewall.log.batch`.
- Map integration: a new "Flows" layer toggle (`web/src/topology/LayerToggleBar.tsx`) that, when
  on, paints active guest-pair conversations as animated/weighted edges over the T-902 canvas
  renderer — edge thickness by current bytes/sec, animated dash-flow direction by src→dst,
  distinct visually from the existing traffic-paint mode (`trafficMode.ts`, per-entity heat) so
  the two don't visually collide when both are on. New `web/src/topology/flowEdges.ts` computing
  the edge set from the flow query cache, following `toFlowElements.ts`'s existing
  inventory-graph-to-React-Flow-elements pattern.
- Guest-pair drill-down: clicking a flow-painted edge opens the inspector pre-filtered to that
  src/dst pair, with a "view in Flow Explorer" deep link that lands on `FlowExplorer.tsx`
  pre-filtered to the same pair (URL-carried filter state, matching Phase 9's saved-view pattern
  where it exists).
- Empty/degraded states: no flow source configured (T-1002's listeners all off, T-1004 not
  enabled) → the Flows layer toggle is present but shows a dismissible hint linking to the flow
  source setup docs, matching `docs/features/topology.md` §5's existing empty-state convention.

**Acceptance criteria:**
1. Vitest + Testing Library: `FlowExplorer.test.tsx` covers filter application, sort, and
   conversation aggregation against a seeded flow fixture set (mirrors `metricsQueries.test.tsx`'s
   query-hook test pattern); `flowEdges.test.ts` covers the flow-record-to-edge-set computation
   (pure function, table-tested) including the "no active flows" empty case.
2. `web/e2e` Playwright scenario: against `make dev` + pvemock `flow-lab` fixture with seeded
   flow records, toggling the Flows layer renders at least one animated edge, clicking it opens
   the inspector filtered to that pair, and the "view in Flow Explorer" link lands on the explorer
   pre-filtered to the same guest pair.
3. Flows layer and traffic-paint mode can both be enabled simultaneously without one obscuring
   the other's legend/controls (render test asserting both control sets are present and distinct).
4. Empty-state hint renders when `GET /flows` returns zero items cluster-wide; disappears once a
   flow arrives via the WS bridge in the same session (no page reload needed).
5. `make check` (incl. `tsc --noEmit`, no `any`) green.

---

## T-1004 · Host-local flow sampling (conntrack/eBPF)
**model:** sonnet-5 · **size:** M · **depends:** T-1002 · **context:** `docs/security.md` (Host footprint), `docs/architecture.md` §2 §7, `internal/host/`, `internal/flow/` (from T-1002)

**Objective:** Per-bridge flow sampling on nodes with no external sFlow/NetFlow source — a
conntrack-based sampler first (works with the capabilities `vnproxd` already holds), an
eBPF-based sampler where the kernel allows (better fidelity, higher risk), both strictly opt-in
per node and feeding the exact same `internal/flow` ring store T-1002 built, so the explorer and
map painting need no awareness of which source produced a record.

**Deliverables:**
- `internal/flow/hostsample/conntrack.go`: periodic `/proc/net/nf_conntrack` (or netlink
  `conntrack` subsystem via the already-approved `vishvananda/netlink` where it exposes conntrack)
  poll per bridge, diffed into `flow.Record`s tagged `source: "conntrack"`; polling interval
  configurable (`[flows] host_sample_interval_sec`, default 10 — coarser than T-1002's live UDP
  ingestion by design, conntrack sampling is inherently lower-resolution).
- `internal/flow/hostsample/ebpf.go`: an eBPF-based per-bridge sampler behind a build tag and a
  runtime kernel-feature probe (refuses to start, logs why, and falls back to conntrack-only when
  the probe fails) — kernel version and `CAP_BPF`/`CAP_PERFMON` requirements documented in the
  package doc comment; **`docs/security.md`'s Host footprint section is updated** to note that
  enabling eBPF sampling requires capabilities beyond the six currently in
  `packaging/systemd/vnprox.service`'s `CapabilityBoundingSet` (flagged, not silently added — the
  systemd unit only grants the extra capability when `[flows] ebpf_sampling_enabled = true` is
  set at install/upgrade time, never unconditionally).
- Opt-in gating: both samplers are off by default and configured per node (never cluster-wide) via
  `vnprox.toml`, matching T-1002's `[flows]` section and the roadmap's "runs only when enabled per
  node" requirement; `GET /config`'s Settings payload surfaces which sampler (if any) is active on
  the current node.
- Fixture-backed tests: a synthetic `/proc/net/nf_conntrack`-format fixture drives the conntrack
  sampler's tests without a live kernel; the eBPF path is exercised only via the kernel-feature
  probe's negative case (probe fails on the CI kernel → clean fallback, asserted by log output) —
  no CI environment is assumed to support real eBPF attachment.

**Acceptance criteria:**
1. Conntrack sampler: golden test feeding a fixture conntrack table produces the expected
   `[]flow.Record` set (table-driven, including a malformed-line-skip case mirroring
   `internal/fwlog`'s defensive parsing convention).
2. Both samplers respect `host_sample_interval_sec` and write into the same `flow_samples` ring
   T-1002 defined — no second storage path, asserted by a test that enables conntrack sampling
   and confirms records are visible via `GET /flows` unmodified.
3. eBPF probe failure (CI kernel, or an explicit forced-failure test hook) logs a structured
   `slog` warning naming the missing capability/feature and the daemon continues running with
   conntrack-only (or fully disabled) sampling — never a fatal error.
4. Opt-in enforcement: with `ebpf_sampling_enabled`/`conntrack sampling` both unset, no sampler
   goroutine starts (asserted via the run-group's goroutine inventory, matching
   `docs/development.md`'s "every goroutine has an owner" convention).
5. Report's needs-hardware-validation list (mandatory, appended to
   `planning/reports/needs-hardware-validation.md`) covers at minimum: exact conntrack table
   format across the target kernel range (PVE 8.2+/9.x per `docs/architecture.md` D9), real
   `CAP_BPF`/`CAP_PERFMON` availability under the hardened systemd unit on a live node, measured
   CPU/memory overhead of each sampler at the Phase 9 scale target (`docs/performance.md`) with a
   concrete measurement plan (not just "TBD"), and eBPF program verifier acceptance across the
   supported kernel range.
6. `make check` green (eBPF build tag excluded from the default `go test ./...` matrix, documented
   in the Makefile change).

---

## T-1005 · Alert routing
**model:** sonnet-5 · **size:** M · **depends:** T-602 · **context:** `docs/features/monitoring.md` §5, `docs/api.md` (Findings), `internal/findings/notify.go`, `internal/findings/pvenotify.go`, `docs/data-model.md` §2

**Objective:** Route findings/drift transitions to webhooks directly from vnprox, independent of
PVE's own notification-target system — closing the documented gap in `pvenotify.go`'s doc comment
(PVE's notification API has no way to carry vnprox's own finding text, only a generic
test-notification). Per-severity/per-source routing rules, delivery retried with backoff, a
delivery log, and Settings UI CRUD — all app-owned data, per CLAUDE.md's store scope.

**Deliverables:**
- `internal/findings/webhook.go`: a new `Notifier` implementation (satisfies the existing
  `findings.Notifier` interface from `notify.go` — no change to `Engine`'s notification-firing
  logic, T-602's "once per transition" contract is reused unchanged) delivering to arbitrary
  webhook targets: generic JSON body (the `Finding` shape verbatim) plus shaped payloads for
  Gotify, ntfy, and Slack (incoming-webhook format) selected per target.
- Routing rules: new SQLite table `alert_rules` (migration `internal/store/migrations/NNNN_alert_rules.sql`)
  — `{id, name, enabled, sourceFilter?: [string], severityFilter?: [string], targetKind:
  "generic"|"gotify"|"ntfy"|"slack", targetUrl, targetSecretEnc?, createdAt, updatedAt}` — filters
  are optional/ANDed like every other filter contract in this codebase; secrets (Gotify/ntfy
  tokens) encrypted at rest the same way session PVE tickets are (`docs/security.md`'s AES-256-GCM
  pattern).
- Delivery: retry with exponential backoff (bounded attempts, e.g. 5, capped interval), a
  `alert_deliveries` table logging `{id, ruleId, findingId, at, attempt, status, error?}` for the
  Settings UI's delivery log view; a failed-after-max-retries delivery is logged, not retried
  indefinitely (bounded, matching the ring-store bound spirit even though this table is
  small/event-driven rather than sampled).
- API: `GET/POST /alert-rules`, `GET/PUT/DELETE /alert-rules/{id}`, `POST /alert-rules/{id}/test`
  (sends a synthetic test finding through the rule's target), `GET /alert-deliveries?ruleId=&status=`
  — documented as a new section in `docs/api.md`.
- Settings UI: `web/src/settings/AlertRules.tsx` — rule CRUD table + editor form (severity/source
  filter pickers, target kind selector with shape-specific fields, test-send button), and a
  delivery log view.
- Test webhook receiver: `internal/findings/testwebhook/` (or an in-process `httptest.Server`
  fixture) used by both Go integration tests and documented for manual verification.

**Acceptance criteria:**
1. Table-driven tests: each target kind (generic/Gotify/ntfy/Slack) produces the documented
   payload shape against a captured request on the test webhook receiver.
2. Routing: a rule with `severityFilter: ["error"]` fires only on error-severity transitions; a
   rule with `sourceFilter: ["health"]` fires only on health-sourced findings; both filters
   combined AND; disabled rules never fire (table test matrix).
3. Retry/backoff: a receiver returning 500 for the first N attempts then 200 is retried per the
   documented schedule and eventually logged `status: "delivered"`; a receiver that never succeeds
   is logged `status: "failed"` after the max attempt count, never retried again.
4. `POST /alert-rules/{id}/test` delivers a synthetic finding to the rule's target and returns the
   delivery outcome synchronously.
5. Vitest + Testing Library: `AlertRules.test.tsx` covers rule CRUD form validation and the
   delivery log rendering against a mocked API; `web/e2e` scenario creates a rule pointed at a
   local test receiver, triggers a qualifying finding via pvemock, and asserts a delivery appears
   in the log.
6. `docs/api.md` and `docs/data-model.md` updated with the new routes/tables; `make check` green.

---

## T-1006 · Firewall log analytics
**model:** sonnet-5 · **size:** M · **depends:** T-505, T-502 · **context:** `docs/features/firewall.md` §4, `docs/features/monitoring.md` §5, `docs/api.md` (Firewall), `internal/fwlog/`, `internal/findings/`

**Objective:** Aggregate the v1 log viewer (`internal/fwlog`) into rule hit counts, top
blocked-source/destination rankings, and an unused-rule report that closes the loop between
firewall editing and reality — "this rule hasn't matched anything in N days" feeding a new
informational finding linked into the rule editor. Read-only aggregation over data `internal/fwlog`
already collects; no new log collection path.

**Deliverables:**
- `internal/fwlog/analytics.go`: aggregates `RingBuffer`/`Correlate`'s already-correlated entries
  (reusing `Correlation.rule`'s `RuleRef` — no new correlation logic) into: per-rule hit counts
  over a configurable window (default 24h, bounded by the same ring the log viewer already caps —
  no new unbounded storage), top-N blocked sources/destinations (by `action` = DROP/REJECT), and
  a per-ruleset "last seen" timestamp per rule position, computed against the current resolved
  ruleset (`internal/fw.Resolve`) so a moved/renumbered rule's history follows it by identity, not
  position.
- `GET /firewall/analytics?scope=&ref=&windowHours=` (documented as a new subsection of
  `docs/api.md`'s Firewall section): `{hitCounts: [{rule: RuleRef, hits, lastSeenAt?}], topBlocked:
  {sources: [...], destinations: [...]}, unusedRules: [{rule: RuleRef, daysSinceLastHit}]}`.
- New finding `fw_rule_unused`: source `health` (reuses the existing `source` enum — no new value
  needed since this is a health-style structural/staleness check over firewall state, documented
  as an addition to `docs/api.md`'s `GET /findings` check vocabulary), severity `info`, one finding
  per enabled rule with zero hits in the configurable N-day window (default 30), `refs` naming the
  guest/scope ruleset, `docsLink` set, `fixable: false` (this is advisory, not machine-fixable —
  deleting a rule is a judgment call), and a UI deep link into `internal/fw`'s rule editor
  (T-502's `web/src/firewall/` rule table) at the exact rule position.
- Analytics UI: a new tab on the existing firewall log viewer page — hit-count table, top-blocked
  charts (`recharts`, per `docs/development.md`'s approved chart library), and the unused-rule list
  with "edit rule" links.

**Acceptance criteria:**
1. Table-driven tests over a seeded `RingBuffer` fixture: hit counts match expected per-rule
   totals; top-blocked rankings match expected order; a rule with zero matches over the window
   appears in `unusedRules` with the correct `daysSinceLastHit`; a rule that has matched within
   the window does not.
2. Rule-identity tracking: moving a rule to a new position in the same changeset (T-502 op
   `fw.rule.move`) carries its hit history forward (asserted by re-resolving the ruleset and
   confirming the same `RuleRef` identity resolves to the same historical count).
3. `fw_rule_unused` findings appear in `GET /findings` with `source: "health"`,
   `check: "fw_rule_unused"`, correct `refs`, and clear once the rule records a hit within the
   window (next poll cycle) or is deleted.
4. Vitest + Testing Library: analytics tab renders hit counts/top-blocked/unused list from a
   mocked `GET /firewall/analytics` response; the "edit rule" link navigates to the correct rule
   row in the editor (`web/e2e` scenario against pvemock `three-node-vlan` with seeded log
   fixtures, since this is genuine cross-page interaction proof).
5. `docs/api.md` updated (new route + `fw_rule_unused` check entry); `make check` green.

---

## T-1007 · History playback
**model:** sonnet-5 · **size:** M · **depends:** T-1003, T-601 · **context:** `docs/features/monitoring.md` §2, `docs/features/topology.md` §2 §3, `docs/api.md` (Metrics, Audit, Flows), `web/src/topology/trafficMode.ts`, `web/src/topology/flowEdges.ts` (from T-1003)

**Objective:** Scrub the map's traffic paint and flow-painted edges back through the retained
metric/flow window — "what did the network look like at 02:00" — with a timeline control showing
event markers (changesets applied, findings raised). Strictly read-only: this replays already-
retained history (`metric_samples`' 24h ring, `flow_samples`' bounded window from T-1002/T-1003),
it does not extend either retention window, and it never triggers a mutation.

**Deliverables:**
- `web/src/topology/history/HistoryTimeline.tsx`: a scrubber control bound to the map's existing
  traffic-paint (`trafficMode.ts`) and flows (`flowEdges.ts`) layers — dragging the scrubber
  re-queries `GET /metrics/history` and `GET /flows?fromTs=&toTs=` for the selected instant/window
  and re-renders the same paint/edge logic those layers already use at "now", so this task adds no
  new rendering path, only a time parameter threaded through the existing one.
- Event markers on the timeline: changeset apply/confirm/rollback events (`GET /audit`, filtered
  to `action` in the T-205 apply-engine lifecycle set already documented in `docs/api.md`'s Audit
  section) and finding-raised events (`GET /findings` history is not currently retained — this
  task adds a lightweight `finding_events` table, migration `internal/store/migrations/NNNN_finding_events.sql`,
  `{id, findingId, at, transition}` populated from `findings.Notifier`'s existing transition
  detection in `notify.go` — reusing that detection, not duplicating it — bounded to the same
  window as `metric_samples`, pruned alongside it).
- `GET /history/events?fromTs=&toTs=` (new `docs/api.md` subsection under Metrics or a new History
  section): merges changeset lifecycle audit rows and finding transitions into one timeline-marker
  feed, `{items: [{at, kind: "changeset"|"finding", ...}]}`.
- Read-only enforcement: the timeline control has no apply/confirm/rollback affordance anywhere in
  its UI — clicking a changeset marker deep-links to that changeset's existing (already-shipped)
  detail/diff view, never re-triggers it.
- Bounds: playback range is clamped to the shorter of the metrics window (24h) and the flows
  window (T-1002's configurable retention, default 60m) — the UI discloses this explicitly (e.g.
  "flow history available for the last 60 minutes only") rather than silently showing a gap.

**Acceptance criteria:**
1. Vitest + Testing Library: `HistoryTimeline.test.tsx` — scrubbing to a timestamp re-issues the
   `GET /metrics/history`/`GET /flows` queries with the correct `fromTs`/`toTs`, and re-renders
   paint/edges from the returned historical data rather than live WS data (asserted by mocking
   both and confirming the WS-fed cache is not read while scrubbed).
2. Event markers render at the correct timeline positions for a seeded set of changeset-lifecycle
   audit rows and finding transitions (`finding_events` table); clicking a changeset marker
   navigates to that changeset's detail view.
3. `GET /history/events` merges and sorts both event kinds correctly (table test) and respects
   `fromTs`/`toTs` bounds.
4. Range clamping: scrubbing before the earliest available flow data disables/greys the flows
   layer with the disclosed message while metrics-only playback continues to work.
5. `web/e2e` scenario: against `make dev` + pvemock with seeded historical samples and a
   changeset audit trail, scrubbing the timeline visibly changes the map's traffic paint and shows
   a changeset marker that deep-links correctly.
6. `docs/api.md` and `docs/data-model.md` (§2 `finding_events`) updated; `make check` green.
