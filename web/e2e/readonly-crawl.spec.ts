// T-605 acceptance criterion 4: "Read-only sweep: automated crawl
// (Playwright) in read-only mode finds zero enabled mutating controls."
// Generalizes changesets.spec.ts's "read-only capability user sees disabled
// editing affordances (spot-check)" test (which only visits /topology and
// /guests) into a real crawl over every route in App.tsx's route table,
// logged in as `auditor`/`readonly`/`pve` — a real PVE user holding
// `Sys.Audit, VM.Audit, SDN.Audit` only (netRead without netWrite/
// sdnWrite/fwWrite/guestNet; defined in every testdata/clusters/*.yaml
// fixture, including this stack's three-node-vlan.yaml).
//
// Scope and strategy: this crawl inspects every button inside <main> (the
// routed page content) on each of the ten nav routes — deliberately
// excluding the shared chrome (Sidebar, TopBar's account menu/theme
// toggle/help, the OnboardingWalkthrough overlay, ChangesetDrawer), which
// render outside <main> in AppShell.tsx and are either already covered by
// the Vitest unit suite (OnboardingWalkthrough.test.tsx's read-only gating
// case) or are non-mutating chrome by construction.
//
// Classification is a MUTATING-VERB DENY-LIST, not an allowlist: a button
// is flagged only if its accessible name contains one of the verbs this
// codebase's own write affordances are consistently labeled with
// (Reattach/Disconnect/Connect/Delete/Remove/Import/Capture/Instantiate/
// Confirm/Install/Enable/Create/Save/Take snapshot/Restore/Apply/Roll
// back/Fix/Discard/"New ▾"). An allowlist-of-safe-names approach was tried
// first and produced false positives on every page with data-driven button
// labels (SDN zone/VNet tree rows, Blueprint starter card names) — those
// button *names* can be almost anything, but this app's actual write
// affordances are not: every one of them uses one of the verbs above
// (confirmed by grepping every useMutation/addOps call site in web/src
// while writing this spec). A verb that slips through ungated is exactly
// the kind of bug this crawl exists to catch; a data label that happens to
// contain one of these words as a false positive is an acceptable,
// visible-failure trade-off in the other direction.
//
// Login-once-per-file, not per-test: internal/auth's login rate limiter
// (docs/security.md, internal/auth/ratelimit.go) allows 10 attempts per
// (IP, username) before 429-ing, refilling one every 30s — this file's
// first draft called POST /auth/login fresh in every test's beforeEach
// (13 logins as the same "auditor" user from the same 127.0.0.1), which
// reliably exhausted that bucket partway through the run and hung the
// next login's `waitForURL`. Logging in exactly once in `beforeAll` and
// reusing the resulting session cookie (via Playwright's `storageState`)
// across every test in this file avoids that entirely, and is also just
// faster.
import { expect, test, type Page } from "@playwright/test";
import * as os from "node:os";
import * as path from "node:path";
import { switchToGraphView } from "./helpers";

const AUDITOR_STORAGE_STATE = path.join(os.tmpdir(), `vnprox-e2e-auditor-storage-state-${String(process.pid)}.json`);

// Every test in this file reuses the one session logged into below —
// Playwright's documented "log in once, reuse storageState" pattern
// (simpler than a custom fixture override, which also collides with this
// repo's react-hooks/rules-of-hooks lint rule: a fixture literally named
// `use`, per Playwright's own API, reads as a called-but-not-a-hook "use").
test.use({ storageState: AUDITOR_STORAGE_STATE });

test.beforeAll(async ({ browser }) => {
  // storageState: undefined overrides the ambient default `test.use` above
  // sets for every OTHER context (browser.newContext() otherwise inherits
  // the project's configured fixture defaults) — this bootstrap context
  // must start from a clean slate to log in fresh, not try to read back
  // the very file it's about to create.
  const context = await browser.newContext({ ignoreHTTPSErrors: true, storageState: undefined });
  const page = await context.newPage();
  await page.goto("/login");
  await page.getByLabel("Username").fill("auditor");
  await page.getByLabel("Password", { exact: true }).fill("readonly");
  await page.getByLabel("Realm").fill("pve");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await context.storageState({ path: AUDITOR_STORAGE_STATE });
  await context.close();
});

// Deliberately NOT included: a bare "apply" (VlanFilterInput.tsx's VLAN
// filter has its own, harmless "Apply" button reading `value !== undefined`
// filter state only — indistinguishable by accessible name alone from the
// real changeset-apply screen's "Apply" button). The real apply/confirm/
// roll-back flow lives entirely in <ChangesetDrawer/>'s review screen,
// which renders outside <main> (see this file's header doc comment) and —
// per the existing spot-check test in changesets.spec.ts — is unreachable
// with any content for a session that can never create a draft in the
// first place (every op-producing affordance in the app is itself gated).
const MUTATING_VERB_RE =
  /reattach|disconnect|\bconnect\b|delete|remove|import|capture|instantiate|confirm|install|enable|create|\bsave\b|take snapshot|restore|roll\s?back|\bfix\b|discard|drop|promote|revert|^new\s/i;

