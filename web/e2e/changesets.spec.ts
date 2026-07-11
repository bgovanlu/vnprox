// T-207 acceptance criterion 1: scripted walkthrough of the editing UX
// against the real stack (pvemock three-node-vlan fixture -> vnproxd ->
// production SPA build), Playwright where feasible. What is and is not
// automatable in this environment, and why:
//
//  AUTOMATED here:
//   - Bridge editor incl. the VLAN-aware toggle + VID range editor,
//     drafting a bridge.create through the drawer.
//   - Draft survival across a full page reload (drawer re-hydrates from
//     the persisted active id + GET /changesets/{id}).
//   - Guest NIC list + bulk reattach (the fixture has 2 guests, not the
//     AC's illustrative 5 — both are bulk-moved; same code path, smaller N).
//   - Review screen: all three tabs (Summary op cards; File diff with the
//     real unified diff from GET /changesets/{id}/diff; Plan preview
//     mirroring BuildPlan) and the warnings-checkbox apply gate.
//   - Apply refusal for a changeset containing guest.nic.update: T-205's
//     documented executable-op boundary (guest/SDN-write/fw/ipam families
//     are refused with 422 unsupported_op before any mutation) surfaces
//     as a clear error instead of a partial apply.
//   - Apply of the node-file-only changeset, asserting the *success*
//     path: the daemon runs with the `dev_interfaces_dir` sandbox
//     (testdata/dev.toml, phase-2 audit F-22 remediation), so the stage
//     and reload steps run against var/dev-host with a no-op ifreload and
//     never touch the real machine — regardless of uid. The apply
//     therefore succeeds into the commit-confirm window, giving us the
//     full AC1 "apply -> countdown -> confirm -> committed" path plus
//     AC3 reload-survival, both WS-driven.
//
//  NOT automatable here (documented per the task card):
//   - Map drag-edit (AC1's "create bond from two NICs via drag") + snap-
//     back on validation failure: React Flow node-drag does not fire
//     reliably under headless Chromium (a synthesized pointer-drag on a
//     `.react-flow__node` does not consistently trigger d3-drag's
//     onNodeDragStop). The drag->op translation is unit-tested in
//     src/changesets/dragDropOps.test.ts (9 cases); the snap-back path
//     (draft op -> revalidate -> pop the erroring op) shares the exact
//     addOps/replaceOps flow this spec drives through the editors below.
//   - "Create bond from two NICs via drag" *succeeding*: needs a fixture
//     with unenslaved NICs; three-node-vlan deliberately has none.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page, username = "root", password = "vnprox-mock", realm = "pam"): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Realm").fill(realm);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Waits for the async elkjs layout to have spread the nodes out (see
 * topology.spec.ts's identical wait). */
async function waitForLayout(page: Page): Promise<void> {
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 10 && transforms.size > nodes.length / 2;
  });
}

