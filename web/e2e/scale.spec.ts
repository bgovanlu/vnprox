// SPDX-License-Identifier: Apache-2.0

// T-607: frontend performance + progressive-disclosure verification at the
// docs/features/topology.md §4 scale target (8 nodes x 6 NICs, 4
// bridges/node, 300 guests, 40 VNets — testdata/clusters/scale-lab.yaml,
// served by web/playwright.config.ts's third webServer pair on
// 28006/28007). Two measurements, both transcribed into
// docs/performance.md:
//
//  1. Initial render time: from navigating to /topology (after the
//     backend's collectors have already converged — polled for directly
//     below, so this isolates frontend render cost from backend poll
//     latency, which internal/inventory's own BenchmarkSnapshotAtScale and
//     cmd/vnproxd's BenchmarkAPIAtScale already measure separately) to the
//     canvas showing its full, collapsed node set.
//  2. Pan/zoom frame timings at this scale, via the exact same
//     requestAnimationFrame sampler perf.spec.ts uses at three-node-vlan
//     scale — see that file's doc comment for the method and its
//     environment caveats (headless/software-rasterized numbers are a
//     pessimistic floor, not a 60fps proof either way).
//
// Progressive disclosure (task deliverable: "collapse defaults, filter
// prompt at the element cap"): this spec also asserts collapsed
// guest-group pills are visible in the real rendered DOM at this scale,
// and that the over-render-cap filter-prompt banner is NOT showing — this
// fixture is deliberately sized at the "smooth" target, not the "beyond
// it" one (see web/src/topology/scaleLab.render.test.tsx for the
// synthetic beyond-cap counterpart, which proves the prompt itself trips
// correctly once the real 2,000-element arithmetic is exceeded).
//
// T-2506 made every number this file gates on come from perf/budgets.json —
// the same file internal/collect/sim_bench_test.go reads for the Go-side
// budgets. Nothing below holds a limit of its own; `budgets` is looked up by
// id, the sample count N comes from the budget rather than from the test, and
// every measurement prints its headroom whether it passed or not. See
// docs/performance.md section 13.
import { expect, request, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";
import {
  budgetById,
  budgetsForSite,
  check,
  evaluate,
  loadBudgets,
  renderReport,
  SCALE_SITE,
  thisMachine,
  type Result,
} from "../perf/budgets";

declare global {
  interface Window {
    __vnproxFrameDeltas?: number[];
    __vnproxSamplerStop?: () => void;
  }
}

const BASE = "https://127.0.0.1:28007";

// See perf.spec.ts's identical helper doc comment: suppressing the
// onboarding walkthrough banner via a JS-set style property (CSP blocks an
// injected <style> element) rather than clicking through it, so this
// measurement sees the map's real steady-state viewport.
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
    };
    const start = () => {
      try {
        suppress();
        new MutationObserver(suppress).observe(document.documentElement, { childList: true, subtree: true });
      } catch {
        setTimeout(start, 0);
      }
    };
    start();
  });
}

async function logIn(page: Page): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await page.goto(BASE + "/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Polls the real backend directly (bypassing the browser) until the
 * collectors have converged past a threshold node count, so the
 * subsequent page load measures frontend render cost against
 * already-settled data rather than conflating it with collector poll
 * latency (a separate, backend-side concern this task measures via
 * cmd/vnproxd's BenchmarkAPIAtScale instead). */
async function waitForBackendConverged(minNodes: number): Promise<void> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(BASE + "/api/v1/auth/login", {
      data: { username: "root@pam", password: "vnprox-mock" },
    });
    expect(login.ok()).toBe(true);

    const deadline = Date.now() + 60_000;
    for (;;) {
      const resp = await ctx.get(BASE + "/api/v1/topology");
      if (resp.ok()) {
        const body = (await resp.json()) as { nodes: unknown[] };
        if (body.nodes.length >= minNodes) return;
      }
      if (Date.now() > deadline) {
        throw new Error(`backend topology never reached ${String(minNodes)} nodes within 60s`);
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  } finally {
    await ctx.dispose();
  }
}

interface FrameStats {
  frames: number;
  meanFps: number;
  p95FrameMs: number;
  maxFrameMs: number;
  pctOver60fpsBudget: number;
}

function stats(deltas: number[]): FrameStats {
  const sorted = [...deltas].sort((a, b) => a - b);
  const total = deltas.reduce((a, b) => a + b, 0);
  const p95 = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))] ?? 0;
  return {
    frames: deltas.length,
    meanFps: deltas.length / (total / 1000),
    p95FrameMs: p95,
    maxFrameMs: sorted[sorted.length - 1] ?? 0,
    pctOver60fpsBudget: (deltas.filter((d) => d > 1000 / 60 + 1).length / deltas.length) * 100,
  };
}

