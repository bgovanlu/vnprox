# T-4305 — The canvas work landed on a renderer that is off by default

**Phase:** 43 (Canvas rendering)
**Status:** criterion 2 measured (below); 1, 3, 4 open
**Corrects:** T-4301's impact claim, and narrows T-4302's

## The correction

T-4301 replaced `themeColors()` with `canvasPalette`, fixing three measured contrast failures:
`kindText` at 2.56/3.07, `nodeBorderOk` at 1.48/1.93, `edgeDefault` at 2.45/2.36. I described
that as fixing "the map's colours." It is narrower than that.

`SceneTheme` is consumed by `TopologyCanvasV2` and nothing else. `store.ts:38` reads the renderer
flag:

```ts
function readRendererFlag(): RendererVersion {
  try {
    return globalThis.localStorage.getItem(RENDERER_FLAG_KEY) === "v2" ? "v2" : "v1";
  } catch {
    return "v1";
  }
}
```

**v1 is the default and v2 is opt-in**, behind the "Canvas v2" toggle visible in the topology
toolbar. So T-4301's fixes are real and land where they were measured — they just land on a
renderer a user has to switch on. The default graph view is xyflow with DOM nodes
(`EntityNode.tsx`), whose colours are Tailwind classes and therefore *are* reachable by the
existing gates.

This was findable only because the T-4304 layout gate selected `topology-canvas-v2` and timed out
three times: the element it asked for is not on the default page.

## What that implies

**1. The v1 renderer needed its own contrast pass — now done, and the result is split.**

**Text passes, everywhere.** Measured against every `KIND_ACCENT` wash a v1 node can actually
land on (which is the only correct denominator — the node brings its own background, so the
surface-ladder gates could not have judged it even if they had looked):

| | worst case | |
|---|---|---|
| light label `slate-800` | 11.87 | PASS |
| light kind/badge `slate-600` | 6.15 | PASS |
| dark label `slate-100` | 8.87 | PASS |
| dark kind/badge `slate-300`/`200` | 6.55 | PASS |

No defect. Worth recording as a null result rather than silence: I expected to find the three
failures v2 had, and there are none. `STATUS_CLASSES` already uses `border-status-critical` /
`-degraded` from T-4204's sweep, and the `ok`/`unknown` states staying neutral slate is the
documented choice `StatusDot` makes too, not a gap.

**The border fails, in both renderers.** The `ok`-state node border measures:

| | vs page | vs worst wash |
|---|---|---|
| light `slate-300` | **1.43** | **1.20** |
| dark `slate-600` | **2.35** | **1.37** |

against WCAG 1.4.11's 3:1. That is the same defect as v2's `nodeBorderOk` (1.48 / 1.93), which
T-4301 fixed with `--color-outline` — so the failure is not a v2 quirk, it is in the product's
default view as well.

**But `--color-outline` does not fix v1 on its own**, and the reason is the third instance of one
pattern today. The token was solved against the four *surface* levels and clears 3:1 on all of
them (3.14 light / 3.80 dark against the page). Against the kind washes it measures **2.64 and
2.21** — because the v1 node introduces backgrounds that were not in the set the token was solved
against. A guard that measures against a surface is blind to a call site that brings its own
surface; here, so is the token.

**Which ties this card to T-4302.** If kind stops being encoded as colour, the washes go away, the
node sits on `surface-raised`, and `--color-outline` measures 3.25 / 3.43 there — it already
clears. So T-4302 is not merely a tidiness card that happens to sit nearby: **removing the
categorical washes is what makes the border fixable with the token that already exists.** Doing
the border first would mean solving a second outline value against a set of backgrounds that
T-4302 is about to delete.

**2. T-4302 has two call sites, not one.** `EntityNode.tsx` carries its own `KIND_ACCENT`, a
second copy of the categorical scale:

```ts
bond: "bg-sky-50 dark:bg-sky-950",  bridge: "bg-indigo-50 dark:bg-indigo-950",
vlan: "bg-violet-50 dark:bg-violet-950",  guest: "bg-emerald-50 dark:bg-emerald-950",  ...
```

The duplication argument holds — one concept, two tables, no mechanism keeping them agreed. But
**the collision argument is much weaker here and T-4302 should say so.** v1 encodes kind as a
*background wash* at the 50/950 steps, not as a saturated stroke. A near-white or near-black tint
does not compete with a status border for attention the way v2's `#0ea5e9` rail does, and the 40°
hue-separation argument barely applies to a colour at that chroma. T-4302's recommendation
(pictogram carries kind, colour means status) is still right for v2 and still probably right for
v1, but for v1 it is a consistency argument, not a legibility one. Do not let the stronger v2
framing smuggle itself across.

**3. Several layers are v2-only.** `wgPaintable`, `k8sPaintable`, `flowsPaintable` and
`latencyPaintable` all require `rendererVersion === "v2"`, so WireGuard, Kubernetes, Flows and
Latency paint nothing in the default renderer even though their toolbar buttons are present. That
is a product question rather than a design-language one, and it is out of scope here — noted
because it is the same root fact and someone will hit it.

## Acceptance criteria

1. `docs/design-language.md`'s canvas section states which renderer each rule applies to. A rule
   that silently means "v2 only" is how this happened.
2. The v1 node's colours are measured against the surface ladder the same way `canvasPalette.test.ts`
   measures v2's, and anything below AA (text) or 3:1 (graphics) is fixed.
3. T-4302 is amended with the v1 table and the weaker-collision caveat above.
4. The visual gate captures the graph view in **both** renderers, or states in one place why one
   is enough. Capturing only the default hid a whole renderer; capturing only v2 would hide the
   one users actually get.

## Note

The lesson is the same one this phase keeps producing, in a new place: I measured a thing, fixed
it, and described the fix in terms of the *product* when it was true of a *code path*. The
measurement was right. The scope sentence around it was not, and only a timed-out selector made
the difference visible.
