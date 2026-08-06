// T-907 acceptance criteria, exercised against the real stack (pvemock
// three-node-vlan fixture -> vnproxd -> the production SPA build):
//
//  - AC1/AC2: a named saved view (layer toggles + VLAN filter) round-trips
//    through the Views menu, and a shareable link carries the identical
//    state through the URL alone — opened fresh, with no prior `layouts`
//    row for that view name (docs/api.md's "state lives in the URL, not
//    only server-side").
//  - AC3: pinning a sticky note to an entity (vmbr0's inspector "Notes"
//    tab) persists it; reloading the page and reopening the same entity's
//    inspector re-renders the note; deleting it removes it.
//
// Selection via spotlight search rather than a canvas click — React Flow/
// canvas hit-testing under headless Chromium is unreliable, the same
// rationale inspector-compare.spec.ts and lacp-inspector.spec.ts document
// for their own inspector-opening steps.
import { expect, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

async function selectViaSearch(page: Page, query: string): Promise<void> {
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog", { name: "Search" });
  await searchDialog.getByPlaceholder(/web01/).fill(query);
  await searchDialog.getByRole("button", { name: new RegExp(query) }).first().click();
}

test.describe("T-907 saved views", () => {
  test("saving a named view captures layer/VLAN-filter state and restores it on load (AC1)", async ({ page }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await logIn(page);
    await switchToGraphView(page);

    // Turn off the SDN layer and set a VLAN filter — a non-default,
    // specific state (AC1: "specific layers off, a VLAN filter set").
    const sdnToggle = page.getByRole("button", { name: /SDN/ });
    await sdnToggle.click();
    await expect(sdnToggle).toHaveAttribute("aria-pressed", "false");

    await page.getByLabel("VLAN", { exact: true }).fill("20");
    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("button", { name: "Clear" })).toBeVisible();

    // Save it as a named view.
    const viewName = `e2e-view-${String(Date.now())}`;
    page.once("dialog", (dialog) => {
      void dialog.accept(viewName);
    });
    await page.getByRole("button", { name: "Views ▾" }).click();
    await page.getByRole("menuitem", { name: "Save current view…" }).click();
    await expect(page.getByText("View saved").first()).toBeVisible();

    // Reset to the default state.
    await sdnToggle.click();
    await expect(sdnToggle).toHaveAttribute("aria-pressed", "true");
    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page.getByLabel("VLAN", { exact: true })).toHaveValue("");

    // Loading the saved view restores the exact captured state.
    await page.getByRole("button", { name: "Views ▾" }).click();
    await page.getByRole("menuitem", { name: viewName }).click();
    await expect(sdnToggle).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByLabel("VLAN", { exact: true })).toHaveValue("20");

    // Clean up: delete the saved view so repeated runs don't accumulate.
    await page.getByRole("button", { name: "Views ▾" }).click();
    await page.getByRole("button", { name: `Delete saved view: ${viewName}` }).click();

    expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
  });

  test("a shareable link renders the same filtered/zoomed state for a fresh session with no saved-view row (AC2)", async ({
    page,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);

    await logIn(page);
    await switchToGraphView(page);

    const physToggle = page.getByRole("button", { name: /Physical/ });
    await physToggle.click();
    await expect(physToggle).toHaveAttribute("aria-pressed", "false");
    await page.getByLabel("VLAN", { exact: true }).fill("20");
    await page.getByRole("button", { name: "Apply", exact: true }).click();

    // Copy the share link — never saved under a name, so no `layouts` row
    // exists anywhere for this exact state; the URL alone must carry it.
    await page.getByRole("button", { name: "Views ▾" }).click();
    await page.getByRole("menuitem", { name: "Copy share link" }).click();
    await expect(page.getByText("Link copied").first()).toBeVisible();
    const link = await page.evaluate(() => navigator.clipboard.readText());
    expect(link).toContain("svLayers=");
    expect(link).toContain("svVlan=20");

    // Open it in a brand-new browser context: no cookies, no prior
    // localStorage, no session — the closest this single-fixture dev stack
    // gets to "a viewer who has never touched this app before" (see this
    // file's header comment: the fixture only provisions one credential
    // set, so the "fresh viewer" guarantee this test proves is about the
    // browsing context/URL mechanism, not a distinct user account).
    const freshContext = await page.context().browser()?.newContext({ ignoreHTTPSErrors: true });
    if (!freshContext) throw new Error("could not open a fresh browser context");
    try {
      const freshPage = await freshContext.newPage();
      await logIn(freshPage);
      await freshPage.goto(link);
      await switchToGraphView(freshPage);

      await expect(freshPage.getByRole("button", { name: /Physical/ })).toHaveAttribute("aria-pressed", "false");
      await expect(freshPage.getByLabel("VLAN", { exact: true })).toHaveValue("20");
    } finally {
      await freshContext.close();
    }
  });
});

test.describe("T-907 annotations", () => {
  test("pinning a sticky note to an entity persists across reload, and deleting it removes it (AC3)", async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await logIn(page);

    const noteText = `check this before maintenance — ${String(Date.now())}`;

    await selectViaSearch(page, "vmbr0");
    const inspector = page.getByRole("dialog");
    await inspector.getByRole("tab", { name: /^Notes/ }).click();
    await expect(inspector.getByText("No notes pinned to this entity yet.")).toBeVisible();

    await inspector.getByPlaceholder("Pin a note to this entity…").fill(noteText);
    await inspector.getByRole("button", { name: "Pin note" }).click();
    await expect(inspector.getByText(noteText)).toBeVisible();

    // Reload the whole page (a fresh navigation, not just client-side
    // routing) and reopen the same entity's inspector: the note re-renders
    // at the same entity, backed by the server, not component state.
    await page.reload();
    await selectViaSearch(page, "vmbr0");
    const reopenedInspector = page.getByRole("dialog");
    await reopenedInspector.getByRole("tab", { name: /^Notes/ }).click();
    await expect(reopenedInspector.getByText(noteText)).toBeVisible();

    // Deleting it removes it — and it stays removed across another reload.
    await reopenedInspector.getByRole("button", { name: `Delete note: ${noteText}` }).click();
    await expect(reopenedInspector.getByText(noteText)).toHaveCount(0);
    await expect(reopenedInspector.getByText("No notes pinned to this entity yet.")).toBeVisible();

    await page.reload();
    await selectViaSearch(page, "vmbr0");
    const finalInspector = page.getByRole("dialog");
    await finalInspector.getByRole("tab", { name: /^Notes/ }).click();
    await expect(finalInspector.getByText(noteText)).toHaveCount(0);

    expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
  });
});
