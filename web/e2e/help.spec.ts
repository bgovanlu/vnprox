// T-2205: end-to-end verification of the online help surface against the
// real stack (pvemock fixture + vnproxd + the production SPA build) — the
// top-bar Help button, the F1 binding, contextual resolution per screen,
// a panel-level <HelpAnchor>, search, seeAlso navigation, and Escape.
//
// The enforcement of "100% of screens have help" lives in vitest
// (src/help/coverage.test.ts), deliberately, because `web/e2e/` is not run
// by any automated gate (T-1806-bug-01, docs/development.md). This spec
// checks the surface actually works in a browser; it is not what backs the
// coverage claim.
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";

isolatedStore();

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
  // waitForURL resolves when the navigation completes, which is BEFORE React
  // has mounted useKeyboardShortcuts' window keydown listener. A key pressed
  // in that gap is dropped on the floor — deterministically, in this app —
  // which is why `?` looked like a permanent product defect that reproduced
  // even at commits predating the help work (T-2108). Waiting for a piece of
  // the app shell to be interactive closes the gap. Proven by experiment:
  // pressing immediately yields 0 dialogs, pressing after this yields 1.
  await expect(page.getByRole("button", { name: "Keyboard shortcuts" })).toBeVisible();
}

test("the Help button opens help for the screen you're on, and F1 does the same", async ({ page }) => {
  await logIn(page);

  await page.getByRole("button", { name: "Help", exact: true }).click();
  const panel = page.getByRole("dialog");
  await expect(panel.getByText("Topology", { exact: true })).toBeVisible();
  // The heading, not any text mentioning the phrase: the panel's own summary
  // paragraph also contains "Switch view", so a bare getByText is a strict-mode
  // violation. Tightened rather than relaxed — the section heading is what this
  // assertion was always about.
  await expect(panel.getByRole("heading", { name: "Switch view" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();

  // A different screen must yield a different topic — proof the panel is
  // contextual rather than always opening the same page.
  await page.goto("/ipam");
  await page.keyboard.press("F1");
  await expect(page.getByRole("dialog").getByText("IPAM", { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog").getByText("Conflicts are the point")).toBeVisible();
});

test("`?` still opens the keyboard shortcut list, not the help panel", async ({ page }) => {
  await logIn(page);

  await page.keyboard.press("?");

  // The two surfaces are deliberately distinct; the shortcut dialog is the
  // one that lists key bindings.
  await expect(page.getByText("Keyboard shortcuts")).toBeVisible();
  await expect(page.getByText("Help for this screen")).toBeVisible();
});

test("a panel-level ? anchor opens help for that panel, and seeAlso navigates and returns", async ({
  page,
}) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("button", { name: "Help: Path simulator" }).click();
  const panel = page.getByRole("dialog");
  await expect(panel.getByText("Path simulator", { exact: true })).toBeVisible();
  await expect(panel.getByText("Four verdicts, including an honest one")).toBeVisible();

  await panel.getByRole("button", { name: "Conntrack" }).click();
  await expect(panel.getByText("Live, not configured")).toBeVisible();

  await panel.getByRole("button", { name: "Back" }).click();
  await expect(panel.getByText("Four verdicts, including an honest one")).toBeVisible();
});

test("help search finds a topic by body text and opens it", async ({ page }) => {
  await logIn(page);

  await page.keyboard.press("F1");
  const panel = page.getByRole("dialog");
  await panel.getByRole("searchbox", { name: "Search help" }).fill("squatter");

  const hit = panel.getByRole("button", { name: /^IPAM/ });
  await expect(hit).toBeVisible();
  await hit.click();
  await expect(panel.getByText("Conflicts are the point")).toBeVisible();
});