test("T-207 walkthrough: bridge editor, reload survival, bulk guests, review tabs, apply outcomes", async ({ page }) => {
  await logIn(page);
  await waitForLayout(page);

  // --- 1. Map drag-edit + snap-back: NOT automated here -----------------
  // AC1's "create bond from two NICs via drag" and topology spec §2's
  // drag-drop edits are driven by React Flow's node-drag, which does not
  // fire reliably under headless Chromium (a synthesized pointer-drag on a
  // `.react-flow__node` does not consistently trigger d3-drag's
  // onNodeDragStop — no changeset POST is issued). Rather than ship a flaky
  // assertion, the drag→op translation and the snap-back-on-validation-
  // failure logic are covered exhaustively by unit tests instead:
  // src/changesets/dragDropOps.test.ts (9 cases: physnic→physnic new bond,
  // physnic→bond append, physnic/bond/vlan→bridge port-add, guest-nic→
  // bridge/VNet retarget, cross-node rejection, self-drop no-op). The
  // TopologyPage handler that consumes them (addOps → revalidate → pop the
  // erroring op on an error-severity finding) is the same addOps/replaceOps
  // path this walkthrough exercises below via the editors and bulk guests.
  const drawer = page.getByRole("region", { name: "Change drawer" });

  // --- 2. Bridge editor: create a VLAN-aware bridge with a VID range ----
  // The "New" menu is a Radix dropdown with a per-kind submenu of nodes
  // (docs/user-guide.md's "Node -> Bonds -> New"); hover the kind to open
  // its submenu, then pick the node.
  await page.getByRole("button", { name: "New ▾" }).click();
  await page.getByRole("menuitem", { name: "Bridge" }).hover();
  await page.getByRole("menuitem", { name: "pve1" }).click();
  await page.getByPlaceholder("vmbr1").fill("vmbr77");
  await page.getByLabel("VLAN aware").check();
  await page.getByPlaceholder("10-30, 100").fill("10-30");
  await page.getByRole("button", { name: "Add to changeset" }).click();
  await expect(drawer).toContainText("Create bridge vmbr77");
  await expect(page.getByText("Added to changeset").first()).toBeHidden({ timeout: 10_000 });

  // --- 3. Draft survives a full page reload ------------------------------
  // (T-207 AC3's reload-survival mechanism at the draft stage: persisted
  // active id + state refetched from GET /changesets/{id}.)
  await page.reload();
  await expect(page.getByRole("region", { name: "Change drawer" })).toContainText("Create bridge vmbr77");

  // --- 4. Guest NIC list + bulk reattach ---------------------------------
  // The fixture has 2 guests (app01 on pve1, cache01 on pve2) — the AC's
  // "5 guests" is illustrative; both are bulk-moved to vnet200 here.
  await page.getByRole("link", { name: "Guests" }).click();
  await expect(page.getByText("app01/net0")).toBeVisible();
  await expect(page.getByText("cache01/net0")).toBeVisible();
  await page.getByLabel("Select all").check();
  await page.getByLabel("Reattach selected guests to").selectOption({ label: "vnet200" });
  await page.getByRole("button", { name: /Reattach 2 guest/ }).click();
  await expect(page.getByText(/Drafted reattach for 2 guest NIC/).first()).toBeVisible();
  await expect(page.getByText(/Drafted reattach for 2 guest NIC/).first()).toBeHidden({ timeout: 10_000 });
  const drawerOnGuests = page.getByRole("region", { name: "Change drawer" });
  await expect(drawerOnGuests).toContainText("Update guest NIC");

  // --- 5. Review screen: all three tabs ----------------------------------
  await page.getByRole("button", { name: "Review & apply" }).click();
  const review = page.getByRole("dialog");
  // Summary tab (default): one card per op.
  await expect(review).toContainText("Create bridge vmbr77");
  await expect(review).toContainText("reattach to vnet200");
  // File diff tab: the real unified diff from GET /changesets/{id}/diff.
  await page.getByRole("tab", { name: "File diff" }).click();
  await expect(review).toContainText("pve1: /etc/network/interfaces");
  await expect(review.locator("pre")).toContainText("vmbr77");
  // Plan tab: preview mirroring BuildPlan — per-node stage->reload pair,
  // plus the not-yet-executable note for the guest ops.
  await page.getByRole("tab", { name: "Plan" }).click();
  await expect(review).toContainText("Stage /etc/network/interfaces on pve1");
  await expect(review).toContainText("Reload network on pve1");
  await expect(review).toContainText("Not yet executable by the apply engine: guest.nic.update");

  // Warnings gate: the bridge has no comment (advisory warning), so Apply
  // stays disabled until "Apply with warnings" is ticked.
  const applyButton = review.getByRole("button", { name: "Apply", exact: true });
  await expect(applyButton).toBeDisabled();
  await review.getByLabel("Apply with warnings").check();
  await expect(applyButton).toBeEnabled();

  // --- 6. Apply refusal for the guest ops (T-205 executable-op boundary) -
  await applyButton.click();
  await expect(page.getByText("Apply failed to start").first()).toBeVisible();
  await expect(page.getByText("Apply failed to start").first()).toBeHidden({ timeout: 10_000 });
  await review.getByRole("button", { name: "Back to drafting" }).click();

  // --- 7. Apply of the node-file-only changeset --------------------------
  // Remove the two guest ops, leaving just the bridge create.
  const guestRows = page.locator("li", { hasText: "Update guest NIC" });
  while ((await guestRows.count()) > 0) {
    await guestRows.first().getByRole("button", { name: "Remove" }).click();
    await expect(page.getByText("Added to changeset").last()).toBeHidden({ timeout: 10_000 }).catch(() => undefined);
  }
  await expect(drawerOnGuests).not.toContainText("Update guest NIC");

  await page.getByRole("button", { name: "Review & apply" }).click();
  const review2 = page.getByRole("dialog");
  await review2.getByLabel("Apply with warnings").check();
  await review2.getByRole("button", { name: "Apply", exact: true }).click();

  // With the dev_interfaces_dir sandbox (testdata/dev.toml) the stage and
  // reload steps run against var/dev-host with a no-op ifreload, so the
  // apply SUCCEEDS into the commit-confirm window: status applying ->
  // awaiting_confirm, and the WS-driven CountdownBanner renders the
  // Confirm / Roll back controls in a role="alert" region (the applying
  // and terminal-outcome states use role="status"; see CountdownBanner).
  const countdownBanner = page
    .getByRole("alert")
    .filter({ has: page.getByRole("button", { name: "Confirm" }) });
  await expect(countdownBanner).toBeVisible({ timeout: 60_000 });

  // AC3: the countdown survives a full page reload — state is rehydrated
  // from GET /changesets/{id} + a WS resubscribe, not held in memory.
  await page.reload();
  await expect(countdownBanner).toBeVisible({ timeout: 30_000 });

  // Confirm within the window -> status `committed`, committed outcome
  // banner (AC1's "apply -> countdown -> confirm").
  await countdownBanner.getByRole("button", { name: "Confirm" }).click();
  const committedBanner = page.getByRole("status").filter({ hasText: "was applied and committed" });
  await expect(committedBanner).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByRole("region", { name: "Change drawer" })).toBeHidden();
});

