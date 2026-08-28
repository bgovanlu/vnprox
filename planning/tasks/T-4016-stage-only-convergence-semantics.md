# T-4016 · Stage-only integrations report "converged" while nothing is live

**Found by:** T-4001's Terraform provider work, 2026-08-28 · **size:** M ·
**depends:** T-4001 (landed) · **blocks-decision-for:** T-4002 (Ansible), T-4003 (runbooks) ·
**affects:** every future stage-only integration

## The observation

`terraform apply` against the vnprox provider stages a changeset and stops — correct, and required
by D4 (the change engine is the sole mutation path). But Terraform's whole contract is *"plan shows
zero diff once reality matches config."* After a stage-only apply:

- `terraform plan` reports **no changes**,
- the state file records the resource as **created**,
- and **nothing has been applied to the network.** A human still has to review and apply the
  changeset inside vnprox.

An operator reading Terraform's output — or worse, a CI pipeline gating on `terraform plan
-detailed-exitcode` — will conclude the infrastructure is converged when it is not. That is the
same failure family as an empty panel meaning both "disabled" and "nothing found" (T-3906), except
here the misleading signal is a *success*.

T-4001 documented this prominently in the provider README rather than pretending it away, which is
the right interim answer. It is not a durable one, because the misleading signal is emitted by
Terraform itself, not by anything vnprox controls.

## Why this needs deciding once, not three times

T-4002 (Ansible) hits it as `changed: true/false` idempotency — a play that reports `ok` when the
change is merely staged. T-4003 (runbooks) hits it as a "prepared" state that looks like "done."
Each will invent its own answer unless one is chosen here. **The provider is the contract-setter;
this is the contract.**

## Options, none obviously right

1. **Permanent diff until applied.** The resource reports drift until a human applies the
   changeset, so `plan` is honest. Cost: `terraform apply` never reaches a clean state on its own,
   which breaks CI pipelines that gate on exit code — and may drive users to `-auto-approve` loops
   that stage duplicate changesets.
2. **A status attribute plus a documented gate.** Resource exposes `changeset_status`
   (`draft`/`validated`/`applied`), and the documented pattern is a `check` block or a data source
   that fails until it reads `applied`. Honest, opt-in, and puts the burden on the pipeline author
   to adopt it — many will not.
3. **Accept and document loudly** (today's state). Cheapest, and defensible for a tool whose whole
   premise is that a human reviews network changes. Weakest against the CI-pipeline case, which is
   exactly the audience an IaC provider attracts.

Option 2 is the strongest candidate on current evidence, but this is a **product decision about
what an integration is allowed to imply**, and it should be made deliberately.

## Deliverables

- A decision, recorded as an ADR under `docs/adr/` (the D1–D11 set now lives there), naming which
  option and why.
- Whichever option is chosen, implemented consistently across T-4001, and specified in T-4002's and
  T-4003's cards *before* they are dispatched.
- A test that the chosen semantics hold — e.g. for option 2, that a resource whose changeset is
  still `draft` reports a status that the documented gate pattern actually catches.

## Acceptance criteria

1. An ADR exists stating what a stage-only integration may and may not imply about liveness.
2. The Terraform provider implements it, with a test.
3. T-4002 and T-4003's cards reference the ADR rather than re-deriving an answer.
