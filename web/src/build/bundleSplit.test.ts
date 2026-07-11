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
        expect(mainSize).toBeLessThan(3_500_000);

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
