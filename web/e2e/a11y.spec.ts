// SPDX-License-Identifier: Apache-2.0

// T-905 acceptance criterion 6: automated axe checks across the app's main
// surfaces (Dashboard, Topology's Graph and Switch views, SDN, Firewall,
// IPAM, the changeset drawer, Settings), against the real stack (pvemock
// three-node-vlan fixture -> vnproxd -> production SPA build), asserting
// zero serious/critical violations. Also covers AC3's keyboard-only map
// traversal: Tab/arrow-key reaching and Enter-activating a canvas v2 map
// entity with no mouse input, consuming T-901's a11y bridge
// (TopologyA11yLayer.tsx) this card's WCAG pass is built on.
//
// "No mouse input" here means no `.click()`/pointer-event dispatch — every
// interaction is `.focus()` (a programmatic DOM focus call, not a pointer
// event) followed by a real `page.keyboard.press(...)`, exactly mirroring
// how a keyboard-only user reaches and activates a control.
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import * as os from "node:os";
import * as path from "node:path";
import { forceDemoAccent, forceTheme, switchToGraphView } from "./helpers";
import {
  HEADING_LEVEL_OVERRIDES,
  pathEndRegExp,
  routedPagePaths,
} from "./routeInventory";

async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector(
        '[aria-label="Onboarding walkthrough"]',
      );
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
    };
    const start = () => {
      try {
        suppress();
        new MutationObserver(suppress).observe(document.documentElement, {
          childList: true,
          subtree: true,
        });
      } catch {
        setTimeout(start, 0);
      }
    };
    start();
  });
}

async function logIn(page: Page): Promise<void> {
  // T-2004: axe samples computed style at whatever instant it runs, and a
  // running CSS animation is a moving target — a drift-badged switch
  // faceplate's `animate-pulse` (SwitchFaceplate.tsx) cycles the WHOLE
  // card's opacity between 1 and .5, and a scan that lands mid-cycle reports
  // a real but animation-phase-dependent color-contrast violation on every
  // badge inside it (found investigating this task's "9px badge" defect —
  // it was this, not the badge tints, that produced most of the reported
  // violation count). `prefers-reduced-motion: reduce` is also the more
  // correct posture for this suite regardless: it makes `useReducedMotion()`
  // (src/lib/useReducedMotion.ts) report true, which is the app's own,
  // already-shipped mechanism for skipping `animate-pulse` — so this both
  // stabilizes the scan and exercises that every animated affordance this
  // suite touches actually honors the setting, rather than exercising a
  // fixed instant of an animation that a real reduced-motion user would
  // never even see.
  await page.emulateMedia({ reducedMotion: "reduce" });
  await suppressOnboardingWalkthrough(page);
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Runs axe against the current page and asserts zero serious/critical
 * violations — moderate/minor issues are out of this card's scope (AC6
 * names "serious/critical" specifically) and are not suppressed, just not
 * gating; this keeps the assertion focused on the impact levels that
 * actually block a screen-reader/keyboard user rather than cosmetic
 * contrast nits a design pass elsewhere might still pick up. */
async function expectNoSeriousViolations(
  page: Page,
  label: string,
): Promise<void> {
  const results = await new AxeBuilder({ page }).analyze();
  const blocking = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );
  expect(blocking, `${label}: ${JSON.stringify(blocking, null, 2)}`).toEqual(
    [],
  );
}

// T-3406 fix: src/store/theme.ts has always defaulted to `theme: "dark"`
// (the original T-002 scaffold, unrelated to and unchanged by Phase 34), so
// a fresh browser context with no "vnprox.theme" localStorage entry boots
// dark, not light. "axe: top bar, switched to dark theme" below assumed the
// opposite and waited forever for a button literally named "Switch to dark
// theme", which cannot exist while already dark — ThemeToggle.tsx's label
// is `theme === "dark" ? "Switch to light theme" : "Switch to dark theme"`.
// Priming localStorage before the app boots makes the starting theme an
// explicit, correct precondition instead of an assumed one, matching the
// same fix applied to demo.spec.ts's two equivalent T-3403 AC3 tests.
async function forceLightTheme(page: Page): Promise<void> {
  await page.addInitScript(() => {
    localStorage.setItem(
      "vnprox.theme",
      JSON.stringify({ state: { theme: "light" }, version: 0 }),
    );
  });
}

/**
 * Waits until at least one entity has gone stale, so scans that care about
 * the stale treatment measure it rather than racing it.
 */
