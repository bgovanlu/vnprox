# T-3707 · The hosted-service decision

**Status: awaiting the owner. Nothing here is decided.**

Eleven shipped features are inert because they need a service that nobody has stood up. That is a
product decision, and it has been deferred by never being asked rather than by being answered. This
page exists so it can be answered once, in writing, and stop being re-discovered by every audit.

**Either outcome is fine.** "No, and we're not going to" is a perfectly good answer that costs
nothing and removes eleven rows from the gap column. The only bad outcome is the current one, where
eleven features sit in permanent limbo and each audit re-reports them as a deficiency.

## What "inert" means here

These features are **implemented, documented and tested** — all eleven are `Shipped / Documented /
Covered` in `docs/audit-matrix-2026-08-23.md`. They are switched off by configuration because the
thing they talk to does not exist. This is not a code gap. No amount of engineering closes it.

## The three groups

### Group A — Registry / hub (7 cards)

| Card | Feature |
|---|---|
| T-1702 | Plugin SDK — versioned extension points for third-party switch drivers, flow ingestors, finding packs |
| T-1705 | Blueprint & plugin hub — opt-in registry client with signature verification and a vetted tier |
| T-2104 | Hosted blueprint and plugin registry |
| T-2803 | Hosted signed registry for blueprints and plugins |
| T-2904 | Hub plugin install hardening |
| T-3303 | Hosted instances + ecosystem: demo, registry, Terraform/Ansible |
| — | plus the local hub client already shipped in `internal/hub/`, `internal/plugin/registry` |

**What saying yes commits to:** a hosted registry is not a static file. It implies a signing key and
somewhere safe to keep it, a vetting process for what earns the "vetted" tier, a revocation story
for a plugin that turns out to be malicious, and an availability expectation from every installation
that points at it. The client-side hardening is already written; the obligation is operational and
ongoing.

**What saying no gets you:** the local hub keeps working — signature and capability gates already
ship and are exercised. Users can host their own registry. The seven cards become "shipped,
deliberately unhosted" and stop counting as a gap.

### Group B — Hosted demo (3 cards)

| Card | Feature |
|---|---|
| T-2801 | One-command install and built-in demo mode |
| T-2802 | Hosted read-only demo and guided tour |
| T-3303 | (also in Group A) |

**Note this group is already half-answered.** Demo mode is a separate `--demo` daemon and is
*correctly* off on a production node — the audit classified it inert, but that is the intended
state, not a defect. The open question is only the **hosted, public** instance: a URL a stranger can
click without owning a Proxmox cluster.

**What saying yes commits to:** a public internet-facing deployment of vnprox, with the exposure
that implies, plus resetting it when visitors mutate the dataset. The demo dataset itself already
exists.

**What saying no gets you:** `--demo` remains available to anyone who installs the package. The
evaluation story becomes "install it" rather than "click this". T-2802 is marked unhosted.

### Group C — Compatibility telemetry (1 card)

| Card | Feature |
|---|---|
| T-2503 | Opt-in compatibility telemetry |

The card's own rationale: *"One cluster validated by us is an anecdote."* It collects, with consent,
which PVE versions and configurations vnprox is actually running against.

**This one has changed in value since it was written.** The project has just discovered it was wrong
about its own test environment for five days — see
`planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt`. Telemetry is the mechanism that turns
"we think it works on 9.2.4" into evidence. It is also the group with the lowest operational burden:
an endpoint that accepts a small opt-in payload.

**What saying yes commits to:** an endpoint, a retention policy, a privacy statement, and the
discipline to actually look at what arrives. Telemetry nobody reads is worse than none.

**What saying no gets you:** compatibility claims stay anecdotal — which, with two nodes of the same
version, they currently are.

## The decision

Fill in one line per group. A reason is worth more than the verdict, because the reason is what
stops it being reopened.

| Group | Going to exist? | Decided by | Date | Reason |
|---|---|---|---|---|
| A — registry / hub | *unanswered* | | | |
| B — hosted demo | *unanswered* | | | |
| C — telemetry | *unanswered* | | | |

## What happens next, either way

- **Yes** → the group earns an implementation card in a future phase, scoped to standing the service
  up. The client-side work is done; the card is infrastructure and operations.
- **No** → the affected rows in `docs/audit-matrix-2026-08-23.md` change from `Shipped, inert` to
  **`Shipped, deliberately unhosted`**, and the matrix's gap count drops by that many. Add a line to
  this page so the next audit finds the answer instead of re-raising the question.

Until this table is filled in, T-3707 is the one card in Phase 37 that no agent can close.
