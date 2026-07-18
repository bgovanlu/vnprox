// T-1003 AC2: against the real stack (pvemock testdata/clusters/
// flow-lab.yaml -> vnproxd's real internal/flow NetFlow v5 listener ->
// the production SPA build), on this task's own webServer pair
// (web/playwright.config.ts, ports 58006/58007, netflow_enabled on port
// 52055 — testdata/dev-flow.toml).
//
// T-1002's own golden `testdata/flows/*.bin` fixtures all happen to
// decode to same-subnet (self-loop) conversations against flow-lab.yaml
// (see that fixture's doc comment — both golden .bin files' IPs sit
// entirely within one node's own /24), which resolve to zero flowEdges.ts
// overlay edges by design (a same-ref "conversation" has no distinct
// second endpoint to draw a line to). Exercising a genuine cross-entity
// edge for this real-browser test therefore needs one more NetFlow v5
// datagram, hand-built here with the exact same wire format
// internal/flow.DecodeNetFlow5 already golden-tests
// (internal/flow/decode_test.go): a 24-byte header + one 48-byte flow
// record, big-endian, with srcaddr inside pve1's vmbr0 (10.0.0.0/24) and
// dstaddr inside pve2's vmbr0 (10.1.1.0/24) — two DIFFERENT resolved refs
// (bridge:pve1:vmbr0 and bridge:pve2:vmbr0, per internal/flow/
// flowlab_integration_test.go's own resolution table), sent as a real UDP
// datagram at the daemon's real listener port. This is genuine ingestion
// through the real decoder/resolver/store/WS path — not a mocked/seeded
// shortcut. (A bridge<->SdnVnet pair was tried first but rejected: the
// vlanz zone's own SDN entity elk-lays-out directly between the vnet and
// its realizing bridge — the same intermediate hop the real topology graph
// has — so a straight line between THOSE two endpoints' centers reliably
// passes straight through that third node's box, hit-testing it instead of
// the flow overlay edge. Two same-layer bridges on different cluster nodes
// sit in separate columns with clear space between them.)
import dgram from "node:dgram";
import { expect, request, test, type Page } from "@playwright/test";

const BASE = "https://127.0.0.1:58007";
const NETFLOW_HOST = "127.0.0.1";
const NETFLOW_PORT = 52055;

const SRC_REF = "bridge:pve1:vmbr0";
const DST_REF = "bridge:pve2:vmbr0";

function ipToBytes(ip: string): number[] {
  return ip.split(".").map((p) => Number(p));
}

/** Builds one NetFlow v5 datagram (24-byte header + `records.length` x
 * 48-byte flow records), matching internal/flow.DecodeNetFlow5's exact
 * wire layout field-for-field. */
function buildNetFlow5Datagram(records: { srcIp: string; dstIp: string; srcPort: number; dstPort: number; proto: number; bytes: number; packets: number }[]): Buffer {
  const header = Buffer.alloc(24);
  header.writeUInt16BE(5, 0); // version
  header.writeUInt16BE(records.length, 2); // count
  header.writeUInt32BE(0, 4); // sysUptime
  header.writeUInt32BE(Math.floor(Date.now() / 1000), 8); // unixSecs
  header.writeUInt32BE(0, 12); // unixNsecs
  header.writeUInt32BE(1, 16); // flowSequence
  header.writeUInt8(0, 20); // engineType
  header.writeUInt8(0, 21); // engineID
  header.writeUInt16BE(0, 22); // samplingInterval

  const recordBufs = records.map((r) => {
    const buf = Buffer.alloc(48);
    const [s0, s1, s2, s3] = ipToBytes(r.srcIp);
    const [d0, d1, d2, d3] = ipToBytes(r.dstIp);
    buf.writeUInt8(s0 ?? 0, 0);
    buf.writeUInt8(s1 ?? 0, 1);
    buf.writeUInt8(s2 ?? 0, 2);
    buf.writeUInt8(s3 ?? 0, 3);
    buf.writeUInt8(d0 ?? 0, 4);
    buf.writeUInt8(d1 ?? 0, 5);
    buf.writeUInt8(d2 ?? 0, 6);
    buf.writeUInt8(d3 ?? 0, 7);
    buf.writeUInt32BE(0, 8); // nexthop
    buf.writeUInt16BE(1, 12); // input ifIndex
    buf.writeUInt16BE(2, 14); // output ifIndex
    buf.writeUInt32BE(r.packets, 16); // dPkts
    buf.writeUInt32BE(r.bytes, 20); // dOctets
    buf.writeUInt32BE(0, 24); // first
    buf.writeUInt32BE(0, 28); // last
    buf.writeUInt16BE(r.srcPort, 32);
    buf.writeUInt16BE(r.dstPort, 34);
    buf.writeUInt8(0, 36); // pad1
    buf.writeUInt8(0, 37); // tcp_flags
    buf.writeUInt8(r.proto, 38);
    buf.writeUInt8(0, 39); // tos
    buf.writeUInt16BE(0, 40); // src_as
    buf.writeUInt16BE(0, 42); // dst_as
    buf.writeUInt8(0, 44); // src_mask
    buf.writeUInt8(0, 45); // dst_mask
    buf.writeUInt16BE(0, 46); // pad2
    return buf;
  });

  return Buffer.concat([header, ...recordBufs]);
}