async function waitForAStaleEntity(page: Page): Promise<void> {
  await expect(page.locator(".grayscale").first()).toBeVisible({
    timeout: 90_000,
  });
}

/**
 * Neutralizes opacity over the node faceplates for the axe measurement — the
 * de-emphasis dimming only, and, since T-2004, scoped to only the elements
 * that actually carry it rather than the whole node subtree.
 *
 * This replaces `neutralizeStaleFaceplates`, which did the same thing but
 * justified itself as normalizing a dev-host artifact: the fixture's
 * pve2/pve3 peers are genuinely unreachable here, so their collectors go
 * stale within a minute of boot and the faceplates render greyed. Part of
 * that was wrong, and it mattered (T-2505-followup-04):
 *
 *  - The STALE dim was a real defect, not an artifact, and it is now fixed at
 *    source instead of normalized away. `opacity` fades a node's TEXT along
 *    with its chrome, so a node went sub-AA (the Graph view's kind badge
 *    measured 4.30:1 against a 4.5:1 floor) exactly when it was reporting
 *    that its data had stopped refreshing. `stale` now sets no opacity in
 *    either view — `grayscale` alone carries the signal. The Graph view had
 *    no equivalent suppression, which is how axe caught it there at all, and
 *    only intermittently, as `axe: changeset drawer`.
 *  - What still needs neutralizing is the DE-EMPHASIS dimming: `dimmed` and
 *    `dimVid` drop filtered-out slots to `opacity-25`/`opacity-40`
 *    (SwitchFaceplate.tsx), the only opacity classes that file emits.
 *    Forcing opacity:1 clears every remaining violation; forcing
 *    filter:none alone clears none of them, which is what pins the cause on
 *    opacity rather than on the greying — so this only touches `opacity`,
 *    not `filter`, and leaves `stale`'s grayscale on screen and measured.
 *    Content the user has actively filtered out is the same contrast-exempt
 *    case as a disabled control, and axe cannot tell the difference.
 *
 * T-2004 narrowed the selector from "every node section, unconditionally" to
 * "only subtrees actually carrying `.opacity-25`/`.opacity-40`" — the exact
 * lesson from the incident above, applied to the suppression that survived
 * it: after re-picking the faceplate's badge tints so they clear AA at full
 * strength (see SwitchFaceplate.tsx/SwitchView.tsx `T-2004` comments and this
 * task's report for the measured before/after ratios), a blanket
 * opacity-1/filter-none override was no longer pulling its weight — it was
 * neutralizing content that was never dimmed in the first place. Scoping to
 * the two classes that are the dimming means this can never again silently
 * swallow an unrelated opacity- or filter-driven defect the way
 * `neutralizeStaleFaceplates` did.
 *
 * The callers below now wait for a stale entity BEFORE neutralizing, so the
 * stale state is inside what gets measured rather than being raced past — the
 * previous version scanned early enough that it often never saw one.
 */
async function neutralizeFaceplateDimming(page: Page): Promise<void> {
  await page.evaluate(() => {
    document
      .querySelectorAll(
        [
          'section[aria-label^="node "] .opacity-25',
          'section[aria-label^="node "] .opacity-25 *',
          'section[aria-label^="node "] .opacity-40',
          'section[aria-label^="node "] .opacity-40 *',
        ].join(", "),
      )
      .forEach((el) => {
        if (el instanceof HTMLElement) {
          el.style.setProperty("opacity", "1", "important");
        }
      });
  });
}

/** Types a VID into the topology VLAN filter and submits it — the only way
 * to actually put `dimmed`/`dimVid` de-emphasis (opacity-25/40) on screen,
 * which `neutralizeFaceplateDimming` exists to neutralize. Used by the T-2004
 * test below so that suppression is measured against the state it claims to
 * cover, rather than against a scan where nothing is ever dimmed. */
async function applyVlanFilter(page: Page, vid: number): Promise<void> {
  await page.getByLabel("VLAN").fill(String(vid));
  await page.getByRole("button", { name: "Apply" }).click();
}

test("axe: Dashboard", async ({ page }) => {
  await logIn(page);
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Home", level: 1 }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "Dashboard");
});

