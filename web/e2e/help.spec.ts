// SPDX-License-Identifier: Apache-2.0

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

// T-3403 AC2: "/" is the top bar search field's global entry point (its
// own kbd hint reads "/") — it must work from any screen, not just
// Topology, by opening the spotlight and navigating there, exactly what
// clicking the rounded search field itself does (TopBar.tsx's openSearch).
// This re-proves that under the restyled markup and asserts the dialog by
// its accessible name ("Search", from SpotlightSearch's DialogTitle)
// rather than by test id, per docs/development.md's "assert on headings"
// rule for specs that must not lie about what they cover.
//
// T-3406 fix: the heading assertion was originally `getByRole("heading", …)`
// — a role-based (accessibility-tree) query — checked WHILE the spotlight
// dialog is open. SpotlightSearch's <Dialog> is a standard Radix modal
// (the shared Dialog wrapper does not pass `modal={false}`), so once it
// mounts, Radix's own `aria-hidden`-the-rest-of-the-page behavior correctly
// removes the Topology toolbar (and its "Topology" <h1>) from the
// accessibility tree — confirmed against a captured trace: the `<h1>` is
// present in the DOM with `aria-hidden="true"` on its wrapper, not
// display:none. That is correct, standard modal a11y (a screen-reader user
// should not be able to tab/read into what's behind an open modal), so the
// original assertion could never pass while the dialog was open — not a
// redesign regression. Swapped to a plain DOM locator (`h1`, bypassing the
// accessibility-tree filter role-based queries apply) so this still proves
// real navigation happened (the destination's own heading text, not just
// the URL), without asserting something that contradicts correct modal
// semantics.
test("'/' opens search from a non-topology page and lands on Topology with the spotlight open", async ({
  page,
}) => {
  await logIn(page);
  await page.goto("/ipam");
  await expect(page.getByRole("heading", { name: "IPAM", level: 1 })).toBeVisible();

  await page.keyboard.press("/");

  await page.waitForURL("**/topology");
  await expect(page.getByRole("dialog", { name: "Search" })).toBeVisible();
  await expect(page.locator("h1", { hasText: "Topology" })).toBeVisible();
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
