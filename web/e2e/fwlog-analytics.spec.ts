// T-1006 acceptance criterion 4's e2e half: the firewall log viewer's
// Analytics tab, against the real stack (pvemock three-node-vlan fixture
// -> vnproxd -> the production SPA build) with the seeded log fixture
// corpus (testdata/firewall-logs/*.log, wired in via testdata/dev.toml's
// [firewalllog] dev_fixture_dir — this suite's default stack already loads
// it, no extra wiring needed).
//
// The fixture's vmids (100/101 on pve1, 200 on pve2, per
// testdata/firewall-logs/pve1.log/pve2.log) don't correspond to
// three-node-vlan.yaml's real guests (app01 = vmid 200 on pve1, cache01 =
// vmid 201 on pve2) — a deliberate mismatch already present before this
// task (T-505's own fixture was built to exercise parsing/correlation
// against synthetic vmids, not to line up with a specific cluster's real
// guest roster). That mismatch is exactly what makes this a robust,
// timing-independent AC4 target: app01 (guest:pve1:200) never receives a
// single logged hit under its own real identity, so its only reachable
// rule (the cluster-wide SSH rule at resolved position 0) always shows up
// in the Analytics tab's unused-rules list, regardless of the analytics
// window or when this suite happens to run — this test's job is proving
// that unused-rule row's "edit rule" link lands on the exact right row in
// the rule editor (genuine cross-page interaction proof no component test
// can substitute for), not exercising the hit-count/top-blocked math
// itself (covered by internal/fwlog/analytics_test.go and
// AnalyticsTab.test.tsx).
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("Analytics tab's unused-rule edit link lands on the exact rule row in the editor", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);
  await page.goto("/tools");

  await expect(page.getByRole("heading", { name: "Firewall log" })).toBeVisible();
  await page.getByRole("tab", { name: "Analytics" }).click();

  const analyticsPanel = page.getByRole("tabpanel", { name: "Analytics" });
  await expect(analyticsPanel.getByText("Unused rules")).toBeVisible();

  // app01 (guest:pve1:200) never appears in the seeded log under its own
  // real identity — its sole reachable rule (cluster origin, pos 0) is
  // always unused. Locate that row specifically (rather than "any row")
  // so this test still means something if the fixture ever grows more
  // guests/rules.
  const unusedRow = analyticsPanel.getByRole("row", { name: /guest:pve1:200/ });
  await expect(unusedRow).toBeVisible();
  const editLink = unusedRow.getByRole("link");
  await expect(editLink).toBeVisible();

  const href = await editLink.getAttribute("href");
  expect(href).toContain("scope=guest");
  expect(href).toContain("ref=guest%3Apve1%3A200");
  expect(href).toContain("pos=0");
  expect(href).toContain("origin=cluster");

  await editLink.click();

  // Lands on /firewall with the guest scope/selection pre-seeded from the
  // deep link, and the exact rule row (cluster origin, pos 0) highlighted
  // — never DOM position (FirewallPage.tsx / ResolvedViewTable.tsx's
  // data-focused marker, the same mechanism T-504/T-505's own deep links
  // already prove out).
  await page.waitForURL(/\/firewall\?/);
  await expect(page.getByRole("heading", { name: "Firewall" })).toBeVisible();

  const focusedRow = page.locator('[data-focused="true"]');
  await expect(focusedRow).toBeVisible();
  await expect(focusedRow).toContainText("0"); // Pos column
  await expect(focusedRow).toContainText("Cluster"); // OriginBadge label

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
