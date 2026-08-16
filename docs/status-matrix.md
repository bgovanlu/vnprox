# vnprox — full-stack audit matrix

**Audit date:** 2026-08-06 · **Commit:** `6c0957e` · **Sweep taken against:** `v3.0.4` · **Deployed at sweep time:** `3.0.4+43+g6c0957e` (pvecube) · **Current release: `v4.0.0`** (2026-08-14) — this document's §2/§3/§4 numbers were never re-run against it; see the note below and §7

This is a mechanical sweep of the whole stack: feature area × backend × GUI × API × docs × tests × hardware validation. Every figure below is derived from the repository at the commit above by a command recorded in the *Method* section, not from a task report's own claim about itself.

Companion documents: [`project-status.md`](project-status.md) (open items, percent complete, roadmap) and [`datasheet.md`](datasheet.md) (shipped capability, for external readers).

> **This matrix predates Arc 5 and Arc 6** (phases 25–28, 25 cards, shipped as `v3.5.0`
> 2026-08-10 to 2026-08-13, and phases 29–33, proposed 2026-08-15 as `docs/roadmap-earned.md` —
> `project-status.md` §6.4 has the Arc 5 per-card delivery record). Retagging §2's rows for the
> new feature areas needs the same mechanical sweep this file's own method requires, re-run
> against the current tree, which has not been done here — so no new rows were added to §2
> rather than adding ones with invented cells. §5.11 and §5.4 below are the two places this file
> *was* updated for Arc 5, both narrowly. **§7 (added 2026-08-15, filled in 2026-08-16 by `T-2906`)
> is a third, deliberately partial update**: 40 Arc 5/v4.0 feature areas §2 has no row for, each
> with one overall `●`/`◐` mark, a mechanically-derived "does a `web/src` client exist" answer,
> and a pointer — **not** the full eight-column re-audit this note says has not been done. Its
> closing paragraph carries the finding that only becomes visible once those areas are listed
> together: ten of them ship a backend an operator cannot reach.

---

## 1. Legend

| Mark | Meaning |
|---|---|
| ● | Complete and verified by a gate. **"Gate" means `make check`/`make e2e`, run on the dev host via `scripts/ci-local.sh` — see §5.7: no GitHub Actions workflow has run on this repository since 2026-08-13** |
| ◐ | Implemented and tested, but with a stated limitation or an open follow-up |
| ○ | Specified, not implemented |
| — | Not applicable to this feature area |
| **HW** | Hardware-validation state: `V` validated on real PVE, `M` mock-validated only, `B` blocked (needs multi-node) |

"Verified" never means "a report said so". It means a test, a gate, or a command I ran against the artifact.

---

## 2. Feature-area matrix

