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
    // Playwright specs (e2e/) are a separate, opt-in runner (`npm run
    // e2e`) — vitest must not pick them up via its default include glob.
    exclude: [...configDefaults.exclude, "e2e/**"],
  },
});
