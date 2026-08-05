# Phase 19 — Operable in the field (v3.2)

Goal: make vnprox survivable and diagnosable when it goes wrong at someone else's site. The
product is very good at observing a Proxmox cluster and comparatively blind to itself: there is
no backup story at all, no support bundle, no daemon-level metrics, no preflight check, and no
retention ceiling on data that grows forever on a hypervisor's root filesystem. Every card here
is something an operator needs at 3 a.m. and does not currently have.

Dependency shape: **T-1901 (backup/restore) is the phase's root** — the support bundle (T-1902)
reuses its collection and redaction machinery, and retention (T-1905) needs a restore path to be
safe to prune against. **T-1903** (self-metrics) gates **T-1904** (`doctor`), which reports on
what T-1903 measures. **T-1906** and **T-1907** are independent and can start immediately.
T-1901 depends on T-1807 (migration upgrade-chain tests) because restore-across-schema-versions
is exactly the path T-1807 makes testable.

Two cards here are 🔒 for the same underlying reason: **they collect everything, and everything
includes secrets.** A backup archive may contain the session key that makes every sealed
credential readable; a support bundle is going to be pasted into a forum thread. Redaction is
not a finishing touch on these cards — it is the hard part.

Exit demo: a deliberately broken install is diagnosed from a support bundle alone, without SSH;
the daemon's own Grafana dashboard shows the apply that caused it; the box is restored from
backup onto different hardware with its audit trail intact.

---

## T-1901 · Backup, restore, and disaster recovery of vnprox state ★ 🔒
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-1807 · **context:** `internal/store/` (schema, migrations, `cipher.go`), `cmd/vnproxctl/`, `docs/deployment.md`, `docs/security.md`, `docs/data-model.md`, `packaging/`

**Objective:** There is no backup story. The SQLite store holds changesets, pre/post snapshots,
audit history, layout, tenants, and blueprint state; `/etc/vnprox/keys/session.key` is what makes
every sealed credential in it readable. Lose the box and you lose the audit trail and every
rollback snapshot — precisely the artifacts you most want after an incident.

**Safety analysis (required section):**
- **Key material is opt-in, loudly.** A backup *without* the session key is safe to store
  anywhere and useless for reading sealed credentials — which is the right default. A backup
  *with* it is a complete compromise of every PVE credential, federation credential, WireGuard
  private key, and (after T-1805) revert ticket vnprox holds. Including it requires an explicit
  flag, prints a warning naming exactly what is inside, and marks the archive header
  accordingly.
- **A restore is a mutation of the daemon's own authoritative state** and must refuse to run
  against a live daemon, refuse to silently overwrite a store with a *newer* schema, and be
  atomic — a half-restored store is worse than no restore.
- **The archive is an untrusted input on restore.** It is parsed defensively: path traversal in
  entry names, decompression bombs, and truncated archives are all rejected before anything
  touches disk.

**Deliverables:**
- `vnproxctl backup` — a versioned, integrity-checked archive: the store (consistent snapshot,
  not a live file copy), config, schema version, and an archive manifest. Key material only under
  `--include-keys`.
- `vnproxctl restore` — with `--dry-run`, refusal against a running daemon, schema-version
  compatibility checks (forward migration allowed, downgrade refused), and atomic replacement.
- Restore-to-different-hardware documented and tested: what carries over, what must be
  re-established (node identity, peer secret, PVE credentials if keys were excluded).
- Scheduled/automatable backup: a documented cron-friendly invocation with sane retention, not a
  new scheduler inside the daemon.
- `docs/deployment.md` gains a Backup and disaster recovery section; `docs/security.md` gains the
  archive's threat model.

**Acceptance criteria:**
1. Backup → wipe → restore round-trips a store with changesets, snapshots, audit rows, and
   sealed credentials; every table's row count and a sampled row's contents match.
2. A backup taken **without** `--include-keys` contains no key material — asserted by scanning the
   archive bytes for the session key and for known plaintext markers, one case per secret class.
