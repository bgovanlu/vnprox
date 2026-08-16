# Changelog

All notable user-facing changes to vnprox are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
vnprox uses semantic versioning; the SQLite schema migrates forward-only.

Versions up to v1.0 correspond to the milestones in `docs/roadmap.md`;
v2.0 is the milestone cut of the second arc in `docs/roadmap-next.md`
("Beyond the cluster"); v3.0 is the platform cut of the third arc in
`docs/roadmap-universal.md` ("The open platform"); **v3.5.0 is the cut of
Arc 5, `docs/roadmap-adopted.md` ("Adoptable, not just proven", phases
25–28), and v4.0.0 is the end-of-arc cut of Arc 4, `docs/roadmap-proven.md`
("Proven in production", phases 18–21) — which is why the two are out of
document order.** `docs/roadmap-leverage.md` (phase 24) and the
out-of-structure phases 22–23 fold into the v3.5.0 line. **`v3.1`, `v3.2`
and `v3.3` were never tagged**, so `roadmap-proven.md`'s "v3.1 → v4.0"
title is a plan, not a ledger. Phase 3 ("Discovery &
true cluster") does not have its own version cut per the roadmap — its
functionality (peer clustering, LLDP discovery, drift detection, FDB
browsing) shipped as part of the v0.8 development cycle and is listed under
v0.8 below, alongside that release's SDN/IPAM work.

Precise dates are given for the v1.0.0, v2.0.0, and v3.0.0 release cuts;
earlier milestones predate this file and are dated only by year.

**Note on v1.1.0–v1.3.6:** these tags (2026-07-12 to 2026-07-15) were interim
development checkpoints cut while the v1.4 → v2.0 arc was in progress; they
don't have dedicated entries below because the `v2.0.0` tag wasn't applied
until the whole arc — including phases 13–15 of the *next* arc, see the note
on `[2.0.0]` below — had already merged onto the same branch. Their
functionality is folded into `[2.0.0]`.

## [Unreleased]

Phase 29 ("Make v4.0 true", `docs/roadmap-earned.md`) — every entry below closes a
verified defect or security gap in v4.0.0; none adds a feature.

### Fixed

- **`vnproxctl verify`'s PWA check now runs on a node that has no API token — which is every
  freshly installed node (T-2901 follow-up, found deploying Phase 29 to real hardware).**
  `pwa.servable` read its root prober off the authenticated daemon client, so on a node with no
  bearer token minted it reported `0 passed, 0 failed, 1 skipped` and printed a message telling
  the operator to supply a `--token` that none of its three fetches needs — the app shell, the
  manifest and `sw.js` are all unauthenticated. **The one check written to detect the v4.0.0 CSP
  defect therefore skipped on exactly the deployments that had it.** `verify.Deps` gained a
  `Root` seam fed by an anonymous prober built from the daemon URL alone. The fix is
  mutation-verified, and the check now passes against a real node — see
  `planning/reports/needs-hardware-validation.md` §T-2901, which records the before/after
  capture from `pvecube`.
- **Two `vnproxctl verify` hardware checks were not checking what they claimed, both found by
  running the suite on a real node (2026-08-16).** `backup.archive_round_trip` decoded
  `sizeBytes`/`includedKeys` while the CLI emits `bytes`/`includesKeyMaterial`, so it called every
  healthy node's backup a "0-byte archive" — and, more seriously, its assertion that a backup must
  not contain key material without `--include-keys` read an absent field and **could never fail**.
  `peer.ca_pins_real_chain` demanded the dial address appear in the leaf's SAN list, which is the
  rule T-2303 deliberately replaced: a PVE-issued certificate covering the node name is now
  verified against that name, so the check reported `T-1906-bug-01`'s failure mode against a node
  where the fix for it was working. Its evidence was also rendered with `firstLine()`, which
  printed an empty SAN list for a certificate carrying six, because `openssl` puts the values on
  the line after the header. Both checks now pass on `pvecube`; the backup contract is pinned by a
  golden captured from the CLI itself, and both broken unit fixtures — which had encoded the same
  wrong expectations as the checks — were corrected.
- **The v4.0.0 PWA actually works in a production browser now (T-2901).** The shipped CSP
  still pinned `worker-src 'none'; manifest-src 'none'` from before the PWA existed, so
  service-worker registration and the manifest fetch were refused in any real browser — the
  installable app and push notifications shipped dead, and only synthetic-subscription tests
  ever ran. Both directives are now `'self'`, the manifest is served as
  `application/manifest+json`, and `web/e2e/pwa.spec.ts` asserts registration, manifest
  type, and embed frameability in real Chromium. `vnproxctl verify` gains `pwa.servable`,
  which detects this defect class on a live deployment.
- **The `/embed/*` views are actually embeddable (T-2901).** They existed to be iframed and
  the global `frame-ancestors 'none'` + `X-Frame-Options: DENY` forbade exactly that. They
  now carry `frame-ancestors 'self'` plus whatever origins `[server] embed_frame_ancestors`
  lists (validated at startup; default same-origin only).

### Security

- **Peer host writes are validated and audited on the receiving side (T-2902).**
  `POST /api/peer/host/{stage-interfaces,restore}` previously handed content straight to the
  host writer — holding the cluster secret was a validation-free, audit-free path to any
  node's `/etc/network/interfaces`. The receiving node now runs its own change-engine
  pipeline (parity with a local changeset, asserted in one table), with restores of
  snapshot-known content exempt by provenance so distributed rollback keeps working, and
  every host write is audited locally with coordinator attribution. Migration 0047 adds the
  `audit_log.ip` column docs/security.md always claimed; `GET /audit` items now carry `ip`.
- **`read_only = true` now constrains bearer tokens (T-2903).** A write-scoped token minted
  before the flag flipped previously kept full write capability. Tokens also expire now:
  migration 0048 adds `expires_at`, newly minted tokens default to 90 days (explicit
  `expiresAt: null` opts out), and an expired token gets the same 401 as a revoked one —
  never applied retroactively to existing tokens. `token.use` audit rows are aggregated
  hourly instead of per-request. The CSRF compare is constant-time.
- **Hub plugin install is containment-checked (T-2904).** A registry manifest's `endpoint`
  must resolve — symlinks included — to a regular file inside `/var/lib/vnprox/plugins`
  (never `$PATH`), and trusting an unsigned artifact now requires
  `[hub] trust_unsigned = true` in server config (startup WARN) — the request flag alone is
  refused with a 403 naming the key. Signed-artifact verification is unchanged.
- **Webhooks lost their blind-SSRF shape (T-2905).** Deliveries reach public https targets
  only by default; loopback/RFC1918/link-local (metadata addresses included) and plain http
  are refused at registration and re-checked against the resolved address at every dial.
  `[webhooks] allow_private_targets` / `allow_insecure_targets` opt out per class, loudly.
- **Auth lifecycle hygiene (T-2903/T-2905).** Sessions past idle/hard expiry are now
  actually swept (taking their push subscriptions via the 0046 cascade), and a session at
  the 12h hard cap stops having its PVE ticket renewed. The login limiter's bucket map is
  bounded (it grew forever on attacker-supplied usernames). The HTTP server gained
  Read/Write/Idle timeouts. `PUT /guests/{ref}/interior-toggle` now requires `netWrite`
  (was `netRead`). The MTU prober's `ping` argv carries a `--` guard; WAN targets are
  validated as IP/hostname. The packaged config installs 0640 (it can hold
  `dev_ticket_password`); active dev knobs WARN at startup; the systemd unit adds
  `UMask=0077` and `RestrictNamespaces=~user` (and the guest-interior `setns` path was
  verified allowed under the existing syscall filter, by inspection).

## [4.0.0] - 2026-08-14

> **Correction, 2026-08-16 (T-2906) — the entry below is left as published; this note says what it
> got wrong.** "Phases 20 and 21 are complete" was **not accurate for two of the seven cards it
> claimed**. `T-2006` (localization) was never implemented. `T-2102` (signed apt repository) shipped
> its tooling and signing pipeline but **not the hosting** — there is no published repository, so
> `apt install vnprox` still resolves to nothing. Arc 4's real delivery is **24 of 26 cards**, not
> 26. Both are rescheduled into Arc 6 (`docs/roadmap-earned.md`) as `T-3106` and `T-3301`.
> Separately, the mobile PWA listed under *Added* below **could not function in any production
> browser as released** — the CSP still carried `worker-src 'none'; manifest-src 'none'` from
> before the PWA existed, so service-worker registration and the manifest fetch were refused; only
> synthetic-subscription tests had ever exercised it. Fixed in `[Unreleased]` above (`T-2901`).

**Phases 20 and 21 are complete, which is what makes this 4.0.** `docs/roadmap-proven.md` reserves
`v3.3` for phase 20 ("Sharper daily use") and `v4.0` for phase 21 ("Ecosystem and reach"); v3.5.0
shipped with three cards outstanding in the former and four in the latter, and named that fact in
its own release note. Those seven have now landed, so this release takes the number the roadmap set
aside for the end of the arc rather than another point release. `v3.1`, `v3.2` and `v3.3` were never
tagged — phases 18 and 19 shipped inside the v3.0.x line, and Arc 5 took `v3.5.0` — so the
phase-to-version map has always been a plan rather than a ledger; this is the point where following
it and describing what shipped agree.

**What 4.0 does not mean.** No breaking change to the API, the data model, or the on-disk store: the
schema migrates forward as always (0046 is the newest), `docs/api.md`'s contract is unchanged, and
an upgrade from 3.x is an ordinary package upgrade. The major number marks the completion of the
proven-in-production arc, not an incompatibility.

### Added

- **The API contract has something on the other side of it, ready for a downstream repo to consume
  — the provider and collection themselves stay outside this repository.** T-1106 froze the
  automation contract; T-2101 publishes it. `docs/automation-contract.json` is a versioned,
  machine-readable manifest of the 11 automation routes at stability v1.7, kept in step with
  `docs/api.md` by a golden test and configured for publication as a release asset alongside
  `openapi.json` — though see the CI note under *Changed*: the workflow that would publish it is
  currently disabled, so no tag actually emits that asset today. The
  conformance scenarios now run unchanged against either the in-process stack or an already-running
  `vnproxd` (`VNPROX_CONFORMANCE_BASE_URL`), authenticating over real HTTP and minting bearer
  tokens through `POST /tokens` with the CSRF double-submit flow — closing a gap in T-1106's own
  suite, which had never exercised that route over the wire. `make conformance-external` is the one
  canonical invocation, and `ci.yml` carries a job for it — which, per the *Changed* note below,
  means it runs locally via `scripts/ci-local.sh` and nowhere else at present. **`terraform-provider-vnprox` and `ansible-collection-vnprox`
  do not exist yet**: T-1106 always scoped those as separate, independently-published repositories,
  and this card's deliverable is the cross-repo wiring they would consume, not the providers
  themselves. One divergence is recorded rather than smoothed over: `touchesMgmtPath` depends on an
  LLDP-identified uplink the minimal in-process harness never wires, so it always reads `false`
  there while a fully-wired daemon can return `true` — redacted in external mode, with the reason
  in the code.

- **A PVE compatibility matrix, generated from mock fixtures rather than hand-maintained — and
  labelled as exactly that.** `docs/roadmap.md` has long promised "a compatibility validation task
  within one phase of each new PVE release" with no mechanism behind it; T-2103 is that mechanism.
  Three version profiles (8.2, 9.0, 9.2) run through a compat-server wrapper that gates the one
  documented, checkable divergence between them — PVE 9.0's SDN Fabrics zone types
  (`openfabric`/`ospf`), rejected with a PVE-shaped 400 on 8.2 — alongside a baseline smoke check
  (ticket auth, a network read, an ordinary VLAN zone create) run per version. `make compat-matrix`
  regenerates `docs/compat-matrix.json` and the published table in `docs/compatibility.md` from
  those runs; nothing in either file is typed by hand.

  **Every cell says `validation: mock`, and none of this is hardware-validated.** The one real data
  point this repository has — a single-node deployment note in
  `planning/reports/needs-hardware-validation.md` — is deliberately *not* folded into the table,
  because mixing a hardware observation into a mock-generated row is exactly the blur this document
  exists to prevent. `docs/compatibility.md` points at `vnproxctl verify`/`vnproxctl telemetry`
  (v3.5.0) as the separate, field-validated pathway instead. The SDN Fabrics behaviour itself is
  modelled from Proxmox's documentation, not captured from hardware — this repository has no PVE
  8.2 or 9.0 to observe, and the docs say so rather than implying otherwise.

- **A plugin's declared capabilities are now checked against what the registry actually listed —
  before a signature or a trust decision ever gets asked.** v3.5.0 shipped the Hub's signed
  registry (T-2803); T-2104 fills in the two things `docs/hub-registry.md` had already named as
  outstanding. First, a real gap: `GET /hub/index` is what an operator reads before deciding to
  install, but nothing enforced that the downloaded artifact's manifest actually matched that
  listing — a registry could advertise one capability scope and deliver another, and the consent
  an operator gave would have been for something they were never shown. Installing a plugin now
  refuses on any scope disagreement, unconditionally, audited as `hub.install/denied`, with no
  trust flag able to override it: a correctly signed artifact from a fully trusted signer is still
  refused, because a valid signature proves who produced a manifest, not that it matches the
  listing that was consented to. Second, content: four seeded blueprints (a homelab single-node, a
  three-node Ceph cluster over a VXLAN overlay, a VLAN-segmented branch office, and a DMZ with
  WireGuard site-to-site), each tested against `internal/pvemock`. The DMZ+WireGuard seed is marked
  **PARTIAL** in its own description, matching the bundled EVPN starter's existing caveat, because
  blueprint v1 has no `wg.*` entity kind. **There is still no hosted registry** — no domain, no
  object storage, no publish job — so these seeds are real, tested content that isn't published
  anywhere `[hub] registry_url` could currently reach.

- **A front door for the documentation, and an honest accounting of what distribution
  infrastructure does and doesn't exist yet.** T-2105 adds a docsify-based, zero-build docs site
  (`docs/README.md`, `_sidebar.md`, CDN-loaded, no new toolchain) restructured for a reader rather
  than a contributor: an install guide, a first-hour walkthrough, a support guide, and
  `CONTRIBUTING.md` with issue templates. Every install path in it was checked against source
  rather than retyped — make targets against the `Makefile`, flags against `install.sh`'s own usage
  text — and the apt/curl blocks are copied verbatim from `packaging/apt-repo.md` so they can't
  drift. It also recommends **against** pursuing Proxmox VE Helper-Scripts inclusion: that project
  installs apps into a fresh LXC, and vnprox has to run as a privileged daemon on the host, which is
  an architectural mismatch rather than a priority call.

  **What this does not change**: this repository is currently private (an anonymous request
  returns a 404, not a "you need permission" error), so there is no public issue tracker and no
  public clone URL — on top of the already-stated absence of a hosted apt repository, a hosted
  registry, and a hosted demo. GitHub Pages is not enabled, so there is no live docs site URL yet,
  and there is still no security-disclosure contact, flagged in the docs as a real gap rather than
  routed around. A forum announcement is written but marked **DRAFT — NOT YET POSTED**.

- **An installable mobile app, with push notifications for critical findings and changesets
  awaiting confirm.** T-2005 adds a PWA manifest, service worker, and an offline shell that fails
  honestly: cached views are labelled with the age of the data they're showing rather than
  presenting stale topology as current. Web-push subscriptions ride the existing event stream, with
  per-category opt-in (critical findings, awaiting-confirm changesets, drift), are listable and
  revocable per device, and are sealed at rest and tied to the session that created them
  (`ON DELETE CASCADE`), so a subscription dies with its session rather than through a cleanup step
  that can be forgotten. The encryption (RFC 8291) and signing (RFC 8292/VAPID) are implemented
  against the Go standard library with no new dependency, and proven byte-for-byte against RFC
  8291's own worked example rather than only against vnprox's own encryptor.

  **A push notification cannot leak the network's shape to whoever is holding the phone.** A
  critical-finding notification's title and body are fixed literals with no finding-specific detail
  and no node or guest name; the deep link opens a filtered view (`/tools?pushCategory=critical`),
  never a specific finding. Leakage is prevented by the notification-building function having
  nothing to leak, not by a redaction step someone has to remember. **Confirming a changeset from a
  notification still requires a real authenticated session with the capability** — the notification
  is a deep link, never an action token, so a lock-screen tap can open the review screen but cannot
  itself confirm anything. **Not verified**: delivery to a real device through FCM, APNs, or Mozilla
  autopush, and installability on real iOS/Android hardware — everything here has been tested
  against synthetic subscriptions and an in-process test server only.

### Changed

- **All three GitHub Actions workflows are disabled; the dev host is the gate.** `CI`, `Packaging
  matrix`, and `Release` are each `disabled_manually`. GitHub Actions billing has been exhausted
  since 2026-08-11, and every trigger after that failed with a payment error rather than a test
  result — 37 of the last 50 runs went red on commits that were green locally. A red X that means
  "unfunded" rather than "broken" trains everyone to ignore the one place a real failure would
  show, which is worse than no signal at all. `scripts/ci-local.sh` reproduces every job in
  `ci.yml` and `packaging-matrix.yml` and is the actual gate. Re-enable with
  `gh workflow enable "<name>"`.

  Two consequences worth stating plainly, because they change what a tag *does*: **a `v*` tag now
  publishes nothing** — no GitHub release, no `.deb`, no `openapi.json` or automation-contract
  asset — so a release has to be built by hand from a clean worktree at the tag; and any workflow
  file referenced elsewhere in this changelog describes configuration that is present but dormant.

### Fixed

- **The topology map's guest network interior panel could get stuck showing a permanent error
  after enabling the toggle, on a raced fetch.** A prior diagnosis of this symptom (`T-2505-followup-02`)
  concluded the interior query was never invalidated after the toggle mutation; that invalidation
  was already present and always had been. The real cause: the interior query treated the expected
  `interior_not_enabled` 404 as "nothing to show yet" and resolved to `undefined`, which TanStack
  Query v5 treats as a bug and forces into a genuine error state — one the later invalidation could
  never clear, because it wasn't a stale success, it was a synthetic error the query was parked in.
  The sentinel is now `null` rather than `undefined`, so the query stays in a real success state and
  the always-present invalidation can do its job once the toggle actually turns on. Reproducible only
  under CPU restriction, which is why it had read as a flake rather than a real race.

- **Contrast failures on the switch faceplate view, and a measurement bug that was hiding more of
  them than it found.** Two real WCAG AA failures in the light theme — the access-port VLAN marker
  (4.4:1) and the VNet tag suffix (2.32:1), plus a five-site copy-paste (`text-slate-400
  dark:text-slate-400`) that had only ever been checked against a dark surface and measured
  2.4–2.63:1 against this app's actual light-mode surfaces — are fixed to 5.14–9.82:1. Chasing the
  rest of the previously-reported violations down turned up why they'd looked so inconsistent: axe
  was catching a drift-badged switch's `animate-pulse` ancestor mid-pulse, at whatever opacity the
  scan happened to land on, so two runs of identical code reported different ratios. The
  accessibility test harness now emulates `prefers-reduced-motion` (driving the app's own
  `useReducedMotion()` hook, not a workaround), which removes the pulse and makes the true residual
  cost of the existing "dim what doesn't match the VLAN filter" suppression visible — and much
  smaller than it looked. That suppression is narrowed to touch only the elements it's actually
  fading rather than every element under a node section, and a new test engages the VLAN filter and
  asserts something is really dimmed before measuring, closing the gap that let this go unexercised.

- **The in-app help now covers the installable app, the offline shell, and push notifications.**
  T-2005's PWA shipped with no online help at all — the only "notification" text in the help was
  about webhooks and Proxmox's own notification system, a different and older feature, so a reader
  looking for push found something that wasn't it. Two topics now cover installing (which is the
  browser's own affordance, not an in-app button), what the offline shell shows and the fact that
  it never caches an API response — so there is no offline editing mode to reason about — and the
  push categories, their per-device scope, and why a critical-finding push deliberately carries no
  detail about the finding.

  Worth recording how this was missed, because the gate involved is a good one: `coverage.test.ts`
  derives its screen inventory from the router and the nav rail rather than a hand-maintained
  checklist, which is what makes "every screen has help" checkable at all. But it can only see
  *routes*, and push settings is a panel inside the Settings route — that route already resolved to
  a topic, so the gate stayed green over an undocumented feature. Extending it to panel-level
  features is a real follow-up; it is deliberately not done here, and nothing in that test was
  weakened to accommodate this change.

### Security

- Upgraded the Go toolchain to 1.26.6, which carries fixes for five stdlib advisories reachable
  from vnprox code (`net/url`, `crypto/tls`, `net/http` ×2, `encoding/asn1`).

## [3.5.0] - 2026-08-13

**Arc 5 — "adoptable, not just proven."** Twenty-five cards across phases 25–28
(`docs/roadmap-adopted.md`). The arc before this one asked whether vnprox was *true*; this one
asks whether anyone else can run it, and whether the answer to the first question can be produced
by a machine rather than by a person with a clipboard. Most of what follows wires together
subsystems that already shipped, or turns an existing claim into something a command can check.

**On the version number.** The phase-to-version map in `docs/roadmap-proven.md` reserves `v3.3`
for phase 20 and `v4.0` for phase 21, and neither phase is complete — phase 20 has three cards
outstanding and phase 21 four. This release takes `v3.5.0` so that Arc 5 ships under a number of
its own without spending either of those before their content exists.

**Two things this release does not have**, stated here because both are easy to assume from the
entries below: there is no `get.vnprox.io` apt repository yet (the installer's own comments say
so), and there is no hosted public demo instance — the demo *mode* ships and runs locally, but
nobody is hosting one. Several features are also API- and CLI-only with no web UI; each says so
where it appears.

### Added

- **You can now help build the compatibility matrix, if you want to — and see exactly what that
  would send.** One cluster validated by us is an anecdote; a hundred clusters reporting which
  `vnproxctl verify` checks pass on which PVE version, kernel and NIC is a matrix. T-2503 adds
  `vnproxctl telemetry`, and every part of it is built around the fact that nobody should have to
  take our word for what leaves their cluster:

  - **It is off, and it has no endpoint.** `[telemetry] enabled` defaults to false and the shipped
    `vnprox.toml` has the section commented out entirely. vnprox also ships no default collector
    URL — there is no vnprox telemetry service — so opting in means naming an `https://` endpoint
    yourself. `enabled = true` with no endpoint is a startup error rather than a quiet no-op.
  - **`vnproxctl telemetry preview` prints the exact bytes.** Not an equivalent rendering: the
    payload is encoded once and both the preview and the send read that one buffer, which a test
    proves by capturing both and comparing them.
  - **What is collected is a fixed, documented list**: check ids and verdicts, the vnprox/PVE/kernel
    versions, the NICs' PCI vendor:device ids, and a node *count*. Never a hostname, address, MAC,
    guest name or cluster name — `docs/security.md` lists every field, a build fails if that list
    and the code disagree, and a payload containing any of those things is refused before it is
    sent, naming the rule and the value.
  - **The only correlator is a random local ULID**, thrown away by `vnproxctl telemetry reset-id`.
  - **A failed or hanging collector cannot affect a `verify` run**, and a run against a mock PVE
    endpoint is never sent at all.

- **The map can now carry what you know, not just what is true.** "This uplink is temporary until
  the switch swap." "Vendor-managed, do not touch." That knowledge lived in a wiki that was wrong
  within a month, because it was not next to the thing it described. T-2806 puts it on the map:
  free-text notes pinned to an entity, and labelled regions drawn on the canvas. Both carry an
  author and a timestamp, both are shared with the whole team, and both appear in the config-doc
  export — which is where a note is most useful to a reader who cannot see the map at all.

  Three things about it are deliberate:

  - **A note can announce its own staleness.** Give a note an expiry and it stops being displayed
    when that moment passes. Expiry is computed on every read, not by a background job, so a daemon
    that was switched off for the week the expiry fell in cannot come back and show you a stale
    note. An expired note is *hidden*, never deleted — you can still list, read and unpin it.

  - **Deleting an entity does not delete what you wrote about it.** The note on a bridge you removed
    is kept and shown as orphaned, because it is frequently the only surviving record of *why* the
    bridge was removed. Nothing sweeps it away: not retention, not the expiry path, not collection.

  - **Regions survive your colleagues.** They live in their own shared table rather than inside the
    per-user layout blob that gets rewritten every time anyone drags a node, so a region persists
    across layout changes and view switches instead of being overwritten by whoever moved something
    last.

  None of this is a copy of Proxmox's config: a note's entity reference names a PVE object, but the
  row carries only the sentence a human typed, and a region corresponds to no PVE object at all.

- **The Hub has a registry to talk to — and it is a signed file, not a service.** vnprox shipped a
  registry *client* with nothing on the other end. T-2803 supplies the other end: the index format,
  the tooling that produces it (`vnproxctl hub publish|index|revoke|verify|keygen`), and the
  verification that makes it binding. The registry is a static directory — `index.json` next to an
  artifact tree — on object storage or GitHub Pages, exactly the posture the apt repository already
  uses. There is nothing new to run, and no new port.

  What the signature over the catalog buys you, given artifacts were already signed:

  - **A corrupted catalog gives you nothing, not part of something.** An index that is unsigned,
    truncated, edited, or signed by a key you did not pin fails as a whole; the Hub reports the
    registry as unavailable rather than showing you a catalog someone else edited. Set the
    registry's fingerprint in `[hub] index_signers` to turn this on.
  - **Revocation works when the network does not.** A withdrawn blueprint or plugin — one version,
    every version, or everything a compromised publisher key ever signed — is refused both in the
    catalog and at download, decided from the index you already fetched. No live revocation call
    exists to be blocked, spoofed, or simply unreachable at the moment it matters.
  - **It cannot make anything trusted.** The index signature is authority over the *catalog* only.
    An artifact from a signer you have not trusted still stops at the same explicit trust step it
    always did, and the capability scope a plugin declares is still shown to you before you confirm
    and still enforced after.

  Publishing is deliberately two-handed: a publisher signs their own artifact and opens a pull
  request; a reviewer — who does not hold the publisher's key — indexes it. Publishing the same
  artifact twice yields one entry and does not even rewrite the published file; publishing
  *different* bytes under a version that already exists is refused rather than swapped. The review
  checklist, the key handling, and the index-key rotation procedure are written down in
  `docs/hub-registry.md` before the first submission rather than after.

- **Try vnprox without a Proxmox cluster.** `vnproxd --demo` (T-2801) runs the whole product against
  a synthetic three-node cluster embedded in the binary — SDN zones, guests, findings, drift, flows
  — with no PVE endpoint and no outbound network at all. Every screen renders from it, and every
  mutating API answers with what it *would* have done rather than touching anything: a store
  checksum before and after a full staged-and-applied changeset in demo mode is unchanged. A
  persistent banner and a distinct accent colour make it unmistakable, and the two modes cannot be
  mixed — demo mode refuses any config naming a real `[pve]` endpoint, and a real endpoint refuses
  to start in demo mode.

  T-2801 also gives the install script real signature verification with no way to turn it off — no
  `--insecure`, no `--no-verify`, no environment variable, checked by a test that greps the script
  for their absence — plus a binary-tarball fallback for non-Debian hosts and idempotence on a
  second run. **One caveat worth being direct about: there is no `get.vnprox.io` yet.** The signing
  and verification machinery is real and tested against a local repository and an ephemeral key; the
  public apt repository and tarball host the script points at by default don't exist as a live
  service today, so the one-command install isn't something you can run against the internet yet.

- **The infrastructure for a public, click-through demo exists; the public demo itself does not.**
  T-2802 builds `vnproxd --demo --public-demo`: an edge in front of the whole daemon that refuses
  anything that isn't `GET`/`HEAD`/`OPTIONS` with a 403, mints each visitor their own session with
  no password and no login screen, keeps per-visitor UI state (layout, tour progress) in memory so
  one visitor's changes are invisible to another, and caps resources per visitor so one hostile
  session can't degrade it for anyone else. A six-step guided tour walks the surfaces
  `docs/datasheet.md` leads with, using T-2801's demo dataset, resumable and skippable.

  **There is no hosted instance to visit.** This repository has no domain, no object storage, and no
  deploy target to publish one from — `docs/features/demo-mode.md` states that gap explicitly,
  alongside the others (the path simulator, the diagnosis ladder, and MCP don't work against a
  public demo, since they need a write-shaped request the edge refuses). What ships is everything
  such an instance would need in order to be safe, tested end to end against a real daemon — not the
  instance.

- **Point vnprox at a git repository, and it will tell you when the cluster stops matching it.**
  `internal/spec` could already export and import a declarative network document for your cluster;
  T-2701 gives it somewhere to live. Configure `[gitsync]` — a remote, a ref, a path — and on every
  poll vnprox fetches that one file, plans it against live inventory, and, only if the plan is
  non-empty, opens a single draft changeset and stops. **It never applies anything.** A second
  divergence updates that same draft rather than opening another one, and `vnproxctl gitsync
  status` — there is deliberately no `gitsync sync` or `gitsync apply` — shows the last commit
  fetched, the last plan, and why the current draft exists.

  Off by default: no `[gitsync]` section means nothing is fetched and no credential file is read.
  The fetch itself is a plain HTTPS read of one file, not a `git` clone — no `git` binary and no git
  library, deliberately, since a subprocess given an operator-supplied remote URL would be a new
  injection surface, and a full git implementation is a lot of dependency for reading one file. The
  trade-off that follows, stated in `docs/security.md` rather than left for you to discover: with no
  object graph to walk, `require_signed_commits` verifies a commit's own SSH-format signature
  against your allowed-signers file rather than verifying history. An unreachable remote degrades to
  a finding and a retry, never a blocked startup.

- **A change you made in the GUI can now be proposed to the repository it belongs in.** If your
  intent lives in git (`[gitsync]`), a change staged in vnprox was, until now, a change made outside
  your system of record: the cluster moved, the repository did not, and the next sync reported your
  own edit back to you as divergence. T-2702's `POST /changesets/{id}/propose` closes the loop — it
  renders the changeset as a spec delta, commits it on a branch, pushes, and opens a pull request,
  on GitHub or GitLab. The changeset records the URL.

  What makes it trustworthy is what it refuses to do:

  - **vnprox opens the pull request and stops.** It never merges, gates, approves, or polls one —
    not as a route and not as a method anything here could call. Whatever happens to the request
    comes back through the ordinary sync, which opens a draft changeset you apply yourself.
  - **The branch means what the changeset meant, and that is checked before anything is written.**
    The proposed document must re-import to exactly your changeset's ops; one that would not is
    refused with the difference named. A changeset the spec cannot express — every delete, and
    every firewall/IPAM/QoS/WireGuard/raw-file op — is refused rather than approximated.
  - **A failed proposal leaves no orphan branch.** Either the branch and the request both exist, or
    neither does; a branch someone is already reviewing is never removed.
  - **Pushing needs its own credential.** `[gitsync] push_token_file` is separate from the
    read-only sync token, so syncing still never pushes, and a deployment that has not opted in
    never reads a write credential off disk. It appears in none of what gets written: not the
    branch name, the commit message, the pull-request body, the audit trail, or any error.

  The pull-request body carries the review context with the review: the spec diff, the per-op
  summary, and the blast radius — which nodes, carriers and guests the change would disturb, and
  whether it touches a management path.

- **See exactly what changed on the cluster since any point in time — including changes vnprox
  didn't make.** T-2704's `GET /topology/diff?from=<ts|snapshotId>&to=<ts|snapshotId|now>` compares
  two points in the snapshot series vnprox already records on a schedule, and reports added,
  removed and modified entities with per-field before/after. The History page has a new
  entity-level diff view (with a *Show on map* link), and the map itself can render a diff overlay
  for a selected range.

  The central claim is honesty about attribution, not completeness: a difference explained by a
  changeset names it, and one that isn't is marked `attributed: false` rather than folded in
  silently — an unattributed change is exactly the out-of-band edit the drift checker exists to
  catch. The comparison currently covers `/etc/network/interfaces`, the one file every snapshot
  captures and the only one `to=now` can read live; SDN config is listed as not compared rather
  than reported as newly added on every diff. A range with no snapshot at either end is refused,
  naming the nearest snapshots that do exist, rather than returned as an empty diff that reads as
  "nothing changed".

- **A drift finding can now name all three positions — spec, config, and live — and offer two
  explicit fixes.** With a spec in git (above), there's a third position beyond "declared" and
  "running". T-2703 adds `POST /drift/{id}/restore-intent` (stage a changeset bringing the cluster
  back to the spec) and `POST /drift/{id}/adopt-reality` (propose a spec commit, via the pull
  request path above, describing the cluster as it actually is). Neither fires automatically, at
  any severity — both require an explicit call naming only the finding's id, with the actual change
  computed server-side rather than accepted from the request, the same pattern the existing
  one-click drift fix uses. **This is API-only for now**: the drift findings panel doesn't yet have
  buttons for either action, so reaching them today means calling the route directly.

- **Map your findings and policies onto compliance controls, with evidence attached.** T-2706 adds
  a declarative profile format — control IDs mapped to the checks, policy rules, and posture
  factors that evidence them — and ships one general-purpose profile as a worked example rather
  than a certification claim (every rendered report says so in its own banner: "This is not a
  certification."). `GET /compliance/{profile}` returns per-control status with the checks that
  back it named as evidence, and `GET /export/compliance/{profile}` renders it as a timestamped
  Markdown, HTML, or JSON report. **A control with no mapped evidence reports `unmapped`, never
  `pass`** — enforced by the one predicate every output format goes through, so a control nobody
  wired up cannot silently read as compliant. A report requested for a date outside the retention
  window is refused with the earliest date that is available, not padded out as a partial one.
  **This is API-only**: there's no `vnproxctl` command and no dedicated screen yet, only the two
  routes and the exported document.

- **An AI operator can now stage a change it cannot apply.** The MCP surface could already diagnose
  a problem in full — and then had to hand you a paragraph of instructions to type. T-2705 adds four
  new tools (`changesets.stage.bridge`, `changesets.stage.iface`, `changesets.stage.fwrule`,
  `changesets.stage.ipam`) that close that gap from the safe side: each turns a request into exactly
  one op in a **draft** changeset and returns its id. You still review it and you still apply it.

  The boundary that makes this safe is not a rule anyone has to remember:

  - **No tool applies, confirms, approves, or deletes** — and one cannot be written. The only
    change-engine capability the MCP server holds is an interface with no such method, and a
    compile-time assertion fails the *build*, naming the offending method, if anyone widens it.
  - **Every op is checked against your policy rules before a draft exists.** A denied op is refused
    with the rule's id and description — feedback a model can act on — and leaves nothing behind. A
    policy that cannot be evaluated refuses the stage rather than skipping the check.
  - **Every staged changeset says who and what made it**: `origin: "mcp"`, the automation token, and
    now the tool's own name (`originTool`), all set once at creation and visible on every changeset
    response, so a reviewer sees which AI action produced the draft rather than only that one did.
  - **Budgeted**: staging is rate-limited per session and the number of open MCP drafts is capped;
    exceeding either is refused with a message naming the limit.

- **An in-app panel that runs the same MCP read tools against your own daemon — no external MCP
  client required.** T-2808 adds a right-hand drawer that calls `topology.get`, `findings.list`,
  `flows.query`, `ipam.subnets.list`, `simulate.path` and `diagnose.run` over your own session and
  your own `/api/v1` routes: no new backend capability, no new route, and no second authorization
  model — a capability you lack is a capability the panel can't reach either, one assertion per
  restricted surface. An answer is rendered only if it cites a tool result from that turn; a claim
  with nothing behind it is not shown, by construction rather than by a renderer remembering to
  check.

  With the MCP staging tools above installed, the panel can stage a changeset from a closed set of
  typed proposals — tagged `[assistant]` in its title — and hands off to the ordinary review screen.
  The same compile-time guarantee that stops any MCP tool from applying is what makes "it never
  applies" true here too, not a separate promise re-implemented for the UI. **No model backend ships
  with vnprox and none is configured by default**; until you configure one, the panel says so
  plainly and nothing leaves your browser. Prompts and answers are excluded from logs and support
  bundles by default.

- **`vnproxctl verify` — the hardware-validation checklist, executed.** vnprox has always had a
  gap it stated openly: almost every behaviour it claims has only ever been tested against a mock
  Proxmox, because validating one on real hardware meant a person reading a checklist line, doing
  the thing, and writing down what happened. That does not scale, does not repeat, and — the part
  that actually mattered — could not be handed to a user who has a cluster and would like to help.

  T-2501's `vnproxctl verify --suite=hardware` is that checklist as a command. Twenty-six checks, one per
  claim, each deciding its own verdict against your cluster and carrying the API response, command
  output or file contents that verdict rests on. It is read-only and safe on a production cluster.
  **If you have a Proxmox cluster, this is the single most useful thing you can send us.**

  Three properties keep the result honest, and each is enforced structurally rather than by
  convention:

  - **A skipped check is never a passing one.** A check that cannot run here says *why*, and names
    the hardware it would take. A run in which everything skipped exits non-zero and reports
    `0 passed` — because "we did not look" read as "we looked and it was fine" is how a validation
    figure becomes fiction.
  - **A verdict with no evidence is a malformed report**, which the command refuses to print. You
    can read the working and disagree with the verdict; that is the whole difference between this
    and a ticked box.
  - **It refuses to run against a mock Proxmox** unless `--allow-mock` is passed, naming the flag
    and saying what it costs. A green run against a mock is indistinguishable from a green run
    against real hardware, so the refusal is the default. A run made with the flag is stamped as
    such *in the report*, so the caveat travels with the document.

  Three suites: `hardware` (one real node, changes nothing), `multinode` (two or more nodes, or two
  clusters for federation; skips loudly with the count it saw otherwise), and `destructive`
  (`--i-understand` required — it interrupts applies, lets commit-confirm windows expire, and stops
  the active daemon). The destructive interlock is wiring, not policy: without the flag no write
  client is constructed at all.

  `--out` writes a signed, timestamped JSON artifact naming the vnprox version, PVE version, kernel
  and NIC models the run observed. Editing it — including merely re-indenting it — invalidates the
  signature. `vnproxctl verify --list` prints every check with the hardware it needs, so you can see
  what the suite will ask of your cluster before running any of it.

- **The confidence behind everything above is now harder to fake.** Four cards hardened the
  machinery that verifies vnprox rather than adding a feature you'll click on — none of them change
  what you can do, only how much you can trust the rest of this file:

  - **T-2502** teaches the Proxmox test client to record real PVE traffic into replayable fixtures
    (`make record`), because every fixture before this was hand-written — a guess at what PVE
    returns, not an observation of it. A cassette containing a ticket, password, or private key
    fails the write rather than being saved, and a replay request that doesn't match a recorded one
    fails loudly instead of falling through to an invented default.
  - **T-2504** adds `make soak`, which runs the real daemon against synthetic churn for a
    configurable duration (30 minutes locally, 8 hours nightly) and fails on a *trend* in
    goroutines, heap, RSS, file descriptors, and table row counts — never a threshold. A leak slow
    enough to look flat still passes; one with a positive slope over the second half of the run
    fails, naming the metric.
  - **T-2505** shards the end-to-end suite across four workers and adds a flake quarantine with an
    expiry — an expired quarantine fails the build even if the test it covers is still red — and,
    along the way, found and fixed several real defects the old serial suite's timing had been
    hiding, including that this suite's own test deadlines need to scale with how many CPU cores
    are actually available.
  - **T-2506** turns `docs/performance.md`'s stated render and collection budgets into an enforced
    gate, compared against a fixed, host-normalised budget rather than the previous run, so a slow
    drift that never regresses more than a few percent at a time still eventually fails.

  All four are `make` targets and CI gates a contributor runs; none is reachable from the product,
  and a cluster operator will never see any of them directly.

- **Policy-as-code guardrails: the change engine can now say no.** Until now the engine's
  guarantees were strong and *advisory* — it told you what would happen, but it refused only one
  thing, hard-coded: cutting a node's management path. Every other rule an organisation has ("no
  guest on VLAN 1", "a bridge carrying guests keeps two uplinks", "nobody touches `vmbr9` on the
  storage nodes") lived in a wiki and was enforced by whoever happened to review the change.

  T-2601 lets you install a **declarative policy rule set** on the cluster. A rule is
  `{id, description, severity, match, assert}`, and `severity: deny` blocks the changeset at the
  validate stage — before any diff or plan is computed — with the rule's id and description in the
  error. `severity: warn` annotates the changeset instead and travels with it to the review
  screen without blocking anything.

  Policies are **data, not a scripting language**: `match` and `assert` are lists of
  `{field, op, value}` conditions over the ops and inventory the change engine already works in.
  There is no embedded expression interpreter and no new dependency — a deliberate limit, not an
  omission. Rules can assert over the changeset's *net effect* on the cluster (`target.guestCount`,
  `target.uplinkCount`, `target.vlanAware`), which is what lets "a bridge carrying guests keeps two
  uplinks" fire on a port removal that never mentions a guest.

  A rule that can never match anything is refused at load, not silently ignored, and a rule that
  has matched nothing for two weeks of real changesets is reported as probably-misconfigured. A
  policy file the daemon cannot parse is fatal at startup: vnproxd will not come up quietly
  enforcing a policy it could not read.

  `vnproxctl policy test --policy=f.yaml --changeset=<id>` evaluates a candidate rule set against a
  real changeset without staging anything, so a rule can be developed safely against the change it
  was written for; `vnproxctl policy lint` validates a document with no daemon at all, and
  `vnproxctl policy examples` prints a worked example set to start from. Rule sets are cluster-
  scoped, versioned in the store, and every change is audited (`policy.update`) with the full
  rule-set diff — both sides of every changed rule — so the audit entry alone reconstructs what
  changed. New routes: `GET`/`PUT /policies` and `POST /policies/test`.

  **The default policy set is empty, and an empty set changes nothing.**

- **A multi-node apply can now stop halfway and let you look, and the change engine can now notice
  its own mistake.** Two guardrail cards, built to work together:

  - **T-2602** adds a canary apply: `POST /changesets/{id}/apply` takes an optional
    `applyStrategy: {mode: canary, canaryNodes, holdFor, gate}`. The apply touches only the canary
    nodes, then **pauses in a resumable state** — the changeset is neither applied nor rolled back,
    and the untouched nodes are never contacted. `gate: manual` waits for
    `POST /changesets/{id}/continue`; `gate: auto` proceeds only on a clean health check during the
    hold. Aborting restores exactly the nodes that were touched. A daemon restart mid-hold resumes
    or rolls back from what was persisted rather than leaving a changeset in limbo, and the
    commit-confirm deadline covers the *whole* sequence — a stalled canary cannot hold the cluster
    open past the window. The default (`mode: all`) is byte-for-byte the existing apply.
  - **T-2603** closes the gap commit-confirm never covered: "the change was wrong and the operator
    is still staring at the screen." With `autoRollbackOnError` set — per changeset, or as a
    `[changesets] auto_rollback_on_error` cluster default, both off by default — a **new** `error`
    finding on an entity the changeset actually touched rolls it back immediately, inside the
    confirm window. A finding that predates the apply never triggers, however severe; one outside
    the changeset's blast radius never triggers, however severe; a `warning` never triggers, ever.
    The audit entry and the changeset both name the finding that caused the rollback. During a
    T-2602 canary hold, a trigger aborts the sequence and restores only the stages that ran.

  **Both are API/CLI-only today** — there is no canary-strategy picker or auto-rollback toggle in
  the review screen yet, only the request body and the cluster config.

- **Some changes can now require more than one person to sign off, with a written-reason emergency
  override.** T-2604 lets a deployment declare protected op classes
  (`[[changesets.protected_class]]` — an op-type glob like `fw.*`, the reserved `mgmtPath`, or a tag
  a policy rule above sets) that need N distinct approvers before `apply`, enforced server-side so a
  request that bypasses the UI is refused identically. The review screen shows this directly: Apply
  stays disabled with a message naming the class and exactly how many more approvals are needed.
  Two approvals from the same person through two different tokens still count as one — distinctness
  is a property of the stored sign-off, not of the token. An emergency **break-glass** override
  exists for when nobody else is available (`POST /changesets/{id}/break-glass`): it requires a
  written reason, is audited under its own action (`change.breakglass`), and raises an `error`
  finding that cannot be acknowledged for 24 hours, so the override still gets reviewed by someone
  who wasn't in the room. **It isn't a button on any screen** — reaching it means calling the route
  directly.

- **See what the map will look like before you click apply.** `GET /changesets/{id}/preview`
  (T-2605) projects the changeset's ops onto the live topology in memory — nothing is written, and a
  store checksum before and after is identical — and the map gains a distinct preview mode showing
  added, removed, and modified entities. **It says plainly what it can't show**: an op whose effect
  can't be projected (a raw `/etc/network/interfaces` edit, anything with an out-of-band-dependent
  result) is listed by name as unprojectable rather than silently dropped or guessed at. A
  changeset with blocking validation findings is refused rather than previewed into nonsense — a
  changeset that can't apply has no post-apply map.

