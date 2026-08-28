// SPDX-License-Identifier: Apache-2.0

// T-3204: per-spec-file daemon isolation for the specific spec files T-2505
// AC3 measured as unsafe under `--repeat-each` — the criterion T-2409 was
// opened for and never ran, then T-2505 subsumed and ran once:
// `E2E_ARGS="--repeat-each=2" scripts/e2e-shards.sh` on all four shards came
// back 168 passed / 10 failed / 2 quarantined-failing / 6 skipped
// (`planning/tasks/phase-25.md`, "AC3: `--repeat-each=2`"). Every failure was
// the same shape: a spec mutates app-owned state (creates a changeset, mutes
// a finding, pins a layout, commits a schedule...) and then asserts a
// starting condition its own first repeat already destroyed, because
// `--repeat-each` runs a spec file twice against the ONE daemon its shard
// shares with every other file.
//
// WHY NOT REVIVE `t-2409-e2e-store-isolation` WHOLESALE. That branch gives
// EVERY spec file (31 of them, pre-T-2505) its own vnproxd — proven to work,
// and proven to cost +79% wall clock. It also allocates each file's port by
// hashing the file's own path into a range (`web/e2e/isolated.ts`'s
// `portForCaller`), which predates — and does not satisfy — the port
// registry `internal/devports` gates on today: a computed, non-literal port
// is invisible to that scan. Reviving the branch as-is would need the
// registry problem solved anyway, and would still pay full-suite isolation
// cost for the ~27 files that never showed a `--repeat-each` failure.
//
// THIS FILE. Only the files that actually failed AC3 (see the constants each
// spec file passes below) get their own daemon, on a NEWLY REGISTERED,
// literal port (`testdata/dev-ports.tsv`), while continuing to share their
// stack's existing, already-registered pvemock fixture — safe because
// nothing writes to it, the same argument T-2409's own branch made for
// sharing pvemock rather than isolating it too. Every other spec file is
// completely unaffected: it still runs against its shard's normal,
// `web/e2e/shards.ts`-managed daemon, exactly as before this file existed.
//
// The daemon lifecycle logic below (config redirection, health-wait,
// process spawn/teardown) is adapted from `t-2409-e2e-store-isolation`'s
// `web/e2e/isolated.ts`, which already solved it once; the port allocation
// is the one part that had to change.

import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { request as httpsRequest } from "node:https";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawn, type ChildProcess } from "node:child_process";
import { test } from "@playwright/test";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** Built once by scripts/e2e-shards.sh, same convention as web/e2e/shards.ts's
 * own `command()` helper — falls back to `go run` so a developer running a
 * single isolated spec file without the script still works, just slower. */
const BIN_DIR = join(REPO_ROOT, "web", "test-results", "e2e-bin");

/** Every var/-relative key in the dev-*.toml configs that must be redirected
 * into this run's own throwaway directory — otherwise an "isolated" daemon
 * would still share its store, session key or interfaces sandbox with
 * whatever else is using that stack's config. Mirrors
 * t-2409-e2e-store-isolation's `web/e2e/isolated.ts` REDIRECTED_KEYS. */
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

/** Keys whose absence means this config's shape has drifted from what this
 * file assumes — the same "hard error, not a silent fallthrough" convention
 * web/e2e/shards.ts's own REQUIRED_KEYS uses, and for the same reason: a
 * config missing one of these would leave the "isolated" daemon sharing
 * state with every other consumer of that base config. */
const REQUIRED_KEYS = ["listen", "api_url", "db_path", "dev_interfaces_dir"] as const;

/** Rewrites a dev-*.toml template for one isolated run: a fixed `listen`
 * port, `api_url` pointed at the (shared, read-only) mock this stack's
 * shard is already running, every var/-relative path redirected under
 * `dir`, and — when the caller names one (the flow stack's NetFlow
 * listener) — a numeric port key rewritten to `netflowPort`. Without that
 * last rewrite, every isolated instance of a config that opens a listener
 * on a fixed port (rather than deriving it from `listen`) would still
 * collide with every other instance of the same config — including
 * whatever this stack's shard.ts-managed shared daemon left running,
 * since removing its webServer entry entirely is out of this card's
 * surgical scope (see this file's own top-of-file doc comment). */