/** The budgets in perf/budgets.json this file measures, and which test
 * measures each. Not a second copy of the budget list: the first test below
 * asserts that the union of these ids is exactly the set of budgets naming
 * this file as their site, so a budget added to the file and forgotten here —
 * or removed from the file and left here — is a failure rather than a silent
 * gap. Same shape as web/e2e/shards.ts's "a spec in no shard is a hard error". */
const MEASURED_BY: Readonly<Record<string, readonly string[]>> = {
  "initial render and progressive disclosure": ["topology.scale.page_render_ms"],
  "v1 pan/zoom": ["topology.scale.v1_pan_zoom_p95_frame_ms"],
  "v2 pan/zoom": ["topology.scale.v2_pan_zoom_p95_frame_ms", "topology.scale.v2_pan_zoom_p95_frame_hardware_ms"],
};

const budgets = loadBudgets();
const machine = thisMachine();

/** Prints the headroom for everything measured (AC5), then fails the test if a
 * gating budget was exceeded. Report first, always: a run that fails should
 * still say how much room the budgets that passed had left. */
async function reportAndGate(testInfo: { attach: (name: string, opts: { body: string; contentType: string }) => Promise<void> }, results: Result[]): Promise<void> {
  const report = renderReport(results, machine);
  console.log(report);
  await testInfo.attach("perf-budgets", { body: report, contentType: "text/plain" });
  const exceeded = check(results);
  expect(exceeded, exceeded ?? "").toBeNull();
}

/** One pan/zoom round: install the rAF sampler, drive four pan passes and 24
 * wheel steps over the canvas at (cx, cy), stop the sampler, return the frame
 * statistics.
 *
 * Extracted by T-2506 because both renderers' budgets are now medians of N
 * rounds rather than one measurement (`samples` in perf/budgets.json), and
 * three copies of the gesture would be three chances for the v1 and v2 numbers
 * to stop being comparable. The gesture itself is byte-for-byte what this file
 * has driven since T-607. */
