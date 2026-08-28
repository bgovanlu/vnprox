// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from "vite";
import { configDefaults } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Dev server proxies /api (REST + the /api/ws WebSocket upgrade) to
// vnproxd, per docs/development.md's `make dev` target (backend against
// pvemock + Vite dev server, hot reload) and testdata/dev.toml (vnproxd
// listens on https://127.0.0.1:8007 with a throwaway self-signed dev
// cert — `secure: false` lets the proxy trust it in dev only).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": {
        target: "https://127.0.0.1:8007",
        changeOrigin: true,
        secure: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    globals: true,
    css: true,
    setupFiles: ["./src/test/setup.ts"],
    restoreMocks: true,
    // Must stay comfortably ABOVE src/test/setup.ts's `asyncUtilTimeout`
    // (5000ms). Vitest's default testTimeout is also 5000ms, so a
    // `findBy*` allowed to run its full course would hit the per-test
    // ceiling at the same moment and report "test timed out" instead of
    // the assertion that actually failed — which is a much worse error
    // message, and is how raising asyncUtilTimeout first broke this suite.
    testTimeout: 20_000,
    // Playwright specs (e2e/) are a separate, opt-in runner (`npm run
    // e2e`) — vitest must not pick them up via its default include glob.
    exclude: [...configDefaults.exclude, "e2e/**"],
    // T-3708: cmd/vitestgate reads the JSON report to apply the quarantine,
    // the hard-expiry rule and the run-history trend — the same relationship
    // cmd/e2egate has to the Playwright suite's JSON reports (T-2505). The
    // human-readable "default" reporter stays alongside it; vitestgate's own
    // verdict line is what `make test` actually gates on, but a developer
    // reading the terminal live still wants per-test output as it happens.
    // outputFile is relative to this file's directory (web/), matching
    // cmd/vitestgate's --report default of web/test-results/vitest.json;
    // test-results/ is already gitignored (see web/e2e's use of the same
    // directory for shard reports).
    reporters: ["default", ["json", { outputFile: "./test-results/vitest.json" }]],
  },
});
