// T-607: E2E coverage for the docs/user-guide.md §3 "Common tasks" table,
// the five rows not already covered end-to-end by an existing spec:
//   - changesets.spec.ts already covers "Make a bridge VLAN-aware and
//     trunk 10-30" (its bridge-editor step) and "Move 12 VMs to another
//     bridge" (its bulk-guest-reattach step, at fixture scale).
//   - simulator.spec.ts already covers "Why can't VM A reach VM B?".
//   - This file covers the remaining four table rows, plus the fifth
//     ("Create a LACP bond from two NICs") via the New-menu *form* path
//     (docs/user-guide.md §3: "Map → select NICs → 'Create bond', or
//     Node → Bonds → New" — the second alternative; the drag-select path
//     is out of scope here for the same reason changesets.spec.ts
//     documents: React Flow node-drag doesn't fire reliably headless, and
//     is unit-tested instead in src/changesets/dragDropOps.test.ts).
//
// Every flow here stops at "op lands in the change drawer" (draft only),
// mirroring changesets.spec.ts's own bridge-editor step — proving each
// documented task is reachable end-to-end against the real stack, not
// exercising the (already covered elsewhere) apply/confirm machinery a
// second time per task.
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";
import { switchToGraphView } from "./helpers";

isolatedStore({ config: "testdata/dev-scale.toml" });

async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
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

async function waitForLayout(page: Page): Promise<void> {
  // 67fff26 landed Switch, not Graph, as /topology's default view (see
  // helpers.ts) — .react-flow__node only exists once Graph is selected.
  await switchToGraphView(page);
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 4 && transforms.size > 1;
  });
}

// --- "Create a LACP bond from two NICs" (New -> Bond form) -----------------
// Neither three-node-vlan.yaml nor sim-lab.yaml has an unenslaved NIC left
// anywhere (every NIC is already on bond0) — a clean bond.create draft
// needs real free NICs, so this reuses T-607's own scale-lab.yaml stack
// (web/playwright.config.ts's third webServer pair, 28006/28007), whose
// eno5/eno6 are free on every node.
test.describe("bond creation via the New-menu form", () => {

  test("Node -> Bonds -> New: LACP bond from two free NICs lands a bond.create draft", async ({ page }) => {
    await logIn(page);
    await waitForLayout(page);

    await page.getByRole("button", { name: "New ▾" }).click();
    await page.getByRole("menuitem", { name: "Bond" }).hover();
    await page.getByRole("menuitem", { name: "pve1" }).click();

    await page.getByLabel(/^Name/).fill("bond9");
    await page.getByLabel(/^Mode/).selectOption("802.3ad");
    // Each slave candidate is its own checkbox with a distinct accessible
    // name ("eno5 up, 0Mbps", etc. — link state/speed from BondEditor's
    // candidate list); a bare /^eno5\b/ prefix match avoids collision with
    // "eno1"/"eno2"/etc. sharing the "eno" prefix.
    await page.getByRole("checkbox", { name: /^eno5\b/ }).check();
    await page.getByRole("checkbox", { name: /^eno6\b/ }).check();
    await page.getByRole("button", { name: "Add to changeset" }).click();

    const drawer = page.getByRole("region", { name: "Change drawer" });
    await expect(drawer).toContainText("Create bond bond9");
    await expect(drawer).toContainText("eno5");
    await expect(drawer).toContainText("eno6");
    await expect(page.getByText("Added to changeset").first()).toBeHidden({ timeout: 10_000 });
  });
});

