// SPDX-License-Identifier: Apache-2.0

// T-4210: the visual regression gate's own Playwright config —
// deliberately NOT web/playwright.config.ts's sharded arrangement. Two hard
// reasons, not a style preference:
//
//  1. web/e2e/shards.ts's validateManifest() requires every *.spec.ts file
//     on disk to be assigned to a shard in web/e2e/shards.json (visual.spec.ts
//     has a placeholder entry there — see that file's own note). But even
//     satisfied, running visual.spec.ts *through* web/playwright.config.ts in
//     unsharded "whole suite" mode would boot every stack every OTHER spec
//     file needs (sim, scale, mgmt, alert, flow, physcollapse, k8s, and two
//     demo daemons — on the order of ten processes) just to run this one
//     file, which is the opposite of "must be possible to run the visual job
//     alone."
//  2. Determinism. A screenshot gate cannot share a daemon/store with specs
//     that mutate app state (changesets, muted findings, pinned layouts,
//     schedules...) — running inside a shard's shared stack would make every
//     baseline race whatever else that shard's other spec files happened to
//     do first. web/e2e/visual.spec.ts's own top-of-file skip guard is the
//     other half of this: every test in that file is a no-op unless it is
//     actually running through THIS config.
//
// So this suite gets its own single-stack, single-process run. It reuses
// web/e2e/shards.ts's `webServers()` helper (imported, not reimplemented) to
// boot exactly the "default" three-node-vlan stack — nothing else, since
// visual.spec.ts names no other stack — on the same 8006/8007 ports the
// unsharded whole-suite run already uses. Those are already-registered ports
// (testdata/dev-ports.tsv: pvemock-default, vnproxd-default), so this claims
// nothing new; it relies on scripts/ci-local.sh's documented "run jobs
// sequentially" convention to never race `make e2e`/job_e2e for them.
// `webServers()` calls `resetShardState()` internally, wiping this stack's
// store before every invocation, so two consecutive standalone runs never
// inherit state from each other — the property T-4210's "run it twice"
// determinism proof depends on.
//
// vnproxd embeds the production SPA build at compile time (same as
// web/playwright.config.ts's own stack), so `npm run build` must run before
// this config is loaded for real — scripts/ci-local.sh's job_visual does
// that; a bare `npx playwright test --config=playwright.visual.config.ts`
// against a stale/missing web/dist will serve a stale/missing SPA, the same
// footgun `make e2e`'s own header comment calls out for its stack.
//
// Run via `scripts/ci-local.sh visual`, or directly:
//   npx playwright test --config=playwright.visual.config.ts
//   npx playwright test --config=playwright.visual.config.ts --update-snapshots
import { defineConfig } from "@playwright/test";

import { webServers, WHOLE_SUITE } from "./e2e/shards";

// The marker web/e2e/visual.spec.ts's describe-level test.skip() looks for.
// Set here (not only by the caller) so this config is self-sufficient: the
// bare command above is enough on its own, with no environment setup step to
// remember first.
process.env.VNPROX_VISUAL_SNAPSHOTS = "1";

