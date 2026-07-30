// T-1907 end to end: the physical-layer per-node summary pill
// (internal/topology/collapse_physical.go) against a real vnproxd + pvemock
// stack whose one node (pve1) has 10 physical NICs — comfortably over
// topology.DefaultPhysicalCollapseThreshold — one of them (eno1) a real
// bridge port, the other nine genuinely unattached. See testdata/clusters/
// phys-collapse.yaml's own doc comment for why this fixture exists rather
// than reusing three-node-vlan/scale-lab (neither has a node anywhere near
// this NIC count — docs/features/topology.md §4's scale target is
// deliberately under the threshold).
//
// Covers AC1 (collapses by default above threshold), AC2 (expanding yields
// exactly the ten real NICs, nothing missing/extra), and AC4 (renders and
// expands in both the default Switch view and the Graph view — the latter
// keyboard-only, no mouse input, mirroring a11y.spec.ts's own AC3 pattern).
//
// This spec runs against its OWN vnproxd+pvemock pair (ports 68006/68007) —
// see web/playwright.config.ts's webServer array and
// testdata/dev-physcollapse.toml.
import { expect, test, type Page } from "@playwright/test";

test.use({ baseURL: "https://127.0.0.1:68007" });

// Mirrors mgmt-redundancy.spec.ts's identical onboarding-banner suppression
// (a CSS-via-.style approach, since the style-src CSP blocks an injected
// <style> element).
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) el.style.display = "none";
    };
    const obs = new MutationObserver(suppress);
    document.addEventListener("DOMContentLoaded", () => {
      suppress();
      obs.observe(document.body, { childList: true, subtree: true });
    });
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

test("Switch view: the physical layer collapses to one pill by default, and expanding it restores all ten real NICs", async ({
  page,
}) => {
  await logIn(page);

  // The pill (rendered inside vmbr0's uplink bay, since eno1 is a real
  // bridge port — see switchModel.ts's isGroup handling) — waited for with
  // a generous timeout since the collector's first poll cycle has to land.
  const pill = page.getByRole("button", { name: "Expand 10 collapsed NICs" });
  await expect(pill).toBeVisible({ timeout: 30_000 });

  // AC1's regression half: none of the ten individual NICs are rendered
  // while collapsed.
  for (let i = 1; i <= 10; i++) {
    await expect(page.getByRole("button", { name: `eno${String(i)}`, exact: true })).toHaveCount(0);
  }

  await pill.click();

  // AC2's losslessness proof, end to end: exactly the ten real NICs appear
  // (eno1 inside vmbr0's uplink bay — the group->target edge survived
  // expansion; eno2..eno10 as unattached free ports), the pill itself is
  // gone, and nothing else is duplicated or missing.
  await expect(pill).toHaveCount(0);
  for (let i = 1; i <= 10; i++) {
    await expect(page.getByRole("button", { name: `eno${String(i)}`, exact: true })).toBeVisible();
  }
});

test("Graph view (v2 canvas): the pill is keyboard-reachable and Enter-activates to expand — no mouse input", async ({
  page,
}) => {
  await logIn(page);

  await page.getByRole("radio", { name: "Graph" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("radio", { name: "Graph" })).toHaveAttribute("aria-checked", "true");

  await page.getByRole("button", { name: "Canvas v2" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();

  // T-901's a11y bridge (TopologyA11yLayer) gives every visible canvas
  // entity a real, focusable proxy button — the pill's is entityAriaLabel's
  // "physical NIC group, N NICs, ..." phrasing (a11yBridge.ts).
  const mapRegion = page.getByRole("application", { name: /Topology map/ });
  const pillProxy = mapRegion.getByRole("button", { name: /physical NIC group, 10 NICs/ });
  await expect(pillProxy).toBeVisible({ timeout: 30_000 });

  await pillProxy.focus();
  await page.keyboard.press("Enter");

  // Enter-activating a phys-group proxy expands it in place (TopologyPage's
  // handleNodeClick routes a phys-group id to toggleExpanded, never the
  // inspector) — the pill disappears and the real eno1 physnic entity
  // (a real inventory ref, unlike the pill) becomes reachable in its place.
  await expect(mapRegion.getByRole("button", { name: /physical NIC group/ })).toHaveCount(0);
  await expect(mapRegion.getByRole("button", { name: /^physnic eno1,/ })).toBeVisible();
});
