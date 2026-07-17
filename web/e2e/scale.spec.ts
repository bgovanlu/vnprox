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
import { expect, request, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

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

test.describe.configure({ mode: "serial" });

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

  const s = stats(deltas);
  await testInfo.attach("frame-stats", { body: JSON.stringify({ stats: s, deltas }, null, 2), contentType: "application/json" });
  console.log(
    `[scale] frames=${String(s.frames)} meanFps=${s.meanFps.toFixed(1)} p95=${s.p95FrameMs.toFixed(1)}ms max=${s.maxFrameMs.toFixed(1)}ms over-budget=${s.pctOver60fpsBudget.toFixed(1)}%`,
  );
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

  const s = stats(deltas);
  await testInfo.attach("frame-stats", { body: JSON.stringify({ stats: s, deltas }, null, 2), contentType: "application/json" });
  console.log(
    `[scale-v2] frames=${String(s.frames)} meanFps=${s.meanFps.toFixed(1)} p95=${s.p95FrameMs.toFixed(1)}ms max=${s.maxFrameMs.toFixed(1)}ms over-budget=${s.pctOver60fpsBudget.toFixed(1)}%`,
  );
  // AC6: p95 frame time <= 20ms for the v2 renderer at the documented scale.
  expect(s.p95FrameMs).toBeLessThanOrEqual(20);
});
