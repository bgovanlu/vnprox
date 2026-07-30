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
    // T-1502's own mock k8s API server (k8s-overlay.spec.ts), additive: a
    // plain-HTTP standalone internal/k8smock instance on port 8008,
    // alongside (not a separate pvemock+vnproxd pair for) the three-node-
    // vlan stack above — the spec registers this as a real k8s cluster via
    // POST /k8s/clusters against the already-running 8007 vnproxd, whose
    // kubeconfig `server:` field points straight at this address (a
    // bearer-token kubeconfig needs no TLS at all — internal/k8s.Client's
    // http.Transport handles a plain `http://` BaseURL the same way
    // `kubectl` would). testdata/k8s/e2e-cluster.yaml's own doc comment
    // explains why its one node's InternalIP is deliberately set to
    // three-node-vlan.yaml's own real IPAM allocation for guest vmid 200
    // (app01/pve1) — a genuinely MATCHED node<->guest correlation, so
    // PodDrilldown's pod -> node-guest -> bridge -> bond chain has real
    // data to trace end to end against the real stack, not a synthetic
    // stand-in.
    {
      command: "go run ./cmd/k8smock --addr 127.0.0.1:8008 --fixture testdata/k8s/e2e-cluster.yaml",
      cwd: "..",
      port: 8008,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    // T-504's own stack (simulator.spec.ts), additive: a second, distinct
    // mock PVE + vnproxd pair on ports 18006/18007 serving
    // testdata/clusters/sim-lab.yaml, purpose-built for the path
    // simulator's AC1 (deny + firewall-editor deep link)/AC2
    // (unreachable-VLAN map rendering)/AC5 (trace-path pre-fill) E2E
    // coverage — see that fixture's own doc comment for why it's a
    // separate cluster rather than reusing three-node-vlan.yaml (shared by
    // other tasks' specs, and unable to express either scenario as-is) or
    // firewall-scenarios.yaml (single-node, no cross-node L2 case for AC2).
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:18006 --fixture testdata/clusters/sim-lab.yaml",
      cwd: "..",
      port: 18006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-sim-vnprox.db && rm -rf var/dev-sim-host && exec go run ./cmd/vnproxd --config testdata/dev-sim.toml'",
      cwd: "..",
      url: "https://127.0.0.1:18007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    // T-607's own stack (scale.spec.ts), additive: a third mock PVE + vnproxd
    // pair on ports 28006/28007 serving testdata/clusters/scale-lab.yaml (the
    // docs/features/topology.md §4 scale target — 8 nodes x 6 NICs, 4
    // bridges/node, 300 guests, 40 VNets), purpose-built for the frontend
    // initial-render/pan-zoom measurement and progressive-disclosure
    // verification at real scale. A longer boot timeout than the other two
    // pairs: this fixture's collectors have ~8x the entities to poll on
    // first cycle.
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:28006 --fixture testdata/clusters/scale-lab.yaml",
      cwd: "..",
      port: 28006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-scale-vnprox.db && rm -rf var/dev-scale-host && exec go run ./cmd/vnproxd --config testdata/dev-scale.toml'",
      cwd: "..",
      url: "https://127.0.0.1:28007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 180_000,
    },
    // T-703's own stack (mgmt-redundancy.spec.ts), additive: a fourth mock
    // PVE + vnproxd pair on ports 38006/38007 serving the single-node
    // fixture — the management-path SPOF case the guided redundancy wizard
    // fixes (three-node-vlan, this suite's default fixture, is already
    // redundant and raises no mgmt_single_path finding). Fresh DB + host
    // sandbox per run so the applied bond doesn't leak into the next run.
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:38006 --fixture testdata/clusters/single-node.yaml",
      cwd: "..",
      port: 38006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-mgmt-vnprox.db && rm -rf var/dev-mgmt-host && exec go run ./cmd/vnproxd --config testdata/dev-mgmt.toml'",
      cwd: "..",
      url: "https://127.0.0.1:38007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    // T-1005's own stack (alert-rules.spec.ts), additive: a fifth mock PVE +
    // vnproxd pair on ports 48006/48007, reusing sim-lab.yaml's scripted
    // vm-a -> vm-c tcp/2222 live-probe divergence (the same fixture
    // simulator.spec.ts's T-806 test drives) on a dedicated daemon instance
    // — see testdata/dev-alert.toml's doc comment for why this can't share
    // simulator.spec.ts's own 18006/18007 stack (a stale/already-notified
    // finding never re-fires Engine's once-per-transition webhook
    // notification).
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:48006 --fixture testdata/clusters/sim-lab.yaml",
      cwd: "..",
      port: 48006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-alert-vnprox.db && rm -rf var/dev-alert-host && exec go run ./cmd/vnproxd --config testdata/dev-alert.toml'",
      cwd: "..",
      url: "https://127.0.0.1:48007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    // T-1003's own stack (flows.spec.ts), additive: a sixth mock PVE +
    // vnproxd pair on ports 58006/58007 serving testdata/clusters/
    // flow-lab.yaml (T-1002's flow ingestion fixture) with netflow_enabled
    // on a dedicated port (testdata/dev-flow.toml) — flows.spec.ts injects
    // a real UDP NetFlow v5 datagram at that port and drives the Flow
    // Explorer / map-painting UI end to end against the resulting ingested
    // record.
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:58006 --fixture testdata/clusters/flow-lab.yaml",
      cwd: "..",
      port: 58006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-flow-vnprox.db && rm -rf var/dev-flow-host && exec go run ./cmd/vnproxd --config testdata/dev-flow.toml'",
      cwd: "..",
      url: "https://127.0.0.1:58007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    // T-1907's own stack (physical-collapse.spec.ts), additive: a seventh
    // mock PVE + vnproxd pair on ports 61006/61007 serving testdata/clusters/
    // phys-collapse.yaml — a single node with 10 physical NICs (over
    // topology.DefaultPhysicalCollapseThreshold), purpose-built to exercise
    // the physical-layer collapse pill and its expand affordance end to end,
    // since none of the other fixtures' nodes get anywhere near that NIC
    // count (the documented scale target is only 6/node, deliberately under
    // the threshold — see that constant's doc comment).
    {
      command: "go run ./cmd/pvemock --addr 127.0.0.1:61006 --fixture testdata/clusters/phys-collapse.yaml",
      cwd: "..",
      port: 61006,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        "sh -c 'rm -f var/dev-physcollapse-vnprox.db && rm -rf var/dev-physcollapse-host && exec go run ./cmd/vnproxd --config testdata/dev-physcollapse.toml'",
      cwd: "..",
      url: "https://127.0.0.1:61007/api/v1/health",
      ignoreHTTPSErrors: true,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
});
