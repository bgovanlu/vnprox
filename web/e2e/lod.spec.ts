// SPDX-License-Identifier: Apache-2.0

// T-902 AC4 (minimap) + AC2 (LOD bands), real-browser coverage against the
// v2 canvas renderer (TopologyCanvasV2) on the primary three-node-vlan
// stack. web/src/topology/lod.test.ts and lod.render.test.tsx already cover
// the transform's data correctness and its Testing-Library-level wiring
// exhaustively; this file exists for the two things that genuinely need a
// real browser: real pointer-drag geometry on the minimap's <canvas>
// (jsdom has none), and a real wheel-driven zoom gesture landing the v2
// canvas in the "capsule" band end to end.
import { expect, test, type Page } from "@playwright/test";

// Same CSP-safe suppression technique as perf.spec.ts/topology.spec.ts's
// identical helper (see their doc comments): a <style> element is blocked
// by this app's style-src 'self' CSP, so the banner is hidden by setting
// the CSSStyleDeclaration property directly instead.
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) el.style.setProperty("display", "none", "important");
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

// T-901's rendererVersion feature flag: setting it before the app boots
// (store.ts reads it once, at construction) lands the Graph view on
// TopologyCanvasV2 with no extra click needed beyond the Switch/Graph
// segmented toggle (see web/e2e/scale.spec.ts's identical helper).
async function enableV2Renderer(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("vnprox.topology.rendererV2", "v2");
    } catch {
      /* private-mode localStorage: the flag simply won't stick */
    }
  });
}

async function logIn(page: Page): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await enableV2Renderer(page);
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();
}

test("v2 canvas: the minimap renders and dragging its viewport rectangle pans the main canvas", async ({ page }) => {
  await logIn(page);

  const region = page.getByRole("application", { name: /Topology map/ });
  // Wait for the topology to have actually projected before reading its
  // proxy layout (same readiness signal scale.spec.ts's v2 case uses).
  await expect(region.getByRole("button", { name: /bridge vmbr0/ }).first()).toBeVisible({ timeout: 15_000 });

  const minimap = page.getByTestId("topology-minimap");
  await expect(minimap).toBeVisible();

  // Scroll it into view BEFORE any coordinate is captured, and note why:
  // T-2505-followup-01's fix gave the map container a `min-h-[22rem]` floor,
  // which deliberately trades "the map is squeezed to zero" for "the page
  // scrolls" (TopologyPage.tsx documents the trade). So with enough banners
  // the minimap — pinned to the map's bottom-right, not the viewport's —
  // sits below the fold. `page.mouse` takes VIEWPORT coordinates and does
  // not auto-scroll the way `locator.dragTo()` would, so without this the
  // drag below lands on nothing and the test fails with `moved === false`,
  // reporting a broken minimap when the minimap is fine.
  //
  // It has to happen before `before` is captured, or the scroll itself moves
  // the proxy and the assertion passes for the wrong reason.
  await minimap.scrollIntoViewIfNeeded();

  // Capture one proxy's on-screen position before the drag.
  const proxy = region.getByRole("button", { name: /bridge vmbr0/ }).first();
  const before = await proxy.boundingBox();
  expect(before).not.toBeNull();
  if (!before) return;

  const box = await minimap.boundingBox();
  expect(box).not.toBeNull();
  if (!box) return;

  // Drag from the minimap's current viewport-rectangle position to a
  // different corner of the minimap — this recenters the main canvas on a
  // different graph point (minimap.ts's panFromMinimapPoint), which must
  // move every on-screen entity, including the one captured above.
  const startX = box.x + box.width * 0.5;
  const startY = box.y + box.height * 0.5;
  const endX = box.x + box.width * 0.85;
  const endY = box.y + box.height * 0.15;

  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(endX, endY, { steps: 8 });
  await page.mouse.up();

  const after = await proxy.boundingBox();
  expect(after).not.toBeNull();
  if (!after) return;

  const moved = Math.abs(after.x - before.x) > 5 || Math.abs(after.y - before.y) > 5;
  expect(moved).toBe(true);
});

test("v2 canvas: zooming out engages the physical-layer capsule band (LOD)", async ({ page }) => {
  await logIn(page);

  const region = page.getByRole("application", { name: /Topology map/ });
  await expect(region.getByRole("button", { name: /bridge vmbr0/ }).first()).toBeVisible({ timeout: 15_000 });

  // Before zooming out, individual physnic proxies exist (full detail).
  await expect(region.getByRole("button", { name: /^physnic / }).first()).toBeVisible();

  const canvas = page.getByTestId("topology-canvas-v2");
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  if (!box) return;
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);

  // Enough wheel ticks to cross the capsule-band threshold (lod.ts:
  // zoom < 0.2), per lod.render.test.tsx's identical math (~17 ticks from
  // zoom=1; 25 gives comfortable real-browser margin).
  for (let i = 0; i < 25; i++) {
    await page.mouse.wheel(0, 120);
  }

  await expect(region.getByRole("button", { name: /phys-capsule/ }).first()).toBeVisible({ timeout: 10_000 });
  await expect(region.getByRole("button", { name: /^physnic / })).toHaveCount(0);
});
