// SPDX-License-Identifier: Apache-2.0

// T-2506: the browser half of the one-file performance budget.
//
// perf/budgets.json is the single source; internal/perfbudget reads it for the
// Go measurement site and this module reads the SAME file for the Playwright
// one. Nothing here holds a number of its own — a limit written twice is a
// limit that can disagree with itself, which is the state docs/performance.md
// was in before this card.
//
// WHY IT IS READ AT RUNTIME rather than imported as JSON. Exactly the reason
// web/e2e/shards.ts reads shards.json with readFileSync: the JSON import
// assertion behaves differently between the transpiler Playwright uses and the
// Node that executes the result, and it would also mean the file had to live
// under web/ to be inside the Vite root. It does not: it is repository-level
// data, and both languages read it from the repository root.
//
// WHAT THE BROWSER SIDE CAN AND CANNOT NORMALISE. `calibrated` scaling is
// measured by a Go CPU kernel in the Go test's own process and is meaningless
// here, so this module implements `cores` and `absolute` and rejects
// `calibrated` for a browser-side site — a rule internal/perfbudget's Validate
// enforces on the file itself, so the two cannot disagree about it either.
import { readFileSync } from "node:fs";
import { availableParallelism } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/** Repo root, relative to web/perf/. import.meta.url and not __dirname:
 * Playwright loads this as an ES module, where __dirname does not exist. */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** The one file. Must equal internal/perfbudget.RepoRelPath. */
export const BUDGETS_PATH = "perf/budgets.json";

/** The Playwright site, as perf/budgets.json spells it. */
export const SCALE_SITE = "web/e2e/scale.spec.ts";

/** Below this many samples a budget may not gate. Must equal
 * internal/perfbudget.MinGateSamples — a gate that decides on one sample fails
 * on one scheduling hiccup, which is what T-2505-input-01 recorded four
 * independent times. */
export const MIN_GATE_SAMPLES = 3;

export type Direction = "max" | "min";
export type Scaling = "calibrated" | "cores" | "absolute";
export type Enforcement = "gate" | "report";

export interface Budget {
  readonly id: string;
  readonly title: string;
  readonly site: string;
  readonly metric: string;
  readonly unit: string;
  readonly direction: Direction;
  readonly scaling: Scaling;
  readonly enforcement: Enforcement;
  readonly why: string;
  readonly limit: number;
  readonly samples: number;
}

export interface ReferenceHost {
  readonly name: string;
  readonly cores: number;
}

export interface BudgetsFile {
  readonly referenceHost: ReferenceHost;
  readonly budgets: readonly Budget[];
}

/** The measuring machine. No calibration kernel: see the header. */
export interface Machine {
  readonly cores: number;
  readonly coresFactor: number;
}

export interface Result {
  readonly budget: Budget;
  readonly samples: readonly number[];
  readonly median: number;
  /** The limit as normalised for this machine — the number actually enforced. */
  readonly effective: number;
  readonly factor: number;
  /** Signed fraction of the effective budget still unused: 0.42 is 42% spare,
   * -0.10 is 10% over. Reported for every budget on every run (AC5). */
  readonly headroom: number;
  readonly pass: boolean;
}

/** T-2505's deadline ladder, and the only place the browser side normalises
 * for machine size. web/playwright.config.ts imports this rather than keeping
 * its own copy, and internal/perfbudget.CoresFactor is the Go mirror with a
 * table test over the same three rungs.
 *
 * availableParallelism() and not cpus().length: it honours CPU affinity, so a
 * `taskset` run and a cpuset-restricted container both read the cores they can
 * actually use. */
export function coresFactor(cores: number): number {
  if (cores >= 8) return 1;
  if (cores >= 4) return 1.5;
  return 2.5;
}

export function thisMachine(): Machine {
  const cores = availableParallelism();
  return { cores, coresFactor: coresFactor(cores) };
}

/** Median, not mean: one sample landing on a GC pause or a sibling process's
 * burst moves a mean and does not move a median. AC4's whole noise policy. */
export function median(samples: readonly number[]): number {
  if (samples.length === 0) throw new Error("median of no samples");
  const sorted = [...samples].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) return sorted[mid] ?? NaN;
  return ((sorted[mid - 1] ?? NaN) + (sorted[mid] ?? NaN)) / 2;
}

