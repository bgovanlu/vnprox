// T-107 acceptance criterion 1's render-verification artifact (audit
// finding F-06): against the real stack (pvemock three-node-vlan fixture →
// vnproxd collectors → GET /topology → the production SPA build), log in
// with real Proxmox-style credentials, open /topology, and verify all four
// layer bands render — then capture a committed screenshot baseline.
//
// Environment caveat, documented rather than hidden: vnproxd's host
// collector reads the REAL machine it runs on (there is no PVE host here),
// so the pve1 band also contains this dev machine's own interfaces (e.g.
// lo, plus whatever NICs the machine has) alongside the fixture's
// pve-declared ones, and the LLDP loop fails unless lldpctl is installed.
// The assertions below therefore pin only fixture-declared, PVE-derived
// entities (stable across machines); the screenshot baseline IS
// machine-dependent — regenerate with `npm run e2e:update` when running on
// a different machine, and treat the committed baseline as this repo's
// reference environment, not a universal truth.
import { expect, test, type Page } from "@playwright/test";
import { switchToGraphView } from "./helpers";

// T-605: a fresh-DB first login (every run here, per the shared webServer
// command's own doc comment) shows the onboarding walkthrough banner
// (docs/user-guide.md §1) between TopBar and <main> — in normal document
// flow, so it pushes the topology canvas down rather than floating over
// it (AppShell.tsx's doc comment explains why: every fixed-position corner
// collided with some page's own controls). That's correct product
// behavior, but this file's screenshot baseline is about the map's
// steady-state rendering, not about the walkthrough. Suppressing it via an
// injected stylesheet (rather than clicking its own "Minimize" button)
// deliberately does NOT persist anything server-side (no PUT
// /layouts/onboarding) — clicking Minimize was tried first and turned out
// to permanently dismiss root@pam's walkthrough for the rest of this
// shared-webServer test run, breaking onboarding.spec.ts's own later
// assertion that a fresh root@pam login shows it. addInitScript runs
// before page scripts on every navigation in this page (including this
// file's own mid-test page.reload()), so the suppression holds throughout.
//
// The same treatment extends (T-607 stabilization) to the two purely
// environmental artifacts this machine's stack always produces, whose
// *timing* depends on how long the shared vnproxd has been up when this
// spec's turn in the suite comes — which made the screenshot baseline
// depend on suite position:
//  - the collector-staleness banner + the no-LLDP banner (header comment:
//    the fixture's peer nodes are unreachable from this machine and
//    lldpctl isn't installed, so host/lldp sources inevitably go stale a
//    minute or two in). Both banners sit above the canvas in document
//    flow, so their appearance (and their line count, which varies with
//    which peers have hit circuit-open and how their error strings wrap)
//    changes the flexed canvas height — the suite-run capture came out
//    ~100px shorter than the single-spec-run baseline, failing the
//    dimension check before any pixel is compared.
//  - the stale-band grey-out (EntityNode.tsx's grayscale/opacity-60 for
//    nodes whose band a stale node-scoped source covers): the same aging
//    turns whole columns grey mid-suite that render colored in a fresh
//    single-spec run.
// Both are real, correct product behavior for a genuinely-stale collector
// — but here they encode "how old is the shared test server", not
// anything about the render pipeline this baseline verifies, so the
// screenshot normalizes them away. StalenessBanner.test.tsx and
// staleness.test.ts cover the banner/grey behavior itself.
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    // docs/security.md's CSP is `style-src 'self'` (no 'unsafe-inline'),
    // so an injected <style> element (tried first) is silently blocked —
    // it exists in the DOM but the browser refuses to apply its rule.
    // Directly setting a CSSStyleDeclaration property via the `.style`
    // object (as opposed to the HTML `style` *attribute*, e.g. via
    // setAttribute) is not restricted by style-src (a well-known CSP
    // nuance), so this sets the property JS-side instead — reapplied via
    // MutationObserver since the banner mounts asynchronously (after
    // GET /layouts/onboarding resolves) well after this init script runs.
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
      // Environment banners (see this function's doc comment). Scoped to
      // the amber-banner class both share (TopologyPage.tsx /
      // StalenessBanner.tsx) plus a text check, so nothing else amber
      // (e.g. a node's own mgmt badge) can match. display:none only —
      // never removed — so text-presence assertions still see them.
      for (const banner of document.querySelectorAll("div.border-amber-300")) {
        const text = banner.textContent;
        if (banner instanceof HTMLElement && (text.includes("last-known data") || text.includes("No LLDP data yet"))) {
          banner.style.setProperty("display", "none", "important");
        }
      }
      // Stale-band grey-out (see this function's doc comment): strip the
      // grayscale filter + 60% opacity EntityNode.tsx applies to nodes in
      // a stale band. opacity-25 (the VLAN filter's dim) is untouched —
      // this file never applies a VLAN filter.
      for (const node of document.querySelectorAll(".react-flow__node .grayscale")) {
        if (node instanceof HTMLElement) {
          node.style.setProperty("filter", "none", "important");
          node.style.setProperty("opacity", "1", "important");
        }
      }
    };
    // lib.dom.d.ts types document.documentElement as always non-null, but
    // empirically (this exact init-script timing, before the parser has
    // created it yet) it can genuinely be null here — hence the try/catch
    // retry loop below instead of a type-checker-satisfying null check
    // that strict lint would flag as "always falsy" against that (in this
    // one narrow case, wrong) type.
    const start = () => {
      try {
        suppress();
        // attributeFilter: a stale band arriving via a topology *refetch*
        // flips `grayscale` on already-mounted nodes — a class-attribute
        // mutation childList alone would miss. Scoped to `class` so React
        // Flow's per-frame style-attribute updates (pan/zoom transforms)
        // don't re-run suppress on every animation frame. setProperty with
        // an unchanged value emits no mutation record, so this cannot loop.
        new MutationObserver(suppress).observe(document.documentElement, {
          childList: true,
          subtree: true,
          attributes: true,
          attributeFilter: ["class"],
        });
      } catch {
        setTimeout(start, 0);
      }
    };
    start();
  });
}

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("all four layer bands render on /topology against the real backend", async ({ page }) => {
  await suppressOnboardingWalkthrough(page);
  await logIn(page);
  // This whole test is about the elk graph canvas (fitView, .react-flow
  // node counts, the committed screenshot baseline below) — switch to the
  // Graph view up front rather than at each individual react-flow query
  // (67fff26 landed Switch, not Graph, as /topology's default view).
  await switchToGraphView(page);

  // One fixture-declared entity per layer (labels from
  // testdata/clusters/three-node-vlan.yaml via the real collector
  // pipeline). Generous timeout: the first collector poll cycle may still
  // be in flight right after the daemon starts.
  const perLayer: Record<string, string> = {
    phys: "eno1", // physical NIC
    l2: "vmbr0", // bridge
    sdn: "vlanz", // SDN zone (cluster-spanning band)
    guest: "app01/net0", // guest NIC
  };
  for (const [layer, label] of Object.entries(perLayer)) {
    await expect(
      page.getByRole("button", { name: label }).first(),
      `expected a visible ${layer}-layer node labeled ${label}`,
    ).toBeVisible();
  }

  // The four layer toggles exist and are all active by default.
  for (const layer of ["phys", "l2", "sdn", "guest"]) {
    await expect(page.getByRole("button", { name: new RegExp(layer, "i") }).first()).toBeVisible();
  }

  // All three per-node columns are populated: each node's bridge renders.
  await expect(page.getByRole("button", { name: "vmbr0", exact: true })).toHaveCount(3);

  // No staleness banner: with pvemock up and polling healthy, the §5
  // last-known-data banner must be absent. (The lldp source may legitimately
  // be failing on a machine without lldpctl — during the first ~3 poll
  // cycles it is not yet marked stale, so assert only the pve-driven case:
  // the map itself rendered fresh, cluster-wide data.)
  await expect(page.getByText("This map is showing last-known data")).toHaveCount(0);

  // React Flow's `fitView` prop fits the viewport on mount — before the
  // async elkjs layout has positioned the nodes (they briefly stack at the
  // origin) — so wait for the layout to have spread the nodes out, then
  // refit explicitly, or the screenshot captures a mostly-empty corner of
  // the canvas.
  await page.waitForFunction(() => {
    const nodes = Array.from(document.querySelectorAll(".react-flow__node"));
    const transforms = new Set(nodes.map((n) => (n instanceof HTMLElement ? n.style.transform : "")));
    return nodes.length >= 10 && transforms.size > nodes.length / 2;
  });
  await page.getByRole("button", { name: "fit view" }).click();
  // Let the viewport transition settle before pixel-comparing. (T-605:
  // bumped from 800ms — under the load of this repo's now-larger e2e
  // suite run sequentially in one worker, 800ms occasionally wasn't
  // enough for the canvas container's post-suppression reflow to fully
  // settle before the screenshot, shrinking the captured height by
  // 80-300px nondeterministically; this didn't reproduce running this
  // spec file alone. Not fully eliminated by this change alone if the
  // sandbox is under heavy load — see this task's report.)
  await page.waitForTimeout(2000);

  // T-702's mgmt-path/mgmt badges (EntityNode.tsx) are new content on nodes
  // that previously had no badge row at all (e.g. bare physnics) — assert
  // at least one renders before the pixel comparison below, so a real
  // regression here fails loudly on this line instead of silently shrinking
  // the committed baseline's amber-badge coverage.
  await expect(page.getByText("mgmt-path").first()).toBeVisible();

  // Committed baseline (see header comment: machine-dependent; regenerate
  // with npm run e2e:update). Screenshots the canvas element rather than
  // the full page so timing-dependent banners above it (e.g. the lldp
  // source going stale ~90s in on machines without lldpctl) can't shift
  // the layout mid-run.
  //
  // Re-baselining gotcha (found landing T-702's amber badges): when an
  // existing baseline PNG is already committed at this path,
  // `--update-snapshots` skips its normal multi-frame stabilization wait
  // and writes back the *first* screenshot it takes — which can land
  // before an in-flight re-render (e.g. a second /topology fetch filling
  // in badges the first response didn't have yet) has painted. With no
  // pre-existing file at this path it correctly waits for a stable frame
  // instead. So: `rm` the old PNG under topology.spec.ts-snapshots/
  // *before* re-running with --update-snapshots, not just re-run in place
  // over the old one — the toBeVisible() check above catches the DOM/data
  // side of this but can't catch a stale-frame screenshot on its own.
  await expect(page.locator(".react-flow").first()).toHaveScreenshot("topology-three-node-vlan.png", {
    maxDiffPixelRatio: 0.05,
    animations: "disabled",
  });
});

