// SPDX-License-Identifier: Apache-2.0

// T-3005: the rollout read model.
//
// The assertion this file exists for is the negative one: a node the server
// did not account for renders as `unknown`, never as `pending` and never as
// `done`. This arc has shipped that bug four times; these cases are the
// tripwire.
import { describe, expect, it } from "vitest";
import type { Changeset, StagedApplyState } from "../api/types";
import { deriveRollout, stagedApplyStateOf } from "./rolloutState";

function changeset(stage?: Partial<StagedApplyState>, rest: Partial<Changeset> = {}): Changeset {
  return {
    id: "cs1",
    title: "Widen vmbr0",
    author: "root@pam",
    status: "applying",
    ops: [],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...(stage
      ? {
          applyStage: {
            state: "canary_hold",
            strategy: { mode: "canary", gate: "manual", canaryNodes: ["pve1"], holdForSec: 60 },
            appliedNodes: ["pve1"],
            pendingNodes: ["pve2", "pve3"],
            holdStartedAt: 1_700_000_100,
            holdDeadline: 1_700_000_160,
            confirmDeadline: 1_700_000_220,
            ...stage,
          },
        }
      : {}),
    ...rest,
  };
}

describe("stagedApplyStateOf", () => {
  it("is undefined for an ordinary apply with no stage", () => {
    expect(stagedApplyStateOf(changeset())).toBeUndefined();
    expect(deriveRollout(changeset())).toBeUndefined();
  });
});

describe("deriveRollout — the happy hold", () => {
  it("reports applied and pending nodes with their real states", () => {
    const view = deriveRollout(changeset({}));
    expect(view?.paused).toBe(true);
    expect(view?.recognized).toBe(true);
    expect(view?.canContinue).toBe(true);
    expect(view?.nodesUnknown).toBe(false);
    expect(view?.nodes).toEqual([
      { node: "pve1", status: "done" },
      { node: "pve2", status: "pending" },
      { node: "pve3", status: "pending" },
    ]);
    expect(view?.holdDeadline).toBe(1_700_000_160);
    expect(view?.confirmDeadline).toBe(1_700_000_220);
  });

  it("explains a manual gate as waiting for the operator", () => {
    expect(deriveRollout(changeset({}))?.gateExplanation).toMatch(/waiting for you/i);
  });

  it("explains an auto gate as waiting for the hold and the evidence", () => {
    const view = deriveRollout(changeset({ strategy: { mode: "canary", gate: "auto", canaryNodes: ["pve1"] } }));
    expect(view?.gateExplanation).toMatch(/healthy/i);
  });

  it("does not assume a gate the server did not report", () => {
    const view = deriveRollout(changeset({ strategy: { mode: "canary", canaryNodes: ["pve1"] } }));
    expect(view?.gateExplanation).toMatch(/was not reported/i);
  });
});

describe("deriveRollout — promoting", () => {
  it("is recognized, not paused, and offers no Continue", () => {
    const view = deriveRollout(changeset({ state: "promoting" }));
    expect(view?.recognized).toBe(true);
    expect(view?.paused).toBe(false);
    expect(view?.promoting).toBe(true);
    expect(view?.canContinue).toBe(false);
  });
});

describe("deriveRollout — nothing unknown renders as definite", () => {
  it("marks a plan node the stage accounted for nowhere as unknown", () => {
    const view = deriveRollout(
      changeset(
        { appliedNodes: ["pve1"], pendingNodes: ["pve2"] },
        {
          plan: {
            steps: [
              { kind: "reload", node: "pve1", summary: "" },
              { kind: "reload", node: "pve2", summary: "" },
              { kind: "reload", node: "pve3", summary: "" },
            ],
          },
        },
      ),
    );
    const pve3 = view?.nodes.find((n) => n.node === "pve3");
    expect(pve3?.status).toBe("unknown");
    expect(pve3?.note).toMatch(/did not report/i);
  });

  it("marks a node reported as both applied and pending as unknown", () => {
    const view = deriveRollout(changeset({ appliedNodes: ["pve1"], pendingNodes: ["pve1", "pve2"] }));
    expect(view?.nodes.find((n) => n.node === "pve1")?.status).toBe("unknown");
    expect(view?.nodes.find((n) => n.node === "pve2")?.status).toBe("pending");
  });

  it("marks a canary node the stage never mentioned as unknown", () => {
    const view = deriveRollout(
      changeset({
        appliedNodes: [],
        pendingNodes: ["pve2"],
        strategy: { mode: "canary", gate: "manual", canaryNodes: ["pve1"] },
      }),
    );
    expect(view?.nodes.find((n) => n.node === "pve1")?.status).toBe("unknown");
  });

  it("flags an entirely absent node report rather than rendering an empty list", () => {
    const view = deriveRollout(changeset({ appliedNodes: undefined, pendingNodes: undefined }));
    expect(view?.nodesUnknown).toBe(true);
  });

  it("does not treat a non-array node field as an empty list", () => {
    // A malformed/absent field is "we were not told", not "no nodes".
    const view = deriveRollout(changeset({ appliedNodes: undefined, pendingNodes: ["pve2"] }));
    expect(view?.nodesUnknown).toBe(false);
    expect(view?.nodes.find((n) => n.node === "pve2")?.status).toBe("pending");
    // pve1 is only known as a canary node — unknown, not silently "done".
    expect(view?.nodes.find((n) => n.node === "pve1")?.status).toBe("unknown");
  });

  it("reports an unrecognised stage state as unrecognised and offers no Continue", () => {
    const view = deriveRollout(changeset({ state: "some_future_state" }));
    expect(view?.recognized).toBe(false);
    expect(view?.paused).toBe(false);
    expect(view?.promoting).toBe(false);
    expect(view?.canContinue).toBe(false);
    expect(view?.headline).toMatch(/does not recognise/i);
  });
});
