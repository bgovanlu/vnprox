// SPDX-License-Identifier: Apache-2.0

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

/** Two or more active notices collapse into one row, and NoticeStack does not
 * render their bodies until it is opened (T-4304). Without this, no session has
 * any of these buttons, so "a read-only session has none" proves nothing. */
async function openNotices(page: Page): Promise<void> {
  const collapsed = page
    .getByRole("region", { name: "Topology notices" })
    .getByRole("button", { expanded: false });
  if ((await collapsed.count()) > 0) {
    await collapsed.first().click();
  }
}

/** The fixture's notices depend on polling state, so they arrive after load.
 * Two minutes matches visual.spec.ts's settleClusterStaleness. */
const NOTICE_SETTLE_MS = 120_000;

test.describe("Phase 36 remediation actions", () => {
  test.setTimeout(NOTICE_SETTLE_MS * 2);

  // The write-capable half. This is what stops the read-only assertion
  // below from passing vacuously: if none of the three banners renders in
  // this fixture at all, this test says so loudly instead of leaving the
  // security check quietly meaningless.
  test("a netWrite session is offered at least one remediation action on /topology", async ({ page }) => {
    await logInAs(page, "root", "vnprox-mock", "pam");

    const main = page.getByRole("main");
    const anyRemedy = main.getByRole("button", {
      name: new RegExp(REMEDIATION_BUTTONS.map((re) => re.source).join("|")),
    });
    await expect(async () => {
      await openNotices(page);
      await expect(anyRemedy.first()).toBeVisible({ timeout: 1_000 });
    }, "no Phase 36 remediation button rendered for a write-capable session — either the banners are " +
      "absent in this fixture (in which case the read-only test below proves nothing and this pair " +
      "needs a fixture that shows them) or the affordances have regressed").toPass({ timeout: NOTICE_SETTLE_MS });
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
      // A read-only session still sees the notices themselves. Waiting for one
      // to be open is what makes the absence checks below mean something.
      await expect(async () => {
        await openNotices(page);
        await expect(main.getByText(/last-known data|No LLDP data yet/).first()).toBeVisible({ timeout: 1_000 });
      }).toPass({ timeout: NOTICE_SETTLE_MS });

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
