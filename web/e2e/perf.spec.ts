// F-07: T-107 acceptance criterion 5's pan/zoom frame-rate measurement,
// executed against the real stack at three-node-vlan scale and written up
// in docs/testing/topology-performance.md (method, environment caveats,
// numbers). The sampler is requestAnimationFrame-based: each rAF callback
// records the delta since the previous frame, so dropped/long frames show
// up directly as deltas above ~16.7ms (60fps budget).
import { expect, test, type Page } from "@playwright/test";

declare global {
  interface Window {
    __vnproxFrameDeltas?: number[];
    __vnproxSamplerStop?: () => void;
  }
}

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
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

test("measure pan/zoom frame timings on the three-node-vlan topology", async ({ page }, testInfo) => {
  await logIn(page);
  await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(3);
  const pane = page.locator(".react-flow__pane");
  await expect(pane).toBeVisible();
  const box = await pane.boundingBox();
  if (!box) throw new Error("canvas pane has no bounding box");
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;

  // Start the rAF sampler.
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

  // Pan: four continuous drags across the canvas (many small mouse-move
  // steps each, like a real drag), then zoom: wheel in and back out at the
  // canvas center.
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
  // Attach + print, for transcription into docs/testing/
  // topology-performance.md. No hard 60fps assertion: headless Chromium in
  // a VM measures the code path, not a user's GPU-composited desktop — the
  // doc records both the numbers and that caveat.
  await testInfo.attach("frame-stats", { body: JSON.stringify({ stats: s, deltas }, null, 2), contentType: "application/json" });
  console.log(`[perf] frames=${String(s.frames)} meanFps=${s.meanFps.toFixed(1)} p95=${s.p95FrameMs.toFixed(1)}ms max=${s.maxFrameMs.toFixed(1)}ms over-budget=${s.pctOver60fpsBudget.toFixed(1)}%`);
});
