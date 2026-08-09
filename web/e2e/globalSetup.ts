// T-2409: compile vnproxd once, before the suite runs.
//
// Each spec file now starts its own daemon (see isolated.ts). `go run` would
// pay the link cost on every one of them — a few seconds each, 30-odd times.
// Building once and exec'ing the binary turns that into one build and a
// process spawn per file.
//
// The binary lands in web/test-results/, which is already gitignored as
// Playwright's own output directory and already wiped between runs.

import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

export default function globalSetup(): void {
  // import.meta.url, not __dirname: Playwright loads config and setup as ES
  // modules, where __dirname does not exist.
  const here = dirname(fileURLToPath(import.meta.url));
  const repoRoot = resolve(here, "..", "..");
  const outDir = join(repoRoot, "web", "test-results");
  mkdirSync(outDir, { recursive: true });

  const out = join(outDir, "e2e-vnproxd");
  // Inherit stdio: a compile error must be readable, not swallowed into a
  // "stack failed to start" message thirty seconds later.
  execFileSync("go", ["build", "-o", out, "./cmd/vnproxd"], { cwd: repoRoot, stdio: "inherit" });
}
