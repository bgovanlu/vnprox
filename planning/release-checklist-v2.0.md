# v2.0.0 release checklist (T-1208)

Mirrors T-607's v1.0 release checklist (`planning/reports/T-607.md` §8), extended with the
federation-specific gates T-1208 requires. v2.0 is the multi-cluster cut of the v1.4 → v2.0 arc
(`docs/roadmap-next.md`, Phase 12): federation, cross-cluster IPAM + external subnets, DNS
management, guarded switch push, PBS awareness, OIDC SSO.

**Legend:** `[x]` done in this environment · `[ ]` must run on the dev host (real hardware /
packaging / GPU) — vnprox has no live PVE, GPU, or apt-repo host in CI.

## 1. Docs freeze

- [x] `CHANGELOG.md`: `[2.0.0]` entry added (keep-a-changelog style, user-facing), covering
      federation, cross-cluster IPAM/external subnets, DNS, switch push, PBS, OIDC; upgrade note
      ("federation is additive, not a fork"); PVE 9.x/10.x target.
- [x] `docs/performance.md` §10: multi-cluster genscale profile defined (3× scale-lab = 24 nodes /
      900 guests / 120 VNets) and a real aggregator-level perf pass recorded.
- [x] `docs/deployment.md`: PVE 10.x target added; federation deployment note; `[oidc]` / `[switches]`
      config stanzas; v1.x → v2.0 upgrade subsection.
- [x] `docs/security.md`: cluster-registry (T-1201) and switch-driver (T-1205) credential-storage
      notes added; threat-model table gains rows for cluster-registry credential theft, rogue/
      compromised attached cluster, switch credential theft/errant push (OIDC rows already present).
- [x] `docs/user-guide.md` §7: chapters for federation, cross-cluster IPAM/external subnets, DNS,
      switch push, PBS, OIDC — matching the existing chapter structure/tone.
- [x] Per-feature `needs-hardware-validation.md` entries consolidated for the v2.0 surfaces
      (PowerDNS, gNMI/switch, real-IdP OIDC, PVE 10.x, full-daemon multi-cluster genscale).
- [ ] Docs-vs-behavior audit (T-607 AC4 precedent): a systematic pass over `docs/` against shipped
      Phase 8–12 behavior. **Partial** here (the v2.0-surface docs above were audited and corrected);
      a full arc-wide re-audit like T-607's §7 is a dev-host/reviewer task.

## 2. Version stamping

- [x] Version is git-tag-derived (`packaging/version.sh` → `git describe --tags`, `-ldflags -X
      main.version`), exactly as T-607 established — **no code bump needed**. Once `v2.0.0` is tagged,
      `make build` / `make deb` and `vnproxd --version` report `2.0.0` automatically.
- [x] `web/package.json` left at its vestigial `0.1.0` marker, per T-607's precedent (it is a
      private frontend package version, unused for release versioning; the authoritative version is
      the git tag).
- [ ] Confirm `dpkg -I` / `vnproxd --version` report `2.0.0` from a `.deb` built at the real tag
      (dev host, §5).

## 3. Federation-specific release gates

- [x] **Single-cluster regression suite green.** `internal/federation`'s single-cluster paths and
      the T-1202 single-cluster-regression bar (capsule view skipped with exactly one cluster;
      topology byte-identical to pre-Phase-12) pass; `TestScaleProfile_Attaches` and the federation
      test suite are green in `make check`.
- [x] **Multi-cluster failure-isolation suite green.** `TestAggregator_FailureIsolation`,
      `TestAggregator_IPAMSubnets_FailureIsolation`, `TestAggregator_TopologySummary_FailureIsolation`
      pass — one cluster killed mid-aggregation → `partial: true` + `failedClusters`, others intact.
- [x] **No cross-cluster mutation primitive.** `internal/change` rejects an op whose target Ref
      belongs to a different cluster than the changeset's `clusterId` (T-1201 AC4 regression).
- [x] **New credential classes encrypted at rest.** `TestService_CredentialCiphertextNeverContains
      Plaintext` (clusters), `TestSwitchRepo_CredentialsEncryptedAtRest` (switches, added by T-1208),
      OIDC link/store tests — each seals with the single AES-256-GCM session key and asserts the
      stored bytes never contain the plaintext.
- [x] **Switch push ships dark.** Push requires both the daemon `[switches] enabled` flag and the
      per-switch `enabled` row; scoped to PVE-facing ports; mandatory pre-write LLDP-neighbor
      re-check; mgmt-path interlock (`safety.protected_switch_port`, no override).

## 4. Migration / upgrade path

- [x] Forward-only migration v1.x → v2.0 proven: `internal/store`'s
      `TestMigrate_FromEachPriorSchemaVersion` freezes a DB at each prior schema version (including
      the last pre-federation one) and migrates to the current version (24), asserting every
      pre-existing row survives byte-for-byte. Federation adds migrations `0021_clusters` …
      `0024_oidc`; no v1.x row is rewritten.
- [x] Schema version stays at `24` — **no new migration added by this release task** (kv_test.go's
      asserted schema version unchanged).
- [ ] **apt upgrade test** (`packaging/test/upgrade.sh`, podman) from a v1.x-schema DB fixture onto
      v2.0 — dev host. Note: real v1.x release tags now exist (`v1.0.0` … `v1.3.6`), so this can use
      a real prior tag as the "old" version rather than only the synthetic-bump path the script's
      (now-stale) header comment describes.
- [ ] **Zero-clusters-attached smoke test** on a real single-cluster install after upgrade: the
      daemon starts and serves its existing single-cluster surface unchanged (dev host).

## 5. Packaging / release workflow (dev host)

- [ ] `make -C packaging deb ARCH=amd64` and `ARCH=arm64` produce installable `.deb`s at `2.0.0`.
- [ ] T-606 container test matrix (`packaging/test/*.sh`: deb-install, port-conflict, pve-token,
      cluster-ssh, answers-parity, upgrade) passes on the v2.0 candidate.
- [ ] `.github/workflows/release.yml` dry run (build → deb → `build-apt-repo.sh` with ephemeral-key
      fallback), as T-607 did locally; publishing to `get.vnprox.io` remains the gated placeholder.

## 6. Compatibility

- [x] PVE 8.2+ / 9.x / 10.x target documented (`docs/deployment.md`, `docs/roadmap-next.md`
      versioning section). PVE 10.x is a stated target, flagged needs-hardware-validation.
- [ ] PVE 10.x validated on real hardware (dev host / follow-up validation task, per the "each PVE
      major gets a validation pass within one phase" rule).

## 7. Tag recommendation

Tag the final merged commit as `v2.0.0` (annotated), **after** merge to `main` — not created or
pushed by this task, per CLAUDE.md and T-607's precedent:

```
git tag -a v2.0.0 -m "v2.0.0: beyond the cluster — multi-cluster federation

Attach many PVE clusters to one pane: global topology with per-cluster
drill-down, cross-cluster search and IPAM, external subnet records with
bidirectional NetBox/phpIPAM sync, SDN DNS management, guarded PVE-facing
switch-port push, PBS network awareness, and OIDC SSO. Config ownership
stays strictly per-cluster; every mutation still flows through the change
engine. See CHANGELOG.md."
```
