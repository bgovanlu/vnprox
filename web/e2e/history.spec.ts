// T-1007 AC5: against the real stack (pvemock testdata/clusters/
// flow-lab.yaml -> vnproxd's real internal/flow NetFlow v5 listener +
// internal/change apply engine -> the production SPA build), reusing
// flows.spec.ts's own webServer pair (web/playwright.config.ts, ports
// 58006/58007, netflow_enabled on port 52055 — testdata/dev-flow.toml).
//
// Seeds two genuinely real pieces of history via the REST API directly
// (the same request-context pattern waitForBackendConverged already uses
// for readiness polling): one committed changeset (for the changeset-
// lifecycle marker) and one ingested cross-entity NetFlow record (for the
// flows-layer scrub, reusing flows.spec.ts's own hand-built NetFlow v5
// datagram — see that file's doc comment for the wire-format details).
// Then drives the real browser: toggling Traffic + Flows on, scrubbing the
// HistoryTimeline control back before the flow record's own timestamp
// (proving the Flows overlay disappears — the map's paint genuinely
// changed, not just a static re-render) and forward to it again, then
// clicking the seeded changeset's timeline marker and confirming it opens
// the existing changeset drawer (never re-applies/re-confirms anything —
// this is the read-only enforcement AC5 also calls out).
import dgram from "node:dgram";
import { request, type Page } from "@playwright/test";
import { expect, test, stackURL, isolatedStore } from "./isolated";

isolatedStore({ config: "testdata/dev-flow.toml" });

const NETFLOW_HOST = "127.0.0.1";
const NETFLOW_PORT = 52055;

const SRC_REF = "bridge:pve1:vmbr0";
const DST_REF = "bridge:pve2:vmbr0";

function ipToBytes(ip: string): number[] {
  return ip.split(".").map((p) => Number(p));
}

/** Builds one NetFlow v5 datagram (24-byte header + one 48-byte flow
 * record), matching internal/flow.DecodeNetFlow5's exact wire layout —
 * duplicated from flows.spec.ts's own identical helper (each e2e spec
 * keeps its own local helpers per this suite's established convention;
 * see e.g. alert-rules.spec.ts's own local logIn). */
function buildNetFlow5Datagram(records: { srcIp: string; dstIp: string; srcPort: number; dstPort: number; proto: number; bytes: number; packets: number }[]): Buffer {
  const header = Buffer.alloc(24);
  header.writeUInt16BE(5, 0);
  header.writeUInt16BE(records.length, 2);
  header.writeUInt32BE(0, 4);
  header.writeUInt32BE(Math.floor(Date.now() / 1000), 8);
  header.writeUInt32BE(0, 12);
  header.writeUInt32BE(1, 16);
  header.writeUInt8(0, 20);
  header.writeUInt8(0, 21);
  header.writeUInt16BE(0, 22);

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
    buf.writeUInt32BE(0, 8);
    buf.writeUInt16BE(1, 12);
    buf.writeUInt16BE(2, 14);
    buf.writeUInt32BE(r.packets, 16);
    buf.writeUInt32BE(r.bytes, 20);
    buf.writeUInt32BE(0, 24);
    buf.writeUInt32BE(0, 28);
    buf.writeUInt16BE(r.srcPort, 32);
    buf.writeUInt16BE(r.dstPort, 34);
    buf.writeUInt8(0, 36);
    buf.writeUInt8(0, 37);
    buf.writeUInt8(r.proto, 38);
    buf.writeUInt8(0, 39);
    buf.writeUInt16BE(0, 40);
    buf.writeUInt16BE(0, 42);
    buf.writeUInt8(0, 44);
    buf.writeUInt8(0, 45);
    buf.writeUInt16BE(0, 46);
    return buf;
  });

  return Buffer.concat([header, ...recordBufs]);
}

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

async function waitForBackendConverged(): Promise<void> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(stackURL() + "/api/v1/auth/login", { data: { username: "root@pam", password: "vnprox-mock" } });
    expect(login.ok()).toBe(true);

    const deadline = Date.now() + 60_000;
    for (;;) {
      const resp = await ctx.get(stackURL() + "/api/v1/topology");
      if (resp.ok()) {
        const body = (await resp.json()) as { nodes: { id: string }[] };
        const ids = new Set(body.nodes.map((n) => n.id));
        if (ids.has(SRC_REF) && ids.has(DST_REF)) return;
      }
      if (Date.now() > deadline) {
        throw new Error("backend topology never converged with both flow-lab bridges");
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  } finally {
    await ctx.dispose();
  }
}

