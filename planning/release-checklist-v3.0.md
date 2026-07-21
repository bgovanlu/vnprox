# v3.0.0 release checklist (T-1707)

Mirrors T-1208's v2.0 checklist (`planning/release-checklist-v2.0.md`) and T-607's v1.0 precedent,
extended with the **platform-freeze**, HA, multi-tenant, and plugin-security gates the v3.0 arc
requires. v3.0 is the platform cut of the v2.0 → v3.0 arc (`docs/roadmap-universal.md`, "The open
platform"): an AI-operator MCP surface, a plugin SDK, multi-tenancy, daemon HA, a blueprint/plugin
hub, and embeddable views — with the change engine as the one boundary that keeps all of it safe.

**Legend:** `[x]` done in this environment · `[ ]` must run on the dev host (real hardware /
packaging / multi-instance HA) — vnprox has no live PVE, GPU, apt-repo host, or second physical
node in CI.

## 1. Docs freeze

- [x] `CHANGELOG.md`: `[3.0.0]` entry added (keep-a-changelog, user-facing), covering the MCP
      server, plugin SDK, multi-tenancy, HA, the hub, embeddable views, the platform-API freeze, and
      the PVE 10.x/11.x target; intro updated to point v3.0 at `docs/roadmap-universal.md`.
- [x] `docs/architecture.md`: §10 decisions table gains **D10** (platform API freeze at v3.0) and
      **D11** (peer protocol stays at v2); the duplicate `## 11` heading fixed (HA topology renumbered
      to **§12**); new **§13 Platform API freeze (v3.0)** enumerates the frozen MCP tool manifest v1,
      plugin SDK interfaces v1, event-stream schema v1, and the peer-protocol compat stance.
- [x] `docs/api.md`: `POST /api/peer/ha/replicate` documented in the Peer API section (additive at
      protocol 2, 503-nil-safe).
- [x] `docs/performance.md` §11: HA + multi-cluster genscale profile defined; the 40-cycle
      deterministic HA failover soak recorded as the proven-here safety pass; failover-promotion
      latency / replication-lag / VIP-DNS propagation stated as **targets, flagged
      needs-hardware-validation** (never fabricated).
- [x] `docs/security.md`: threat-model table gains rows for captured-payload exposure (T-1301),
      WireGuard tunnel keys (T-1401), the MCP AI-operator surface (T-1701), and the HA
      replication-channel/rogue-standby case (T-1704); HA replication transport note added
      (reuses the peer TLS+HMAC channel, no new transport credential). Plugin (T-1702) and tenant
      (T-1703) rows already present from those cards.
- [x] `docs/deployment.md`: PVE 10.x/11.x forward-compat target for the v3.0 arc; "Upgrading a v2.x
      install to v3.0" subsection (forward-only migrations `0025`–`0031`, every platform feature
      opt-in/dormant); the HA standby-first upgrade sequence already present from T-1704.
- [x] `docs/user-guide.md` §8: chapters for MCP/AI operators, plugins, multi-tenancy, HA operations,
      the hub, and embeddable views — matching the existing chapter structure/tone.
- [x] Per-feature `needs-hardware-validation.md` entries consolidated for the v3.0 surfaces (T-1707
      section added: HA failover timing/replication-lag at scale, apt upgrade v2.x→v3.0, `.deb`
      version stamp, PVE 10.x/11.x, live MCP client, tenant graph expansion; T-1702/T-1704 sections
      already present).
- [ ] Full docs-vs-behavior arc-wide re-audit (T-607 §7 precedent): **partial** here — the
      v3.0-surface docs above were audited/corrected (incl. the duplicate `§11` fix). A systematic
      arc-13–17 sweep is a dev-host/reviewer task.

## 2. Version stamping

- [x] Version is git-tag-derived (`packaging/version.sh` → `git describe --tags`, `-ldflags -X
      main.version`), exactly as T-607/T-1208 established — **no code bump needed**. Once `v3.0.0` is
      tagged, `make build` / `make deb` and `vnproxd --version` report `3.0.0` automatically.
- [x] `web/package.json` left at its vestigial `0.1.0` marker (private frontend package version,
      unused for release versioning; the authoritative version is the git tag).
- [ ] Confirm `dpkg -I` / `vnproxd --version` report `3.0.0` from a `.deb` built at the real tag
      (dev host, §6).

## 3. Platform-API freeze gates (new for v3.0)

- [x] **Frozen-surface enumeration reviewed.** `docs/architecture.md` §13 names exactly the three
      frozen public surfaces (MCP tool manifest, plugin SDK interfaces, event-stream schema) and
      nothing else, each with the deprecation policy (additive-only; a breaking change mints a new
      version, old kept ≥1 minor release).
- [x] **MCP manifest frozen v1.** The nine-tool allowlist is pinned by the registry-enumeration test
      + the forbidden-verb package-load check; no apply/confirm/rollback/discard tool exists or can
      be added (`internal/mcp` `TestRegistryIsStageOnlyAllowlist` / `TestNoMutatingToolByName` /
      `TestChangesetStagerHasNoMutationVerb`).
- [x] **Plugin interfaces frozen v1.** Five extension points at `plugin.APIVersion == "v1"`; the
      registry refuses an unknown api version; the stage-only `Stager` seam has no mutation method
      (interface-surface tests).
- [x] **Event-stream schema frozen v1.** The flat `{"event", ...}` envelope + the `"events"`-topic
      event set; `internal/apicontract`'s golden fixtures enforce the changeset-API half of the
      freeze in `make check`.
