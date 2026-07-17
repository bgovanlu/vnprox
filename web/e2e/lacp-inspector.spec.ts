// T-804 acceptance criterion 4's e2e half: open the bond inspector against
// the real stack (pvemock three-node-vlan fixture -> vnproxd -> the
// production SPA build) and assert the LACP section renders. This suite's
// default fixture (playwright.config.ts) declares pve1's bond0 in 802.3ad
// mode ("LACP to access switch"), but — like topology.spec.ts's own doc
// comment explains — vnproxd's host collector here reads the REAL dev
// machine it runs on, which has no actual bond0 device, so host-netlink
// contributes no SlaveDetail for it. The LACP tab's job in that case is to
// degrade honestly ("no negotiation detail available") rather than crash or
// show fabricated data; on real PVE hardware with a genuine 802.3ad bond
// (needs-hardware-validation.md), the same tab would instead show the
// negotiated/mismatched callouts BondLacpSection.test.tsx covers against
// fixture data. This spec's job is "the tab exists and renders one of its
// known states without error" — the state-specific rendering itself is
// unit-tested.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("bond inspector's LACP tab renders for pve1's bond0", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);

  // Open bond0 via spotlight search rather than a canvas click (React Flow
  // node hit-testing is unreliable under headless Chromium — the same
  // rationale changesets.spec.ts's read-only-capability test documents for
  // its own inspector-opening step).
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog");
  await searchDialog.getByPlaceholder(/web01/).fill("bond0");
  await searchDialog.getByRole("button", { name: /bond0/ }).first().click();

  const inspector = page.getByRole("dialog");
  await expect(inspector.getByRole("tab", { name: "LACP" })).toBeVisible();
  await inspector.getByRole("tab", { name: "LACP" }).click();

  const lacpPanel = inspector.getByRole("tabpanel", { name: "LACP" });
  await expect(lacpPanel).toBeVisible();
  await expect(
    lacpPanel.getByText(
      /LACP negotiated correctly on every slave\.|split-brain|not fully negotiated|No 802\.3ad LACP negotiation detail available|No slave detail reported for this bond yet\./,
    ),
  ).toBeVisible();

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
