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

## What's stubbed, and why

`install.sh` and `vnprox-setup` implement the full documented flow
end-to-end, except for three things that genuinely need a live Proxmox VE
cluster to do or verify, and are marked `TODO(T-606)` at the point they're
skipped rather than faked:

- Installing from a real apt repository (none exists yet — repo tooling is
  T-606's job). Use `install.sh --offline <deb>` in the meantime.
- Creating the PVE API token `vnprox@pve!daemon` (needs a live `pveum`).
- Verifying pmxcfs replication of the cluster secret across real nodes, and
  automatic multi-node SSH rollout (falls back to printing per-node manual
  instructions, which the doc explicitly allows).

`vnproxctl`'s `snapshots list/restore` and `rollback-now` subcommands are
also stubs — they print `available after T-206` and exit non-zero. Only
`status` is real: it hits the local daemon's `/api/v1/health` over TLS.

## Testing (`test/`)

Both scripts spin up a throwaway `debian:12` podman container and are
self-contained; run them after `make deb`:

```
bash packaging/test/deb-install.sh     # install / apt remove / apt purge semantics
bash packaging/test/port-conflict.sh   # install.sh's port-8007-conflict detection
```

Notes for the sandbox they were developed in (may not apply on a normal
Debian/CI host): rootless podman here has no `pasta`/`slirp4netns` binary,
so ordinary bridge networking fails outright; both scripts use
`--network=host` to get real internet access for `apt-get`, and only ever
bind test listeners to `127.0.0.1`, torn down with the container.

Neither script exercises a live `systemctl start vnprox` under a real PID 1
systemd — plain `debian:12` containers don't run systemd as PID 1. That
*was* verified once, manually, by building a custom systemd-enabled image
(`podman run --systemd=always ...`) and confirming `systemctl start vnprox`
actually serves `/api/v1/health` and `systemctl stop` drains cleanly; see
the task report for details. That verification isn't scripted into `test/`
because it needs a non-stock base image, so it's not part of the routine
one-liner test path.
