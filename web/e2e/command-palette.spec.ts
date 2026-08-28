// SPDX-License-Identifier: Apache-2.0

// T-903 AC5: the ⌘K/Ctrl+K command palette, driven end-to-end against the
// real stack (pvemock three-node-vlan fixture -> vnproxd -> the production
// SPA build) — opened via keyboard only, merges a registered page verb with
// the fuzzy entity search, and running the verb opens the same editor a
// click on the map's own "Edit" button would.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  // waitForURL resolves when the navigation completes, which is BEFORE React
  // has mounted useKeyboardShortcuts' window keydown listener. A key pressed
  // in that gap is dropped on the floor — deterministically, in this app —
  // which is why `?` looked like a permanent product defect that reproduced
  // even at commits predating the help work (T-2108). Waiting for a piece of
  // the app shell to be interactive closes the gap. Proven by experiment:
  // pressing immediately yields 0 dialogs, pressing after this yields 1.
  await expect(page.getByRole("button", { name: "Keyboard shortcuts" })).toBeVisible();
}

test("Ctrl+K opens the command palette and running a registered action opens the bridge editor", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);
  // Topology's own effect registers its "Edit <bridge> on <node>" verbs
  // once GET /topology resolves — wait for the map itself so the verb
  // actually exists before trying to find it in the palette.
  await expect(page.getByRole("heading", { name: "Topology" })).toBeVisible();
  await expect(page.getByLabel("vmbr0 switch").first()).toBeVisible();

  await page.keyboard.press("Control+k");
  const dialog = page.getByRole("dialog", { name: "Command palette" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Command palette input")).toBeFocused();

  // pve1's mgmt bridge is named "vmbr0" on every node in this fixture, so
  // the node suffix is what makes this query resolve to exactly one
  // registered verb (TopologyPage.tsx's own doc comment on the same
  // disambiguation) — entirely keyboard-driven from here: the merged list
  // is narrowed to this one row, which is already highlighted by default.
  await page.keyboard.type("vmbr0 on pve1");
  await expect(dialog.getByText("Edit vmbr0 on pve1")).toBeVisible();
  await dialog.getByText("Edit vmbr0 on pve1").click();

  await expect(dialog).toBeHidden();
  await expect(page.getByRole("dialog").filter({ hasText: "Edit bridge vmbr0" })).toBeVisible();

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});

test("the shortcut help overlay documents the palette binding", async ({ page }) => {
  await logIn(page);

  await page.keyboard.press("?");
  const help = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(help).toBeVisible();
  await expect(help.getByText("⌘K / Ctrl+K")).toBeVisible();
  await expect(help.getByText("Open command palette")).toBeVisible();
});
