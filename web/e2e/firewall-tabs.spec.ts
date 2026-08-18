// T-3404 acceptance criterion 2: one representative tabbed page (Firewall,
// chosen over SDN because it's the one whose tabs already deep-link —
// FirewallPage.tsx's scope/ref/pos/origin query params, T-504/T-2002) both
// switches tabs and deep-links, against the shared underlined Tabs wrapper
// (docs/development.md "Visual language" — role="tab", not the pre-T-3404
// hand-rolled role="button"/aria-pressed toggle).
//
// Follows docs/development.md's three lessons about specs that lie: assert
// on headings (the page-level "Firewall" h1, present throughout — this
// proves no navigation away happened), assert the *previous* tab's own
// content is gone (not just that the new content appeared), and no
// conditional steps.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("Firewall tabs switch content, and a deep link opens directly on the target tab", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);

  // A plain navigation (no deep-link params) defaults to the Datacenter
  // tab — its own content (the cluster-scope enablement toggle) is visible.
  await page.goto("/firewall");
  await expect(page.getByRole("heading", { name: "Firewall", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Datacenter" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("Datacenter firewall is", { exact: false })).toBeVisible();

  // Clicking the Guests tab switches the visible panel: the Datacenter
  // panel's own content disappears, the Guests panel's own content (a
  // guest picker) appears — and the page never navigated away (same "Firewall" h1).
  await page.getByRole("tab", { name: "Guests" }).click();
  await expect(page.getByRole("tab", { name: "Guests" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Select guest")).toBeVisible();
  await expect(page.getByText("Datacenter firewall is", { exact: false })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Firewall", level: 1 })).toBeVisible();

  // A fresh, cold navigation carrying the guest deep-link query params
  // (the same shape a simulator deny verdict or a correlated firewall-log
  // line links to — simulator.spec.ts / fwlog-analytics.spec.ts) lands
  // directly on the Guests tab, already selected, with no manual click —
  // proving the *destination page* itself opens on the right tab, not just
  // that the link's href is well-formed.
  await page.goto("/firewall?scope=guest&ref=guest%3Apve1%3A200&pos=0&origin=cluster");
  await expect(page.getByRole("heading", { name: "Firewall", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Guests" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Select guest")).toBeVisible();
  await expect(page.getByText("Datacenter firewall is", { exact: false })).toHaveCount(0);

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
