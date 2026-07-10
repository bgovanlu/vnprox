# Topology render verification (T-107 AC1 / audit F-06)

T-107's acceptance criterion 1 requires a render-verification artifact: a Playwright
screenshot-baseline test **or** a documented executed manual checklist. This repo ships the
Playwright option, executed 2026-07-10 in the environment described below.

## What exists

- `web/e2e/topology.spec.ts` — logs in with **real credentials** (`root` / `vnprox-mock`,
  realm `pam`, against pvemock's fixture users — no auth stub), opens `/topology`, and
  asserts against the real stack:
  - one visible node per layer band: `eno1` (phys), `vmbr0` (l2), `vlanz` (sdn),
    `app01/net0` (guest);
  - all three per-node columns populated (exactly 3 `vmbr0` bridges);
  - no §5 staleness banner while polling is healthy;
  - a committed screenshot baseline of the canvas:
    `web/e2e/topology.spec.ts-snapshots/topology-three-node-vlan-linux.png`.
- `web/e2e/perf.spec.ts` — the pan/zoom frame-timing measurement
  (see [topology-performance.md](topology-performance.md)).
- `web/playwright.config.ts` — boots the whole real stack itself via Playwright's
  `webServer`: `go run ./cmd/pvemock` (three-node-vlan fixture, port 8006) +
  `go run ./cmd/vnproxd --config testdata/dev.toml` (port 8007), with vnproxd serving the
  **production SPA build** it embeds from `web/dist` at compile time.

## How to run

```sh
cd web
npx playwright install chromium   # once; downloads a browser (~150 MB)
npm run e2e                       # builds the SPA, boots the stack, runs the specs
npm run e2e:update                # same, but re-baselines the screenshots
```

Ports 8006/8007 must be free (stop `make dev` first — `reuseExistingServer` is
deliberately off so the test never runs against a stale daemon).

## Why this is NOT part of `make check`

The suite needs a downloaded Chromium binary (network + disk, `npx playwright install`),
a Go toolchain to boot the stack, and exclusive use of ports 8006/8007. `make check` stays
hermetic (lint + typecheck + unit tests); this suite is the opt-in integration layer on
top. Vitest explicitly excludes `web/e2e/**` (see `vite.config.ts`).

## Environment caveats (read before trusting a diff)

- vnproxd's **host collector reads the real machine it runs on** — on a dev box (not a
  PVE node) the `pve1` band additionally contains that machine's own interfaces (e.g.
  `lo`, `ens18`), and the LLDP loop fails without `lldpctl`. The spec's assertions pin
  only fixture-declared PVE-derived entities, which are stable across machines; the
  **screenshot baseline is machine-dependent** (extra host NICs shift the auto-layout).
  On a different machine, re-baseline with `npm run e2e:update` and eyeball the diff.
- The committed baseline was captured headless (software rasterization) on the reference
  environment below; font rendering differs across OSes, hence the Linux-suffixed
  snapshot name and the 5% `maxDiffPixelRatio` tolerance.

## Executed record

- Date: 2026-07-10, headless Chromium (Playwright 1.61) on Linux 6.12 (x86_64 VM,
  no GPU), Node 20+, repo HEAD at the phase-1 remediation branch.
- `npx playwright test` → **2 passed** (topology render + perf measurement), baseline
  written and then re-verified against a second clean run of the full stack.
- Visual spot-check of the four bands (colors, band grouping, edge routing) on a real
  display has **not** been performed in this headless environment — the committed PNG is
  the artifact to eyeball; anyone on a dev machine can also just run `make dev` and look.