async function panZoomRound(page: Page, cx: number, cy: number): Promise<FrameStats> {
  await page.evaluate(() => {
    window.__vnproxFrameDeltas = [];
    let last = performance.now();
    let running = true;
    const tick = (now: number) => {
      window.__vnproxFrameDeltas?.push(now - last);
      last = now;
      if (running) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
    window.__vnproxSamplerStop = () => {
      running = false;
    };
  });

  for (let pass = 0; pass < 4; pass++) {
    const dir = pass % 2 === 0 ? 1 : -1;
    await page.mouse.move(cx - dir * 200, cy - 100);
    await page.mouse.down();
    for (let step = 0; step <= 40; step++) {
      await page.mouse.move(cx - dir * 200 + dir * step * 10, cy - 100 + step * 5, { steps: 1 });
    }
    await page.mouse.up();
  }
  await page.mouse.move(cx, cy);
  for (let i = 0; i < 12; i++) {
    await page.mouse.wheel(0, -120);
  }
  for (let i = 0; i < 12; i++) {
    await page.mouse.wheel(0, 120);
  }

  const deltas = await page.evaluate(() => {
    window.__vnproxSamplerStop?.();
    return window.__vnproxFrameDeltas ?? [];
  });
  expect(deltas.length).toBeGreaterThan(30);
  return stats(deltas);
}

/** Runs `rounds` pan/zoom rounds and returns each round's stats. */
async function panZoomRounds(page: Page, cx: number, cy: number, rounds: number): Promise<FrameStats[]> {
  const out: FrameStats[] = [];
  for (let i = 0; i < rounds; i++) {
    out.push(await panZoomRound(page, cx, cy));
  }
  return out;
}

function logRounds(tag: string, rounds: readonly FrameStats[]): void {
  for (const s of rounds) {
    console.log(
      `[${tag}] frames=${String(s.frames)} meanFps=${s.meanFps.toFixed(1)} p95=${s.p95FrameMs.toFixed(1)}ms max=${s.maxFrameMs.toFixed(1)}ms over-budget=${s.pctOver60fpsBudget.toFixed(1)}%`,
    );
  }
}

test.describe.configure({ mode: "serial" });

// Cheap, browser-free, and deliberately first in the file: a budget that
// quietly stops being measured reads exactly like a budget that passes.
test("scale-lab: every budget naming this file is measured by one of its tests", () => {
  const declared = [...new Set(Object.values(MEASURED_BY).flat())].sort();
  const inFile = budgetsForSite(budgets, SCALE_SITE)
    .map((b) => b.id)
    .sort();
  expect(declared).toEqual(inFile);
  for (const id of declared) {
    // Throws, naming the alternatives, if the file no longer has this budget.
    expect(budgetById(budgets, id).site).toBe(SCALE_SITE);
  }
});

test("scale-lab: initial render time and progressive disclosure at the documented scale target", async ({ page }, testInfo) => {
  // The captured web/src/topology/__fixtures__/scale-lab-topology.json
  // (recorded once against this exact fixture — see that file's doc
  // comment) landed at 203 projected nodes post-collapse; 150 is a safe
  // "converged enough" floor that tolerates this dev machine's own host
  // interfaces varying the exact count.
  await waitForBackendConverged(150);

  await suppressOnboardingWalkthrough(page);
  const start = Date.now();
  await page.goto(BASE + "/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  // 67fff26 landed Switch, not Graph, as /topology's default view (see
  // helpers.ts) — the exact "vmbr0" bridge buttons and "N guests" pills
  // this measurement counts below are the Graph canvas's EntityNode
  // renderings (Switch's faceplate labels the same bridge "vmbr0 switch"
  // instead), so this test is inherently Graph-scoped. Switching happens
  // as early as possible (right after the URL lands) to keep the extra
  // click's cost out of most of the measured window, but the reported
  // initialRenderMs below does now include that one click+render, unlike
  // before this view toggle existed.
  await switchToGraphView(page);
  // "Rendered" = at least one per-node bridge button has mounted for every
  // one of the 8 scale-lab nodes (vmbr0 is declared on every node).
  await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(8, { timeout: 30_000 });
  const initialRenderMs = Date.now() - start;

  await testInfo.attach("initial-render-ms", { body: String(initialRenderMs), contentType: "text/plain" });
  console.log(`[scale] initial render (login -> 8x vmbr0 mounted): ${String(initialRenderMs)}ms`);
  expect(initialRenderMs).toBeGreaterThan(0);

  // Progressive disclosure, part 1: collapsed guest-group pills ("N
  // guests") are visible in the real DOM — collapse-by-default engaged.
  const guestPills = page.getByText(/^\d+ guests$/);
  await expect(guestPills.first()).toBeVisible();
  expect(await guestPills.count()).toBeGreaterThan(0);

  // Progressive disclosure, part 2: this fixture is sized at the "smooth"
  // scale target, not the "beyond it" one — the over-render-cap filter
  // prompt must NOT be showing (web/src/topology/scaleLab.render.test.tsx
  // covers the synthetic beyond-cap case where it does trip).
  await expect(page.getByText(/above the ~2,000/)).toHaveCount(0);

  // T-2506's budget, measured separately from the login-inclusive number
  // above. Its window is narrower on purpose — navigation to /topology on an
  // already-authenticated session until the canvas has mounted all 8 nodes'
  // vmbr0 — because that is the part a render regression would move, and it
  // is repeatable, which the one-shot login path is not. N comes from the
  // budget (3), not from this test.
  const budget = budgetById(budgets, "topology.scale.page_render_ms");
  const samples: number[] = [];
  for (let i = 0; i < budget.samples; i++) {
    const from = Date.now();
    await page.goto(BASE + "/topology");
    await switchToGraphView(page);
    await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(8, { timeout: 30_000 });
    samples.push(Date.now() - from);
  }
  console.log(`[scale] page render samples: ${samples.map((s) => `${String(s)}ms`).join(", ")}`);
  await reportAndGate(testInfo, [evaluate(budget, samples, machine)]);
});

test("scale-lab: pan/zoom frame timings at the documented scale target", async ({ page }, testInfo) => {
  await waitForBackendConverged(150);
  await logIn(page);
  // This measurement is specifically of the elk graph canvas's pan/zoom
  // (67fff26 landed Switch, not Graph, as /topology's default view).
  await switchToGraphView(page);
  await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(8, { timeout: 30_000 });

  const pane = page.locator(".react-flow__pane");
  await expect(pane).toBeVisible();
  const box = await pane.boundingBox();
  if (!box) throw new Error("canvas pane has no bounding box");
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;

  // Before T-2506 this test asserted only that more than 30 frames had been
  // sampled, so a renderer change that halved v1's frame rate would have
  // passed it. The budget it gates on now is the same headless ceiling T-901
  // chose for v2, and it lives in perf/budgets.json rather than here.
  const budget = budgetById(budgets, "topology.scale.v1_pan_zoom_p95_frame_ms");
  const rounds = await panZoomRounds(page, cx, cy, budget.samples);
  logRounds("scale", rounds);
  await testInfo.attach("frame-stats", { body: JSON.stringify({ rounds }, null, 2), contentType: "application/json" });
  await reportAndGate(testInfo, [evaluate(budget, rounds.map((s) => s.p95FrameMs), machine)]);
});

// T-901: the same rAF frame-delta sampler above, but against the v2 canvas
// renderer (TopologyCanvasV2) instead of the v1 React Flow one, on the same
// scale-lab fixture. Proves T-901 AC6's frame budget (p95 <= 20ms) for the
// new engine, with the identical headless/software-rasterized caveat this
// file's other measurements carry (see perf.spec.ts's doc comment): these
// numbers are a pessimistic floor on a GPU-less QEMU host, not a hardware
// guarantee. Attached as its own `frame-stats` artifact and transcribed into
// docs/performance.md §3.
//
// The v2 flag is set in localStorage *before* the app boots (store.ts reads
// it on construction), so switching to the Graph view lands the v2 canvas
// with no refetch. Readiness is asserted via the v2 accessibility bridge:
// one focusable, aria-labeled proxy per entity (8 nodes each declare vmbr0),
// which exists only once v2 has projected the full topology.
async function enableV2Renderer(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("vnprox.topology.rendererV2", "v2");
    } catch {
      /* private-mode localStorage: the flag simply won't stick */
    }
  });
}

