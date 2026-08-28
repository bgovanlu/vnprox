# T-4216 — The visual gate had never run, and still does not capture the map

**Phase:** 42 (Design language) — follow-up to T-4210
**Status:** parts 1 and 2 done (`e0590c43`, `231a34ba`); part 3 open
**Depends on:** T-4210 (visual regression gate)

## 1. It had never run — fixed

T-4210 shipped `web/e2e/visual.spec.ts`, a screenshot gate over every routed page in
light/dark/demo, and recorded that baselines were deliberately not committed. The reason no
baselines were committed turns out to be that **no baseline had ever been produced**. On its first
real execution all 96 captures failed:

```
page.addStyleTag: Applying inline style violates the following
Content Security Policy directive 'style-src 'self''
```

`hideScrollbars()` injected a stylesheet into the page, and vnproxd serves a strict CSP that
refuses it. **The CSP was right.** A screenshot helper is not entitled to an exemption from a
security header, and relaxing it for the test build would make the gate guard a page that is not
the page users get — the one thing a visual gate must never do.

Both determinism tricks moved outside the document:

- `--hide-scrollbars` as a Chromium launch flag, so the page under test is byte-identical to
  production.
- `contextOptions: { reducedMotion: "reduce" }` instead of injected `animation: none`, which
  exercises T-4206's *existing* global reduced-motion gate rather than a path invented for the
  test. (In Playwright 1.61 `reducedMotion` is a BrowserContext option; the obvious top-level
  `use: { reducedMotion }` spelling type-errors rather than silently doing nothing, which is the
  better of the two ways to be wrong about it.)

Result: **81 passed, 15 failed**, and 81 baselines exist for the first time.

## 2. The gate does not capture the topology map — DONE

The remaining problem is larger than the fifteen failures.

`src/topology/store.ts:176` sets `viewMode: "switch"`. The topology page therefore opens on the
Switch faceplate view, and `visual.spec.ts` navigates and captures without clicking anything. So
the baseline named `topology-dark-linux.png` is a screenshot of the switch view.

**The graph canvas — the product's flagship visual, and the entire subject of Phase 43 — has no
baseline in the visual regression gate.** T-4301 changed every colour that canvas draws with and
this suite could not have detected any of it.

Fixed with the product's own share-link deeplink rather than a click:
`/topology?svLayers=phys,l2,sdn,guest&svView=graph&svZoom=1&svX=0&svY=0`. A click would make the
capture depend on a control label, so renaming the Switch/Graph button would silently stop
capturing the map — the same class of silent skip this card exists for.

Two details worth keeping. `svLayers` is **mandatory**: `decodeViewFromSearch` returns `undefined`
without it and ignores the whole deeplink, which would have left the new test quietly
screenshotting the switch view a second time and reporting green. And the test asserts the
deeplink actually took (`getByRole("radio", { name: "Graph" })` is checked) — a silently-ignored
parameter and a working one are indistinguishable in a screenshot nobody has looked at, which is
how the map came to be missing from this suite to begin with.

While looking at the one baseline that does exist, two things are worth recording:

- Three stacked banners occupy roughly the top 60% of the topology page before any content —
  a stale-data warning, an eight-item findings list, and an LLDP notice. The content is
  environment-specific (the mock cluster's peers genuinely are unreachable), but the *stacking*
  is not, and neither is the second point.
- Those banners print **raw wrapped Go error strings** to the operator, e.g.
  `Get "https://10.10.0.12:8007/api/peer/host/neighbors?node=pve2": peer: peer_untrusted:
  cluster CA trust anchor unavailable: reading /etc/pve/pve-root-ca.pem: open ...: no such file
  or directory`. That is a debugging artifact in a product surface. Worth its own card; noted
  here because a visual gate that had never run is exactly why nobody had seen it.

## 2b. The harness was serving a three-day-old binary — fixed

Capturing the graph view worked, and the first thing the picture showed was a violet accent that
no longer exists in the source. `web/e2e/shards.ts`'s `command()` read:

```ts
const head = existsSync(prebuilt) ? prebuilt : `go run ./cmd/${binary}`;
```

The prebuilt binary in `test-results/e2e-bin/` was used **whenever the file existed, regardless of
age**, and that directory is a gitignored cache nothing invalidates. The vnproxd it ran was built
on 08-25, before Phase 42 started, and vnproxd embeds `web/dist` at compile time — so
`npm run build` was not enough on its own. Proof: the binary contains
`assets/index-CX813A1Z.css` while the current build emits `assets/index-CIgh2cj4.css`.

**All 87 baselines from the first successful run were of an app that no longer exists**, and
nothing in the output said so. I drew a conclusion from them (that the topology layer chips were
still pre-T-4201 indigo) and it was wrong — the source says `bg-accent-600` and the built CSS
resolves it to `#027a9a`.

Fixed by comparing the binary's mtime against `web/dist/index.html` and falling back to `go run`
when the SPA is newer. Falling back is always correct, just slower.

The severity is not symmetric across suites, which is worth stating: a functional spec usually
survives a stale binary by failing loudly. A visual gate cannot. A stale build yields baselines
that are internally consistent, reproducible, and describe the wrong product — every property you
would use to convince yourself they were trustworthy.

This is the second gate in one day found green while measuring the wrong thing; the first was
`slateContrast.test.ts` measuring against pre-T-4203 surfaces. Both had the same shape: the gate
worked, its *referent* had moved, and nothing tied the two together.

## 3. Five routes land on `/topology` instead of themselves — open

The fifteen failures are five routes x three modes, all failing the same way — `page.goto()`
lands on `/topology`:

`/cabling`, `/firewall/compiled`, `/guest`, `/route-explorer`, `/wireguard`

What is established: all five are declared with static `path=` literals before App.tsx's
`<Route path="*" element={<Navigate to="/topology" replace />} />` catch-all, all five are
statically imported (not lazy), and none is narrow-viewport-gated — `useNarrowViewport` is
`(max-width: 767px)` and the gate runs at 1400px, so `DesktopOnlyRoute` renders its children.

What is **not** established is the cause for four of them. For `/guest` it is clear:
`GuestEgoView.tsx:492` reads `searchParams.get("ref")` and the component calls
`navigate("/topology")` when it has nothing to show. A guest ego-view with no guest is
legitimately not a page.

That points at the real defect rather than the symptom: **`routeInventory.ts` derives its list
from `<Route path=>` declarations, and a route declaration cannot express "needs context to be a
page".** The same limitation is why T-4212 exists — the axe sweep's hand-kept list had drifted
from the router — so deriving the list was right; it just is not sufficient.

Note the overlap: `/guest` and `/wireguard` are two of the five here **and** the two routes
T-4212 found have never been accessibility-checked. Two independent gates, both silently skipping
the same pair. Whatever fixes this should fix T-4212's list too, rather than each gate growing
its own exception table.

## Acceptance criteria

1. The topology route is captured in graph view as well as switch view, in all three modes, and a
   deliberate change to a `canvasPalette` role fails the suite.
2. The five context-requiring routes either get the context they need (a fixture ref for `/guest`)
   or are declared as such in one place that **both** this gate and the axe sweep read — not two
   exception lists that will drift apart the way the axe list already drifted from the router.
3. The suite is green end to end and the run is repeated once to prove determinism, which is what
   T-4210's own card asked for and what could not be done until it ran at all.
4. Baselines stay uncommitted, per T-4210's deliverable 4 — this card does not change that.
