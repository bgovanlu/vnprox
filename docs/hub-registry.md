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

`registry_url` also accepts a local mirror directory (a bare path, or an
explicit `file://` URL) instead of a hosted `https://` one — see "Air-gapped
operation" below for a cluster with no outbound network at all.

## Sigstore-backed key custody (T-3709)

`index_signers` above is a list of trusted fingerprints an operator has to
obtain somehow. Historically that has meant an unverifiable side channel —
release notes, a forum post, a maintainer's word — for exactly the value
that decides whether the daemon trusts a registry's catalog at all. T-3709
adds a keyless, publicly-auditable way to obtain that fingerprint instead,
using [sigstore-go](https://github.com/sigstore/sigstore-go): Fulcio-issued,
OIDC-bound certificates and Rekor transparency-log inclusion.

**This does not change what the daemon does.** `index.json` is still,
unchanged, signed the ordinary Ed25519 way (everything above this section),
and the daemon still runs `internal/hubreg.Gate`/`Verify` on every fetch —
that code has no Sigstore dependency and never will (see the acceptance
test below). What T-3709 adds is a separate, much smaller, much
less-frequently-published document — a **key-custody attestation** — that a
registry's CI can sign keylessly to vouch for which Ed25519 fingerprint(s)
currently hold index-signing custody:

```jsonc
// registry/trusted-signers.json — published far less often than index.json,
// typically only at key rotation
{
  "schemaVersion": 1,
  "generatedAt": 1770000000,
  "registryUrl": "https://hub.example.com/vnprox",
  "indexSigners": [
    { "fingerprint": "<the current index-signing key fingerprint>",
      "note": "primary index key, rotated 2026-08-24" }
  ]
}
```

`.github/workflows/publish-registry.yml` signs this file keylessly
(`cosign sign-blob --bundle`, `id-token: write`) whenever it changes,
writing the sibling bundle `trusted-signers.json.sigstore.json` next to it —
the same `cosign sign-blob --bundle` shape
`internal/hubreg/sigstoreverify` parses.

**Verifying it — an explicit, separate operator step:**

```
vnproxctl hub verify --sigstore-key-bundle trusted-signers.json \
                     --sigstore-bundle trusted-signers.json.sigstore.json \
                     --sigstore-issuer https://token.actions.githubusercontent.com \
                     --sigstore-identity "https://github.com/<owner>/<repo>/.github/workflows/publish-registry.yml@refs/heads/main"
```

This checks the Fulcio certificate chain, the Rekor transparency-log
inclusion, and — the check a bare "the signature verifies" story would
miss — that the signing certificate's **identity** (OIDC issuer + subject,
exact or regexp) matches what you configured. On success it prints the
attested fingerprint(s) and the attestation's own transparency-log entry
id. **vnproxctl never writes daemon config for you.** Completing the pin is
a second, deliberate action: copy the printed fingerprint(s) into your own
`vnprox.toml`'s `[hub] index_signers`, exactly the manual step Ed25519 key
rotation (below, "Index key rotation") has always required — Sigstore
replaces the *unverifiable* side channel that fingerprint used to travel
over, not the manual pin itself.

**Revoking an attestation you no longer trust** uses the same
`--log-entry` addressing mode as any other `hub revoke`, and — unlike
addressing by artifact id or by signer — needs no fingerprint, only the
transparency-log entry id `hub verify --sigstore-key-bundle` printed:

```
vnproxctl hub revoke --root ./registry --key index-signing.key \
                     --log-entry <id> --reason "compromised workflow run"
```

This still needs `--key`, the same as every other `hub revoke` invocation:
`index.json` is always Ed25519-signed in this design (there is no
keylessly-signed document for a revocation to ride inside without a
re-sign), so revoking by log entry writes an ordinary revocation record
(`Revocation.TransparencyLogIndex`) into the same signed index everything
else uses. An operator who wants to check whether a given attestation's log
entry has already been revoked, before pinning its fingerprints, can do so
in the same `hub verify` call:

```
vnproxctl hub verify --sigstore-key-bundle trusted-signers.json \
                     --sigstore-bundle trusted-signers.json.sigstore.json \
                     --sigstore-issuer ... --sigstore-identity ... \
                     --check-revoked-against index.json
```

