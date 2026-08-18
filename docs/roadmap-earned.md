# Arc 6 — Earned

**Status: proposed 2026-08-15.** Scoped from a four-dimension audit (features, docs, security,
planning leftovers) of the tree at tag `v4.0.0` (`5c19dcc`, released 2026-08-14). The five arcs
before this one are described in [`roadmap.md`](roadmap.md) (Phases 0–7, v1.0),
[`roadmap-next.md`](roadmap-next.md) (8–12, v2.0), [`roadmap-universal.md`](roadmap-universal.md)
(13–17, v3.0), [`roadmap-proven.md`](roadmap-proven.md) (18–21, v3.1 → v4.0, shipped as v4.0.0),
and [`roadmap-adopted.md`](roadmap-adopted.md) (25–28, v3.5.0), with Phases 22–24
([`roadmap-leverage.md`](roadmap-leverage.md)) between them.

Arc 4 asked *"is it true?"* and Arc 5 asked *"can anyone else run it?"* Arc 6 asks the question
both of them left on the table: **"is everything we already claim actually true, visible, and
public?"** — because the audit's organising finding is that vnprox's remaining distance to its
own README is not new features. It is claims running ahead of reality in four specific ways.

**This document supersedes the open-items sections of every earlier roadmap.** Every item any
prior arc deferred, cut with intent to return, or shipped-with-gaps is either absorbed by a card
below (see the consolidation ledger, §"Where every leftover went") or explicitly retired (§"Not
carried"). There is no other backlog.

## What the audit found

| Dimension | Finding | Evidence anchor |
|---|---|---|
| **Truth** | The shipped CSP (`worker-src 'none'; manifest-src 'none'`, `internal/api/middleware.go:84`) predates T-2005 and blocks service-worker registration and the manifest — **the v4.0.0 PWA/push feature cannot work in a production browser**. Embed views are similarly dead under global `frame-ancestors 'none'`. Both fail safe; neither works as documented. | `internal/api/middleware.go:70-86` |
| **Safety** | `/api/peer/host/*` writes `/etc/network/interfaces` and reloads with **no safety validation, no interlocks, no audit row** on the receiving side (`internal/peer/server.go:946`). `read_only = true` does not constrain bearer tokens. Hub install `exec`s a registry-supplied endpoint string, and `trustUnsigned` is a request field. | security audit §b1–b3 |
| **Visibility** | **~19 backend feature areas have zero frontend client** (gitsync, spec import, policies, compliance, digests, webhooks, tokens, HA, doctor-live, failsim, WAN, capacity, PBS, QoS, tenant approval, SR-IOV, canary). For a product whose identity is "visual", a headless feature is half-shipped. *(The audit also claimed canary apply "returns 501 by default"; that was re-checked against the code on 2026-08-16 and is false — see the `T-3005` note in Phase 30.)* | features audit §b1 |
| **Coverage** | PVE 9's SDN Fabrics are **not modeled at all**. *(This row originally read "(`openfabric`/`ospf`) … the compat matrix gates on them" — both halves were false, found 2026-08-16 by querying pvecube: those are fabric **protocols**, not zone types, and the matrix gate was asserting a divergence that does not exist. See `planning/tasks/phase-31.md`.)* SDN controllers are a string field, not objects. VNet-scope firewall is unmodeled. `faucet` zones and `forward` firewall rules are **rejected by vnprox though real PVE accepts them**. README promises "**all** Proxmox networking". | `planning/reports/evidence/pve-9.2.4-sdn-schema.txt` |
| **Proof** | **127 open hardware-validation items vs 15 validated (~11%)**, everything validated from one single-node box. The blocked register (T-1803) that Arc 4 named as the authoritative ledger was never written. Commit-confirm — the product's central safety claim — has never been observed self-healing on real iron (T-1804). | `planning/reports/needs-hardware-validation.md` |
| **Public existence** | CI disabled (billing), a `v*` tag publishes nothing, repo private, docs site not enabled, no security-disclosure contact, no apt repo host, no hosted registry, no demo instance, no Terraform/Ansible repos, forum announcement still a draft. External consumers: **0**. | `CHANGELOG.md:146-158`, `docs/README.md:11-19` |
| **Docs currency** | `project-status.md` and `status-matrix.md` frozen two releases back; three docs still assert "CI runs on every push" (now false); `datasheet.md` is at 3.0.4 and lists shipped features as "not yet shipped"; 21 docs unreachable from any index; 57 of 60 `T-2xxx` cards have no report; the v4.0.0 release note claims "phases 20 and 21 complete" while T-2006 (i18n) and T-2102 (apt repo hosting) never shipped and were never descoped on the record. | docs audit §1–§8 |

## Stale claims — do not re-add

The audit also surfaced items other documents report as missing that **already ship**. If a
future report claims one of these is absent, the report is reading a stale doc:

| Claimed missing | Reality |
|---|---|
| Standalone map SVG/PNG export ("T-607 gap", `docs/features.md:32`) | Ships as `web/src/topology/ExportMapMenu.tsx` (T-2404); the features.md row is stale |
| Ports/LLDP frontend page (`docs/features/lldp-discovery.md:16`) | Ships as `web/src/ports/PortsPage.tsx`, routed at `/ports` |
| Failure-impact analysis / posture score / traffic anomaly detection | Ship as `internal/failsim` / `internal/posture` / `internal/baseline` (Arc 5 already re-dropped these once) |
| Prometheus metrics endpoint | Ships, bearer-gated, `internal/api/metrics_exporter.go` |

## The five phases

| Phase | Theme | Cards | Release | Question it answers |
|---|---|---|---|---|
| **29** | Make v4.0 true | 6 | v4.1 | Does everything the release notes claim actually work — and is it safe? |
| **30** | The visible product | 6 | v4.2 | Can an operator reach every shipped capability from the UI? |
| **31** | All of Proxmox networking | 6 | v4.3 | Does "all PVE networking" include what PVE 9 ships today? |
| **32** | Proven on iron | 4 | v4.4 | Can the 11% hardware-validation figure become a majority? |
| **33** | In the world | 3 | v5.0 | Does vnprox exist for anyone who isn't its developer? |

Phase 29 comes first because several of its items are live defects or open security gaps in the
release currently deployed; everything after it builds on a release whose claims are true.
Phase 32 before 33: nothing goes public carrying a safety claim that has never been observed
working.

**One hard external deadline, phase order notwithstanding:** the `scale.spec.ts` quarantine
entry (`web/e2e/quarantine.json`) **expires 2026-09-15 and an expired quarantine fails the
build**. T-3204 must resolve or consciously renew it before that date regardless of where
Phase 32 sits in the schedule.

## Invariants carried forward

Unchanged, and not renegotiated by anything below:

- **Proxmox stays the source of truth.** Nothing here adds authoritative state to the store.
- **Every mutation flows through the change engine.** T-2902 exists precisely because one
  internal channel currently violates the spirit of this rule; the fix is to extend the
  guarantee to the receiving side of the peer API, not to add a second write path.
- **Cluster-aware by default.** T-3201's two-node environment makes this testable for the first
  time; cards that were "cluster-aware by code inspection" become cluster-aware by observation.
- **A guardrail that cannot fail is not a guardrail.** Every fix in Phase 29 ships with the
  test that proves the old behavior was wrong (the CSP fix ships a test that registers a real
  service worker; the peer-write fix ships a test that watches an unvalidated write get refused).
- **Proof is not self-report.** Phase 32 closes items by artifact (`vnproxctl verify` output,
  recorded cassettes), never by narrative.

## The twenty-five cards

### Phase 29 — Make v4.0 true → v4.1

| # | Card | Item | Pri |
|---|---|---|---|
| 1 | `T-2901` | Un-break the PWA and the embeds: CSP/headers vs shipped features | P0 |
| 2 | `T-2902` | Peer host-write safety parity + audit attribution (incl. source IP) | P0 |
| 3 | `T-2903` | Bearer tokens honor `read_only`; token expiry; `token.use` flood control | P0 |
| 4 | `T-2904` | Hub plugin install hardening (endpoint constraints, config-gated trust) | P0 |
| 5 | `T-2905` | Auth/daemon hardening punch list (sessions, limiter, SSRF, timeouts, perms) | P1 |
| 6 | `T-2906` | Documentation truth pass + single doc index | P1 |

**`T-2901`** — `securityHeadersMiddleware` still ships the T-604 policy whose comment asserts
"no Worker()/service worker … no web app manifest"; T-2005 made both false. Allow
`worker-src 'self'` and `manifest-src 'self'`, correct the comment, and add a per-route
relaxation of `frame-ancestors`/`X-Frame-Options` for the three `/embed/*` views (which exist
to be iframed and currently cannot be, `docs/security.md:34-37`). Acceptance is end-to-end:
a real browser against a real daemon registers `sw.js`, fetches the manifest, and passes the
install criteria; an embed renders inside a same-origin test iframe. This card also closes the
open half of T-2005's release note: push delivery verified through at least one real push
service to one real device via pvecube.

**`T-2902`** — `POST /api/peer/host/stage-interfaces|ifreload|restore|discard-staged` bypass
every interlock the product documents as absolute. Enforce `DetectProtected`/safety validation
on the receiving side, append audit rows with originating node + user attribution threaded
through the peer envelope, and add the source-IP column the audit contract
(`docs/security.md:451`) already claims exists — for every mutating handler, not just auth's.
The peer channel stays HMAC+CA-pinned; this card removes its status as a privileged shortcut.

**`T-2903`** — `forceReadOnly` is applied to cookie sessions only; a pre-existing write-scoped
bearer token retains full write capability in a read-only deployment
(`internal/auth/middleware.go:176`). Apply the filter in `authenticateBearer`, add optional
`expiresAt` at mint (default 90 days), and aggregate `token.use` audit rows (currently one row
per authenticated request, unbounded).

**`T-2904`** — `exec.Command(m.Endpoint)` on a registry-supplied string, with signature
verification bypassable by a `trustUnsigned` request field (`cmd/vnproxd/hubinstall.go:114`,
`internal/api/hub.go:348`). Constrain endpoints to absolute paths under a vnprox-owned install
root (no `..`, no symlink escape, no `$PATH` resolution); move `trustUnsigned` from request
body to a config flag that warns at startup, matching the `[peer] tls_trust` precedent.

**`T-2905`** — The remainder of the security audit as one card, each item small and
mechanical: session expiry sweeper + stop renewing past the 12h hard cap; login-limiter map
sweep/cap and IP-before-username charging (memory DoS + targeted lockout); webhook target
SSRF guard (deny loopback/RFC1918/link-local by default, opt-in override); HTTP server
`ReadTimeout`/`WriteTimeout`/`IdleTimeout`; constant-time CSRF compare; `--` guard in the MTU
prober argv + host validation on `PUT /wan/targets`; config file installed 0640 (it can hold
`dev_ticket_password`); startup `WARN` for every active dev knob; systemd `UMask=0077` +
`RestrictNamespaces`; and confirm the `nsenter` guest-interior path even works under the
shipped `SystemCallFilter` (the audit suspects it is already broken). Also gate
`PUT /guests/{ref}/interior-toggle` on a write capability — it is currently a write gated on
`netRead`.

**`T-2906`** — Make the documentation stop lying by omission. Rewrite `project-status.md` §1–§5
and `status-matrix.md`'s header for v4.0.0 and collapse the append-only snapshot accretion;
correct the three "CI runs on every push" claims (`project-status.md:20`,
`status-matrix.md:143`, `:279` — the last is a double-negation trap); update `datasheet.md`
(at 3.0.4, listing shipped features as unshipped) and `README.md` ("shipped through v3.5.0");
record T-2006 and T-2102 as **descoped from the v4.0.0 "phases complete" claim and rescheduled
here** (as T-3106 and T-3301); mark the five shipped roadmap docs' status lines shipped and
note wherever `v3.1/v3.2/v3.3` appear that those tags were never cut; index the 21 orphaned
docs and reconcile `docs/README.md` with `_sidebar.md`; create the referenced-but-nonexistent
`T-2505-followup-0{1,2}` report files; add a "Upgrading v3.x → v4.0" section to
`deployment.md` and extend its migration list (ends at 0034) to 0046. Backfill of the 57
missing per-card reports is satisfied by inline delivery records per phase file (the
phase-24/25 pattern), not 57 retro-written documents.

### Phase 30 — The visible product → v4.2

| # | Card | Item | Pri |
|---|---|---|---|
| 7 | `T-3001` | Config-as-code cockpit: gitsync, spec import/pin, drift reconciliation UI | P0 |
| 8 | `T-3002` | Governance surfaces: policies, compliance, two-person approvals, digests | P1 |
| 9 | `T-3003` | Platform panel: tokens, webhooks, plugins, HA status, doctor-live | P1 |
| 10 | `T-3004` | Analysis surfaces: failsim/SPOF, WAN health, capacity, PBS, QoS editing | P1 |
| 11 | `T-3005` | Canary apply: implement the default path and give it a UI | P0 |
| 12 | `T-3006` | Help completion: panel anchors, field-level help, panel-aware coverage gate | P1 |

The organising rule for 30: **a backend feature without a UI is not shipped in this product.**
Each of T-3001–T-3004 takes a cluster of API-only features (features audit §b1: 19 areas with
zero `web/src` clients) and gives them their screens, wired to the routes that already exist —
these are assembly cards, not design-from-scratch cards. `docs/status-matrix.md` marks most of
these GUI `●` today; that column becomes true rather than aspirational.

**`T-3005`** is called out separately because staged/canary apply is headless while
`docs/features.md:54` lists it as shipped P0. **Correction, 2026-08-16 (T-2906):** the audit that
produced this card said the route "returns 501 by default" (`internal/api/changesets.go:578`).
**That is wrong, and the card must not be implemented against it.** The 501 is behind a type
assertion (`svc.(StagedApplyService)`) and fires only for a changeset service that does not
implement `ApplyStaged` — a test-double escape hatch. The production wiring injects
`*change.Service`, which implements all three staged-apply methods (`internal/change/apply.go:92`),
so the backend works. The real gap is **UI only**: surface staged apply in the changeset review
screen and add the rollout-state view. Scope the card to that.

**`T-3006`** absorbs `T-2202-followup-01` (20+ help topics with no `?` anchor at their own
panel), `T-2202-followup-02` (field-level inline help in entity editors), and the deliberate
gap recorded at v4.0.0: the help coverage gate is route-derived and blind to panel-level
features — exactly how T-2005 shipped with zero help. Extend the gate's inventory to panels so
the next T-2005 fails a test instead of an audit.

### Phase 31 — All of Proxmox networking → v4.3

| # | Card | Item | Pri |
|---|---|---|---|
| 13 | `T-3101` | SDN Fabrics: model, editors, topology overlay (PVE 9.x `openfabric`/`ospf`) | P0 |
| 14 | `T-3102` | SDN controllers as first-class objects (evpn/bgp/isis CRUD) | P1 |
| 15 | `T-3103` | Firewall fidelity: VNet-scope firewall, IP-level rule-effects preview, real resolution order | P1 |
| 16 | `T-3104` | IPAM completion: next-free everywhere, IPAM plugin CRUD, production external-IPAM write client | P1 |
| 17 | `T-3105` | Restore & rename fidelity: OVS bond restore, NIC renaming, ingress write support | P2 |
| 18 | `T-3106` | Localization (i18n) — the rescheduled T-2006 | P2 |

**`T-3101`** — the sharpest intent gap in the audit: a PVE 9.x user cannot see or manage SDN
Fabrics from the product that promises "the SDN stack".

**This card's original scoping was wrong and was rewritten on 2026-08-16 against captured
hardware** (`planning/reports/evidence/pve-9.2.4-sdn-schema.txt`). It said: *"`validSdnZoneTypes`
stops at `evpn`; `openfabric`/`ospf` exist only inside `pvemock`'s compat gate — model the zone
types, add wizard + editors + map overlay, and extend the compat matrix rows from 'rejected
correctly on 8.2' to 'managed on 9.x'."* Every clause after the first is built on a false premise.
`openfabric`/`ospf` are **not SDN zone types** on PVE 9 — the real 9.2 zone enum is
`<evpn | faucet | qinq | simple | vlan | vxlan>`. Fabrics are a separate API family,
`/cluster/sdn/fabrics`, and `openfabric`/`ospf` are two of four fabric *protocols*
(`bgp | openfabric | ospf | wireguard`). Modelling them as zone types would have shipped the
wrong object graph, wizard, and map overlay, and the compat matrix row it names was itself
asserting a divergence that does not exist (now corrected — see `docs/compatibility.md`).

The card as it now stands is in [`planning/tasks/phase-31.md`](../planning/tasks/phase-31.md).
It also picks up two defects the same capture exposed: `faucet` is a real zone **and** controller
type vnprox rejects, and `/cluster/sdn` carries `prefix-lists` and `route-maps` families vnprox
does not model at all.

**`T-3102`** — controllers today are a string field on a zone (`internal/sdn/service.go:77`).
First-class objects with CRUD ops (`sdn.controller.*`) through the change engine, shown in the
SDN tree, with EVPN/BGP status attached to the controller rather than inferred.

**`T-3103`** — absorbs two documented simplifications: VNet-scope firewall (PVE 8.2+) missing
from the scope model (`internal/inventory/entity.go:686`), and the rule-effects preview being
guest-level only with a documented divergence from pve-firewall's real chain traversal
(`docs/features/firewall.md:7,13`). The simulator's answer should match what pve-firewall does,
not a simplification of it.

**`T-3104`** — absorbs the `docs/features/ipam.md:21` gap (next-free picker wired only into
the bridge editor; VLAN/interface/subnet-gateway fields still lack it), adds CRUD for PVE IPAM
plugin entries, and productionizes the external-IPAM write client (one of the six deliberately
absent backends, `docs/status-matrix.md:49` — the read path ships, writes report
"not configured").

**`T-3105`** — three restore-fidelity debts: time-machine restore silently cannot re-create
OVS bonds (`internal/change/restore_ops.go:175` — inventory doesn't carry the `ovs_bridge`
attachment; carry it); NIC renaming's partial implementation has three unvalidated hardware
behaviours (udev+reboot realization, guest re-binding, VLAN child cascade — validation half
lands in Phase 32); and reverse-proxy ingress write support, read-only since Arc 3 deferred it
"in this arc" (`docs/roadmap-universal.md:146`) and no arc since picked it up.

**`T-3106`** — T-2006 verbatim: the single Arc-4 roadmap item with zero code in the tree.
Rescheduled here rather than silently dropped; if it is instead descoped permanently, that
decision gets written down in T-2906's truth pass and this card is closed as retired.

**Landed 2026-08-17, as scaffolding rather than a localized product — that distinction is the
whole point of writing this down rather than leaving the card "in progress" forever.** The
framework is `react-i18next` v17 + `i18next` v26, wired at the app root (`web/src/main.tsx`'s
`<I18nextProvider>`) with statically-bundled JSON resources (Vite's `resolveJsonModule`, never a
runtime/CDN fetch — the service worker and CSP constraint both hold). One bounded subset is
actually localized: `web/src/onboarding/OnboardingWalkthrough.tsx`, chosen for being small,
self-contained (no copy dependency on any other feature area), and representative (a real
`<Trans>`-mediated interpolation for its inline `<code>lldpd</code>` sentence, real i18next
plural forms for its severity counts). `web/src/onboarding/i18nCoverage.test.ts` is the
anti-regression gate — a source-scanning Vitest test in the same style as
`web/src/help/coverage.test.ts` and `web/src/ipam/nextFreeCoverage.test.ts`, scoped to that one
subset's files, that fails the build on a new hardcoded JSX text node or label-ish string prop.
English is the only *shipped* locale: `fr` exists only to prove the pipeline round-trips
end-to-end (`web/src/onboarding/i18nLocale.test.tsx` asserts rendered text actually changes on
`i18n.changeLanguage("fr")`), is machine-quality and unreviewed, and is reachable from nowhere a
real user's browser can put it — no language auto-detection, no switcher UI. **What this does not
claim:** the other ~76 rows of `docs/status-matrix.md` §2 remain entirely unlocalized: every other
screen, panel, toast, and dialog in this 273-test-file frontend still hardcodes English. Extending
coverage to a new area means adding that area's own `i18n` namespace
(`web/src/i18n/i18n.ts`'s doc comment names the pattern) and its own coverage-gate glob, not
growing the onboarding namespace past its own screen — "localize the UI" stays an arc, not a card,
and this card does not pretend otherwise.

### Phase 32 — Proven on iron → v4.4

| # | Card | Item | Pri |
|---|---|---|---|
| 19 | `T-3201` | Second node + the blocked register: cross-node validation for real | P0 |
| 20 | `T-3202` | Failure-injection proof of commit-confirm + validation burndown | P0 |
| 21 | `T-3203` | Scale & performance on real cluster data (T-1808 → T-1907 threshold) | P1 |
| 22 | `T-3204` | Test-debt closure: quarantine, flake, isolation, frozen-payload guards | P1 |

**`T-3201`** — stand up a second PVE node beside pvecube and finally write
`planning/reports/blocked-validation.md`, the register Arc 4 named as the authoritative ledger
of what remains unproven (T-1803 — referenced by `implementation-plan-proven.md:26`, never
created). With two nodes: peer API round trips, node-vs-node drift, distributed rollback
timers, federation transport, cross-node presence fan-out (T-2805's stated gap), the two
remaining `doctor` checks (`clock_skew` needs a PVE server-time surface, `peer_secret` needs a
peer digest route — `T-2406-followup-0{1,2}`), and `T-1906-bug-01` (the stale-IP-SAN
certificate failure observed once and never confirmed) all become observable. Absorbs T-1802's
burndown mechanics for every cross-node section of `needs-hardware-validation.md`.

**`T-3202`** — T-1804 verbatim: break connectivity mid-change on real hardware and watch
commit-confirm self-heal. The product's headline guarantee has passed every mock test and has
never once been observed on iron. Plus the record/replay hardware half (`T-2502-followup-01`:
are real PVE list responses order-stable?), first real-hardware cassettes replacing the
mock-recorded ones, and a hardware-validated row in the compat matrix (T-2103's open half).
Also closes `T-1904-followup-01`: `install.sh` aborts (not reports) on failing doctor — its
blocker was resolved in Arc 4 and nobody went back.

**`T-3203`** — T-1808 verbatim: real per-node port counts and guest densities, then re-derive
`DefaultPhysicalCollapseThreshold = 8`, provisional since T-1907 with a written promise to
revisit (`planning/reports/T-1907.md:70`). Re-baseline the perf budgets on hardware rather
than the 32-core dev host (`T-2505-input-02`'s standing evidence that every timing criterion
is host-relative).

**`T-3204`** — the accumulated test debt, owned in one place: root-cause the quarantined
`scale.spec.ts` ordering failure (**quarantine expires 2026-09-15**, `T-2505-followup-01`, 4/4
reproducible, mechanism unexplained); the `simulator.spec.ts` T-504 AC5 flake (37.5% and the
suite's worst); revive the parked `t-2409-e2e-store-isolation` branch to meet T-2505's unmet
AC3 (`--repeat-each=2` green — shard-level isolation was proven weaker than per-run); and
`T-2002-bug-01`: field-removal regression guards for the 8 of 9 frozen MCP tool payloads, 5
frozen plugin-SDK interfaces, and the event-stream schema that currently have none. OpenAPI
request/response body schemas (T-2405's gap) ride along so the contract gate can see them.

### Phase 33 — In the world → v5.0

| # | Card | Item | Pri |
|---|---|---|---|
| 23 | `T-3301` | Distribution that works: CI decision, signed apt repo at a real host, release publishing | P0 |
| 24 | `T-3302` | Public presence: repo, docs site, security contact, forum announcement | P0 |
| 25 | `T-3303` | Hosted instances + ecosystem: demo, registry, Terraform/Ansible | P1 |

**`T-3301`** — done, 2026-08-18. The CI decision is made and on the record
(`docs/development.md`'s CI section): hosted Actions is retired, not paused — `scripts/ci-local.sh`
(`make ci` for the fast subset) is the permanent gate, enforced by `.githooks/pre-push`
(`make install-hooks`) rather than GitHub's `required_status_checks`, which would be unsatisfiable
now that nothing posts them. `main` carries the branch protection this repo can actually use
(force-push/deletion disabled, `enforce_admins` on) instead. T-2102's `packaging/build-apt-repo.sh`
machinery is now behind a real host: `apt.vnprox.com` (pve001, `packaging/apt-repo.md`), signed
with a real production Ed25519 key that lives only on that host (not a GitHub Actions secret —
Actions doesn't run releases anymore). `packaging/publish-release.sh` is the manual release-cut
flow replacing `release.yml`'s job: builds both arches, signs+publishes the apt repo, stamps
`openapi.json`/`automation-contract.json`, regenerates the compat matrix, and cuts a GitHub
release. This leaves T-2410's three-consecutive-green criterion permanently unmeetable **by
Actions runs**, on purpose — the gate that matters now is local; that AC is superseded by this
decision, not still open. Not yet done: `apt.vnprox.com` doesn't resolve publicly — pending the
VPS reverse-proxy leg shared with T-3303 — and no `vX.Y.Z` release has been cut through the new
flow yet (the repo's own version tag is still `v4.0.0`, from before this phase).

**`T-3302`** — the T-2105 remainder, all human-gated, none of it code: make the repo public
(the git-history binary blobs were left in history by T-2411's decision — revisit only if
publishing forces it), enable the already-built docsify site, publish a security-disclosure
contact (an audit finding in its own right: there is no way to report a vulnerability), and
post the forum announcement that has sat in draft.

**`T-3303`** — done, 2026-08-18: the hosted demo is live at `demo.vnprox.com` (pve001,
`vnprox-demo-public.service`) — the login limiter's `(IP, username)` keying gained a real
production knob for a shared public username (`[server] login_rate_username_capacity` /
`login_rate_username_refill_seconds`, `internal/auth.RateLimitByUsername`), and
`T-2801-followup-01` is resolved for plain `vnproxd --demo` (`demo.go`'s `demoReadOnlyPosts` —
`internal/publicdemo`'s hosted edge deliberately stays method-blind, a decision re-confirmed, not
revisited). Standing this up on a real PVE host also found and fixed a real bug nothing had
exercised before: `certs.Service` had no `cfg.Demo` gate on its `/etc/pve` scan root, so the first
time demo mode ran anywhere `/etc/pve` genuinely exists it leaked real node names into a
supposedly synthetic public demo's findings — see `resolveCertsRoot`
(`planning/reports/T-3303-demo-mode-real-host-isolation.md` has the full account, including what's
contained by deployment posture rather than fixed at the code level yet). The hosted signed
registry is live at `registry.vnprox.com` with all four T-2104 seed blueprints actually published
through the real `vnproxctl hub publish`/`hub index` pipeline (not a test fixture), and the
DMZ+WireGuard seed's missing `wg.*` blueprint entity kind is closed
(`blueprint.KindWgTunnel` — tunnel only, not peer; a peer needs a remote-site public key
exchanged out of band, so that stays a separate step). `terraform-provider-vnprox` /
`ansible-collection-vnprox` are seeded (contract-pointer READMEs, Apache-2.0) — real repos,
no provider/module code yet; that implementation is real, separate future work, not rushed
alongside standing up the public infrastructure. Not yet done: `apt.vnprox.com` /
`demo.vnprox.com` / `registry.vnprox.com` don't resolve publicly — pending the VPS reverse-proxy
leg of T-3301/T-3303.

## Where every leftover went

Every open item the audit found in prior roadmaps, phase files, and reports, and the card that
absorbs it:

| Leftover | Source | Absorbed by |
|---|---|---|
| T-2005 push unverified on real device; PWA install unverified | `CHANGELOG.md:141` | T-2901 |
| Embed views unusable under global frame-ancestors | `docs/security.md:34` | T-2901 |
| Audit rows lack the source IP the docs claim | `docs/security.md:451` | T-2902 |
| ~~T-2006 localization (zero code)~~ — **fixed 2026-08-17**: `react-i18next`/`i18next` wired, `web/src/onboarding/` localized with a source-scanning anti-regression gate, `fr` stubbed to prove the pipeline round-trips; ~76 other feature areas remain unlocalized by design (see the T-3106 write-up above) | `planning/tasks/phase-20.md:195` | T-3106 |
| T-2102 signed apt repo (machinery built, no host) | `roadmap-proven.md:249` | T-3301 |
| T-2104 hosted registry PARTIAL: no instance, no `wg.*` blueprint kind | `CHANGELOG.md:100` | T-3303 |
| T-2105 remainder: private repo, no docs site, no security contact, draft announcement | `CHANGELOG.md:115` | T-3302 |
| T-1802 hardware-validation burndown (127 open / 15 done) | `roadmap-proven.md:91` | T-3201 / T-3202 |
| T-1803 blocked register never written | `roadmap-proven.md:98` | T-3201 |
| T-1804 failure-injection proof of commit-confirm | `roadmap-proven.md:111` | T-3202 |
| T-1808 scale validation on real cluster data | `roadmap-proven.md:144` | T-3203 |
| T-1907 provisional collapse threshold (pending T-1808) | `planning/reports/T-1907.md:70` | T-3203 |
| T-1904-followup-01 (install.sh aborts on failing doctor) | `planning/tasks/phase-19.md:224` | T-3202 |
| T-1904-followup-02 / T-2406-followup-01/-02 (doctor `clock_skew`, `peer_secret`) | `planning/tasks/phase-24.md:290` | T-3201 |
| T-1906-bug-01 stale-IP-SAN peer TLS failure | `needs-hardware-validation.md:29` | T-3201 |
| T-2002-bug-01 frozen-payload guards (8 MCP tools, 5 SDK interfaces, event stream) | `planning/tasks/phase-18.md:552` | T-3204 |
| T-2108-followup-01 / T-2409 per-spec store isolation (parked branch, 2 ACs unmet) | `planning/tasks/phase-24.md:307` | T-3204 |
| T-2505 AC3 (`--repeat-each=2`) unmet | `planning/tasks/phase-25.md:746` | T-3204 |
| T-2505-followup-01 quarantined scale.spec failure — **expires 2026-09-15** | `planning/tasks/phase-25.md:824` | T-3204 |
| simulator.spec T-504 AC5 flake (37.5%) | flake ledger 2026-08-14 | T-3204 |
| T-2405 OpenAPI body schemas + disabled-subsystem blind spot | `planning/tasks/phase-24.md:310` | T-3204 |
| T-2502-followup-01 hardware half (real-PVE order stability) | `needs-hardware-validation.md:1161` | T-3202 |
| T-2103 compat matrix 100% mock-validated | `CHANGELOG.md:84` | T-3202 |
| T-2410 AC3 three consecutive green runner runs (blocked by disabled CI) | `docs/project-status.md:218` | T-3301 |
| T-2202-followup-01/-02 help anchors + field-level help | `planning/tasks/phase-22.md:156` | T-3006 |
| Panel-level help coverage gate (named follow-up at v4.0.0) | `CHANGELOG.md:196` | T-3006 |
| T-2801-followup-01 read-shaped POSTs in demo mode | `docs/features/demo-mode.md:85` | T-3303 |
| T-2802 hosted demo: no instance, limiter keying gap | `docs/features/demo-mode.md:258` | T-3303 |
| T-2805 presence/locks node-local; peer fan-out unfilled | `docs/project-status.md:283` | T-3201 |
| Terraform provider / Ansible collection ("published separately", Arc 2) | `roadmap-next.md:180` | T-3303 |
| Ingress write support (deferred "in this arc", Arc 3) | `roadmap-universal.md:146` | T-3105 |
| SR-IOV VF lifecycle: no UI, needs real NIC | `docs/status-matrix.md:83` | T-3004 (UI) / T-3202 (hw) |
| eBPF flow sampler / AF_PACKET capture / switch-driver hardware paths | `docs/status-matrix.md:59,66,79` | T-3202 (validation targets; backends stay scoped as-is) |
| Next-free IPAM picker only in bridge editor | `docs/features/ipam.md:21` | T-3104 |
| IP-level firewall rule-effects preview | `docs/features/firewall.md:13` | T-3103 |
| ~~OVS bond restore incomplete (missing attachment in inventory)~~ — **fixed 2026-08-16**: `inventory.Bond.OVSBridge` carries it; the refusal narrowed to snapshots taken before the field existed | `internal/change/restore_ops.go` | T-3105 |
| NIC renaming: 3 unvalidated hardware behaviours | `needs-hardware-validation.md:411` | T-3105 / T-3202 |
| WireGuard wizard "pick a target cluster" extension (post-T-1407) | `docs/features/topology.md:106` | T-3001 |
| Corosync ring status local-node only | `docs/features/monitoring.md:48` | T-3201 |
| 57 missing T-2xxx reports; T-2505-followup files referenced but nonexistent | docs audit §7 | T-2906 |
| status-matrix ~24 missing Arc-5/v4.0 rows; stale T-2505-followup-02 row | docs audit §1 | T-2906 |

## Not carried

Retired deliberately, with the reason on the record. Do not resurrect without new evidence:

| Item | Why |
|---|---|
| Post-confirm revert of a committed changeset | Cut on contact with code in Phase 24 (`roadmap-leverage.md:19`) — revert-by-new-changeset is the supported path |
| Management-IP re-addressing | Out of scope **by construction** (T-203 interlock); the interlock is the feature |
| Certificate renewal/reissue | A decision, not an omission — PVE owns it (`planning/tasks/phase-23.md:154`) |
| Git-history rewrite for tracked binary blobs | T-2411 chose untrack-forward; revisit only if T-3302's publishing forces it |
| Full non-root daemon operation | Post-1.0 note that no arc ever scheduled (`docs/features.md:164`). Still unscheduled: the 7-capability bounding set + syscall filter is the accepted posture. Revisit after v5.0 if the public repo draws contributors who need it |
| Suppression semantics for findings; retention changes | Phase 24 non-goals (`roadmap-leverage.md:104`), still non-goals |

## What "done" means for this arc

v5.0 cuts when: the deployed release's documented features all work in a production browser;
no internal channel can mutate host networking without validation and audit; a second node has
turned the cross-node sections of the validation ledger from `[ ]` to `[x]` or into named
blocked-register entries; commit-confirm has been watched saving a cluster at least once; and
a stranger can install vnprox from a public apt repo, read its docs on a public site, report a
vulnerability to a published address, and try it on a hosted demo. The README's claims and the
tree's contents describe the same product — which is what "earned" means.