// --- SDN wizards + IPAM reserve + firewall macro: three-node-vlan stack ----
test.describe("SDN zone wizards, IPAM reserve, firewall macro rule", () => {
  test("SDN -> New zone -> Simple: an isolated test network on all nodes lands a full zone/vnet/subnet/apply draft", async ({
    page,
  }) => {
    await logIn(page);
    await page.getByRole("link", { name: "SDN" }).click();
    await page.getByRole("button", { name: "+ New zone (guided)" }).click();
    await page.getByRole("button", { name: "Simple network" }).click();

    await page.getByLabel(/^Name/).fill("homelab");
    await page.getByRole("checkbox", { name: "pve1" }).check();
    await page.getByRole("checkbox", { name: "pve2" }).check();
    await page.getByRole("checkbox", { name: "pve3" }).check();
    await page.getByRole("button", { name: "Next" }).click();

    await page.getByLabel(/^Name/).fill("vnet1");
    await page.getByRole("button", { name: "Next" }).click();

    await page.getByLabel("Address range (CIDR)").fill("10.50.0.0/24");
    // T-701: "no gateway" is now an explicit radio choice (SubnetStep.tsx)
    // instead of just leaving the gateway textbox empty.
    await page.getByRole("radio", { name: /Keep this network isolated/ }).check();
    await page.getByRole("button", { name: "Next" }).click();

    // The wizard's own Review step spells out "isolated, no gateway" for the
    // drafted subnet (SimpleZoneWizard.tsx) — assert that before drafting,
    // since the change drawer's op summary never included gateway/snat for
    // sdn.subnet.create ops to begin with.
    await expect(page.getByText("Subnet 10.50.0.0/24 (isolated, no gateway)")).toBeVisible();

    await page.getByRole("button", { name: "Create draft" }).click();

    const drawer = page.getByRole("region", { name: "Change drawer" });
    await expect(drawer).toContainText("Create sdn zone homelab");
    await expect(drawer).toContainText("Create sdn vnet");
    await expect(drawer).toContainText("Create sdn subnet 10.50.0.0/24");
    await expect(drawer).toContainText("Apply pending SDN configuration");
  });

  test("SDN -> New zone -> VXLAN: stretching a network across nodes lands a vxlan zone draft with underlay peers", async ({
    page,
  }) => {
    await logIn(page);
    await page.getByRole("link", { name: "SDN" }).click();
    await page.getByRole("button", { name: "+ New zone (guided)" }).click();
    await page.getByRole("button", { name: "VXLAN network (overlay)" }).click();

    await page.getByLabel(/^Name/).fill("overlay1");
    await page.getByRole("checkbox", { name: "pve1" }).check();
    await page.getByRole("checkbox", { name: "pve2" }).check();
    await page.getByRole("checkbox", { name: "pve3" }).check();
    // Peer underlay addresses auto-suggest from each node's own vmbr0
    // address (three-node-vlan.yaml: 10.10.0.11/.12/.13) — nothing to fill.
    await page.getByRole("button", { name: "Next" }).click();

    // Both values here used to be rejected by the wizard's own validation,
    // which is why this step could never be reached, let alone passed:
    //   - "vnet-overlay1" contains a hyphen; SDN names are letters and
    //     digits only, starting with a letter (Proxmox's own rule).
    //   - 10001 is outside the accepted VNI range. validation.ts caps VNIs
    //     at VNI_MAX (4094) to match internal/change.maxVID; widening that
    //     to VXLAN's full 16777215 is a documented follow-up there, not
    //     something this test gets to assume.
    await page.getByLabel("VNet name").fill("vnetov1");
    await page.getByLabel("VNI").fill("4001");
    await page.getByRole("button", { name: "Next" }).click();

    await page.getByLabel("Address range (CIDR)").fill("10.90.0.0/24");
    // T-701: "Gateway" alone now strict-mode-collides with the gateway-mode
    // radiogroup (aria-label "Gateway") and its two radios, so select the
    // textbox specifically. The field also pre-fills from the CIDR's first
    // usable address (10.90.0.1 here) — the explicit fill is a no-op given
    // that prefill, kept for clarity/robustness rather than relying on it.
    await page.getByRole("textbox", { name: /Gateway/ }).fill("10.90.0.1");
    await page.getByRole("button", { name: "Next" }).click();

    await page.getByRole("button", { name: "Create draft" }).click();

    const drawer = page.getByRole("region", { name: "Change drawer" });
    await expect(drawer).toContainText("Create sdn zone overlay1");
    await expect(drawer).toContainText("vnetov1");
    await expect(drawer).toContainText("Create sdn subnet 10.90.0.0/24");
  });

  test("IPAM -> subnet -> address list -> Reserve: reserving a free IP lands an ipam.alloc.create draft naming the address", async ({
    page,
  }) => {
    await logIn(page);
    await page.getByRole("link", { name: "IPAM" }).click();
    // three-node-vlan.yaml's vnet100 subnet: only .1 (gateway) and .50
    // (app01) are allocated — .51 is genuinely free.
    await page.getByRole("button", { name: /10\.100\.0\.0\/24/ }).click();
    // The per-address "grid" of clickable cells this test was written
    // against is gone: AddressList.tsx replaced it with a NetBox-style list
    // that collapses contiguous free space into "N addresses free" range
    // rows, precisely so a /16 reads like a /30. There is no
    // "10.100.0.51: Free" button to click any more — free space is reserved
    // from the range that contains it. Anchoring on the range whose start
    // IS .51 keeps this test asserting on the same concrete address it
    // always did. docs/user-guide.md's task table was stale in the same way
    // and is corrected alongside this.
    const freeRange = page.locator("div", { hasText: /^10\.100\.0\.51 – 10\.100\.0\.254/ }).last();
    await freeRange.getByRole("button", { name: "Reserve first free →" }).click();
    await page.getByPlaceholder("Hostname (optional)").fill("web02");
    await page.getByRole("button", { name: "Reserve 10.100.0.51" }).click();

    await expect(page.getByText("Reserve 10.100.0.51 added to changeset").first()).toBeVisible();
    const drawer = page.getByRole("region", { name: "Change drawer" });
    await expect(drawer).toContainText("Reserve 10.100.0.51");
    await expect(drawer).toContainText("web02");
  });

  test("Firewall -> guest -> builder row (macro HTTP): allowing only web traffic lands an fw.rule.create draft", async ({
    page,
  }) => {
    await logIn(page);
    await page.getByRole("link", { name: "Firewall" }).click();
    await page.getByRole("button", { name: "Guests" }).click();
    await page.getByLabel("Select guest").selectOption("guest:pve1:200"); // app01

    await page.getByLabel("Macro").selectOption("HTTP");
    await page.getByRole("button", { name: "Add rule" }).click();

    await expect(page.getByText("Rule added to draft").first()).toBeVisible();
    const drawer = page.getByRole("region", { name: "Change drawer" });
    await expect(drawer).toContainText("Add firewall rule");
    await expect(drawer).toContainText("HTTP");
  });
});
