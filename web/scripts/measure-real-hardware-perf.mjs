// T-3203 (T-1808 verbatim) — repeatable frontend scale/perf measurement
// against a real, running vnproxd's live UI. Standalone (uses the
// `playwright` package directly, NOT the `@playwright/test` runner), so it
// does not touch web/playwright.config.ts's shard system (pvemock-only,
// deliberately excluded from `make check` — see that file's own header)
// and cannot regress CI. Read-only against the daemon: logs in and reads
// the page, stages/applies nothing.
//
// Lives under web/scripts/ (not planning/reports/) so its bare `playwright`
// import resolves against web/node_modules without any module-path
// gymnastics — the same install `npm run e2e` already uses.
//
// Usage (from web/):
//   VNPROX_URL=https://192.168.1.9:8007 \
//   VNPROX_USER=root VNPROX_PASSWORD=<temporary password> \
//   node scripts/measure-real-hardware-perf.mjs
//
// VNPROX_PASSWORD is read once for one login form fill and never written to
// disk. Same temporary-password-then-lock discipline as
// planning/reports/T-3203-harness/measure-api.sh's header describes applies
// here — bracket the run in the calling shell.
import { chromium } from "playwright";

const url = process.env.VNPROX_URL;
const user = process.env.VNPROX_USER ?? "root";
const password = process.env.VNPROX_PASSWORD;
if (!url || !password) {
  console.error("set VNPROX_URL and VNPROX_PASSWORD (VNPROX_USER defaults to root)");
  process.exit(1);
}

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ ignoreHTTPSErrors: true });

const navStart = Date.now();
await page.goto(`${url}/login`);
await page.getByLabel("Username").fill(user);
await page.getByLabel("Password", { exact: true }).fill(password);
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL("**/topology");
await page.waitForLoadState("networkidle");
const wallClockLoginToTopologyMs = Date.now() - navStart;

const paintTiming = await page.evaluate(() => {
  const nav = performance.getEntriesByType("navigation")[0];
  const paints = Object.fromEntries(performance.getEntriesByType("paint").map((p) => [p.name, p.startTime]));
  return {
    domContentLoadedMs: nav ? nav.domContentLoadedEventEnd : null,
    loadEventMs: nav ? nav.loadEventEnd : null,
    firstPaintMs: paints["first-paint"] ?? null,
    firstContentfulPaintMs: paints["first-contentful-paint"] ?? null,
  };
});

// Switch to the Graph (react-flow) view and measure a short pan/zoom
// interaction, mirroring web/e2e/perf.spec.ts's rAF frame-delta sampler —
// same method, applied to the real cluster's real (small) entity count
// instead of a pvemock fixture.
await page.getByRole("radio", { name: "Graph" }).click();
const pane = page.locator(".react-flow__pane");
await pane.waitFor({ state: "visible" });
const box = await pane.boundingBox();

let frameStats = null;
if (box) {
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.evaluate(() => {
    window.__vnproxFrameDeltas = [];
    let last = performance.now();
    const tick = (now) => {
      window.__vnproxFrameDeltas.push(now - last);
      last = now;
      if (window.__vnproxSamplerRunning) requestAnimationFrame(tick);
    };
    window.__vnproxSamplerRunning = true;
    requestAnimationFrame(tick);
  });
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  for (let i = 0; i < 20; i++) {
    await page.mouse.move(cx + i * 4, cy + i * 2, { steps: 2 });
  }
  await page.mouse.up();
  for (let i = 0; i < 8; i++) {
    await page.mouse.wheel(0, -100);
  }
  for (let i = 0; i < 8; i++) {
    await page.mouse.wheel(0, 100);
  }
  const deltas = await page.evaluate(() => {
    window.__vnproxSamplerRunning = false;
    return window.__vnproxFrameDeltas ?? [];
  });
  if (deltas.length > 0) {
    const sorted = [...deltas].sort((a, b) => a - b);
    const total = deltas.reduce((a, b) => a + b, 0);
    frameStats = {
      frames: deltas.length,
      meanFps: Math.round((deltas.length / (total / 1000)) * 10) / 10,
      p95FrameMs: Math.round(sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))] * 10) / 10,
      maxFrameMs: Math.round(sorted[sorted.length - 1] * 10) / 10,
      pctOver60fpsBudget: Math.round((deltas.filter((d) => d > 1000 / 60 + 1).length / deltas.length) * 1000) / 10,
    };
  }
}

const memory = await page.evaluate(() => {
  // Chromium-only (performance.memory is non-standard); undefined elsewhere.
  const m = performance.memory;
  return m ? { usedJSHeapMb: Math.round((m.usedJSHeapSize / 1048576) * 10) / 10 } : null;
});

const result = {
  wallClockLoginToTopologyMs,
  ...paintTiming,
  panZoom: frameStats,
  memory,
};
console.log(JSON.stringify(result, null, 2));

await browser.close();