**Say the cost out loud, and do not describe this as equivalent to
per-fetch keyless verification.** An earlier design (preserved, not merged,
on the `sigstore-in-daemon` branch) verified index.json itself this way, on
every single fetch, inside vnproxd — there was never a persistent private
key anywhere for an attacker to steal; every verification required a fresh
Fulcio certificate from a live OIDC token. That design was abandoned for
its dependency cost: `sigstore-go` grows the module graph from 64 to
roughly 400 modules (a TUF client, a Certificate Transparency verifier,
gRPC, OpenTelemetry all arrive transitively) and vnproxd is the privileged
daemon that controls host networking. This design is **materially
weaker**: a long-lived Ed25519 private key still signs every ordinary index
publish, exactly as before T-3709 existed at all. If that key is stolen
between attestations, an attacker can forge index signatures indefinitely,
and nothing described on this page would ever see it happen — this
mechanism is never in the request path of an ordinary index fetch, only in
the path of an operator explicitly re-pinning trust. What it buys instead
is a better rotation and distribution story: "here is the new fingerprint"
becomes a cryptographically checkable, Fulcio/Rekor-logged claim instead of
an unverifiable side channel — not a removal of key-custody risk. See
`docs/security.md`'s hub section for the same account from the threat-model
side.

**The structural guarantee this still keeps.** A served index can never
downgrade or otherwise change what the daemon trusts, because the daemon
has no code path that can even parse a Sigstore bundle: `internal/hubreg`
(imported by `cmd/vnproxd`) has no dependency on `sigstore-go`, and
`internal/hubreg/sigstoreverify` (which does) is imported only by
`cmd/vnproxctl`. This is checked by the build, not left to a reviewer to
remember — `go list -deps ./cmd/vnproxd` must never contain `sigstore`,
enforced by `cmd/vnproxd`'s own `TestVnproxdDoesNotImportSigstore` on every
`make check`. The only thing a served index can ever do to the daemon's
trust is what it could already do before T-3709: fail Ed25519 verification
against whatever `[hub] index_signers` an operator most recently,
explicitly configured.

## Self-hosting your own registry

Everything below this heading through "Keys" — publishing, indexing, and
revoking — is the complete self-hosting path, unmodified by who runs the
host. It does not depend on the vnprox project's own registry existing,
being reachable, or being trustworthy; a self-hosted registry an operator
fully controls does not share the public registry's current key-custody
limitation (see "Status" below). At a glance, going from nothing to a
served registry:

```
vnproxctl hub keygen --key publisher.key      # once, on a publisher's machine
vnproxctl hub keygen --key index.key          # once, on the registry maintainer's machine
vnproxctl hub publish --artifact <bundle.json> --type blueprint --version 1.0.0 \
                      --key publisher.key --out submission.json
vnproxctl hub index --root ./registry --submission submission.json --key index.key
# serve ./registry over any static host — object storage, GitHub Pages, nginx
```

Then point an installation at it with `[hub] registry_url` and
`index_signers` (above) set to that registry's own index key fingerprint.
`scripts/hub-registry-harness.sh run` runs exactly this sequence against a
real daemon, end to end, including a tampered artifact, a tampered index and
both revocation paths — see its transcript at
`planning/reports/evidence/hub-registry-verification-2026-08-24.txt` for
what each step actually printed. The harness and this section are meant to
agree step for step; if they ever don't, the harness is describing what the
tooling actually does and this section is the one that's stale.

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

# one sigstore key-custody attestation event you no longer trust (T-3709 —
# still needs --key; see "Sigstore-backed key custody" above for why)
vnproxctl hub revoke --root ./registry --key index-signing.key \
                     --log-entry <id> \
                     --reason "compromised workflow run"
```

A revoked entry stays in the signed document (so the revocation is itself
published and auditable) but is not offered to clients, and any fetch of its
artifact is refused. Because the revocation is inside the signed index, a
client that has fetched the index honours it with no further network access —
including when the registry is unreachable. `--log-entry` revocations are
never offered to clients as a catalog entry in the first place (they name no
artifact at all — see `Revocation.Matches`); they exist only for
`vnproxctl hub verify --sigstore-key-bundle --check-revoked-against` to
consult.

Revocation is a **catalog** action. It stops new installs; it does not reach
into an installation and remove something already installed. Withdrawing
something already deployed is an operator action, and the revocation reason is
what tells them to take it.

## Air-gapped operation (T-4009)

A cluster with no outbound network can still browse and install from this
registry, via `vnproxctl hub mirror` plus a `file://` registry URL —
`internal/hub`'s client and `internal/hubreg`'s Gate run **unmodified** in
both cases; only the transport at the bottom changes (a network `http.Client`
vs. `internal/hub.LocalDoer` reading the mirrored files off disk).

**Mirror, from a machine that does have network access:**

```
vnproxctl hub mirror --registry https://hub.example.com/vnprox \
                     --signers <index signer fingerprint> \
                     --out ./hub-mirror
```

This fetches the signed index and every live (non-revoked) entry's artifact,
byte-for-byte, into `./hub-mirror`, laid out exactly the way `vnproxctl hub
index` lays out a registry root (`index.json` + `artifacts/<type>/<id>/
<version>.json`). It refuses to write anything at all if the fetched index
does not verify against `--signers` — a mirror of an unverifiable catalog is
not a mirror of anything trustworthy, so there is no partial or "trust it
this once" output.

