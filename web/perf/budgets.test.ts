// SPDX-License-Identifier: Apache-2.0

// T-2506. Unit coverage for the browser half of the budget gate.
//
// These run under vitest (`make check`), NOT under Playwright: everything here
// is pure logic over the same perf/budgets.json the e2e spec reads, so the
// comparator, the median and the two structural rules are proven on every
// commit rather than only when somebody runs the 5-minute e2e suite.
import { describe, expect, it } from "vitest";

import {
  budgetById,
  budgetsForSite,
  check,
  coresFactor,
  evaluate,
  factorFor,
  loadBudgets,
  median,
  missing,
  parseBudgets,
  renderReport,
  SCALE_SITE,
  thisMachine,
  type Budget,
  type Machine,
} from "./budgets";

const machine: Machine = { cores: 32, coresFactor: 1 };

function budget(overrides: Partial<Budget> = {}): Budget {
  return {
    id: "example.thing_ms",
    title: "an example",
    site: "web/e2e/example.spec.ts",
    metric: "wall clock of the example",
    unit: "ms",
    direction: "max",
    scaling: "cores",
    enforcement: "gate",
    why: "measured on the reference host on 2026-08-12",
    limit: 100,
    samples: 5,
    ...overrides,
  };
}

function fileText(budgets: readonly Partial<Budget>[]): string {
  return JSON.stringify({
    comment: "fixture",
    reference_host: { name: "test host", cores: 32 },
    calibration: { workload: "w", reference_ns: 1, samples: 1, max_factor: 8 },
    budgets: budgets.map((b) => budget(b)),
  });
}

describe("median", () => {
  it("is a real observation for an odd N", () => {
    expect(median([5, 1, 3])).toBe(3);
  });
  it("averages the middle pair for an even N", () => {
    expect(median([1, 2, 3, 4])).toBe(2.5);
  });
  it("is not moved by one huge outlier", () => {
    expect(median([70, 71, 9000, 72, 73])).toBe(72);
  });
  it("refuses to invent a value for no samples", () => {
    expect(() => median([])).toThrow(/no samples/);
  });
});

describe("coresFactor", () => {
  // The same three rungs as web/playwright.config.ts's slowFactor and
  // internal/perfbudget.CoresFactor. Three copies of a ladder that disagree is
  // two answers to "how much slower is a small machine".
  it.each([
    [1, 2.5],
    [2, 2.5],
    [3, 2.5],
    [4, 1.5],
    [7, 1.5],
    [8, 1],
    [32, 1],
  ])("%i cores -> x%f", (cores, want) => {
    expect(coresFactor(cores)).toBe(want);
  });
});

describe("the structural rules the file must obey", () => {
  it("refuses an absolute budget that gates", () => {
    expect(() => parseBudgets(fileText([{ scaling: "absolute", enforcement: "gate" }]), "fixture")).toThrow(
      /absolute budget may not gate/,
    );
  });
  it("allows an absolute budget that only reports", () => {
    expect(() => parseBudgets(fileText([{ scaling: "absolute", enforcement: "report" }]), "fixture")).not.toThrow();
  });
  it("refuses a gate that decides on fewer than three samples", () => {
    expect(() => parseBudgets(fileText([{ samples: 2 }]), "fixture")).toThrow(/at least 3 samples/);
  });
  it("allows a single-sample budget that only reports", () => {
    expect(() => parseBudgets(fileText([{ samples: 1, enforcement: "report", scaling: "absolute" }]), "fixture")).not.toThrow();
  });
  it("refuses a browser-side budget that asks for the Go calibration kernel", () => {
    expect(() => parseBudgets(fileText([{ scaling: "calibrated" }]), "fixture")).toThrow(/Go calibration kernel/);
  });
  it("refuses duplicate ids", () => {
    expect(() => parseBudgets(fileText([{}, {}]), "fixture")).toThrow(/duplicate budget id/);
  });
  it("refuses a budget with a missing field rather than defaulting it to zero", () => {
    const text = JSON.stringify({
      reference_host: { name: "h", cores: 1 },
      budgets: [{ id: "a", title: "t", site: "s", metric: "m", unit: "ms", direction: "max", scaling: "cores", enforcement: "report", why: "w" }],
    });
    expect(() => parseBudgets(text, "fixture")).toThrow(/limit must be a finite number/);
  });
});

