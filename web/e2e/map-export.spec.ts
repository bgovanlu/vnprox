// SPDX-License-Identifier: Apache-2.0

// T-906 acceptance criteria 1/2/3/4: the "Export map" toolbar control (SVG +
// PNG, on both Graph and Switch views) and the print stylesheet, against the
// real stack (pvemock's three-node-vlan fixture, same as topology.spec.ts).
//
// The exported SVG is a self-contained document (export.ts's renderSvg)
// that tags every rendered entity with stable, parseable hooks —
// data-export-entity="node"|"edge", data-entity-ref, data-entity-kind — and
// the root <svg> with data-export-node-count/data-export-edge-count summary
// attributes, specifically so a test can assert "only the filtered/toggled
// entity set" (AC1) without depending on visual layout.
import { readFile } from "node:fs/promises";
import { expect, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

interface ExportedSvg {
  text: string;
  nodeCount: number;
  edgeCount: number;
}

/** Opens the "Export map" menu, clicks "Download SVG", and reads the
 * downloaded file's text plus its declared node/edge counts. */
async function exportSvg(page: Page): Promise<ExportedSvg> {
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export map" }).click();
  await page.getByRole("menuitem", { name: "Download SVG" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toMatch(/^vnprox-map-.*\.svg$/);
  const path = await download.path();
  if (!path) throw new Error("download did not save to a local path");
  const text = await readFile(path, "utf-8");
  const nodeCountMatch = /data-export-node-count="(\d+)"/.exec(text);
  const edgeCountMatch = /data-export-edge-count="(\d+)"/.exec(text);
  if (!nodeCountMatch || !edgeCountMatch) throw new Error("downloaded file is not a recognizable map export SVG");
  return { text, nodeCount: Number(nodeCountMatch[1]), edgeCount: Number(edgeCountMatch[1]) };
}

test.describe("Map export (SVG/PNG) — Graph view", () => {
  test("SVG export reflects a layer toggle and a VLAN filter — only the filtered/toggled entity set", async ({
    page,
  }) => {
    await logIn(page);
    await switchToGraphView(page);
    // Let the initial layout settle so the baseline export isn't captured
    // mid-layout (elkjs positions arrive asynchronously — see
    // topology.spec.ts's identical wait).
    await page.waitForFunction(() => document.querySelectorAll(".react-flow__node").length >= 10);

    // --- Baseline: every layer on, no VLAN filter ---------------------
    const baseline = await exportSvg(page);
    expect(baseline.nodeCount).toBeGreaterThan(0);
    // Fixture-declared entities across every layer are all present.
    expect(baseline.text).toContain('data-entity-kind="physnic"');
    expect(baseline.text).toContain('data-entity-kind="bridge"');
    expect(baseline.text).toContain('data-entity-kind="guest-nic"');

    // --- Toggle off the Physical layer + filter to VLAN 20 -------------
    // (vmbr0.20, the "storage VLAN" sub-interface every fixture node
    // declares — testdata/clusters/three-node-vlan.yaml.)
    await page.getByRole("button", { name: /physical/i }).click();
    // exact: true — getByLabel's default substring match also picks up
    // several Graph-view edge aria-labels that happen to contain "vlan"
    // (e.g. "Edge from vlan:pve1:vmbr0.20 to bridge:pve1:vmbr0").
    const vlanInput = page.getByLabel("VLAN", { exact: true });
    await vlanInput.fill("20");
    await vlanInput.press("Enter"); // submits VlanFilterInput's <form>, same as clicking "Apply"

    const filtered = await exportSvg(page);

    // The toggled-off layer's entity kind never appears at all.
    expect(filtered.text).not.toContain('data-entity-kind="physnic"');
    // Strictly fewer entities than the unfiltered baseline: the layer
    // toggle removed a whole kind, and the VLAN filter dropped every
    // non-matching node/edge on top of that.
    expect(filtered.nodeCount).toBeLessThan(baseline.nodeCount);
    expect(filtered.nodeCount).toBeGreaterThan(0);
    // The embedded caption records the active filters (deliverable 2:
    // "current filter/legend state ... as a caption").
    expect(filtered.text).toContain("VLAN filter: 20");
    expect(filtered.text).toContain("Layers hidden: Physical");
  });

  test("PNG export produces a non-empty image of the same filtered scene", async ({ page }) => {
    await logIn(page);
    await switchToGraphView(page);
    await page.waitForFunction(() => document.querySelectorAll(".react-flow__node").length >= 10);

    await page.getByRole("button", { name: /guests/i }).click(); // narrow the scene a bit

    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "Export map" }).click();
    await page.getByRole("menuitem", { name: "Download PNG" }).click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^vnprox-map-.*\.png$/);
    const path = await download.path();
    if (!path) throw new Error("download did not save to a local path");
    const bytes = await readFile(path);
    // Non-empty, and a real PNG (the 8-byte PNG signature) — AC2's
    // "byte-size/dimension sanity check, not pixel-exact".
    expect(bytes.length).toBeGreaterThan(100);
    expect(bytes.subarray(0, 8)).toEqual(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
  });
});

test("Export map is also available on the Switch view (the default landing view)", async ({ page }) => {
  await logIn(page);
  // No switchToGraphView call: this asserts against the Switch faceplate
  // view, /topology's default (see topology.spec.ts's own regression test
  // for that default).
  await expect(page.getByRole("button", { name: "vmbr0 switch" }).first()).toBeVisible();

  const exported = await exportSvg(page);
  expect(exported.nodeCount).toBeGreaterThan(0);
  expect(exported.text).toContain('data-entity-kind="bridge"');
});

test("Print stylesheet hides toolbar/drawer chrome while map entities stay visible", async ({ page }) => {
  await logIn(page);
  await expect(page.getByRole("button", { name: "Export map" })).toBeVisible();
  const switchEntity = page.getByRole("button", { name: "vmbr0 switch" }).first();
  await expect(switchEntity).toBeVisible();

  await page.emulateMedia({ media: "print" });

  // Toolbar chrome (layer toggles, VLAN filter, Export map, search) is
  // hidden under print media (TopologyPage.tsx's print:hidden toolbar row).
  await expect(page.getByRole("button", { name: "Export map" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Search ( / )" })).toBeHidden();
  // The global app chrome (nav rail, top bar) is hidden too (index.css's
  // @media print rule).
  await expect(page.getByRole("navigation", { name: "Primary" })).toBeHidden();

  // The map itself — a real rendered entity — stays visible.
  await expect(switchEntity).toBeVisible();

  // The print-only filter/legend caption is now shown.
  await expect(page.getByText("vnprox topology map — Switch view")).toBeVisible();

  await page.emulateMedia({ media: "screen" });
});