/** Sends one hand-built NetFlow v5 datagram carrying a single cross-entity
 * flow (pve1's vmbr0 -> pve2's vmbr0) to the real
 * daemon's real UDP listener. */
async function sendCrossEntityFlow(): Promise<void> {
  const datagram = buildNetFlow5Datagram([
    { srcIp: "10.0.0.5", dstIp: "10.1.1.50", srcPort: 51000, dstPort: 443, proto: 6, bytes: 150_000, packets: 100 },
  ]);
  const socket = dgram.createSocket("udp4");
  await new Promise<void>((resolve, reject) => {
    socket.send(datagram, NETFLOW_PORT, NETFLOW_HOST, (err) => {
      if (err) reject(err);
      else resolve();
    });
  });
  socket.close();
}

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

// T-901's rendererVersion feature flag (see web/e2e/lod.spec.ts's/
// scale.spec.ts's identical helper) — the Flows layer overlay is v2-canvas
// -only per this task's card, so every scenario below needs it.
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
  await page.goto(BASE + "/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.getByTestId("topology-canvas-v2")).toBeVisible();
}

/** Waits until both flow-lab.yaml nodes' bridges and the vlanz zone's
 * vnet100 SDN vnet are visible in the real backend's own GET /topology
 * response — proof the collectors (PVE + SDN) have converged, mirroring
 * scale.spec.ts's identical direct-backend readiness poll. */
async function waitForBackendConverged(): Promise<void> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(BASE + "/api/v1/auth/login", { data: { username: "root@pam", password: "vnprox-mock" } });
    expect(login.ok()).toBe(true);

    const deadline = Date.now() + 60_000;
    for (;;) {
      const resp = await ctx.get(BASE + "/api/v1/topology");
      if (resp.ok()) {
        const body = (await resp.json()) as { nodes: { id: string }[] };
        const ids = new Set(body.nodes.map((n) => n.id));
        if (ids.has(SRC_REF) && ids.has(DST_REF)) return;
      }
      if (Date.now() > deadline) {
        throw new Error("backend topology never converged with both flow-lab bridges and vnet100");
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  } finally {
    await ctx.dispose();
  }
}

test.describe.configure({ mode: "serial" });