function writeIsolatedConfig(configPath: string, dir: string, port: number, mockURL: string, netflowPort?: number): string {
  const source = resolve(REPO_ROOT, configPath);
  const replaced = new Set<string>();

  const out = readFileSync(source, "utf8")
    .split("\n")
    .map((line) => {
      const trimmed = line.trim();
      if (trimmed.startsWith("listen ") || trimmed.startsWith("listen=")) {
        replaced.add("listen");
        return `listen = "127.0.0.1:${String(port)}"`;
      }
      if (trimmed.startsWith("api_url ") || trimmed.startsWith("api_url=")) {
        replaced.add("api_url");
        return `api_url = "${mockURL}"`;
      }
      if (netflowPort !== undefined && (trimmed.startsWith("netflow_port ") || trimmed.startsWith("netflow_port="))) {
        replaced.add("netflow_port");
        return `netflow_port = ${String(netflowPort)}`;
      }
      const m = /^(\s*)([a-z_]+)\s*=\s*"var\/(.+)"\s*$/.exec(line);
      if (m !== null) {
        const [, indent, key, rest] = m;
        if (indent !== undefined && key !== undefined && rest !== undefined && (REDIRECTED_KEYS as readonly string[]).includes(key)) {
          replaced.add(key);
          return `${indent}${key} = "${join(dir, rest)}"`;
        }
      }
      return line;
    })
    .join("\n");

  if (netflowPort !== undefined && !replaced.has("netflow_port")) {
    throw new Error(`${configPath} has no netflow_port key to redirect, but isolateFile was called with a netflowPort override.`);
  }

  const missing = REQUIRED_KEYS.filter((k) => !replaced.has(k));
  if (missing.length > 0) {
    throw new Error(
      `${configPath} has no ${missing.join(", ")} key(s) to redirect; an isolated run would share state with ` +
        `every other consumer of that config. Update web/e2e/isolate.ts to match its current shape.`,
    );
  }

  const path = join(dir, "isolated.toml");
  writeFileSync(path, out);
  return path;
}

interface HealthBody {
  status?: string;
  collectors?: { name?: string; last_success?: string }[];
}

/** node:https with rejectUnauthorized off, not global fetch: the dev
 * certificate is self-signed, and fetch would need
 * NODE_TLS_REJECT_UNAUTHORIZED set process-wide, which would also silence
 * real certificate errors in every other spec. Same as
 * t-2409-e2e-store-isolation's `web/e2e/isolated.ts`. */
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

/** Whether the PVE collector AND at least one host collector have each
 * completed a successful poll — without this, a cold-started isolated
 * daemon's FIRST test pays for the first poll cycle itself, in locator
 * timeouts rather than in one cheap wait loop here. Started as a pve-only
 * check ported from t-2409-e2e-store-isolation's `web/e2e/isolated.ts`
 * (which measured the difference at 16.7 min and 3 failures without it);
 * T-3204 added the host half after finding that a pve-only check let
 * history.spec.ts's NetFlow record arrive and get ingested before ANY host
 * collector cycle had populated the interface data flow resolution needs to
 * turn a raw IP into a `srcRef`/`dstRef` — reproducing only when this
 * file's isolated daemon happened to reach that point fast enough (chained
 * after another isolated file in the same run, where Node/Chromium startup
 * overhead is already paid, rather than standalone), which is why it looked
 * order-dependent rather than a plain cold-start race.
 *
 * Deliberately "at least one", not "every" host collector: every dev
 * fixture's OTHER nodes are cross-node peers this sandbox can never reach
 * (no /etc/pve/pve-root-ca.pem — see internal/peer's own trust-anchor
 * error), so their host collectors never succeed at all, by design. Only
 * the local node's own host collector's first cycle is what flow
 * resolution actually needs. */
function collectorsReady(body: HealthBody | undefined): boolean {
  if (body?.status !== "ok") return false;
  const collectors = body.collectors ?? [];
  if (collectors.length === 0) return true;
  // A collector kind absent entirely (collection disabled for it) does not
  // block readiness — only a kind that IS present and hasn't reported a
  // success yet does.
  const readyFor = (name: string): boolean => {
    const of = collectors.filter((c) => c.name === name);
    if (of.length === 0) return true;
    return of.some((c) => c.last_success !== undefined && c.last_success !== "");
  };
  return readyFor("pve") && readyFor("host");
}

