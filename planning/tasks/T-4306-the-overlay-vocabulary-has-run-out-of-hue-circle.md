# T-4306 — The overlay vocabulary has run out of hue circle, and one collision was added tonight

**Phase:** 43 (Canvas rendering)
**Status:** partly done — the `SIM_STROKE` consolidation landed. `blast-radius` and `flow` remain.
**Found by:** closing out T-4302's "what this card did not fix" note, 2026-08-30 · **size:** M
**Depends on:** T-4211 (landed), T-4302 (landed)
**Related:** T-4302 proved the same arithmetic for entity *kind* and moved it to shape

## What the comments claim, and what is true

`SIM_STROKE`, `FLOW_EDGE_COLOR`, `STP_BLOCKING_STROKE` and `BLAST_RADIUS_COLOR` each carry a
comment asserting the value is *"distinct from every status/SIM_STROKE/FLOW_EDGE_COLOR/diff/recency
colour already in use"*. Nothing measured that. Measured now:

**Sixteen pairs sit under the 40deg OKLCH floor the palette holds everywhere else.**

This is T-4301's finding relocated. That card measured a palette kept in sync "by hand" and found
three of twelve values correct on the day they were written. **A comment promising a property is
not a mechanism for having it** — and here the comments are not merely unverified, they are false.

`src/topology/overlayHues.test.ts` now measures it. It deliberately does **not** assert the floor,
because most of these collisions are the design working:

| pair | why it is under the floor |
|---|---|
| `sim/allow` ↔ `status/ok` | deliberate — `SIM_STROKE` maps a verdict onto severity |
| `sim/deny` ↔ `status/critical` | deliberate — a `deny` **should** look like an error |
| `sim/unreachable` ↔ `status/degraded` | deliberate (0.2deg) |
| `stp-blocking` ↔ critical / degraded / deny | deliberate — T-3901 chose a burnt orange near but not equal to "down" |
| `accent(base)` ↔ `status/info` | deliberate — T-4204 made `info` **be** the accent |
| `flow` / `flow-selected` ↔ each other | deliberate — one ramp, two shades |
| **`flow` / `flow-selected` ↔ `accent(base)` / `status/info`** | **unexamined** — a flow edge reads as "selected" or "informational" |
| **`accent(demo)` ↔ `blast-radius`** | **introduced tonight, 2.9deg** |

So the file is a **census**, not a floor: it pins the exact set, which makes the deliberate ones
explicit and makes a seventeenth fail.

## The one I introduced

T-4211 moved the demo accent to hue 320 to get it off `degraded`. `BLAST_RADIUS_COLOR` is
fuchsia-600 at hue **322.9**. In demo mode a blast-radius ring and any selected control are now
2.9deg apart — the same class of defect T-4211 exists to remove, created by the commit that removed
it.

**T-4211's own gate passed**, because it compares the accent against `--color-status-*` and these
literals are not status tokens. That is this phase's recurring shape once more: *a guard that
measures against one set is blind to a call site outside it.* The census test would have caught it
in the same commit, and now will.

It was not reverted, because there is nowhere better to go — see below.

## The circle is full, and this is the arithmetic

Thirteen distinct hues are already spent on the map:

```
critical 21.9 · sim/deny 25.3 · stp-blocking 38.4 · sim/unreachable 70.1 · degraded 70.3
ok 145.1 · sim/allow 162.5 · flow 215.2 · flow-selected 223.1 · info 224.3
accent(base) 224.5 · sim/indeterminate 292.7 · blast-radius 322.9
```

**The best separation any fourteenth hue can achieve against all of them is 37.1deg** (at hue 108),
against a 40deg floor. There is no hue left. Asserted directly in the test rather than left as a
claim.

T-4302 proved eleven hues at 40deg need 440deg of a 360deg circle and responded by moving entity
*kind* to the shape channel. This is the same result at product scale, and it means the demo accent
could not have been placed to clear everything: T-4211's 320 is as good as available, and the
collision it created is a symptom of exhaustion rather than a bad choice within it.

## What to do

- **`blast-radius` needs to come off hue — but NOT the way this card first proposed.** Checked
  against the code before implementing, and the proposal does not survive it:

  1. **The dash channel is already taken.** `drawBlastRadiusOverlay` encodes the *role* in dash
     (`path` gets `[3, 2]`, `target`/`affected` solid), in width (1.5 vs 2.5) **and** in a glyph
     (`X` / `!` / `*`). "Status-critical plus a distinct dash" would collide with the overlay's own
     role encoding.
  2. **It would state something false.** An entity in a blast radius is *at risk*, not failed.
     Painting it `--color-status-critical` says it is failed — the exact objection T-4303 raised
     against giving `recency` the critical token, and the reason that card exempted it.

  So the fix is genuinely architectural and is not "pick a different token". What blast radius has
  that nothing else does is the **scrim**: it dims every non-focused node while active. That is
  already a non-hue channel doing the heavy lifting, and it is the thread to pull — the ring may
  not need a distinctive hue at all once everything around it is dimmed.