function fail(where: string, message: string): never {
  throw new Error(`${where}: ${message}`);
}

function str(raw: Record<string, unknown>, key: string, where: string): string {
  const v = raw[key];
  if (typeof v !== "string" || v === "") fail(where, `${key} must be a non-empty string`);
  return v;
}

function num(raw: Record<string, unknown>, key: string, where: string): number {
  const v = raw[key];
  if (typeof v !== "number" || !Number.isFinite(v)) fail(where, `${key} must be a finite number`);
  return v;
}

function oneOf<T extends string>(raw: Record<string, unknown>, key: string, allowed: readonly T[], where: string): T {
  const v = str(raw, key, where);
  const hit = allowed.find((a) => a === v);
  if (hit === undefined) fail(where, `${key} must be one of ${allowed.join(", ")}, got ${v}`);
  return hit;
}

function parseBudget(raw: unknown, where: string): Budget {
  if (typeof raw !== "object" || raw === null) fail(where, "not an object");
  const o: Record<string, unknown> = { ...raw };
  const id = str(o, "id", where);
  const at = `${where} (${id})`;
  const budget: Budget = {
    id,
    title: str(o, "title", at),
    site: str(o, "site", at),
    metric: str(o, "metric", at),
    unit: str(o, "unit", at),
    direction: oneOf(o, "direction", ["max", "min"] as const, at),
    scaling: oneOf(o, "scaling", ["calibrated", "cores", "absolute"] as const, at),
    enforcement: oneOf(o, "enforcement", ["gate", "report"] as const, at),
    why: str(o, "why", at),
    limit: num(o, "limit", at),
    samples: num(o, "samples", at),
  };
  // The two structural rules internal/perfbudget.Validate enforces, restated
  // here so the browser side is not the weaker reader of the same file.
  if (budget.enforcement === "gate" && budget.scaling === "absolute") {
    fail(at, "an absolute budget may not gate — an unnormalised wall-clock number is green on the reference host and red on a 2-4 core runner (T-2505-input-02)");
  }
  if (budget.enforcement === "gate" && budget.samples < MIN_GATE_SAMPLES) {
    fail(at, `a gating budget needs at least ${String(MIN_GATE_SAMPLES)} samples so the verdict is a median, got ${String(budget.samples)}`);
  }
  if (budget.scaling === "calibrated" && budget.site.startsWith("web/")) {
    fail(at, "a browser-side budget cannot use the Go calibration kernel; use \"cores\"");
  }
  return budget;
}

/** Parses the budgets document without trusting its shape. Exported for the
 * unit tests, which feed it deliberately broken files. */
export function parseBudgets(text: string, where: string): BudgetsFile {
  const parsed: unknown = JSON.parse(text);
  if (typeof parsed !== "object" || parsed === null) fail(where, "not an object");
  const root: Record<string, unknown> = { ...parsed };

  const hostRaw: unknown = root.reference_host;
  if (typeof hostRaw !== "object" || hostRaw === null) fail(where, "reference_host is missing");
  const host: Record<string, unknown> = { ...hostRaw };

  const budgetsRaw: unknown = root.budgets;
  if (!Array.isArray(budgetsRaw)) fail(where, "budgets must be an array");
  const list: unknown[] = budgetsRaw;
  const budgets = list.map((b, i) => parseBudget(b, `${where}[${String(i)}]`));

  const ids = new Set<string>();
  for (const b of budgets) {
    if (ids.has(b.id)) fail(where, `duplicate budget id ${b.id}`);
    ids.add(b.id);
  }

  return {
    referenceHost: { name: str(host, "name", `${where}.reference_host`), cores: num(host, "cores", `${where}.reference_host`) },
    budgets,
  };
}

/** Reads the repository's own budgets file. */
export function loadBudgets(): BudgetsFile {
  const path = join(REPO_ROOT, BUDGETS_PATH);
  return parseBudgets(readFileSync(path, "utf8"), BUDGETS_PATH);
}

export function budgetById(file: BudgetsFile, id: string): Budget {
  const hit = file.budgets.find((b) => b.id === id);
  if (hit === undefined) {
    throw new Error(`no budget "${id}" in ${BUDGETS_PATH}; known: ${file.budgets.map((b) => b.id).join(", ")}`);
  }
  return hit;
}

