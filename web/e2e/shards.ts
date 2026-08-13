// T-2505: the e2e suite's shard manifest — which specs run where, which
// stacks each shard brings up, and on which ports.
//
// WHY SHARDS AND NOT WORKERS. `workers: 1, fullyParallel: false` made the
// suite's wall clock the sum of every spec (9.9 min measured on the dev host,
// 2026-08-12). A second Playwright worker inside one process would not fix
// that: the workers would share one vnproxd, one SQLite store and one
// collector, which is the arrangement T-2409 was opened to remove. A shard is
// a whole separate Playwright process with its OWN stacks, so two shards share
// no writable surface at all — parallelism and isolation come from the same
// change.
//
// WHY NOT A DAEMON PER SPEC FILE. That is T-2409's branch
// (`t-2409-e2e-store-isolation`): it works, and it cost +79% wall clock
// (16.3 min) for 31 daemon starts. Shard granularity buys the isolation that
// matters — a spec cannot corrupt another shard's store — at four daemon
// starts instead of thirty-one. Order-dependence *within* a shard is not
// removed by this file; see T-2505's card for what the bisection found.
//
// WHY THE PORTS ARE WRITTEN AS LITERALS. testdata/dev-ports.tsv is this
// repository's port registry and internal/devports' test scans
// `web/e2e/*.ts` for port literals, refusing any it cannot find a row for.
// Computing shard ports as `base + shard * stride` would hide every one of
// them from that scan — an unregistered bind is exactly the failure the
// registry exists to prevent — so each one is spelled out below in the
// `port:` shape the scan reads.
//
// HOW TO ADD A SPEC. Put its file name in exactly one shard's `specs`. If it
// needs a stack other than the three-node-vlan default, name that stack in
// SPEC_STACKS too. A spec file that is in no shard is a hard error at config
// load (see validateManifest) — a spec that silently stops running is the
// failure mode T-2108 spent an arc recovering from.

import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/** Repo root, relative to web/e2e/.
 *
 * import.meta.url rather than __dirname: Playwright loads the config and the
 * specs as ES modules, where __dirname does not exist. */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** Where generated per-shard daemon configs and shard reports are written.
 * Under web/test-results/, which is already Playwright's own output directory
 * and already gitignored. */
const OUT_DIR = join(REPO_ROOT, "web", "test-results");

/** Optional pre-built binaries. scripts/e2e-shards.sh builds cmd/vnproxd,
 * cmd/pvemock and cmd/k8smock once and puts them here; without them each of
 * the ~20 servers a full sharded run starts would pay `go run`'s link cost
 * separately. A developer running one shard by hand without the script still
 * works — `go run` is the fallback, not a requirement. */
const BIN_DIR = join(OUT_DIR, "e2e-bin");

/** One listening endpoint. The `port:` key is deliberate: it is the shape
 * internal/devports' registry scan recognises (see this file's header). */
interface Endpoint {
  readonly port: number;
}

/** One running copy of a stack: the mock cluster API, and the vnproxd that
 * polls it. `daemon` is absent for the Kubernetes mock, which no vnproxd of
 * its own polls — k8s-overlay.spec.ts registers it against the default
 * stack's daemon. */
interface Instance {
  /** Absent for a stack with no mock server of its own — see the `demo`
   * stack, whose synthetic cluster runs inside its daemon. */
  readonly mock?: Endpoint;
  readonly daemon?: Endpoint;
}

/** The stacks the suite can bring up. */
export type StackName = "default" | "sim" | "scale" | "mgmt" | "alert" | "flow" | "physcollapse" | "k8s" | "demo" | "publicdemo";

interface StackDef {
  /** Which mock server serves this stack's fixture. Absent when the stack
   * has no mock process at all. */
  readonly mockCommand?: "pvemock" | "k8smock";
  /** Repo-relative fixture the mock serves. Absent alongside mockCommand. */
  readonly fixture?: string;
  /** Extra arguments the daemon needs beyond `--config`. `--demo` is the
   * only current user: it is a mode the process is started in, not a
   * setting a config file can carry (see internal/config.Config.Demo). */
  readonly daemonArgs?: readonly string[];
  /** Repo-relative vnproxd config template. Absent when the stack has no
   * daemon of its own. */
  readonly config?: string;
  /** How long the daemon may take to report healthy. scale-lab's collectors
   * have ~8x the entities to poll on their first cycle. */
  readonly daemonTimeoutMS: number;
  /** One entry per shard slot that may run this stack. A stack with a single
   * entry is single-homed: every spec that needs it lives in one shard, which
   * is why only the default stack needs replicas. */
  readonly instances: readonly Instance[];
  /** Why this stack exists as a separate fixture, for the next reader. */
  readonly why: string;
}

