# packaging/

Everything needed to build and install vnprox as a real Debian package,
plus the operator-facing scripts documented in `docs/deployment.md`. See
`planning/tasks/phase-0.md#T-006` for the task this was built against.

## Layout

```
packaging/
  Makefile              `make deb` entry point (invoked by the root Makefile)
  version.sh             computes the Debian package version from git
  debian/                dpkg-deb control scripts (control.tmpl, postinst, prerm, postrm, conffiles)
  systemd/vnprox.service  the hardened systemd unit (docs/security.md "Host footprint")
  config/vnprox.toml      default config, shipped as a conffile at /etc/vnprox/vnprox.toml
  bin/vnprox-setup        per-node interactive setup (docs/deployment.md "Manual install")
  install.sh              the curl-fetchable quick-install script (docs/deployment.md "Quick install")
  build-apt-repo.sh       assembles + signs the apt repo release.yml publishes (see apt-repo.md)
  apt-repo.md             apt repo layout + signing key doc (docs/development.md's release.yml spec)
  test/                   container-based verification scripts (see below)
```

## Building

```
make deb            # from the repo root; builds dist/vnprox_<version>_<arch>.deb
```

This builds `vnproxd` and `vnproxctl` from source (`-trimpath`,
version stamped via `-ldflags -X main.version=...`), assembles a package
root under `packaging/build/pkgroot/`, and calls `dpkg-deb --root-owner-group
--build`. No `nfpm` dependency — `dpkg-deb` alone is sufficient and is
present on every Debian build host and target.

The version string comes from `version.sh`: the current git tag if HEAD is
tagged, else `0.0.0+g<short-hash>` (Debian versions must start with a
digit, so a bare commit hash isn't usable as-is).

## What the package does

- `vnproxd` → `/usr/bin/vnproxd`, `vnproxctl` → `/usr/bin/vnproxctl`,
  `vnprox-setup` → `/usr/bin/vnprox-setup`.
- Systemd unit → `/lib/systemd/system/vnprox.service`, with every hardening
  directive `docs/security.md`'s "Host footprint" section requires as a v1
  item (`ProtectSystem=strict` + explicit `ReadWritePaths`, `ProtectHome`,
  `PrivateTmp`, `NoNewPrivileges`, a capability bounding set), plus some
  additional standard-safe hardening (see the unit file's own comments for
  exactly which directives are "required by the doc" vs. "extra, remove
  first if something regresses").
- Default config → `/etc/vnprox/vnprox.toml`, marked as a **conffile** (an
  `apt upgrade` never silently clobbers a local edit).
- `postinst` creates `/var/lib/vnprox` and idempotently generates the
  session encryption key at `/etc/vnprox/keys/session.key` (root:root 0600,
  AES-256-GCM key material, never regenerated once present). It does **not**
  enable or start the service — the shipped default config assumes a real
  PVE node (certificate reuse, live PVE API) and would crash-loop on a bare
  install; enabling/starting is `vnprox-setup`'s / the manual install flow's
  job, exactly as `docs/deployment.md` documents.
- `prerm`/`postrm` implement the documented `apt remove` (keeps
  `/etc/vnprox` and `/var/lib/vnprox`) vs. `apt purge` (removes both)
  distinction. Network configuration (`/etc/network`, `/etc/pve`) is never
  touched by any of these scripts.

## Status (T-606: install/upgrade/uninstall, apt repo, cluster rollout)

`install.sh` and `vnprox-setup` implement the full documented flow for
real: cluster detection (`pvecm`), port-conflict handling, PVE API token
provisioning (`pveum`, idempotent), cluster-secret bootstrap, multi-node
SSH rollout (per-node, with a per-node manual-instructions fallback when a
node isn't reachable), and apt-repo-or-`--offline` package installation.
`vnproxctl status/snapshots/rollback-now` are all real (T-206's
daemon-independent disaster-recovery path; T-606 completed `status`'s rich
output — peer reachability, PVE API health, collector ages, per
docs/deployment.md).

What genuinely cannot be exercised in a sandbox without a live Proxmox VE
cluster (CLAUDE.md: "you do NOT have a live Proxmox cluster") — the exact
`pveum`/`pvecm` output shapes and real pmxcfs replication semantics are
approximated by fixtures/stubs in `test/` rather than assumed; see the
T-606 completion report's "needs hardware validation" list for specifics.

## Testing (`test/`)

Every script spins up throwaway podman container(s) and is self-contained;
run after `make deb`:

```
bash packaging/test/deb-install.sh       # install / apt remove / apt purge semantics
bash packaging/test/port-conflict.sh     # install.sh's port-8007-conflict detection
bash packaging/test/pve-token.sh         # vnprox-setup's PVE token/role provisioning, idempotent (fakepveum)
bash packaging/test/answers-parity.sh    # --answers file vs. interactive flow, resulting state diffed
bash packaging/test/upgrade.sh           # dpkg upgrade (two synthetic versions — no real tag exists yet)
bash packaging/test/cluster-ssh.sh       # 3-container SSH cluster rollout simulation
```

Set `VNPROX_TEST_IMAGE=debian:13` (default `debian:12`) to run any of them
against the other supported base; `.github/workflows/packaging-matrix.yml`
runs the full set against both.

Notes for the sandbox these were developed in (may not apply on a normal
Debian/CI host, including GitHub Actions' hosted runners): rootless podman
here has no `pasta` binary, so a user-defined bridge network (real
per-container IPs/hostnames) isn't available — every script uses
`--network=host` instead (real internet access for `apt-get`, and for
`cluster-ssh.sh`, one sshd per simulated node on a distinct port with SSH
client aliases, rather than per-container addresses). This is an
environment constraint documented in each script's header, not a design
choice: nothing under test depends on which addressing scheme reaches a
"node".

None of these scripts exercise a live `systemctl start vnprox` under a
real PID 1 systemd — plain `debian:12`/`debian:13` containers don't run
systemd as PID 1. That *was* verified once, manually, by building a custom
systemd-enabled image (`podman run --systemd=always ...`) and confirming
`systemctl start vnprox` actually serves `/api/v1/health` and
`systemctl stop` drains cleanly; see the T-006 task report for details.
That verification isn't scripted into `test/` because it needs a
non-stock base image, so it's not part of the routine test path.