- **When something breaks, one timeline instead of five open tabs.** "Start incident" (Incidents in
  the nav) opens a view — not a mode — that assembles, at read time, the findings that appeared and
  cleared, changesets staged or applied, diagnosis-ladder runs, captures, and flows across the
  window, plus your own timestamped annotations sitting alongside them. Opening one doesn't start a
  collector, subscribe to a stream, or copy an event: T-2804 stores only the window and your
  annotations, so an incident opened **retroactively** over a past window shows the same timeline as
  one opened live, and reopening a closed incident shows the same events it had when you closed it —
  nothing is deleted by closing. Closing produces an export — the timeline plus a support bundle —
  through the same secret-redaction path the existing support bundle uses.

- **See who else is looking at a changeset, and get warned before you silently overwrite their
  work.** T-2805 adds advisory locks: staging a draft against an entity another draft already
  touches warns you who holds it and lets you proceed anyway — the override is audited, and a lock
  **never** blocks an apply, even one it's still holding. Presence — who else is viewing this
  changeset or entity — rides the existing WebSocket event stream and updates live in the changeset
  drawer. A lock releases when its session disconnects (a closed laptop, not a timeout you have to
  wait out) and also on an explicit timeout, so nothing outlives the person who staged it. This is
  deliberately not a mandatory lock: **it prevents an accidental collision, never an emergency
  change.**

