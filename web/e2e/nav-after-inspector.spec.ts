// T-2003-bug-01 regression: nav-rail navigation must keep working from the
// Topology page — including after the entity inspector has been opened and
// dismissed.
//
// The defect as originally reported: spotlight search → open an inspector →
// Escape → click any nav-rail link. The URL changed, the rendered page did
// not. It survived because nothing ran this suite (T-1806-bug-01) and the
// one spec that crossed this path asserted on text loose enough to match the
// *stale* page it should have navigated away from.
//
// **The reported reproduction named the wrong trigger** (settled 2026-08-07,
// under T-2108). The inspector is irrelevant. Bisecting the four
// combinations showed Switch-view + inspector navigates fine and Graph-view
// with *no* inspector at all is broken, so the precondition is simply "the
// Graph view is mounted" — which is why the first regression spec written
// for this card passed while the bug was still live: it never left the
// Switch view. Cause: `HistoryTimeline` (Graph-view-only) received a
// freshly-built `liveFlowRecords` array on every render from
// `useLiveFlowRecords(false)`, re-fired the effect that calls back into
// TopologyPage's `setPlayback`, and looped forever — starving the
// `startTransition` React Router v7 wraps every location update in. Fixed in
// src/flows/flowsQueries.ts, with unit-level pins in
// flowsQueries.stability.test.tsx and HistoryTimeline.test.tsx.
//
// Two rules this file follows, both learned from that:
//
//  1. Every navigation assertion checks a **heading** of the destination AND
//     that the origin's heading is gone. A test that passes because the app
//     did not navigate is the exact failure mode that hid this bug.
//  2. No step is conditional. A `if (await x.isVisible())` guard turns a
//     regression into a silent skip, which is the same failure wearing a
//     different hat.
import { expect, test, type Page } from "@playwright/test";
import { waitForLayoutSettled } from "./helpers";

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
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Asserts we really are on the named page: its heading is present, and
 * Topology's is not. */
async function expectOnPage(page: Page, heading: string): Promise<void> {
  await expect(page.getByRole("heading", { name: heading, exact: true }).first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Topology", exact: true })).toHaveCount(0);
}

/** Opens spotlight from the top bar (the affordance that works from any
 * page), searches, and opens the first result's inspector. */
async function openInspectorViaSpotlight(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Search", exact: true }).click();

  const spotlight = page.getByRole("dialog");
  await expect(spotlight).toBeVisible();
  await spotlight.getByPlaceholder("web01, 192.168.1.10, aa:bb:cc:...").fill("vmbr0");

  // Unconditional: if the fixture stops returning a match, this fails loudly
  // rather than skipping the whole point of the test.
  const firstResult = spotlight.locator("li button").first();
  await expect(firstResult).toBeVisible();
  await firstResult.click();

  // Selecting a result closes spotlight and opens the inspector, which is a
  // dialog whose accessible name is the entity itself. Asserting on THAT
  // name — rather than on "some dialog is visible" — is what distinguishes
  // "the inspector opened" from "spotlight has not closed yet".
  await expect(page.getByRole("dialog", { name: "vmbr0" })).toBeVisible();
}

test("nav-rail still navigates after the inspector is opened and dismissed", async ({ page }) => {
  await logIn(page);
  await openInspectorViaSpotlight(page);

  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "vmbr0" })).toHaveCount(0);

  // The regression itself: a nav-rail link must change both the URL and
  // what is rendered.
  await page.getByRole("link", { name: "Audit" }).click();
  await page.waitForURL("**/audit");
  await expectOnPage(page, "Audit");

  // And it must keep working, not just once.
  await page.getByRole("link", { name: "IPAM" }).click();
  await page.waitForURL("**/ipam");
  await expectOnPage(page, "IPAM");
});

/** Switches to the Graph view and waits for the async elkjs layout to have
 * SETTLED — the same readiness signal topology.spec.ts uses (helpers.ts's
 * waitForLayoutSettled — T-3713). Mounting React Flow is the real
 * precondition for this bug, so a spec that clicks "Graph" and navigates
 * immediately could pass without ever having reproduced it. */
async function waitForGraphLayout(page: Page): Promise<void> {
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.locator(".react-flow")).toBeVisible();
  await waitForLayoutSettled(page, { minNodes: 10, minDivergedFraction: 0.5 });
}

test("nav-rail still navigates once the Graph view is mounted, with no inspector involved", async ({ page }) => {
  await logIn(page);
  await waitForGraphLayout(page);

  // No dialog is opened anywhere in this test. Before the fix this failed
  // exactly as the card describes — URL updated, page did not — which is how
  // the inspector was ruled out as the cause.
  await page.getByRole("link", { name: "Guests" }).click();
  await page.waitForURL("**/guests");
  await expectOnPage(page, "Guests");

  // Return to Topology, re-mount the graph, and leave again: the loop was
  // re-armed every time the Graph view mounted, so one successful departure
  // is not enough to call it fixed.
  await page.getByRole("link", { name: "Topology" }).click();
  await page.waitForURL("**/topology");
  await waitForGraphLayout(page);
  await page.getByRole("link", { name: "Audit" }).click();
  await page.waitForURL("**/audit");
  await expectOnPage(page, "Audit");
});
