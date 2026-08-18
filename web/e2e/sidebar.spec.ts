// T-3402: end-to-end verification of the Stripe-style grouped Sidebar
// against the real stack (pvemock three-node-vlan fixture + vnproxd + the
// production SPA build) — specifically the one behavior NavRail never had:
// a collapsible group. Read-only (login + click-through chrome only, no
// changeset/finding state mutated), so this runs against the shard's
// shared default-stack daemon like nav-after-inspector.spec.ts and
// readonly-crawl.spec.ts do.
//
// The countdown-banner-overlap regression this task's card also names
// (T-909: CountdownBanner is `fixed z-40` and once painted over the nav,
// making items unclickable) is NOT re-tested here — it is already covered,
// unmodified, by responsive-triage.spec.ts's "phone width: Dashboard ->
// Findings -> a pending changeset -> confirm" test, which clicks the
// Tools link while the countdown banner is visible at narrow width. That
// spec's own doc comment predates this task; its continuing to pass
// against the new Sidebar (relative z-50 preserved, see Sidebar.tsx) is
// this task's proof the regression hasn't come back, without a second,
// redundant apply/confirm round trip just to re-observe the same thing.
import { expect, test, type Page } from "@playwright/test";

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

test("a collapsed group's route is reachable by expanding the group, then clicking its link", async ({ page }) => {
  await logIn(page);

  const nav = page.getByRole("navigation", { name: "Primary" });
  const networkToggle = nav.getByRole("button", { name: "Network" });
  await expect(networkToggle).toHaveAttribute("aria-expanded", "true");

  // Collapse it first — the scenario this test is named for is "expand ->
  // click", which only means something starting from collapsed.
  await networkToggle.click();
  await expect(networkToggle).toHaveAttribute("aria-expanded", "false");
  await expect(nav.getByRole("link", { name: "IPAM" })).toHaveCount(0);

  // Expand -> click, unconditionally (no isVisible() guard — a group stuck
  // collapsed must fail this test, not skip the rest of it).
  await networkToggle.click();
  await expect(networkToggle).toHaveAttribute("aria-expanded", "true");
  await nav.getByRole("link", { name: "IPAM" }).click();

  await page.waitForURL("**/ipam");
  await expect(page.getByRole("heading", { name: "IPAM", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Topology", level: 1 })).toHaveCount(0);
});

test("a group containing the active route is expanded on load, even though groups start collapsed for a returning user who closed it", async ({
  page,
}) => {
  await logIn(page);

  const nav = page.getByRole("navigation", { name: "Primary" });
  const automateToggle = nav.getByRole("button", { name: "Automate" });
  await automateToggle.click();
  await expect(automateToggle).toHaveAttribute("aria-expanded", "false");

  // Navigate straight to a route inside the now-collapsed group, the way a
  // deep link or a browser-history back/forward would.
  await page.goto("/governance");
  await expect(page.getByRole("heading", { name: "Governance", level: 1 })).toBeVisible();

  // The group auto-expanded to keep its own active route visible.
  await expect(nav.getByRole("button", { name: "Automate" })).toHaveAttribute("aria-expanded", "true");
  await expect(nav.getByRole("link", { name: "Governance" })).toBeVisible();
});
