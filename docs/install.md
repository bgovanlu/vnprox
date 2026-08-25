# Install

**Honesty note first, because it changes which section applies to you:** as of 2026-08-18
(T-3301), `apt.vnprox.com` is a real, signed apt repository — nginx serving a real, signed
`Release` file and package tree from `pve001` — but two things are true at once and both matter.
First, **the name itself resolves nowhere**: `apt.vnprox.com` is NXDOMAIN from the public internet
*and* from every host inside this project's own dev environment, pending a DNS/edge-proxy step
outside this repository (see `planning/reports/evidence/registry-vnprox-com-2026-08-24.txt` for the
transcript — the same gap affects `registry.vnprox.com` and `demo.vnprox.com`). It is reachable
today only by sending an explicit `Host:` header to the serving IP, which is not something an
installer or a package manager does. Second — and this is the part a reader with only the DNS
caveat in mind would miss — **the signing key for this hosting lives only on the host serving it**,
which this project has no credentials for; nobody working from this repository can currently publish
a new package, rotate the key, or (for the sibling blueprint/plugin registry) publish a revocation.
Neither of those is a lie on its own, and saying only one invites the wrong mental model: "hosted
and serving" reads as "reachable," and "not resolvable yet" reads as "otherwise fully operable" —
this page is written in two parts so the eventual reader doesn't have to relearn the shape of the
thing: **what works today**, if you already have this source tree, and **what will work once
distribution is publicly reachable AND its keys are reachable by whoever needs to publish**.

## Supported platforms

Proxmox VE 8.2+ (Debian 12) or 9.x (Debian 13), amd64 or arm64, installed on every node of the
cluster. Full detail, including the PVE 10.x/11.x forward-compatibility statement: `deployment.md`
§Supported platforms.

## Try it without installing anything

If you already have a `vnproxd` binary (built from source, below, or from a future release):

```bash
vnproxd --demo
```

Runs the whole product against a synthetic three-node cluster built into the binary — no Proxmox
VE endpoint, no outbound network, no root required. Log in with `root` / `vnprox-mock` / realm
`pam`. See `features/demo-mode.md`.

## What works today: building and installing from this source tree

There is no public release to download from, so today's path is: build it yourself, then install
the package it produces exactly the way a downloaded one would install.

```bash
make deb                          # from the repository root; builds the SPA first, then the .deb
ls dist/vnprox_*_*.deb             # version is derived from `git describe` — packaging/version.sh

# on the target PVE node:
bash packaging/install.sh --offline dist/vnprox_<version>_<arch>.deb --skip-pve-check
```

`--offline <file>` is the installer's local-package path — it does not touch the network for the
package itself, runs the same PVE-detection, port-conflict, and node-setup steps as every other
path, and offers the same cluster SSH rollout. `--skip-pve-check` is only needed if you're trying
this on a machine that isn't a real PVE node (a container, for local evaluation); drop it on real
hardware. Full flag reference: `install.sh --help`, or `deployment.md`'s "Quick install (script)"
section, which documents every step this script takes.

**Verified:** `make deb` and `packaging/install.sh --offline` are exercised by this repository's
own container tests (`packaging/test/deb-install.sh`, `packaging/test/port-conflict.sh`,
`packaging/test/cluster-ssh.sh`) inside real Debian 12/13 containers — not a claim about what the
script is *supposed* to do.

## What will work once vnprox has a published release (not yet true)

Everything below is real code, already written and tested against the fixtures noted, waiting for
somewhere to point at. See `packaging/apt-repo.md`'s "Status" section for exactly what exists
(the tooling) versus what doesn't (a live `apt.vnprox.com` host and a production signing key).

### Quick install (script)

```bash
# On any one node; the script offers to roll out to all cluster nodes via SSH.
curl -fsSL https://apt.vnprox.com/install.sh -o install.sh
less install.sh   # you're piping root on a hypervisor — read it
bash install.sh
```

Every download this script makes is signature-verified against a pinned key fingerprint before
anything is installed — there is no `--insecure` and no way to turn that off. See
`deployment.md`'s "Signatures and trust" section. **Today, the script's own pinned fingerprint is
still a placeholder and every download path refuses to proceed because of it** — that's the
installer failing closed on purpose (`packaging/install.sh`'s own comment on
`VNPROX_RELEASE_KEY_FPR`), not a bug.

### Manual apt configuration

```bash
curl -fsSL https://apt.vnprox.com/vnprox-archive-keyring.gpg \
  | gpg --dearmor -o /usr/share/keyrings/vnprox-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/vnprox-archive-keyring.gpg] https://apt.vnprox.com stable main" \
  > /etc/apt/sources.list.d/vnprox.list
apt-get update
apt-get install vnprox
```

**Verified:** the repository layout these commands assume, `packaging/build-apt-repo.sh`'s output,
was built with an ephemeral test key, `gpg --verify`'d, and `apt-get install`'d cleanly inside a
fresh Debian 13 container over `file://` and plain HTTP — see `packaging/apt-repo.md`'s own
"Verified" section. What's unverified is the last mile: a real `apt.vnprox.com` and a real
production signing key, neither of which exist.

### Unprivileged / air-gapped (binary tarball)

```bash
bash install.sh --prefix ~/.local --release-key vnprox-release.asc
```

Installs verified binaries under `<prefix>/bin` and stops — no systemd unit, no PVE token, no
root. Needs a `vnprox-release.asc` trust anchor and a reachable `--dist-url`, neither of which is
published yet.

## Upgrading, configuration reference, and troubleshooting

Once installed (by any path above), upgrades, the full `vnprox.toml` reference, and the supported
schema-migration path are documented in `deployment.md` — not duplicated here, so there is exactly
one place that can go stale.