- **A scheduled push instead of three dashboards you have to remember to check.** T-2807 assembles
  the posture score with its named factors and the change since the last digest, capacity
  projections crossing their horizon, unresolved drift, and findings opened/closed in the period —
  delivered through your existing alert targets, reusing the same scheduling, quiet-hours, and
  retry machinery as the rest of vnprox's notifications. **A quiet period gets a one-line digest,
  not a padded one** — under a stated 200-byte bound, with no manufactured "nothing to report, but
  here's a chart anyway" filler, because a digest that arrives full every week regardless is the
  fastest way to make people stop opening it. Deltas are always against the *previous* digest, not
  an arbitrary window, and a first-ever digest says plainly it has no baseline rather than showing a
  spurious jump from zero. The schedule is stored per-cluster and re-read on every tick, so changing
  the cadence takes effect without restarting the daemon. **No dedicated screen yet** — the schedule
  is set through the API.

- **Certificate management for the cluster.** Every cross-node thing vnprox does — applying a
  changeset, arming a distributed rollback timer, reading a peer's state — rides peer-API TLS,
  which is pinned to your cluster's own CA and fails closed. Until now there was no way to see
  those certificates, no warning before one expired, and one confirmed latent failure. There is
  now a **Settings → Certificates** screen, a `GET /certs` API, a `vnproxctl certs` command, and a
  `cert` family of findings covering expiry, name coverage, chain to the cluster CA, key strength,
  and missing or unreadable certificates. Each finding names the exact PVE command that fixes it.

  The inventory is cluster-wide from a **local** read: `/etc/pve` is pmxcfs, so every node's
  certificate is already on every node's disk. That is deliberate rather than incidental — a
  certificate problem is precisely what makes peers unreachable, so an inventory that needed the
  peer API to diagnose a peer-API failure would be useless at the only moment it mattered.
  `vnproxctl certs` reads the same data with the daemon down.

  Private keys live in the same pmxcfs directory as the certificates. The scanner only ever
  constructs paths from a fixed filename allowlist rather than listing a directory and filtering,
  and the certificate type carries no raw-bytes field at all — so "this cannot leak key material"
  is a property of the types, not of care taken at each call site.