export function budgetsForSite(file: BudgetsFile, site: string): Budget[] {
  return file.budgets.filter((b) => b.site === site);
}

export function factorFor(budget: Budget, machine: Machine): number {
  switch (budget.scaling) {
    case "cores":
      return machine.coresFactor;
    case "absolute":
      return 1;
    case "calibrated":
      throw new Error(
        `budget ${budget.id}: "calibrated" scaling is measured by internal/perfbudget's Go kernel and cannot be evaluated in the browser harness; a browser-side budget must use "cores"`,
      );
  }
}

/** Compares samples against a budget.
 *
 * Note what is NOT a parameter: any previous run. The gate is threshold-based
 * by construction (AC3), so a drift that never regresses 5% in one step still
 * fails once it crosses the line, and there is nothing here a step-over-step
 * comparison could be made against. */
export function evaluate(budget: Budget, samples: readonly number[], machine: Machine): Result {
  if (samples.length !== budget.samples) {
    throw new Error(
      `budget ${budget.id}: ${BUDGETS_PATH} declares samples=${String(budget.samples)}, got ${String(samples.length)} — the median N is part of the budget, not the caller's choice`,
    );
  }
  const factor = factorFor(budget, machine);
  const med = median(samples);
  const effective = budget.limit * factor;
  const pass = budget.direction === "max" ? med <= effective : med >= effective;
  const headroom = budget.direction === "max" ? (effective - med) / effective : (med - effective) / effective;
  return { budget, samples: [...samples], median: med, effective, factor, headroom, pass };
}

function fmt(v: number, unit: string): string {
  return `${v.toFixed(1)} ${unit}`;
}

/** Every result, passing ones included — AC5. */
export function renderReport(results: readonly Result[], machine: Machine): string {
  const lines = [
    `performance budgets (${BUDGETS_PATH}), ${String(machine.cores)} cores, x${machine.coresFactor.toFixed(2)} cores normalisation`,
    `  ${"budget".padEnd(48)} ${"median".padStart(10)} ${"budget".padStart(10)} ${"effective".padStart(10)} ${"headroom".padStart(9)} verdict`,
  ];
  for (const r of results) {
    const verdict = r.pass ? "PASS" : r.budget.enforcement === "report" ? "OVER (report-only)" : "FAIL";
    lines.push(
      `  ${r.budget.id.padEnd(48)} ${fmt(r.median, r.budget.unit).padStart(10)} ${fmt(r.budget.limit, r.budget.unit).padStart(10)} ` +
        `${fmt(r.effective, r.budget.unit).padStart(10)} ${(r.headroom * 100).toFixed(1).padStart(8)}% ${verdict}`,
    );
  }
  return lines.join("\n");
}

/** The gate. Returns null when every gating budget is inside its limit, and
 * otherwise a message naming each one, both its limits and the measurement. */
export function check(results: readonly Result[]): string | null {
  const over = results
    .filter((r) => !r.pass && r.budget.enforcement === "gate")
    .map(
      (r) =>
        `${r.budget.id} (${r.budget.title}): ${r.budget.direction === "min" ? "under" : "over"} ${r.budget.direction} budget — ` +
        `measured ${fmt(r.median, r.budget.unit)} (median of ${String(r.samples.length)}), budget ${fmt(r.budget.limit, r.budget.unit)}, ` +
        `effective ${fmt(r.effective, r.budget.unit)} here (x${r.factor.toFixed(2)} ${r.budget.scaling} normalisation). ${r.budget.why}`,
    );
  if (over.length === 0) return null;
  return `performance budget exceeded (${BUDGETS_PATH}):\n  ${over.join("\n  ")}`;
}

/** Names budgets that were expected and never measured. An unmeasured budget
 * reads exactly like a passing one — the same failure shape as a spec file in
 * no shard (T-2505's manifest check). */
export function missing(expected: readonly Budget[], measured: readonly Result[]): string | null {
  const got = new Set(measured.map((r) => r.budget.id));
  const gone = expected.filter((b) => !got.has(b.id)).map((b) => b.id);
  if (gone.length === 0) return null;
  return `these budgets name this site in ${BUDGETS_PATH} but nothing measured them: ${gone.join(", ")}`;
}
