// T-2901 AC2/AC3: the browser-level assertions that would have caught the
// v4.0.0 CSP defect. T-604's policy pinned worker-src/manifest-src 'none'
// before T-2005 existed; every Go-level test kept passing while a real
// browser refused the service worker and the manifest outright — the
// installable app and push shipped dead. These specs run the real stack
// (pvemock → vnproxd → production SPA build) in Chromium, so a CSP that
// blocks sw.js or manifest.webmanifest fails HERE, not in a field report.
import { expect, test, type Page } from "@playwright/test";

// Chromium fetches a service worker's script over its own network path that
// does NOT honor Playwright's ignoreHTTPSErrors (a page-level bypass): under
// the e2e stack's self-signed cert, register() rejects with "An SSL
// certificate error occurred when fetching the script" while every page
// fetch sails through. --ignore-certificate-errors is browser-wide and does
// cover it. Test-environment accommodation only — production serves the
// node's real PVE certificate.
test.use({ launchOptions: { args: ["--ignore-certificate-errors"] } });

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("T-2901 AC2: the service worker registers and activates under the shipped CSP", async ({ page }) => {
  await logIn(page);
  // registerServiceWorker() runs from main.tsx on every load; under a
  // worker-src that excludes 'self' the register() promise rejects with a
  // SecurityError and .ready never resolves — which is exactly the defect
  // shape this asserts against, so give it a hard timeout rather than the
  // suite default.
  const state = await page.evaluate(async () => {
    if (!("serviceWorker" in navigator)) {
      return "unsupported";
    }
    const reg = await Promise.race([
      navigator.serviceWorker.ready,
      new Promise<null>((resolve) => {
        setTimeout(() => {
          resolve(null);
        }, 15_000);
      }),
    ]);
    if (reg === null) {
      return "timed-out (worker-src blocking sw.js?)";
    }
    return reg.active?.state ?? "registered-but-no-active-worker";
  });
  expect(state).toBe("activated");
});

test("T-2901 AC2: the web app manifest is served and CSP-admissible", async ({ page }) => {
  await logIn(page);
  // The <link rel="manifest"> the PWA installability criteria read.
  const href = await page.locator('link[rel="manifest"]').getAttribute("href");
  if (href === null) {
    throw new Error("index.html has no <link rel=\"manifest\">");
  }
  // Served with a manifest media type (spa.go's mime registration), not a
  // sniffed text/plain — and reachable at all, which manifest-src 'none'
  // forbade.
  const resp = await page.request.get(href);
  expect(resp.status()).toBe(200);
  expect(resp.headers()["content-type"]).toContain("application/manifest+json");
  const manifest = (await resp.json()) as { name?: string; start_url?: string };
  expect(manifest.name).toBeTruthy();
  expect(manifest.start_url).toBeTruthy();
});

test("T-2901 AC3: the /embed/ views are frameable and render; the app itself stays unframeable", async ({ page }) => {
  await logIn(page);

  // Mint a read-only embed token through the real API (one-time reveal).
  // CSRF is the double-submit cookie (vnprox_csrf → X-VNPROX-CSRF, per
  // web/src/api/auth.ts), and an embed token requires at least one
  // read-only scope.
  const minted = await page.evaluate(async () => {
    const csrf =
      document.cookie
        .split("; ")
        .find((row) => row.startsWith("vnprox_csrf="))
        ?.slice("vnprox_csrf=".length) ?? "";
    const r = await fetch("/api/v1/embed/tokens", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-VNPROX-CSRF": csrf },
      body: JSON.stringify({ name: "e2e-pwa-spec", scopes: ["netRead"] }),
    });
    return { status: r.status, body: (await r.json()) as { token?: string } };
  });
  expect(minted.status).toBe(201); // POST /embed/tokens creates — 201 per docs/api.md
  const token = minted.body.token;
  if (token === undefined || token === "") {
    throw new Error("embed token mint returned no token");
  }
  const embedURL = `/embed/map?token=${encodeURIComponent(token)}`;

  // Header shape: the embed route must carry frame-ancestors 'self' (not
  // 'none') and no X-Frame-Options — before T-2901, DENY + 'none' forbade
  // every embedding this feature exists for. An app route keeps both locks.
  const embedResp = await page.request.get(embedURL);
  expect(embedResp.status()).toBe(200);
  expect(embedResp.headers()["x-frame-options"]).toBeUndefined();
  expect(embedResp.headers()["content-security-policy"]).toContain("frame-ancestors 'self'");
  const appResp = await page.request.get("/topology");
  expect(appResp.headers()["x-frame-options"]).toBe("DENY");
  expect(appResp.headers()["content-security-policy"]).toContain("frame-ancestors 'none'");

  // Render: the embed shell mounts with only the token (no session). Loaded
  // top-level rather than inside an <iframe>: every same-origin page —
  // including the app and the embeds themselves — pins `frame-src 'none'`,
  // so no same-origin parent can host an iframe AT ALL, by design. The
  // real embedding consumer is an external origin (a wiki, a NOC page),
  // whose own CSP governs its frames; OUR half of that handshake is
  // exactly the two headers asserted above, plus the shell rendering,
  // asserted here in a session-free context.
  const embedPage = await page.context().newPage();
  try {
    await embedPage.context().clearCookies();
    await embedPage.goto(embedURL);
    await expect(embedPage.locator("#root > *").first()).toBeVisible({ timeout: 15_000 });
  } finally {
    await embedPage.close();
  }
});