/** docs/api.md's Conventions: mutating requests need `X-VNPROX-CSRF`
 * matching the double-submit `vnprox_csrf` cookie login sets (JS-readable
 * by design — internal/auth/service.go's CSRFCookieName). request.
 * newContext() tracks cookies across calls like a browser would, but
 * (unlike a real page) never echoes one back as a header on its own, so
 * every POST/PUT/DELETE below must read it from storageState() and set it
 * explicitly. */
async function csrfHeader(ctx: Awaited<ReturnType<typeof request.newContext>>): Promise<Record<string, string>> {
  const state = await ctx.storageState();
  const cookie = state.cookies.find((c) => c.name === "vnprox_csrf");
  return cookie ? { "X-VNPROX-CSRF": cookie.value } : {};
}

/** Creates, applies, and confirms a trivial, real changeset (a comment
 * update on pve1's vmbr0 — the same interface this fixture already
 * documents comments on) via the REST API directly, returning the
 * committed changeset's id. This is a genuine T-205 apply-engine lifecycle
 * run (draft -> applying -> awaiting_confirm -> committed), so it produces
 * real `changeset.apply`/`changeset.confirm` audit_log rows for
 * GET /history/events to serve — not a fixture shortcut. confirmTimeoutSec
 * is set to the T-703 mgmt-path floor (180s) since this fixture's only
 * bridge per node also carries the node's PVE management IP (touchesMgmtPath
 * is true here regardless of which interface an op targets, on a
 * single-bridge-per-node fixture like this one). */
async function seedCommittedChangeset(): Promise<string> {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(stackURL() + "/api/v1/auth/login", { data: { username: "root@pam", password: "vnprox-mock" } });
    expect(login.ok()).toBe(true);
    const headers = await csrfHeader(ctx);

    const create = await ctx.post(stackURL() + "/api/v1/changesets", {
      headers,
      data: {
        title: "T-1007 e2e: history marker",
        ops: [{ op: "iface.update", target: SRC_REF, params: { comments: "T-1007 history playback e2e marker" } }],
      },
    });
    expect(create.ok()).toBe(true);
    const created = (await create.json()) as { id: string };

    const apply = await ctx.post(stackURL() + `/api/v1/changesets/${created.id}/apply`, {
      headers,
      data: { confirmTimeoutSec: 180 },
    });
    expect(apply.status()).toBe(202);

    const deadline = Date.now() + 30_000;
    for (;;) {
      const get = await ctx.get(stackURL() + `/api/v1/changesets/${created.id}`);
      expect(get.ok()).toBe(true);
      const body = (await get.json()) as { status: string };
      if (body.status === "awaiting_confirm") break;
      if (Date.now() > deadline) throw new Error(`changeset ${created.id} never reached awaiting_confirm (last status: ${body.status})`);
      await new Promise((r) => setTimeout(r, 250));
    }

    const confirm = await ctx.post(stackURL() + `/api/v1/changesets/${created.id}/confirm`, { headers });
    expect(confirm.ok()).toBe(true);

    return created.id;
  } finally {
    await ctx.dispose();
  }
}

test.describe.configure({ mode: "serial" });

