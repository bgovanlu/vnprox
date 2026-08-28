// SPDX-License-Identifier: Apache-2.0

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
//
// T-806 (extends this suite, same sim-lab stack): "Verify live" — vm-a ->
// vm-c on tcp/2222 is sim-lab.yaml's own scripted divergence fixture (no
// explicit rule matches, so the simulator falls to the cluster's default
// policy_in DROP; the fixture scripts vm-a's live probe toward that exact
// tuple as "reachable"), clicking Verify live surfaces the divergence
// callout both in the result panel and on the embedded map overlay.
import { availableParallelism } from "node:os";

import { expect, test, type Locator, type Page } from "@playwright/test";
import { switchToGraphView, waitForLayoutSettled } from "./helpers";
import { coresFactor } from "../perf/budgets";
import { isolateFile } from "./isolate";
import { mockURL } from "./shards";

// T-3204: T-806's "Verify live" tests below mute/ack findings and drive
// real divergence probes through the daemon's own store; `--repeat-each=2`
// against a shared daemon ran the second repeat against state the first
// repeat had already mutated (T-2505's AC3). Its own vnproxd
// (web/e2e/isolate.ts) removes the sharing — sim-lab's pvemock (18006)
// stays shared and read-only.
isolateFile({ config: "testdata/dev-sim.toml", port: 64009, mockURL: mockURL("sim") });

/** T-2505's deadline ladder (web/playwright.config.ts's `slowFactor`), applied
 * here explicitly: traceFromContextMenu's per-action/toPass timeouts below are
 * literals, not `expect()` calls, so they never inherited the config's
 * `expect: { timeout: 30_000 * slowFactor }` scaling the rest of this suite
 * gets for free. T-3204's flake investigation (30-37.5% failure rate on this
 * exact test, evidence in web/test-results/e2e-wave2*.log and
 * var/e2e-runs/runs.jsonl, all citing full concurrent-shard runs — never
 * reproduced in 15/15 isolated repeats on an idle machine) is consistent with
 * exactly this: a machine slow/contended enough that a mid-gesture re-layout
 * (this function's own doc comment) does not settle inside a fixed 5s/60s
 * budget, the same "deadlines are a function of the machine" conclusion
 * T-2505-input-01/02 reached for the rest of the suite. */
const slowFactor = coresFactor(availableParallelism());

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Switches to the Graph view (67fff26 landed Switch as the default — see
 * helpers.ts; the main map's node-context-menu "Trace path" actions this
 * file drives are only wired up in Graph view, not Switch) and waits for
 * the async elkjs layout to have SETTLED (see helpers.ts's
 * waitForLayoutSettled — T-3713) — only needed before interacting with the
 * *main* topology map (right-click); the simulator's own result panel and
 * its embedded map's "missing link" marker render independently of layout
 * settling, so those assertions don't need this. */
