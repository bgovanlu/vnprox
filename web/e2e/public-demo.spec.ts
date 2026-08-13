// T-2802 the hosted read-only demo, end to end against a real
// `vnproxd --demo --public-demo`.
//
// The daemon under test is the `publicdemo` stack (web/e2e/shards.ts): the
// second of the suite's two stacks with no mock server process, and a
// separate daemon from T-2801's on purpose — the two answer the same routes
// differently, so one process cannot be both.
//
// WHAT IS ASSERTED HERE AND WHAT IS NOT. AC1's full enumeration — every
// route in docs/openapi.json, driven directly — is a Go test
// (cmd/vnproxd/publicdemo_test.go), because 430 HTTP round trips through a
// browser harness would buy nothing over 430 through an http.Client and
// cost minutes. What this file adds is the part a Go test cannot: the
// SHIPPED SPA, in a browser, with the edge in front of it — a visitor who
// never logs in, a tour that completes, and two real browser contexts that
// cannot see each other.
//
// Port 25007 is written as a literal, per shards.ts's header.
import { expect, test, type Page } from "@playwright/test";

const DEMO_URL = "https://127.0.0.1:25007";

test.use({ baseURL: DEMO_URL });

// No storageState, and no login step anywhere in this file. That absence is
// an assertion: a public demo has no login screen, and POST /auth/login is
// refused like every other write. The edge mints a session per visitor.
test.use({ storageState: { cookies: [], origins: [] } });

test.describe("T-2802 AC1: writes are refused at the edge", () => {
  test("a visitor is served without ever logging in", async ({ request }) => {
    const res = await request.get(`${DEMO_URL}/api/v1/auth/me`);
    expect(res.status(), "a visitor had to log in to a public demo").toBe(200);
    expect(res.headers()["x-vnprox-public-demo"]).toBe("1");
  });

  test("the login route itself is refused", async ({ request }) => {
    const res = await request.post(`${DEMO_URL}/api/v1/auth/login`, {
      data: { username: "root", password: "vnprox-mock", realm: "pam" },
    });
    expect(res.status()).toBe(403);
    expect(res.headers()["x-vnprox-public-demo-refused"]).toBe("public_demo_read_only");
  });

  // T-2801's demo daemon answers this one 200 with a "would have" body. The
  // difference between the two is the whole of this card's first bullet.
  test("a mutating API is refused before the daemon sees it", async ({ request }) => {
    const before = (await (await request.get(`${DEMO_URL}/api/v1/changesets`)).json()) as unknown;

    const res = await request.post(`${DEMO_URL}/api/v1/changesets`, { data: { title: "e2e public demo write", ops: [] } });
    expect(res.status()).toBe(403);
    expect(res.headers()["x-vnprox-public-demo-refused"]).toBe("public_demo_read_only");
    const body = (await res.json()) as { error?: { code?: string } };
    expect(body.error?.code).toBe("public_demo_read_only");

    const after = (await (await request.get(`${DEMO_URL}/api/v1/changesets`)).json()) as unknown;
    expect(after, "a changeset was created on a read-only public demo").toEqual(before);
  });

  test("reads are served — without which the refusals above prove nothing", async ({ request }) => {
    const res = await request.get(`${DEMO_URL}/api/v1/topology`);
    expect(res.status()).toBe(200);
    expect(res.headers()["x-vnprox-public-demo-refused"]).toBeUndefined();

    // Polled, not sampled once: the first collector cycle may not have run
    // when this spec's turn comes up, and "the read was refused" and "the
    // read was empty for a moment" must not fail the same way.
    await expect
      .poll(
        async () => {
          const body = (await (await request.get(`${DEMO_URL}/api/v1/topology`)).json()) as { nodes?: unknown[] };
          return body.nodes?.length ?? 0;
        },
        { timeout: 60_000 },
      )
      .toBeGreaterThan(10);
  });

  test("it is still unmistakably a demo", async ({ page }) => {
    await page.goto("/topology");
    await expect(page.getByTestId("demo-banner")).toBeVisible();
  });
});

