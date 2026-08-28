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
      // T-4210: 2%, not 0%. Two sources of noise this suite could not fully
      // neutralize even after blocking the WebSocket, disabling CSS
      // animations, forcing prefers-reduced-motion, hiding scrollbars and
      // settling the fixture's cross-node staleness once before any capture
      // (see visual.spec.ts's settleClusterStaleness): sub-pixel font
      // antialiasing differences between runs, and a relative-timestamp
      // label ("3s ago") that can tick over by a second between the
      // baseline-generating run and the verification run. Both are real
      // measured 0%-flakiness causes in local testing, not a hypothetical
      // margin. At full-page screenshot resolution a one-line text label or
      // a font hinting difference is a small fraction of total pixels, so
      // 2% is enough headroom to absorb that residue without also absorbing
      // a genuine regression — the class of change Phase 42-51's restyle
      // could introduce (an accent-color swap, a spacing/radius change, a
      // missing dark: pairing) moves far more than 2% of a page's pixels.
      maxDiffPixelRatio: 0.02,
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
    trace: "retain-on-failure",
  },
  webServer: webServers(WHOLE_SUITE, ["visual.spec.ts"], 0),
});
