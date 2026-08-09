// Regression e2e for the EVPN/BGP tab blank-screen bug: on a cluster whose
// nodes have no FRR (the three-node-vlan fixture), the EVPN status endpoint
// returned nodes with `null` peers/vnis, which the EVPN view iterated
// directly (`for..of node.peers`, `node.vnis.map`) — an uncaught TypeError
// that unmounted the whole page to a blank screen. This opens the tab and
// asserts (a) no uncaught page error, and (b) real content renders.
import { expect, test, isolatedStore } from "./isolated";

isolatedStore();

test("EVPN/BGP tab renders on a no-FRR cluster without a page crash", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  // Log in fresh (auditor/readonly/pve is a real read-only PVE user in the
  // three-node-vlan fixture).
  await page.goto("/login");
  await page.getByLabel("Username").fill("auditor");
  await page.getByLabel("Password", { exact: true }).fill("readonly");
  await page.getByLabel("Realm").fill("pve");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");

  await page.goto("/sdn");
  await page.getByRole("main").waitFor();

  // Open the EVPN / BGP tab.
  await page.getByRole("tab", { name: "EVPN / BGP" }).click();

  // The view renders real content — this no-FRR fixture shows the empty
  // states, not a blank page and not the page-level error fallback.
  await expect(page.getByRole("heading", { name: "No BGP/EVPN sessions observed" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No EVPN VNIs observed" })).toBeVisible();
  await expect(page.getByText("This page hit an error")).toHaveCount(0);

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