- **Online help, on every screen.** vnprox previously had one in-app help surface: a dialog
  listing keyboard shortcuts. Everything else a user might need to know lived in `docs/` — on
  disk, in a git repo, on a machine they are probably not looking at while a commit-confirm
  countdown is running. There is now a help panel that answers "what is this, and what do I do
  here?" from wherever you are. Press **F1** or the **Help** button in the top bar for the screen
  you're on; click the **?** next to a panel heading for that specific surface. The panel carries
  full-text search across every topic, **See also** links between related ones, and Back to
  retrace. `?` still opens the keyboard-shortcut list, unchanged — two different questions, two
  different affordances.

  Coverage spans the concepts the UI assumes you hold (the change engine, commit-confirm,
  protected interfaces, drift, findings, permissions, read-only mode, cluster awareness), every
  routed screen, the panels and wizards inside them, and the v2.0/v3.0 opt-ins — federation, AI
  operators, plugins, tenants, HA, switch push, embeds, OIDC, PBS. Topics state the safety
  boundaries where they apply: that an AI operator can draft but never apply, that a switch push
  cannot be rolled back remotely, that unattended rollback covers interface changes always and
  firewall/SDN changes only for as long as your sealed PVE ticket lasts.

  Help content is bundled into the SPA rather than fetched, so it adds no API surface. Every topic
  cites the repo doc it was written from.

