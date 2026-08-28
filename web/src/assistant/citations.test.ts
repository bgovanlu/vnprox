// SPDX-License-Identifier: Apache-2.0

// T-2808 AC2, at the unit the render path depends on: an answer that cites
// nothing real never becomes a renderable value in the first place.
import { describe, expect, it } from "vitest";
import { evaluateReply, parseModelReply, resolveCitations } from "./citations";
import type { ToolRun } from "./tools";

const topologyRun: ToolRun = {
  tool: "topology.get",
  status: "ok",
  entities: [{ ref: "bridge:pve1:vmbr0", label: "vmbr0 (bridge)", href: "/topology" }],
  summary: { nodeCount: 1 },
};

const refusedIpamRun: ToolRun = {
  tool: "ipam.subnets.list",
  status: "refused",
  note: "the server refused this surface for your session (403)",
  entities: [],
  summary: undefined,
};

function reply(answer: string, citations: { tool: string; ref: string }[]): string {
  return JSON.stringify({ answer, citations });
}

describe("parseModelReply", () => {
  it("accepts the reply contract", () => {
    expect(parseModelReply(reply("hello", [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }]))).toEqual({
      answer: "hello",
      citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
      proposals: [],
    });
  });

  it("unwraps a ```json fence", () => {
    const fenced = "```json\n" + reply("hello", [{ tool: "topology.get", ref: "x" }]) + "\n```";
    expect(parseModelReply(fenced)?.answer).toBe("hello");
  });

  it("refuses prose, an empty answer, and non-JSON", () => {
    expect(parseModelReply("Sure! vmbr0 looks fine to me.")).toBeUndefined();
    expect(parseModelReply(reply("   ", [{ tool: "topology.get", ref: "x" }]))).toBeUndefined();
    expect(parseModelReply("")).toBeUndefined();
  });
});

describe("resolveCitations", () => {
  it("resolves a citation that names a tool that ran and a ref it returned", () => {
    const { resolved, unresolved } = resolveCitations(
      [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
      [topologyRun],
    );
    expect(unresolved).toHaveLength(0);
    expect(resolved).toEqual([
      { tool: "topology.get", ref: "bridge:pve1:vmbr0", label: "vmbr0 (bridge)", href: "/topology" },
    ]);
  });

  it("does not resolve a fabricated ref, a fabricated tool, or a ref from a refused surface", () => {
    const { resolved, unresolved } = resolveCitations(
      [
        { tool: "topology.get", ref: "bridge:pve1:vmbr9-does-not-exist" },
        { tool: "totally.invented", ref: "bridge:pve1:vmbr0" },
        { tool: "ipam.subnets.list", ref: "10.0.0.0/24" },
      ],
      [topologyRun, refusedIpamRun],
    );
    expect(resolved).toHaveLength(0);
    expect(unresolved).toHaveLength(3);
  });

  it("does not credit a ref to the wrong tool", () => {
    // The ref is real, but findings.list did not return it.
    const { resolved } = resolveCitations([{ tool: "findings.list", ref: "bridge:pve1:vmbr0" }], [topologyRun]);
    expect(resolved).toHaveLength(0);
  });
});

describe("evaluateReply — AC2", () => {
  it("withholds an answer whose every citation is fabricated, and does not carry its text", () => {
    const result = evaluateReply(reply("vmbr9 is misconfigured", [{ tool: "topology.get", ref: "bridge:pve1:vmbr9" }]), [
      topologyRun,
    ]);

    expect(result.status).toBe("withheld");
    // The strongest form of "is not rendered": the answer text is not in
    // the value a renderer receives, so no renderer can show it.
    expect(JSON.stringify(result)).not.toContain("vmbr9 is misconfigured");
  });

  it("withholds an answer with an empty citation list", () => {
    expect(evaluateReply(reply("trust me", []), [topologyRun]).status).toBe("withheld");
  });

  it("withholds an unparsable reply", () => {
    const result = evaluateReply("vmbr0 is fine, I promise.", [topologyRun]);
    expect(result.status).toBe("withheld");
    if (result.status === "withheld") {
      expect(result.reason).toBe("unparsable-reply");
    }
  });

  it("CONTROL: an answer with one resolving citation is returned with its text", () => {
    const result = evaluateReply(
      reply("vmbr0 is up", [
        { tool: "topology.get", ref: "bridge:pve1:vmbr0" },
        { tool: "topology.get", ref: "bridge:pve1:invented" },
      ]),
      [topologyRun],
    );
    expect(result.status).toBe("answered");
    if (result.status === "answered") {
      expect(result.answer).toBe("vmbr0 is up");
      expect(result.citations).toHaveLength(1);
      // The fabricated one is reported rather than quietly discarded.
      expect(result.unresolved).toEqual([{ tool: "topology.get", ref: "bridge:pve1:invented" }]);
    }
  });
});
