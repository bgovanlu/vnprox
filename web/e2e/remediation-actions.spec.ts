// T-3605: Phase 36's buttons, end to end, against the real stack.
//
// The three topology banners now offer to fix what they report. Each one is
// a mutation reachable in two clicks from a page an operator looks at all
// day, so the property that actually matters is not "does the button work"
// — the handler tests cover that, including the audit row and the
// allow-list refusal — but **who is offered one at all**.
//
// So the shape of this spec is a pair: a write-capable session must see the
// affordance, and a read-only session must not. Asserting only the second
// half would pass just as well if the banner never rendered for anybody,
// which is the failure mode a "no button is visible" test is most likely to
// hide.
//
// Where the audit assertions live: in the Go handler tests
// (internal/api/servicestart_test.go, collectorrefresh_test.go), because
// that is where the row is actually produced and where a refusal — the most
// interesting case — can be driven directly. Asserting it again through the
// SPA would test the audit *page*, not the audit *write*.
import { expect, test, type Page } from "@playwright/test";

async function logInAs(page: Page, username: string, password: string, realm: string): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Realm").fill(realm);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await expect(page.getByRole("main")).toBeVisible();
}

/** Every Phase 36 affordance the topology view can render, by accessible
 * name. Kept as one list so "a read-only session sees none of them" is a
 * single assertion that cannot silently miss a newly added button. */
const REMEDIATION_BUTTONS = [
  /Install lldpd on all nodes/,
  /Retry now/,
  /^Start (dnsmasq|frr)$/,
];

test.describe("Phase 36 remediation actions", () => {
  // The write-capable half. This is what stops the read-only assertion
  // below from passing vacuously: if none of the three banners renders in
  // this fixture at all, this test says so loudly instead of leaving the
  // security check quietly meaningless.
  test("a netWrite session is offered at least one remediation action on /topology", async ({ page }) => {
    await logInAs(page, "root", "vnprox-mock", "pam");

    const main = page.getByRole("main");
    let found: string | undefined;
    for (const re of REMEDIATION_BUTTONS) {
      if ((await main.getByRole("button", { name: re }).count()) > 0) {
        found = re.source;
        break;
      }
    }
    expect(
      found,
      "no Phase 36 remediation button rendered for a write-capable session — either the banners are " +
        "absent in this fixture (in which case the read-only test below proves nothing and this pair " +
        "needs a fixture that shows them) or the affordances have regressed",
    ).toBeDefined();
  });

  // AC1. A read-only session must not be offered a button that installs
  // software on every node, starts a daemon, or spends PVE API calls.
  //
  // Absent, not disabled: remediation.ts resolves an operational remedy to
  // nothing without netWrite, deliberately, because a greyed-out control
  // explains nothing about why it is grey and a read-only operator is
  // better served by the finding's own text and its documentation link.
  test("a read-only session is offered none of them", async ({ browser }) => {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, storageState: undefined });
    const page = await context.newPage();
    try {
      await logInAs(page, "auditor", "readonly", "pve");

      const main = page.getByRole("main");
      for (const re of REMEDIATION_BUTTONS) {
        await expect(
          main.getByRole("button", { name: re }),
          `a read-only session was offered ${re.source}`,
        ).toHaveCount(0);
      }

      // The documentation link the LLDP banner has always carried stays —
      // it is the thing a read-only operator can actually act on, and
      // removing it along with the button would leave them worse off than
      // before Phase 36.
      const docsLink = main.getByRole("link", { name: "Set up lldpd" });
      if ((await docsLink.count()) > 0) {
        await expect(docsLink.first()).toBeVisible();
      }
    } finally {
      await context.close();
    }
  });
});
