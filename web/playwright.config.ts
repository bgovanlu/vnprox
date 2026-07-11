// Opt-in end-to-end verification of the topology UI against the REAL stack
// (audit findings F-06/F-07): pvemock (three-node-vlan fixture) + vnproxd
// serving the production SPA build from web/dist. Run with `npm run e2e`
// (which builds the SPA first — vnproxd embeds web/dist at `go run`
// compile time). Deliberately NOT part of `make check` / `npm test`: it
// needs a downloaded Chromium (npx playwright install chromium), a Go
// toolchain, and free ports 8006/8007 — see docs/testing/
// topology-render-verification.md.
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  // The two servers below are one stack; a parallel second worker would
  // fight over the shared vnproxd (session store, collector timing).
  workers: 1,
  fullyParallel: false,
  timeout: 120_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: "https://127.0.0.1:8007",
    // testdata/certs is a throwaway self-signed dev keypair (dev.toml).
    ignoreHTTPSErrors: true,
    viewport: { width: 1400, height: 900 },
    trace: "retain-on-failure",
  },
  webServer: [
    {
      // The mock PVE cluster the collectors poll (docs/development.md's
      // `make dev` shape, minus the Vite dev server: e2e runs against the
      // production build vnproxd itself serves).
      command: "go run ./cmd/pvemock --addr 127.0.0.1:8006 --fixture testdata/clusters/three-node-vlan.yaml",
      cwd: "..",
      port: 8006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      // Fresh SQLite store AND fresh interfaces sandbox per run: e2e specs
      // create/apply changesets (changesets.spec.ts, T-207). Stale drafts
      // would pile up in the drawer's "resume parked drafts" list, and —
      // now that the dev_interfaces_dir sandbox lets an apply actually
      // succeed — a committed change (e.g. vmbr77) would persist in
      // var/dev-host and pollute the next run's base file. The dev
      // NodeAgent re-seeds var/dev-host's fixture when it is missing, so
      // removing it restores a clean, deterministic starting state.
      command: "sh -c 'rm -f var/dev-vnprox.db && rm -rf var/dev-host && exec go run ./cmd/vnproxd --config testdata/dev.toml'",
      cwd: "..",
      url: "https://127.0.0.1:8007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
});
