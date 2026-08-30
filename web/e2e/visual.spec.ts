// SPDX-License-Identifier: Apache-2.0

// T-4210: the visual regression gate. Screenshots every routed page (see
// routeInventory.ts) in light theme, dark theme, and demo-accent mode, so
// Phases 42-51's restyle of the whole product (planning/roadmap-visual.md)
// has something that fails loudly on an accidental regression instead of
// only on someone happening to notice. Read docs/development.md's "Visual
// regression gate" section before touching this file — it documents the
// baseline-generation workflow this suite depends on.
//
// THIS SUITE ONLY TAKES REAL SCREENSHOTS UNDER ITS OWN DEDICATED HARNESS.
// web/e2e/shards.ts's validateManifest() requires every *.spec.ts file on
// disk to belong to a shard (web/e2e/shards.json), so this file has a
// placeholder entry there — but running it *through* that config, inside a
// shard shared with specs that mutate app state (changesets, muted
// findings, pinned layouts...), would make every baseline race whatever
// else that shard's other files did first, and would boot every stack
// every OTHER file in that shard needs just to run this one. Neither is
// acceptable for a screenshot gate. So every test in this file skips,
// cheaply and without opening a browser context, unless
// VNPROX_VISUAL_SNAPSHOTS=1 is set — which playwright.visual.config.ts (the
// suite's own, single-stack, single-purpose config) sets for itself. Run it
// with:
//
//   npx playwright test --config=playwright.visual.config.ts
//   npx playwright test --config=playwright.visual.config.ts --update-snapshots
//
// or `scripts/ci-local.sh visual`, which does both npm/chromium setup and
// the SPA build vnproxd needs to embed first.
import { forceDemoAccent, forceTheme } from "./helpers";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import {
  HEADING_LEVEL_OVERRIDES,
  pathEndRegExp,
  routedPagePaths,
  slugifyRoute,
} from "./routeInventory";

const RUNNING_STANDALONE = process.env.VNPROX_VISUAL_SNAPSHOTS === "1";

// --- determinism helpers ---------------------------------------------------
//
// "Determinism is the whole game" (T-4210's task card). Each helper below
// neutralizes exactly one class of nondeterminism; see visualTest's own
// comments for what is deliberately NOT neutralized and why.

/** Mirrors a11y.spec.ts's own local suppressOnboardingWalkthrough: the
 * onboarding walkthrough overlay (aria-label "Onboarding walkthrough") is
 * unrelated content that can render on top of a freshly-logged-in session
 * and would otherwise land differently across runs depending on exactly
 * when it mounts relative to the screenshot. Not imported from a11y.spec.ts
 * — that function is private to that file, and duplicating six lines here
 * is cheaper than exporting a helper whose only other caller is a different
 * test suite with different determinism needs. */
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector(
        '[aria-label="Onboarding walkthrough"]',
      );
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
    };
    const start = () => {
      try {
        suppress();
        new MutationObserver(suppress).observe(document.documentElement, {
          childList: true,
          subtree: true,
        });
      } catch {
        setTimeout(start, 0);
      }
    };
    start();
  });
}

/** The Graph view's two implementations. See docs/design-language.md §8.0:
 * v1 (xyflow + DOM nodes) is what users get by default; v2 (a real `<canvas>`)
 * is opt-in. */
type Renderer = "v1" | "v2";

/** T-4305 AC4: selects the Graph view's renderer before the app boots.
 *
 * `store.ts` reads this key exactly once, at store construction, so it has to
 * be set via `addInitScript` rather than after navigation. v1 is the default
 * and needs no flag — deliberately set to nothing rather than to `"v1"`, so
 * the v1 capture exercises the same absent-key path a real user has. */
async function forceRenderer(page: Page, renderer: Renderer): Promise<void> {
  if (renderer !== "v2") return;
  await page.addInitScript(() => {
    localStorage.setItem("vnprox.topology.rendererV2", "v2");
  });
}

/** Never lets a live WebSocket connection reach the daemon. src/api/ws.ts's
 * live channel pushes exactly the class of thing a screenshot gate cannot
 * tolerate mid-capture: throughput counters, new findings, collector-cycle
 * updates. Closing the mock route without ever calling
 * `ws.connectToServer()` is the same "server unreachable" shape the app
 * already has to tolerate on a flaky network — this doesn't exercise a
 * degraded path the product doesn't already handle, it just makes that path
 * the deterministic one for every capture. */
async function blockLiveUpdates(page: Page): Promise<void> {
  await page.routeWebSocket(/\/api\/ws/, async (ws) => {
    await ws.close();
  });
}