| # | Feature area | Backend | GUI | API | Help | Docs | Unit tests | E2E | HW | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | Topology map (Switch + Graph view) | ● | ● | ● | ● | ● | ● | ◐ | M | Render budget mock-validated; e2e suite ungated (§5) |
| 2 | Entity inspector, live state, guest interior | ● | ● | ● | ● | ● | ● | ◐ | M | LXC interior read needs real container |
| 3 | Change engine (stage→validate→diff→apply→confirm) | ● | ● | ● | ● | ● | ● | ◐ | **B** | Multi-node apply/rollback unproven on hardware |
| 4 | Commit-confirm + unattended rollback | ● | ● | ● | ● | ● | ● | ◐ | **B** | Failure injection (T-1804) not yet run |
| 5 | Snapshots / time machine / restore | ● | ● | ● | ● | ● | ● | ◐ | M | Restore path mock-validated |
| 6 | Bridges, bonds, VLANs, interfaces | ● | ● | ● | ● | ● | ● | ◐ | M | LACP partner parse needs cross-kernel check |
| 7 | Guest NIC ops + bulk reattach | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 8 | Raw `/etc/network/interfaces` editor | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 9 | SDN cockpit (zones/VNets/subnets) | ● | ● | ● | ● | ● | ● | ◐ | M | EVPN anycast GW realization unverified |
| 10 | Guided zone wizards (5 types) | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 11 | EVPN/BGP health (FRR) | ● | ● | ● | ● | ● | ● | ○ | M | |
| 12 | DHCP / DNS (PowerDNS) management | ● | ● | ● | ● | ● | ● | ○ | M | |
| 13 | Visual IPAM + conflicts | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 14 | External subnets + NetBox/phpIPAM sync | ◐ | ● | ● | ● | ● | ● | ○ | **B** | Production write client unwritten; reports "not configured" |
| 15 | IPv6 planning grid + dual-stack wizard | ● | ● | ● | ● | ● | ● | ○ | M | |
| 16 | Firewall editor (3 scopes, objects) | ● | ● | ● | ● | ● | ● | ◐ | M | Resolve order is a documented simplification |
| 17 | Path simulator (4 verdicts) + verify-live | ● | ● | ● | ● | ● | ● | ● | M | In-guest probe command per OS unvalidated |
| 18 | Microsegmentation planner | ● | ● | ● | ● | ● | ● | ● | M | |
| 19 | Firewall log viewer | ● | ● | ● | ● | ● | ● | ● | M | Rule correlation heuristic, disclosed |
| 20 | Findings stream (15 sources, 43 checks) | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 21 | Drift detection (config-vs-live, node-vs-node) | ● | ● | ● | ● | ● | ● | ◐ | **B** | Node-vs-node needs 2+ real nodes |
| 22 | Metrics, sparklines, 24h history | ● | ● | ● | ● | ● | ● | ◐ | M | |
| 23 | Prometheus exporter + Grafana panels | ● | ● | ● | ● | ● | ● | ○ | M | |
| 24 | Flows (sFlow/NetFlow/IPFIX) + explorer | ◐ | ● | ● | ● | ● | ● | ● | **B** | eBPF sampler is probe+scaffolding only |
| 25 | Conntrack explorer | ● | ● | ● | ● | ● | ● | ● | M | |
| 26 | Latency mesh + paint mode | ● | ● | ● | ● | ● | ● | ○ | M | |
| 27 | Path MTU prober | ● | ● | ● | ● | ● | ● | ○ | M | |
| 28 | WAN & upstream health | ● | ● | ● | ● | ● | ● | ○ | M | |
| 29 | Edge & NAT cockpit | ● | ● | ● | ● | ● | ● | ○ | M | |
| 30 | Diagnosis ladder | ● | ● | ● | ● | ● | ● | ● | M | |
| 31 | Packet capture + BPF builder | ◐ | ● | ● | ● | ● | ● | ● | **B** | Real AF_PACKET backend unvalidated |
| 32 | LLDP discovery + ports view | ● | ● | ● | ● | ● | ● | ◐ | V | `lldpd` install/read validated (T-608) |
| 33 | MAC/FDB browser | ● | ● | ● | ● | ● | ● | ○ | M | |
| 34 | Blueprints (import, instantiate, sign) | ● | ● | ● | ● | ● | ● | ● | M | |
| 35 | Hub (signed registry client) | ● | ● | ● | ● | ● | ● | ○ | M | No hosted registry exists yet (T-2104) |
| 36 | Audit log | ● | ● | ● | ● | ● | ● | ● | M | |
| 37 | History timeline + playback | ● | ● | ● | ● | ● | ● | ● | M | |
| 38 | Doc export (Markdown/HTML) | ● | ● | ● | ● | ● | ● | ● | M | |
| 39 | Changeset review (comments, approval, share link) | ● | ● | ● | ● | ● | ● | ● | M | |
| 40 | Scheduled apply / maintenance windows | ● | ● | ● | ● | ● | ● | ○ | M | |
| 41 | Federation (multi-cluster) | ● | ● | ● | ● | ● | ● | ● | **B** | Never run against 2 real clusters |
| 42 | Cross-cluster IPAM conflicts | ● | ● | ● | ● | ● | ● | ○ | **B** | |
| 43 | WireGuard cluster interconnect | ● | ● | ● | ● | ● | ● | ● | **B** | |
| 44 | Switch config push (opt-in, 2-key) | ◐ | ● | ● | ● | ● | ● | ○ | **B** | Driver validated against mock switch only |
| 45 | PBS backup-path awareness | ● | ● | ● | ● | ● | ● | ○ | M | |
| 46 | Ceph network awareness | ● | ● | ● | ● | ● | ● | ○ | M | |
| 47 | Kubernetes overlay + flow attribution | ● | ● | ● | ● | ● | ● | ● | M | |
| 48 | SR-IOV VF lifecycle | ◐ | ● | ● | ● | ● | ● | ○ | **B** | Needs real SR-IOV NIC |
| 49 | Migration network planner | ● | ● | ● | ● | ● | ● | ○ | M | |
| 50 | Capacity forecasting | ● | ● | ● | ● | ● | ● | ○ | M | |
| 51 | Traffic baseline / anomaly detection | ● | ● | ● | ● | ● | ● | ○ | M | |
| 52 | Rogue-service / L2-anomaly detection | ● | ● | ● | ● | ● | ● | ○ | M | |
| 53 | MCP (AI operator surface) | ● | — | ● | ● | ● | ● | ○ | M | 9 tools, none mutating; guard-tested (§4) |
| 54 | Plugin SDK (5 extension points) | ● | ● | ● | ● | ● | ● | ○ | M | |
| 55 | Multi-tenancy + self-service | ● | ● | ● | ● | ● | ● | ○ | M | |
| 56 | HA active/standby | ● | ● | ● | ● | ● | ● | ○ | **B** | Failover never exercised on hardware |
| 57 | OIDC SSO | ● | ● | ● | ● | ● | ● | ○ | M | Against `oidcmock` only |
| 58 | Embeds (read-only tokens) | ● | ● | ● | ● | ● | ● | ● | M | |
| 59 | Automation tokens + webhooks | ● | ● | ● | ● | ● | ● | ○ | M | |
| 60 | Alert rules + PVE notification routing | ● | ● | ● | ● | ● | ● | ● | M | |
| 61 | Backup / restore of vnprox state | ● | — | ● | ● | ● | ● | — | **V** | Validated on pvecube 2026-08-05 |
| 62 | Support bundle export | ● | — | ● | ● | ● | ● | — | **V** | Secret-redaction validated with controls |
| 63 | Daemon self-observability (RED metrics) | ● | ● | ● | ● | ● | ● | ○ | M | |
| 64 | Retention / rotation / compaction | ● | — | ● | ● | ● | ● | — | M | |
| 65 | Peer-API CA pinning + verify-names | ● | ● | ● | ● | ● | ● | ○ | **V** | CA load + chain validated; name fix mock-tested |
| 66 | **Certificate management** (new) | ● | ● | ● | ● | ● | ● | ○ | **V** | Validated against real pvecube certs |
| 67 | **Online help** (new) | ● | ● | — | ● | ● | ● | ◐ | — | Coverage gate enforced by `make check` (see the legend note on where the gate runs) |
| 68 | Onboarding walkthrough | ● | ● | ● | ● | ● | ● | ● | M | |
| 69 | Keyboard shortcuts + command palette | ● | ● | — | ● | ● | ● | ● | — | |
| 70 | Responsive / narrow-viewport triage | ● | ● | — | ● | ● | ● | ● | — | |
| 71 | Accessibility (WCAG AA pass 1) | ● | ● | — | ● | ● | ● | ● | — | Second pass open (`T-2004`) |
| 72 | i18n | ○ | ○ | — | ○ | ○ | ○ | ○ | — | **Still not started** (`T-2006`) — zero code in the tree as of `v4.0.0`; rescheduled as `T-3106` (`docs/roadmap-earned.md`) |
| 73 | Mobile PWA + push | ● | ● | ● | ● | ● | ● | ○ | **V** | **Shipped** (`T-2005`, `v4.0.0`) — manifest, service worker, offline shell, web-push. **Row updated 2026-08-15: the shipped CSP (`worker-src 'none'; manifest-src 'none'`) blocks the service worker and manifest in a real browser**, so the feature could not actually run in production until `T-2901` (Phase 29) relaxes it. **HW V as of 2026-08-16, for the machine-checkable half only:** the Phase 29 package was deployed to `pvecube` and `vnproxctl verify -only pwa.servable` passed against it, with a `curl -D-` capture of the same node serving `worker-src 'none'` and a `text/plain` manifest taken minutes earlier. Push delivery to a real device (FCM/APNs/autopush), install on real iOS/Android, and the offline shell under airplane mode stay unverified and are the reason this row is not `●` in the E2E column — `planning/reports/needs-hardware-validation.md` §T-2901 |
| 74 | `vnproxctl` operator CLI | ● | — | ● | ● | ● | ● | — | **V** | `certs`, `backup`, `support-bundle` validated; `doctor`/`verify`/`telemetry` added since (rows 75/76 area) |
| 75 | `vnproxctl doctor` | ● | — | ◐ | — | ● | ● | — | ◐ | **Shipped** (`T-1904`, phase 19) — ten checks; `--live` (`T-2406`) answers `pve_reachable`/`pve_privileges` over `GET /doctor/live`. `clock_skew` and `peer_secret` still `skip` by design pending `T-2406-followup-01`/`-02` |
| 76 | Terraform provider / Ansible collection | ○ | — | ● | — | ● | ● | ○ | — | Contract published (`docs/automation-contract.json`, `T-2101`, `v4.0.0`) with a conformance suite runnable externally. **`terraform-provider-vnprox` and `ansible-collection-vnprox` still do not exist** as repositories — always scoped as separate, independently-published projects |
| 77 | Signed apt repository | ◐ | — | — | — | ● | ○ | — | — | **Machinery built, not hosted** (`T-2102`, `v4.0.0`: `packaging/build-apt-repo.sh`, `docs/../packaging/apt-repo.md`). `get.vnprox.io` does not exist; `install.sh` fails closed with no signing key to verify against. Rescheduled as `T-3301` |

