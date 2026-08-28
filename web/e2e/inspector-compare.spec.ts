// SPDX-License-Identifier: Apache-2.0

// T-908 acceptance criterion 4: against the real stack (pvemock
// three-node-vlan fixture -> vnproxd -> the production SPA build), open the
// bond0 inspector, pin it, select a second node's bond0, and assert both
// remain visible with distinct per-pane metrics wiring.
//
// Selection via spotlight search rather than a canvas click — React Flow
// node hit-testing under headless Chromium is unreliable, same rationale
// lacp-inspector.spec.ts and changesets.spec.ts's read-only-capability test
// document for their own inspector-opening steps. Pinning switches the
// inspector from a modal Radix Dialog to a non-modal region specifically so
// the Search button (behind a modal's full-screen overlay otherwise) stays
// reachable for the second selection — see InspectorStack.tsx's doc
// comment.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

async function selectViaSearch(page: Page, query: string, resultIndex: number): Promise<void> {
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog", { name: "Search" });
  await searchDialog.getByPlaceholder(/web01/).fill(query);
  await searchDialog.getByRole("button", { name: new RegExp(query) }).nth(resultIndex).click();
}

test("pinning bond0's inspector and selecting a second node's bond0 opens both, compared side by side", async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);

  // Open pve1's bond0 (the default single, modal inspector — unchanged
  // pre-T-908 look).
  await selectViaSearch(page, "bond0", 0);
  const firstInspector = page.getByRole("dialog");
  await expect(firstInspector.getByRole("button", { name: "Pin" })).toBeVisible();

  // Pin it: the inspector switches to a non-modal region (its Close button
  // now appears — modal Drawers close via Escape/overlay instead).
  await firstInspector.getByRole("button", { name: "Pin" }).click();
  const pane1 = page.getByRole("region", { name: /Inspector panes \(1\)/ });
  await expect(pane1.getByRole("button", { name: "Pinned" })).toBeVisible();

  // Select a second node's bond0 without ever closing the first — the pin
  // is what makes this additive instead of replacing.
  await selectViaSearch(page, "bond0", 1);

  const panes = page.getByRole("region", { name: /Inspector panes \(2\)/ });
  await expect(panes).toBeVisible();

  // Both bond0 panes are visible: either the aligned compare grid (both are
  // bonds, so same-kind) or, degrading honestly, two independent panes —
  // either way, never a broken layout.
  // Unconditional. Both selections are bond0 — same kind — so the aligned
  // compare grid is the ONE outcome this scenario has, and asserting it
  // directly is what makes the test able to fail. The previous form waited
  // for `compare.or(mismatch)` and then branched on a second, separately
  // evaluated `compare.isVisible()`; under load that second read could land
  // while the panes were still settling, sending the test down the
  // mismatched-kind branch to assert on an element that was never going to
  // exist. A conditional step turns a regression into a silent skip — and
  // here it turned a timing wobble into a confusing failure about
  // mismatched kinds. The mismatched-kind fallback copy is covered where it
  // belongs, as a pure render: src/topology/InspectorCompareView.test.tsx.
  const compare = panes.getByTestId("inspector-compare");
  await expect(compare).toBeVisible();

  const closeButtons = panes.getByRole("button", { name: /^Close bond0/ });

  {
    // Same-kind aligned compare: one shared Metrics tab, two independently
    // mounted MetricsTab instances underneath (docs/features/monitoring.md
    // §1-2's per-entity live rate + history) — distinct per-pane wiring,
    // not one shared dataset. bond0 exists on pve1/pve2/pve3, so the two
    // headings are guaranteed to differ by node even though the label
    // ("bond0") is the same on every node.
    await compare.getByRole("tab", { name: "Metrics" }).click();
    const headings = await compare.getByRole("heading", { level: 3 }).allTextContents();
    expect(headings).toHaveLength(2);
    expect(headings[0]).not.toEqual(headings[1]);
  }

  // Closing one pane leaves the other intact (acceptance criterion 1).
  await expect(closeButtons).toHaveCount(2);
  await closeButtons.first().click();
  await expect(page.getByTestId("inspector-compare")).toHaveCount(0);
  await expect(page.getByTestId("inspector-compare-mismatch")).toHaveCount(0);
  // One inspector (the pane that wasn't closed) is still open.
  await expect(page.getByText("bond0").first()).toBeVisible();

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