// --- AC2: the tour ---------------------------------------------------------

const TOUR = "guided-tour";
const TOUR_STEP = "guided-tour-step";
const TOUR_STEPS = 6;

async function openApp(page: Page): Promise<void> {
  await page.goto("/topology");
  await expect(page.getByRole("main")).toBeVisible();
}

test.describe("T-2802 AC2: the tour completes end to end", () => {
  test("six steps, no cluster, no login", async ({ page }) => {
    await openApp(page);

    const panel = page.getByTestId(TOUR);
    await expect(panel).toBeVisible();
    await expect(page.getByTestId(TOUR_STEP)).toHaveText(`1/${String(TOUR_STEPS)}`);

    for (let step = 1; step <= TOUR_STEPS; step++) {
      await expect(page.getByTestId(TOUR_STEP)).toHaveText(`${String(step)}/${String(TOUR_STEPS)}`);
      // Each stop navigates to its own screen; assert the screen rendered
      // before advancing, or "the tour completes" would be satisfied by six
      // clicks on a panel floating over a blank app.
      await expect(page.getByRole("main")).toBeVisible();
      const label = step === TOUR_STEPS ? "Finish" : "Next";
      await panel.getByRole("button", { name: label }).click();
    }

    await expect(panel).toBeHidden();
    await expect(page.getByTestId("guided-tour-reopen")).toBeVisible();
  });

  test("a step can be skipped and the tour resumed after a reload", async ({ page }) => {
    await openApp(page);
    const panel = page.getByTestId(TOUR);
    await expect(panel).toBeVisible();

    await panel.getByRole("button", { name: "Skip" }).click();
    await expect(page.getByTestId(TOUR_STEP)).toHaveText(`2/${String(TOUR_STEPS)}`);
    await panel.getByRole("button", { name: "Next" }).click();
    await expect(page.getByTestId(TOUR_STEP)).toHaveText(`3/${String(TOUR_STEPS)}`);

    // Resumable: the place is in the visitor's own scratch state at the
    // edge, not in the component, so a reload finds it again.
    await page.reload();
    await expect(page.getByRole("main")).toBeVisible();
    await expect(page.getByTestId(TOUR_STEP)).toHaveText(`3/${String(TOUR_STEPS)}`);

    // Minimising keeps it too.
    await panel.getByRole("button", { name: "Minimize guided tour" }).click();
    const pill = page.getByTestId("guided-tour-reopen");
    await expect(pill).toContainText(`3/${String(TOUR_STEPS)}`);
    await pill.click();
    await expect(page.getByTestId(TOUR_STEP)).toHaveText(`3/${String(TOUR_STEPS)}`);
  });
});

// --- AC3: per-visitor state ------------------------------------------------