**Totals:** 77 feature areas · **68 complete (●) · 6 partial (◐) · 3 not started (○)** on the backend axis.

---

## 3. Layer-by-layer coverage

| Layer | Measure | Value | Assessment |
|---|---|---|---|
| **Backend** | Go packages (`internal/` + `cmd/`) | 73 | — |
| | Production LOC | 138,136 | — |
| | Test LOC | 112,788 | 0.82 test:prod ratio |
| | Packages with tests | 68 / 73 (93%) | ● The 5 without are all mock/fixture servers (`pvemock`, `k8smock`, `switchmock`, `ingressmock`, `oidcmock`) |
| | Go tests (incl. fuzz) | 2,558 | ● |
| **Frontend** | Production LOC | 50,855 | — |
| | Test LOC | 23,740 | 0.47 test:prod ratio |
| | Feature modules | 38 | — |
| | Modules with tests | 38 / 38 (100%) | ● |
| | Vitest tests | 1,500 across 217 files | ● |
| | Routed screens | 26 | — |
| | Screens with help | 26 / 26 (100%) | ● Enforced by `web/src/help/coverage.test.ts` |
| | Help topics registered | 72 | ● Every one cites the repo doc it was written from |
| **API** | Route registrations | 186 | — |
| | Documented in `api.md` | 431 route mentions | ● Contract-frozen at v3.0 (additive-only) |
| | Changeset op types | 76 | — |
| | MCP tools | 9 | ● None mutating; enforced by a panicking guard test |
| **Data** | Schema migrations | 34 | ● Forward-only; chain validated on a real 3.7 MB store |
| **Docs** | Files / lines | 24 / 5,970 | ◐ One file materially stale (§5.4) |
| **E2E** | Playwright specs | 35 | ✗ **Run by no automated gate** (§5.1) |
| **Quality gate** | `make check` | lint + vet + 4,058 tests + govulncheck + npm audit | ● Exit 0 at this commit |
| **CI** | GitHub Actions | **disabled** (`disabled_manually` since 2026-08-13, billing exhausted 2026-08-11) | ✗ `CI`, `Packaging matrix`, and `Release` are all off; §5.7 records both the 2026-08-08 "actually it's running" correction and this later reversal — read it in full rather than by title |
| | `scripts/ci-local.sh` (the actual gate) | green at this commit | ● reproduces every job in `ci.yml`/`packaging-matrix.yml` on the dev host — CHANGELOG `[4.0.0]` |
| | `make ci` (local equivalent) | green at this commit | ● check + arm64 cross-build + 7 fuzz targets + package |
| | `Packaging matrix` (last runs) | 2 of last 3 red | ✗ `cluster-ssh` job only (§5.2) |
| **Validation** | Hardware-validated items | **6 / 123 (4.9%)** | ✗ **The single largest gap** (§5.3) |