3. Restore across a schema upgrade works (backup at version N, restore into a binary at N+k,
   forward migration runs); restore into an *older* binary is refused with a clear error.
4. Restore refuses to run against a live daemon, and a restore interrupted midway leaves the
   original store intact (atomicity, tested by injecting a failure).
5. Malicious archives — path traversal, decompression bomb, truncated — are rejected without
   writing outside the target directory; table-driven.
6. `make check` green; docs as above.

---

## T-1902 · Support bundle export ★ 🔒
**model:** strong (Opus/Fable-class) · **size:** M · **depends:** T-1901 · **context:** T-1901's collection and redaction machinery, `internal/api/` (health, findings, drift), `internal/change/`, `cmd/vnproxctl/`, `docs/security.md`

**Objective:** One command producing one redacted archive that lets someone diagnose a stranger's
broken install without SSH. This is the single highest-leverage card in the phase for anyone who
ever supports a deployment they cannot log into.

**Safety analysis (required section):** a support bundle is going to be attached to a forum post.
It must be **redacted by construction, not by review**. Every collector declares what it emits and
every emitted field passes an explicit allowlist or a redactor; a collector that cannot describe
its output does not ship. The failure mode being designed against is a PVE API token or a
WireGuard private key reaching a public thread — worse than having no support bundle at all.

**Deliverables:**
- `vnproxctl support-bundle`: version and build info, config with secrets stripped, schema
  version, collector health and poll history, recent findings and drift, the last N changesets
  with diffs (ops redacted per `redactOpSecrets`' existing rules), daemon logs, peer reachability,
  host network state, and the daemon's own metrics (T-1903 once it lands).
- A redaction layer with a declared inventory of secret classes — PVE tokens and tickets,
  session/metrics/blueprint keys, WireGuard private and preshared keys, federation credentials,
  webhook target secrets, T-1805's revert tickets — each with a test.
- A `--dry-run` that prints exactly what would be collected, so an operator can decide before
  producing anything.
- A bundle manifest and a documented "what's in here and what isn't" for the reader.
- `docs/deployment.md` (how to produce one) and `docs/security.md` (what it deliberately omits).

**Acceptance criteria:**
1. A bundle produced from a store seeded with one of every secret class contains none of them —
   table-driven, one case per class, scanning the whole archive rather than individual files.
2. Adding a new field to a collected structure without declaring it fails a test — the allowlist
   is enforced, so redaction cannot rot as the product grows.
3. The bundle is sufficient to diagnose a scripted broken install (wrong PVE credential, port
   conflict, failed migration) without shell access — proven by three fixture scenarios.
4. `--dry-run` output matches what a real run collects.
5. `make check` green; docs as above.

---

## T-1903 · Self-observability: RED metrics for the daemon ★
**model:** sonnet-5 · **size:** M · **depends:** T-1801 · **context:** `internal/metrics/`, `internal/api/` (middleware), `internal/collect/`, `internal/change/`, `internal/store/`, `web/src/grafana/`, `docs/features/monitoring.md`

**Objective:** Today's exporter reports cluster-derived gauges (`vnprox_findings_open`,
`vnprox_drift_open`, `vnprox_changesets`, interface counters) plus build and session info. It
reports almost nothing about *vnprox itself*: no HTTP request rate/error/duration, no per-collector
poll duration and failure counters, no change-engine outcome counters, no store size or query
latency, no peer-RPC health, no WS connection count. vnprox observes your cluster far better than
it observes itself.

**Deliverables:**
- HTTP RED metrics: request count, error count, and duration histogram by route pattern and
  status class — route *pattern*, never raw path, or the cardinality will melt Prometheus.
- Collector metrics: poll duration, success/failure counters, and consecutive-failure gauge per
  source, matching what `/health` already tracks internally.