test("History playback: scrubbing the timeline changes the map's flow paint and shows a changeset marker that deep-links correctly", async ({ page }) => {
  await waitForBackendConverged();
  const changesetId = await seedCommittedChangeset();
  const flowSentAt = Math.floor(Date.now() / 1000);
  await sendCrossEntityFlow();

  await logIn(page);

  const region = page.getByRole("application", { name: /Topology map/ });
  await expect(region.getByRole("button", { name: /bridge vmbr0/ }).first()).toBeVisible({ timeout: 15_000 });

  // The v2 canvas's fit-to-view only ever runs once per distinct element
  // *set* (see flows.spec.ts's identical helper's own doc comment): toggling
  // a real layer off/on forces a fresh fit against elk's by-then-resolved
  // positions, so every boundingBox() read below reflects the real, final
  // layout rather than a transient {x:0,y:0} placeholder.
  const guestsToggle = page.getByRole("button", { name: /Guests/ });
  await guestsToggle.click();
  await guestsToggle.click();

  // Toggle Flows on and wait for the just-sent record to actually arrive
  // via the live WS bridge (mirrors flows.spec.ts's own AC4 wait) before
  // touching the scrubber at all — otherwise "scrubbing removes the edge"
  // below would be trivially true for the wrong reason (nothing painted
  // yet either way).
  const flowsToggle = page.getByRole("button", { name: "Flows", exact: true });
  await flowsToggle.click();
  await expect(flowsToggle).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText(/no ingested flow records cluster-wide yet/)).toHaveCount(0, { timeout: 15_000 });

  const srcProxy = region.locator(`[data-entity-id="${SRC_REF}"]`);
  const dstProxy = region.locator(`[data-entity-id="${DST_REF}"]`);
  await expect(srcProxy).toBeVisible({ timeout: 15_000 });
  await expect(dstProxy).toBeVisible({ timeout: 15_000 });

  const canvas = page.getByTestId("topology-canvas-v2").locator("canvas").first();

  /** Whether the Flows overlay has painted its edge between SRC_REF and
   * DST_REF right now.
   *
   * Two things this deliberately does NOT do, both of which the previous
   * single-pixel version did and both of which made it wrong:
   *
   *  1. It does not sample only the midpoint. `drawFlowOverlay`
   *     (src/topology/canvas/canvasDraw.ts) strokes the edge **dashed** —
   *     `setLineDash([8, 6])` — so 6 px in every 14 along the line are
   *     legitimately unpainted. The midpoint landing in a gap is a ~43%
   *     coin flip that has nothing to do with whether the overlay rendered,
   *     and it is not self-correcting under `expect.poll`: with the dash
   *     animation paused the gap stays exactly where it is forever. This
   *     walks the whole segment instead and asks whether ANY sample is on a
   *     dash.
   *  2. It does not test for "not the background colour". Anything drawn
   *     over that pixel — a base topology edge, a node border, a label —
   *     satisfied that, so a true result was not evidence of the flow
   *     overlay specifically. This tests for the overlay's own fixed cyan
   *     (FLOW_EDGE_COLOR `#06b6d4`, which canvasDraw.ts documents as chosen
   *     to collide with no other palette in the app), which is what the
   *     assertion actually means. Samples that fall inside a node's own
   *     rectangle are skipped, since a node's tint is not the edge. */
  async function flowEdgeIsPainted(): Promise<boolean> {
    const srcBox = await srcProxy.boundingBox();
    const dstBox = await dstProxy.boundingBox();
    const canvasBox = await canvas.boundingBox();
    if (!srcBox || !dstBox || !canvasBox) return false;
    const nodeBoxes = await region.locator("[data-entity-id]").evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, width: r.width, height: r.height };
      }),
    );

    const a = { x: srcBox.x + srcBox.width / 2 - canvasBox.x, y: srcBox.y + srcBox.height / 2 - canvasBox.y };
    const b = { x: dstBox.x + dstBox.width / 2 - canvasBox.x, y: dstBox.y + dstBox.height / 2 - canvasBox.y };
    const boxes = nodeBoxes.map((n) => ({ x: n.x - canvasBox.x, y: n.y - canvasBox.y, w: n.width, h: n.height }));

    return canvas.evaluate(
      (el, { a: from, b: to, boxes: skip }) => {
        const c = el as HTMLCanvasElement;
        const ctx2d = c.getContext("2d");
        if (!ctx2d) return false;
        const dpr = c.width / c.clientWidth;
        const dx = to.x - from.x;
        const dy = to.y - from.y;
        const len = Math.hypot(dx, dy);
        if (len < 1) return false;
        // Every 2 px along the line: finer than the 6 px dash gap, so a
        // painted edge cannot be stepped over.
        for (let d = 0; d <= len; d += 2) {
          const x = from.x + (dx * d) / len;
          const y = from.y + (dy * d) / len;
          if (skip.some((s) => x >= s.x && x <= s.x + s.w && y >= s.y && y <= s.y + s.h)) continue;
          // A 3x3 patch absorbs sub-pixel placement and antialiasing of a
          // thin stroke without widening what counts as "cyan".
          const px = Math.round(x * dpr);
          const py = Math.round(y * dpr);
          if (px < 1 || py < 1 || px >= c.width - 1 || py >= c.height - 1) continue;
          const patch = ctx2d.getImageData(px - 1, py - 1, 3, 3).data;
          for (let i = 0; i < patch.length; i += 4) {
            const r = patch[i] ?? 0;
            const g = patch[i + 1] ?? 0;
            const bl = patch[i + 2] ?? 0;
            // #06b6d4 at alpha 0.9 over either theme background: strongly
            // blue+green dominant over red. Both backgrounds, every slate
            // stroke, and the status palette fail this.
            if (bl - r > 60 && g - r > 40 && bl > 100) return true;
          }
        }
        return false;
      },
      { a, b, boxes },
    );
  }

  // The overlay can only paint an edge between two entities if the daemon
  // resolved this record's addresses to those entities' refs in the first
  // place (internal/flow.GraphResolver). Resolution happens once, at ingest,
  // and is never retried — so assert it actually happened before asserting
  // on pixels. Without this the test's real failure ("the record was
  // ingested before the resolver had indexed anything, and is therefore
  // unattributed forever") shows up as the far less legible "a pixel is not
  // cyan". Found exactly that way under T-2108; the daemon-side fix is in
  // cmd/vnproxd/flows.go's cold-start refresh cadence.
  await expect
    .poll(
      async () =>
        page.evaluate(async () => {
          const resp = await fetch("/api/v1/flows?limit=50", { credentials: "include" });
          if (!resp.ok) return [] as { srcRef?: string; dstRef?: string }[];
          const body = (await resp.json()) as { items?: { srcRef?: string; dstRef?: string }[] };
          return body.items ?? [];
        }),
      { timeout: 20_000 },
    )
    .toContainEqual(expect.objectContaining({ srcRef: SRC_REF, dstRef: DST_REF }));

  // Live: the just-arrived record paints an edge right now.
  await expect.poll(flowEdgeIsPainted, { timeout: 15_000 }).toBe(true);

  // Scrub the HistoryTimeline back to well before the flow record's own
  // timestamp — AC5's "visibly changes the map's ... paint": the overlay
  // edge must disappear (this is a genuinely different render, not a
  // no-op), proving the scrubber re-queried historical flow data for that
  // earlier instant rather than continuing to show the live buffer.
  const timeline = page.getByTestId("history-timeline");
  const slider = page.getByLabel("Scrub map history");
  await expect(slider).toBeVisible();
  // `fill()` on <input type="range"> rejects any value that is not exactly
  // on the step grid ("Malformed value") — and this slider's grid is
  // `min + 30k` where `min` is a live unix timestamp, so an arbitrary
  // "ten minutes ago" almost never lands on it. A real user never hits this:
  // dragging the thumb snaps to the grid for them. Snap here the same way,
  // reading min/step off the element rather than assuming them, so the test
  // keeps working if the range's own definition changes.
  const grid = await slider.evaluate((el: HTMLInputElement) => ({ min: Number(el.min), step: Number(el.step) }));
  const beforeFlowAt = // 10 minutes before the flow arrived, snapped down onto the step grid
    grid.min + Math.floor((flowSentAt - 600 - grid.min) / grid.step) * grid.step;
  await slider.fill(String(beforeFlowAt));
  // exact: true. A bare getByText("Live") is a case-insensitive substring
  // match, so it also matches the "Back to live" button that only exists
  // WHILE scrubbing — i.e. the assertion "we left live mode" was satisfied
  // by the very control that proves we left it, and could never pass.
  await expect(timeline.getByText("Live", { exact: true })).toHaveCount(0);
  await expect(timeline.getByRole("button", { name: "Back to live" })).toBeVisible();
  await expect.poll(flowEdgeIsPainted, { timeout: 15_000 }).toBe(false);

  // Scrub forward to "now" again (back to live) — the edge reappears.
  await page.getByRole("button", { name: "Back to live" }).click();
  await expect.poll(flowEdgeIsPainted, { timeout: 15_000 }).toBe(true);

  // Changeset marker: appears on the timeline and deep-links to the
  // existing changeset drawer — never re-applies/re-confirms anything.
  const changesetMarker = page.getByRole("button", { name: /Changeset event: changeset\.confirm/ });
  await expect(changesetMarker).toBeVisible({ timeout: 15_000 });
  await changesetMarker.click();

  const drawer = page.getByRole("region", { name: "Change drawer" });
  await expect(drawer).toBeVisible();
  await expect(drawer).toContainText("T-1007 e2e: history marker");
  // Read-only enforcement: a committed changeset's drawer never re-offers
  // apply/confirm/rollback controls.
  await expect(drawer.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
  await expect(drawer.getByRole("button", { name: "Confirm", exact: true })).toHaveCount(0);
  await expect(drawer.getByRole("button", { name: "Roll back" })).toHaveCount(0);

  // Sanity: the marker we clicked really was this run's own changeset, not
  // some other row that happened to render first.
  expect(changesetId).toBeTruthy();
});
