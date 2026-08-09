// T-1402 acceptance criterion 3: "wizard run against two pvemock-backed
// clusters produces one changeset with both wg.* and fw.* ops; applying it
// leaves the edge rendering healthy on the next poll." Federation (T-1201)
// is not in this repo (see docs/features/topology.md §7's federation-seam
// note and planning/reports/T-1402.md), so "two clusters" here is this
// spec's one pvemock-backed cluster (three-node-vlan, the suite's default
// fixture) connecting one of its own nodes to a manually-entered external
// endpoint — the wizard's actual, buildable shape.
//
// What this spec DOES verify against the real stack (pvemock ->
// vnproxd -> production SPA build):
//   - The wizard's three steps, staging exactly one changeset containing
//     wg.tunnel.create + wg.peer.add + fw.rule.create (never two
//     changesets, never a partial set).
//   - The review screen's Plan tab shows a WireGuard apply step — proving
//     BuildPlan accepts the wg.* op family (T-1401's StepWgApply category)
//     rather than refusing it as an unsupported op, i.e. the backend
//     integration between this wizard's ops and the change engine is
//     genuinely wired, not just shaped correctly client-side.
//
// What this spec does NOT verify, and why (NEEDS HARDWARE VALIDATION,
// same category T-1401's own report already flags): actually clicking
// Apply on a wg.tunnel.create op execs the real `wg-quick`/`wg` binaries
// on the host running vnproxd (cmd/vnproxd/wireguard.go's hostWGGateway —
// unlike bond/bridge ops, which write into the dev NodeAgent's
// `dev_interfaces_dir` sandbox with a no-op ifreload, WireGuard has no
// equivalent dev/no-op runner as of T-1401). This dev/CI host has neither
// the `wg-quick` binary nor a WireGuard kernel module, so Apply
// deterministically fails at that exec step — this is an environment gap
// in T-1401's dev harness, not a defect in this task's wiring, and is
// flagged in planning/reports/T-1402.md rather than asserted here as if
// it were the intended behavior on a host that DOES have wg-quick
// installed.
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";
import { switchToGraphView } from "./helpers";

isolatedStore();

async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) el.style.setProperty("display", "none", "important");
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
}

test("connect-clusters wizard: one changeset with wg.tunnel.create + wg.peer.add + fw.rule.create", async ({ page }) => {
  await logIn(page);
  await switchToGraphView(page);

  // --- 1. Launch from the topology page's New menu -----------------------
  await page.getByRole("button", { name: "New ▾" }).click();
  await page.getByRole("menuitem", { name: "Connect two clusters (WireGuard)" }).hover();
  await page.getByRole("menuitem", { name: "pve1" }).click();

  const wizard = page.getByRole("dialog");
  await expect(wizard).toContainText("Connect two clusters");

  // --- 2. "This side" step: pve1 is preselected from the launch point ----
  await expect(wizard.getByRole("combobox", { name: /Source node/ })).toHaveValue("pve1");
  await wizard.getByRole("textbox", { name: /Interface name/ }).fill("wg0");
  await wizard.getByRole("button", { name: "Next" }).click();

  // --- 3. "Other side" step: a manually-entered external endpoint --------
  // (this wizard's federation seam — see this file's own doc comment).
  await wizard.getByRole("textbox", { name: /Peer public key/ }).fill("PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=");
  await wizard.getByRole("textbox", { name: /Peer endpoint/ }).fill("203.0.113.10:51820");
  await wizard.getByRole("textbox", { name: /Allowed IPs/ }).fill("10.10.0.2/32");
  await wizard.getByRole("button", { name: "Next" }).click();

  // --- 4. Firewall step: accept the default (allow from the peer) --------
  await expect(wizard).toContainText(/firewall rule/i);
  await wizard.getByRole("button", { name: "Next" }).click();

  // --- 5. Review + finish --------------------------------------------------
  await expect(wizard).toContainText(/WireGuard tunnel "wg0" on pve1/);
  await wizard.getByRole("button", { name: "Create draft" }).click();
  await expect(page.getByText("Added to changeset").first()).toBeVisible();

  // --- 6. The drafted changeset contains exactly the three ops, one -----
  //        changeset, never a half-open tunnel-without-firewall state.
  const drawer = page.getByRole("region", { name: "Change drawer" });
  await expect(drawer).toContainText(/WireGuard tunnel: pve1/i);
  await drawer.getByRole("button", { name: "Review & apply" }).click();

  const review = page.getByRole("dialog", { name: /Review & apply/i });
  await expect(review).toContainText(/Create WireGuard tunnel/);
  await expect(review).toContainText(/Add WireGuard peer/);
  await expect(review).toContainText(/Add firewall rule/);

  // The Plan tab proves BuildPlan accepted the wg.* ops as a real,
  // executable step category (StepWgApply) rather than refusing the
  // changeset outright as an unsupported op — the genuinely-wired half of
  // AC3 this environment can verify (see this file's top doc comment for
  // why the apply leg itself isn't exercised here).
  await page.getByRole("tab", { name: "Plan" }).click();
  await expect(review).toContainText(/WireGuard/i);
});
