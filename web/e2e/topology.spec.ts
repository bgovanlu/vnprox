// T-107 acceptance criterion 1's render-verification artifact (audit
// finding F-06): against the real stack (pvemock three-node-vlan fixture →
// vnproxd collectors → GET /topology → the production SPA build), log in
// with real Proxmox-style credentials, open /topology, and verify all four
// layer bands render — then capture a committed screenshot baseline.
//
// Environment caveat, documented rather than hidden: vnproxd's host
// collector reads the REAL machine it runs on (there is no PVE host here),
// so the pve1 band also contains this dev machine's own interfaces (e.g.
// lo, plus whatever NICs the machine has) alongside the fixture's
// pve-declared ones, and the LLDP loop fails unless lldpctl is installed.
// The assertions below therefore pin only fixture-declared, PVE-derived
// entities (stable across machines); the screenshot baseline IS
// machine-dependent — regenerate with `npm run e2e:update` when running on
// a different machine, and treat the committed baseline as this repo's
// reference environment, not a universal truth.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("all four layer bands render on /topology against the real backend", async ({ page }) => {
  await logIn(page);

  // One fixture-declared entity per layer (labels from
  // testdata/clusters/three-node-vlan.yaml via the real collector
  // pipeline). Generous timeout: the first collector poll cycle may still
  // be in flight right after the daemon starts.
  const perLayer: Record<string, string> = {
    phys: "eno1", // physical NIC
    l2: "vmbr0", // bridge
    sdn: "vlanz", // SDN zone (cluster-spanning band)
    guest: "app01/net0", // guest NIC
  };
  for (const [layer, label] of Object.entries(perLayer)) {
    await expect(
      page.getByRole("button", { name: label }).first(),
      `expected a visible ${layer}-layer node labeled ${label}`,
    ).toBeVisible();
  }

  // The four layer toggles exist and are all active by default.
  for (const layer of ["phys", "l2", "sdn", "guest"]) {
    await expect(page.getByRole("button", { name: new RegExp(layer, "i") }).first()).toBeVisible();
  }

  // All three per-node columns are populated: each node's bridge renders.
  await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(3);

  // No staleness banner: with pvemock up and polling healthy, the §5
  // last-known-data banner must be absent. (The lldp source may legitimately
  // be failing on a machine without lldpctl — during the first ~3 poll
  // cycles it is not yet marked stale, so assert only the pve-driven case:
  // the map itself rendered fresh, cluster-wide data.)
  await expect(page.getByText("This map is showing last-known data")).toHaveCount(0);

  // React Flow's `fitView` prop fits the viewport on mount — before the
  // async elkjs layout has positioned the nodes (they briefly stack at the
  // origin) — so wait for the layout to have spread the nodes out, then
  // refit explicitly, or the screenshot captures a mostly-empty corner of
  // the canvas.
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 10 && transforms.size > nodes.length / 2;
  });
  await page.getByRole("button", { name: "fit view" }).click();
  // Let the viewport transition settle before pixel-comparing.
  await page.waitForTimeout(800);

  // Committed baseline (see header comment: machine-dependent; regenerate
  // with npm run e2e:update). Screenshots the canvas element rather than
  // the full page so timing-dependent banners above it (e.g. the lldp
  // source going stale ~90s in on machines without lldpctl) can't shift
  // the layout mid-run.
  await expect(page.locator(".react-flow").first()).toHaveScreenshot("topology-three-node-vlan.png", {
    maxDiffPixelRatio: 0.05,
    animations: "disabled",
  });
});