- **The coverage claim is enforced, not asserted.** `web/src/help/coverage.test.ts` derives the
  screen inventory by parsing `App.tsx` and `NavRail.tsx` for the routes they actually declare,
  then requires each to resolve to a substantial topic. It also rejects stale mappings for deleted
  routes, `<HelpAnchor>` values that resolve to nothing, unresolvable `seeAlso` ids, orphaned
  topics unreachable from any screen, `docRef`s naming a file that doesn't exist, and content below
  a quality floor. Adding a route without help is a failing test naming the path. Each source parse
  asserts a floor on what it found and that a known sentinel is among the results, so a regex that
  stops matching after a refactor fails loudly instead of certifying full coverage of an empty set.

### Fixed

- **Pinned peer TLS no longer fails against a certificate that doesn't list the node's IP**
  (`T-1906-bug-01`, found on real hardware). Peers are dialled by IP, and Proxmox does not
  necessarily regenerate a node's `pve-ssl.pem` when its management address changes — the node
  this was found on carried a **stale** IP SAN (`192.168.100.99`) for a machine now at
  `192.168.1.9`. Pinned verification against the dial address therefore failed closed on a
  correctly configured cluster: correct components, no operator error, **every peer down at once
  on upgrade**.

  vnprox now verifies a peer against its PVE node name where the certificate covers it, while
  still dialling the IP and still pinning the cluster CA — what `curl --resolve` does. No network
  round trip is needed, because pmxcfs already holds every node's certificate locally. Three
  properties are held by adversarial tests: the CA pin is untouched (a certificate from another CA
  is still rejected), candidate names come from PVE's authoritative node name rather than from the
  presented certificate, and an FQDN candidate must be rooted at the node name — so node A's
  certificate cannot authenticate node B even though the same CA issued both. Where nothing is
  covered, the handshake still fails closed, but `cert_san_mismatch` is now raised at **startup**,
  before the first peer call, instead of surfacing as an opaque handshake error later.

