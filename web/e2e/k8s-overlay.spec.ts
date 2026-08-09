// T-1502 AC3: end-to-end verification of the Kubernetes map layer +
// PodDrilldown against the REAL stack — the three-node-vlan pvemock/
// vnproxd pair (ports 8006/8007) every other core topology spec shares,
// plus this task's own standalone `cmd/k8smock` instance (port 8008,
// web/playwright.config.ts) standing in for a real k8s cluster.
//
// The spec registers a real k8s cluster via POST /k8s/clusters (a raw API
// call, not a UI flow — T-1501's own registration UI/wizard doesn't exist
// and is explicitly out of scope; this task never adds one either, per
// AC4's "no k8s object management affordance" regression), toggles the
// Kubernetes layer on, selects the one pod on the fixture's single k8s
// node, and asserts PodDrilldown renders the full pod -> node-guest ->
// bridge -> bond chain — real data end to end (real IPAM engine
// correlating k8s-node-1's InternalIP to guest:pve1:200/app01, real
// topology graph resolving app01's net0 -> vmbr0 -> bond0), not a
// synthetic stand-in. See testdata/k8s/e2e-cluster.yaml's own doc comment
// for why that specific IP was chosen.
import { request, type Page } from "@playwright/test";
import { expect, test, stackURL, isolatedStore } from "./isolated";

isolatedStore();

const K8SMOCK_URL = "http://127.0.0.1:8008";

const GUEST_REF = "guest:pve1:200";
const BRIDGE_REF = "bridge:pve1:vmbr0";

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

// T-901's rendererVersion feature flag (see flows.spec.ts's identical
// helper) — every overlay layer, Kubernetes included, is v2-canvas-only.
async function enableV2Renderer(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("vnprox.topology.rendererV2", "v2");
    } catch {
      /* private-mode localStorage: the flag simply won't stick */
    }
  });
}

async function logIn(page: Page): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await enableV2Renderer(page);
  await page.goto(stackURL() + "/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();
}

/** Waits until three-node-vlan.yaml's own guest 200 (app01) and its bridge
 * are visible in the real backend's GET /topology response — proof the
 * collectors have converged before this spec drives the UI, mirroring
 * flows.spec.ts's identical direct-backend readiness poll. */
async function waitForBackendConverged(): Promise<void> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(stackURL() + "/api/v1/auth/login", {
      data: { username: "root@pam", password: "vnprox-mock" },
    });
    expect(login.ok()).toBe(true);

    const deadline = Date.now() + 60_000;
    for (;;) {
      const resp = await ctx.get(stackURL() + "/api/v1/topology");
      if (resp.ok()) {
        const body = (await resp.json()) as { nodes: { id: string }[] };
        const ids = new Set(body.nodes.map((n) => n.id));
        if (ids.has(GUEST_REF) && ids.has(BRIDGE_REF)) return;
      }
      if (Date.now() > deadline) {
        throw new Error("backend topology never converged with guest:pve1:200/bridge:pve1:vmbr0");
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  } finally {
    await ctx.dispose();
  }
}

/** docs/api.md's Conventions: mutating requests need the double-submit
 * `X-VNPROX-CSRF` header (see history.spec.ts's/mgmt-redundancy.spec.ts's
 * identical helper). */
async function readCsrfCookie(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === "vnprox_csrf");
  if (!csrf) throw new Error("vnprox_csrf cookie not set — is the session logged in?");
  return csrf.value;
}

/** A minimal, valid bearer-token kubeconfig pointed at this task's own
 * k8smock instance — no TLS at all (internal/k8s.Client's http.Transport
 * handles a plain `http://` BaseURL the same way `kubectl` would; a
 * bearer-token credential needs no client certificate either), and the
 * token itself is a throwaway placeholder k8smock never checks. */
