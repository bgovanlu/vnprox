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
import { expect, type Page } from "@playwright/test";

/** Selects the Graph segment of Topology's view-mode toggle and waits for
 * the React Flow canvas to mount. Assumes the page is already on
 * /topology (or navigating there — the toggle itself renders regardless of
 * load state, per TopologyPage.tsx). */
export async function switchToGraphView(page: Page): Promise<void> {
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.locator(".react-flow")).toBeVisible();
}
