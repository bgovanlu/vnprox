// SPDX-License-Identifier: Apache-2.0

// T-1302 acceptance criteria against the real stack (pvemock
// three-node-vlan.yaml -> vnproxd collectors -> the production SPA build),
// this task's own capture engine (T-1301's internal/capturemock scripted
// agent, which writes its whole deterministic frame burst synchronously on
// start — see internal/capturemock/agent.go's doc comment, which is why
// this spec never needs to wait out a real capture duration).
//
//  AC3: right-click a bridge -> build a filter -> start capture -> live
//       status updates -> stop -> decode renders in-browser -> download
//       link fetches a pcap.
//  AC4: a session group with ≥2 correlated sessions renders the side-by-side
//       view with both panes populated. The dev harness runs a single
//       vnproxd (no live second peer daemon to genuinely capture on a
//       second node against — see this task's completion report), so this
//       scenario is driven against a page.route-mocked /captures response
//       shaped exactly like a real two-node group; AC3 above exercises the
//       real backend end to end for the single-point case.
import { expect, test, type Page } from "@playwright/test";
import { switchToGraphView, waitForLayoutSettled } from "./helpers";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** switchToGraphView + wait for elkjs's layout to have SETTLED — the map's
 * right-click menu is only wired up in Graph view (see helpers.ts's
 * waitForLayoutSettled doc comment; T-3713). */
async function waitForLayout(page: Page): Promise<void> {
  await switchToGraphView(page);
  await waitForLayoutSettled(page, { minNodes: 4 });
}

/** Right-clicks a map node and picks a context-menu action, retrying the
 * whole gesture if the canvas re-layouts mid-way — mirrors simulator.spec.ts's
 * traceFromContextMenu (same collector-churn flakiness risk on this shared
 * dev host: continuous WS-pushed topology updates from the fixture's
 * deliberately-unreachable peer nodes can pan/re-fit the canvas mid-gesture).
 * Re-fits the view on every retry so the target node is back in the
 * viewport before the click, rather than relying on wherever a previous
 * attempt's pan left it. */
async function actFromContextMenu(page: Page, nodeName: string, action: string): Promise<void> {
  await expect(async () => {
    const fitView = page.getByRole("button", { name: "fit view" });
    if (await fitView.isVisible().catch(() => false)) {
      await fitView.click({ timeout: 2_000 }).catch(() => undefined);
    }
    await page.getByRole("button", { name: nodeName, exact: true }).first().click({ button: "right", timeout: 5_000 });
    await page.getByRole("menuitem", { name: action }).click({ timeout: 5_000 });
  }).toPass({ timeout: 90_000, intervals: [500, 1_000, 2_000] });
}

test("right-click a bridge, capture, decode in-browser, download the pcap", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);
  await waitForLayout(page);

  await actFromContextMenu(page, "vmbr0", "Start capture");

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/^Capture on vmbr0/)).toBeVisible();

  // Build a filter (AC1's picker composition, exercised here against the
  // real server-side validator rather than a mock).
  await dialog.getByRole("combobox", { name: /Protocol/ }).selectOption("tcp");
  await expect(dialog.getByTestId("bpf-filter-preview")).toHaveText("tcp");

  await dialog.getByRole("button", { name: "Start capture" }).click();

  // Live status: the scripted agent writes its whole burst synchronously on
  // start, so packets/bytes are already non-zero by the time the group
  // response comes back — this is the "live status updates" affordance
  // rendering real, server-reported accounting (never a client guess).
  await expect(dialog.getByTestId("capture-group-status")).toHaveText(/Group status: running/);
  await expect(dialog.getByTestId(/^session-status-/)).toContainText(/[1-9]\d* packets/);
  // The granted caps shown are the server's, never whatever the request
  // fields happened to hold — this dialog never had non-default request
  // values here, but AC1's "always the server's value" contract is pinned
  // more directly by BpfBuilder.test.tsx; this just confirms the readout
  // renders at all against the real backend's response shape.
  await expect(dialog.getByTestId("granted-caps")).toBeVisible();

  await dialog.getByRole("button", { name: "Stop" }).click();
  await expect(dialog.getByTestId("capture-group-status")).toHaveText(/Group status: stopped/);
  await expect(dialog.getByRole("button", { name: "Stop" })).toHaveCount(0);

  await dialog.getByRole("button", { name: "Decode" }).click();
  const packetList = dialog.getByTestId("packet-list");
  await expect(packetList).toBeVisible();
  // The scripted agent's first frame is always ARP (capturemock.CorpusOrder)
  // — a positive signal the bytes fetched really did decode, not just that
  // *some* UI rendered.
  await expect(packetList).toContainText(/ARP/);

  const downloadLink = dialog.getByRole("link", { name: "Download pcap" });
  const href = await downloadLink.getAttribute("href");
  expect(href).toMatch(/\/api\/v1\/captures\/.+\/download/);
  const downloadResponse = await page.request.get(new URL(href ?? "", page.url()).toString());
  expect(downloadResponse.ok()).toBe(true);
  expect(downloadResponse.headers()["content-type"]).toBe("application/vnd.tcpdump.pcap");
  const body = await downloadResponse.body();
  // Classic-pcap magic (little-endian 0xa1b2c3d4) — the same file
  // CaptureDecoder.ts's headerValid check looks for.
  expect(body.subarray(0, 4).toString("hex")).toBe("d4c3b2a1");

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});

