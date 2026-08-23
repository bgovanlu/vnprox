# T-3709 · Stand up the hosted blueprint and plugin registry

**Origin:** T-3707's decision, `planning/hosted-services-decision.md` (Group A, answered YES
2026-08-23). · **size:** L · **depends:** — · **unblocks:** T-1702, T-1705, T-2104, T-2803, T-2904,
T-3303

## What this card is not

It is **not** an implementation card for the registry client. That is already built, documented,
tested and hardened — `internal/hub/`, `internal/plugin/registry`, `internal/blueprint/`,
`cmd/vnproxd/hubinstall.go`, and the `trustUnsigned` gate in `internal/api/hub.go`. Seven features
sit inert not because code is missing but because the service they point at does not exist.

So this card is **infrastructure and operations**. Read that as a warning about where the effort
actually is: the parts that are hard here are the parts that are not Go.

## The obligations, which are the real deliverable

The decision page records these as what "yes" commits to. They are not follow-ups.

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
