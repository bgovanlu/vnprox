// T-2409: per-spec-file store isolation for the e2e suite.
//
// THE PROBLEM. 31 spec files shared one vnproxd and one mutable SQLite
// store. A spec that created a changeset, saved a layout, pinned a view or
// registered an alert rule left it there for every spec that ran afterwards,
// which means a spec's pass depended on which specs ran before it. That is
// what turned latent ambiguous locators into failures during the T-2108
// triage, and it is why `saved-views.spec.ts › annotations` failed once in
// three full runs and passed in isolation.
//
// THE FIX. Each spec file that opts in gets its own vnproxd process, with its
// own database, session key, interfaces sandbox and port, started in
// beforeAll and stopped in afterAll. Nothing one file writes is visible to
// another, because there is no shared writable surface left.
//
// WHAT IS STILL SHARED, and why that is safe: the pvemock fixture server on
// 8006. It is the *source of truth* the daemons poll, it is read-only for the
// duration of a run, and giving each file its own would multiply process count
// for no isolation gain — a spec cannot contaminate another through a server
// neither of them writes to.
//
// COST. One daemon start/stop per spec file, against a binary the global
// setup compiles once (see globalSetup.ts — `go run` per file would pay the
// link cost 31 times). Measured at the time of writing: see
// docs/development.md's e2e section for the before/after wall clock.

import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { request as httpsRequest } from "node:https";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";
import { spawn, type ChildProcess } from "node:child_process";
import { test as base, expect } from "@playwright/test";

/** Repo root, relative to web/e2e/.
 *
 * import.meta.url rather than __dirname: Playwright loads specs as ES
 * modules, where __dirname is not defined. */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** The binary globalSetup builds. Falls back to `go run` if it is absent, so
 * a developer running a single spec without the global setup still works —
 * slower, but not broken. */
const PREBUILT = join(REPO_ROOT, "web", "test-results", "e2e-vnproxd");

export interface Stack {
  url: string;
  port: number;
  stop: () => Promise<void>;
}

/** Asks the kernel for a free port and immediately releases it.
 *
 * There is an unavoidable race between releasing and vnproxd binding. It is
 * the same race cmd/vnproxd's own daemon tests accept, and for the same
 * reason: the alternative is a fixed port table, which is what made
 * `T-1807-bug-02` spend two days on port collisions. */
async function freePort(): Promise<number> {
  return new Promise((resolvePort, reject) => {
    const srv = createServer();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (addr === null || typeof addr === "string") {
        srv.close();
        reject(new Error("listener did not report a TCP address"));
        return;
      }
      const { port } = addr;
      srv.close(() => {
        resolvePort(port);
      });
    });
  });
}

/** Every path in testdata/dev.toml that must be redirected into this stack's
 * own temp directory. A key missing from dev.toml is a hard error rather than
 * a silent fallthrough: a stack that quietly kept writing to var/ would share
 * state with every other stack, which is the exact bug this file exists to
 * remove. */
const REDIRECTED_KEYS = [
  "db_path",
  "session_key_file",
  "protected_path",
  "dev_interfaces_dir",
  "secret_path",
  "key_file",
  "signing_key_file",
  "trusted_signers_dir",
] as const;

/** The keys whose absence makes a stack NOT isolated. The rest are redirected
 * when present and shrugged at when not — the dev-*.toml variants legitimately
 * omit blueprint or metrics settings, but a config with no db_path or no
 * interfaces sandbox would silently share both with every other stack. */
const REQUIRED_KEYS = ["db_path", "dev_interfaces_dir", "listen", "tls_cert", "tls_key"] as const;

function writeIsolatedConfig(dir: string, port: number, configPath: string): string {
  const source = resolve(REPO_ROOT, configPath);
  const lines = readFileSync(source, "utf8").split("\n");

  const replaced = new Set<string>();
  const out = lines.map((line) => {
    const trimmed = line.trim();
    if (trimmed.startsWith("listen ") || trimmed.startsWith("listen=")) {
      replaced.add("listen");
      return `listen = "127.0.0.1:${String(port)}"`;
    }
    for (const key of REDIRECTED_KEYS) {
      if (trimmed.startsWith(`${key} `) || trimmed.startsWith(`${key}=`)) {
        replaced.add(key);
        return `${key} = ${JSON.stringify(join(dir, key))}`;
      }
    }
    // TLS material is read-only and shared; make the relative paths absolute
    // so the daemon can find them from any working directory.
    if (trimmed.startsWith("tls_cert ")) {
      replaced.add("tls_cert");
      return `tls_cert = ${JSON.stringify(join(REPO_ROOT, "testdata", "certs", "dev-cert.pem"))}`;
    }
    if (trimmed.startsWith("tls_key ")) {
      replaced.add("tls_key");
      return `tls_key = ${JSON.stringify(join(REPO_ROOT, "testdata", "certs", "dev-key.pem"))}`;
    }
    return line;
  });

  const missing = REQUIRED_KEYS.filter((k) => !replaced.has(k));
  if (missing.length > 0) {
    throw new Error(
      `${configPath} has no ${missing.join(", ")} key to redirect; an isolated stack would ` +
        `share state with every other one. Update e2e/isolated.ts to match that config's current shape.`,
    );
  }

  const path = join(dir, "dev.toml");
  writeFileSync(path, out.join("\n"));
  return path;
}