/** The default three-node-vlan stack, replicated once per shard.
 *
 * Slot 0 keeps 8006/8007 — the ports `make dev`, the shipped package and
 * every existing doc use — so an unsharded run (VNPROX_E2E_SHARD unset) is
 * byte-identical to what this suite did before T-2505. Slots 1-3 are new
 * registry rows in the 21006/22006/23006 families, deliberately clear of the
 * NNN006 families already claimed. */
const DEFAULT_INSTANCES: readonly Instance[] = [
  { mock: { port: 8006 }, daemon: { port: 8007 } },
  { mock: { port: 21006 }, daemon: { port: 21007 } },
  { mock: { port: 22006 }, daemon: { port: 22007 } },
  { mock: { port: 23006 }, daemon: { port: 23007 } },
];

const STACKS: Readonly<Record<StackName, StackDef>> = {
  default: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/three-node-vlan.yaml",
    config: "testdata/dev.toml",
    daemonTimeoutMS: 120_000,
    instances: DEFAULT_INSTANCES,
    why: "The suite's baseline cluster: three nodes, VLANs, the guests every non-specialised spec drives.",
  },
  sim: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/sim-lab.yaml",
    config: "testdata/dev-sim.toml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 18006 }, daemon: { port: 18007 } }],
    why: "T-504's path-simulator fixture: a deny verdict and an unreachable VLAN neither other cluster can express.",
  },
  scale: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/scale-lab.yaml",
    config: "testdata/dev-scale.toml",
    // 8 nodes x 6 NICs, 300 guests, 40 VNets: the first collector cycle is
    // the slowest boot in the suite.
    daemonTimeoutMS: 180_000,
    instances: [{ mock: { port: 28006 }, daemon: { port: 28007 } }],
    why: "T-607's documented scale target, and the only fixture with free NICs for a bond.create draft.",
  },
  mgmt: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/single-node.yaml",
    config: "testdata/dev-mgmt.toml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 38006 }, daemon: { port: 38007 } }],
    why: "T-703's single-node management SPOF, and the second cluster the federation specs peer with.",
  },
  alert: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/sim-lab.yaml",
    config: "testdata/dev-alert.toml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 48006 }, daemon: { port: 48007 } }],
    why: "T-1005 needs a daemon that has never notified on the finding, which the simulator stack has.",
  },
  flow: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/flow-lab.yaml",
    config: "testdata/dev-flow.toml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 58006 }, daemon: { port: 58007 } }],
    why: "T-1003's NetFlow ingest: the only daemon with a netflow listener bound.",
  },
  physcollapse: {
    mockCommand: "pvemock",
    fixture: "testdata/clusters/phys-collapse.yaml",
    config: "testdata/dev-physcollapse.toml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 61006 }, daemon: { port: 61007 } }],
    why: "T-1907's 10-NIC node, over the physical-collapse threshold no other fixture reaches.",
  },
  k8s: {
    mockCommand: "k8smock",
    fixture: "testdata/k8s/e2e-cluster.yaml",
    daemonTimeoutMS: 120_000,
    instances: [{ mock: { port: 8008 } }],
    why: "T-1502's mock Kubernetes API, registered as a cluster against whichever default daemon the shard owns.",
  },
  // T-2801. The only stack with no mock process: `vnproxd --demo` serves
  // its synthetic cluster from inside itself, over an http.RoundTripper
  // that never dials (internal/demo/transport.go). That is the property
  // demo.spec.ts asserts, so giving this stack a pvemock on a port would
  // quietly remove the thing under test.
  demo: {
    config: "testdata/demo.toml",
    daemonArgs: ["--demo"],
    daemonTimeoutMS: 120_000,
    instances: [{ daemon: { port: 24007 } }],
    why: "T-2801's demo mode: no PVE endpoint, no outbound network, every mutating route a no-op that reports what it would have done.",
  },
  // T-2802. The demo stack's sibling, and deliberately a SECOND daemon
  // rather than a flag on the first: the two assert incompatible things
  // about the same routes. On 24007 POST /changesets is 200 "would have";
  // on 25007 it is 403 before the router sees it. One process cannot be
  // both, so demo.spec.ts and public-demo.spec.ts cannot share one.
  publicdemo: {
    config: "testdata/demo-public.toml",
    daemonArgs: ["--demo", "--public-demo"],
    daemonTimeoutMS: 120_000,
    instances: [{ daemon: { port: 25007 } }],
    why: "T-2802's hosted demo: every mutating route refused at the edge, a session per visitor, per-visitor resource caps.",
  },
};

