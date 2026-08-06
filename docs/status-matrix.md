# vnprox — full-stack audit matrix

**Audit date:** 2026-08-06 · **Commit:** `6c0957e` · **Released:** `v3.0.4` · **Deployed:** `3.0.4+43+g6c0957e` (pvecube)

This is a mechanical sweep of the whole stack: feature area × backend × GUI × API × docs × tests × hardware validation. Every figure below is derived from the repository at the commit above by a command recorded in the *Method* section, not from a task report's own claim about itself.

Companion documents: [`project-status.md`](project-status.md) (open items, percent complete, roadmap) and [`datasheet.md`](datasheet.md) (shipped capability, for external readers).

---

## 1. Legend

| Mark | Meaning |
|---|---|
| ● | Complete and verified by a gate that runs on every push |
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
| 39 | Changeset review (comments, approval, share link) | ● | ◐ | ● | ● | ● | ● | ● | M | `T-2003-bug-01`: nav dead-end after inspector close |
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
| 67 | **Online help** (new) | ● | ● | — | ● | ● | ● | ◐ | — | Coverage gate enforced on every push |
| 68 | Onboarding walkthrough | ● | ● | ● | ● | ● | ● | ● | M | |
| 69 | Keyboard shortcuts + command palette | ● | ● | — | ● | ● | ● | ● | — | |
| 70 | Responsive / narrow-viewport triage | ● | ● | — | ● | ● | ● | ● | — | |
| 71 | Accessibility (WCAG AA pass 1) | ● | ● | — | ● | ● | ● | ● | — | Second pass open (`T-2004`) |
| 72 | i18n | ○ | ○ | — | ○ | ○ | ○ | ○ | — | Not started (`T-2006`) |
| 73 | Mobile PWA + push | ○ | ○ | ○ | ○ | ○ | ○ | ○ | — | Not started (`T-2005`) |
| 74 | `vnproxctl` operator CLI | ● | — | ● | ● | ● | ● | — | **V** | `certs`, `backup`, `support-bundle` validated |
| 75 | `vnproxctl doctor` | ○ | — | — | ○ | ◐ | ○ | — | — | Not started (`T-1904`) |
| 76 | Terraform provider / Ansible collection | ○ | — | ● | — | ◐ | ○ | — | — | API contract exists; artifacts unpublished (`T-2101`) |
| 77 | Signed apt repository | ○ | — | — | — | ◐ | ○ | — | — | Not started (`T-2102`) |

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
| **CI** | GitHub Actions | **not running — unfunded** | ✗ The gate is now `make ci` on a dev host (§5.7) |
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

### 5.1 The e2e suite — gated 2026-08-06, and it is red (`T-1806-bug-01` → `T-2108`)

35 Playwright specs existed with no `make` target and no CI job. There is now `make e2e` and an
`e2e` CI job, so the three-arc period where nothing ran them is over.

Turning it on immediately paid for itself. A full run found **29 failures against 59 passes**; five
triage passes took that to **9 failures against 78 passes**, with run time down from 29.6 to 16.1
minutes.

**Four real product defects were found, all of which had been invisible while the suite ran in no
gate:**

| Defect | Detail |
|---|---|
| WCAG AA contrast, nav rail | Findings badge white on amber, **2.61:1** against a 4.5:1 requirement — on every page, which is why nine `a11y` specs failed identically |
| WCAG AA contrast, muted text | `dark:text-slate-500` on `dark:bg-slate-900`, **3.74:1**. `TopBar.tsx` already carried a comment describing this exact fix from `T-905`, applied once and never generalised |
| Spotlight results announce as one word | The kind badge was separated by an `ml-2` *margin*, which puts no whitespace in the accessible name: `"app01guest· pve1 name"` |
| Entity-node badge unreadable on tinted nodes | Contrast is measured against the node's own tint, not the page. Both halves of the usual muted pairing fail there — 1.84:1 and 3.7-4.4:1 |

