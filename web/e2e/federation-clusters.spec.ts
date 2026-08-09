// T-2001 AC1/AC2/AC3: the federation cluster editor UI end to end against
// the real stack — /federation/clusters has had full CRUD, audit coverage,
// and capability gating since T-1201 with no UI at all until this task.
// Reuses the already-running "west" pvemock instance (single-node.yaml,
// http://127.0.0.1:38006) playwright.config.ts's default webServer fleet
// already starts for federation.spec.ts's own scenario — no new server
// pair needed (docs/development.md's port ladder is already fully spoken
// for; see this task's own report for the "don't add a new webServer"
// warning this repeats from a prior round's mistake).
//
// Attach -> edit (rename) -> edit (re-credential, asserting the credential
// is never rendered back into the DOM) -> detach (with the confirmation
// dialog naming what is lost). wgTunnelSource's explicit-vs-peer display
// and the "clearing doesn't unlink" copy are exercised by
// FederationClusters.test.tsx's mocked-data Vitest suite instead — this
// spec's job is proving the real POST/PUT/DELETE wiring, session/CSRF, and
// capability gating end to end, which nothing else in this task's test
// suite exercises against a live daemon.
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";

isolatedStore();

const CLUSTER_API_URL = "http://127.0.0.1:38006";

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
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test.describe.configure({ mode: "serial" });

test.describe("T-2001 federation cluster editor", () => {
  test("attach, rename, re-credential (never rendered back), and detach entirely through the UI", async ({ page }) => {
    const clusterName = `e2e-cluster-${String(Date.now())}`;
    const renamedName = `${clusterName}-renamed`;

    await logIn(page);
    await page.goto("/settings/federation");
    await page.getByRole("heading", { name: "Federated clusters" }).waitFor();

    // --- Attach --------------------------------------------------------
    await page.getByRole("button", { name: "Attach cluster", exact: true }).click();
    await page.getByLabel("Name", { exact: true }).fill(clusterName);
    await page.getByLabel("API URL", { exact: true }).fill(CLUSTER_API_URL);
    await page.getByLabel("Username", { exact: true }).fill("root@pam");
    await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
    await page.getByRole("button", { name: "Attach", exact: true }).click();

    const listItem = page.getByRole("button", { name: new RegExp(clusterName) });
    await expect(listItem).toBeVisible();

    // --- Edit: rename ----------------------------------------------------
    await listItem.click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue(clusterName);
    // The credential fields never rehydrate from the fetched cluster — the
    // API never returns it (AC2).
    await expect(page.getByLabel("Username", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");

    await page.getByLabel("Name", { exact: true }).fill(renamedName);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("button", { name: new RegExp(renamedName) })).toBeVisible();

    // --- Edit: re-credential, still never rendered back (AC2) -----------
    await page.getByLabel("Username", { exact: true }).fill("root@pam");
    await page.getByLabel("Password", { exact: true }).fill("a-different-mock-secret");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    // The rename step above may still have its own toast on screen (5s
    // auto-dismiss) — match the most recent "Cluster saved" toast rather
    // than requiring exactly one.
    await expect(page.getByText("Cluster saved").last()).toBeVisible();

    // After the save round-trips, the form's credential fields are blank
    // again and the raw page HTML never contains the secret we just typed
    // — the write-only guarantee holds against the real response, not
    // just a mocked one.
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
    const html = await page.content();
    expect(html).not.toContain("a-different-mock-secret");

    // --- Detach, with a confirmation naming what is lost -----------------
    await page.getByRole("button", { name: "Detach", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(/aggregated global topology/i);
    await expect(dialog).toContainText(/cross-cluster IPAM conflict detection/i);
    await dialog.getByRole("button", { name: "Detach", exact: true }).click();

    await expect(page.getByRole("button", { name: new RegExp(renamedName) })).toHaveCount(0);
  });

  test("without netWrite, the read view works but every mutating control is disabled with a tooltip", async ({ page }) => {
    await suppressOnboardingWalkthrough(page);
    await page.goto("/login");
    await page.getByLabel("Username").fill("auditor");
    await page.getByLabel("Password", { exact: true }).fill("readonly");
    await page.getByLabel("Realm").fill("pve");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/topology");

    await page.goto("/settings/federation");
    await page.getByRole("heading", { name: "Federated clusters" }).waitFor();

    const attachButton = page.getByRole("button", { name: "Attach cluster", exact: true });
    await expect(attachButton).toBeDisabled();
    await attachButton.hover();
    await expect(page.getByRole("tooltip")).toContainText(/network write/i);
  });
});