- **Closing a dialog no longer drops keyboard focus to nowhere.** Radix restores focus to a
  `DialogTrigger`; the help panel is opened programmatically and has none, so closing it would have
  left focus on `<body>`. The panel now returns focus to whatever opened it — the top-bar button,
  or the specific `?` anchor you clicked.

## [3.0.4] - 2026-07-30

Closes T-1407's two deferred follow-ups (see `planning/reports/T-1407-followups.md`).
No schema change (stays 32).

### Added

- **Name the cluster on the other side of a tunnel.** The "connect two clusters" wizard's
  *Other side* step gains an optional **Federated cluster** field. Picking one of your
  attached clusters tags the tunnel's peer as that cluster, inside the very same changeset
  that creates the tunnel — no extra step, and nothing is linked unless you actually apply
  the changeset. That tag is what makes the cluster count as reachable over the tunnel, so
  a tunnel outage shows up as one finding naming the cluster instead of every cross-cluster
  view reporting it separately as unreachable. The field is disabled with an explanation
  when no clusters are attached.

### Changed

- **A cluster's tunnel linkage now has one answer, not two.** The peer-level tag above and
  the cluster-level `wgTunnelId` recorded the same fact from opposite ends and could
  disagree. Every read now resolves a single effective linkage: an explicitly-set
  `wgTunnelId` always wins, otherwise it is derived from a tagged peer. `GET`/`PUT
  /federation/clusters` report the new read-only `wgTunnelSource` (`"explicit"` or
  `"peer"`) saying which applied. One behaviour change worth noting: clearing `wgTunnelId`
  no longer necessarily unlinks a cluster — if a tagged peer still exists the cluster stays
  linked, now sourced `"peer"`. Removing a peer-derived link means retiring or retagging
  that peer through an ordinary WireGuard changeset.

### Fixed

- `docs/roadmap-universal.md` still described T-1407 as unimplemented and open, three
  releases after it shipped.

## [3.0.3] - 2026-07-22

Completes phase 14 (`docs/roadmap-universal.md`): T-1407, the one card from that phase
that shipped no code in v2.0.0/v3.0.0/v3.0.1/v3.0.2 despite its six siblings landing
around it (found by a docs/plans-vs-implementation audit; see
`planning/reports/T-1407.md`). Schema 32 (forward-only migration from 31; a v2.0.0+
install upgrades in place).

### Added

- **Tunnel-aware federation transport.** An attached federation cluster can now declare
  itself reachable only via a specific WireGuard tunnel this daemon manages
  (`PUT /federation/clusters/{id}`'s new optional `wgTunnelId`). When that tunnel's live
  handshake goes stale, the cluster is excluded from the global topology, audit, and
  cross-cluster IPAM-conflict reads — but, unlike an ordinary unreachable cluster, it no
  longer raises three redundant per-surface `partial`/`failedClusters` flags for the same
  root cause. In their place, one named finding — `tunnel_down_peer_unreachable`
  (`source: "federation"`) — points at the down tunnel. No auto-remediation: the only
  action is a link to the tunnel's own changeset editor; a human fixes it through an
  ordinary `wg.*` change.

## [3.0.2] - 2026-07-22

Packaging patch — no code or schema change (schema stays 31; a v3.0.0/v3.0.1
install upgrades in place). Completes the `ProtectSystem=strict` fix begun in
v3.0.1, which covered only the first-boot key writes.

### Fixed

- **WireGuard apply fails `read-only file system` on a hardened host.**
  Applying a WireGuard changeset op writes each tunnel's wg-quick config under
  `/etc/wireguard` (`cmd/vnproxd/wireguard.go`'s `hostWGGateway` — `MkdirAll`
  `0700` + `WriteFile` `0600`), but the hardened unit's `ProtectSystem=strict`
  left `/etc/wireguard` read-only, so every WireGuard apply on a hardened node
  failed — the same crash class as the v3.0.1 keys bug, missed because that
  fix only added `/etc/vnprox/keys`. `ReadWritePaths` now also includes
  `/etc/wireguard`, and `postinst` creates that directory (`0700 root:root`,
  wireguard-tools' own convention) so the sandbox bind target always exists
  even on nodes without `wireguard-tools` installed. The rest of `/etc` stays
  read-only. The cluster secret's fallback generate-if-absent write to
  `/etc/pve/priv/vnprox/` is deliberately not added: `/etc/pve` is a pmxcfs
  FUSE mount that `vnprox-setup` pre-seeds, and whether `ProtectSystem=strict`
  even makes it read-only is unconfirmed — tracked in
  `planning/reports/needs-hardware-validation.md`.

## [3.0.1] - 2026-07-21

Packaging patch — no code or schema change (schema stays 31; a v3.0.0
install upgrades in place).

### Fixed

- **Service crash-loop on first upgrade to the v1.4→v3.0 key-generating
  releases.** The hardened systemd unit's `ProtectSystem=strict` made
  `/etc/vnprox` read-only, but the daemon generates first-run secrets under
  `/etc/vnprox/keys/` on startup — the Prometheus metrics scrape token
  (`metrics.key`) and the blueprint signing key (`blueprint-signing.key`) —
  which `postinst` does not pre-seed (only `session.key` is). On a node
  upgrading from a release that predates those keys, the first boot failed
  with `open /etc/vnprox/keys/…: read-only file system` and the service
  restart-looped. `ReadWritePaths` now includes `/etc/vnprox/keys` so the
  daemon can create its own first-run secrets (still `0600 root:root`; the
  rest of `/etc` stays read-only). Found on real hardware upgrading
  1.3.6 → 3.0.0; no sandboxed or fresh-`vnprox-setup` test caught it because
  setup had already created the key files.

## [3.0.0] - 2026-07-21

The platform release — the cut where vnprox stops being only a product and
becomes infrastructure other tools build on. v3.0 caps the v2.0 → v3.0 arc:
deep-sight diagnostics, edge/WireGuard, and Kubernetes/Ceph/QoS visibility
(phases 13–15) shipped earlier, under the `[2.0.0]` cut below — see that
entry's note. This release adds phase 16 (flow baselining, microsegmentation
planning, failure-impact simulation, rogue-service detection, capacity
forecasting, and a posture score) plus phase 17: an AI-operator MCP surface,
a plugin SDK, multi-tenancy, daemon HA, a blueprint/plugin hub, and
embeddable views. Target platforms: Proxmox VE 8.2+ and 9.x; **PVE 10.x and
11.x** are the forward compatibility targets for this arc (see
`docs/deployment.md`, flagged needs-hardware-validation until validated on
real hardware).

**Every new surface stages through the one change engine.** AI-proposed
changesets (MCP), plugin-staged changesets, and tenant request-changesets
are all ordinary changesets — staged, validated, diffed, applied, and
confirmed/rolled-back exactly like every mutation since v0.5. No card in
this arc introduced a second mutation path, and this release **freezes** the
new programmable surfaces as stable, versioned compatibility contracts. DB
migrations remain forward-only; a v2.x install upgrades in place.

### Added

- **Flow baselining, microsegmentation, and failure simulation.** Automatic
  per-guest traffic baselines with anomaly findings when observed flow
  deviates from the learned norm; a microsegmentation planner that proposes
  least-privilege firewall rules from observed flow history with a
  dry-run/review step before any rule is staged; a failure-impact simulator
  that answers "what breaks if this bridge/bond/link goes down" before it
  happens; rogue-service detection (unexpected listeners, unauthorized DHCP/
  DNS servers on the network); capacity forecasting for VLANs, subnets, and
  link utilization trending toward exhaustion; and a cluster-wide posture
  score rolling up firewall coverage, drift, and findings into one number
  with a point-in-time report.
- **MCP server for AI operators (read + stage only).** A first-class Model
  Context Protocol server exposes vnprox's read surfaces (topology, findings,
  flows, IPAM, path simulation, diagnostics) and lets an AI operator *stage*
  a draft changeset — and nothing else. No apply/confirm/rollback verb is
  reachable through MCP by any tool or combination; a human remains the sole
  apply authority. Every AI-originated changeset is unerasably labelled
  `origin: "mcp"` and every tool call is audited with an `mcp:<token-name>`
  actor, so an operator can always tell an AI action from a human one. Off by
  default (`[mcp] enabled`), authenticated with a capability-scoped
  automation token.
- **Plugin SDK.** Stable, versioned extension points third parties can
  implement — switch drivers, flow/telemetry ingestors, finding packs,
  ingress discoverers, and dashboard tiles — with an in-process and a
  supervised out-of-process option, each plugin declaring a capability scope
  that is a server-enforced *ceiling*, never a grant. A plugin can stage a
  changeset for a human to apply but is never itself a mutation path. Install/
  enable/disable/uninstall are all audited with the recorded scope.
- **Multi-tenancy & self-service.** Delegated, server-side-scoped views: a
  tenant sees only its own guests/VLANs/subnets and *requests* changes
  through request-changesets that route to an approver, with scoped
  dashboards and alert routes. Scoping is enforced at the data-access layer
  (an out-of-scope lookup is a `404`, never confirming existence); a member
  can never approve, and an approver can never approve their own request.
- **vnproxd high availability (active/standby).** An optional active/standby
  daemon pair with state replication and VIP-or-DNS failover, so the network
  tool is not itself a single point of failure. Commit-confirm timers and
  scheduled applies survive failover — re-armed to their original absolute
  deadlines — governed by a fenced single-writer lease with explicit
  split-brain handling. Off by default; a single-daemon install is unchanged.
- **Blueprint & plugin hub.** An opt-in client for a public registry of
  signed blueprint bundles and SDK plugins — browse and install with
  Ed25519 signature verification, a per-installation trust decision, and an
  informational "vetted" tier that never substitutes for that decision.
