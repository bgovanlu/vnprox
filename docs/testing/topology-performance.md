# Topology pan/zoom performance measurement (T-107 AC5 / audit F-07)

T-107's acceptance criterion 5: "pan/zoom stays at 60fps at the documented scale target —
document measurement." This is that document: method, numbers, and what the numbers do and
do not prove.

## Method

`web/e2e/perf.spec.ts` (run via `npm run e2e` in `web/`, see
[topology-render-verification.md](topology-render-verification.md) for the stack it
boots): after a real login against pvemock's three-node-vlan fixture, the spec

1. installs a `requestAnimationFrame` sampler in the page — every rAF callback records
   the delta since the previous frame, so long/dropped frames appear directly as deltas
   above the 60fps budget (~16.7 ms);
2. drives four continuous mouse drags across the canvas (React Flow pane panning,
   40 move steps each) followed by 12 wheel-zoom steps in and 12 out at the canvas
   center;
3. reports frame count, mean fps, p95/max frame time, and the share of frames over
   budget (attached as `frame-stats` JSON on the test result, printed as `[perf] ...`).

An idle-page control run (same headless browser, no canvas) was taken to establish the
environment's rAF ceiling.

## Results (2026-07-10, reference environment)

Environment: headless Chromium (Playwright 1.61), **software rasterization, no GPU**,
Linux 6.12 x86_64 VM. Scale: three-node-vlan fixture (~26 nodes / ~22 edges — the
fixture's PVE entities plus this machine's own host interfaces; well under the §4
2,000-element render cap).

| Run | Frames | Mean fps | p95 frame | Max frame | Frames > 16.7 ms |
|---|---|---|---|---|---|
| Idle control (no canvas) | 180 | 60.4 | 16.8 ms | — | ~0% |
| Pan/zoom, run 1 | 427 | 35.4 | 50.1 ms | 66.7 ms | 40.3% |
| Pan/zoom, run 2 | 424 | 34.6 | 50.1 ms | 66.8 ms | 40.3% |

## Reading the numbers honestly

- The idle control hits 60fps, so the headless environment **can** tick at 60 — the
  ~35fps mean under interaction is real render/layout cost, not a sampler artifact or an
  environment frame cap.
- **This environment is not the criterion's environment.** Headless Chromium in a GPU-less
  VM software-rasterizes every frame; a desktop browser composites pan/zoom transforms on
  the GPU, which is precisely the work React Flow's viewport transform offloads. The
  measured 35fps here is a *pessimistic floor*, not evidence the criterion fails on real
  hardware — and equally, 60fps on real hardware is not proven by this run.
- Scripted CDP mouse input serializes each move step over the protocol, which spreads the
  drag over more wall-clock time than a human gesture; frame deltas during genuine
  waiting are idle-fast, so this skews the mean *up* if anything, while the p95/max frame
  times (50/67 ms during active rendering) are the honest tail numbers.

**Conclusion:** measurement performed and documented; the 60fps criterion itself still
**requires human execution on a dev machine with GPU compositing** (see below). If that
run also shows sustained frames > 16.7 ms at three-node scale, treat it as a real finding
against the canvas rendering, not the environment.

## Re-running on a dev machine

Preferred: `cd web && npm run e2e` (headed variant: `npx playwright test e2e/perf.spec.ts
--headed`) and read the `[perf]` line.

Manual alternative (any browser, `make dev` running): paste into the devtools console on
`/topology`, then pan/zoom by hand for ~10 s:

```js
(() => {
  const deltas = [];
  let last = performance.now();
  const tick = (now) => { deltas.push(now - last); last = now; requestAnimationFrame(tick); };
  requestAnimationFrame(tick);
  setTimeout(() => {
    const s = [...deltas].sort((a, b) => a - b);
    const total = deltas.reduce((a, b) => a + b, 0);
    console.log({
      frames: deltas.length,
      meanFps: +(deltas.length / (total / 1000)).toFixed(1),
      p95ms: +s[Math.floor(s.length * 0.95)].toFixed(1),
      maxMs: +s[s.length - 1].toFixed(1),
      pctOverBudget: +((deltas.filter((d) => d > 1000 / 60 + 1).length / deltas.length) * 100).toFixed(1),
    });
  }, 10_000);
})();
```
