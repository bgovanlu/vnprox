// T-909: end-to-end verification of the narrow-viewport ("phone width")
// on-call triage layout against the real stack (pvemock three-node-vlan
// fixture + vnproxd + the production SPA build).
//
// Covers:
//  AC1 — Dashboard and Findings render a usable layout at phone width.
//  AC3 (Playwright half) — a desktop-only route, reached via a direct
//    navigation (not an in-app nav click, since the narrow nav rail no
//    longer even offers it), renders the explicit "desktop only"
//    affordance instead of a broken/cramped attempt at the real page.
//  AC5 — logs in, reaches Dashboard -> Findings -> a pending changeset,
//    and confirms it, all at phone width.
//
// The pending changeset is seeded directly via the API (bypassing the
// UI's staging/apply affordances) rather than through the wizard/bridge-
// editor flow other specs use: staging new ops is desktop-only per this
// task's objective, so a phone-width spec can't create one through the UI
// without contradicting the very thing it's testing. This mirrors the
// actual on-call scenario the task card names — "commit-confirm from a
// phone is the target scenario": someone else staged and applied this
// from a desktop, and the phone user's whole job here is to review and
// confirm it. mgmt-redundancy.spec.ts's narrow-viewport test covers the
// touchesMgmtPath ack-block variant of this same ceremony, staged through
// the real wizard at desktop width first.
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";

isolatedStore();

test.use({ viewport: { width: 390, height: 844 } });

// See changesets.spec.ts's identical helper doc comment: setting the
// property JS-side (not an injected <style>) sidesteps the style-src CSP.
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
  // The post-login redirect target is hardcoded to /topology (LoginPage.tsx)
  // regardless of viewport — at phone width that route immediately renders
  // the desktop-only notice instead of the real page, which is fine here:
  // this only waits for the URL, not the page content.
  await page.waitForURL("**/topology");
}

/** Every mutating request needs the double-submit `X-VNPROX-CSRF` header
 * (docs/api.md §Auth; src/api/auth.ts's `readCsrfCookie`) — the browser
 * sends the session cookie automatically, but this header has to be set
 * explicitly from the readable `vnprox_csrf` cookie the login response
 * sets. */
async function readCsrfCookie(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === "vnprox_csrf");
  if (!csrf) throw new Error("vnprox_csrf cookie not set — is the session logged in?");
  return csrf.value;
}

/** Seeds a plain (non-management-path) changeset straight into
 * `awaiting_confirm` via the API — `page.request` shares the browser
 * context's session cookie, so no separate login is needed. */
async function seedPendingChangeset(page: Page, bridgeName: string): Promise<void> {
  const csrf = await readCsrfCookie(page);
  const create = await page.request.post("/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: {
      title: `Add ${bridgeName}`,
      ops: [
        {
          op: "bridge.create",
          target: `bridge:pve1:${bridgeName}`,
          params: { ports: [], vlanAware: false, stp: false },
        },
      ],
    },
  });
  expect(create.ok(), `POST /changesets failed: ${String(create.status())} ${await create.text()}`).toBe(true);
  const cs = (await create.json()) as { id: string };

  const apply = await page.request.post(`/api/v1/changesets/${cs.id}/apply`, {
    headers: { "X-VNPROX-CSRF": csrf },
    data: { confirmTimeoutSec: 180 },
  });
  expect(apply.ok(), `POST /changesets/{id}/apply failed: ${String(apply.status())} ${await apply.text()}`).toBe(true);
}

test("phone width: Dashboard -> Findings -> a pending changeset -> confirm", async ({ page }) => {
  await logIn(page);
  await seedPendingChangeset(page, "vmbr91");

  // --- Dashboard, reached at phone width (AC1) ---------------------------
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  // The narrow nav rail only offers the reachable pages (Home, Tools) —
  // desktop-only pages aren't even dangled as a link to tap into.
  await expect(page.getByRole("link", { name: "Topology" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Settings" })).toHaveCount(0);
  const toolsLink = page.getByRole("link", { name: "Tools" });
  await expect(toolsLink).toBeVisible();

  // --- A pending changeset, surfaced by the Dashboard's own tile ---------
  // The changeset was seeded via the API, not through this browser's own
  // UI, so the drawer's client-only `activeId` doesn't already point to it
  // — PendingChangesetsTile.tsx (T-904) is exactly the "someone else
  // (or a script) applied this — open it" affordance: it lists every
  // non-terminal changeset from the server (not just this session's own),
  // and clicking one sets it active. This is the realistic on-call
  // discovery path, not a shortcut around it.
  await page
    .getByRole("region", { name: "Pending changesets" })
    .getByRole("button", { name: "Add vmbr91" })
    .click();
  const countdown = page.getByRole("alert").filter({ has: page.getByRole("button", { name: "Confirm" }) });
  await expect(countdown).toBeVisible({ timeout: 30_000 });

  // --- Findings (AC1) — and the countdown survives the navigation --------
  await toolsLink.click();
  await page.waitForURL("**/tools");
  await expect(page.getByRole("heading", { name: "Findings", level: 1 })).toBeVisible();
  // The rest of Tools (simulator, raw editor, MAC/FDB, fw log, export) is
  // gone, replaced by an explicit affordance rather than half-rendering.
  await expect(page.getByText(/needs a larger screen/i)).toBeVisible();
  await expect(page.getByRole("button", { name: /Verify live/i })).toHaveCount(0);
  // The countdown banner is mounted app-wide (not scoped to a route), so
  // it's still visible here without any extra action (AC5).
  await expect(countdown).toBeVisible();

  await countdown.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: /applied and committed/i }),
  ).toBeVisible({ timeout: 30_000 });
});

test("phone width: a desktop-only route reached via a direct link shows the explicit affordance, not a broken layout", async ({ page }) => {
  await logIn(page);

  // A direct navigation (not an in-app nav click) to a desktop-only route
  // — the SDN page (zone/vnet wizards + editing) — per AC3.
  await page.goto("/sdn");

  await expect(page.getByText("SDN needs a larger screen")).toBeVisible();
  await expect(page.getByRole("link", { name: "Go to Dashboard" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Go to Findings" })).toBeVisible();
  await expect(page.getByText(/confirm\/roll back controls still work/i)).toBeVisible();

  // Not a broken/cramped attempt at the real page — the SDN tree never
  // mounts at all.
  await expect(page.getByRole("tree")).toHaveCount(0);

  // The affordance is actionable: it actually gets you to a reachable page.
  await page.getByRole("link", { name: "Go to Findings" }).click();
  await page.waitForURL("**/tools");
  await expect(page.getByRole("heading", { name: "Findings", level: 1 })).toBeVisible();
});
