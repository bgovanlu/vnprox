// T-2801 demo mode, end to end against a real `vnproxd --demo`.
//
// This file exists because two of the card's acceptance criteria say so in
// as many words:
//
//   AC1 "...asserted end-to-end in the e2e suite, not by unit test"
//   AC6 "...asserted by the e2e suite sweeping all routes"
//
// The daemon under test is the `demo` stack (web/e2e/shards.ts): the ONLY
// stack in the suite with no mock server process. `vnproxd --demo` serves
// its synthetic cluster from inside itself, over an http.RoundTripper with
// no dialer (internal/demo/transport.go). That absence is the subject, not
// an optimisation — a demo daemon that needed a pvemock on a port would not
// be demonstrating "no PVE and no network access".
//
// Port 24007 is written as a literal, per shards.ts's header: computed
// ports hide from internal/devports' registry scan, which is the failure
// the registry exists to prevent.
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import * as os from "node:os";
import * as path from "node:path";

const DEMO_URL = "https://127.0.0.1:24007";

test.use({ baseURL: DEMO_URL });

const DEMO_STORAGE_STATE = path.join(os.tmpdir(), `vnprox-e2e-demo-storage-state-${String(process.pid)}.json`);

// Log in once for the whole file and reuse the session, as readonly-crawl
// does: internal/auth's login limiter is per (IP, username), and this file
// visits ~15 routes.
test.beforeAll(async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, storageState: undefined, baseURL: DEMO_URL });
  const page = await context.newPage();
  await page.goto("/login");
  // The demo fixture's own built-in superuser (internal/demo/dataset/
  // cluster.yaml `users:`). There is no other credential store — a demo
  // login is a real PVE ticket login against the embedded cluster.
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await context.storageState({ path: DEMO_STORAGE_STATE });
  await context.close();
});

test.describe("T-2801 demo mode", () => {
  test.use({ storageState: DEMO_STORAGE_STATE });

  // --- AC1 ------------------------------------------------------------

  // The demo daemon is reachable and says what it is, on the one
  // unauthenticated route. This is also the assertion the banner depends
  // on: the SPA reads /health before login.
  test("the daemon reports demo mode on the unauthenticated health route", async ({ request }) => {
    const res = await request.get(`${DEMO_URL}/api/v1/health`);
    expect(res.status()).toBe(200);
    expect(res.headers()["x-vnprox-demo"]).toBe("1");
    const body: unknown = await res.json();
    expect(body).toMatchObject({ status: "ok", demo: true });
  });

  test("the topology renders a populated multi-node map", async ({ page }) => {
    await page.goto("/topology");
    await page.getByRole("radio", { name: "Graph" }).click();
    // elkjs lays the graph out asynchronously; a bare node-count check can
    // pass against a pile of nodes stacked at the origin. Wait for them to
    // have been spread out, exactly as topology.spec.ts and
    // readonly-crawl.spec.ts do.
    await page.waitForFunction(() => {
      const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
      const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
      return nodes.length >= 10 && transforms.size > nodes.length / 2;
    });

    // All three synthetic nodes, not just the one the daemon calls local.
    // A demo daemon has no peers (peer fan-out would dial the fixture's own
    // addresses over the real network); every node's host state comes from
    // the same cluster-wide fixture reader. If that wiring regressed, pve2
    // and pve3 would be missing here and nowhere else.
    for (const node of ["pve1", "pve2", "pve3"]) {
      await expect(page.getByText(node, { exact: false }).first()).toBeVisible();
    }
  });

  test("findings and drift are populated", async ({ page, request }) => {
    // The screen first: the operator-visible surface is what AC1 names.
    await page.goto("/tools");
    await expect(page.getByRole("main")).toBeVisible();
    const findings = page.getByRole("list", { name: "Findings" });
    await expect(findings).toBeVisible();
    await expect(findings.getByRole("listitem").first()).toBeVisible();

    // Then the API, so a failure says WHICH of the demo dataset's
    // deliberate imperfections stopped being detected rather than only
    // "the list was empty". Both families are enumerated in the fixture's
    // own `mess:` list.
    const res = await request.get(`${DEMO_URL}/api/v1/drift`);
    expect(res.status()).toBe(200);
    const drift = (await res.json()) as { check?: string }[];
    const checks = new Set(drift.map((d) => d.check));
    expect(checks, "the demo dataset's staged interfaces.new no longer produces drift").toContain("pending_interfaces");
    expect(checks, "the demo dataset's diverged bridge MTU no longer produces drift").toContain("mtu_consistency");
  });

  test("the flow explorer renders populated rows", async ({ page }) => {
    await page.goto("/flows");
    await expect(page.getByRole("main")).toBeVisible();
    // The empty state names itself; assert its absence explicitly rather
    // than inferring it from a row count, so a UI that renders a table of
    // placeholder rows cannot pass.
    await expect(page.getByText("No flow records yet")).toHaveCount(0);
    const rows = page.getByRole("row");
    await expect.poll(async () => rows.count(), { timeout: 30_000 }).toBeGreaterThan(1);
  });

  // --- AC2, at the edge -------------------------------------------------
  //
  // The store-checksum proof of "touches nothing" lives in
  // internal/api/demo_test.go, where a checksum over every table is
  // possible. What the e2e adds is that the SHIPPED daemon, started with
  // the real flag, actually behaves that way — and that the changeset it
  // was told to create genuinely does not exist afterwards.
  test("a mutating API answers what it would have done, and creates nothing", async ({ request }) => {
    const changesets = async (): Promise<unknown> => (await request.get(`${DEMO_URL}/api/v1/changesets`)).json();
    const before: unknown = await changesets();

    const res = await request.post(`${DEMO_URL}/api/v1/changesets`, {
      data: { title: "e2e demo write", ops: [] },
    });
    expect(res.status()).toBe(200);
    const body = (await res.json()) as { demo?: { mode?: string; method?: string; path?: string } };
    expect(body.demo?.mode).toBe("demo");
    expect(body.demo?.method).toBe("POST");
    expect(body.demo?.path).toBe("/api/v1/changesets");

    const after: unknown = await changesets();
    expect(after, "a changeset was created in demo mode").toEqual(before);
  });

  // --- AC3, direction two ----------------------------------------------
  //
  // Direction one (a demo daemon refusing to START against a configured
  // PVE endpoint) cannot be asserted from a browser — it is a process that
  // exits before it binds anything. It is covered, table-driven over every
  // [pve] key, in internal/config/demo_test.go.
  test("a real endpoint cannot be configured while in demo mode", async ({ request }) => {
    const res = await request.post(`${DEMO_URL}/api/v1/federation/clusters`, {
      data: { name: "production", apiUrl: "https://pve.example.com:8006" },
    });
    expect(res.status()).toBe(403);
    const body = (await res.json()) as { error?: { code?: string } };
    expect(body.error?.code).toBe("demo_real_endpoint_refused");
  });
});