/** Specs needing something other than the default stack alone.
 *
 * A spec absent from this table needs `["default"]`. Listing every spec would
 * be a second copy of the shard manifest that could disagree with it. */
const SPEC_STACKS: Readonly<Record<string, readonly StackName[]>> = {
  "simulator.spec.ts": ["sim"],
  "scale.spec.ts": ["scale"],
  // Its bond-creation describe drives the scale stack (the only fixture with
  // free NICs); the SDN/IPAM/firewall describe drives the default one. Both,
  // therefore — and NOT "scale" alone, which is the trap T-2409's branch fell
  // into when it moved the whole file onto dev-scale.toml.
  "user-guide-tasks.spec.ts": ["default", "scale"],
  "mgmt-redundancy.spec.ts": ["mgmt"],
  "federation.spec.ts": ["default", "mgmt"],
  "federation-clusters.spec.ts": ["default", "mgmt"],
  "alert-rules.spec.ts": ["alert"],
  "flows.spec.ts": ["flow"],
  "history.spec.ts": ["flow"],
  "physical-collapse.spec.ts": ["physcollapse"],
  "k8s-overlay.spec.ts": ["default", "k8s"],
  // T-2801: the demo daemon is the whole subject — it needs no default
  // stack, and must not be handed one, since "there is no PVE reachable"
  // is the first thing it asserts.
  "demo.spec.ts": ["demo"],
  // T-2802: same reasoning as demo.spec.ts, one stack further out. It must
  // not be handed a default stack either — "there is nothing real behind
  // this" is the first thing a public demo claims.
  "public-demo.spec.ts": ["publicdemo"],
};

export interface ShardDef {
  readonly name: string;
  /** Which slot of a replicated stack this shard uses. Index into
   * StackDef.instances; a single-homed stack ignores it. */
  readonly slot: number;
  readonly specs: readonly string[];
}

/** The manifest, read from shards.json.
 *
 * JSON and not a TypeScript literal for one reason: scripts/e2e-shards.sh has
 * to know the shard names before it can launch anything, and a shell script
 * cannot import a TypeScript module. One file both can read beats a list in
 * the script that drifts from the list in the module.
 *
 * Balanced against per-file durations measured on the dev host on 2026-08-12
 * (89 passed in 9.92 min; per-file totals in T-2505's report): each shard
 * carries ~132s of test time. The single-homed stacks pin their own specs —
 * everything driving the scale stack must share a shard, because a second copy
 * of that stack would need a second pair of registered ports for no gain.
 *
 * Rebalancing is expected as specs are added: the invariants are that every
 * spec appears in exactly one shard and that a single-homed stack's specs stay
 * together. Both are enforced by validateManifest, at config load.
 */
export const SHARDS: readonly ShardDef[] = loadManifest();

/** Parses shards.json without trusting its shape.
 *
 * Read at runtime rather than `import ... with { type: "json" }`: the import
 * assertion's behaviour differs between the transpiler Playwright uses and the
 * Node that executes the result, and a manifest that fails to load is a suite
 * that does not run. */
function loadManifest(): ShardDef[] {
  const path = join(REPO_ROOT, "web", "e2e", "shards.json");
  const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (!Array.isArray(parsed)) {
    throw new Error(`${path} must contain an array of shards`);
  }
  const entries: unknown[] = parsed;
  return entries.map((raw, i): ShardDef => {
    if (typeof raw !== "object" || raw === null) {
      throw new Error(`${path}[${String(i)}] is not an object`);
    }
    // Checked, not blind: the three lines above establish an object, and each
    // field below is type-tested before it is used.
    const entry: Record<string, unknown> = { ...raw };
    const { name, slot, specs } = entry;
    if (typeof name !== "string" || name === "") {
      throw new Error(`${path}[${String(i)}] has no name`);
    }
    if (typeof slot !== "number" || !Number.isInteger(slot) || slot < 0) {
      throw new Error(`${path}[${String(i)}] (${name}) has no integer slot`);
    }
    if (!Array.isArray(specs)) {
      throw new Error(`${path}[${String(i)}] (${name}) has no specs array`);
    }
    const list: unknown[] = specs;
    if (!list.every((s): s is string => typeof s === "string")) {
      throw new Error(`${path}[${String(i)}] (${name}) has a non-string entry in specs`);
    }
    return { name, slot, specs: list };
  });
}