/** Scrollbar suppression now happens at the browser, not in the page — see
 * playwright.visual.config.ts's `--hide-scrollbars`. This helper used to
 * inject the CSS with `page.addStyleTag`, and vnproxd's Content Security
 * Policy (`style-src 'self'`) refused it, failing all 96 captures on this
 * suite's first real run. Keeping a no-op wrapper would only hide where the
 * behaviour moved to, so it is gone and its call sites are gone with it. */

/** Mirrors a11y.spec.ts's own waitForLoadingPlaceholderToClear: several
 * pages' data-loading placeholders ("Loading SDN configuration…",
 * "Simulating…") are a real, timing-dependent piece of UI this suite must
 * not race — capturing mid-fetch would make the baseline describe a loading
 * spinner instead of the page it is meant to guard. */
async function waitForSteadyState(page: Page): Promise<void> {
  await expect(page.getByText(/^(Loading|Simulating)[….]/).first()).toHaveCount(
    0,
  );
}

// --- login + one-time cluster settle ---------------------------------------

const VISUAL_STORAGE_STATE = join(
  tmpdir(),
  `vnprox-e2e-visual-storage-state-${String(process.pid)}.json`,
);

/** Waits, once, for the fixture's cross-node staleness to reach its
 * permanent state before any test takes a screenshot.
 *
 * three-node-vlan.yaml's pve2/pve3 peers are genuinely unreachable in this
 * sandbox (see a11y.spec.ts's neutralizeFaceplateDimming comment for the
 * full story), so their collectors go stale within roughly a minute of boot
 * — a ONE-WAY transition from "fresh" to "permanently stale" for the rest
 * of the daemon's life, never flapping back. Left unhandled, that is a real
 * source of nondeterminism this task's card explicitly calls out
 * ("determinism is the whole game"): whether any given page happens to be
 * captured before or after that transition would depend on how far into
 * the run it sits, which would make the exact same page flake between the
 * two runs T-4210's own determinism proof requires. Paying for the wait
 * ONCE here, before the parameterised sweep starts, means every subsequent
 * capture — on every route, on both the baseline-generating run and the
 * verification run — measures the same (already-settled) steady state
 * instead of racing a clock that ticks differently every time.
 *
 * Best-effort: not every fixture necessarily reaches this (and a future
 * fixture swap should not be able to hang this suite's beforeAll forever),
 * so a timeout here is swallowed rather than propagated.
 */
async function settleClusterStaleness(page: Page): Promise<void> {
  await page.goto("/topology");
  await page
    .locator(".grayscale")
    .first()
    .waitFor({ state: "visible", timeout: 120_000 })
    .catch(() => {
      // Nothing to settle on this fixture, or it never reached staleness in
      // the allotted time — either way, every capture below still measures
      // whatever singular state exists at that point, so this is not fatal.
    });
}