**Carry `./hub-mirror` across the air gap** (removable media, a one-way
transfer diode, whatever the site's policy is), then point the air-gapped
installation at it — a config value, not a different code path:

```toml
# /etc/vnprox/vnprox.toml
[hub]
registry_url  = "file:///var/lib/vnprox/hub-mirror"
index_signers = ["<the same fingerprint given to --signers above>"]
```

or fetch one artifact by hand with no daemon involved at all:

```
vnproxctl hub pull --registry ./hub-mirror --signers <fingerprint> \
                   --type blueprint --id <id> --version <version> \
                   --out artifact.json
```

Both paths run the full verification `hub verify`/the daemon's Gate already
run against a hosted registry — `hubreg.Verify` on the index, then the
artifact fetch checked against that verified index's own
allowlist/revocations — with **zero network access**: `internal/hub.
NewLocalDoer` only ever opens files under the mirror directory, so there is
no `*http.Client`, no DNS resolution, no socket, on this path at all. A
mirror directory that has been tampered with since it was written — a
corrupted copy, a stripped signature, an edited entry — fails verification
exactly as a tampered network response would; nothing is silently accepted
because the network isn't there to double-check it.

**What an air-gapped operator can know about revocations, and what they
cannot.** A revocation published into the mirrored index *before* the mirror
was made is fully honoured, offline, forever — it rides inside the signed
document, and `Gate.doArtifact` refuses a revoked entry's fetch without any
network call (the same property the "Revocation" section above describes for
an online client whose registry has gone unreachable). What an air-gapped
operator **cannot** know is whether a revocation has been published *since*
the mirror was taken: there is no push channel across an air gap, and this
document is not going to pretend one exists. A mirror is a snapshot of the
catalog's trust state at the moment `hub mirror` ran, nothing more current.
The operational consequence is the same one any offline security-advisory
feed has: re-mirror on a cadence proportional to how much a stale
revocation would cost you, and treat "when was this mirror last refreshed"
as an auditable fact (`vnproxctl hub verify --index ./hub-mirror/index.json
--signers <fp>` prints `generatedAt`) rather than an assumption.

**Scope limitation.** Offline consumption only works for entries using the
registry's default self-hosted artifact URL convention — an absolute path
like `/artifacts/blueprint/<id>/<version>.json` (`hubreg.ArtifactPath`'s
default, and what `vnproxctl hub index` always produces). An entry whose
`artifactUrl` is a full `https://...` URL on a *different* host cannot be
resolved by a `file://` client at all (the same origin-pinning check that
refuses a foreign artifact URL online, `hub.ErrForeignArtifact`, has no
concept of "this registry's own host" once the registry is a local
directory) — `hub mirror` still copies its bytes for archival and prints a
warning, but a local `hub pull`/daemon cannot fetch it back. The hosted
`registry.vnprox.com` registry (T-3709) uses the default convention
throughout, so this is not a limitation in practice against it.

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
- **Known limitation, T-3709 (2026-08-24): the public registry's revocation
  channel is not currently operable.** Both Ed25519 identities this document
  describes under "Keys" — the registry's own index key AND the seed
  publisher's key — live only on `pve001`, a host this project has no SSH
  credentials for and no authorisation to modify (`CLAUDE.md`'s "Real PVE
  access" section). Consequence, stated plainly rather than discovered by an
  operator who needs it: **nobody working from this repository can currently
  publish a new entry to `registry.vnprox.com`, rotate its key, or publish a
  revocation to it.** The revocation *mechanism* is not merely unproven in
  production against this registry — it is inoperable there, because
  publishing a revocation means re-signing `index.json` with a key nobody
  reachable holds. This is the strongest available argument for routing
  signing custody through Sigstore/keyless OIDC (pending an owner decision,
  tracked outside this document) — a key nobody can reach to sign *or*
  revoke with is the failure mode keyless signing exists to remove. **This
  limitation is specific to the one public registry `registry.vnprox.com`
  happens to be hosted on today.** It is not a property of the registry
  *format*, and it does not apply to a registry you host yourself — see
  "Publishing: the process" and "Revocation" above, which are the complete,
  self-hosting instructions regardless of who runs the host.
- **Verified end-to-end against a real daemon, T-3709 (2026-08-24).**
  `scripts/hub-registry-harness.sh run` builds the real `vnproxd`/
  `vnproxctl`/`pvemock` binaries, publishes the real seed blueprints above
  through the real `hub publish`/`hub index` pipeline, serves the result
  over local HTTP, and points a real running `vnproxd` at it via `[hub]
  registry_url` — i.e., it *is* the self-hosting path above, exercised by a
  script rather than by hand. It demonstrates, against that real daemon: a
  successful install; a tampered ARTIFACT refused with `invalidSignature`
  (the index left alone); a tampered INDEX refused as a whole — HTTP 502,
  zero entries, a *different* failure than the artifact case — with a clean
  recovery once the pristine index is restored; a by-entry revocation
  honoured (offered=false, install refused) while the artifact's own bundle
  signature is shown still verifying (revocation is a catalog decision, not
  a forgery claim); and a by-signer revocation withdrawing an entry that was
  never named in any revoke command. Transcript:
  `planning/reports/evidence/hub-registry-verification-2026-08-24.txt`.
- **Air-gapped consumption, T-4009 (2026-08-28):** `vnproxctl hub mirror`
  plus `internal/hub`'s `file://` support (`internal/hub.LocalDoer`) exist
  and are exercised end to end — mirror creation, offline consumption
  through the real `internal/hub.Client`+`internal/hubreg.Gate` stack with
  the origin registry's socket closed, signature verification against the
  mirrored index (including a tampered-index case, two ways: a corrupted
  signed byte, and a stripped signature), and a revoked entry honoured
  purely from the mirrored bytes — all in `cmd/vnproxctl/hubcmd_mirror_
  test.go`. Not yet exercised: mirroring the real `registry.vnprox.com`
  itself (unreachable from this environment per the DNS note above; tested
  here against a local registry built through the real `hub publish`/`hub
  index` pipeline instead, same posture as the harness script above).

## Automated vetting (T-3709)

The owner's decision on the "vetted" tier: **automated checks only —
hygiene, not human vouching.** Before this, "vetted" meant only "this
signer's fingerprint is in the operator's `[hub] vetted_signers` allowlist"
— i.e. "an operator listed this key," which is not what a badge reading
"vetted" implies to a reader, and inventing an unwritten bar behind a
trust-sounding word is the trust-laundering failure this section exists to
close off.

An entry is shown "vetted" only when **both** of these are true:

1. The signer is in the operator's own `[hub] vetted_signers` allowlist —
   this opts a signer *into* consideration; it is not sufficient by itself.
2. `internal/hubreg.AutomatedVetChecks` recorded, at `vnproxctl hub index`
   time, that the artifact passed every check it runs:
   - **A capability manifest is present and well-formed.** For a plugin: a
     real identity (id/name/version), the frozen v1 plugin SDK surface
     (`plugin.APIVersion`), a recognized transport, and internally
     consistent extension points/capabilities — checked with
     `plugin.NewScope` + `plugin.ValidateScope`, the exact vocabulary
     `plugin.Registry.Install` enforces at install time, reused rather than
     reimplemented. A blueprint has no capability manifest of its own, so
     this is vacuously true for one.
   - **The artifact declares no privilege the catalog entry didn't also
     declare** — `hub.CapabilityMismatch`, T-2104's own gate, reused rather
     than reimplemented. Vacuously true for a blueprint.
   - **The artifact decodes strictly**, with no unrecognized fields riding
     along (`encoding/json`'s `DisallowUnknownFields`). A signature is
     verified against the *canonicalized* form of whatever a client's
     `json.Unmarshal` accepts, so an extra field smuggled into an otherwise
     validly-signed artifact would decode today with nothing to say so —
     this check refuses to call that artifact vetted.

The verdict is folded into the catalog entry (`AutomatedChecksPassed`)
**before** the index is (re-)signed, so it rides inside the same signature
an operator's daemon already verifies: forging a vetted verdict requires
forging the index signature, not a second, separately-trusted claim.

**What "vetted" explicitly does not mean: a reproducible build.** The
decision named a reproducible-build check; it is not implemented, and the
gap is stated here rather than hidden behind a check that always passes.

> **Narrowed 2026-08-27 (T-3806).** This paragraph used to open by saying
> "vnprox has no source-to-artifact build pipeline." That half is no longer
> true: vnprox's *own* release `.deb` is now byte-reproducible and
> `scripts/verify-reproducible.sh` proves it on demand. It changes nothing
> about *this* gap, which is the sentence below and was always the real
> reason: the registry never receives a submitted plugin's executable, so
> there is nothing for it to rebuild. Keeping the old wording would have let
> a reader conclude the vetting gap had closed along with the build one.

For a plugin specifically,
the registry never receives the executable the manifest's `endpoint` names
at all — only a `{manifest, signature}` artifact
(`cmd/vnproxd/hubinstall.go`'s doc comment: delivering the actual binary to
that endpoint is the registry's own responsibility, out of band) — so there
is nothing here to rebuild and compare. Every `VetResult` (`internal/hubreg/
vetting.go`) carries this residual as a note, pass or fail, so it is never
silently absent from anything that inspects one.

The badge in the web UI reads "vetted" with a hover explanation naming
exactly these checks and this residual — never wording that could be read
as a person's review or endorsement of the artifact.
