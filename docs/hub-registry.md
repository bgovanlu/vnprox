# The hosted blueprint & plugin registry (T-2803, T-2104)

How the registry `internal/hub` browses is published, reviewed, signed and
revoked. Companion to `docs/features/blueprints.md` §7 (the client) and
`docs/security.md`'s hub section (the trust model). The apt-repository
equivalent of this document is `packaging/apt-repo.md`, and the posture is
deliberately identical: **static hosting, no service to operate**.

## What the registry is

Two things on a static host (object storage, GitHub Pages, any web server):

```
hub.example.com/vnprox/
├── index.json                                   # the signed catalog
└── artifacts/
    ├── blueprint/<id>/<version>.json            # a T-1107 signed bundle
    └── plugin/<id>/<version>.json               # a {manifest, signature} artifact
```

There is no API, no database and no daemon. Publishing is a directory change
committed to the registry repository; deploying is copying that directory to
the host. `[hub] registry_url` points at the directory above.

## The index document

`index.json` is a strict superset of the client's existing index contract
(`docs/api.md`'s Hub section), which is why the client consumes it unmodified:

```jsonc
{
  "schemaVersion": 1,
  "generatedAt": 1770000000,
  "entries": [                    // exactly the pre-existing entry shape
    { "type": "blueprint", "id": "ceph-3node", "name": "…", "version": "1.0.0",
      "publisher": "acme", "description": "…",
      "artifactUrl": "/artifacts/blueprint/ceph-3node/1.0.0.json",
      "signature": { "alg": "ed25519", "publicKeyFingerprint": "…", "publicKey": "…", "sig": "…" } }
  ],
  "revocations": [                // additive; see below
    { "type": "plugin", "id": "flowtap", "version": "0.3.1",
      "reason": "capability scope escalated in a silent update", "at": 1770000100 }
  ],
  "signature": { "alg": "ed25519", "publicKeyFingerprint": "…", "publicKey": "…", "sig": "…" }
}
```

- `generatedAt`, `revocations` and `signature` are **additive**: a client that
  does not know about them ignores them and reads the catalog exactly as
  before. Nothing about the entry shape changed.
- The index `signature` is an Ed25519 signature by the **registry's index key**
  over the document with `signature` removed, using T-1107's envelope and
  primitive (`blueprint.BundleSignature` / `VerifySignature`) — not a second
  signature format.
- `artifactUrl` is **derived**, never hand-written:
  `/artifacts/{type}/{id}/{version}.json`. Ids and versions are restricted to
  letters, digits and `. - _ +`, so a published path can never escape the
  artifact tree.
- A published `(type, id, version)` is **immutable**. Changing an artifact
  means publishing a new version; the tooling refuses an in-place change.

### What the index signature does and does not buy

Artifacts were already signed before T-2803, and the artifact signature plus
the per-installation trust store remains **the only install gate**. The index
signature is about the *catalog*:

- **Catalog integrity.** An attacker in front of the hosting cannot silently
  remove an entry, downgrade a version, or re-point an `artifactUrl`. A
  corrupted index fails verification whole — the client gets an error and zero
  entries, never a partial catalog.
- **Offline revocation.** Revocations ride inside the same signed document, so
  a client honours them from the index it already fetched — no second
  endpoint, no OCSP-style live call, nothing to be unreachable at the moment it
  matters.

It buys **no** trust in an artifact. A signed index entry for an
untrusted-signer artifact still stops at the operator's explicit trust step.

## Operator setup

```toml
[hub]
registry_url  = "https://hub.example.com/vnprox"
index_signers = ["<the registry index key fingerprint>"]
```

With `index_signers` set, the daemon installs the T-2803 gate on the hub
client's existing HTTP seam: an index that is unsigned, corrupted, or signed by
another key yields nothing at all, and revocations are enforced on every
artifact fetch without further network access. Leaving it empty logs a warning
at startup and keeps the pre-T-2803 behaviour (an unauthenticated catalog and
no revocation enforcement); artifact signatures and the trust store still gate
every install either way.

Verify a published index yourself, exactly as the daemon does:

```
vnproxctl hub verify --index index.json --signers <fingerprint>
```

## Publishing: the process

The publisher and the registry are separated on purpose — a publisher signs
their own artifact but **cannot index it**; a reviewer indexes it but does not
hold the publisher's key.

### 1. Publisher: sign and submit

```
vnproxctl hub keygen  --key ~/.config/vnprox/publisher.key       # once
vnproxctl hub publish --artifact ceph-3node.bundle.json \
                      --type blueprint --version 1.0.0 \
                      --key ~/.config/vnprox/publisher.key \
                      --publisher acme --out submissions/ceph-3node-1.0.0.json
```

`publish` signs the artifact, derives the catalog entry from the artifact's own
contents (id, name, capability scope, extension points — never from
publisher-supplied prose), derives the artifact URL, and writes a submission
file. **Submitting is opening a pull request** against the registry repository
with that file. Publishing an unsigned artifact requires `--allow-unsigned` and
is reported loudly, because an unsigned artifact can only be installed behind
the operator's explicit `trustUnsigned` step — which is itself double-gated
(T-2904): the request flag alone is never sufficient. The **server** must also
opt in with `[hub] trust_unsigned = true`, which warns at every startup; with
it off (the default), a `trustUnsigned: true` request is refused with a `403`
naming the config key. Signed artifacts are unaffected by both flags —
signature verification is never optional. A plugin's declared `endpoint` is
additionally constrained at install (T-2904): it must be an absolute path that
resolves — symlinks included — to a regular file inside the vnprox-owned
plugin install root (`/var/lib/vnprox/plugins`); bare names are never looked
up via `$PATH`, and any path or symlink escaping the root is refused with the
constraint named.

### 2. Reviewer: what gets checked

A reviewer reads the submission diff and checks, at minimum:

| Check | Why |
|---|---|
| The artifact is signed, and the signer is who the PR claims | The signature is what the eventual install decision is made against |
| The declared **capability scope** is the minimum the plugin needs | The scope is shown to operators before install and is enforced at runtime; an over-broad scope is the plugin supply chain's main risk |
| The extension points match what the plugin actually implements | A mismatch means an operator approves something other than what runs |
| The blueprint applies cleanly against `internal/pvemock` and produces the documented topology | A blueprint that does not apply is a support incident, not a catalog entry |
| Nothing in the artifact reaches outside vnprox's documented op set | The registry distributes bundles; it never relaxes what the client enforces |
| The version has not been published before | Published versions are immutable |

`vnproxctl hub index` re-checks mechanically what a human cannot reliably check
by reading a diff: that the artifact's identity agrees with its entry, that the
entry advertises the artifact's *own* signature, that the artifact URL is the
derived one, and that the signature verifies over the exact bytes being
published. It does not need to separately re-check that a plugin entry's
`capabilities`/`extensionPoints` agree with the artifact's manifest, because
`vnproxctl hub publish` *derives* those catalog fields from the manifest in
the first place (`hubreg.BuildSubmission`) — a submission built through the
documented tooling cannot disagree with itself. The daemon does not lean on
that alone, though: **`POST /hub/install` (T-2104) independently refuses an
install whose catalog entry and downloaded artifact disagree about
capabilities or extension points**, before any signature/trust decision —
status `capabilityMismatch`, no trust flag overrides it (`docs/security.md`'s
Hub section). That is the belt this table's "minimum scope" row is the
suspenders for: an operator's install decision is made against what
`GET /hub/index` showed them, so installing something else, however it
happened, is refused rather than silently allowed.

### 3. Reviewer: index it

```
vnproxctl hub index --root ./registry \
                    --submission submissions/ceph-3node-1.0.0.json \
                    --key /path/to/index-signing.key
```

This writes the artifact into `./registry/artifacts/...`, adds one entry, and
re-signs `./registry/index.json`. It is **idempotent**: re-running with the
same submission changes nothing — one entry, and `index.json` is not even
rewritten. A *different* artifact under an already-published version is
refused (exit code 3) and leaves the index untouched.

Deploying is publishing `./registry` to the static host.

## Revocation

```
# one version
vnproxctl hub revoke --root ./registry --key index-signing.key \
                     --type plugin --id flowtap --version 0.3.1 \
                     --reason "capability scope escalated in a silent update"

# every version of an id
vnproxctl hub revoke --root ./registry --key index-signing.key \
                     --type plugin --id flowtap --reason "unmaintained, known RCE"

# everything a compromised key signed
vnproxctl hub revoke --root ./registry --key index-signing.key \
                     --signer <publisher fingerprint> \
                     --reason "publisher signing key compromised"
```

A revoked entry stays in the signed document (so the revocation is itself
published and auditable) but is not offered to clients, and any fetch of its
artifact is refused. Because the revocation is inside the signed index, a
client that has fetched the index honours it with no further network access —
including when the registry is unreachable.

Revocation is a **catalog** action. It stops new installs; it does not reach
into an installation and remove something already installed. Withdrawing
something already deployed is an operator action, and the revocation reason is
what tells them to take it.

## Keys

Two distinct Ed25519 identities, both in T-1107's on-disk format
(base64 seed, `0600`, never silently overwritten — `vnproxctl hub keygen`):

| Key | Held by | Signs | Compromise means |
|---|---|---|---|
| Publisher key | each publisher | their own artifacts | artifacts appear to come from that publisher — revoke by `--signer`, and every installation that pinned the fingerprint must untrust it |
| Index key | the registry | `index.json` | the catalog can be rewritten: entries added, removed or re-pointed, and **revocations stripped** |

The index key is the higher-value secret. It should live only in the registry
repository's CI secrets, be used only by the publish job, and never be
available to a pull-request-triggered run — the same handling
`packaging/apt-repo.md` documents for the apt signing key, and for the same
reason.

**Index key rotation.** The fingerprint is operator-pinned config, so rotation
is a coordinated change, not a silent one:

1. Generate the new key and publish its fingerprint out of band (release notes,
   the project site) alongside the current one.
2. Publish an index signed by the **old** key that announces the change (a
   release note is sufficient — the index itself carries no key-transition
   field on purpose; adding one would let a compromised key nominate its own
   successor).
3. Ask operators to set `index_signers = ["<old>", "<new>"]` — the list accepts
   several fingerprints precisely so a rotation has an overlap window.
4. Re-sign with the new key, then drop the old fingerprint from the published
   guidance.

An operator who has not rotated sees an untrusted-signer failure and an empty
catalog: the failure mode is a visibly broken hub, never a silently accepted
new key.

## Status: what exists and what does not

- **Exists in this repository:** the index format, the signing/verification
  code (`internal/hubreg`), the client-side gate, the publisher/reviewer CLI
  (`vnproxctl hub`), this process, and (T-2104) the registry's first real
  content: `internal/hub/seed` ships four seeded blueprints — homelab
  single-node, three-node Ceph cluster storage, VLAN-segmented SMB branch
  office, and a DMZ fronting a WireGuard site-to-site tunnel (T-3303 closed
  blueprint v1's missing `wg.*` entity kind gap: this seed now provisions the
  DMZ segment AND the local WireGuard tunnel interface. Still PARTIAL,
  narrower now — the remote peer needs a public key exchanged out of band,
  which cannot exist at instantiation time, so it is still configured
  separately; see the package's doc comment). Every seed is validated,
  instantiates to the documented changeset against a bare fixture — the
  DMZ+WireGuard seed's wg-tunnel entity is the one exception to "zero ops
  against an already-conforming one": it always proposes a create, since
  inventory.Snapshot never contains a wg-tunnel to diff against (see
  `diffWgTunnel`'s doc comment) — and the three-node Ceph seed's produced
  ops are additionally applied through a real `internal/pve.Client` against
  a running `internal/pvemock` server and read back to confirm the exact
  zone/vnet/subnet topology landed (`internal/hub/seed`'s tests).
  `cmd/vnproxctl`'s `TestHubCLI_SeedBlueprintsPublishReviewIndex` walks the
  submission/review process above once, end to end, with each seed as the
  real bundle: signed, submitted, indexed, verified, and the published
  artifact read back and re-verified to confirm real multi-entity content —
  not the placeholder fixture — survives the pipeline intact.
- **Hosted, 2026-08-18 (T-3303):** `registry.vnprox.com` — static nginx
  hosting on pve001 (`/srv/vnprox-registry`), reverse-proxied the same way
  as `apt.vnprox.com`/`demo.vnprox.com`. All four seeded blueprints above
  are published on it for real, through the exact `vnproxctl hub
  publish`/`hub index` steps this document describes (not a copy of the
  test fixture) — the index carries 4 signed entries, verifies against the
  registry's own key, and `GET /index.json` serves it. The registry index
  signing key lives only on pve001 (`/etc/vnprox-release/registry-index.key`,
  mirroring the apt repo's signing-key posture in `packaging/apt-repo.md`),
  never in this repository. Not yet done: `apt.vnprox.com`/`demo.vnprox.com`/
  `registry.vnprox.com` don't resolve publicly yet, pending the VPS
  reverse-proxy leg of T-3301/T-3303 — see `docs/development.md`'s CI
  section for the network design.
