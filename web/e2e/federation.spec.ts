// T-1202 AC4/AC5: global cross-cluster topology, drill-down, and the
// cluster-namespaced command palette against the REAL stack. Reuses two
// pvemock instances that playwright.config.ts already starts as two
// genuinely-distinct attached clusters — no new server needed:
//
//   - cluster "east"  -> http://127.0.0.1:8006  (three-node-vlan.yaml)
//   - cluster "west"  -> http://127.0.0.1:38006 (single-node.yaml, guest web01)
//
// Both are attached to the primary vnproxd on 8007 via POST
// /federation/clusters (a raw API call — the attach UI/wizard is T-1201's
// registry surface, out of this card's scope), exactly as k8s-overlay.spec.ts
// registers its mock cluster.
//
// Scenario (a): land on the global capsule view (>=2 clusters), drill into
// cluster "west", confirm its topology behaves like the single-cluster
// baseline (Switch/Graph toggle, layer toggles, saved views).
// Scenario (b): the command palette search finds an entity (web01) that lives
// only in cluster "west" and switches active context to it.
import { expect, request, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

const BASE = "https://127.0.0.1:8007";
const CLUSTER_EAST_URL = "http://127.0.0.1:8006";
const CLUSTER_WEST_URL = "http://127.0.0.1:38006";

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
  await page.goto(BASE + "/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  // With zero clusters attached at first login, the landing page is the
  // ordinary topology page — federation is still invisible.
  await page.waitForURL("**/topology");
}

async function readCsrfCookie(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === "vnprox_csrf");
  if (!csrf) throw new Error("vnprox_csrf cookie not set — is the session logged in?");
  return csrf.value;
}

/** Attaches one cluster via POST /federation/clusters, returning its id. */
async function attachCluster(page: Page, name: string, apiUrl: string): Promise<string> {
  const csrf = await readCsrfCookie(page);
  const res = await page.request.post(BASE + "/api/v1/federation/clusters", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: {
      name,
      apiUrl,
      credential: { kind: "ticket", username: "root@pam", password: "vnprox-mock" },
    },
  });
  expect(res.ok(), `attach ${name} failed: ${String(res.status())} ${await res.text()}`).toBe(true);
  const body = (await res.json()) as { id: string };
  return body.id;
}

async function detachCluster(page: Page, id: string): Promise<void> {
  const csrf = await readCsrfCookie(page);
  await page.request.delete(BASE + `/api/v1/federation/clusters/${id}`, { headers: { "X-VNPROX-CSRF": csrf } });
}

/** Polls GET /federation/topology directly until both attached clusters are
 * reachable — proof the aggregator can fan out before the UI is driven. */
async function waitForFederationReady(): Promise<void> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(BASE + "/api/v1/auth/login", {
      data: { username: "root@pam", password: "vnprox-mock" },
    });
    expect(login.ok()).toBe(true);
    const deadline = Date.now() + 60_000;
    for (;;) {
      const res = await ctx.get(BASE + "/api/v1/federation/topology");
      if (res.ok()) {
        const body = (await res.json()) as { clusters: { reachable: boolean }[] };
        if (body.clusters.length >= 2 && body.clusters.every((c) => c.reachable)) return;
      }
      if (Date.now() > deadline) throw new Error("federation clusters never became reachable");
      await new Promise((r) => setTimeout(r, 1000));
    }
  } finally {
    await ctx.dispose();
  }
}

test.describe.configure({ mode: "serial" });

test("global map: capsules with two clusters, drill into one, palette switches context", async ({ page }) => {
  await logIn(page);

  const eastId = await attachCluster(page, "east", CLUSTER_EAST_URL);
  const westId = await attachCluster(page, "west", CLUSTER_WEST_URL);

  try {
    await waitForFederationReady();

    // (a) Land on the global capsule view — a capsule per attached cluster.
    await page.goto(BASE + "/topology");
    await expect(page.getByRole("region", { name: "Global cluster map" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Open cluster east" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Open cluster west" })).toBeVisible();

    // Drill into cluster "west" — its ordinary topology, with a back link.
    await page.getByRole("button", { name: "Open cluster west" }).click();
    await expect(page).toHaveURL(new RegExp(`cluster=${westId}`));
    await expect(page.getByRole("button", { name: /Global map/ })).toBeVisible();

    // Baseline behaviours: Switch/Graph toggle and layer toggles work exactly
    // as the single-cluster page.
    await expect(page.getByRole("radio", { name: "Switch" })).toBeVisible();
    await switchToGraphView(page);
    await expect(page.locator(".react-flow__node").first()).toBeVisible();

    // Back to the global map.
    await page.getByRole("button", { name: /Global map/ }).click();
    await expect(page.getByRole("region", { name: "Global cluster map" })).toBeVisible();

    // (b) Palette search finds web01 (only in cluster "west") and switches
    // context to it.
    await page.keyboard.press("Control+k");
    await page.getByLabel("Command palette input").fill("web01");
    const hit = page.getByRole("button", { name: /web01/ });
    await expect(hit).toBeVisible();
    await hit.click();
    await expect(page).toHaveURL(new RegExp(`cluster=${westId}`));
  } finally {
    await detachCluster(page, eastId);
    await detachCluster(page, westId);
  }
});
