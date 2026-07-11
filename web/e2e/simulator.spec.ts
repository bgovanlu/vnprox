// T-504 acceptance criteria, against the real stack (pvemock
// testdata/clusters/sim-lab.yaml -> vnproxd collectors -> the production
// SPA build), on this task's own webServer pair (web/playwright.config.ts,
// ports 18006/18007 — see sim-lab.yaml's doc comment for why this fixture
// exists rather than reusing three-node-vlan.yaml/firewall-scenarios.yaml).
//
//  AC1: vm-a -> vm-c on tcp/80 is a deny (vm-c's own guest-scope DROP
//       rule) -> the blocking-rule card's "Open in firewall editor" link
//       lands on /firewall with that exact rule scrolled-to/highlighted.
//  AC2: vm-a -> vm-b is unreachable (pve2's vmbr0 is deliberately not
//       VLAN-aware) -> the embedded map marks the missing link.
//  AC3: the "Simulated" badge and caveat list are checked inline as part
//       of AC1/AC2 (both verdicts always carry them — see ResultPanel.tsx).
//  AC5: "Trace path" from the main topology map (right-click a guest NIC)
//       pre-fills the simulator and covers guest->guest, guest->external,
//       and IP->guest.
import { expect, test, type Page } from "@playwright/test";

test.use({ baseURL: "https://127.0.0.1:18007" });

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Waits for the async elkjs layout to have spread the nodes out (see
 * topology.spec.ts's identical wait) — only needed before interacting
 * with the *main* topology map (right-click); the simulator's own result
 * panel and its embedded map's "missing link" marker render independently
 * of layout settling, so those assertions don't need this. */
async function waitForLayout(page: Page): Promise<void> {
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 4 && transforms.size > 1;
  });
}

/** Types into one endpoint picker's guest-NIC search box and clicks the
 * (single) matching result — scoped to that picker's own labeled results
 * list (EndpointPicker.tsx's `aria-label`), since `/tools` also renders a
 * full topology map whose node buttons share the same "<guest>/<nic>"
 * text and would otherwise make a plain role query ambiguous. */
async function pickGuestNic(page: Page, side: "Source" | "Destination", query: string): Promise<void> {
  await page.getByLabel(`${side} guest NIC search`).fill(query);
  const results = page.getByRole("list", { name: `${side} guest NIC results` });
  const result = results.getByRole("button").first();
  await expect(result).toBeVisible();
  await result.click();
}

const VERDICT_TEXT = /^(Allowed|Blocked|Unreachable|Could not determine)$/;

test("T-504 AC1/AC3: guest deny verdict deep-links into the focused firewall rule", async ({ page }) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
  await pickGuestNic(page, "Source", "vm-a");
  await pickGuestNic(page, "Destination", "vm-c");

  await page.getByLabel("Protocol").selectOption("tcp");
  await page.getByLabel("Port").fill("80");

  // Verdict: deny, always labeled Simulated, caveats always visible.
  await expect(page.getByText("Blocked", { exact: true })).toBeVisible();
  await expect(page.getByText("Simulated", { exact: true })).toBeVisible();
  await expect(page.getByText(/Caveats \(\d+\)/)).toBeVisible();

  // Blocking rule card names the exact fixture rule.
  await expect(page.getByText("Blocking rule")).toBeVisible();
  await expect(page.getByText("override: block http on this guest specifically")).toBeVisible();

  // One click lands on the firewall page with the rule focused.
  await page.getByRole("link", { name: "Open in firewall editor" }).click();
  await page.waitForURL(/\/firewall\?scope=guest/);
  await expect(page.getByRole("button", { name: "Guests" })).toHaveAttribute("aria-pressed", "true");
  // The rule appears twice on this page (the guest's own RuleTable AND its
  // resolved-order entry in ResolvedViewTable) — both are expected.
  await expect(page.getByText("override: block http on this guest specifically").first()).toBeVisible();
  await expect(page.locator('[data-focused="true"]').first()).toBeVisible();
});

test("T-504 AC2: unreachable-VLAN case renders the missing link on the map", async ({ page }) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
  await pickGuestNic(page, "Source", "vm-a");
  await pickGuestNic(page, "Destination", "vm-b");

  await expect(page.getByText("Unreachable", { exact: true })).toBeVisible();
  await expect(page.getByText("Missing link")).toBeVisible();
  // The exact operator-facing message (docs/features/firewall.md §5),
  // naming both the VLAN and the offending node.
  await expect(page.getByText("bridge vmbr0 on node pve2 is not VLAN-aware, so VLAN 100 traffic is not carried")).toBeVisible();

  // The break is marked at the correct edge/node on the embedded map.
  await expect(page.getByRole("img", { name: "missing link" })).toBeVisible();
});

test("T-504 AC5: Trace path from the map pre-fills and runs (guest->guest, guest->external, IP->guest)", async ({ page }) => {
  await logIn(page);
  await waitForLayout(page);

  // --- guest->guest: "Trace path from here" on vm-a, then pick vm-c -----
  await page.getByRole("button", { name: "vm-a/net0" }).click({ button: "right" });
  await page.getByRole("menuitem", { name: "Trace path from here" }).click();
  await page.waitForURL(/\/tools\?srcKind=guest-nic/);
  await expect(page.getByText(/guest-nic:pve1:300\/net0/)).toBeVisible();

  await pickGuestNic(page, "Destination", "vm-c");
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();

  // --- guest->external: single map action pre-fills AND runs -----------
  await page.goto("/topology");
  await waitForLayout(page);
  await page.getByRole("button", { name: "vm-a/net0" }).click({ button: "right" });
  await page.getByRole("menuitem", { name: "Trace path to external" }).click();
  await page.waitForURL(/\/tools\?srcKind=guest-nic.*dstKind=external/);
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();

  // --- IP->guest: "Trace path to here" on vm-b, then type a source IP ---
  await page.goto("/topology");
  await waitForLayout(page);
  await page.getByRole("button", { name: "vm-b/net0" }).click({ button: "right" });
  await page.getByRole("menuitem", { name: "Trace path to here" }).click();
  await page.waitForURL(/\/tools\?dstKind=guest-nic/);
  await expect(page.getByText(/guest-nic:pve2:302\/net0/)).toBeVisible();

  await page.getByRole("radio", { name: "IP address" }).first().click();
  await page.getByLabel("Source IP address").fill("10.20.0.99");
  await page.getByLabel("Source IP address").blur();
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();
});