/** The shard this process is running, or undefined for a whole-suite run.
 *
 * An unsharded run is not a legacy path to be removed: it is how a developer
 * reproduces a cross-spec interaction, how `--repeat-each=2` is run over
 * everything at once, and the arrangement every wall-clock number before
 * T-2505 was measured against. */
export function activeShard(): ShardDef | undefined {
  const name = process.env.VNPROX_E2E_SHARD;
  if (name === undefined || name === "") return undefined;
  const shard = SHARDS.find((s) => s.name === name);
  if (shard === undefined) {
    throw new Error(`VNPROX_E2E_SHARD=${name} names no shard; known shards: ${SHARDS.map((s) => s.name).join(", ")}`);
  }
  return shard;
}

/** The slot the current process uses for a replicated stack. Slot 0 for a
 * whole-suite run, which is what keeps that run on 8006/8007. */
function activeSlot(): number {
  return activeShard()?.slot ?? 0;
}

/** Every stack the given specs need, deduplicated. */
function stacksFor(specs: readonly string[]): StackName[] {
  const needed = new Set<StackName>();
  for (const spec of specs) {
    for (const stack of SPEC_STACKS[spec] ?? (["default"] as const)) {
      needed.add(stack);
    }
  }
  return [...needed];
}

function instanceFor(stack: StackName, slot: number): Instance {
  const def = STACKS[stack];
  // A single-homed stack has one instance whatever slot asks: every spec that
  // needs it lives in one shard, enforced by validateManifest.
  const idx = def.instances.length === 1 ? 0 : slot;
  const inst = def.instances[idx];
  if (inst === undefined) {
    throw new Error(`stack ${stack} has no instance for slot ${String(slot)}; add a registered port pair to DEFAULT_INSTANCES`);
  }
  return inst;
}

/** The base URL of a stack's daemon, for the shard this process is running.
 *
 * Specs use this instead of a hard-coded https://127.0.0.1:8007: shard-2's
 * default daemon is on 21007, and a literal would quietly talk to whichever
 * shard happened to own 8007. */
export function stackURL(stack: StackName = "default"): string {
  const inst = instanceFor(stack, activeSlot());
  if (inst.daemon === undefined) {
    throw new Error(`stack ${stack} has no daemon; it is a mock server only`);
  }
  return `https://127.0.0.1:${String(inst.daemon.port)}`;
}

/** The base URL of a stack's mock cluster API (plain HTTP, as pvemock and
 * k8smock serve). */
export function mockURL(stack: StackName = "default"): string {
  const mock = instanceFor(stack, activeSlot()).mock;
  if (mock === undefined) {
    throw new Error(`stack ${stack} has no mock server; its cluster runs inside its own daemon`);
  }
  return `http://127.0.0.1:${String(mock.port)}`;
}

/** Per-shard scratch root. Every var/ path in a generated config is redirected
 * under here, so two shards' stores, interface sandboxes, capture files and
 * key material never touch. Wiped by scripts/e2e-shards.sh before a run. */
export function shardVarDir(shardName: string): string {
  return `var/e2e-shards/${shardName}`;
}

/** Keys whose absence from a config template means the generated config would
 * silently keep writing where the template pointed — i.e. would NOT be
 * isolated. Inherited from T-2409's branch, which learned it the same way. */
const REQUIRED_KEYS = ["db_path", "dev_interfaces_dir", "listen"] as const;

/** Rewrites a dev config template for one shard: its ports, and every var/
 * path moved under the shard's own directory.
 *
 * Generated rather than committed as testdata/dev-shard-N.toml, on purpose.
 * There are already seven near-identical dev-*.toml files, and three more
 * hand-maintained copies of dev.toml would drift from it the first time a
 * setting is added — the T-1307 comment in dev-scale.toml is a record of
 * exactly that happening. The ports still appear as literals in this file, so
 * the registry scan sees them.
 */