// --- AC6: the banner, on every screen ----------------------------------
//
// "Sweeping all routes" is taken literally: every path App.tsx routes,
// including the login screen (which is outside AppShell and the first thing
// a visitor sees) and the token-scoped embed frames (which are screens
// someone put on a wall).
//
// Selected by data-testid and not by its text: LoginPage already contains
// the words "Demo mode" in the unrelated AUTH_STUB affordance, and an
// assertion that passes on the wrong element is worse than none.
const BANNER = "demo-banner";

const AUTHENTICATED_ROUTES = [
  "/",
  "/topology",
  "/management",
  "/guests",
  "/sdn",
  "/firewall",
  "/ipam",
  "/flows",
  "/conntrack",
  "/edge",
  "/diagnose",
  "/ports",
  "/blueprints",
  "/hub",
  "/history",
  "/incidents",
  "/audit",
  "/tools",
  "/settings",
  "/settings/alert-rules",
  "/settings/certificates",
  "/settings/federation",
];

async function expectBanner(page: Page, where: string): Promise<void> {
  const banner = page.getByTestId(BANNER);
  await expect(banner, `no demo banner on ${where}`).toBeVisible();
  await expect(banner).toContainText("synthetic cluster");
}

test.describe("T-2801 AC6: the demo banner is on every screen", () => {
  test.use({ storageState: DEMO_STORAGE_STATE });

  for (const route of AUTHENTICATED_ROUTES) {
    test(`banner on ${route}`, async ({ page }) => {
      await page.goto(route);
      await expect(page.getByRole("main")).toBeVisible();
      await expectBanner(page, route);
    });
  }
});

test.describe("T-2801 AC6: the banner is there before login too", () => {
  // No storageState: this is the screen a visitor sees first, and a demo
  // that only announces itself after login has let someone type
  // credentials at what they believed was their own cluster.
  test.use({ storageState: { cookies: [], origins: [] } });

  test("banner on /login", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await expectBanner(page, "/login");
  });
});

// --- T-3403 AC3: the restyled dark test-mode bar, both app themes --------
//
// DemoBanner.tsx's restyle is deliberately theme-INDEPENDENT (no `dark:`
// pairing — see that file's own comment): the bar is the same dark-navy
// surface whether the app chrome around it is in light or dark mode. That
// makes "both themes" a claim about the SAME element rendering correctly
// regardless of the `<html class="dark">` toggle, rather than two different
// colour pairings to verify separately — but it still has to actually be
// checked, not assumed, per this codebase's own axe-or-it-didn't-happen
// rule (docs/development.md's "Visual language" section).
async function expectNoSeriousViolations(page: Page, label: string): Promise<void> {
  const results = await new AxeBuilder({ page }).analyze();
  const blocking = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
  expect(blocking, `${label}: ${JSON.stringify(blocking, null, 2)}`).toEqual([]);
}

test.describe("T-3403 AC3: the test-mode bar in both themes", () => {
  test.use({ storageState: DEMO_STORAGE_STATE });

  test("axe: demo bar, app in light theme (the default)", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/topology");
    await expect(page.getByRole("main")).toBeVisible();
    await expectBanner(page, "/topology (light)");
    await expect(page.locator("html")).not.toHaveClass(/dark/);
    await expectNoSeriousViolations(page, "demo bar, light theme");
  });

  test("axe: demo bar, app switched to dark theme", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/topology");
    await expect(page.getByRole("main")).toBeVisible();
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expectBanner(page, "/topology (dark)");
    await expectNoSeriousViolations(page, "demo bar, dark theme");
  });
});
