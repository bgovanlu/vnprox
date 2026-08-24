# T-3710 · Stand up opt-in compatibility telemetry

**Origin:** T-3707's decision, `planning/hosted-services-decision.md` (Group C, answered YES
2026-08-23). · **size:** M · **depends:** — · **unblocks:** T-2503

## Why this is worth doing, stated precisely

T-2503's own rationale is *"one cluster validated by us is an anecdote."* That was already true. It
became pointed on 2026-08-23, when the project discovered it had been wrong about its own test
environment for five days — see `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt`. Every
compatibility claim vnprox makes currently rests on **two nodes running the same PVE version**,
observed by the people who wrote the code.

Telemetry is the mechanism that replaces that with evidence. It is also the cheapest of T-3707's
three groups: an endpoint that accepts a small opt-in payload.

The client side is already shipped, documented and tested. This card is the endpoint and the policy.

## Hosting — decided 2026-08-24

The owner chose **GitHub-native where possible** for the hosted-service group. That works for
T-3709's registry, which is a static signed index. **It does not work here.** A static host cannot
accept a submission, so this card needs the one genuinely dynamic piece of infrastructure in the
whole group — and it should be scoped and named as such rather than assumed to come free with the
registry decision.

Keep it as small as the job allows: an endpoint that accepts a small opt-in payload and stores it.
The privacy and retention obligations below are what make it non-trivial, not the request handling.

## Deliverables

- **The endpoint.** Accepts the payload `internal/` already produces — read what the client actually
  sends before designing the receiver, and do not let the two drift. It is the only server-side
  component either hosted-service card requires; size it accordingly.
- **Consent that is real.** Opt-in means off until someone turns it on, with what is collected
  visible at the moment of the choice, not only in documentation. Verify the daemon sends nothing at
  all before consent — a test, not an assurance.
- **A retention policy**, written down, with an actual deletion mechanism behind it. A retention
  policy nobody implements is a privacy statement that is false.
- **A privacy statement** covering what is collected, what is not, how long it is kept, and how to
  revoke consent and have prior submissions deleted.
- **A way to read what arrives.** This is a deliverable, not a nicety: the decision page records
  that *telemetry nobody reads is worse than none* — it carries the privacy cost with none of the
  benefit. Something that answers "which PVE versions are vnprox installations actually running
  against" without a human writing a query each time.

## Acceptance criteria

1. With consent withheld, the daemon transmits nothing. Demonstrated by observation, not by reading
   the code path.
2. With consent given, a submission arrives and is visible in whatever reads the data.
3. Revoking consent stops transmission **and** deletes prior submissions on request.
4. Retention actually expires data. Demonstrate it, with a shortened window if necessary.
5. The privacy statement matches what the code does. Check it against the payload, field by field —
   this is the claim most likely to quietly become false as the payload grows.

## Scope discipline

Collect what answers the compatibility question — PVE version, and the configuration shape vnprox
had to cope with. Nothing that identifies a network, a host or a person. The value of this feature
is entirely in people being willing to switch it on, and that is spent the first time the payload
contains something a user did not expect.

## Note for whoever picks this up

`docs/audit-matrix-2026-08-23.md` currently lists T-2503 as `Shipped, inert`. When this lands it
becomes `Live`, and its evidence should be a real submission from a real node — which, once this
exists, `vnprox-dev` can provide.