- [x] **Peer protocol compat decided.** Stays at **version 2** for v3.0 (decision D11): T-1704's
      `ha/replicate` is additive and 503-nil-safe; documented in `docs/architecture.md` §13.4 and the
      api.md Peer API section. Resolves the open note T-1704's report left for this freeze pass.

## 4. HA / multi-tenant / plugin-security gates (new for v3.0)

- [x] **HA failover soak green.** `TestFailoverSoak_NoDoubleApply_NoDroppedRollback` (T-1707) drives
      **40 independent failover cycles** (alternating rollback and commit outcomes) and asserts zero
      double-apply and zero dropped-rollback on every cycle. The full `internal/ha` suite
      (arbitration, split-brain, re-arm-same-absolute-deadline, scheduled-window survival) is green.
- [x] **Single-writer fencing holds.** A promoted standby always writes a strictly-higher lease
      term; an isolated active self-demotes after a TTL of failed pushes; a healed old-active adopts
      the newer term and drives nothing; `change.Service`'s unattended timers fire as no-ops off the
      leader (`internal/ha/manager_test.go`, `LeaderGuard`).
- [x] **Tenant-isolation suite green.** Cross-tenant leakage regression (randomized reads, zero
      cross-tenant data), out-of-scope lookup `404` (existence not confirmed), member-cannot-approve
      / approver-cannot-approve-own-request, request-changeset blocked from apply until approved,
      WS upgrade fail-closed for tenant-scoped principals (`internal/api/tenant_test.go`).
- [x] **Plugin-security invariants green.** Capability scope is a server-enforced ceiling; the only
      change-engine seam is stage-only (no Apply/Confirm/Rollback reachable, in-process or
      out-of-process); an out-of-scope op is rejected before reaching `internal/change`;
      install/enable/disable/uninstall audited with scope; a killed out-of-process plugin degrades
      gracefully (`internal/plugin` suite, incl. `TestFaultInjection_KillMidFlight`).
- [x] **New credential classes encrypted at rest.** Targeted tests: WireGuard tunnel key
      (`TestWireGuardRepo_PrivateKeyEncryptedAtRest`, **added by T-1707**), WireGuard PSK
      (`TestSealOpSecrets_NoPlaintextPSKAtRest`), switch driver (`TestSwitchRepo_CredentialsEncryptedAtRest`),
      cluster registry (`TestService_CredentialCiphertextNeverContainsPlaintext`), k8s kubeconfig,
      OIDC links, sessions — each sealed with the one AES-256-GCM session key, ciphertext never
      containing plaintext. Captured payload bytes never persisted outside the pcap file
      (`TestNoPayloadBytesPersistedOutsideCaptureFile`). HA introduces no new at-rest credential class
      (replicated sealed columns stay ciphertext; `api_tokens` replicate as one-way hashes).

## 5. Migration / upgrade path

- [x] Forward-only migration v2.x → v3.0 proven: `internal/store`'s
      `TestMigrate_FromEachPriorSchemaVersion` freezes a DB at each prior schema version and migrates
      to the current version (**31**), asserting every pre-existing row survives byte-for-byte. The
      arc adds `0025_flow_baselines` … `0031_ha`; no earlier row is rewritten.
- [x] Schema version is **31** — **no new migration added by this release task** (kv_test.go's
      asserted schema version unchanged; the only code added is test-only).
- [ ] **apt upgrade test** (`packaging/test/upgrade.sh`, podman) from a v2.x-schema DB onto v3.0,
      plus the **HA-pair standby-first-then-active** upgrade sequence on a two-node pair — dev host.
- [ ] **Feature-dormant smoke test** after upgrade: a v2.x install upgraded to v3.0 with no
      `[mcp]`/`[ha]`/`[hub]` config, no plugins, and no tenants serves its existing surface unchanged
      (dev host).

## 6. Packaging / release workflow (dev host)

- [ ] `make -C packaging deb ARCH=amd64` and `ARCH=arm64` produce installable `.deb`s at `3.0.0`.
- [ ] T-606 container test matrix (`packaging/test/*.sh`) passes on the v3.0 candidate.
- [ ] `.github/workflows/release.yml` dry run (build → deb → apt-repo with ephemeral-key fallback);
      publishing to `get.vnprox.io` remains the gated placeholder.

## 7. Compatibility

- [x] PVE 8.2+ / 9.x supported; **10.x / 11.x** stated forward targets for the v3.0 arc
      (`docs/deployment.md`, `docs/roadmap-universal.md` versioning section), flagged
      needs-hardware-validation.
- [ ] PVE 10.x and 11.x validated on real hardware (dev host / follow-up validation task, per the
      "each PVE major gets a validation pass within one phase" rule), tracking new SDN capabilities
      (fabrics, NAT zones).

## 8. `make check`

- [x] Full `make check` green across the v3.0 surface (golangci-lint incl. fieldalignment+shadow,
      `go test ./...`, `tsc`, eslint, Vitest, govulncheck where installed, `npm audit`). Tail pasted
      in `planning/reports/T-1707.md`.

## 9. Tag recommendation

Tag the final merged commit as `v3.0.0` (annotated), **after** merge to `main` — **not** created or
pushed by this task, per CLAUDE.md and the T-607/T-1208 precedent:

```
git tag -a v3.0.0 -m "v3.0.0: the open platform

vnprox becomes infrastructure: a read/stage-only MCP surface for AI
operators, a capability-scoped plugin SDK, delegated multi-tenancy with an
approval workflow, active/standby daemon HA whose commit-confirm timers
survive failover, a signed blueprint/plugin hub, and embeddable read-only
views. The MCP tool surface, plugin interfaces, and event stream are frozen
as stable v1 contracts. Every new surface still stages through the one
change engine; DB migrations stay forward-only. See CHANGELOG.md."
```
