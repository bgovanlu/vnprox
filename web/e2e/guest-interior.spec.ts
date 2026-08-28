// SPDX-License-Identifier: Apache-2.0

// T-1304 acceptance criterion 6: enabling the interior tab on a
// three-node-vlan fixture guest (app01, pve1/200 — see that fixture
// YAML's own "T-1304 guest network interior inspector fixtures" comment)
// with a scripted guest-agent response renders the interior view end to
// end, against the real stack (pvemock -> vnproxd -> the production SPA
// build). Unlike lacp-inspector.spec.ts's bond case, this path is fully
// deterministic here: the qemu guest-agent reads (AgentExec/
// GetGuestAgentInterfaces) go through internal/pve.Client straight to
// pvemock's mocked HTTP API, never through the real dev machine's own
// network state — the same reason POST /simulate/verify's own e2e
// coverage (simulator.spec.ts) is deterministic against sim-lab.
import { expect, test, type Page } from "@playwright/test";
import { isolateFile } from "./isolate";
import { mockURL } from "./shards";

// T-3204: this file toggles the interior inspector's own enable/mute state
// server-side; `--repeat-each=2` against a shared daemon ran the second
// repeat against the first repeat's already-toggled state (T-2505's AC3).
// Its own vnproxd (web/e2e/isolate.ts) removes the sharing — shard-4's
// default pvemock (read-only) is unaffected.
isolateFile({ config: "testdata/dev.toml", port: 64004, mockURL: mockURL("default") });

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("guest interior tab: opt-in toggle then renders app01's interior view", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);

  // Spotlight search returns both app01's NIC (kind "guest-nic", the map
  // entity) and app01 itself (kind "guest", the entity the Interior tab
  // lives on) as separate results — SpotlightSearch.tsx renders each
  // button's accessible name as "<label> <kind>", so an exact match on
  // "app01 guest" (not "app01/net0 guest-nic") picks the right one.
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog");
  await searchDialog.getByPlaceholder(/web01/).fill("app01");
  await searchDialog.getByRole("button", { name: "app01 guest" }).click();

  const inspector = page.getByRole("dialog");
  await expect(inspector.getByRole("tab", { name: "Interior" })).toBeVisible();
  await inspector.getByRole("tab", { name: "Interior" }).click();

  const interiorPanel = inspector.getByRole("tabpanel", { name: "Interior" });
  await expect(interiorPanel.getByText("Show this guest's network interior")).toBeVisible();
  await expect(interiorPanel.getByText(/Enable the toggle above to read this guest's interfaces/)).toBeVisible();

  const toggle = interiorPanel.getByRole("checkbox", { name: /Show this guest's network interior/ });
  await expect(toggle).not.toBeChecked();
  await toggle.check();

  // The interior view renders once the toggle flips on and the read
  // completes — app01's fixture-scripted qemu-ga response.
  await expect(interiorPanel.getByTestId("interior-view")).toBeVisible();
  await expect(interiorPanel.getByText("qemu-ga")).toBeVisible();
  await expect(interiorPanel.getByText(/10\.10\.0\.200\/24/)).toBeVisible();
  await expect(interiorPanel.getByText("reachable")).toBeVisible();

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
