// Opt-in end-to-end verification of the topology UI against the REAL stack
// (audit findings F-06/F-07): pvemock + vnproxd serving the production SPA
// build from web/dist. Run with `npm run e2e` (which builds the SPA first —
// vnproxd embeds web/dist at compile time). Deliberately NOT part of
// `make check` / `npm test`: it needs a downloaded Chromium (npx playwright
// install chromium), a Go toolchain, and the ports `make ports` lists — see
// docs/testing/topology-render-verification.md.
//
// T-2505 made the suite shardable. Everything about *which* specs run, *which*
// stacks come up and *on which ports* now lives in e2e/shards.ts; this file is
// the thin Playwright-shaped wrapper around it. Two modes:
//
//   npx playwright test                      whole suite, one process, 8006/8007
//   VNPROX_E2E_SHARD=shard-2 npx playwright test    that shard only, on its own ports
//
// `scripts/e2e-shards.sh` runs all four shards concurrently and hands their
// reports to cmd/e2egate, which is what decides whether the run passed.
//
// workers stays at 1 INSIDE a shard. A second worker in one process would
// share that shard's single vnproxd, store and collector — the arrangement
// T-2409 exists to remove. Parallelism comes from running shards as separate
// processes with separate stacks, which is the same change that gives them
// isolation.
import { availableParallelism } from "node:os";

import { defineConfig } from "@playwright/test";

import { activeShard, allSpecs, stackURL, validateManifest, webServers, WHOLE_SUITE } from "./e2e/shards";

validateManifest();

const shard = activeShard();
const name = shard?.name ?? WHOLE_SUITE;
const specs = shard?.specs ?? allSpecs();
const slot = shard?.slot ?? 0;

/** How much longer every deadline gets on a small machine.
 *
 * T-2505, measured. These deadlines are wall clock, and every one of them is
 * waiting on a real daemon and a real browser rather than on this process. The
 * dev host is 32-core; a hosted `ubuntu-latest` runner is 2-4. On commit
 * `4968bf3` that difference alone — same commit, same command — was two failed
 * specs there and none here (`T-2505-input-02`).
 *
 * Reproduced locally with `taskset -c 0,1`: one whole shard on two cores fails
 * exactly one test per run, a DIFFERENT one each time (`guest-interior` in one
 * run, `a11y: Topology (Graph view, v1)` in the next), each of them a
 * `toBeVisible` that ran out its 30-second budget — and every one of them
 * passes on the same two cores when run alone. That is not four flaky specs;
 * it is a suite whose deadlines are tight enough to be a function of the
 * machine, which is the conclusion `T-2505-input-01` reached from four
 * independent sightings.
 *
 * Scaling them is cheap in the case that matters: a deadline only costs time
 * when it fires, so a passing run on a big machine is unaffected.
 *
 * availableParallelism() and not cpus().length: it honours CPU affinity, so a
 * `taskset`-restricted run — and a container with a restricted cpuset — reads
 * the cores it can actually use rather than the cores the box has. */
const cores = availableParallelism();
const slowFactor = cores >= 8 ? 1 : cores >= 4 ? 1.5 : 2.5;

export default defineConfig({
  testDir: "./e2e",
  // Only this shard's specs. A file in no shard is a hard error inside
  // validateManifest above, so this can never silently shrink the suite.
  testMatch: specs.map((s) => `**/${s}`),
  workers: 1,
  fullyParallel: false,
  timeout: 120_000 * slowFactor,
  expect: { timeout: 30_000 * slowFactor },
  // Per-shard, so four concurrent shards do not overwrite each other's traces
  // or reports. cmd/e2egate reads every file in shards/ as one run.
  outputDir: `./test-results/${name}`,
  reporter: [
    ["list"],
    ["json", { outputFile: `./test-results/shards/${name}.json` }],
  ],
  use: {
    // This shard's own default-stack daemon: shard-2's is not on 8007. A
    // literal here would have shard-2 quietly driving shard-1's daemon.
    baseURL: stackURL("default"),
    // testdata/certs is a throwaway self-signed dev keypair (dev.toml).
    ignoreHTTPSErrors: true,
    viewport: { width: 1400, height: 900 },
    trace: "retain-on-failure",
  },
  webServer: webServers(name, specs, slot),
});