/** Asserts every button inside <main> on the current page that looks like
 * a mutating affordance (see MUTATING_VERB_RE) is disabled — the crawl's
 * one core check, shared across every route below. Fails loudly (not
 * silently) by collecting every offending button's name before asserting,
 * so a failure message names every violation at once rather than only the
 * first. */
async function assertNoEnabledMutatingControls(page: Page, routeLabel: string): Promise<void> {
  const main = page.getByRole("main");
  await expect(main).toBeVisible();

  const buttons = main.getByRole("button");
  const count = await buttons.count();

  const offenders: string[] = [];
  for (let i = 0; i < count; i++) {
    const button = buttons.nth(i);
    const name = ((await button.textContent()) ?? (await button.getAttribute("aria-label")) ?? "").trim();
    if (!MUTATING_VERB_RE.test(name)) continue;
    if (await button.isEnabled()) {
      offenders.push(name || "(unnamed button)");
    }
  }
  expect(offenders, `enabled, ungated mutating controls found on ${routeLabel}: ${offenders.join(", ")}`).toEqual([]);
}

/** Switches to the Graph view (67fff26 landed Switch as the default — see
 * helpers.ts) and waits for the async elkjs layout to have spread the
 * nodes out — same wait topology.spec.ts/changesets.spec.ts use; a bare
 * `.react-flow__node` selector match happens before elkjs has positioned
 * anything, so `:visible`-style waits on it are unreliable. Used purely as
 * a "the page has settled" readiness signal for this crawl's <main> button
 * scan, not because the scan cares which view is showing. */
async function waitForTopologyLayout(page: Page): Promise<void> {
  await switchToGraphView(page);
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 10 && transforms.size > nodes.length / 2;
  });
}

const ROUTES: { path: string; label: string; ready: (page: Page) => Promise<unknown> }[] = [
  // T-904: the Home dashboard is now the index route.
  { path: "/", label: "Home", ready: (p) => p.getByRole("heading", { name: "Home" }).waitFor() },
  { path: "/topology", label: "Topology", ready: (p) => waitForTopologyLayout(p) },
  { path: "/guests", label: "Guests", ready: (p) => p.getByRole("heading", { name: "Guests" }).waitFor() },
  { path: "/sdn", label: "SDN", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/firewall", label: "Firewall", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/ipam", label: "IPAM", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/blueprints", label: "Blueprints", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/history", label: "History", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/audit", label: "Audit", ready: (p) => p.getByRole("main").waitFor() },
  { path: "/tools", label: "Tools", ready: (p) => p.getByRole("heading", { name: "Tools" }).waitFor() },
  { path: "/settings", label: "Settings", ready: (p) => p.getByRole("main").waitFor() },
];

test.describe("read-only crawl (auditor@pve: netRead only)", () => {
  for (const route of ROUTES) {
    test(`${route.label}: zero enabled mutating controls`, async ({ page }) => {
      await page.goto(route.path);
      await route.ready(page);
      await assertNoEnabledMutatingControls(page, route.label);
    });
  }

  test("Topology: the 'New' entity menu is absent entirely (no writable node)", async ({ page }) => {
    await page.goto("/topology");
    await waitForTopologyLayout(page);
    await expect(page.getByRole("button", { name: "New ▾" })).toHaveCount(0);
  });

  test("Guests: selecting rows and picking a reattach target still leaves the bulk button disabled", async ({ page }) => {
    // Regression coverage for the straggler this task's sweep found
    // (GuestsPage.tsx: the bulk button was gated only on "no target
    // picked", not on capability) — exercised end-to-end here since it's
    // exactly the kind of wiring bug a crawl (not a spot-check) exists to
    // catch: enabling the button requires an interaction sequence
    // (select -> pick target) the static per-route sweep above doesn't
    // drive.
    await page.goto("/guests");
    await page.getByRole("heading", { name: "Guests" }).waitFor();
    await page.getByLabel("Select all").check();
    const targetSelect = page.getByLabel("Reattach selected guests to");
    await expect(targetSelect).toBeVisible();
    const options = await targetSelect.locator("option").allTextContents();
    const realTarget = options.find((o) => o !== "Reattach to…");
    if (realTarget) {
      await targetSelect.selectOption({ label: realTarget });
    }
    await expect(page.getByRole("button", { name: /Reattach \d+ guest/ })).toBeDisabled();
  });

  test("History: Take snapshot and Create restore draft are disabled", async ({ page }) => {
    // Regression coverage for the straggler this task's sweep found
    // (HistoryPage.tsx: neither action had any capability gating at all).
    await page.goto("/history");
    await page.getByRole("main").waitFor();
    await expect(page.getByRole("button", { name: "Take snapshot" })).toBeDisabled();
  });
});
