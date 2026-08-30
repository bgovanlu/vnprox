# T-4306 — The overlay vocabulary has run out of hue circle, and one collision was added tonight

**Phase:** 43 (Canvas rendering)
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

- **Stop `blast-radius` competing on hue.** It is an *emphasis*, not a category — "these are the
  entities this failure would take out". It already draws its own ring, so it can carry
  `--color-status-critical` plus a distinct dash/animation, the way T-4303 moved traffic from a
  private ramp onto width plus a severity band. That frees hue 323 and removes two of the sixteen
  pairs.
- **Decide what `flow` means.** A flow edge 9deg from `info` and from the accent is the one
  genuinely *unexamined* collision left. Either it is deliberately informational — in which case
  say so and let it resolve `--color-status-info` — or it needs its own channel.
- **Do not re-hue `SIM_STROKE` or `STP_BLOCKING_STROKE`.** Those collisions are the point, and the
  census records them as such. A future card that "fixes" them would be undoing two deliberate
  designs on the strength of a number.
- Rewrite the four comments. They currently assert a property the code does not have.

## Acceptance criteria

1. `overlayHues.test.ts`'s census shrinks, and every pair removed from it is removed by a *design*
   change, not by editing the expected list.
2. `blast-radius` and `accent(demo)` are no longer under the floor.
3. The four "distinct from every colour" comments say something true, or are deleted.
4. `docs/design-language.md` §8.4's channel-assignment table gains a row for what an overlay does
   when the circle has nothing left — that is now a documented, measured fact rather than a thing
   each card rediscovers.
