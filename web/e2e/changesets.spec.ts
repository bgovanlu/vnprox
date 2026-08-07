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
import { switchToGraphView } from "./helpers";

// T-605: see topology.spec.ts's identical helper doc comment — suppressing
// the onboarding walkthrough banner via an injected stylesheet (rather
// than clicking its "Minimize" button) avoids persisting a PUT
// /layouts/onboarding that would leak across the two usernames this file
// logs in as (and into other spec files sharing this same webServer/DB
// within one test run).
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

async function logIn(page: Page, username = "root", password = "vnprox-mock", realm = "pam"): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await page.goto("/login");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Realm").fill(realm);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Switches to the Graph view (67fff26 landed Switch as the default — see
 * helpers.ts) and waits for the async elkjs layout to have spread the
 * nodes out (see topology.spec.ts's identical wait). Every call site below
 * uses this purely as a "the page has settled" readiness signal, not
 * because the rest of that test drives the canvas directly. */
async function waitForLayout(page: Page): Promise<void> {
  await switchToGraphView(page);
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
  // Removing a row re-renders the whole op list, so the *next* iteration's
  // "first row" is a different element than the one this iteration resolved.
  // Waiting for the count to actually drop before looking again is what
  // makes that safe: the previous form of this loop resolved a Remove
  // button, then clicked it while the list was still settling, and failed
  // with "element is not stable" / "element was detached from the DOM"
  // whenever the machine was loaded enough for the re-render to land inside
  // the click's actionability window. Anchoring each step on the observable
  // state change instead of on a toast that may already have expired removes
  // the race rather than widening a timeout around it.
  const guestRows = page.locator("li", { hasText: "Update guest NIC" });
  for (let remaining = await guestRows.count(); remaining > 0; remaining--) {
    await guestRows.first().getByRole("button", { name: "Remove" }).click();
    await expect(guestRows).toHaveCount(remaining - 1);
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
  // What this step means is "the committed changeset is no longer the active
  // draft", and that is what it now asserts. It used to assert the drawer
  // region was *hidden*, which is a stronger claim than the product makes:
  // ChangesetDrawer only unmounts when there is no active draft AND no other
  // parked draft to resume, so any draft left behind by an earlier spec in
  // the same single-worker run (change-review.spec.ts sorts immediately
  // before this file) keeps a collapsed "Changes" launcher on screen and
  // fails the assertion for a reason that has nothing to do with T-207.
  // That cross-spec store sharing is tracked as T-2108-followup-01; this
  // assertion should not be a second, misleading detector for it.
  // toHaveCount(0), not a negated assertion scoped to the drawer: whether
  // the drawer region exists at all depends on drafts other specs left in
  // the shared store, and a `not.toContainText` on a region that isn't
  // rendered fails with "element(s) not found" rather than passing. Counting
  // is the form that holds in both worlds.
  await expect(page.getByText("Create bridge vmbr77")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Review & apply" })).toHaveCount(0);
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
  // Radix's Dialog overlay (fixed inset-0, above the nav rail) can still be
  // mounted for its exit animation for a moment after Escape — a click that
  // lands on it during that window is swallowed rather than reaching the
  // "Guests" nav link underneath, and the app never navigates (observed
  // flake: nav stays on /topology). Wait for the dialog to be fully gone
  // from the accessibility tree before clicking through it.
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // Guests view: per-row disconnect affordances are disabled too.
  // Wait for the Guests page's own heading first — without this, the
  // client-side route transition can still be in flight (this runs right
  // after Escape-closing the inspector dialog) and the topology graph's own
  // "app01/net0" node label, which is still in the DOM a moment longer, can
  // satisfy a bare getByText assertion below and let the test race onto a
  // page that never actually finished navigating (observed flake: 0
  // Disconnect/Connect buttons found). Anchoring on the page-level heading
  // makes the navigation itself part of what's asserted, not assumed.
  await page.getByRole("link", { name: "Guests" }).click();
  await expect(page.getByRole("heading", { name: "Guests", level: 1 })).toBeVisible();
  // exact: true — the row's sr-only a11y description ("guest-nic app01/net0,
  // status ok, badges: ...", EntityNode.tsx) also contains "app01/net0" as a
  // substring, so a plain getByText match is ambiguous between it and the
  // visible label; exact match picks only the visible span.
  await expect(page.getByText("app01/net0", { exact: true })).toBeVisible();
  const disconnectButtons = page.getByRole("button", { name: /Disconnect|Connect/ });
  const n = await disconnectButtons.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    await expect(disconnectButtons.nth(i)).toBeDisabled();
  }
});