---

## 4. Safety-invariant audit

The product's central claims, each checked against the code rather than the prose.

| Invariant | Where enforced | Verified how | Result |
|---|---|---|---|
| No network change bypasses the change engine | `internal/change` | All 76 op types route through `Apply`; no writer outside it | ● |
| An AI operator can read and draft, never apply | `internal/mcp/registry.go` | 9 registered tools, none mutating; `TestValidateRegistryPanicsOnMutatingTool` panics if one is added | ● |
| A plugin can stage but never apply | `internal/plugin/caps.go` | Capability ceiling; install rejects a write-adjacent point under a read-only scope | ● |
| External-IPAM writes never enter the change engine | `internal/ipam` | Regression test asserts the sync path never imports `internal/change` | ● |
| Peer TLS never falls back to the system trust pool | `internal/peer/trust.go` | Escape hatches need per-mode ack literals; unknown mode is fatal | ● |
| Peer TLS name resolution does not weaken the pin | `internal/certs/peername.go` | 3 adversarial tests (foreign CA, wrong node, wildcard) + a baseline test proving the fix changes behaviour | ● |
| Certificate scanning cannot read a private key | `internal/certs/scan.go` | Fixed filename allowlist; type carries no raw bytes; leak test with a planted marker + control | ● |
| Support bundle carries no secrets | `cmd/vnproxctl/bundlecmd.go` | Validated on a real install against the real session key and PVE token, with a control | ● **V** |
| Management-path changes cannot be scheduled | `internal/change` | Server-side, unconditional | ● |
| Approval is decided server-side, not by the UI | `internal/api/changesets.go` | Refused for UI, API, and CLI callers alike | ● |
| Help coverage is complete | `web/src/help/coverage.test.ts` | Parses the real router; mutation-tested 3 ways | ● |

No invariant failed. This is the strongest part of the codebase and it is strong because each claim has a test whose failure mode is loud.

---

## 5. Open defects and structural gaps

### 5.1 The e2e suite — gated 2026-08-06, green and **required** 2026-08-07 (`T-1806-bug-01` → `T-2108`, both closed)

35 Playwright specs existed with no `make` target and no CI job. There is now `make e2e` and an
`e2e` CI job, so the three-arc period where nothing ran them is over.