/** One health probe, returning the parsed body when the daemon answers 200.
 *
 * node:https with rejectUnauthorized off rather than global fetch: the dev
 * certificate is self-signed, and fetch would need
 * NODE_TLS_REJECT_UNAUTHORIZED set on the *Playwright* process — a global
 * switch that would also silence real certificate errors in every spec.
 */
interface HealthBody {
  status?: string;
  collectors?: { name?: string; last_success?: string }[];
}

async function health(url: string): Promise<HealthBody | undefined> {
  return new Promise((done) => {
    const req = httpsRequest(url, { rejectUnauthorized: false, timeout: 2000 }, (res) => {
      if (res.statusCode !== 200) {
        res.resume();
        done(undefined);
        return;
      }
      let body = "";
      res.setEncoding("utf8");
      res.on("data", (chunk: string) => {
        body += chunk;
      });
      res.on("end", () => {
        try {
          done(JSON.parse(body) as HealthBody);
        } catch {
          done(undefined);
        }
      });
    });
    req.on("error", () => {
      done(undefined);
    });
    req.on("timeout", () => {
      req.destroy();
      done(undefined);
    });
    req.end();
  });
}

/** Whether the PVE collector has completed at least one successful poll.
 *
 * THIS IS THE DIFFERENCE BETWEEN A 25% AND AN 83% WALL-CLOCK REGRESSION, and
 * between three failures and none. Under the old shared-daemon arrangement one
 * daemon had been polling since the suite started, so by the time any spec ran
 * the topology was long since populated. A per-file daemon starts cold, and
 * without this wait each file's first test pays for the first poll itself — in
 * `expect` retries, in 30s locator timeouts, and for the two heaviest fixtures
 * (scale-lab, and user-guide-tasks' IPAM/firewall flows) in outright failure.
 *
 * Measured: 16.7 min and 3 failures without it. Waiting here moves that cost
 * into a single cheap poll loop per file instead of spreading it across every
 * assertion in the file.
 */
function pveCollectorReady(body: HealthBody | undefined): boolean {
  if (body?.status !== "ok") return false;
  const collectors = body.collectors ?? [];
  if (collectors.length === 0) {
    // A daemon with no collector reporting at all (a config with collection
    // disabled) must not block forever waiting for one.
    return true;
  }
  const pve = collectors.find((c) => c.name === "pve");
  if (pve === undefined) return true;
  return pve.last_success !== undefined && pve.last_success !== "";
}

async function waitForHealth(url: string, child: ChildProcess, log: () => string): Promise<void> {
  const deadline = Date.now() + 60_000;
  for (;;) {
    if (child.exitCode !== null) {
      throw new Error(`vnproxd exited with code ${String(child.exitCode)} before serving health:\n${log()}`);
    }
    if (pveCollectorReady(await health(`${url}/api/v1/health`))) return;
    if (Date.now() > deadline) {
      throw new Error(`vnproxd did not report a healthy, PVE-polled state within 60s:\n${log()}`);
    }
    await new Promise((r) => setTimeout(r, 100));
  }
}

export interface StackOptions {
  /** The port to bind. Supplied by isolatedStore, which must know it before
   * the daemon starts — see that function. Omitted only by a direct
   * startStack caller, which then gets a kernel-assigned free port. */
  port?: number;
  /** Repo-relative daemon config. Defaults to the three-node-vlan dev config
   * every core spec uses; specs needing a different pvemock fixture name the
   * matching testdata/dev-*.toml, whose pvemock still runs as a shared
   * read-only webServer. */
  config?: string;
}

