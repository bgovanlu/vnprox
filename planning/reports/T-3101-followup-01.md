# T-3101-followup-01 · `sdn.apply` commits SDN changes that never entered the change engine

**kind:** safety gap in a documented guarantee · **found by:** Phase 31 card authoring, while
answering T-3101's `--lock-token` question against real PVE 9.2.4 · **status:** open, filed not
fixed — the fix requires a product decision

## The claim this is measured against

`CLAUDE.md`, verbatim, as the product's stated core:

> **Never apply network changes outside the change engine** (`internal/change/`). All mutations
> flow: stage → validate → diff → apply → confirm/rollback. This is the product's core safety
> guarantee.

This finding is the **inverse** of the rule as usually read. Nothing here bypasses the change
engine to write config. What happens is that a mutation which never entered the change engine
gets applied **by** it, carried along by an unrelated changeset an operator approved.

## What the code does

Three facts, each independently checkable:

1. **`PUT /cluster/sdn` applies everything pending, cluster-wide.** vnprox's own doc comment says
   so — `internal/pve/sdn.go:164`: *"apply all pending SDN changes cluster-wide via an async
   task"*. This is correct about PVE and is not a bug in the comment.
2. **vnprox never takes PVE's SDN configuration lock.** `internal/pve/sdn.go:171` issues the PUT
   with empty `requestParams{}`. Real PVE 9.2.4 offers `--lock-token` ("the token for unlocking
   the global SDN configuration") and `--release-lock` on that exact call — captured in
   `planning/reports/evidence/pve-9.2.4-sdn-schema.txt`. The parameter is optional, so this is
   not an API misuse; it is a guarantee not taken.
3. **No validation anywhere looks at SDN pending state.** `grep -n Pending
   internal/change/validate*.go` returns nothing. vnprox *reads* pending state (it is on
   `sdn.Zone` as `Pending` and `Diff`, and the SDN cockpit renders it), but no validator raises a
   finding, and nothing blocks.

## The failure this permits

An operator stages an SDN change in the Proxmox GUI and does not apply it. Separately, they stage
a one-line VNet change in vnprox, review vnprox's diff, and approve it. vnprox's planner appends
`sdn.apply` — and PVE commits **both**. The operator was shown one change and applied two.

The second change was never validated by vnprox, never appeared in its diff, is not in its audit
trail as an applied mutation, and — because vnprox's snapshot/rollback reasoning is built around
the ops it staged — is not covered by the rollback the operator believes they have.

Nothing about this requires a race or an adversary. It needs one unapplied pending change, which
is a normal state for the PVE SDN GUI to leave a cluster in.

## Why this is filed rather than fixed

There are at least three defensible fixes and they are not equivalent:

- **Block.** Refuse to apply when foreign pending SDN state exists, with the list. Safest,
  matches how `internal/fw` already blocks deleting a referenced object — but it makes vnprox
  refuse to work on a cluster whose GUI has anything pending, which may be common.
- **Surface and confirm.** Show foreign pending changes in the review screen as "this apply will
  also commit …" and require explicit acknowledgement. Preserves the honest-diff property without
  refusing.
- **Take the lock.** Hold `--lock-token` across stage→apply so foreign edits cannot interleave.
  Narrows the window but does not address pending state that already existed when staging began,
  so it is a complement to one of the above, not a substitute.

Choosing among these is a product decision about how vnprox behaves on a cluster it does not
exclusively own. `CLAUDE.md` says to flag such a decision rather than take it unilaterally.

## What T-3101 must not do

T-3101 adds SDN Fabrics, which are applied through this same `PUT /cluster/sdn` path. **It must
not widen this gap** — every fabric op it adds inherits the behaviour above. If T-3101 lands
before this is decided, its report says so explicitly rather than leaving the new surface to
inherit the problem silently.

## Not yet done

**This finding has no test pinning it.** Phase 30's practice — pin today's behaviour with a test
asserting what it *is*, not that it is right — should apply here, and does not yet: demonstrating
it needs `internal/pvemock` to model SDN pending state and a foreign edit, which it does not
today. That mock work is a prerequisite for the fix in any of its three forms, and belongs with
whichever card takes it.