- **Embeddable views & Grafana panels.** Read-only, token-scoped embeds of
  the map, dashboards, and posture report for wikis/NOC screens, plus Grafana
  panels backed by the Prometheus exporter and the event stream. An embed
  token is hard-restricted to read-only scopes at mint and can never exceed
  its minting user.

### Changed

- **Platform API freeze.** The MCP tool surface, the plugin SDK interfaces,
  and the WebSocket `"events"` stream schema are now stable, documented
  compatibility contracts with the same deprecation policy the changeset API
  adopted at v1.7 (additive-only within a version; a breaking change mints a
  new version, keeping the old accepted for ≥1 minor release). See
  `docs/architecture.md` §13.
- Compatibility target advanced to Proxmox VE 10.x and 11.x for this arc
  (8.2+/9.x still supported); real-hardware validation is tracked as a
  needs-hardware-validation item.

### Security

- Every new credential/write-adjacent surface across the v2.0 → v3.0 arc is
  covered in `docs/security.md`'s threat-model summary: packet-capture files
  (Phase 13), WireGuard tunnel keys (Phase 14), the MCP AI-operator
  write-adjacent surface, plugin capability grants and the out-of-process
  plugin boundary, tenant credentials/isolation, and the HA replication
  channel. Every new at-rest credential class is sealed with the single
  AES-256-GCM session key vnprox already uses, never a second cipher or key,
  and each has a targeted encrypted-at-rest test.

## [2.0.0] - 2026-07-20

The multi-cluster release — the cut where a vnprox instance is no longer
1:1 with a Proxmox cluster. v2.0 caps the v1.4 → v2.0 arc (federation,
cross-cluster IPAM, DNS management, guarded switch push, PBS awareness, and
OIDC SSO). Target platforms: Proxmox VE 8.2+ and 9.x; PVE 10.x is the
forward compatibility target (see `docs/deployment.md`, flagged
needs-hardware-validation until validated on real hardware).

**Note:** this tag was applied after phases 13–15 of the *next* arc
(`docs/roadmap-universal.md`) had already merged onto the same branch, so
this release also includes deep-sight diagnostics (Phase 13), edge/WireGuard
networking (Phase 14, excluding T-1407 — see below), and Kubernetes/Ceph/QoS
workload visibility (Phase 15) — listed under "Added" below alongside the
v1.4 → v2.0 federation work they shipped together with. Phase 14's T-1407
("tunnel-aware federation transport") was **not** implemented and is not
included; it remains open, tracked as P1 in `docs/roadmap-universal.md`.

**Federation is additive, not a fork:** a v1.x single-cluster install that
upgrades with zero clusters attached keeps serving its existing
single-cluster experience unchanged — the global cluster view only appears
once a second cluster is attached, and DNS/switch-push/OIDC stay dormant
until explicitly configured. DB migrations remain forward-only.

### Added

- **Multi-cluster federation.** Attach any number of PVE clusters to one
  designated primary and see them all on one screen: a global topology with
  per-cluster capsules and drill-down into each cluster's ordinary view, a
  global search and command palette spanning clusters, per-cluster
  changesets with a merged cluster-wide audit trail, and per-cluster failure
  isolation — an unreachable cluster is greyed out and flagged as a partial
  result, never blanking or erroring the whole view. Config ownership stays
  strictly per-cluster; there is no cross-cluster mutation.
- **Cross-cluster IPAM and external subnets.** The same or an overlapping
  subnet allocated in two attached clusters now surfaces as a conflict
  finding. Non-PVE subnets (office LANs, upstream transit, colo ranges) are
  first-class IPAM records you can add and manage directly. The
  NetBox/phpIPAM bridge is upgraded from read-merge to **bidirectional
  sync**, with a dry-run preview that never writes and an explicit-confirm
  apply step — every sync write is audited with before/after per record.
- **DNS management.** Surface and edit PVE SDN's DNS plugin (PowerDNS):
  zone and record visibility, guest names shown as badges on the map, and
  record edits staged as ordinary changesets through the same SDN
  apply flow as zones/VNets/subnets.
- **Guarded switch config push.** The read-write step beyond LLDP discovery:
  driver-based (OpenConfig/gNMI) pushes scoped strictly to switch ports
  facing your PVE nodes (VLAN membership, port descriptions, LACP), each one
  an ordinary changeset with validate/diff/confirm and the management-path
  interlocks extended onto the uplink port. Per-switch, explicit opt-in;
  ships dark (feature-flagged off) until you enable it for a specific switch,
  with a plainly-stated residual risk that a switch made unreachable by a
  push cannot be remotely reverted.
- **PBS network awareness.** Proxmox Backup Server hosts appear on the map
  with their interfaces, the backup traffic path (node → PBS) is
  highlighted, and the inspector shows datastore-network sizing hints.
  Entirely read-only — no new write actions, no PBS credentials stored.
- **OIDC SSO.** Log in via OpenID Connect (authorization-code + PKCE)
  alongside the existing Proxmox ticket bridge, for federated deployments
  where per-cluster PVE credentials stop scaling, with group→role mapping.
  OIDC authenticates you to vnprox; your Proxmox permissions still gate every
  cluster-scoped action per cluster, and an OIDC role can never grant a
  capability your real PVE ACLs don't already allow.
- **Distributed packet capture, with a permission model and in-browser
  decode.** Capture on any node/interface, build filters with a guided BPF
  builder, decode packets in the browser, and download a `.pcap` — gated by
  a dedicated capability so capture access is granted independently of
  general admin rights.
- **Latency & loss mesh, guest network interior inspector, and conntrack/NAT
  table explorer.** A continuously-probed latency/loss heatmap across the
  cluster; a live view into a guest's own interfaces/routes/DNS via the
  guest agent; and a searchable connection-tracking/NAT table explorer per
  node.
- **Path MTU prober and guided diagnosis flows.** Active end-to-end MTU
  discovery feeding the existing `vxlan_underlay_mtu` finding, and
  guided, honesty-contract-preserving diagnosis flows that walk a user from
  symptom ("can't reach X") through the capture/latency/conntrack/interior
  tools above to a root cause.
- **WireGuard tunnel engine.** First-class WireGuard tunnels — key
  generation and custody, changeset-integrated apply (`wg.*` ops through the
  ordinary stage/validate/diff/apply/confirm flow), map edges for
  tunnel-connected clusters, and a "connect two clusters" wizard.
- **Edge & NAT cockpit, IPv6 enablement, WAN/upstream health, and ingress
  visibility.** A dedicated view for edge routing/NAT configuration; a
  guided IPv6 enablement suite; upstream/WAN link health monitoring; and
  visibility into ingress paths reaching the cluster from outside.
- **Kubernetes overlay mapping (read-only, CNI-aware) and Ceph network
  awareness.** A read-only Kubernetes overlay layer on the topology map with
  service-flow attribution, and Ceph's own network topology (public/cluster
  networks, OSD/mon placement) surfaced without new credentials — both
  follow the "read the owning system's own knowledge, zero new write
  surface" pattern PBS awareness established in this same release.
- **Service-network attribution, QoS & traffic shaping, SR-IOV lifecycle,
  and a migration network planner.** Flow records classified against known
  services (including Ceph/PBS/K8s traffic); `qos.*` changeset ops for
  traffic shaping; real SR-IOV VF lifecycle management; and a planner that
  recommends the least-disruptive network path/timing for a live migration
  using the latency mesh's data.

### Changed

- Compatibility target advanced to Proxmox VE 9.x and 10.x for this arc
  (8.2+ still supported); PVE 10.x validation on real hardware is tracked as
  a needs-hardware-validation item.

### Security

- Every new v2.0 credential class — the per-cluster registry credential
  (`clusters.credential_enc`), the OIDC client secret and mapped PVE
  credentials (`oidc_pve_links.credential_enc`), and switch-driver
  credentials (`switches.credentials_enc`) — is sealed at rest with the same
  single AES-256-GCM session key vnprox already uses for Proxmox tickets, and
  is never returned by any API response, log line, or audit entry. Each has a
  targeted encrypted-at-rest test.
- The threat-model summary gains rows for the arc's new surfaces
  (cluster-registry credential theft, a rogue or compromised attached
  cluster, switch-driver credential theft/errant push, OIDC token forgery)
  with stated mitigations.

## [1.0.0] - 2026-07-12

The 1.0 release: operations, hardening, and release polish on top of
everything below. Target platforms: Proxmox VE 8.2+ and 9.x.

### Added

- Live traffic visualization on the topology map: edge thickness and color
  reflect real-time link utilization, with a dedicated "Traffic" view mode
  and per-bond slave balance shown in the inspector.
- 24-hour traffic history with rate and error charts in the entity
  inspector (sparklines plus a full history chart).
- A unified health and findings stream that brings together drift
  detection, LLDP/switch mismatches, IPAM conflicts, and new health checks
  (interface error/drop rate thresholds, bond slave down, bridge without a
  carrier uplink, MTU mismatches, STP topology instability, stale
  unreloaded network changes, dnsmasq/FRR service health) — each finding
  has a plain-English explanation, affected entities, and, where possible,
  a one-click fixing change.
- Optional notifications (email/webhook, via your existing Proxmox
  notification targets) when a finding's severity crosses a threshold.
- Blueprints: reusable, parameterized network templates you can save,
  import, and export as files. Five ready-made starters are included
  (single-NIC homelab, dual-NIC management+trunk, LACP bond with a storage
  VLAN, VXLAN overlay, and EVPN datacenter). Re-applying a blueprint is
  idempotent — already-matching parts of your network are left alone, only
  the differences are staged. You can also "capture" an existing node's
  configuration as a new blueprint to replicate it elsewhere.
- A guided first-login walkthrough: see a summary of what vnprox found on
  your cluster, confirm (or correct) which interfaces are protected as
  management/cluster links, optionally install LLDP for switch discovery,
  and review your initial health findings — dismissible and resumable, and
  it never blocks normal use.
- Config documentation export: generate a point-in-time Markdown or
  standalone HTML report of your entire network — per-node interface
  tables, VLAN matrix, SDN inventory, firewall summary, LLDP wiring, and an
  embedded topology diagram — for change records or audits.
- Optional, server-enforced read-only mode: when enabled, no write actions
  are possible through vnprox for anyone, regardless of their Proxmox
  privileges.