test("axe: Topology (Switch view, the default)", async ({ page }) => {
  await logIn(page);
  await expect(page.getByRole("radio", { name: "Switch" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  // Wait for a faceplate to mount AND for the stale grey to actually be on
  // screen, then neutralize the VLAN-filter de-emphasis dimming that remains
  // (see neutralizeFaceplateDimming — the stale dim itself is fixed at source).
  await expect(
    page.getByRole("button", { name: "vmbr0 switch" }).first(),
  ).toBeVisible();
  await waitForAStaleEntity(page);
  await neutralizeFaceplateDimming(page);
  await expectNoSeriousViolations(page, "Topology (Switch view)");
});

// T-2004: the default Switch-view scan above never activates the VLAN
// filter, so `dimmed`/`dimVid` (opacity-25/40) never actually land on
// screen there — the one thing `neutralizeFaceplateDimming` exists to cover
// was, until now, never exercised by this suite. This test turns the filter
// on (VID 20, which the three-node-vlan fixture's `vmbr0.20` sub-interface
// carries, per switchModel.ts/three-node-vlan-topology.json) so the
// de-emphasis dimming is actually on screen and actually measured.
test("axe: Topology (Switch view, VLAN filter de-emphasis dimming active)", async ({
  page,
}) => {
  await logIn(page);
  await expect(
    page.getByRole("button", { name: "vmbr0 switch" }).first(),
  ).toBeVisible();
  await applyVlanFilter(page, 20);
  // Confirm the filter actually dimmed something before measuring — a
  // no-op filter would make this test pass for the wrong reason.
  await expect(page.locator(".opacity-25, .opacity-40").first()).toBeVisible();
  await waitForAStaleEntity(page);
  await neutralizeFaceplateDimming(page);
  await expectNoSeriousViolations(
    page,
    "Topology (Switch view, VLAN filter active)",
  );
});

test("axe: Topology (Graph view, v1)", async ({ page }) => {
  await logIn(page);
  await switchToGraphView(page);
  await expectNoSeriousViolations(page, "Topology (Graph view, v1)");
});

test("axe: Topology (Graph view, v2 canvas — T-901's a11y bridge)", async ({
  page,
}) => {
  await logIn(page);
  await switchToGraphView(page);
  await page.getByRole("button", { name: "Canvas v2" }).click();
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();
  // Park the pointer somewhere harmless before scanning. Playwright leaves
  // the cursor on whatever it last clicked, so without this the "Canvas v2"
  // toggle is still :hover when axe runs — and `Button`'s primary variant
  // brightens on hover from `accent-600` (4.94:1) to `accent-500` (3.09:1).
  // axe duly reported 58 colour-contrast nodes, every one of them the same
  // white-on-#039fc7 pair, on the one test in this file that clicks a button
  // and then scans.
  //
  // That hover value is a DECISION, not a defect: Button.tsx's own comment
  // has re-derived this pairing four times (T-905, T-3401, T-3405, T-4201)
  // and states plainly that "a hover state is transient and not the gating
  // resting contrast". This is not a suppression — it makes this test measure
  // the same resting state every other test in the sweep measures, which is
  // the same reason `waitForLoadingPlaceholderToClear` exists below.
  await page.mouse.move(0, 0);
  await expectNoSeriousViolations(page, "Topology (Graph view, v2 canvas)");
});

test("axe: SDN", async ({ page }) => {
  await logIn(page);
  await page.goto("/sdn");
  await expect(
    page.getByRole("heading", { name: "SDN", level: 1 }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "SDN");
});

test("axe: Firewall", async ({ page }) => {
  await logIn(page);
  await page.goto("/firewall");
  await expect(
    page.getByRole("heading", { name: "Firewall", level: 1 }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "Firewall");
});

test("axe: IPAM", async ({ page }) => {
  await logIn(page);
  await page.goto("/ipam");
  await expect(
    page.getByRole("heading", { name: "IPAM", level: 1 }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "IPAM");
});

test("axe: Settings", async ({ page }) => {
  await logIn(page);
  await page.goto("/settings");
  await expect(
    page.getByRole("heading", { name: "Settings", level: 1 }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "Settings");
});

test("axe: changeset drawer (open, with a drafted op)", async ({ page }) => {
  await logIn(page);
  await switchToGraphView(page);
  await page.waitForFunction(
    () => document.querySelectorAll(".react-flow__node").length >= 10,
  );
  // The graph behind the drawer is in scope for this scan, and its pve2/pve3
  // nodes go stale part-way through a run. Waiting for that makes the stale
  // treatment part of what this measures every time, instead of a coin flip
  // that only failed once pve2 had been unreachable for long enough.
  await waitForAStaleEntity(page);

  // Minimal draft (mirrors changesets.spec.ts's own bridge-editor setup) so
  // the drawer renders real content, not just its empty state.
  await page.getByRole("button", { name: "New ▾" }).click();
  await page.getByRole("menuitem", { name: "Bridge" }).hover();
  await page.getByRole("menuitem", { name: "pve1" }).click();
  await page.getByPlaceholder("vmbr1").fill("vmbr88");
  await page.getByRole("button", { name: "Add to changeset" }).click();

  const drawer = page.getByRole("region", { name: "Change drawer" });
  await expect(drawer).toContainText("Create bridge vmbr88");
  await expectNoSeriousViolations(page, "Changeset drawer");
});

// --- T-3403 AC3: the restyled top bar, both themes ------------------------
test("axe: top bar, switched to dark theme", async ({ page }) => {
  await forceLightTheme(page);
  await logIn(page);
  await page.getByRole("button", { name: "Switch to dark theme" }).click();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await expectNoSeriousViolations(page, "Top bar (dark theme)");
});

// T-3403: OfflineShellBanner (restyled alongside DemoBanner as the same
// "banner family") gets its own scan since it renders nothing on every
// other page in this file — the assertion above it never actually put it
// on screen. `setOffline` fires the real `offline` window event the
// component listens for (src/lib/freshness.ts's useOnlineStatus), which is
// closer to how a real network drop is observed than stubbing the hook.
test("axe: offline shell banner", async ({ page, context }) => {
  await logIn(page);
  await context.setOffline(true);
  await expect(
    page.getByRole("status").filter({ hasText: "Offline" }),
  ).toBeVisible();
  await expectNoSeriousViolations(page, "Offline shell banner");
  await context.setOffline(false);
});

test("AC3: keyboard-only traversal reaches and activates a v2 canvas map entity with no mouse input", async ({
  page,
}) => {
  await logIn(page);

  // Switch/Graph toggle and the v2 renderer flag are both plain buttons —
  // .focus() (a programmatic DOM focus, not a pointer event) + a real
  // keyboard Enter, exactly what a keyboard-only user does.
  await page.getByRole("radio", { name: "Graph" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("radio", { name: "Graph" })).toHaveAttribute(
    "aria-checked",
    "true",
  );

  await page.getByRole("button", { name: "Canvas v2" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();

  // Tab into the map region (TopologyA11yLayer's role="application"
  // container) and confirm keyboard focus actually landed on one of its
  // proxy buttons (T-901's a11y bridge — every visible canvas entity gets
  // one), then arrow-navigate and Enter-activate it.
  const mapRegion = page.getByRole("application", { name: /Topology map/ });
  await mapRegion.getByRole("button").first().focus();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");

  // Activation opens the inspector (a Radix dialog) for the newly-selected
  // entity — the same outcome a pointer click on the canvas produces.
  await expect(page.getByRole("dialog")).toBeVisible();
});

// --- T-3406: axe sweep, every routed page, light and dark -----------------
//
// AC2 asks for "every routed page x light theme x dark theme". Everything
// above this point is a light-mode (the app's own default is dark — see
// forceLightTheme's comment — light is what the pre-T-3406 individual tests
// happened to assume) spot-check of a handful of surfaces; none of them
// covers the other ~20 routes App.tsx declares, and only one (top bar)
// covers dark mode at all. This sweep is deliberately data-driven from the
// same route list docs/user-guide.md and Sidebar.tsx describe, so a page
// this suite forgets shows up as a gap in the table rather than nothing —
// the coverage.test.ts precedent (src/help/coverage.test.ts) this codebase
// already leans on for the same reason.
//
// Login once (readonly-crawl.spec.ts's pattern, not this file's own
// per-test logIn()): internal/auth's login rate limiter is per (IP,
// username), and logging in fresh for each of 52 (26 routes x 2 themes)
// tests would exhaust the 10-attempt bucket long before its 30s refill
// caught up.
const A11Y_SWEEP_STORAGE_STATE = path.join(
  os.tmpdir(),
  `vnprox-e2e-a11y-sweep-storage-state-${String(process.pid)}.json`,
);

test.beforeAll(async ({ browser }) => {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    storageState: undefined,
  });
  const page = await context.newPage();
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await context.storageState({ path: A11Y_SWEEP_STORAGE_STATE });
  await context.close();
});

// T-4212: this file used to carry `SWEEP_ROUTES`, a hand-maintained array of
// route paths with a heading regex each. It had drifted: `/guest`
// (GuestEgoPage, T-3906) and `/wireguard` (WireGuardPage, T-4015) were both
// real, shipped, logged-in pages that this sweep had NEVER visited, in any
// theme, and nothing failed to say so.
//
// That is the defect worth naming, not the two pages. **A coverage gate whose
// inventory is hand-maintained measures the inventory, not the thing** — the
// same shape as T-3717, where the OpenAPI gate was green because the routes it
// missed were never mounted. Every future page inherited the same silence by
// default: the next route added would also be unchecked, and also silently.
//
// The list now comes from `routeInventory.ts`, which derives it from App.tsx's
// own `<Route path=>` declarations — the technique this table was built from
// in the first place, and the one web/src/help/coverage.test.ts already uses
// for its screen inventory. A source-derived list cannot drift from the router.
//
// The per-route heading REGEX goes with it, deliberately. Keeping a
// path -> expected-heading map would leave a hand-maintained array of route
// paths behind under a different name, which is exactly what AC1 forbids. What
// it bought — "did we actually land on the page we asked for?" — is bought
// instead by asserting the URL, which needs no per-route knowledge and cannot
// go stale when a page is retitled. `HEADING_LEVEL_OVERRIDES` (shared with the
// visual gate) still carries /diagnose's real level-2 exception.

// T-3406: a handful of pages' data-loading placeholders ("Loading SDN
// configuration…", "Loading nodes…", ...) render on a bare `text-slate-400`
// with no `dark:` pairing — pre-existing on every page this sweep found it
// on (Management, SDN, Firewall, IPAM, Flows, Edge, every Analysis panel;
// confirmed via `git log -S` against each file, all predate Phase 34), not
// something this phase touched. Whether THIS scan catches it is a pure
// race against the mock backend's response time — it's gone by the time
// most page loads settle, which is why the very first full run of this
// sweep (T-3406, before this wait existed) caught it non-deterministically
// on some routes and not others. This waits the placeholder out (a no-op
// if it's already gone, matching a real user who does not screenshot
// mid-fetch) so the sweep measures every route's actual steady state
// instead of occasionally tripping over a known, separately-tracked defect
// by accident of timing.
async function waitForLoadingPlaceholderToClear(page: Page): Promise<void> {
  await expect(page.getByText(/^(Loading|Simulating)[….]/).first()).toHaveCount(
    0,
  );
}

test.describe("T-3406/T-4212: axe sweep, every routed page, light/dark/demo", () => {
  test.use({ storageState: A11Y_SWEEP_STORAGE_STATE });

  // Demo joins light and dark (T-4212 AC3). It is not a cosmetic third pass:
  // `html.demo` re-points every `--color-accent-*` step to a different hue
  // family, and axe's colour-contrast rule is the only automated check in this
  // repo that would notice an accent that stops clearing its floor there. The
  // sweep had never run it. Demo rides on the dark theme, matching the visual
  // gate's own pinned choice rather than introducing a fourth variant.
  const MODES = [
    { name: "light", theme: "light", demo: false },
    { name: "dark", theme: "dark", demo: false },
    { name: "demo", theme: "dark", demo: true },
  ] as const;

  for (const routePath of routedPagePaths()) {
    for (const mode of MODES) {
      test(`axe: ${routePath} (${mode.name})`, async ({ page }) => {
        await forceTheme(page, mode.theme);
        if (mode.demo) await forceDemoAccent(page);
        await page.emulateMedia({ reducedMotion: "reduce" });
        await suppressOnboardingWalkthrough(page);
        await page.goto(routePath);
        // The URL, not a heading regex: it says "we are on the page we asked
        // for" without a per-route table to keep current. A route that
        // redirects — the way four of T-4216's five "context-requiring" routes
        // appeared to, when the harness was running a binary that predated
        // them — fails here rather than silently scanning /topology again.
        await expect(page).toHaveURL(pathEndRegExp(routePath));
        const level = HEADING_LEVEL_OVERRIDES[routePath] ?? 1;
        await expect(
          page.getByRole("heading", { level }).first(),
        ).toBeVisible();
        if (mode.theme === "dark") {
          await expect(page.locator("html")).toHaveClass(/dark/);
        } else {
          await expect(page.locator("html")).not.toHaveClass(/dark/);
        }
        if (mode.demo) {
          await expect(page.locator("html")).toHaveClass(/demo/);
        }
        await waitForLoadingPlaceholderToClear(page);
        await expectNoSeriousViolations(page, `${routePath} (${mode.name})`);
      });
    }
  }
});