- Change-engine metrics: applies, confirms, rollbacks, and unattended reverts (T-1805) by outcome;
  time spent in `awaiting_confirm`.
- Store metrics: database size on disk, query duration, and migration state.
- Peer-RPC and WebSocket metrics: peer call latency and failure rate, live WS connections.
- A Grafana dashboard for the daemon, shipped alongside the existing cluster one.
- `docs/features/monitoring.md` documents every new series.

**Acceptance criteria:**
1. Every new series is documented with its labels and cardinality bound; a test asserts route
   labels use patterns and that an unbounded label source (raw path, guest name, changeset id)
   never reaches a metric.
2. Metrics reflect reality: driving requests, a failing collector, and a rolled-back changeset
   through the test harness moves the expected series.
3. The daemon dashboard renders against a scrape of a running test daemon.
4. Scrape overhead is measured and stated; the metrics endpoint stays well under a documented
   budget with the full series set.
5. `make check` green; `docs/features/monitoring.md` updated.

---

## T-1904 · `vnproxctl doctor` ★
**model:** sonnet-5 · **size:** M · **depends:** T-1903 · **context:** `cmd/vnproxctl/`, `internal/config/`, `internal/peer/` (secret consistency), `internal/store/`, `packaging/install.sh` (existing port-conflict detection), `docs/deployment.md`

**Objective:** A preflight and self-check that turns "it doesn't work" into an actionable message
— runnable before install and any time after.

**Deliverables:**
- Checks, each with a clear pass/warn/fail and a remediation hint: config sanity; PVE reachability
  and token privilege adequacy (the privileges vnprox actually needs, named); port conflicts
  (including the PBS `:8007` collision `install.sh` already knows about); key file existence and
  permissions; pmxcfs availability; peer secret consistency across nodes; schema version vs.
  binary; clock skew (which breaks ticket lifetimes and commit-confirm timers); disk headroom for
  snapshots and captures.
- Machine-readable output (`-o json`) alongside the human format, so it can be dropped into a
  support bundle and into CI.
- Non-zero exit on any `fail`, so it can gate an install script.
- Integration into `packaging/install.sh` as a preflight step, and into T-1902's bundle.

**Acceptance criteria:**
1. Each check has a test with a deliberately broken fixture proving it actually fails — a check
   that only ever passes is not a check.
2. Every `fail` and `warn` carries a remediation hint naming the file, port, or command involved.
3. `-o json` output is schema-stable and consumed by T-1902's bundle.
4. Exit code is non-zero iff at least one check fails; `install.sh` aborts on it.
5. `make check` green; `docs/deployment.md` documents `doctor`.

---

## T-1905 · Retention, rotation, and compaction ★
**model:** sonnet-5 · **size:** M · **depends:** T-1901 · **context:** `internal/store/` (audit, snapshots, flows, capacity samples, latency mesh), `internal/capture/` (pcap files), `docs/data-model.md`, `docs/deployment.md`

**Objective:** Audit rows, flow records, capacity samples, latency-mesh history, snapshots, and
`.pcap` captures all accumulate with no documented ceiling. The failure mode today is a full root
filesystem on a hypervisor — which is to say, an outage caused by the tool meant to prevent one.

**Deliverables:**
- A retention policy per data class with a configurable default in `vnprox.toml`, chosen and
  argued rather than guessed: audit (long, it is a compliance artifact), snapshots (bounded by
  count and age, since they are the rollback safety net), flows and capacity samples
  (downsample then expire), captures (short, and already the largest).
- Enforcement: a periodic prune, and a `VACUUM`/compaction path that reclaims space without
  blocking the daemon.
- **Guardrails:** a prune never removes the snapshot backing a changeset that could still roll
  back, nor an audit row younger than the configured floor. Retention must not be able to destroy
  the safety net.
- A `store_near_capacity` finding with a configurable threshold, so a filling disk is a warning
  rather than a surprise.
- `docs/data-model.md` documents every class's retention; `docs/deployment.md` covers sizing.