test("multi-point session group renders both nodes' decoded streams side by side", async ({ page }) => {
  // No live second peer daemon runs in this dev harness (see this spec's
  // top-of-file doc comment), so this scenario mocks GET /captures/{id} to
  // return a two-session group shaped exactly like T-1301's real multi-point
  // response, and mocks each session's /download to return a real one-frame
  // pcap the in-browser decoder can genuinely parse — everything downstream
  // of that response (the side-by-side layout, per-pane decode/download) is
  // the real CaptureDialog/CaptureDecoder code under test, not mocked.
  const GROUP_ID = "grp-multipoint";
  const group = {
    id: GROUP_ID,
    status: "stopped",
    startedBy: "root@pam",
    startedAt: 1_700_000_000,
    caps: { maxDurationSec: 30, maxBytes: 1_048_576, maxPackets: 5000, retentionHours: 24 },
    sessions: [
      {
        id: "sess-pve1", groupId: GROUP_ID, targetRef: "bridge:pve1:vmbr0", node: "pve1", filter: "",
        caps: { maxDurationSec: 30, maxBytes: 1_048_576, maxPackets: 5000, retentionHours: 24 },
        status: "stopped", startedBy: "root@pam", startedAt: 1_700_000_000, stoppedAt: 1_700_000_005, fileBytes: 66, packets: 1,
      },
      {
        id: "sess-pve2", groupId: GROUP_ID, targetRef: "bridge:pve2:vmbr0", node: "pve2", filter: "",
        caps: { maxDurationSec: 30, maxBytes: 1_048_576, maxPackets: 5000, retentionHours: 24 },
        status: "stopped", startedBy: "root@pam", startedAt: 1_700_000_000, stoppedAt: 1_700_000_005, fileBytes: 66, packets: 1,
      },
    ],
  };

  // A minimal, real classic-pcap file (24-byte global header + one 16-byte
  // record header + a bare 14-byte Ethernet frame, EtherType ARP with no
  // ARP body) — built field-by-field to match internal/capturemock/pcap.go's
  // exact byte layout, so CaptureDecoder.ts genuinely decodes it (the
  // missing ARP body exercises the same defensive "ran out of bytes mid-
  // layer" path CaptureDecoder.test.ts's truncated.pcap case covers,
  // summarizing as "ARP (truncated)" rather than throwing).
  const frame = Buffer.concat([
    Buffer.from("ffffffffffff", "hex"), // dst: broadcast
    Buffer.from("020000000001", "hex"), // src
    Buffer.from([0x08, 0x06]), // EtherType: ARP
  ]);
  const globalHeader = Buffer.alloc(24);
  globalHeader.writeUInt32LE(0xa1b2c3d4, 0);
  globalHeader.writeUInt16LE(2, 4);
  globalHeader.writeUInt16LE(4, 6);
  globalHeader.writeUInt32LE(0, 8);
  globalHeader.writeUInt32LE(0, 12);
  globalHeader.writeUInt32LE(65535, 16);
  globalHeader.writeUInt32LE(1, 20); // linktype: Ethernet
  const recordHeader = Buffer.alloc(16);
  recordHeader.writeUInt32LE(1_700_000_000, 0); // ts_sec
  recordHeader.writeUInt32LE(0, 4); // ts_usec
  recordHeader.writeUInt32LE(frame.length, 8); // incl_len
  recordHeader.writeUInt32LE(frame.length, 12); // orig_len
  const pcapBytes = Buffer.concat([globalHeader, recordHeader, frame]);

  await page.route("**/api/v1/captures", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify(group) });
      return;
    }
    await route.continue();
  });
  await page.route(`**/api/v1/captures/${GROUP_ID}`, async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(group) });
  });
  await page.route(`**/api/v1/captures/${GROUP_ID}/download*`, async (route) => {
    await route.fulfill({ status: 200, contentType: "application/vnd.tcpdump.pcap", body: pcapBytes });
  });

  await logIn(page);
  await waitForLayout(page);
  await actFromContextMenu(page, "vmbr0", "Start capture");

  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Start capture" }).click();

  const sideBySide = dialog.getByTestId("capture-side-by-side");
  await expect(sideBySide).toBeVisible();
  const panes = sideBySide.locator('[data-testid^="session-pane-"]');
  await expect(panes).toHaveCount(2);

  const pane1 = dialog.getByTestId("session-pane-sess-pve1");
  const pane2 = dialog.getByTestId("session-pane-sess-pve2");
  await expect(pane1).toContainText("pve1");
  await expect(pane2).toContainText("pve2");

  await pane1.getByRole("button", { name: "Decode" }).click();
  await pane2.getByRole("button", { name: "Decode" }).click();

  await expect(pane1.getByTestId("packet-list")).toBeVisible();
  await expect(pane2.getByTestId("packet-list")).toBeVisible();
});
