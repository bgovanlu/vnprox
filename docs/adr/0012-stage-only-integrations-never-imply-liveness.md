# ADR-0012: A stage-only integration reports intent recorded, never network converged

**D-number:** none — see "Why this ADR is different" below
**Status:** Accepted
**Decided by:** T-4016, 2026-08-30 · **Binding on:** T-4001 (Terraform), T-4002 (Ansible), T-4003
(runbooks), and every future integration that stages rather than applies

## Context

D4 (ADR-0004) makes the change engine the sole mutation path: an integration may stage a changeset
and must stop. `terraform apply` against the vnprox provider does exactly that, and it is correct.

The problem is what Terraform then says. Its whole contract is *"plan shows zero diff once reality
matches config"*, so after a stage-only apply:

- `terraform plan` reports **no changes**,
- the state file records the resource as **created**,
- and **nothing has been applied to the network**.

An operator reading that output — or a CI pipeline gating on `terraform plan -detailed-exitcode` —
concludes the infrastructure is converged when it is not. The misleading signal is emitted by
Terraform itself, not by anything vnprox controls, so documentation alone cannot retract it.

This is the same failure family as an empty panel meaning both "disabled" and "nothing found"
(T-3906), with one difference that makes it worse: **here the misleading signal is a success.** A
confusing failure gets investigated. A confident success does not.

Each integration would otherwise invent its own answer — Terraform as a diff, Ansible as
`changed: true/false`, runbooks as a "prepared" state that reads as "done". The provider is the
contract-setter, so the contract is set once, here.

## Decision

**A stage-only integration may report that it recorded an intent. It may never report, or allow its
host tool to imply, that the network matches the configuration.**

Concretely, every stage-only integration MUST:

1. **Expose the changeset's identity and live status** as a first-class, refreshed value —
   `changeset_status` on a Terraform resource, `result.changeset.status` on an Ansible module —
   with the documented values `draft` / `validated` / `applied`. "Refreshed" is load-bearing: the
   status MUST be re-read from the daemon on every read/plan, not frozen at creation, or a gate
   built on it reads a value that stopped being true.
2. **State in its own resource/module documentation** that a successful run means *staged*, not
   *live*.
3. **Publish a gate pattern** its users can adopt to make a pipeline honest, and keep that pattern
   tested rather than merely written down.

And MUST NOT:

4. **Synthesise a permanent diff** to force the host tool to report drift (option 1, rejected
   below).
5. **Block, poll or wait** for a human to apply. Staging is the boundary; an integration that waits
   for review is a second apply path wearing a disguise, and D4 exists to prevent exactly that.

### The gate is opt-in, and that is the deliberate cost

A pipeline author who adopts nothing gets today's behaviour: a green `terraform apply` over a
network that has not changed. That is a real, accepted cost of this decision, taken because the
alternative is worse (below) — and it is why requirement 3 makes the pattern a *published, tested*
artefact rather than a paragraph.

## Options considered

**1 — Permanent diff until applied.** The resource reports drift until a human applies, so `plan`
is honest by construction. **Rejected**, and the decisive evidence came from building the second
integration rather than from reasoning: **Ansible has no state file.** There is no persisted
"last staged changeset id" for a later run to check the status of; every run has only live
inventory to compare against. A permanent-diff model has nowhere to store the thing it would diff
against, so choosing it would make the two integrations behave *differently* — which is worse than
either behaviour alone, because the inconsistency itself becomes something operators must learn.
It also drives users toward `-auto-approve` loops that stage duplicate changesets.

**2 — Status attribute plus a documented, tested gate.** **Chosen.** It is honest, it is uniform
across integrations that have nothing else in common, and it already half-existed: the Terraform
provider exposes `changeset_status` and refreshes it in `Read`; the Ansible modules already return
`result.changeset.status`. Making this the contract is a consistency and documentation job, not new
machinery — which is itself evidence it is the shape that fits.

**3 — Accept and document loudly** (the state before this ADR). Cheapest, and genuinely defensible
for a tool whose premise is that a human reviews network changes. **Rejected** because it is
weakest against precisely the audience an IaC provider attracts: a CI pipeline does not read
READMEs, and `-detailed-exitcode` is the interface it actually consumes.

## Consequences

**What this enables.** A pipeline can be made correct without a second apply path: gate on
`changeset_status == "applied"` and the pipeline is as honest as the network. Two integrations with
no shared code report liveness the same way, and T-4003's runbooks inherit the answer instead of
inventing a third.

**What it costs.** The default remains a green apply over an unconverged network. This ADR does not
fix that — it makes fixing it possible, uniform, and testable, and it says plainly that the burden
sits with the pipeline author.

**What it forbids.** Any future integration that "helpfully" waits for a human, retries until
applied, or reports success on the strength of a staged changeset is in breach of this ADR and of
D4. If a genuinely unattended path is ever wanted, it needs its own decision about who is
accountable for an unreviewed network change — not a quiet relaxation here.

## Why this ADR is different

This directory's README describes itself as **"a publication step, not a decision-making step"** —
ADRs 0001–0011 extract decisions that were already locked in `docs/architecture.md` §10 as D1–D11.

**ADR-0012 is the first that makes a decision rather than publishing one.** It has no D-number
because it is not one of the eleven; it is an integration-contract decision that T-4016 was opened
to take. The README's preamble is amended alongside this file rather than left to quietly become
false — which is the same failure mode this ADR is about, applied to a document.