// Pins /topology's default view mode (67fff26): a fresh login must land on
// the Switch faceplate view, not the Graph canvas — and the toggle must
// still be able to reach Graph. This is a regression test for the exact
// bug this task fixed (every other spec in this suite assumed Graph was
// the landing view and silently broke when the default flipped to Switch);
// it exists to catch a silent default-flip in *either* direction in future.
test("Topology lands on the Switch view by default, and Graph is reachable via the toggle", async ({ page }) => {
  await suppressOnboardingWalkthrough(page);
  await logIn(page);

  // Switch is the landing view: its radio is checked, Graph's is not, and
  // the react-flow canvas has not mounted at all (not merely hidden).
  await expect(page.getByRole("radiogroup", { name: "Topology view mode" })).toBeVisible();
  await expect(page.getByRole("radio", { name: "Switch" })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByRole("radio", { name: "Graph" })).toHaveAttribute("aria-checked", "false");
  await expect(page.locator(".react-flow")).toHaveCount(0);
  // A faceplate rendered — e.g. pve1's vmbr0 switch header.
  await expect(page.getByRole("button", { name: "vmbr0 switch" }).first()).toBeVisible();

  // Toggling to Graph swaps in the elk canvas.
  await switchToGraphView(page);
  await expect(page.getByRole("radio", { name: "Graph" })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByRole("radio", { name: "Switch" })).toHaveAttribute("aria-checked", "false");
});