function inlineKubeconfig(): string {
  return `apiVersion: v1
kind: Config
current-context: e2e
clusters:
  - name: e2e-cluster
    cluster:
      server: ${K8SMOCK_URL}
contexts:
  - name: e2e
    context:
      cluster: e2e-cluster
      user: e2e-user
users:
  - name: e2e-user
    user:
      token: e2e-placeholder-token
`;
}

/** Registers the k8smock cluster via a raw POST /k8s/clusters call (no UI
 * registration flow exists — T-1501 never built one, and this task never
 * adds one either, per AC4) and returns its assigned id. */
async function registerK8sCluster(page: Page): Promise<string> {
  const csrf = await readCsrfCookie(page);
  const res = await page.request.post(stackURL() + "/api/v1/k8s/clusters", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: { name: "e2e-cluster", kubeconfig: inlineKubeconfig() },
  });
  expect(res.ok(), `POST /k8s/clusters failed: ${String(res.status())} ${await res.text()}`).toBe(true);
  const body = (await res.json()) as { id: string };
  return body.id;
}

async function deregisterK8sCluster(page: Page, id: string): Promise<void> {
  const csrf = await readCsrfCookie(page);
  await page.request.delete(stackURL() + `/api/v1/k8s/clusters/${id}`, { headers: { "X-VNPROX-CSRF": csrf } });
}

test.describe.configure({ mode: "serial" });

test("Kubernetes layer: toggle on, select a pod, drill down to the full pod -> node-guest -> bridge -> bond chain", async ({
  page,
}) => {
  await waitForBackendConverged();
  await logIn(page);

  const clusterId = await registerK8sCluster(page);
  try {
    const region = page.getByRole("application", { name: /Topology map/ });

    const k8sToggle = page.getByRole("button", { name: "Kubernetes", exact: true });
    await expect(k8sToggle).toBeVisible();
    await k8sToggle.click();
    await expect(k8sToggle).toHaveAttribute("aria-pressed", "true");

    // The pod-cidr region (AC1's "pod/service CIDR shapes") for the
    // fixture's one k8s node.
    const podNetProxy = region.locator(`[data-entity-id="k8s-podnet:${clusterId}/k8s-node-1"]`);
    await expect(podNetProxy).toBeVisible({ timeout: 20_000 });

    // The node<->guest correlation line: app01 (guest:pve1:200) must also
    // be on screen for the overlay edge to connect to.
    const guestProxy = region.locator(`[data-entity-id="${GUEST_REF}"]`);
    await expect(guestProxy).toBeVisible({ timeout: 15_000 });

    // AC3: select the fixture's one pod. The v2 canvas's a11y proxies are
    // deliberately `pointer-events: none` (TopologyA11yLayer.tsx's own doc
    // comment: "the canvas beneath owns ALL mouse interaction" — a real
    // browser click at this element's screen position would hit-test
    // through to the canvas underneath it, not this button) but remain
    // keyboard-focusable/activatable, which is exactly what this drives
    // instead of computing on-canvas pixel coordinates.
    const podProxy = region.locator(`[data-entity-id="k8s-pod:${clusterId}/default/web-abc123"]`);
    await expect(podProxy).toBeVisible({ timeout: 15_000 });
    await podProxy.focus();
    await page.keyboard.press("Enter");

    const drilldown = page.getByRole("region", { name: "Pod drilldown" });
    await expect(drilldown).toBeVisible({ timeout: 10_000 });
    await expect(drilldown).toContainText("default/web-abc123");
    // The correlated guest — never "unmatched" for this fixture.
    await expect(drilldown).toContainText(GUEST_REF);
    await expect(drilldown).not.toContainText("unmatched");
    // AC3's own wording: the full chain, pod -> node-guest -> bridge -> bond.
    await expect(drilldown).toContainText("app01");
    await expect(drilldown).toContainText("vmbr0");
    await expect(drilldown).toContainText("bond0");

    await drilldown.getByRole("button", { name: "Close" }).click();
    await expect(drilldown).toHaveCount(0);
  } finally {
    await deregisterK8sCluster(page, clusterId);
  }
});