**Acceptance criteria:**
1. Each data class prunes to its configured policy — table-driven, one case per class.
2. A snapshot required by a changeset in `awaiting_confirm` or within its rollback window is never
   pruned, regardless of age — explicit test, since this is the dangerous case.
3. Compaction reclaims measurable space and the daemon serves reads throughout.
4. The `store_near_capacity` finding fires at threshold and clears below it, hysteresis-debounced
   like every other finding.
5. Defaults are stated and argued in the report; `make check` green.

---

## T-1906 · Peer-API CA pinning ★ 🔒
**model:** strong (Opus/Fable-class) · **size:** M · **depends:** — · **context:** `internal/peer/` (`Client`, `ClientOptions`), `planning/reports/T-301.md`, `planning/reports/needs-hardware-validation.md` (the flagged item), `docs/architecture.md` §9, `docs/security.md`

**Objective:** `internal/peer.Client` inherits the system trust store rather than pinning the
cluster's own `/etc/pve/pve-root-ca.pem`, which is what real peer daemons present. The peer API
carries cluster-wide network mutations; it should not accept any publicly-trusted certificate.

**Safety analysis (required section):** the current posture means an attacker with a certificate
from *any* public CA, plus a position on the management network, can impersonate a peer daemon —
and the peer API is a mutation surface. Pinning must fail **closed** (an unverifiable peer is
unreachable, not trusted), while degrading sanely where `/etc/pve/pve-root-ca.pem` genuinely does
not exist (dev, tests, a non-PVE host) — and that degradation must be an explicit, logged,
configured choice, never a silent fallback to the system pool.

**Deliverables:**
- Pin the cluster CA in `peer.Client`, with the trust anchor configurable and defaulting to
  `/etc/pve/pve-root-ca.pem`.
- An explicit dev/test escape hatch that is opt-in, logged loudly at startup, and impossible to
  enable by accident.
- Certificate rotation handled: a re-read path so a rotated cluster CA does not require a daemon
  restart.
- A finding when a peer's certificate fails verification, distinguishing "unreachable" from
  "untrusted" — an operator must be able to tell a network problem from an attack.
- `docs/security.md` and `docs/architecture.md` §9 updated; the corresponding
  `needs-hardware-validation.md` item moved into T-1801's harness so the real-CA shape is
  captured on hardware.

**Acceptance criteria:**
1. A peer presenting a certificate signed by a different CA is rejected, even if that CA is in the
   system trust store — the headline case.
2. A peer presenting the pinned cluster CA is accepted.
3. The escape hatch cannot be enabled without an explicit config value, and its use is logged at
   `WARN` on every startup.
4. CA rotation is picked up without a restart.
5. Untrusted and unreachable produce distinguishable findings; `make check` green.

---

## T-1907 · Physical-layer progressive collapse ★
**model:** sonnet-5 · **size:** M · **depends:** — · **context:** `internal/topology/collapse.go` (guest-layer collapse, `DefaultCollapseThreshold`), `internal/topology/types.go`, `web/src/topology/`, `docs/features/topology.md` §4, `planning/reports/T-607.md` §6

**Objective:** The last unclosed gap from T-607's docs audit. `docs/features/topology.md` §4 has
documented physical-layer collapse to a per-node summary since v1.0; only guest-layer collapse was
ever built. Four releases of documented behavior that does not exist.

**Deliverables:**
- Physical-layer collapse in `internal/topology`, mirroring `collapse.go`'s existing guest-layer
  mechanism (threshold, synthetic summary entity, expand-on-demand) rather than inventing a second
  pattern.
- Frontend expand/collapse affordance consistent with the guest-layer one, including keyboard
  reachability (T-2004 will audit it either way).
- A threshold chosen against the real numbers T-1808 measures, not guessed — the card should
  consume that result if it has landed, and say so if it has not.
- `docs/features/topology.md` §4 reconciled with what now actually exists.

