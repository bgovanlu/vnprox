// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { groupCommentsByOp } from "./commentGrouping";
import type { ChangesetComment } from "../api/types";

function comment(overrides: Partial<ChangesetComment>): ChangesetComment {
  return { id: "c1", author: "alice", body: "x", createdAt: 1, ...overrides };
}

describe("groupCommentsByOp", () => {
  it("groups changeset-level comments (no opId) under the empty-string key", () => {
    const groups = groupCommentsByOp([comment({ id: "c1" }), comment({ id: "c2" })]);
    expect(groups.get("")).toHaveLength(2);
    expect(groups.size).toBe(1);
  });

  it("groups per-op comments under their opId, preserving insertion order within a group", () => {
    const groups = groupCommentsByOp([
      comment({ id: "c1", opId: "op-a", body: "first" }),
      comment({ id: "c2", opId: "op-b" }),
      comment({ id: "c3", opId: "op-a", body: "second" }),
    ]);
    expect(groups.size).toBe(2);
    const opA = groups.get("op-a");
    expect(opA?.map((c) => c.body)).toEqual(["first", "second"]);
    expect(groups.get("op-b")).toHaveLength(1);
  });

  it("returns an empty map for no comments", () => {
    expect(groupCommentsByOp([]).size).toBe(0);
  });
});
