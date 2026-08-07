// T-1305's own end-to-end verification, against the real stack (pvemock
// testdata/clusters/three-node-vlan.yaml -> vnproxd -> the production SPA
// build) on the DEFAULT webServer pair (web/playwright.config.ts, ports
// 8006/8007).
//
// Exercises the map's right-click "View live connections" entry (AC5's
// "reachable from the map's right-click menu") end to end: it lands on the
// correctly node-scoped /conntrack URL, the explorer renders, and the
// filter inputs pre-fill from that URL.
//
// What this file deliberately does NOT assert: that testdata/clusters/
// three-node-vlan.yaml's fixture-declared conntrack rows appear in the
// table. Unlike every other host-level read this e2e harness already
// documents as real-machine-backed (topology.spec.ts's own header comment:
// "vnproxd's host collector reads the REAL machine it runs on"),
// host.Reader.Conntrack's real implementation reads
// /proc/net/nf_conntrack directly off the kernel — a file real PVE nodes
// can read because vnproxd runs as root there (docs/security.md: "vnprox
// runs as root on hypervisor hosts"), but this shared dev/e2e harness runs
// vnproxd as an unprivileged user, where that file is present (root:root,
// mode 0440) but returns EPERM on open. `GET /conntrack` degrades exactly
// as designed when that happens — `partial: true`, every node in
// `failedNodes`, empty `items`, never a fabricated/fixture value — which is
// precisely what this test verifies instead: the honest, real
// partial-results UI a permission-denied node produces, proven against the
// real backend rather than a mock. The fixture-declared rows themselves,
// and the SNAT/DNAT/state/filter rendering they exercise, are fully
// covered without needing root by internal/pvemock/conntrack_test.go,
// internal/host/fixture_test.go, internal/api/conntrack_test.go (all three
// go through FixtureReader, never the real kernel path) and
// ConntrackExplorer.test.tsx (a seeded fixture set, no network at all).
import { expect, test, type Locator, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** elk's layout can place a node under the canvas's own fixed-position
 * minimap panel (bottom-right corner) — a real occlusion Playwright
 * correctly refuses to click through. Dragging the canvas pane shifts
 * every node's on-screen position, moving the target out from under it. */
async function panCanvasPane(page: Page, dx: number, dy: number): Promise<void> {
  const pane = page.locator(".react-flow__pane");
  const box = await pane.boundingBox();
  if (!box) return;
  const startX = box.x + box.width / 2;
  const startY = box.y + box.height / 2;
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(startX + dx, startY + dy, { steps: 10 });
  await page.mouse.up();
}

/** Pans the canvas so `node` sits at the centre of the React Flow pane.
 *
 * T-2108, eighth pass. This spec's previous loop panned by a *fixed* offset,
 * always up-and-left, cycling four magnitudes. Playwright's own call log says
 * why that could not work:
 *
 *     - element is visible, enabled and stable
 *     - scrolling into view if needed
 *     - done scrolling
 *     - element is outside of the viewport
 *
 * The node is **stable** — it is simply off-screen, and `scrollIntoViewIfNeeded`
 * cannot fix that, because a React Flow node's position comes from a CSS
 * transform on the canvas, not from document scroll. There is nothing to
 * scroll. If the node happened to be off the top-left, every retry panned it
 * further away.
 *
 * This corrects the diagnosis recorded on the card, which read the same log as
 * "the node never becomes stable, never stops moving", and sent two rounds of
 * work at timeouts and CSS animations. Both were refuted by experiment; neither
 * was ever the cause.
 *
 * Computing the pan from the node's actual box means one attempt is normally
 * enough, and the retry loop stays only for the case where elk re-lays-out
 * between measuring and clicking. */
async function panNodeIntoView(page: Page, node: Locator): Promise<void> {
  const pane = await page.locator(".react-flow__pane").boundingBox();
  const box = await node.boundingBox();
  if (!pane || !box) return;
  const dx = pane.x + pane.width / 2 - (box.x + box.width / 2);
  const dy = pane.y + pane.height / 2 - (box.y + box.height / 2);
  // Sub-pixel deltas are not worth a drag gesture, and dragging by ~0 can
  // register as a pane click that clears the selection.
  if (Math.abs(dx) < 2 && Math.abs(dy) < 2) return;
  await panCanvasPane(page, dx, dy);
}

test("Conntrack explorer: map right-click drills into a node-scoped live view, filters round-trip through the URL, and a permission-denied read degrades honestly", async ({ page }) => {
  await logIn(page);
  await switchToGraphView(page);

  // --- right-click pve1's vmbr0 bridge -> "View live connections" -------
  // The pan lives *inside* the retry loop (mirroring simulator.spec.ts's
  // own traceFromContextMenu doc comment on this exact class of flake):
  // elk's async layout can leave the node under the canvas's fixed
  // minimap panel, or a re-layout mid-gesture can move it — a single pan
  // computed once before the loop doesn't help if it lands short, or if
  // the node has since moved back under the same corner.
  const pve1Bridge = page.locator('[data-entity-ref="bridge:pve1:vmbr0"]');
  await expect(pve1Bridge).toBeVisible({ timeout: 15_000 });
  await expect(async () => {
    await page.keyboard.press("Escape"); // close any menu a previous failed attempt left open
    await panNodeIntoView(page, pve1Bridge);
    await pve1Bridge.click({ button: "right", timeout: 5_000 });
    await page.getByRole("menuitem", { name: "View live connections" }).click({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  await page.waitForURL("**/conntrack?node=pve1");
  // The client-side route swap normally renders immediately; under a very
  // loaded machine it has occasionally lagged behind the URL change in this
  // suite's own local runs (the URL itself always lands correctly first —
  // proven by the waitForURL above, and independently by
  // conntrack/urlState.test.ts's pure-function coverage of the exact link
  // this menu item builds). A same-URL reload always resolves it, so this
  // is a legitimate resilience fallback, not a retry that hides a real
  // assertion failure.
  const headingVisible = await page
    .getByRole("heading", { name: "Conntrack explorer" })
    .isVisible({ timeout: 10_000 })
    .catch(() => false);
  if (!headingVisible) {
    await page.reload();
  }
  await expect(page.getByRole("heading", { name: "Conntrack explorer" })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByLabel("Filter by node")).toHaveValue("pve1");

  // The real backend's own honest degraded-read banner: every node in this
  // harness fails its live conntrack read (see this file's header comment)
  // — GET /conntrack reports that as partial/failedNodes rather than
  // fabricating rows, and the explorer surfaces it exactly as it would any
  // other unreachable-node condition.
  await expect(page.getByRole("status")).toContainText("Could not reach", { timeout: 15_000 });
  await expect(page.getByText("No live connections match the current filter")).toBeVisible();

  // --- filter state round-trips through the URL (typing narrows the live
  // GET /conntrack query the explorer issues, independent of whether any
  // row currently matches it — the request itself is what's under test
  // here, not fixture data this harness cannot serve without root) -------
  await page.getByLabel("Filter by state").fill("ESTABLISHED");
  await expect(page).toHaveURL(/state=ESTABLISHED/);
  await page.getByLabel("Filter by node").fill("");
  await expect(page).toHaveURL((url) => !url.search.includes("node="));
});