test("scale-lab (v2 canvas renderer): pan/zoom frame timings at the documented scale target", async ({ page }, testInfo) => {
  await waitForBackendConverged(150);
  await enableV2Renderer(page);
  await logIn(page);
  // Selecting Graph mounts the v2 canvas (the flag above chose the engine).
  await page.getByRole("radio", { name: "Graph" }).click();
  const v2 = page.getByTestId("topology-canvas-v2");
  await expect(v2).toBeVisible();
  // The v2 a11y bridge exposes one labeled proxy per entity — wait for all 8
  // nodes' vmbr0 bridges to have projected before measuring.
  const region = page.getByRole("application", { name: /Topology map/ });
  await expect(region.getByRole("button", { name: /bridge vmbr0/ })).toHaveCount(8, { timeout: 30_000 });

  const box = await v2.boundingBox();
  if (!box) throw new Error("v2 canvas has no bounding box");
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;

  const ceiling = budgetById(budgets, "topology.scale.v2_pan_zoom_p95_frame_ms");
  const hardware = budgetById(budgets, "topology.scale.v2_pan_zoom_p95_frame_hardware_ms");
  const rounds = await panZoomRounds(page, cx, cy, ceiling.samples);
  logRounds("scale-v2", rounds);
  await testInfo.attach("frame-stats", { body: JSON.stringify({ rounds }, null, 2), contentType: "application/json" });
  const p95s = rounds.map((s) => s.p95FrameMs);
  // The SAME measurement, compared against two different budgets, which is the
  // honest way to state what T-901 AC6 actually asked for:
  //
  //  - the 90 ms headless ceiling GATES. That was the hard-coded
  //    HEADLESS_REGRESSION_CEILING_MS here until T-2506 moved it into
  //    perf/budgets.json. It catches a catastrophic renderer regression against
  //    the ~50 ms this software-rasterised runner measures for BOTH renderers.
  //  - the 20 ms hardware target REPORTS. It is a GPU-compositing number, and
  //    this runner is headless swiftshader where v1 measures the same ~50 ms;
  //    gating on it would be gating on hardware the runner does not have. It is
  //    in the budgets file rather than in prose so its headroom is printed on
  //    every run, and so a future GPU-capable runner promotes it by editing one
  //    field. `absolute` scaling is why perfbudget refuses to let it gate.
  await reportAndGate(testInfo, [evaluate(ceiling, p95s, machine), evaluate(hardware, p95s, machine)]);
});