test.describe("T-2802 AC3: one visitor's state is invisible to another", () => {
  test("two browsers, two tours", async ({ browser }) => {
    const first = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: DEMO_URL });
    const second = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: DEMO_URL });
    try {
      const a = await first.newPage();
      const b = await second.newPage();

      await openApp(a);
      await expect(a.getByTestId(TOUR_STEP)).toHaveText(`1/${String(TOUR_STEPS)}`);
      await a.getByTestId(TOUR).getByRole("button", { name: "Next" }).click();
      await a.getByTestId(TOUR).getByRole("button", { name: "Next" }).click();
      await expect(a.getByTestId(TOUR_STEP)).toHaveText(`3/${String(TOUR_STEPS)}`);

      // The assertion: B, arriving after A got to step 3, starts at 1.
      await openApp(b);
      await expect(b.getByTestId(TOUR_STEP)).toHaveText(`1/${String(TOUR_STEPS)}`);

      // The control leg: A still has their own place after B arrived.
      await a.reload();
      await expect(a.getByRole("main")).toBeVisible();
      await expect(a.getByTestId(TOUR_STEP)).toHaveText(`3/${String(TOUR_STEPS)}`);
    } finally {
      await first.close();
      await second.close();
    }
  });

  test("two visitors' layouts do not mix", async ({ playwright }) => {
    const a = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    const b = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    try {
      // Each request context has its own cookie jar, so each is a distinct
      // visitor with a distinct session, not one visitor with two headers.
      const sessionA = (await (await a.get("/demo/visitor/session")).json()) as { visitor: string };
      const sessionB = (await (await b.get("/demo/visitor/session")).json()) as { visitor: string };
      expect(sessionA.visitor).not.toBe(sessionB.visitor);

      const saved = await a.put("/demo/visitor/state/topology", { data: { state: { positions: { pve1: { x: 12, y: 34 } } } } });
      expect(saved.status()).toBe(200);

      expect((await b.get("/demo/visitor/state/topology")).status(), "visitor B could see visitor A's layout").toBe(404);
      const readback = await a.get("/demo/visitor/state/topology");
      expect(readback.status(), "visitor A could not read back their own layout").toBe(200);
      expect(await readback.text()).toContain('"x":12');

      // And the daemon's own layout route, which is what this surface
      // exists because of, stays refused.
      const refused = await a.put("/api/v1/layouts/topology", { data: { layout: {} } });
      expect(refused.status()).toBe(403);
    } finally {
      await a.dispose();
      await b.dispose();
    }
  });
});

// --- AC4: caps -------------------------------------------------------------

test.describe("T-2802 AC4: a cap degrades one session, not the instance", () => {
  test("a visitor who floods is throttled while another is served", async ({ playwright }) => {
    const hostile = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    const bystander = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    try {
      // The control leg first: the bystander is served BEFORE the flood, so
      // a 200 afterwards means "still served" rather than "served once".
      expect((await bystander.get("/api/v1/health")).status()).toBe(200);

      // DefaultCaps: a 120-request burst refilling every 500ms. Two hundred
      // back-to-back requests outruns it; an ordinary visitor never does.
      let throttled = 0;
      for (let i = 0; i < 200; i++) {
        const res = await hostile.get("/api/v1/health");
        if (res.status() === 429) {
          expect(res.headers()["x-vnprox-public-demo-refused"]).toBe("public_demo_rate_limited");
          throttled++;
        }
      }
      expect(throttled, "200 back-to-back requests were never throttled; the per-visitor cap is not enforced").toBeGreaterThan(0);

      // The assertion AC4 is actually making.
      const res = await bystander.get("/api/v1/health");
      expect(res.status(), "one visitor's flood degraded another visitor's session").toBe(200);
      expect(res.headers()["x-vnprox-public-demo-refused"]).toBeUndefined();
    } finally {
      await hostile.dispose();
      await bystander.dispose();
    }
  });

  test("a visitor's scratch state is capped, and the refusal is theirs alone", async ({ playwright }) => {
    const a = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    const b = await playwright.request.newContext({ baseURL: DEMO_URL, ignoreHTTPSErrors: true });
    try {
      const caps = (await (await a.get("/demo/visitor/session")).json()) as { caps: { maxStateBytes: number } };
      const oversized = "x".repeat(caps.caps.maxStateBytes + 1024);

      expect((await a.put("/demo/visitor/state/small", { data: { state: { ok: true } } })).status()).toBe(200);
      const refused = await a.put("/demo/visitor/state/fat", { data: { state: oversized } });
      expect(refused.status()).toBe(413);
      expect(refused.headers()["x-vnprox-public-demo-refused"]).toBe("public_demo_state_too_large");

      // Nothing already stored was lost, and the other visitor never noticed.
      expect((await a.get("/demo/visitor/state/small")).status()).toBe(200);
      expect((await b.put("/demo/visitor/state/small", { data: { state: { ok: true } } })).status()).toBe(200);
    } finally {
      await a.dispose();
      await b.dispose();
    }
  });
});
