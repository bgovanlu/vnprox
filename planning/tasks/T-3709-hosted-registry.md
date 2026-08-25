# T-3709 · Stand up the hosted blueprint and plugin registry

**Origin:** T-3707's decision, `planning/hosted-services-decision.md` (Group A, answered YES
2026-08-23). · **size:** L · **depends:** — · **unblocks:** T-1702, T-1705, T-2104, T-2803, T-2904,
T-3303

## What this card is not

It is **not** an implementation card for the registry client. That is already built, documented,
tested and hardened — `internal/hub/`, `internal/plugin/registry`, `internal/blueprint/`,
`cmd/vnproxd/hubinstall.go`, and the `trustUnsigned` gate in `internal/api/hub.go`.

So this card is **infrastructure and operations**. Read that as a warning about where the effort
actually is: the parts that are hard here are the parts that are not Go.

## CORRECTION 2026-08-24 — the premise above was wrong, and wrong in a useful way

This card was written saying seven features sit inert "because the service they point at does not
exist." **The service exists.** Observed, read-only, from pvecube — transcript in
`planning/reports/evidence/registry-vnprox-com-2026-08-24.txt`:

```
Host: registry.vnprox.com  /index.json                      200 2584b   4 entries, 0 revocations
Host: registry.vnprox.com  /artifacts/blueprint/…/1.0.0.json 200 2330b
Host: apt.vnprox.com       /dists/stable/Release            200  573b
index signature   ed25519  fp 1e75a5f0565a870596c4b6121c8c59ca05deea672d2264b93357378b9446e3ed
entry signatures  ed25519  fp 791f441167f31ae8b0b5dfb37fffd0d2fec24e5c7cff03ff22e42d3a4ddf092a
```

It is a real, correctly signed, static registry with four seed blueprints and their artifacts. It
was stood up by T-3303 on 2026-08-18 and this card, written five days later, did not know. That is
the *same failure mode* as the "no cluster" premise: a claim about our own infrastructure, restated
from a secondary source, going stale without anyone re-checking it. Third instance now.

**So the gap is not the service. The gap is these three things, and they are what this card is:**

1. **The name resolves nowhere.** `registry.vnprox.com`, `apt.vnprox.com` and `demo.vnprox.com` are
   NXDOMAIN from the dev box *and* from pvecube. The registry is reachable only by sending an
   explicit `Host:` header to `192.168.1.7`. No vnprox installation anywhere can reach it by name,
   which is why the seven features are inert — not because nothing is serving. Already tracked as
   `debt-sweep-2026-08-19.md` item 7 (the VPS reverse-proxy leg); it is a prerequisite of this card,
   not a footnote to it.

2. **Both signing keys live only on `pve001` — a host we have no credentials for and no
   authorisation to modify.** T-3303's own commit message says so: *"The registry's Ed25519
   index-signing key lives only on that host."* Consequences, which nobody has written down until
   now:
   - We cannot publish a new entry.
   - **We cannot publish a revocation.** The index shows `revocations: 0` and there is no way for us
     to make it show one. The shipped revocation mechanism is not merely unproven in production —
     it is *inoperable*, and this card's AC3 cannot be met against the current registry by anyone
     without pve001 access.
   - We cannot rotate the key.

   This is the strongest available argument for the owner's Sigstore decision, and it was made
   without it: keyless signing moves publication off a host we cannot reach. The decision is
   well-aimed. It is also now **load-bearing**, not a preference.

3. **The deployed daemon does not point at it.** `registry_url` is commented out in
   `/etc/vnprox/vnprox.toml` on pvecube. Even with DNS, nothing would happen until that is set.

Two documentation claims should be re-read against the above before this card closes:
`docs/install.md:4` calls `apt.vnprox.com` "a real, live, signed apt repository, hosted and serving",
and `docs/security.md:508` says "there is no registry *service*". The first is true only for someone
who can already resolve the name; the second is a description of the architecture (static files) that
reads as a description of the deployment (nothing there). Neither is a lie; both mislead.

## The obligations — ANSWERED 2026-08-24

These were the real deliverable, and the owner has now decided all four. What remains is
implementing them faithfully, not choosing them.

| Obligation | Decision |
|---|---|
| Signing key custody | **Sigstore / keyless OIDC** |
| "Vetted" tier | **Automated checks only** (hygiene, not human vouching) |
| Revocation | A published deny-list keyed on the transparency-log entry |
| Availability | GitHub-served static index; degrade cleanly when unreachable |
| Hosting | **GitHub-native** — signed static index, no server of ours |

Three consequences that are easy to get wrong, so they are called out:

1. **The vetted badge must not read as if a person vouched.** Automated checks certify a capability
   manifest, absence of undeclared privileges, and a reproducible build. That is hygiene. Wording it
   as endorsement is the trust-laundering failure this card exists to avoid — **the badge copy is a
   deliverable of this card**, and it should be reviewed against what the checks actually prove.
2. **Sigstore is a dependency, not an absence of one.** Trading a private key for public
   transparency-log infrastructure is a good trade here, but it is a third party in the trust path
   and operators are entitled to know that. Say so in the operator docs.
3. **Revocation changes shape under keyless signing.** There is no key to rotate. The client has to
   learn that a signature it can still cryptographically verify is no longer trusted — which is a
   deny-list lookup, and is the half of this card most likely to be skipped.

The original framing of these obligations follows, retained because the reasoning still applies.

1. **A signing key, and somewhere safe to keep it.** The client verifies signatures; something has
   to produce them. Decide custody before anything is published, because rotating a key that
   installations already trust is materially harder than choosing well once. Write down where the
   key lives, who can use it, and how it is rotated.
2. **A vetting bar for the "vetted" tier.** The client already renders the distinction. An
   unwritten bar means the tier means nothing, and a tier that means nothing is worse than no tier —
   it launders trust.
3. **Revocation.** What happens when a published plugin turns out to be malicious or simply broken.
   The client's install path must be able to learn that something it already trusts is no longer
   trusted. Check what `internal/plugin/registry` already supports before designing anything.
4. **An availability expectation.** Every installation pointing at this registry now depends on it.
   Decide, and state, whether it is best-effort or something stronger, and make sure the client
   degrades to "cannot reach the registry" rather than to a failure that looks like a bad signature.

## Deliverables

- The service itself, and the decision record for points 1–4 above, in `planning/` or `docs/` —
  whichever the repo's existing convention puts operational policy in.
- End-to-end verification against a real installation: publish a signed bundle, install it from a
  vnprox node, verify the signature path, then **revoke it and verify the revocation is honoured.**
  The revocation half is the one that gets skipped; it is the half that matters.
- `docs/` gains the operator-facing side: how to point an installation at the registry, and how to
  point it at a self-hosted one instead — the self-hosting path must keep working, it is what the
  "no" answer would have left behind and it should not regress.

## Acceptance criteria

1. The seven cards above move from `Shipped, inert` to `Live` in `docs/audit-matrix-2026-08-23.md`,
   each with evidence rather than assertion.
2. Signature verification is demonstrated **failing** on a tampered bundle, not only succeeding on a
   good one. A verifier only ever exercised against valid input is untested.
3. Revocation is demonstrated end to end.
4. Key custody, the vetting bar, revocation and the availability expectation are all written down.
   An undocumented answer to any of the four means this card is not done.

## Note for whoever picks this up

Apply the lesson from `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt` and CLAUDE.md's
mock rule: verify the client against the **real** service, not against a fixture written from the
same understanding that produced the client. A registry mock and the client that talks to it,
both derived from one reading of the design, will agree with each other forever.
