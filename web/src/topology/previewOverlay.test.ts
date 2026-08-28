// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import type {
  ChangesetPreviewResponse,
  PreviewChange,
  PreviewChangeKind,
} from "../api/changesetPreview";
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { buildPreviewScene, summarizePreviewScene, summarizeUnprojectable } from "./previewOverlay";

function node(id: string): TopologyNode {
  return {
    id,
    kind: id.split(":")[0] ?? "bridge",
    label: id.split(":")[2] ?? id,
    layer: "l2",
    nodeGroup: id.split(":")[1] ?? "pve1",
    status: "ok",
    badges: [],
  };
}

function edge(from: string, to: string): TopologyEdge {
  return { from, to, kind: "port-of", status: "ok", badges: [] };
}

function change(ref: string, kind: PreviewChangeKind, name?: string): PreviewChange {
  return {
    ref,
    kind: ref.split(":")[0] ?? "bridge",
    node: ref.split(":")[1],
    name: name ?? ref.split(":")[2],
    change: kind,
    fields: [],
  };
}

function preview(partial: Partial<ChangesetPreviewResponse>): ChangesetPreviewResponse {
  return {
    changesetId: "cs-1",
    topology: { nodes: [], edges: [], layers: ["phys", "l2", "sdn", "guest"], generatedAt: 100 },
    changes: [],
    unprojectable: [],
    bestEffort: true,
    generatedAt: 100,
    ...partial,
  };
}

function live(nodes: TopologyNode[], edges: TopologyEdge[] = []): TopologyResponse {
  return { nodes, edges, layers: ["phys", "l2", "sdn", "guest"], generatedAt: 90 };
}

describe("buildPreviewScene", () => {
  it("returns an empty scene when nothing is being previewed", () => {
    const scene = buildPreviewScene(undefined, live([node("bridge:pve1:vmbr0")]));
    expect(scene.nodes).toEqual([]);
    expect(scene.marks).toEqual([]);
    expect(summarizePreviewScene(scene)).toBe("This changeset does not change the map.");
  });

  it("marks an added entity that is already in the projected map", () => {
    const scene = buildPreviewScene(
      preview({
        topology: {
          nodes: [node("bridge:pve1:vmbr0"), node("bridge:pve1:vmbr9")],
          edges: [],
          layers: [],
          generatedAt: 100,
        },
        changes: [change("bridge:pve1:vmbr9", "added")],
      }),
      live([node("bridge:pve1:vmbr0")]),
    );

    expect(scene.addedCount).toBe(1);
    expect(scene.marks).toHaveLength(1);
    expect(scene.marks[0]?.nodeId).toBe("bridge:pve1:vmbr9");
    expect(scene.marks[0]?.change).toBe("added");
    // A preview mark is always attributed: this changeset is exactly what
    // would cause it.
    expect(scene.marks[0]?.attributed).toBe(true);
    expect(scene.marks[0]?.label).toContain("would be added");
  });

  // The load-bearing case: the projected map does NOT contain a removed
  // entity, so rendering it directly would show a deletion as an absence.
  it("draws a removed entity back onto the scene from the live map, marked removed", () => {
    const scene = buildPreviewScene(
      preview({
        topology: { nodes: [node("physnic:pve1:eno1")], edges: [], layers: [], generatedAt: 100 },
        changes: [change("bridge:pve1:vmbr0", "removed")],
      }),
      live(
        [node("physnic:pve1:eno1"), node("bridge:pve1:vmbr0")],
        [edge("physnic:pve1:eno1", "bridge:pve1:vmbr0")],
      ),
    );

    expect(scene.nodes.map((n) => n.id)).toContain("bridge:pve1:vmbr0");
    expect(scene.edges).toHaveLength(1);
    expect(scene.removedCount).toBe(1);
    expect(scene.marks.map((m) => m.nodeId)).toEqual(["bridge:pve1:vmbr0"]);
    expect(scene.offMap).toEqual([]);
  });

  it("never dangles an edge whose other endpoint is not on the scene", () => {
    const scene = buildPreviewScene(
      preview({
        topology: { nodes: [], edges: [], layers: [], generatedAt: 100 },
        changes: [change("bridge:pve1:vmbr0", "removed")],
      }),
      live(
        [node("bridge:pve1:vmbr0"), node("physnic:pve1:eno1")],
        [edge("physnic:pve1:eno1", "bridge:pve1:vmbr0")],
      ),
    );

    expect(scene.nodes.map((n) => n.id)).toEqual(["bridge:pve1:vmbr0"]);
    expect(scene.edges).toEqual([]);
  });

  it("counts a change it cannot paint instead of dropping it", () => {
    const scene = buildPreviewScene(
      preview({
        topology: { nodes: [], edges: [], layers: [], generatedAt: 100 },
        changes: [change("bridge:pve1:vmbr0", "removed")],
      }),
      undefined,
    );

    expect(scene.nodes).toEqual([]);
    expect(scene.marks).toEqual([]);
    expect(scene.offMap).toHaveLength(1);
    expect(scene.removedCount).toBe(1);
    expect(summarizePreviewScene(scene)).toContain("not on the current map");
  });

  it("summarizes added, removed and modified counts", () => {
    const scene = buildPreviewScene(
      preview({
        topology: {
          nodes: [node("bridge:pve1:vmbr9"), node("bridge:pve1:vmbr1")],
          edges: [],
          layers: [],
          generatedAt: 100,
        },
        changes: [
          change("bridge:pve1:vmbr9", "added"),
          change("bridge:pve1:vmbr1", "modified"),
          change("bridge:pve1:vmbr0", "removed"),
        ],
      }),
      live([node("bridge:pve1:vmbr0"), node("bridge:pve1:vmbr1")]),
    );

    expect(summarizePreviewScene(scene)).toBe("1 added · 1 removed · 1 modified.");
  });
});

describe("summarizeUnprojectable", () => {
  it("is empty when everything projected", () => {
    expect(summarizeUnprojectable(preview({}))).toBe("");
    expect(summarizeUnprojectable(undefined)).toBe("");
  });

  // The disclosure must survive: an op the server said it could not project
  // has to reach the operator, or the preview lies by omission.
  it("names every op the server could not project", () => {
    const line = summarizeUnprojectable(
      preview({
        unprojectable: [
          { op: "iface.raw.replace", target: "node:pve1:pve1", reason: "a raw file edit" },
          { op: "fw.rule.create", target: "fw-ruleset:pve1:cluster", reason: "no graph entity" },
        ],
      }),
    );
    expect(line).toContain("2 ops are not shown here");
    expect(line).toContain("iface.raw.replace");
    expect(line).toContain("fw.rule.create");
  });

  it("uses the singular for one op", () => {
    const line = summarizeUnprojectable(
      preview({ unprojectable: [{ op: "sdn.apply", reason: "applies staged config" }] }),
    );
    expect(line).toContain("1 op is not shown here");
  });
});
