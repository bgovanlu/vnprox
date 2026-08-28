# T-4305 — The canvas work landed on a renderer that is off by default

**Phase:** 43 (Canvas rendering)
**Status:** open
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

**1. The v1 renderer needs its own contrast pass.** It was never measured, because the whole of
T-4301 was framed around `SceneTheme`. `EntityNode.tsx`'s `STATUS_CLASSES` already uses
`border-status-critical` / `-degraded` (T-4204's sweep reached it), and its `ok`/`unknown` states
deliberately stay neutral slate — the same choice `StatusDot` documents, so that is intentional
and not a gap. What has not been checked is the rest of the node: label text, chips, the drift
dash, the sim rings.

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
