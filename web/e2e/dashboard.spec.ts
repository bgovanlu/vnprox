// T-904 acceptance criterion 6: end-to-end verification of the Home
// dashboard against the real stack (pvemock three-node-vlan fixture +
// vnproxd + the production SPA build) — logs in, confirms the dashboard
// renders at "/" (the app's index route, this task's change to App.tsx),
// and exercises one tile's deep link end to end (findings-by-severity ->
// /tools).
//
// Note on "landing page": this task changed App.tsx's index route
// (`/`) from `<Navigate to="/topology">` to `<DashboardPage/>` — that is
// the scope this card's instructions named. LoginPage.tsx's own
// post-login redirect target is a separate, hardcoded fallback
// (`from ?? "/topology"`, unrelated to the router's index route) that
// this task deliberately did not touch: every other e2e spec in this
// repo asserts `waitForURL("**/topology")` right after login, so
// repointing that fallback at "/" would be a much larger, cross-cutting
// change out of scope for a single card (flagged in this task's own
// report). This spec therefore logs in, then explicitly visits "/" (and
// separately clicks the Sidebar's Home entry) to verify the dashboard
// itself, rather than asserting login redirects there.
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

test("Home dashboard renders at / with every tile, and a tile deep-links to its owning page", async ({ page }) => {
  await logIn(page);

  // Sidebar's Home entry (AC1) reaches the dashboard from anywhere in the
  // app — exercised here rather than relying on login's own redirect
  // target (see this file's header doc comment).
  await page.getByRole("link", { name: "Home" }).click();
  await page.waitForURL("**/");

  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();

  // Every tile is present (AC2's six tiles), each as an accessible
  // labelled region.
  for (const title of [
    "Findings by severity",
    "Drift status",
    "Pending changesets",
    "Management-path redundancy",
    "Top talkers",
    "Recent audit activity",
  ]) {
    await expect(page.getByRole("region", { name: title })).toBeVisible();
  }

  // Deep link (AC3, AC6): findings-by-severity -> /tools.
  await page
    .getByRole("region", { name: "Findings by severity" })
    .getByRole("button", { name: "Open findings" })
    .click();
  await page.waitForURL("**/tools");
  await expect(page.getByRole("heading", { name: "Tools", level: 1 })).toBeVisible();
});

test("reloading directly at / (a fresh navigation, not just client-side routing) renders the dashboard", async ({ page }) => {
  await logIn(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await expect(page.getByRole("region", { name: "Findings by severity" })).toBeVisible();
});
