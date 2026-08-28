// SPDX-License-Identifier: Apache-2.0

// T-208 AC4: "Monaco loads only when the editor opens (lazy-loaded chunk)
// with a bundle-size assertion in the build." This runs a real production
// `vite build` (into a scratch directory, never the tracked web/dist) and
// inspects the emitted chunks: the main entry bundle must not carry
// Monaco's code, and a separate chunk reachable only via the raw editor's
// dynamic import must exist and actually contain it.
//
// This is deliberately a real build, not a mock of Vite's rollup output —
// a mocked assertion would only prove the *source* says `React.lazy`, not
// that the bundler actually honored the split (e.g. a stray eager import
// of MonacoRawEditor anywhere in the app would silently pull Monaco into
// the main chunk despite the lazy() call still being present in the
// source). It is slow (a full production build) and intentionally so.
import { mkdtemp, readFile, readdir, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { build } from "vite";

const WEB_ROOT = join(__dirname, "..", "..");

describe("production bundle: Monaco code-split (T-208 AC4)", () => {
  it(
    "keeps Monaco out of the main entry chunk and puts it in its own lazy chunk",
    async () => {
      const outDir = await mkdtemp(join(tmpdir(), "vnprox-bundlesplit-"));
      try {
        await build({
          root: WEB_ROOT,
          configFile: join(WEB_ROOT, "vite.config.ts"),
          logLevel: "warn",
          build: {
            outDir,
            emptyOutDir: true,
            // A real build's minified output is what the assertion below
            // reads; sourcemaps would only slow this down for no benefit.
            sourcemap: false,
          },
        });

        const assetsDir = join(outDir, "assets");
        const files = await readdir(assetsDir);
        const jsFiles = files.filter((f) => f.endsWith(".js"));

        const mainEntry = jsFiles.find((f) => f.startsWith("index-"));
        const monacoChunk = jsFiles.find((f) => f.startsWith("MonacoRawEditor-"));

        if (mainEntry === undefined) {
          throw new Error(`expected a main "index-*.js" entry chunk among: ${jsFiles.join(", ")}`);
        }
        if (monacoChunk === undefined) {
          throw new Error(
            `expected a separate "MonacoRawEditor-*.js" chunk (the raw editor's lazy import target) among: ${jsFiles.join(", ")}`,
          );
        }

        const mainPath = join(assetsDir, mainEntry);
        const monacoPath = join(assetsDir, monacoChunk);
        const [mainSize, monacoSize] = await Promise.all([
          stat(mainPath).then((s) => s.size),
          stat(monacoPath).then((s) => s.size),
        ]);

        // The Monaco chunk should be substantial (it embeds Monaco's editor
        // core) — a near-empty chunk here would mean the dynamic import
        // resolved to a stub, not the real editor.
        expect(monacoSize).toBeGreaterThan(500_000);
        // A coarse regression guard on the main chunk's own size: this app
        // (React, React Flow, TanStack Query, Radix, Recharts, ...) is a
        // few MB unminified on its own, so this is intentionally a loose
        // ceiling — the load-bearing assertion is the content check below,
        // not a byte-count comparison against the Monaco chunk (the rest
        // of the app growing over time shouldn't make an unrelated bundle
        // test flaky).
        // T-2202 raised this from 3_500_000: the online-help content
        // (src/help/content/*.ts, ~110KB of prose across ~60 topics) is
        // bundled deliberately rather than fetched, so it costs main-chunk
        // bytes. Measured over the wire it's ~33KB gzipped, which did not
        // justify code-splitting the registry and losing synchronous topic
        // lookup in <HelpAnchor>'s accessible name. Raised once, with the
        // reason recorded — this ceiling exists to catch an accidental
        // dependency landing in the wrong chunk, not to ratchet quietly
        // every time content grows.
        //
        // Phase 30 wave 1 raised it again, from 3_800_000, and the reason is
        // recorded to the same standard because the previous note asked for
        // it. Three cards (T-3001 config-as-code, T-3004 analysis surfaces,
        // T-3005 canary apply) added ~236KB of source and ~20KB of help prose
        // between them, giving screens to backend features that previously had
        // none — which is the entire point of the phase. Measured: 3_826_289,
        // i.e. 0.7% over the old ceiling.
        //
        // What was checked before raising it, because "the number went up" is
        // not by itself a reason to move the number:
        //   - `git diff web/package.json web/package-lock.json` is EMPTY. No
        //     dependency was added, so this is content, not a stray import.
        //   - The MonacoEnvironment content assertion below — the load-bearing
        //     one — still passes, so the split itself is intact.
        // Raised to 4_000_000 rather than to just above the measurement, so
        // the next card does not have to re-litigate 20KB; the guard still
        // catches a real dependency (React Flow, Recharts and Monaco are each
        // an order of magnitude more than the headroom this leaves).
        //
        // T-3101 raised it again, from 4_000_000 to 4_100_000, for the same
        // reason and checked the same way: `git diff web/package.json
        // web/package-lock.json` is EMPTY (SDN Fabrics needed no new
        // dependency — the create/edit form reuses this codebase's existing
        // EditorDialog/Field/Table primitives), and the MonacoEnvironment
        // split assertion below still passes. Measured: 4_014_027, i.e. 0.35%
        // over the old ceiling — `web/src/sdn/FabricsView.tsx` (a new
        // panel-shaped view, eagerly imported by SdnPage.tsx exactly like its
        // EvpnView/DhcpView siblings already are — none of the SDN cockpit's
        // sub-views are code-split from each other), the accompanying
        // `SdnFabric*`/`Fabric`/`PrefixList`/`RouteMap` types and op builders,
        // and one new help topic account for it.
        //
        // T-3106 raised it again, from 4_100_000 to 4_200_000. Unlike the two
        // prior raises, `git diff web/package.json web/package-lock.json` is
        // NOT empty this time: T-3106 adds the i18n scaffolding's framework
        // dependency, `i18next` + `react-i18next` (no runtime CDN fetch —
        // translation JSON is bundled via Vite's static import, the same as
        // every other content this file's history already accounts for; see
        // web/src/i18n/i18n.ts). Both packages are eagerly imported from
        // main.tsx (the app wraps its whole tree in <I18nextProvider>), so
        // this is exactly the "real dependency landing in the main chunk"
        // case this ceiling exists to catch — checked and confirmed
        // intentional rather than stray. Measured: 4_111_142, i.e. 0.27%
        // over the old ceiling.
        //
        // Phase 39's first batch raised it again, from 4_200_000 to
        // 4_300_000. Unlike T-3106's raise, `git diff web/package.json` IS
        // empty this time — no new runtime dependency, so this is the benign
        // half of what this ceiling exists to distinguish. The growth is four
        // cards' worth of first-party page code, all eagerly imported because
        // `web/src/App.tsx` code-splits no page (there is not one `lazy(` in
        // it; Monaco is the sole split, asserted below): T-3903's route
        // explorer page and next-hop graph, T-3911's composable dashboard
        // grid and tile registry, T-3902's multicast/MDB browser, T-3901's
        // STP overlay, plus their help topics and API types. Measured:
        // 4_200_548 — 0.013% over the old ceiling, i.e. 548 bytes, which is
        // what a hair's-breadth overshoot rather than a regression looks
        // like. If a future raise finds package.json non-empty again, check
        // the new dependency is intended before moving this number.
        //
        // T-4006 raised it again, from 4_300_000 to 4_320_000. `git diff
        // web/package.json web/package-lock.json` is empty — no new runtime
        // dependency, the benign case again. The growth is the freeze-window
        // calendar surface: `web/src/governance/CalendarPanel.tsx` (a new
        // GovernancePage tab, eagerly imported exactly like PoliciesPanel/
        // CompliancePanel beside it) and `calendar.ts`, plus
        // `web/src/changesets/FreezeOverridePanel.tsx` and
        // `freezeOverride.ts` (the audited override, wired into
        // ReviewApplyScreen.tsx beside BreakGlassPanel), their API types in
        // web/src/api/policies.ts/types.ts, and one new help topic. Measured:
        // 4_307_562, i.e. 0.18% over the old ceiling.
        expect(mainSize).toBeLessThan(4_320_000);

        // The load-bearing check: Monaco's own distinctive runtime marker
        // must appear in the Monaco chunk and must NOT appear in the main
        // entry chunk — proof the split actually happened, not just that
        // a separate file with a plausible name exists.
        const mainSource = await readFile(mainPath, "utf8");
        expect(mainSource).not.toContain("MonacoEnvironment");

        const monacoSource = await readFile(monacoPath, "utf8");
        expect(monacoSource).toContain("MonacoEnvironment");
      } finally {
        await rm(outDir, { recursive: true, force: true });
      }
    },
    120_000,
  );
});