test("read-only capability user sees disabled editing affordances (spot-check)", async ({ page }) => {
  // auditor@pve holds Sys.Audit/VM.Audit/SDN.Audit only -> netRead without
  // netWrite/guestNet (T-207 acceptance criterion 4).
  await logIn(page, "auditor", "readonly", "pve");
  await waitForLayout(page);

  // The "New" entity menu is hidden entirely (no writable node).
  await expect(page.getByRole("button", { name: "New ▾" })).toHaveCount(0);

  // Inspector: Edit/Delete are disabled for a read-only session. Open it
  // via spotlight search rather than clicking a canvas node — React Flow
  // node hit-testing under headless Chromium is unreliable, and search ->
  // select is the same select() path a click would take.
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog");
  await searchDialog.getByPlaceholder(/web01/).fill("vmbr0");
  await searchDialog.getByRole("button", { name: /vmbr0/ }).first().click();
  const inspector = page.getByRole("dialog");
  await expect(inspector.getByRole("button", { name: "Edit" })).toBeDisabled();
  await expect(inspector.getByRole("button", { name: "Delete" })).toBeDisabled();
  await inspector.press("Escape");

  // Guests view: per-row disconnect affordances are disabled too.
  await page.getByRole("link", { name: "Guests" }).click();
  await expect(page.getByText("app01/net0")).toBeVisible();
  const disconnectButtons = page.getByRole("button", { name: /Disconnect|Connect/ });
  const n = await disconnectButtons.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    await expect(disconnectButtons.nth(i)).toBeDisabled();
  }
});