**Acceptance criteria:**
1. A cluster whose physical layer exceeds the threshold projects a per-node summary; below it,
   individual elements are unchanged — regression test for the small case.
2. Expanding a collapsed node yields exactly the elements the uncollapsed projection would have
   produced — the collapse is lossless.
3. Guest-layer collapse behavior is unchanged; existing tests pass untouched.
4. Frontend renders and expands the summary, keyboard-reachable; Vitest plus a Playwright spec.
5. `docs/features/topology.md` §4 matches the implementation; `make check` green.

---

## Card-author notes

- **T-1901 and T-1902 share machinery deliberately.** The bundle is, structurally, a backup with
  a harsher redaction policy and a narrower scope. T-1902 depends on T-1901 so that collection and
  redaction are built once; an executor who finds the abstraction fighting them should say so in
  the report rather than quietly duplicating it.
- **T-1905's guardrails are the card.** Pruning is easy; pruning without ever destroying a
  rollback path is the part that needs care. A reviewer should read AC2 first.
- **T-1907's threshold is the one number this phase cannot invent.** If T-1808 has not run when
  T-1907 executes, pick a defensible default, state it as provisional in the report, and file a
  follow-up to revisit it against real numbers.

---

## T-1906-bug-01 · Pinned peer TLS can fail closed against a stale IP SAN

**Severity:** High — on a multi-node cluster this makes **every peer unreachable at once**, on
upgrade, with no config change by the operator. Peer-API traffic carries cross-node changeset
application and distributed rollback, so this degrades the cluster to single-node behaviour.

**Found on real hardware** during the deploy of this arc's work to `pvecube`
(pve-manager/9.2.4, kernel 7.0.14-4-pve, single node).

T-1906 pins `/etc/pve/pve-root-ca.pem` as the sole trust anchor for peer TLS and fails closed by
design. Its report flagged the top risk as "peers are dialled by IP; if real PVE's `pve-ssl.pem`
carries only hostname SANs, pinning fails closed on every peer at once." The hardware answer is
worse than that framing:

```
node addresses:   vmbr0: 192.168.1.9/24        (the only real address)
pve-ssl.pem SANs: IP 127.0.0.1, IP ::1, IP 192.168.100.99,
                  DNS localhost, DNS pvecube, DNS pvecube.localdomain
```

The certificate **does** carry an IP SAN — so the flagged "hostname-only" case does not apply —
but it is a **stale** address (`192.168.100.99`) that the node no longer has. The node's actual
address is absent from the SAN list. Pinned verification of a peer dialled at `192.168.1.9` would
therefore fail hostname validation and be rejected as untrusted.

This node is single-node, so nothing is currently broken here; the finding is that the *assumption*
"a PVE leaf cert covers the address vnprox will dial" is false in practice. PVE generates
`pve-ssl.pem` at cluster/cert-creation time and does not necessarily regenerate it when a node's
management address changes.

**What was verified working** (same host, same session): `/etc/pve/pve-root-ca.pem` exists at the
documented path and loads as a trust anchor (`peer: cluster CA trust anchor loaded; peer TLS is
pinned to it`), and `openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem`
returns OK. The chain is sound; only the address coverage is not.

**Scope for whoever picks this up:**
- Decide the policy deliberately. Options include dialling peers by **name** rather than IP where a
  DNS SAN exists (this cert carries `pvecube`/`pvecube.localdomain`), setting `ServerName`
  explicitly from the PVE node name while still pinning the CA, or detecting the mismatch at
  startup and raising a named finding *before* the first peer call fails.
- Whatever is chosen, do **not** relax to the system trust pool — that reinstates the
  vulnerability T-1906 closed.
- Add a startup preflight: compare the local node's addresses against its own `pve-ssl.pem` SANs
  and warn loudly on a mismatch. `vnproxctl doctor` (T-1904) is the natural home; this is exactly
  the class of "it will fail later, for a knowable reason" check that card exists for.