test("Flows layer: toggling it paints a real ingested cross-entity flow as an edge, clicking it drills down, and the Explorer deep link lands pre-filtered", async ({ page }) => {
  await waitForBackendConverged();
  await logIn(page);

  const region = page.getByRole("application", { name: /Topology map/ });
  await expect(region.getByRole("button", { name: /bridge vmbr0/ }).first()).toBeVisible({ timeout: 15_000 });

  // The v2 canvas's fit-to-view only ever runs once per distinct element
  // *set* (topology/TopologyCanvasV2.tsx's fittedRef/fitSignatureRef —
  // keyed on node count/ids, not position), and can fire before elkjs's
  // async computeLayout has resolved real positions (every node still at
  // its {x:0,y:0} placeholder at that instant), fitting to a degenerate
  // single-point bounding box and leaving the *real* layout scattered
  // outside the viewport once positions do arrive. Toggling a real layer
  // off/on changes the element set's signature (filterByLayers drops/
  // restores nodes), forcing a fresh fit against whatever positions are
  // current by then — by this point in the test (well after the initial
  // page load), elk has long since resolved, so this reliably re-centers
  // on the real, final layout before this test reads any node's on-screen
  // position.
  const guestsToggle = page.getByRole("button", { name: /Guests/ });
  await guestsToggle.click();
  await guestsToggle.click();

  // Toggle the Flows layer on *before* the record arrives, so the
  // dismissible empty-state hint (AC4) is observed transitioning away
  // live via the WS bridge in this same session — no reload.
  const flowsToggle = page.getByRole("button", { name: "Flows", exact: true });
  await expect(flowsToggle).toBeVisible();
  await flowsToggle.click();
  await expect(flowsToggle).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText(/no ingested flow records cluster-wide yet/)).toBeVisible();

  await sendCrossEntityFlow();

  // AC4: the hint disappears once a flow arrives via the WS bridge, in
  // the same session, no reload.
  await expect(page.getByText(/no ingested flow records cluster-wide yet/)).toHaveCount(0, { timeout: 15_000 });

  // AC2 "renders at least one animated edge": both endpoints (a Bridge and
  // an SdnVnet, both real map entities) must now be on screen for the
  // overlay to have anything to connect — locate their a11y proxy buttons
  // precisely by `data-entity-id` (the exact Ref string, not just the
  // display label — flow-lab.yaml has a same-named "vmbr0" bridge on BOTH
  // nodes, so a label-text match alone could resolve to the wrong node's
  // proxy) and click the midpoint between them, which only succeeds
  // (hitTestFlowEdge) if the Flows-layer overlay edge genuinely exists at
  // that geometry.
  const srcProxy = region.locator(`[data-entity-id="${SRC_REF}"]`);
  const dstProxy = region.locator(`[data-entity-id="${DST_REF}"]`);
  await expect(srcProxy).toBeVisible({ timeout: 15_000 });
  await expect(dstProxy).toBeVisible({ timeout: 15_000 });

  const srcBox = await srcProxy.boundingBox();
  const dstBox = await dstProxy.boundingBox();
  expect(srcBox).not.toBeNull();
  expect(dstBox).not.toBeNull();
  if (!srcBox || !dstBox) return;

  const midX = (srcBox.x + srcBox.width / 2 + dstBox.x + dstBox.width / 2) / 2;
  const midY = (srcBox.y + srcBox.height / 2 + dstBox.y + dstBox.height / 2) / 2;

  // A pixel sampled at the overlay edge's own midpoint must differ from
  // BOTH of canvasDraw.ts's plain light/dark background colors (#f8fafc /
  // #0f172a — themeColors' `background` field, whichever theme this
  // browser context happens to render in) — direct proof something was
  // actually painted there (the cyan overlay stroke), independent of the
  // click-driven assertions below (which only prove the *geometry*
  // resolves, not that pixels were drawn).
  const canvas = page.getByTestId("topology-canvas-v2").locator("canvas").first();
  const canvasBox = await canvas.boundingBox();
  expect(canvasBox).not.toBeNull();
  if (canvasBox) {
    const localX = midX - canvasBox.x;
    const localY = midY - canvasBox.y;
    const pixel = await canvas.evaluate(
      (el, [x, y]) => {
        const c = el as HTMLCanvasElement;
        const ctx2d = c.getContext("2d");
        if (!ctx2d) return null;
        const dpr = c.width / c.clientWidth;
        const data = ctx2d.getImageData(Math.round(x * dpr), Math.round(y * dpr), 1, 1).data;
        return Array.from(data);
      },
      [localX, localY] as [number, number],
    );
    expect(pixel).not.toBeNull();
    const [r, g, b] = pixel ?? [0, 0, 0];
    const isLightBg = r === 0xf8 && g === 0xfa && b === 0xfc;
    const isDarkBg = r === 0x0f && g === 0x17 && b === 0x2a;
    expect(isLightBg || isDarkBg).toBe(false);
  }

  await page.mouse.click(midX, midY);

  // "clicking it opens the inspector filtered to that pair"
  const pairPanel = page.getByRole("region", { name: "Flow conversation" });
  await expect(pairPanel).toBeVisible({ timeout: 10_000 });
  await expect(pairPanel).toContainText(SRC_REF);
  await expect(pairPanel).toContainText(DST_REF);

  // "the 'view in Flow Explorer' link lands on the explorer pre-filtered
  // to the same guest pair"
  const explorerLink = pairPanel.getByRole("link", { name: "View in Flow Explorer" });
  const href = await explorerLink.getAttribute("href");
  expect(href).toBeTruthy();
  await explorerLink.click();
  await page.waitForURL("**/flows?**");
  await expect(page.getByText(/Showing only the conversation between/)).toBeVisible();
  await expect(page.getByText(SRC_REF, { exact: false }).first()).toBeVisible();
});

test("Flows layer and Traffic paint mode coexist without colliding controls", async ({ page }) => {
  await waitForBackendConverged();
  await logIn(page);

  const flowsToggle = page.getByRole("button", { name: "Flows", exact: true });
  const trafficToggle = page.getByRole("button", { name: "Traffic" });
  await expect(flowsToggle).toBeVisible();
  await expect(trafficToggle).toBeVisible();

  await flowsToggle.click();
  await trafficToggle.click();

  await expect(flowsToggle).toHaveAttribute("aria-pressed", "true");
  await expect(trafficToggle).toHaveAttribute("aria-pressed", "true");
  // Both controls remain present and independently rendered — no
  // collision/merge of the two toggle affordances.
  await expect(flowsToggle).toBeVisible();
  await expect(trafficToggle).toBeVisible();
});