export default defineConfig({
  testDir: "./e2e",
  testMatch: ["visual.spec.ts"],
  // One worker, against one dedicated daemon (see the header comment) —
  // not shards.ts's per-shard replicas. A second worker here would be a
  // second browser hammering the same one store, the exact shared-mutable-
  // state hazard this config exists to avoid.
  workers: 1,
  fullyParallel: false,
  // Playwright applies this to beforeAll hooks too, not just tests — needs
  // to comfortably cover settleClusterStaleness's own up-to-120s wait
  // (visual.spec.ts) plus login, or the hook gets aborted mid-flight
  // ("Test ended") before it can save the storage state every other test
  // in the file depends on. Measured empirically: 60s was not enough.
  timeout: 180_000,
  expect: {
    timeout: 30_000,
    toHaveScreenshot: {
      animations: "disabled",
      // T-4213: 0.001, measured — not chosen.
      //
      // T-4210 set this to 0.02 and attributed the residue to sub-pixel font
      // antialiasing plus a relative-timestamp label ticking over between
      // runs, recording the second as not neutralizable "without a source
      // change this task's scope excludes". Both halves turned out to be
      // wrong, and measuring is what showed it.
      //
      // `page.clock` has shipped since Playwright 1.45 and this repo pins
      // 1.61.1, so the clock IS freezable from outside the app —
      // visual.spec.ts now does it. But freezing it barely moved the number,
      // because the dominant cause was never a client-derived label: it was
      // timestamps the SERVER produces, rendered as absolute local time, on
      // pages whose content the test suite itself writes. With the clock
      // frozen and baselines regenerated, `/audit` still differed by 103,641
      // of 1,260,000 pixels — **8.2%, four times the tolerance the gate was
      // already set to.** The suite was photographing its own logins.
      //
      // So the gate was not "loose"; it was loose AND non-deterministic, and
      // nothing had caught that because the visual suite had never been run
      // twice in a row (and `make ci`, the pre-push gate, omits the e2e job
      // entirely — see the Makefile).
      //
      // With the volatile regions masked (visual.spec.ts's VOLATILE), the
      // residue across all 102 captures measures:
      //
      //     /settings/certificates   246 px   0.0195%   <- worst
      //     /analysis                220 px   0.0175%
      //     every other capture        0 px
      //
      // That is the antialiasing floor T-4210 was reaching for, and it is
      // ~100x smaller than the tolerance it settled on.
      //
      // The value is 0.0005 rather than 0.001, and the reason is the second
      // half of T-4213's proof — "confirm the gate FAILS on a real change".
      // Sizing the things this gate is supposed to catch, at fullPage
      // 1400x900 (1,260,000 px):
      //
      //     a StatusDot        h-2 w-2      ~64 px
      //     a small Badge      soft chip  ~1,200 px
      //     0.001 tolerance                 1,260 px   <- larger than both
      //     0.0005 tolerance                  630 px
      //
      // At 0.001 a single recoloured badge is INSIDE the tolerance. That is
      // not a threshold that catches "a status badge that changed colour",
      // which is the property the card asked for. 0.0005 is 2.5x the measured
      // 246 px floor and catches anything larger than roughly a 25x25 block.
      //
      // An 8px status dot remains below it, and no full-page ratio gate will
      // ever catch one — that is a real limit of this technique and is worth
      // stating rather than implying otherwise. Component-level assertions
      // (index.css.test.ts, findingChipContrast.test.ts) are what cover marks
      // that small.
      maxDiffPixelRatio: 0.0005,
      // `threshold` is deliberately LEFT AT PLAYWRIGHT'S DEFAULT of 0.2, and
      // that is a known blind spot rather than an oversight. Measured, because
      // T-4213 asked for a ratio and the ratio turned out not to be the
      // binding constraint:
      //
      // `threshold` is a per-PIXEL tolerance in YIQ space. A pixel whose
      // colour moved less than it is not counted as different at all, so
      // `maxDiffPixelRatio` never sees it. Changing
      // `--color-status-degraded` from #9b6200 to #8a5a10 — a real, visible
      // token regression — moves YIQ luminance by **0.0312, six times below
      // the 0.2 default**. The suite reported 102 passed. No value of
      // maxDiffPixelRatio would have changed that, which means tightening the
      // ratio (this card's entire premise) could not have fixed the blind
      // spot the card describes. The same trap cost T-4302 six tool calls
      // when `--update-snapshots` silently rewrote nothing for an
      // #ecfdf5 -> #ffffff change.
      //
      // Lowering it was tried and measured, and the two settings trade
      // against each other:
      //
      //                              threshold 0.2   threshold 0.02
      //     /settings/certificates        246 px         3,750 px
      //     /audit (masked)                 0 px       144,690 px
      //     one small Badge             ~1,200 px       ~1,200 px
      //
      // At 0.02 the antialiasing floor on a text-dense page EXCEEDS a single
      // badge, so no ratio can both suppress the noise and catch the badge.
      // "Catch a one-badge colour change on a full-page screenshot" is not
      // achievable with this technique at this viewport, and pretending
      // otherwise by shipping an unstable pair would be worse than saying so.
      //
      // What this gate is therefore FOR, stated plainly: layout, geometry,
      // spacing, and colour changes above ~ΔY 0.2. What covers the rest is
      // the token-level assertions (index.css.test.ts measures every status
      // and foreground role against every surface it lands on) and the axe
      // sweep, which measures rendered pixels against real backgrounds. See
      // T-4213's card for the follow-up.
    },
  },
  // Baselines are deliberately NOT committed (docs/development.md's "Visual
  // regression gate" section): Phases 42-51 restyle the entire product and
  // would invalidate every one of them on the very first card. Routed into
  // web/test-results/, already gitignored, instead of the default
  // `<file>-snapshots/` location next to the spec — which IS the committed
  // convention topology.spec.ts's one baseline uses, and deliberately not
  // this suite's.
  snapshotPathTemplate: "test-results/visual-baselines/{arg}-{platform}{ext}",
  outputDir: "./test-results/visual",
  reporter: [["list"], ["json", { outputFile: "./test-results/visual-report.json" }]],
  use: {
    baseURL: "https://127.0.0.1:8007",
    // testdata/certs is a throwaway self-signed dev keypair (dev.toml),
    // same as web/playwright.config.ts's own `use`.
    ignoreHTTPSErrors: true,
    // Fixed viewport (deliverable 3): every capture is the same size, on
    // every route, in every mode.
    viewport: { width: 1400, height: 900 },
    // T-4210-followup: scrollbars are hidden by a BROWSER FLAG, not by
    // injecting CSS into the page.
    //
    // This suite's first real run failed all 96 captures on
    // `page.addStyleTag: Applying inline style violates the following Content
    // Security Policy directive 'style-src 'self''`. That is the app's own
    // CSP working correctly — vnproxd serves a strict policy and a
    // screenshot helper is not entitled to an exemption from it. Relaxing
    // the header for the test build would make this gate guard a page that
    // is not the page users get, which is the one thing a visual gate must
    // never do.
    //
    // --hide-scrollbars achieves the same determinism outside the page, so
    // the document under test is byte-for-byte the production document.
    launchOptions: { args: ["--hide-scrollbars"] },
    // The app collapses every motion token to 0.01ms under
    // prefers-reduced-motion (T-4206's global gate). Asking the browser to
    // report that preference is how this suite stops animations, again
    // without touching the page: it exercises a code path the product
    // already ships rather than one invented for the test.
    // Nested under contextOptions: in Playwright 1.61 `reducedMotion` is a
    // BrowserContext option, not a top-level test option — the top-level
    // spelling type-errors rather than silently doing nothing, which is the
    // better of the two ways to be wrong about this.
    contextOptions: { reducedMotion: "reduce" },
    trace: "retain-on-failure",
  },
  webServer: webServers(WHOLE_SUITE, ["visual.spec.ts"], 0),
});