describe("evaluate", () => {
  it("passes when the median is inside the effective limit, and reports the headroom", () => {
    const r = evaluate(budget(), [40, 41, 39, 40, 41], machine);
    expect(r.pass).toBe(true);
    expect(r.headroom).toBeCloseTo(0.6, 5);
  });

  // AC4, at the comparator level. Every position, not just the middle: an
  // implementation that read samples[0], or that trimmed the extremes, would
  // survive a fixture that only put the slow sample where it was not looking.
  it.each([0, 1, 2, 3, 4])("is not failed by a single slow outlier at position %i", (pos) => {
    const samples = [70, 71, 72, 73, 74];
    samples[pos] = 900;
    const r = evaluate(budget(), samples, machine);
    expect(r.pass).toBe(true);
    expect(check([r])).toBeNull();
  });

  it("is not rescued by a single fast sample", () => {
    const r = evaluate(budget(), [900, 901, 70, 902, 903], machine);
    expect(r.pass).toBe(false);
  });

  it("stretches the limit on a small machine and says by how much", () => {
    const r = evaluate(budget(), [200, 200, 200, 200, 200], { cores: 2, coresFactor: coresFactor(2) });
    expect(r.effective).toBe(250);
    expect(r.factor).toBe(2.5);
    expect(r.pass).toBe(true);
  });

  it("refuses a sample count the budget did not ask for", () => {
    expect(() => evaluate(budget(), [1, 2, 3], machine)).toThrow(/samples=5/);
  });

  it("handles a floor as well as a ceiling", () => {
    const fps = budget({ direction: "min", unit: "fps", limit: 30 });
    expect(evaluate(fps, [45, 44, 46, 45, 44], machine).pass).toBe(true);
    const under = evaluate(fps, [20, 21, 19, 20, 21], machine);
    expect(under.pass).toBe(false);
    expect(check([under])).toMatch(/under min budget/);
  });

  it("cannot evaluate a Go-calibrated budget", () => {
    // Constructed directly rather than parsed, since parseBudgets rejects this
    // combination for a web/ site — this is the second line of defence.
    const b = budget({ site: "internal/collect/sim_bench_test.go", scaling: "calibrated" });
    expect(() => factorFor(b, machine)).toThrow(/cannot be evaluated in the browser harness/);
  });
});

// AC3, in the browser half: the same 4%-per-step fixture the Go side runs.
it("a 4%-per-step degradation fails at the crossing, not at any single step", () => {
  const b = budget();
  const perStep = 1.04;
  let value = 88; // 12% under budget
  const verdicts: boolean[] = [];
  for (let step = 0; step <= 5; step++) {
    verdicts.push(evaluate(b, [value, value, value, value, value], machine).pass);
    value *= perStep;
  }
  // No step is a 5% regression on its own, so a "worse than last run by 5%"
  // gate never fires on this series. A threshold gate fires exactly once.
  expect(perStep - 1).toBeLessThan(0.05);
  expect(verdicts).toEqual([true, true, true, true, false, false]);
});

describe("check and report", () => {
  it("names the budget, both limits and the measurement", () => {
    const r = evaluate(budget(), [300, 300, 300, 300, 300], { cores: 2, coresFactor: 2.5 });
    const msg = check([r]);
    expect(msg).not.toBeNull();
    for (const want of ["example.thing_ms", "300.0 ms", "100.0 ms", "250.0 ms", "x2.50", "cores"]) {
      expect(msg).toContain(want);
    }
  });

  it("never fails the run on a report-only budget, but still records it as over", () => {
    const b = budget({ enforcement: "report", scaling: "absolute" });
    const r = evaluate(b, [900, 900, 900, 900, 900], machine);
    expect(r.pass).toBe(false);
    expect(check([r])).toBeNull();
    expect(renderReport([r], machine)).toContain("OVER (report-only)");
  });

  // AC5.
  it("reports every budget, including the passing ones", () => {
    const results = [
      evaluate(budget({ id: "a.pass" }), [40, 40, 40, 40, 40], machine),
      evaluate(budget({ id: "a.fail" }), [140, 140, 140, 140, 140], machine),
    ];
    const out = renderReport(results, machine);
    expect(out).toContain("a.pass");
    expect(out).toContain("60.0%");
    expect(out).toContain("a.fail");
    expect(out).toContain("-40.0%");
  });

  it("catches a budget nothing measured", () => {
    const measured = evaluate(budget({ id: "a.measured" }), [1, 1, 1, 1, 1], machine);
    expect(missing([budget({ id: "a.measured" }), budget({ id: "a.forgotten" })], [measured])).toContain("a.forgotten");
    expect(missing([budget({ id: "a.measured" })], [measured])).toBeNull();
  });
});

describe("the shipped perf/budgets.json", () => {
  it("loads, and the scale spec's budgets are all there", () => {
    const file = loadBudgets();
    const forSpec = budgetsForSite(file, SCALE_SITE);
    expect(forSpec.length).toBeGreaterThan(0);
    for (const b of forSpec) {
      expect(b.scaling).not.toBe("calibrated");
      expect(budgetById(file, b.id)).toEqual(b);
    }
  });

  it("agrees with the Go reader about the reference host's core count", () => {
    // Not an assertion about this machine — an assertion that the file says
    // which machine its numbers hold for at all, which T-2505-input-02 is the
    // argument for.
    expect(loadBudgets().referenceHost.cores).toBeGreaterThan(0);
    expect(loadBudgets().referenceHost.name).not.toBe("");
  });

  it("normalises for whatever machine is running it", () => {
    const m = thisMachine();
    expect(m.cores).toBeGreaterThan(0);
    expect(m.coresFactor).toBe(coresFactor(m.cores));
  });
});