function writeShardConfig(stack: StackName, shardName: string, slot: number): string {
  const def = STACKS[stack];
  if (def.config === undefined) {
    throw new Error(`stack ${stack} has no daemon config to rewrite`);
  }
  const inst = instanceFor(stack, slot);
  const daemon = inst.daemon;
  if (daemon === undefined) {
    throw new Error(`stack ${stack} has no daemon endpoint`);
  }

  const source = join(REPO_ROOT, def.config);
  const varDir = shardVarDir(shardName);
  const replaced = new Set<string>();

  const out = readFileSync(source, "utf8")
    .split("\n")
    .map((line) => {
      const trimmed = line.trim();
      if (trimmed.startsWith("listen ") || trimmed.startsWith("listen=")) {
        replaced.add("listen");
        return `listen = "127.0.0.1:${String(daemon.port)}"`;
      }
      if (trimmed.startsWith("api_url ") && inst.mock !== undefined) {
        replaced.add("api_url");
        return `api_url = "http://127.0.0.1:${String(inst.mock.port)}"`;
      }
      // Any relative var/ path is app-owned mutable state: the SQLite store,
      // the interfaces sandbox, session/metrics/signing keys, captures.
      const m = /^(\s*)([a-z_]+)\s*=\s*"var\/(.+)"\s*$/.exec(line);
      if (m !== null) {
        const [, indent, key, rest] = m;
        if (indent !== undefined && key !== undefined && rest !== undefined) {
          replaced.add(key);
          return `${indent}${key} = "${varDir}/${rest}"`;
        }
      }
      return line;
    })
    .join("\n");

  const missing = REQUIRED_KEYS.filter((k) => !replaced.has(k));
  if (missing.length > 0) {
    throw new Error(
      `${def.config} has no ${missing.join(", ")} to redirect; shard ${shardName} would share state with every ` +
        `other shard. Update web/e2e/shards.ts to match that config's current shape.`,
    );
  }

  const dir = join(OUT_DIR, "shard-configs");
  mkdirSync(dir, { recursive: true });
  const path = join(dir, `${shardName}-${stack}.toml`);
  writeFileSync(path, out);
  // Repo-relative: the servers run with cwd = repo root.
  return `web/test-results/shard-configs/${shardName}-${stack}.toml`;
}

/** `go run ./cmd/x` unless scripts/e2e-shards.sh already built it. */
function command(binary: string, args: readonly string[]): string {
  const prebuilt = join(BIN_DIR, binary);
  const head = existsSync(prebuilt) ? prebuilt : `go run ./cmd/${binary}`;
  return [head, ...args].join(" ");
}

/** A Playwright webServer entry. Structurally typed rather than imported from
 * @playwright/test, which does not export the type on its own. */
export interface WebServerSpec {
  command: string;
  cwd: string;
  port?: number;
  url?: string;
  ignoreHTTPSErrors?: boolean;
  reuseExistingServer: boolean;
  timeout: number;
}

/** The directory the shard-isolation canary writes its marker into.
 *
 * Outside shardVarDir on purpose: it is the one thing two shards must both be
 * able to see, because proving they cannot see each other's *store* requires
 * some out-of-band channel to say when there was something to see. */
export const CANARY_DIR = "var/e2e-canary";

/** Wipes the mutable state this shard is about to create, so a run never
 * inherits the previous one's store, interfaces sandbox or captures.
 *
 * Called at config load — before any server starts — which is where the
 * per-webServer `rm -f var/dev-vnprox.db` shell preludes used to do it. */
function resetShardState(shardName: string): void {
  // ONLY in the runner process. Playwright re-imports this config in every
  // worker process it spawns, and a worker that wiped the shard's directory
  // would be deleting the SQLite store and the interfaces sandbox out from
  // under a daemon that has already started. That cost an hour: the symptom
  // was physical-collapse.spec.ts finding six of its fixture's ten NICs,
  // because the sandbox the dev NodeAgent had seeded at boot was gone by the
  // time the topology was read.
  if (process.env.TEST_WORKER_INDEX !== undefined) return;

  rmSync(join(REPO_ROOT, "var", "e2e-shards", shardName), { recursive: true, force: true });
  // Only the shard that owns the canary writer clears the marker directory:
  // every other shard reads it, and a reader that wipes it would race the
  // writer it is waiting for.
  if (SHARDS.find((s) => s.specs.includes(CANARY_WRITER_SPEC))?.name === shardName || shardName === WHOLE_SUITE) {
    rmSync(join(REPO_ROOT, CANARY_DIR), { recursive: true, force: true });
  }
}