- **Accept that no re-hue can fix this.** The measurement is unambiguous: 13 hues spent, best
  fourteenth 37.1deg. Any card that responds to this one by moving a colour is re-solving an
  unsatisfiable constraint. Something has to *leave* the hue channel.

- **The cheapest real win is `SIM_STROKE`, and it removes three pairs by design.** Its four values
  already sit 0.2-17deg from `ok`/`degraded`/`critical` — because that mapping is deliberate. So
  they are a **fourth copy of the status scale**, the same defect T-4302 deleted three copies of,
  and they should resolve `--color-status-*` rather than restate it. Only `indeterminate` (violet,
  292.7) has no status equivalent and keeps a private hue. Doing this frees three hues, removes
  three census pairs by a design change rather than by editing the expected list, and is
  independent of the blast-radius question.
- **Decide what `flow` means.** A flow edge 9deg from `info` and from the accent is the one
  genuinely *unexamined* collision left. Either it is deliberately informational — in which case
  say so and let it resolve `--color-status-info` — or it needs its own channel.
  To be unambiguous, because the two readings of "fix `SIM_STROKE`" are opposites: do **not** move
  those values *away* from the status hues — that would undo a deliberate design on the strength of
  a number. Move them *onto* the status tokens exactly, which is the same intent the near-miss hues
  were expressing approximately.

- **`STP_BLOCKING_STROKE` stays a literal.** T-3901 chose a burnt orange deliberately *near but not
  equal to* "down"/"deny" so a port STP has cut never reads as either. That is a value with no
  token behind it and it should keep one — its four census pairs are the design, not drift.
- Rewrite the four comments. They currently assert a property the code does not have.

## Acceptance criteria

1. `overlayHues.test.ts`'s census shrinks, and every pair removed from it is removed by a *design*
   change, not by editing the expected list.
2. `blast-radius` and `accent(demo)` are no longer under the floor.
3. The four "distinct from every colour" comments say something true, or are deleted.
4. `docs/design-language.md` §8.4's channel-assignment table gains a row for what an overlay does
   when the circle has nothing left — that is now a documented, measured fact rather than a thing
   each card rediscovers.

---

## Done: `SIM_STROKE` consolidated (5 census pairs removed by design)

**There were four copies, not two.** `canvasDraw.ts`'s `SIM_STROKE` and `EntityEdge.tsx`'s
`SIM_STROKE` as hex, and `EntityNode.tsx`'s `SIM_RING_CLASS` **and** `SIM_MARKER_CLASS` as Tailwind
utilities.

All four agreed — and that is the part worth recording. They agreed because four authors reached
for the same Tailwind-500 swatch, not because anything kept them in step. `STATUS_STROKE` had three
copies that did **not** agree (T-4302 measured them disagreeing about `ok` and `unknown`), and
nothing about this table made it less likely to drift, only luckier so far.

The mapping is `src/topology/simVerdict.ts` now, returning a token *name* that each renderer
resolves the way it can — DOM through `var()`, canvas through `canvasPalette`, Tailwind call sites
by picking between fully-written class literals. Same division of labour `trafficMode.ts`'s
`toneVar` established at T-4303.

| verdict | was | is | separation it was restating |
|---|---|---|---|
| `allow` | emerald-500 | `--color-status-ok` | 17.4deg |
| `deny` | red-500 | `--color-status-critical` | 3.5deg |
| `unreachable` | amber-500 | `--color-status-degraded` | **0.2deg** |
| `indeterminate` | violet-500 | violet-500, in **one** place | — |

Five census pairs are gone, and gone the way AC1 requires — because the colours no longer exist,
not because the expected list was edited.

### Two things this turned up

**`statusOk` had to be a new role, not a reuse.** The canvas already had `nodeBorderOk`, and it is
`--color-outline` — neutral, because a healthy node is the *absence* of a signal, which is
`StatusDot`'s documented convention. An `allow` verdict is the opposite case: it is an answer to a
question the operator asked, so it says yes in green. Two roles, two tokens, and the palette test
records why the twentieth lookup exists.

**The Tailwind tables kept their class spelling on purpose.** `SIM_RING_CLASS` and
`SIM_MARKER_CLASS` now key on the tone but still write every class out in full, because Tailwind v4
resolves utilities by scanning source text and an interpolated `ring-${tone}` is never emitted.
Not hypothetical — `NoticeStack` shipped exactly that bug during T-4303 and carries a test
forbidding it.

## Still open

- **`blast-radius` ↔ `accent(demo)`** (2.9deg, introduced by T-4211). The analysis above stands:
  the dash channel is taken by the overlay's own role encoding, `--color-status-critical` would
  state something false, and no re-hue can clear the floor. The scrim is the thread to pull.
- **`flow` / `flow-selected` ↔ `info` / `accent(base)`** — the one genuinely *unexamined*
  collision. Needs a decision about what a flow edge means, not a colour.
- The four "distinct from every colour" comments still overclaim; two of the four modules they sit
  in no longer have a private palette at all.