async function waitForHealth(url: string, child: ChildProcess, log: () => string): Promise<void> {
  const deadline = Date.now() + 60_000;
  for (;;) {
    if (child.exitCode !== null) {
      throw new Error(`vnproxd exited with code ${String(child.exitCode)} before serving health:\n${log()}`);
    }
    if (collectorsReady(await health(`${url}/api/v1/health`))) {
      // cmd/vnproxd's flow resolver re-indexes on its own timer, decoupled
      // from collector success (runFlowResolverRefreshLoop's cold-start
      // ticker, cmd/vnproxd/flows.go): the very first index build happens
      // BEFORE collect has polled anything (so it is empty), and the next
      // one can be up to flowResolverColdStartInterval (1s) after this
      // health check first goes green. Resolution happens once at ingest
      // and is never retried, so a NetFlow record sent inside that ~1s gap
      // is permanently unattributed — not a failure, just data with no
      // srcRef/dstRef. A short grace period here, paid once per isolated
      // daemon, is cheaper than a spec file re-deriving this same race.
      await new Promise((r) => setTimeout(r, 2_000));
      return;
    }
    if (Date.now() > deadline) {
      throw new Error(`vnproxd did not report a healthy, PVE-polled state within 60s:\n${log()}`);
    }
    await new Promise((r) => setTimeout(r, 100));
  }
}

export interface IsolateOptions {
  /** Repo-relative base dev-*.toml this file's stack normally uses (e.g.
   * "testdata/dev.toml" for the default stack, "testdata/dev-flow.toml"
   * for the flow stack). */
  config: string;
  /** This spec file's own literal, registered port (testdata/dev-ports.tsv).
   * One per isolated file — never shared, so two isolated files can never
   * race for the same listener even if Playwright ever ran them
   * concurrently. */
  port: number;
  /** The shared, read-only mock this file's stack is already running,
   * resolved from the active shard (web/e2e/shards.ts's `mockURL`) so this
   * keeps working if a spec file's shard assignment ever changes. */
  mockURL: string;
  /** Override for `netflow_port`, needed only by the flow stack's two
   * isolated files (history.spec.ts, flows.spec.ts), each of which sends a
   * real UDP NetFlow datagram at its own daemon and would otherwise
   * collide with the shard's still-running shared flow daemon (which also
   * opens dev-flow.toml's fixed 52055) and with each other. */
  netflowPort?: number;
}

/** Call at the top level of a spec file (before any `test(...)`) to give
 * that file its own vnproxd for the duration of the run: started in
 * `beforeAll`, torn down in `afterAll`. Confirmed empirically (T-3204) that
 * Playwright re-runs `beforeAll`/`afterAll` for each repetition under
 * `--repeat-each`, so this is also what makes `--repeat-each=2` clean for
 * the file that calls it — no separate per-repeat handling needed. */
export function isolateFile(opts: IsolateOptions): void {
  const baseURL = `https://127.0.0.1:${String(opts.port)}`;
  // Known at import time (a literal port, not a kernel-assigned or hashed
  // one) so this can run before beforeAll — Playwright resolves `baseURL`
  // ahead of hooks, the same ordering constraint
  // t-2409-e2e-store-isolation's `web/e2e/isolated.ts` documents.
  test.use({ baseURL });

  let dir: string | undefined;
  let child: ChildProcess | undefined;

  test.beforeAll(async () => {
    dir = mkdtempSync(join(tmpdir(), "vnprox-e2e-isolate-"));
    const cfg = writeIsolatedConfig(opts.config, dir, opts.port, opts.mockURL, opts.netflowPort);

    const prebuilt = join(BIN_DIR, "vnproxd");
    const [command, args] = existsSync(prebuilt) ? [prebuilt, ["--config", cfg]] : ["go", ["run", "./cmd/vnproxd", "--config", cfg]];

    child = spawn(command, args, { cwd: REPO_ROOT, env: process.env, stdio: ["ignore", "pipe", "pipe"] });
    let output = "";
    const capture = (chunk: Buffer): void => {
      output += chunk.toString();
      if (output.length > 20_000) output = output.slice(-20_000); // tail only
    };
    // stdio was explicitly set to ["ignore", "pipe", "pipe"] above, so
    // stdout/stderr are real streams — but node:child_process's type keeps
    // them nullable regardless, since the type doesn't track the option.
    child.stdout?.on("data", capture);
    child.stderr?.on("data", capture);

    try {
      await waitForHealth(baseURL, child, () => output);
    } catch (err) {
      child.kill("SIGKILL");
      rmSync(dir, { recursive: true, force: true });
      throw err;
    }
  });

  test.afterAll(async () => {
    if (child?.exitCode === null) {
      child.kill("SIGTERM");
      await new Promise<void>((done) => {
        const timer = setTimeout(() => {
          child?.kill("SIGKILL");
          done();
        }, 10_000);
        child?.on("exit", () => {
          clearTimeout(timer);
          done();
        });
      });
    }
    if (dir !== undefined) rmSync(dir, { recursive: true, force: true });
  });
}