/** The spec that writes the canary, named here so resetShardState and the
 * manifest cannot disagree about which shard owns it. */
export const CANARY_WRITER_SPEC = "aa-shard-canary.spec.ts";

/** The pseudo-shard name a whole-suite run uses for its scratch directory. */
export const WHOLE_SUITE = "whole-suite";

/** Every server the given shard (or the whole suite) has to bring up. */
export function webServers(shardName: string, specs: readonly string[], slot: number): WebServerSpec[] {
  resetShardState(shardName);
  const servers: WebServerSpec[] = [];
  for (const stack of stacksFor(specs)) {
    const def = STACKS[stack];
    const inst = instanceFor(stack, slot);
    // A stack may have no mock process of its own (the demo stack serves
    // its cluster from inside the daemon). mockCommand/fixture/mock travel
    // together — all three or none.
    if (def.mockCommand !== undefined && def.fixture !== undefined && inst.mock !== undefined) {
      servers.push({
        command: command(def.mockCommand, ["--addr", `127.0.0.1:${String(inst.mock.port)}`, "--fixture", def.fixture]),
        cwd: "..",
        port: inst.mock.port,
        reuseExistingServer: false,
        timeout: 120_000,
      });
    }
    if (def.config !== undefined && inst.daemon !== undefined) {
      servers.push({
        command: command("vnproxd", [...(def.daemonArgs ?? []), "--config", writeShardConfig(stack, shardName, slot)]),
        cwd: "..",
        url: `https://127.0.0.1:${String(inst.daemon.port)}/api/v1/health`,
        ignoreHTTPSErrors: true,
        reuseExistingServer: false,
        timeout: def.daemonTimeoutMS,
      });
    }
  }
  return servers;
}

/** Fails loudly on a manifest that would silently not run something.
 *
 * Called at config load, so the mistake surfaces before a single test runs
 * rather than as a suite that is quietly 8 specs smaller than it was. */
export function validateManifest(): void {
  const onDisk = readdirSync(join(REPO_ROOT, "web", "e2e"))
    .filter((f) => f.endsWith(".spec.ts"))
    .sort();

  const seen = new Map<string, string>();
  for (const shard of SHARDS) {
    for (const spec of shard.specs) {
      const already = seen.get(spec);
      if (already !== undefined) {
        throw new Error(`${spec} is in both ${already} and ${shard.name}; a spec must be in exactly one shard`);
      }
      seen.set(spec, shard.name);
    }
  }

  const missing = onDisk.filter((f) => !seen.has(f));
  if (missing.length > 0) {
    throw new Error(
      `these spec files are in no shard and would not run: ${missing.join(", ")}. ` +
        `Add each to a shard's specs in web/e2e/shards.ts.`,
    );
  }
  const ghosts = [...seen.keys()].filter((f) => !onDisk.includes(f));
  if (ghosts.length > 0) {
    throw new Error(`web/e2e/shards.ts names spec files that do not exist: ${ghosts.join(", ")}`);
  }

  // A single-homed stack has one registered port pair, so every spec needing
  // it must share a shard; two shards would race for the same port and the
  // second would fail to bind with a message about ports, not about this.
  const homes = new Map<StackName, string>();
  for (const shard of SHARDS) {
    for (const stack of stacksFor(shard.specs)) {
      if (STACKS[stack].instances.length > 1) continue;
      const home = homes.get(stack);
      if (home !== undefined && home !== shard.name) {
        throw new Error(
          `stack "${stack}" is single-homed but is needed by both ${home} and ${shard.name}; ` +
            `move those specs into one shard, or register a second port pair for it in testdata/dev-ports.tsv.`,
        );
      }
      homes.set(stack, shard.name);
    }
  }

  const slots = new Set(SHARDS.map((s) => s.slot));
  if (slots.size !== SHARDS.length) {
    throw new Error("two shards share a slot; each needs its own replica of the default stack");
  }
}

/** All spec file names, in manifest order — the whole-suite run's set. */
export function allSpecs(): string[] {
  return SHARDS.flatMap((s) => s.specs);
}