Turning it on immediately paid for itself. A full run found **29 failures against 59 passes**;
eleven triage passes took that to **89 passed / 0 failed / 2 skipped** (the two skips are
`microseg`'s own documented `test.skip`s), with run time down from 29.6 to ~10 minutes. The `e2e`
job is now required.

**Ten real product defects were found, all of which had been invisible while the suite ran in no
gate:**

| Defect | Detail |
|---|---|
| WCAG AA contrast, nav rail | Findings badge white on amber, **2.61:1** against a 4.5:1 requirement — on every page, which is why nine `a11y` specs failed identically |
| WCAG AA contrast, muted text | `dark:text-slate-500` on `dark:bg-slate-900`, **3.74:1**. `TopBar.tsx` already carried a comment describing this exact fix from `T-905`, applied once and never generalised |
| Spotlight results announce as one word | The kind badge was separated by an `ml-2` *margin*, which puts no whitespace in the accessible name: `"app01guest· pve1 name"` |
| Entity-node badge unreadable on tinted nodes | Contrast is measured against the node's own tint, not the page. Both halves of the usual muted pairing fail there — 1.84:1 and 3.7-4.4:1 |
| Entity-node **badge chip** at 4.35–4.39:1 | `dark:text-slate-300` on a translucent `slate-700/70` over a tinted node. Under 4.5:1 on some tints only, which is why it surfaced on the third full run and not the two before it |
| **Guest interior returned 400 to every browser request** | `T-1304`'s feature was unreachable from the SPA since the day it shipped: the only ref-taking handler that never `PathUnescape`d, while its own tests spelled the ref raw and stayed green |
| **The app could not navigate away from the Topology page** | `T-2003-bug-01`. A fresh `[]` literal per render fed an effect that set state upward — an unbreakable render loop that starved the `startTransition` react-router v7 wraps navigation in. URL changed, page did not, forever. Sole cause of three separate e2e failures |
| **The VXLAN zone wizard could not be completed** | Peer auto-suggest read a `fields` key and type the API has never sent, so every address input stayed empty and Next was permanently disabled — under copy promising the addresses were suggested automatically |
| **The VLAN wizard's LLDP trunk check warned on every neighbour** | Same wrong-field-shape bug, second site. Every neighbour reported an empty trunk, so the check reported the chosen VID as un-trunked everywhere, naming a blank switch and port. A false alarm is worse than no check |
| Flow records ingested at daemon start are unattributable **forever** | Resolution happens once, at ingest, with no retry, against an index that is empty until the first inventory poll lands — a silent up-to-15s hole after every restart |
| A decorative timeline marker swallowed changeset clicks | `disabled` finding markers share one 3px track with changeset markers and, being later in the DOM, painted over them — making the timeline's only actionable control unclickable |

Plus a **stale visual baseline** that had been failing since the commit that created it (`5909807`
added an `eno3` fixture NIC and never regenerated the snapshot) — the clearest possible evidence
that nothing was watching.

Two of the causes were the harness, not the product: the suite exhausts vnprox's own login
brute-force limiter (82 logins, three HTTP 429s), and specs mutate a shared daemon store, which
made previously-latent ambiguous locators start matching the wrong elements. Both fixed; the
structural half of the second is `T-2108-followup-01`.

**Four of those ten had green unit tests sitting on top of fixtures that invented the shape the code
expected.** That is the failure mode no amount of unit testing can catch, and the clearest argument
for keeping this job required.

### 5.2 `Packaging matrix / cluster-ssh` — **root-caused 2026-08-08** (`T-1806-bug-02` → `T-2410`)

`echo "$OUT" | grep -q PATTERN || die` fails **when the pattern MATCHES**, if `$OUT` exceeds the
pipe buffer and the match occurs early: `grep -q` exits at the first match, `echo` still has bytes
to write, the write returns `EPIPE`, `pipefail` turns that into a failed pipeline, and `|| die`
fires on a successful match.

The runner log for job `93012069994` shows the expected `URL: https://pve1:8008` line, then
`echo: write error: Broken pipe` on the asserting line, then the die — **the content was right and
the assertion failed anyway.**

The pipefail/SIGPIPE theory recorded on the card was therefore correct; what the three local
reproductions were missing was the *signal disposition*, not the size. Bash prints
`write error: Broken pipe` from a builtin only when SIGPIPE is **ignored**, which every Actions step
inherits from the Node-based runner. On a workstation SIGPIPE is fatal and the same race is silent.

Fixed by converting all seven instances to here-strings (no pipe, so no mechanism), with
`packaging/test/lib/sigpipe-guard.sh` failing the build if the pattern returns. See
`planning/tasks/phase-18.md`. **Three consecutive green runner runs (AC3) are still outstanding.**

### 5.3 Hardware validation: 15 of 151 items — the arc's whole premise

> **Recount, 2026-08-16 (T-2906).** This heading read "6 of 123" when the sweep was taken at
> `6c0957e`. Counting the `[x]`/`[ ]` marks in `planning/reports/needs-hardware-validation.md`
> today gives **15 validated, 136 open, 151 total** — the denominator moves as cards add items
> (Phase 29's `T-2901` added the PWA install/push items). The ratio, ~10%, is what the prose
> below is about and it has not materially changed.

| State | Count |
|---|---|
| Validated on real PVE | 6 |
| Mock-validated only | ~100 |
| Blocked (needs 2+ real nodes) | ~17 |

Five of the six were validated on 2026-08-05 (CA path, migration chain, `backup`, `support-bundle` redaction, cluster-secret pmxcfs permissions); the sixth is the earlier `lldpd` work. Everything marked **B** in §2 — multi-node apply, distributed rollback, node-vs-node drift, federation, HA failover, switch push — is unproven where it matters most. Phase 18's blocked cards (`T-1802`, `T-1803`, `T-1804`, `T-1808`) exist to close this and are the only items in the project that **an agent cannot do**.

**`T-2501` changes what "an agent cannot do" costs.** It does not validate anything — no card can, without hardware — but it removes the reason validation did not scale. `vnproxctl verify` is 26 checks in `internal/verify`, one per claim, each deciding its own verdict and carrying the API response, command output or file contents it rests on; the run produces a signed artifact rather than a message in a chat log. Every feature area marked **B** above has at least one check (enforced: `Reconcile` fails the build for a `B` or `V` row with nothing behind it), and every check states the hardware it needs, so `vnproxctl verify --list` is now the answer to "what would it take". Three properties keep the resulting number honest: a `skip` is never a `pass` and a run of nothing but skips exits non-zero, a verdict without evidence is a malformed report the CLI refuses to print, and the suite refuses to run against `internal/pvemock` at all without `--allow-mock`. What remains genuinely blocked is unchanged — somebody has to run it on iron.

### 5.4 `docs/features.md` is materially stale — **closed 2026-08-06 (`T-2107`), then updated again for Arc 5**

Historical note, kept for the record: this section used to report that `features.md` still described
the v1.0 feature set and listed five since-shipped capabilities as non-goals. `T-2107` rewrote it on
2026-08-06, and it now also carries an Arc 5 section. This paragraph is the only part of it that was
stale, and it was the irony of a staleness note about a staleness note — see `features.md`'s own
correction note at its top for the full history.

### 5.5 Open user-facing defects

| ID | Severity | Area | Summary |
|---|---|---|---|
| `T-2003-bug-01` | High → **FIXED 2026-08-07** | GUI | Root cause: an infinite render loop in `HistoryTimeline` (fed by a fresh `[]` literal per render from `useLiveFlowRecords`) starved the `startTransition` react-router v7 wraps navigation in — the URL changed and the page never did, for as long as the Graph view was mounted. The reported reproduction named the wrong trigger (the inspector is irrelevant), which is why the first regression spec passed against the live bug. Fixed, mutation-checked, and pinned at three levels; also the sole cause of three e2e failures. See `phase-20.md` |
| `T-2002-bug-01` | Medium | API | Frozen MCP payloads had no field-removal regression guard (guards added; card open for the general pattern) |
| `T-1807-bug-01` | Medium → **closed 2026-08-06** | Tooling | Test tooling assumed exclusive use of the machine. Closed by `T-1807-bug-02`'s enforced port registry — see §5.9 |
| `T-1806-bug-01` | High → **closed 2026-08-07** | Process | Gate landed and the backlog is triaged; the `e2e` job is required. See §5.1 |
| `T-1806-bug-02` | Medium | CI | See §5.2 |

### 5.6 Licensing — resolved 2026-08-06 (`T-2106`)

The repository had no license at all through 617 commits and three arcs. It is now **Apache-2.0**:
permissive, redistributable, with attribution carried by `NOTICE` as §4 requires.

Chosen over MIT for the explicit patent grant and the NOTICE mechanism, both of which matter for
infrastructure software with corporate users. Verified compatible: all 8 direct Go modules and all
117 production npm packages are permissive (MIT/ISC/BSD/Apache/0BSD), with two called out —
`elkjs` is EPL-2.0 and genuinely ships in the SPA bundle, and `dompurify` is dual-licensed with
Apache-2.0 elected. `THIRD-PARTY-LICENSES.md` is generated by `make third-party`, and
`internal/licensecheck` fails the build if any attribution file is dropped or emptied.

Proxmox VE's own AGPL-3.0 does not reach vnprox: interoperation is over the published HTTP API and
on-disk config only, with no linking.

### 5.7 ~~GitHub Actions is unfunded~~ — **corrected 2026-08-08: it is running**

**This section was wrong.** `gh run list` shows both `CI` and `Packaging matrix` executing on every
push through 2026-08-07 — including the `cluster-ssh` failure whose log finally root-caused
`T-1806-bug-02` (see §5.2). The claim that "no workflow runs on this repository" was carried
forward from an earlier period and never re-checked; it is corrected here.

The practical consequence of the error was not small: it is the reason `T-1806-bug-02` sat
unexplained for two days with a "reproduce under runner-like conditions" next step, when the
runner's own log was one `gh api` call away and contained the answer.

`make ci` still reproduces every job locally and remains the fastest gate for a working tree.

**Dated update, 2026-08-15, stated plainly rather than as another correction-to-a-correction:**
GitHub Actions billing was exhausted 2026-08-11. `CI`, `Packaging matrix`, and `Release` were
each set to `disabled_manually` on 2026-08-13. As of today, no GitHub Actions workflow runs on
this repository. `scripts/ci-local.sh` is the gate that actually runs, on the dev host
(CHANGELOG `[4.0.0]`, "Changed"). The heading above this paragraph is kept as written on
2026-08-08 rather than edited in place, because it was true when written — this paragraph is
the record of it becoming false again, not a rewrite of history. §3's CI row above states the
current fact without needing this section's history to make sense of it.

### 5.8 Partial implementations, honestly labelled

Six features are `◐` because a real backend is deliberately absent, and each says so in its own docs rather than pretending otherwise: external-IPAM production write client, eBPF flow sampler (probe + capability scaffolding only), packet-capture AF_PACKET backend, switch-driver hardware path, SR-IOV VF lifecycle, and the hub's hosted registry. None is mislabelled as complete anywhere in the shipped docs.

### 5.9 Port collisions — resolved 2026-08-06 (`T-1807-bug-02`)

One collision class produced five failures in a single phase, each first presenting as a product
defect. The fourth was the fix for the third: `T-1807-bug-01` moved a packaging test to 61007
"chosen outside the entire N8006/N8007 family", and 61007 is the phys-collapse e2e stack's vnproxd.
Commit `9047685` had to move it again.

That history is why the registry is **enforced, not documented**. `testdata/dev-ports.tsv` holds 21
rows; `internal/devports` runs seven checks in `make check`, including one that catches the case a
registry alone cannot — a *known* port bound by a second, independently-authored family of tooling,
which is exactly what `9047685` was. Replaying that commit's change now fails the build with a
message naming the owner. `packaging/test/lib/ports.sh` names the holding PID at runtime, and
`make ports` reports live status.

Building it surfaced three binds nobody had written down, two of them latent traps: `cluster-ssh.sh`
binds host sshd on 2201-2203 and asserts a fallback onto **8008**, the e2e suite's own `k8smock`
port; and `answers-parity.sh` depends in its own comment on 8007 being free while running
`--network=host`. Both now preflight.

### 5.10 Operator self-check — shipped 2026-08-06 (`T-1904`)

`vnproxctl doctor` closes the last agent-completable card in phase 19. Ten checks — config, key-file
permissions, pmxcfs, schema version, disk headroom, port conflicts, PVE reachability and
privileges, peer-secret agreement, clock skew — each of which names the file, port, privilege, or
command to fix. Read-only, and works with the daemon down.

Two properties are worth recording because they are the difference between a diagnostic and a
decoration:

- **A remediation is structurally required.** A `fail` or `warn` with no remediation is a malformed
  report; the CLI refuses to print it and exits with an internal error. It is not merely asserted in
  a test.
- **`skip` is not `pass`.** A check that could not run says why. Conflating "we did not look" with
  "we looked and it was fine" is how a green report hides a problem.

Building the install gate found a real defect in the first version: it failed every *correct*
install, because the session key does not exist until the daemon's first start. The check is now
state-aware, with a control test proving the same missing key after the daemon has run is still a
failure.

Four checks (`pve_reachable`, `pve_privileges`, `clock_skew`, `peer_secret`) are implemented and
tested but report `skip` from the CLI pending live-daemon wiring (`T-1904-followup-02` — also the
home for `T-1906-bug-01`'s certificate/SAN preflight), and `install.sh` reports rather than aborts
(`T-1904-followup-01`, deliberately blocked on `T-1806-bug-02`). Both stated in
`docs/deployment.md` rather than left to be discovered.

### 5.11 Arc 5 open items carried out of the e2e-sharding card (`T-2505`)

`T-2505` (phase 25) is closed with two items explicitly left open rather than faked closed —
both recorded on `planning/tasks/phase-25.md`'s delivery record, restated here so they show up in
the defect list a reader actually checks:

- **`scale.spec.ts › scale-lab (v2 canvas renderer)` is quarantined, not fixed**
  (`web/e2e/quarantine.json`, `T-2505-followup-01`, expires 2026-09-15). It fails only when it runs
  after two specific preceding specs in the same browser process — reproducible 4/4 in that
  arrangement, passing alone or in the full serial suite — and the mechanism is unexplained. An
  expired quarantine fails the build, so this either gets re-triaged or starts failing the gate on
  its own by the expiry date.
- ~~**The guest-interior panel does not refetch after its toggle is enabled**~~
  (`T-2505-followup-02`, found by `T-2505`'s two-core reproduction) — **FIXED in `v4.0.0`,
  commit `da58781` (2026-08-13); the diagnosis above was also wrong.** The symptom is as
  described: the panel issues its one `GET .../interior` read before the toggle flips on, gets a
  `404`, and stays showing "could not read this guest's interior" until the tab is remounted.
  Fast machines rarely see it, which is why the hosted runner failed this spec on a commit that
  passed locally. But it was **not** a missing cache invalidation — `onSuccess` always invalidated
  both query keys. The real cause was `useGuestInteriorQuery`'s `queryFn` resolving to
  **`undefined`** for the expected `interior_not_enabled` 404; TanStack Query v5 treats that as a
  caller bug, throws `"data is undefined"`, and parks the query in a synthetic `isError` state
  that no invalidation can clear. The sentinel is now `null`. Full evidence trail:
  `planning/reports/T-2505-followup-02.md`.

---

## 6. Method

Every figure above came from one of these, run at `6c0957e`:

```bash
# Code and test volume
find internal cmd -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1
find web/src \( -name '*.ts' -o -name '*.tsx' \) ! -name '*.test.*' | xargs wc -l | tail -1
go test ./... -list '.*' | grep -cE '^(Test|Fuzz|Example)'

# Coverage breadth
comm -13 <(find internal cmd -name '*_test.go' -exec dirname {} \; | sort -u) \
         <(find internal cmd -name '*.go' ! -name '*_test.go' -exec dirname {} \; | sort -u)

# Surface counts
grep -cE 'path="' web/src/App.tsx
grep -ohE 'r\.(Get|Post|Put|Delete|Patch)\("' internal/api/*.go | wc -l
grep -rohE '"(iface|bridge|bond|vlan|sdn|fw|guest|ipam|nat|route|qos|wg|switch)\.[a-z_.]+"' internal/change/*.go | sort -u | wc -l

# Card and validation state
grep -ohE '^#+ (T-[0-9]+[a-zA-Z0-9-]*)' planning/tasks/*.md | grep -oE 'T-[0-9]+[a-zA-Z0-9-]*' | sort -u
grep -oE '^\- \[[x ]\]' planning/reports/needs-hardware-validation.md | sort | uniq -c

# Gates
make check ; gh run list --limit 8
```

**What this audit does not establish.** It measures presence, structure, and gate state. It does not re-derive whether each feature's *behaviour* is correct — that rests on the automated tests (**4,058** at `6c0957e`; **5,358** as recounted 2026-08-16 — see `datasheet.md`), which themselves rest overwhelmingly on `internal/pvemock` rather than on real Proxmox. §5.3 is the honest boundary of everything else in this document.

---

## 7. Arc 5 / v4.0 feature areas §2 does not cover (stub, 2026-08-15; filled in 2026-08-16, T-2906)

§2's 77 rows were swept at `6c0957e` (`v3.0.4`). Phases 20–21 and 24–28 added the areas below and
§2 has no row for any of them. **This is deliberately a stub, not a re-audit.** It carries one
overall state mark and a pointer per area — the eight-column backend/GUI/API/help/docs/tests/e2e/HW
grid is *absent on purpose*, because filling it would mean inventing cells rather than deriving
them, which is exactly what this document's own method forbids. Re-running the mechanical sweep
against the current tree is `T-3204`'s to schedule, not this section's.

The one column that *is* derived mechanically, because it can be: **UI** — whether a `web/src`
client for the area exists at all (`ls web/src/<area>`, `grep -rl` across `*.tsx`). It answers
"can an operator reach this without curl", which is the specific question
`docs/roadmap-earned.md`'s Phase 30 was scoped from.

| Area | Card | State | UI | Pointer |
|---|---|---|---|---|
| Scheduled automatic config snapshots | `T-2401` | ● | yes | `planning/tasks/phase-24.md` delivery record |
| Finding acknowledgement and mute | `T-2402` | ● | yes | phase-24 record; `vnprox_findings_acked` metric |
| Entity change history ("blame") | `T-2403` | ● | yes | `GET /inventory/history?ref=` |
| Blast-radius preview before apply | `T-2404` | ● | yes | `GET /changesets/{id}/impact` |
| OpenAPI 3.1 document + completeness gate | `T-2405` | ● | — | `docs/openapi.json`, 250 ops / 211 paths; **no body schemas** — stated in `docs/api.md` |
| `vnproxctl doctor --live` | `T-2406` | ◐ | — | §2 row 75; two checks still `skip` |
| Alert quiet hours + digest coalescing | `T-2407` | ● | yes | `web/src/settings/AlertRules.tsx` |
| Batch-fix findings into one changeset | `T-2408` | ● | yes | phase-24 record |
| Per-spec e2e store isolation | `T-2409` | ◐ | — | Built, missed 2 of its 4 ACs, parked on a branch — phase-24 second pass |
| Hardware-validation suite (`vnproxctl verify`) | `T-2501` | ● | — | `docs/deployment.md`; refuses to run against a mock |
| Record/replay real PVE traffic into fixtures | `T-2502` | ● | — | phase-25 |
| Opt-in compatibility telemetry | `T-2503` | ● | — | `vnproxctl telemetry`; separate from the mock compat matrix by design |
| Nightly soak / resource-leak gate | `T-2504` | ● | — | phase-25 |
| E2E sharding, isolation, flake quarantine | `T-2505` | ◐ | — | **Two ACs explicitly unmet**; one spec quarantined, expiry **2026-09-15** — §5.11, `planning/reports/T-2505-followup-01.md` |
| Performance regression budget gate | `T-2506` | ● | — | phase-25 |
| Policy-as-code guardrails at validate | `T-2601` | ● | no | `vnproxctl policy`; **no `web/src` client** |
| Canary / staged multi-node apply | `T-2602` | ◐ | no | Backend complete and reachable; **API/CLI only** — `T-3005`. The claim that it "returns 501 by default" is false; see that card's note |
| Finding-triggered auto-rollback | `T-2603` | ◐ | no | API/CLI only — `T-3005` |
| Two-person rule on protected op classes | `T-2604` | ◐ | partial | The approval gate is enforced in `ReviewApplyScreen.tsx`; **break-glass has no button** — `POST /changesets/{id}/break-glass` is route-only |
| Post-apply topology preview | `T-2605` | ● | yes | `GET /changesets/{id}/preview` |
| Git-backed spec sync | `T-2701` | ● | no | `[gitsync]`; **no `web/src` client** |
| Changeset → pull request | `T-2702` | ● | no | `POST /changesets/{id}/propose`; no UI |
| Drift-to-git reconciliation | `T-2703` | ● | yes | `POST /drift/{id}/restore-intent` |
| Point-in-time topology diff | `T-2704` | ● | yes | `GET /topology/diff` |
| MCP stage-only AI operator surface | `T-2705` | ● | — | Compile-time non-apply guarantee (`internal/mcp/stageonly.go`) |
| Compliance control mapping | `T-2706` | ● | no | Unmapped controls report `unmapped`, never `pass`; **no `web/src` client** |
| Demo mode / one-command install | `T-2801` | ● | yes | `vnproxd --demo`; `web/src/demo` |
| Hosted read-only demo | `T-2802` | ◐ | yes | Edge + tour built; **no instance hosted** — `T-3303` |
| Hosted signed registry | `T-2803` | ◐ | yes | Format + publisher tooling built; **no instance hosted** — `T-3303`. §6.4 of `project-status.md` marks this `●`; that disagrees with its own note text and with `T-2802`/`T-2104` — see `planning/tasks/phase-28.md`'s record |
| Incident mode | `T-2804` | ● | yes | `web/src/incidents`, `IncidentsPage.tsx` |
| Advisory entity locks + presence | `T-2805` | ◐ | yes | Node-local only; cross-node presence fan-out unbuilt — `T-3201` |
| Map annotation layer | `T-2806` | ● | yes | phase-28 record |
| Scheduled digest reports | `T-2807` | ◐ | partial | `GET`/`PUT /digest/schedule`; schedule is set through the API, no dedicated screen |
| In-app assistant over MCP read tools | `T-2808` | ● | yes | `web/src/assistant`; no backend configured by default |
| PWA CSP / embed frameability | `T-2901` | ● | yes | §2 row 73; `pwa.servable` verify check |
| Peer host-write validation + audit IP | `T-2902` | ● | — | Migration 0047; `planning/tasks/phase-29.md` |
| Bearer `read_only` + token expiry | `T-2903` | ● | — | Migration 0048; expiry never retroactive |
| Hub endpoint containment | `T-2904` | ● | — | `resolvePluginEndpoint`, symlink-escape tested |
| Hardening punch list | `T-2905` | ● | — | Session sweep, webhook SSRF policy, HTTP timeouts, config 0640 |
| Documentation truth pass | `T-2906` | ● | — | This section is part of it |

**What this stub says that §2 cannot.** Ten of the 40 areas are `◐`, and the largest single cause
is not incompleteness — it is unreachability. **Ten areas ship a working backend that an operator
cannot reach from the product**: six have no `web/src` client at all (`T-2601` policy-as-code,
`T-2602` canary apply, `T-2603` auto-rollback, `T-2701` git spec sync, `T-2702` changeset→PR,
`T-2706` compliance mapping), two are reachable only in part (`T-2604`'s break-glass, `T-2807`'s
schedule), and two are built but have no hosted instance (`T-2802`, `T-2803`). Four of those ten
are nonetheless marked `●` here, because the card delivered exactly what it promised — the gap is
the product's, not the card's. That distinction is the whole reason Arc 6's Phase 30 exists, and
none of it is visible in §2's grid, which predates all of it.