- Production-grade packaging: a signed apt repository, a one-command
  cluster installer that rolls the package out to every cluster node over
  SSH (or prints manual per-node steps if SSH isn't available), automatic
  provisioning of vnprox's own read-only Proxmox API token and role, and a
  tested upgrade path that preserves your data and config across versions.
  `apt remove` keeps your config and data; `apt purge` removes them
  (prompting before touching cluster-shared state on the last node).
- `vnproxctl status` now reports collector health, Proxmox API
  reachability, and peer reachability/version compatibility all in one
  place, for quick troubleshooting.
- A multi-node change now refuses to start at all if a peer is running an
  incompatible version, rather than risking a partially-applied change.

### Changed

- Tightened the browser Content-Security-Policy to the minimum the app
  actually needs (no inline scripts, no third-party connections, no
  frames/objects/workers beyond what the app uses).
- The daemon's systemd service now runs with additional Linux hardening
  (restricted address families, a syscall allow-list) to reduce its attack
  surface further.
- Session identifiers are now truncated in log output rather than logged
  in full.

### Fixed

- The SQLite database file could be created world-readable under some
  system umask settings; it's now always created (and corrected on
  upgrade) with strict, owner-only permissions.
- A guest-selection bug in the firewall UI that could send the wrong
  reference and fail to load a guest's rules.
- An EVPN zone created via the guided wizard or the plain editor could
  silently omit its BGP controller reference, leaving the zone
  non-functional.
- A cluster-installer parsing bug could mistake the coordinating node's own
  status line for another node's name during multi-node rollout.
- SDN zone/VNet/subnet creation, editing, and deletion (both the guided
  wizards and the plain editors) could be permanently disabled for every
  user, including full-privilege admins, on any real multi-node cluster —
  a capability-resolution bug that made the entire SDN cockpit's write
  path non-functional outside of single-node test setups. Found and fixed
  during v1.0 release verification.
- Several SDN and firewall edit/delete controls (zone/VNet/subnet editors,
  per-rule delete/enable/reorder, shared object deletion) were missing
  read-only-mode capability gating entirely, so a read-only or
  under-privileged session could see them enabled (the underlying write
  would still be rejected server-side — no privilege escalation was
  possible, but the controls should have been disabled with an
  explanatory tooltip).
- `vnproxctl snapshots list/restore` and `rollback-now` — the documented
  "works even when the daemon/UI is unreachable" disaster-recovery path —
  incorrectly required a resolvable Proxmox TLS certificate to run at
  all, which these commands never actually need. Fixed so they work on
  any host, with or without a working Proxmox VE certificate.

### Security

- Completed a full security-hardening review: every claim in the security
  documentation is now backed by an automated test or a documented
  verification procedure.
- Zero known high/critical vulnerabilities in Go and npm dependencies,
  checked continuously in CI.
- Extended fuzz testing to cover every parser that handles data from
  outside vnprox's own control (interface files, LLDP, FRR/BGP output,
  DHCP leases, firewall logs, peer authentication).

## [0.9.0] - 2026

Firewall management and the path simulator.

### Added

- Full visual firewall management across datacenter, node, and guest
  scopes: rule tables, aliases, IP sets, and security groups.
- A "resolved rules" view for any guest showing the exact effective rule
  evaluation order Proxmox's firewall applies, with each rule labeled by
  where it came from (cluster, security group, or the guest itself).
- Clear warning banners whenever a firewall scope is disabled, so a
  disabled datacenter or guest firewall is never a silent surprise.
- Drag-to-reorder rule editing, inline enable/disable, and a rule builder
  with autocomplete for aliases, IP sets, and service macros (with an
  expansion preview showing exactly what a macro matches).
- Safety guard on deleting a shared alias, IP set, or security group: if
  it's still referenced by any rule, the delete is blocked and every
  referencing rule is listed.
- Rule effects preview: for a group-referencing rule, see exactly which
  guests it applies to before you apply it.
- The path simulator: ask "can this VM reach that VM (or IP, or the
  internet) on this port?" and get a real, statically-computed answer —
  allowed, blocked, unreachable, or an honest "couldn't determine" — with
  the full hop-by-hop path, the specific rule that blocked it (one click
  takes you to that rule's editor), and a complete list of caveats. The
  simulator is built to never give a confidently wrong answer.
- Trace-path from the topology map: right-click any guest to trace a path
  to another guest, an IP address, or "the outside world"; the result
  highlights the path on the map with its verdict.
- Simulation results are shareable via URL.
- A cluster-wide firewall log viewer with rule correlation (which rule
  produced a given log line, where determinable) and protection against
  log storms overwhelming the browser.

## [0.8.0] - 2026

True multi-node clustering, physical-layer discovery, and the SDN/IPAM
cockpit. (Includes Phase 3's cluster/discovery work, which the roadmap
does not cut as a separate release.)

### Added — cluster & discovery

- Genuine multi-node clustering: every view and every edit works the same
  way whether the node involved is local or a cluster peer.
- Physical switch discovery via LLDP: real switches now appear on the
  topology map with port-level wiring to your hosts.
- A VLAN cross-check that compares your bridge/bond VLAN configuration
  against what your switches actually advertise over LLDP, and flags
  mismatches.
- A dedicated ports table (with CSV export) showing every LLDP-discovered
  physical link.
- Automatic drift detection: vnprox continuously checks for configuration
  inconsistencies across your cluster — mismatched same-named bridges,
  MTU mismatches along a path or across nodes, SDN configuration that
  doesn't match zone membership, staged-but-never-applied network changes,
  and live state that's drifted from declared configuration — with a
  one-click fixing change wherever the fix is unambiguous.
- Distributed rollback safety for multi-node changes: each affected node
  arms its own independent safety timer, so no single node's rollback
  protection depends on the others staying reachable.
- A cluster-wide MAC/FDB browser: look up any MAC address (or part of one)
  and see which bridge and port it's on, and which guest owns it.
- Cluster-wide audit log and snapshot history, with a clear "partial
  results" indicator if a peer is temporarily unreachable — never a silent
  gap.

### Added — SDN & IPAM

- An SDN cockpit: browse your zones, VNets, and subnets as a tree with
  per-node health/apply status, and see exactly what a staged-but-
  unapplied SDN change will do before you apply it.
- A topology map overlay for SDN — VNet planes across the bridges that
  realize them, and a VXLAN/EVPN tunnel mesh with MTU annotations.
- Guided setup wizards for all five SDN zone types (Simple/NAT, VLAN,
  QinQ, VXLAN, EVPN), each with plain-English explanations and a live
  preview of the resulting topology as you fill in the form.
- The VLAN zone wizard cross-checks your chosen VLAN ID against what LLDP
  says your switch port actually trunks, and warns you if it's missing.
- The VXLAN wizard shows the MTU math explicitly (underlay MTU minus
  overhead) with a one-click "use the safe value" fix.
- EVPN/BGP observability: a peering matrix, per-session detail (prefixes,
  uptime, last error), a VNI list, exit-node health, and detection of
  flapping BGP sessions.
- Visual IPAM: color-coded allocation grids for your subnets showing
  confidence (allocated via Proxmox IPAM, observed via guest agent, both,
  or conflicting), automatic conflict detection (duplicate IPs, observed-
  but-unallocated addresses, allocations that don't match any known
  guest) with suggested resolutions, and a "next free address" picker
  usable directly from the bridge editor.
- DHCP range management on SDN subnets, plus a live leases view correlated
  to your guests by MAC address — reservations and allocations are one
  and the same record, shown consistently in both the IPAM grid and the
  DHCP view.
- First-class Open vSwitch support: OVS bridges, bonds, and internal ports
  are visualized, edited, and validated with the same safety checks as
  Linux bridging.

## [0.5.0] - 2026

Beta: the change engine and core network editing. This is the first
release where vnprox can actually modify your network, not just show it
to you.

### Added

- Safe network editing end to end: every change is staged as a draft,
  validated, shown as a diff, applied, and requires explicit confirmation
  — with automatic rollback if you never confirm.
- A changeset drawer: accumulate multiple pending edits, reorder them,
  and park a named draft to resume later.
- Live, plain-English validation as you edit, with one-click fixes for
  common problems (e.g. an MTU or VLAN ID out of range).
- A review screen before every apply, with three views of exactly what
  will happen: a human-readable summary, the literal file diff, and the
  execution plan.
- A commit-confirm safety window after every apply: a visible countdown
  during which you must confirm the change worked; if you don't (for
  example, because the change broke your connection to vnprox), it's
  automatically rolled back.
- Protected-interface guardrails: vnprox detects your management IP and
  cluster (corosync) links during setup and blocks any change that would
  cut them off, unless you explicitly override it.
- Deleting a bridge that still has running VMs/containers attached is
  blocked unless you also move those guests in the same change.
- Full editors for bridges (including VLAN-aware bridges with a VID range
  editor), bonds, VLAN sub-interfaces, and plain interfaces, each with
  inline plain-English help.
- Drag-and-drop editing directly on the topology map — drag a NIC onto a
  bond or bridge, or retarget a guest's network connection.
- Bulk guest network reattachment: move many VMs/containers to a new
  bridge in a single operation.
- A raw interfaces-file editor for advanced users, with syntax
  highlighting and live linting, protected by the same safety checks as
  the guided editors.
- Full change history: every applied change is snapshotted; browse,
  diff against any other point in time or against live state, and
  restore.
- An audit log recording every action taken through vnprox.
- `vnproxctl` command-line recovery tools that work even if the daemon
  itself is down (direct snapshot restore and emergency rollback).

## [0.1.0] - 2026

Private preview: read-only visibility.

### Added

- Installable as a Proxmox VE add-on via a `.deb` package with a
  systemd service.
- Log in with your existing Proxmox VE credentials — no separate account
  or password to manage.
- A live, auto-laid-out network topology map of your whole cluster:
  physical NICs, bonds, bridges, VLANs, and guest network connections.
- Four togglable map layers (physical / L2 / SDN / guests) so you can
  focus on the level of detail you need.
- Cluster-wide search by name, MAC address, IP address, or VM/container
  ID, with a keyboard shortcut to jump straight to it.
- Click any element on the map for full detail: its normalized fields,
  live status, and raw underlying configuration.
- Real-time updates: the map reflects changes on your cluster within one
  polling cycle, no manual refresh needed.
- Works from any single node's vnprox instance — the whole cluster's
  network is visible regardless of which node you're connected to.
- Dark and light themes, and keyboard shortcuts for layer toggles, VLAN
  filtering, and search.
- Read-only by design at this stage, and permission-aware: what you can
  see mirrors your existing Proxmox VE permissions, and vnprox never
  modifies your network configuration.
