// The verdict mapping is the load-bearing judgement in the whole failure-
// simulation surface, so it is tested directly rather than only through the
// panel. The property under test throughout: an impact the simulator could
// not decide must never come out as a definite one.
import { describe, expect, it } from "vitest";
import type { FailsimImpact } from "../api/types";
import {
  SPOF_VERDICT_LABEL,
  describeDimensions,
  spofAffectedRefs,
  spofVerdict,
  spofVerdictExplanation,
  spofVerdictIsPartial,
} from "./spofVerdict";

function impact(overrides: Partial<FailsimImpact> = {}): FailsimImpact {
  return {
    target: "physnic:pve1:eno1",
    severity: "none",
    disconnectedGuests: [],
    strandedVlans: [],
    mgmtPathLoss: [],
    notEvaluated: [],
    quorumRisk: false,
    cephRisk: false,
    ...overrides,
  };
}

describe("spofVerdict", () => {
  it("maps the two known-damage severities", () => {
    expect(spofVerdict(impact({ severity: "critical" }))).toBe("critical");
    expect(spofVerdict(impact({ severity: "warning" }))).toBe("degraded");
  });

  it("claims no impact only when every dimension was evaluated", () => {
    expect(spofVerdict(impact({ severity: "none" }))).toBe("no-impact");
    expect(spofVerdict(impact({ severity: "none", notEvaluated: ["quorum"] }))).toBe("indeterminate");
  });

  it("renders 'info' — nothing known-broken but something unassessed — as indeterminate", () => {
    // This is the arm the whole rule exists for: internal/failsim emits
    // "info" precisely when it could not check something, and reading it as
    // a pass would be the confident-skip bug this codebase has now hit
    // several times.
    const im = impact({ severity: "info", notEvaluated: ["ceph", "tunnels"] });
    expect(spofVerdict(im)).toBe("indeterminate");
    expect(SPOF_VERDICT_LABEL[spofVerdict(im)]).toBe("Indeterminate");
  });

  it("renders an unrecognised severity as indeterminate rather than coercing it", () => {
    expect(spofVerdict(impact({ severity: "catastrophic" }))).toBe("indeterminate");
    expect(spofVerdict(impact({ severity: "" }))).toBe("indeterminate");
    expect(spofVerdict(impact({ severity: "NONE" }))).toBe("indeterminate");
  });

  it("never explains an indeterminate verdict as safe", () => {
    const unknownDimension = spofVerdictExplanation(impact({ severity: "info", notEvaluated: ["quorum"] }));
    expect(unknownDimension).toContain("Not enough information");
    expect(unknownDimension).toContain("corosync quorum");

    const unknownSeverity = spofVerdictExplanation(impact({ severity: "who-knows" }));
    expect(unknownSeverity).toContain("Not enough information");
  });
});

describe("spofVerdictIsPartial", () => {
  it("flags a known-damage verdict that still has an unevaluated dimension", () => {
    expect(spofVerdictIsPartial(impact({ severity: "critical", notEvaluated: ["ceph"] }))).toBe(true);
    expect(spofVerdictIsPartial(impact({ severity: "critical" }))).toBe(false);
  });

  it("is false for indeterminate — the verdict already says it is unknown", () => {
    expect(spofVerdictIsPartial(impact({ severity: "info", notEvaluated: ["ceph"] }))).toBe(false);
  });
});

describe("describeDimensions", () => {
  it("names the four known dimension codes", () => {
    expect(describeDimensions(["quorum", "ceph", "tunnels", "guest-connectivity"])).toBe(
      "corosync quorum, Ceph networks, WireGuard tunnels, guest connectivity",
    );
  });

  it("passes an unknown code through rather than dropping it", () => {
    expect(describeDimensions(["something-new"])).toBe("something-new");
  });
});

describe("spofAffectedRefs", () => {
  it("collects guests then VLANs, deduped", () => {
    const refs = spofAffectedRefs(
      impact({
        disconnectedGuests: ["guest:pve1:100", "guest:pve1:101"],
        strandedVlans: ["vlan:pve1:vmbr0.20", "guest:pve1:100"],
      }),
    );
    expect(refs).toEqual(["guest:pve1:100", "guest:pve1:101", "vlan:pve1:vmbr0.20"]);
  });

  it("excludes mgmtPathLoss, which carries node names rather than refs", () => {
    expect(spofAffectedRefs(impact({ mgmtPathLoss: ["pve2"] }))).toEqual([]);
  });
});