async function waitForLayout(page: Page): Promise<void> {
  await switchToGraphView(page);
  await waitForLayoutSettled(page, { minNodes: 4 });
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

/** The Source / Destination endpoint pickers as their own scopes.
 *
 * EndpointPicker.tsx renders each side as a <fieldset> with a <legend>, which
 * is role="group" named by that legend. Asserting a ref inside its own picker
 * — rather than anywhere on the page — is what stops these assertions from
 * colliding with the Findings panel and the topology map, both of which also
 * render entity refs on /tools. `pickGuestNic` above already scopes for the
 * same reason; these exist so the surrounding assertions can too. */
function sourcePicker(page: Page): Locator {
  return page.getByRole("group", { name: "Source" });
}

function destinationPicker(page: Page): Locator {
  return page.getByRole("group", { name: "Destination" });
}

const VERDICT_TEXT = /^(Allowed|Blocked|Unreachable|Could not determine)$/;

/** Right-clicks a map node and picks one of its context-menu actions,
 * retrying the whole gesture if the canvas re-layouts mid-way. Late in a
 * full-suite run the shared vnproxd's collector churn (the fixture's peer
 * nodes are unreachable from this machine, so their circuit breakers flap
 * open/half-open forever) produces a steady stream of topology deltas;
 * each refetch re-runs the elk layout, and a node that moves or remounts
 * between actionability checks can make a single click() with no timeout
 * wait for the rest of the test (observed once at full-suite scale: AC5's
 * plain click({button:"right"}) on vm-b/net0 hung 112s until the test
 * timeout, with the same gesture passing in ~0.3s standalone). Short
 * per-action timeouts + a toPass retry make the gesture as a whole robust
 * against a mid-gesture re-layout without hiding a real regression — a
 * genuinely missing node/menu item still fails after the outer timeout. */
async function traceFromContextMenu(page: Page, nodeName: string, action: string): Promise<void> {
  await expect(async () => {
    await page.getByRole("button", { name: nodeName }).click({ button: "right", timeout: 5_000 * slowFactor });
    await page.getByRole("menuitem", { name: action }).click({ timeout: 5_000 * slowFactor });
  }).toPass({ timeout: 60_000 * slowFactor });
}

test("T-504 AC1/AC3: guest deny verdict deep-links into the focused firewall rule", async ({ page }) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
  await pickGuestNic(page, "Source", "vm-a");
  await pickGuestNic(page, "Destination", "vm-c");

  await page.getByLabel("Protocol").selectOption("tcp");
  await page.getByLabel("Port", { exact: true }).fill("80");

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
  // T-3404: FirewallPage's hierarchy tabs migrated to the shared underlined
  // Tabs wrapper (role="tab", not the old role="button").
  await expect(page.getByRole("tab", { name: "Guests" })).toHaveAttribute("aria-selected", "true");
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
  await traceFromContextMenu(page, "vm-a/net0", "Trace path from here");
  await page.waitForURL(/\/tools\?srcKind=guest-nic/);
  // Scoped to the Source picker's own fieldset (EndpointPicker.tsx renders a
  // <fieldset>/<legend>, i.e. role="group" named by the legend). Unscoped,
  // this raced the findings engine: /tools also renders the Findings panel,
  // and once a cycle produces a finding naming this same guest NIC the plain
  // getByText resolves to two elements and fails on strict mode — passing or
  // failing purely on whether collection had completed yet. Seen failing
  // 2026-08-12, before the wave it surfaced in.
  await expect(sourcePicker(page).getByText(/guest-nic:pve1:300\/net0/)).toBeVisible();

  await pickGuestNic(page, "Destination", "vm-c");
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();

  // --- guest->external: single map action pre-fills AND runs -----------
  await page.goto("/topology");
  await waitForLayout(page);
  await traceFromContextMenu(page, "vm-a/net0", "Trace path to external");
  await page.waitForURL(/\/tools\?srcKind=guest-nic.*dstKind=external/);
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();

  // --- IP->guest: "Trace path to here" on vm-b, then type a source IP ---
  await page.goto("/topology");
  await waitForLayout(page);
  await traceFromContextMenu(page, "vm-b/net0", "Trace path to here");
  await page.waitForURL(/\/tools\?dstKind=guest-nic/);
  await expect(destinationPicker(page).getByText(/guest-nic:pve2:302\/net0/)).toBeVisible();

  await page.getByRole("radio", { name: "IP address" }).first().click();
  await page.getByLabel("Source IP address").fill("10.20.0.99");
  await page.getByLabel("Source IP address").blur();
  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();
});

test("T-806: Verify live surfaces the divergence callout on the result panel and the map overlay", async ({ page }) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
  await pickGuestNic(page, "Source", "vm-a");
  await pickGuestNic(page, "Destination", "vm-c");
  await page.getByLabel("Protocol").selectOption("tcp");
  await page.getByLabel("Port", { exact: true }).fill("2222");

  // Simulated verdict: deny (no explicit rule matches tcp/2222 — falls to
  // the cluster's default policy_in DROP, per sim-lab.yaml's T-806 doc
  // comment).
  await expect(page.getByText("Blocked", { exact: true })).toBeVisible();

  // "Verify live" is enabled for vm-a (a qemu guest with a reachable
  // guest agent) and runs the scripted live probe.
  const verifyButton = page.getByRole("button", { name: "Verify live" });
  await expect(verifyButton).toBeEnabled();
  await verifyButton.click();

  // Side-by-side simulated/observed rendering, plus the divergence
  // callout (the fixture's scripted "reachable" outcome disagrees with
  // the deny verdict above). "Reachable" appears twice (the Observed
  // summary and the callout's own restatement), so scope to the first.
  await expect(page.getByText("Live result disagrees with the simulated verdict")).toBeVisible();
  await expect(page.getByText("Reachable", { exact: true }).first()).toBeVisible();

  // The map overlay marks the probed source with a distinct divergence
  // indicator (EntityNode.tsx's "verify live diverges" marker), the same
  // role="img" convention AC2's "missing link" marker above uses.
  await expect(page.getByRole("img", { name: "verify live diverges" })).toBeVisible();
});

test("T-806: Verify live is disabled with plain-English copy for an external source", async ({ page }) => {
  await logIn(page);
  await page.goto("/tools");

  await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
  // Source: External (the first "External" tab in DOM order belongs to
  // the Source picker — SimulatorPage.tsx renders Source before
  // Destination in its two-column grid).
  await page.getByRole("radio", { name: "External" }).first().click();
  await pickGuestNic(page, "Destination", "vm-c");

  await expect(page.getByText(VERDICT_TEXT).first()).toBeVisible();
  const verifyButton = page.getByRole("button", { name: "Verify live" });
  await expect(verifyButton).toBeDisabled();
  await expect(page.getByText(/pick a guest NIC as the source/i)).toBeVisible();
});
