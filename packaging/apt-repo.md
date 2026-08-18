# vnprox apt repository

How `release.yml` publishes signed `.deb` packages, the repo layout it
produces, and how a client (`install.sh`, or a manually configured node)
consumes it. Companion to `docs/deployment.md`, which documents the
operator-facing install flow this repo backs.

## Layout

A flat, single-suite repo (no per-release codenames — `apt-get upgrade`
always tracks the latest published version, matching `docs/deployment.md`'s
documented `apt update && apt install vnprox` upgrade flow):

```
apt.vnprox.com/
├── vnprox-archive-keyring.gpg      # ASCII-armored public signing key
├── pool/main/v/vnprox/
│   ├── vnprox_1.2.3_amd64.deb
│   └── vnprox_1.2.3_arm64.deb
└── dists/stable/
    ├── Release                     # plaintext index metadata + hashes
    ├── Release.gpg                 # detached signature over Release
    ├── InRelease                   # Release, inline-signed (clearsign)
    └── main/
        ├── binary-amd64/{Packages,Packages.gz}
        └── binary-arm64/{Packages,Packages.gz}
```

`Suite: stable`, `Component: main`. Every tagged release replaces the pool
contents and regenerates `Packages`/`Release`/signatures — this is a
mirror of "current", not a per-version archive (older `.deb`s remain
reachable via the GitHub release assets, not through apt).

## Building it: `packaging/build-apt-repo.sh`

```
packaging/build-apt-repo.sh <repo-dir> <deb-file> [<deb-file> ...]
```

Given one or more built `.deb`s (one per architecture, from `make deb`),
assembles the tree above and signs it. Uses `dpkg-scanpackages` +
`apt-ftparchive`-equivalent hand-rolled `Release` generation (both this
script and the project's other packaging tooling — `packaging/Makefile` —
deliberately avoid a `reprepro`/`aptly` dependency; a flat single-suite repo
is simple enough to assemble by hand with tools already present on any
Debian build host: `dpkg-scanpackages`, `gzip`, `gpg`).

## Signing key

- **Production**: an Ed25519 GPG key held only as a GitHub Actions repository
  secret (`APT_SIGNING_KEY`, ASCII-armored private key exported with
  `gpg --export-secret-keys --armor`). `release.yml` imports it into a
  scratch `GNUPGHOME` for the duration of the release job only — it is
  never written anywhere else, never logged (the workflow does not echo
  secret values; GitHub Actions also redacts registered secrets from logs
  as a second layer). The corresponding public key is committed nowhere in
  this repository on purpose (a public key alone, published at
  `vnprox-archive-keyring.gpg`, is what clients need — see below) but its
  fingerprint should be published out-of-band (project website, release
  notes) so an operator can verify `vnprox-archive-keyring.gpg` themselves
  after fetching it, the same trust-on-first-use caveat every apt
  third-party repo has.
- **Dev/test**: if `VNPROX_SIGNING_KEY_FILE` is unset, `build-apt-repo.sh`
  generates a throwaway Ed25519 key in a temp `GNUPGHOME` (1-day expiry,
  clearly labeled "ephemeral, test-only" in its identity) and signs with
  that instead. This is what `packaging/test/apt-repo.sh` (the T-606
  container test) and any local `make`-driven repo build use — never
  production, and the key is discarded when the script's process exits.

Rotating the production key: generate a new key, add it as a *second*
signed export (not yet implemented — a documented future need, single-key
today, tracked as a follow-up rather than solved speculatively here since
there has been no production key to rotate yet).

## Client configuration

What `install.sh --apt-repo <url>` (default `https://apt.vnprox.com`,
`docs/deployment.md`'s quick-install default) does, and what the manual
"Install" section could tell an operator to do by hand:

```bash
curl -fsSL https://apt.vnprox.com/vnprox-archive-keyring.gpg \
  | gpg --dearmor -o /usr/share/keyrings/vnprox-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/vnprox-archive-keyring.gpg] https://apt.vnprox.com stable main" \
  > /etc/apt/sources.list.d/vnprox.list
apt-get update
apt-get install vnprox
```

`signed-by` (rather than a global `apt-key add`, deprecated since Debian
11/Bullseye) scopes trust in this key to exactly this one repo's requests.

## Verified

`packaging/build-apt-repo.sh`'s output was validated end-to-end in this
task (T-606): a real `.deb` built with `make deb`, assembled into a repo
with an ephemeral key, `gpg --verify`'d successfully against the exported
keyring, and `apt-get install`'d cleanly inside a fresh Debian 13 container
via `file://` (and, in the CI container matrix, plain HTTP) — see
`packaging/test/apt-repo.sh`. What is **not** validated here (needs
infra/hardware, per `CLAUDE.md`'s "needs hardware validation" note): a real
production signing key in GitHub Actions secrets, and an actual live
`apt.vnprox.com` host serving this layout.
