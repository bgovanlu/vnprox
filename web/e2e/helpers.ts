// Shared across every spec that drives the Topology page's elk pan/zoom
// canvas. 67fff26 landed the switch-faceplate view (SwitchView.tsx) as
// /topology's default (store.ts: `viewMode: "switch"`), with the pre-
// existing React Flow graph now behind the Switch/Graph segmented toggle
// (ViewModeToggle.tsx — role="radiogroup" containing two role="radio"
// controls labeled "Switch"/"Graph"). `.react-flow`/`.react-flow__pane`/
// `.react-flow__node` no longer exist in the DOM until a test explicitly
// selects "Graph" — every spec that queries those selectors, clicks
// "fit view", pans/zooms, or right-clicks a canvas node for its context
// menu needs to call this first.
import { availableParallelism } from "node:os";

import { expect, type Page } from "@playwright/test";

import { coresFactor } from "../perf/budgets";

/** Selects the Graph segment of Topology's view-mode toggle and waits for
 * the React Flow canvas to mount. Assumes the page is already on
 * /topology (or navigating there — the toggle itself renders regardless of
 * load state, per TopologyPage.tsx). */
export async function switchToGraphView(page: Page): Promise<void> {
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.locator(".react-flow")).toBeVisible();
}

// T-2505's deadline ladder (web/playwright.config.ts's own `slowFactor`),
// applied here too: waitForLayoutSettled below has its own bounded timeout,
// separate from `expect.timeout`, and it needs the same normalisation for a
// small machine every other deadline in this suite already gets.
const slowFactor = coresFactor(availableParallelism());

export interface WaitForLayoutSettledOptions {
  /** Layout is not even considered "started" below this many rendered
   * nodes. Matches whichever fixture the caller's spec runs against
   * (sim-lab.yaml's 4-node minimum vs. three-node-vlan.yaml's 10+). */
  minNodes?: number;
  /** Fraction of rendered nodes whose transform must differ from the rest
   * before layout is considered to have started diverging from the
   * pre-elkjs stacked-at-the-origin state — e.g. 0 means "at least 2 of
   * them differ, however many nodes there are" (sim-lab's small maps), 0.5
   * means "over half of them differ" (the larger three-node-vlan maps).
   * Purely a "has elkjs's first pass actually run" gate; the real settle
   * condition is `stableFrames` below. */
  minDivergedFraction?: number;
  /** Consecutive requestAnimationFrame ticks the full set of node
   * transforms must be byte-identical across before layout counts as
   * SETTLED, not just started. */
  stableFrames?: number;
  /** Overall bound on the wait. Already scaled for a small machine by
   * `slowFactor` unless the caller overrides it. Defaults to 30s (scaled)
   * — Playwright's own unwritten default for `page.waitForFunction` — so
   * this fix tightens the *condition*, not the *budget*. */
  timeout?: number;
}

/** Polls `.react-flow__node` transforms once per animation frame and
 * resolves only once layout has both STARTED (see `minDivergedFraction`)
 * and SETTLED — the same set of transforms held steady for `stableFrames`
 * consecutive frames.
 *
 * T-3713: every caller of this used to stop at "started" alone
 * (`nodes.length >= N && transforms.size > 1`), which is true the instant
 * elkjs's layout begins to diverge from the origin, not once it has
 * finished moving nodes into place. Nodes render with `transition-opacity`
 * (EntityNode.tsx), so a click that lands on "started" can land mid-
 * animation and burn its own 5s click-stability budget waiting for the
 * node to stop moving — measured at a ~33% failure rate in
 * simulator.spec.ts, with the offending runs taking 33.7s/1.2m/1.2m against
 * 5-7s for settled siblings. Requiring N consecutive stable rAF ticks fixes
 * the race without widening any budget: a layout that never settles still
 * times out via `timeout` below, it just no longer reports "ready" while
 * still moving. */
export async function waitForLayoutSettled(page: Page, options: WaitForLayoutSettledOptions = {}): Promise<void> {
  const minNodes = options.minNodes ?? 4;
  const minDivergedFraction = options.minDivergedFraction ?? 0;
  const stableFrames = options.stableFrames ?? 8;
  const timeout = options.timeout ?? 30_000 * slowFactor;

  await page.waitForFunction(
    ({ minNodes, minDivergedFraction, stableFrames }) => {
      interface SettleState {
        transform: string;
        frames: number;
      }
      const w = window as unknown as { __vnproxLayoutSettle?: SettleState };

      const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
      const values = nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : ""));
      const distinct = new Set(values).size;
      // Reproduces the pre-T-3713 "started" thresholds exactly: fraction 0
      // requires >1 distinct transform regardless of node count (the small
      // sim-lab maps' old `transforms.size > 1`); fraction 0.5 requires
      // strictly more than half the nodes to differ (the larger maps' old
      // `transforms.size > nodes.length / 2`).
      const minDistinct = Math.max(2, Math.floor(nodes.length * minDivergedFraction) + 1);

      if (nodes.length < minNodes || distinct < minDistinct) {
        w.__vnproxLayoutSettle = undefined;
        return false;
      }

      const transform = values.join("|");
      if (w.__vnproxLayoutSettle?.transform !== transform) {
        w.__vnproxLayoutSettle = { transform, frames: 1 };
        return false;
      }
      w.__vnproxLayoutSettle.frames += 1;
      return w.__vnproxLayoutSettle.frames >= stableFrames;
    },
    { minNodes, minDivergedFraction, stableFrames },
    { polling: "raf", timeout },
  );
}