export async function startStack(opts: StackOptions = {}): Promise<Stack> {
  const configPath = opts.config ?? "testdata/dev.toml";
  const port = opts.port ?? (await freePort());
  const dir = mkdtempSync(join(tmpdir(), "vnprox-e2e-"));
  const cfg = writeIsolatedConfig(dir, port, configPath);

  const [command, args] = existsSync(PREBUILT)
    ? [PREBUILT, ["--config", cfg]]
    : ["go", ["run", "./cmd/vnproxd", "--config", cfg]];

  const child = spawn(command, args, {
    cwd: REPO_ROOT,
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });

  let output = "";
  const capture = (chunk: Buffer): void => {
    output += chunk.toString();
    // Keep the tail only; a daemon that logs for two minutes must not turn a
    // failure message into a megabyte.
    if (output.length > 20_000) output = output.slice(-20_000);
  };
  child.stdout.on("data", capture);
  child.stderr.on("data", capture);

  const url = `https://127.0.0.1:${String(port)}`;
  try {
    await waitForHealth(url, child, () => output);
  } catch (err) {
    child.kill("SIGKILL");
    rmSync(dir, { recursive: true, force: true });
    throw err;
  }

  return {
    url,
    port,
    stop: async () => {
      if (child.exitCode === null) {
        child.kill("SIGTERM");
        await new Promise<void>((done) => {
          const timer = setTimeout(() => {
            child.kill("SIGKILL");
            done();
          }, 10_000);
          child.on("exit", () => {
            clearTimeout(timer);
            done();
          });
        });
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

/** A stack shared by every file, used ONLY to demonstrate what isolation is
 * worth. Set VNPROX_E2E_SHARED=1 and the cross-contamination pair in
 * isolation-*.spec.ts fails, exactly as it did before this file existed.
 *
 * It is a deliberate escape hatch for one purpose and is never set by `npm
 * run e2e`. See T-2409 AC2: a test that only passes proves nothing about the
 * arrangement it was written to defend. */
let shared: Stack | undefined;
const SHARED = process.env.VNPROX_E2E_SHARED === "1";

/** The stack the current spec file owns.
 *
 * A module-level variable is safe here because Playwright loads this module
 * once per worker process, and a worker runs one file at a time. */
let current: Stack | undefined;

/** The base URL of the current file's isolated stack. Specs that talk to the
 * API directly (rather than through the page) use this instead of a
 * hard-coded https://127.0.0.1:8007. */
export function stackURL(): string {
  if (current === undefined) {
    throw new Error("no isolated stack is running; did this spec file call isolatedStore()?");
  }
  return current.url;
}

/** `test` with baseURL bound to this file's own stack. Import this instead of
 * @playwright/test's `test` in any spec that calls isolatedStore. */
/** The suite's `test`.
 *
 * NOT extended with a baseURL fixture, though that was the obvious design and
 * the first one tried. Playwright resolves option fixtures — baseURL among
 * them — *before* a file's beforeAll hook runs, so a fixture that reads the
 * stack started in beforeAll throws every time. The port therefore has to be
 * known at import time instead; see isolatedStore.
 */
export const test = base;

export { expect };

/** The first port of the range isolatedStore hands out. Clear of every port
 * this suite already binds: the pvemock fixtures (8006, 18006-61006), the k8s
 * mock (8008), and dev-flow.toml's netflow collector (52055). */
const PORT_BASE = 41000;

/** Deterministic port per spec file.
 *
 * Derived from the calling file's path rather than an incrementing counter, so
 * two workers importing different subsets of the suite still disagree about
 * nothing. A hash collision would show up as a daemon that cannot bind, with
 * the bind error in the beforeAll failure — loud, not silent.
 */
function portForCaller(): number {
  const stack = new Error("locate caller").stack ?? "";
  const line = stack.split("\n").find((l) => l.includes(".spec.ts"));
  const key = line ?? String(nextFallbackIndex++);
  let h = 0;
  for (let i = 0; i < key.length; i++) {
    h = (h * 31 + key.charCodeAt(i)) % 4000;
  }
  return PORT_BASE + h;
}

let nextFallbackIndex = 0;

/** Call once at the top level of a spec file to give it its own store.
 *
 * Named isolatedStore, not useIsolatedStore: the `use` prefix makes eslint's
 * react-hooks/rules-of-hooks treat every call as a misplaced React hook.
 *
 * Deliberately a function call rather than an import side effect: a reader
 * scanning the file sees, on one line, that this spec is isolated — and a
 * spec that has NOT been converted is visibly missing it. */
export function isolatedStore(opts: StackOptions = {}): void {
  const port = opts.port ?? portForCaller();
  // Declared here, at import time, because Playwright needs baseURL before
  // beforeAll runs (see `test` above). The daemon then binds exactly this
  // port rather than one the kernel picked.
  test.use({ baseURL: `https://127.0.0.1:${String(port)}` });

  test.beforeAll(async () => {
    if (SHARED) {
      shared ??= await startStack({ ...opts, port });
      current = shared;
      return;
    }
    current = await startStack({ ...opts, port });
  });
  test.afterAll(async () => {
    if (SHARED) {
      // Left running on purpose: the next file must inherit its state, which
      // is the whole point of this mode.
      current = undefined;
      return;
    }
    await current?.stop();
    current = undefined;
  });
}