test.describe("T-4210: visual regression, every routed page x light/dark/demo", () => {
  // Skips the whole file, before any fixture (browser context, page) is
  // created, unless the dedicated visual harness set the marker env var —
  // see this file's header comment for why sharing web/e2e/shards.ts's
  // default stack with other specs is not safe for a screenshot gate.
  test.skip(
    !RUNNING_STANDALONE,
    "Visual regression suite only runs under its own harness: " +
      "npx playwright test --config=playwright.visual.config.ts (or scripts/ci-local.sh visual).",
  );

  test.use({ storageState: VISUAL_STORAGE_STATE });

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext({
      ignoreHTTPSErrors: true,
      storageState: undefined,
    });
    const page = await context.newPage();
    await page.goto("/login");
    await page.getByLabel("Username").fill("root");
    await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
    await page.getByLabel("Realm").fill("pam");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/topology");
    await settleClusterStaleness(page);
    await context.storageState({ path: VISUAL_STORAGE_STATE });
    await context.close();
  });

  interface Mode {
    readonly name: "light" | "dark" | "demo";
    readonly theme: "light" | "dark";
    readonly demo: boolean;
  }

  // Three captures per page, matching the task card exactly — not a 2x2
  // cross product. Demo mode is captured on the dark theme (the app's own
  // default, src/store/theme.ts) rather than introducing a fourth
  // light+demo variant nobody asked for; which theme demo rides on is
  // otherwise arbitrary, so it is pinned explicitly here rather than left
  // to whatever the app's default happens to be, so a future default-theme
  // change can't silently change what this mode captures.
  const MODES: readonly Mode[] = [
    { name: "light", theme: "light", demo: false },
    { name: "dark", theme: "dark", demo: false },
    { name: "demo", theme: "dark", demo: true },
  ];

  for (const routePath of routedPagePaths()) {
    for (const mode of MODES) {
      const slug = `${slugifyRoute(routePath)}-${mode.name}`;

      test(`visual: ${routePath} (${mode.name})`, async ({
        page,
      }, testInfo) => {
        await page.emulateMedia({ reducedMotion: "reduce" });
        await forceTheme(page, mode.theme);
        if (mode.demo) {
          await forceDemoAccent(page);
        }
        await blockLiveUpdates(page);
        await suppressOnboardingWalkthrough(page);

        await page.goto(routePath);
        await expect(page).toHaveURL(pathEndRegExp(routePath));

        const level = HEADING_LEVEL_OVERRIDES[routePath] ?? 1;
        await expect(
          page.getByRole("heading", { level }).first(),
        ).toBeVisible();
        if (mode.theme === "dark") {
          await expect(page.locator("html")).toHaveClass(/dark/);
        } else {
          await expect(page.locator("html")).not.toHaveClass(/dark/);
        }
        if (mode.demo) {
          await expect(page.locator("html")).toHaveClass(/demo/);
        }

        await waitForSteadyState(page);
        await page.evaluate(() => document.fonts.ready);

        // T-4213: `--update-snapshots` only WRITES when the comparison
        // fails. A change smaller than the per-pixel threshold — a node fill
        // moving from #ecfdf5 to #ffffff, say — leaves every baseline
        // untouched and still reports "passed", which is indistinguishable
        // from a run that rewrote them all. Until that card lands, DELETE the
        // baselines before a regeneration run rather than trusting the flag
        // to overwrite them. This cost a wrong conclusion on 2026-08-28: the
        // stale file was read as evidence the change had not shipped.
        //
        // Deliverable 4: this suite deliberately does not commit baseline
        // PNGs (see docs/development.md) — Phases 42-51 restyle the whole
        // product and would invalidate every one of them on the first
        // card. `--update-snapshots` always writes/overwrites regardless of
        // what is on disk, so the skip below only applies to a plain
        // verification run with no baseline yet — it turns "every test
        // fails on a fresh checkout" into one clear, actionable message.
        const updating = testInfo.config.updateSnapshots !== "none";
        const snapshotName = `${slug}.png`;
        if (!updating && !existsSync(testInfo.snapshotPath(snapshotName))) {
          test.skip(
            true,
            `No baseline snapshot for ${routePath} (${mode.name}) yet. Generate baselines first: ` +
              "npx playwright test --config=playwright.visual.config.ts --update-snapshots",
          );
        }

        await expect(page).toHaveScreenshot(snapshotName, { fullPage: true });
      });
    }
  }

  // T-4216: the topology map, which the route table above does NOT capture.
  //
  // `src/topology/store.ts` defaults `viewMode` to "switch", and the loop
  // above navigates without clicking anything, so `topology-*.png` is the
  // switch faceplate. The graph canvas — the flagship visual, and the whole
  // subject of Phase 43 — had no baseline at all: T-4301 re-pointed every
  // colour that canvas draws with, and this suite could not have noticed.
  //
  // The view is selected with the product's own share-link deeplink
  // (`svView=graph`, src/topology/savedViews.ts) rather than by clicking the
  // Switch/Graph control. A click would make the capture depend on a control
  // label, so renaming a button would silently stop capturing the map — the
  // same class of silent-skip this card was opened for. `svLayers` is
  // mandatory: decodeViewFromSearch returns undefined without it and the
  // deeplink is ignored wholesale, which would leave this test quietly
  // screenshotting the switch view a second time.
  const GRAPH_DEEPLINK =
    "/topology?svLayers=phys,l2,sdn,guest&svView=graph&svZoom=1&svX=0&svY=0";

  // T-4305 AC4: "The visual gate captures the graph view in BOTH renderers,
  // or states in one place why one is enough. Capturing only the default hid
  // a whole renderer; capturing only v2 would hide the one users actually
  // get."
  //
  // Both, and not because the choice was hard. The two renderers have already
  // drifted from each other twice this phase in ways every green test missed:
  // T-4301 fixed three contrast failures in v2 while v1 kept its own copies,
  // and T-4302 found `STATUS_STROKE` living in three tables that disagreed
  // about what `ok` and `unknown` were. Both are exactly the kind of
  // divergence a screenshot catches and a unit test cannot, because the whole
  // claim is that the two renderers draw the SAME picture.
  //
  // Six captures rather than three. That is the cost of a rendering swap the
  // product ships as a user-flippable flag.
  const RENDERERS: readonly Renderer[] = ["v1", "v2"];

  for (const renderer of RENDERERS) {
    for (const mode of MODES) {
      test(`visual: /topology graph view ${renderer} (${mode.name})`, async ({
        page,
      }, testInfo) => {
        await page.emulateMedia({ reducedMotion: "reduce" });
        await forceTheme(page, mode.theme);
        await forceRenderer(page, renderer);
        if (mode.demo) {
          await forceDemoAccent(page);
        }
        await blockLiveUpdates(page);
        await suppressOnboardingWalkthrough(page);

        await page.goto(GRAPH_DEEPLINK);
        await expect(
          page.getByRole("heading", { level: 1 }).first(),
        ).toBeVisible();
        // Assert the deeplink actually took, rather than trusting it: a
        // silently-ignored parameter is indistinguishable from a working one in
        // a screenshot nobody has looked at yet, which is how the map came to
        // be missing from this suite in the first place.
        await expect(page.getByRole("radio", { name: "Graph" })).toBeChecked();
        await waitForSteadyState(page);
        await page.evaluate(() => document.fonts.ready);

        // T-4304's gate. Measured off the capture that opened that card, the
        // canvas got 235px of a 900px viewport — 26% — while three stacked
        // notice banners took 39%. A visual networking tool whose visual is
        // the smallest region on its own page.
        //
        // This asserts the share from the RENDERED box rather than from CSS,
        // because the thing that pushed the map down was three siblings above
        // it, none of which appears anywhere in the canvas's own styles. A
        // number, so it cannot regress back into an opinion.
        // `topology-map` is the shared wrapper around whichever renderer is
        // mounted. The first version of this asked for `topology-canvas-v2`,
        // which only exists behind the opt-in v2 flag — on the default page it
        // was never going to appear, and boundingBox() waited out the full
        // three-minute timeout instead of failing. Assert visibility first so a
        // wrong selector fails in seconds and says so.
        const mapLocator = page.getByTestId("topology-map");
        await expect(mapLocator).toBeVisible({ timeout: 10_000 });
        const canvasBox = await mapLocator.boundingBox();
        const viewport = page.viewportSize();
        expect(
          canvasBox,
          "topology map container must be laid out",
        ).not.toBeNull();
        expect(
          viewport,
          "visual suite runs at a fixed viewport",
        ).not.toBeNull();
        if (canvasBox !== null && viewport !== null) {
          const share = canvasBox.height / viewport.height;
          expect(
            share,
            `map is ${String(Math.round(share * 100))}% of the viewport`,
          ).toBeGreaterThan(0.5);
        }

        // The renderer is asserted, not assumed. `topology-canvas-v2` mounts
        // only behind the flag, so its presence/absence is the one signal that
        // says the localStorage key actually took — without this, a broken flag
        // would silently capture v1 twice and the suite would report six green
        // checks over three pictures.
        const v2Canvas = page.getByTestId("topology-canvas-v2");
        if (renderer === "v2") {
          await expect(v2Canvas).toBeVisible({ timeout: 10_000 });
        } else {
          await expect(v2Canvas).toHaveCount(0);
        }

        const updating = testInfo.config.updateSnapshots !== "none";
        const snapshotName = `topology-graph-${renderer}-${mode.name}.png`;
        if (!updating && !existsSync(testInfo.snapshotPath(snapshotName))) {
          test.skip(
            true,
            `No baseline snapshot for the topology graph view ${renderer} (${mode.name}) yet. Generate baselines first: ` +
              "npx playwright test --config=playwright.visual.config.ts --update-snapshots",
          );
        }
        await expect(page).toHaveScreenshot(snapshotName, { fullPage: true });
      });
    }
  }

  // /login is intentionally outside routedPagePaths() (see routeInventory.ts)
  // since it is the one route every other test's shared storage state
  // never actually visits — pre-auth chrome with its own layout, captured
  // in its own dedicated test rather than forced into the same table.
  for (const mode of MODES) {
    test(`visual: /login (${mode.name})`, async ({ browser }, testInfo) => {
      const context = await browser.newContext({
        ignoreHTTPSErrors: true,
        storageState: undefined,
      });
      const page = await context.newPage();
      await page.emulateMedia({ reducedMotion: "reduce" });
      await forceTheme(page, mode.theme);
      if (mode.demo) {
        await forceDemoAccent(page);
      }
      await blockLiveUpdates(page);

      await page.goto("/login");
      await expect(page.getByLabel("Username")).toBeVisible();
      await page.evaluate(() => document.fonts.ready);

      const updating = testInfo.config.updateSnapshots !== "none";
      const snapshotName = `login-${mode.name}.png`;
      if (!updating && !existsSync(testInfo.snapshotPath(snapshotName))) {
        test.skip(
          true,
          `No baseline snapshot for /login (${mode.name}) yet. Generate baselines first: ` +
            "npx playwright test --config=playwright.visual.config.ts --update-snapshots",
        );
      }
      await expect(page).toHaveScreenshot(snapshotName, { fullPage: true });
      await context.close();
    });
  }
});