Plus a **stale visual baseline** that had been failing since the commit that created it (`5909807`
added an `eno3` fixture NIC and never regenerated the snapshot) — the clearest possible evidence
that nothing was watching.

Two of the causes were the harness, not the product: the suite exhausts vnprox's own login
brute-force limiter (82 logins, three HTTP 429s), and specs mutate a shared daemon store, which
made previously-latent ambiguous locators start matching the wrong elements. Both fixed; the
structural half of the second is `T-2108-followup-01`.

Nine failures remain and the job is still `continue-on-error`. **Until it is required, a green `CI`
badge still does not mean the e2e suite passed.**

### 5.2 `Packaging matrix / cluster-ssh` is red on the runner only — `T-1806-bug-02`

Red on 2 of the last 3 pushes; the `debian:12` and `debian:13` matrix jobs are green, and the same job passes locally under podman. A pipefail/SIGPIPE theory was written, tested at 100 KB / 1 MB / 5 MB, **failed to reproduce, and was reverted rather than shipped speculatively**. Unexplained. Blocks tagging a release with confidence.

### 5.3 Hardware validation: 6 of 123 items — the arc's whole premise

| State | Count |
|---|---|
| Validated on real PVE | 6 |
| Mock-validated only | ~100 |
| Blocked (needs 2+ real nodes) | ~17 |

Five of the six were validated on 2026-08-05 (CA path, migration chain, `backup`, `support-bundle` redaction, cluster-secret pmxcfs permissions); the sixth is the earlier `lldpd` work. Everything marked **B** in §2 — multi-node apply, distributed rollback, node-vs-node drift, federation, HA failover, switch push — is unproven where it matters most. Phase 18's blocked cards (`T-1802`, `T-1803`, `T-1804`, `T-1808`) exist to close this and are the only items in the project that **an agent cannot do**.

### 5.4 `docs/features.md` is materially stale

It still describes the v1.0 feature set and lists as **explicit non-goals** five things that have since shipped: NetFlow/sFlow collection, PBS networking, multi-cluster federation, physical switch config push, and the Prometheus exporter. A reader taking it at face value would form a wrong picture of the product. It is the only doc in this state — `api.md`, `security.md`, `user-guide.md`, and the roadmaps are all current.

### 5.5 Open user-facing defects

| ID | Severity | Area | Summary |
|---|---|---|---|
| `T-2003-bug-01` | High → **reproduces (correction, 2026-08-06)** | GUI | Previously recorded as unreproducible. That verdict came from running `nav-after-inspector.spec.ts` **standalone**; in the full suite it **fails**. A regression spec verified only in isolation has not been verified, because isolation is the one condition the reported bug did not occur under. See `T-2108` triage |
| `T-2002-bug-01` | Medium | API | Frozen MCP payloads had no field-removal regression guard (guards added; card open for the general pattern) |
| `T-1807-bug-01` | Medium → **closed 2026-08-06** | Tooling | Test tooling assumed exclusive use of the machine. Closed by `T-1807-bug-02`'s enforced port registry — see §5.9 |
| `T-1806-bug-01` | High → **partially closed** | Process | Gate landed; backlog triage is `T-2108`. See §5.1 |
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

### 5.7 GitHub Actions is unfunded — the gate moved to the dev host

No workflow runs on this repository at present. That silently inverts what a commit without a red
check means: previously "CI passed", now "nothing looked". `make ci` reproduces all four jobs
locally (`make check`, arm64 cross-build, seven fuzz targets, package build) and is green at this
commit. `docs/development.md` states this at the top of its CI section so nobody infers a pass from
an absence.

The `e2e` workflow job added for `T-1806-bug-01` is therefore inert until funding returns; `make
e2e` is the live path, and the suite it runs is red (§5.1).

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

**What this audit does not establish.** It measures presence, structure, and gate state. It does not re-derive whether each feature's *behaviour* is correct — that rests on the 4,058 automated tests, which themselves rest overwhelmingly on `internal/pvemock` rather than on real Proxmox. §5.3 is the honest boundary of everything else in this document.
