// F-07: T-107 acceptance criterion 5's pan/zoom frame-rate measurement,
// executed against the real stack at three-node-vlan scale and written up
// in docs/testing/topology-performance.md (method, environment caveats,
// numbers). The sampler is requestAnimationFrame-based: each rAF callback
// records the delta since the previous frame, so dropped/long frames show
// up directly as deltas above ~16.7ms (60fps budget).
import { expect, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

declare global {
  interface Window {
    __vnproxFrameDeltas?: number[];
    __vnproxSamplerStop?: () => void;
  }
}

// T-605: see topology.spec.ts's identical helper doc comment — suppressing
// the onboarding walkthrough banner via an injected stylesheet (rather
// than clicking its "Minimize" button) avoids persisting a PUT
// /layouts/onboarding that would leak into other spec files sharing this
// same webServer/DB within one test run (this measurement wants the map's
// full steady-state viewport regardless of onboarding state either way).
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    // docs/security.md's CSP is `style-src 'self'` (no 'unsafe-inline'),
    // so an injected <style> element (tried first) is silently blocked —
    // it exists in the DOM but the browser refuses to apply its rule.
    // Directly setting a CSSStyleDeclaration property via the `.style`
    // object (as opposed to the HTML `style` *attribute*, e.g. via
    // setAttribute) is not restricted by style-src (a well-known CSP
    // nuance), so this sets the property JS-side instead — reapplied via
    // MutationObserver since the banner mounts asynchronously (after
    // GET /layouts/onboarding resolves) well after this init script runs.
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
    };
    // lib.dom.d.ts types document.documentElement as always non-null, but
    // empirically (this exact init-script timing, before the parser has
    // created it yet) it can genuinely be null here — hence the try/catch
    // retry loop below instead of a type-checker-satisfying null check
    // that strict lint would flag as "always falsy" against that (in this
    // one narrow case, wrong) type.
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
  // This measurement is specifically of the elk graph canvas's pan/zoom
  // (67fff26 landed Switch, not Graph, as /topology's default view).
  await switchToGraphView(page);
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
