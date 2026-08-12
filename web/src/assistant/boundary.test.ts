// T-2808 AC5: no apply path is reachable from the assistant.
//
// The guarantee itself is INHERITED, not re-implemented. T-2705 made the
// MCP surface's stage-only property a compile-time fact
// (internal/mcp/stageonly.go: a placeholder type whose method set is
// exactly the stage-only verbs is asserted to satisfy the change-engine
// seam, so widening that seam stops the package building). Nothing in this
// card weakens or re-derives that.
//
// What this file adds is the assistant's own half of the same boundary, in
// the shape T-2805's internal/presence/deps_test.go established: scan the
// REAL module's source and fail if a forbidden dependency appears, with a
// non-vacuity guard proving the scan is reading what it thinks it is.
//
// The assistant is a browser module, so its equivalent of "the type it is
// handed has no Apply method" is "the module never reaches the api
// functions that apply". Those functions exist, are exported from
// web/src/api/changesets.ts, and are used elsewhere in the app — so their
// absence here is a property of this module, not an accident of the
// codebase.
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const ASSISTANT_DIR = resolve(__dirname);
const CHANGESETS_API = resolve(__dirname, "..", "api", "changesets.ts");

/** The verbs that move a changeset past staging. Spelled exactly as
 * web/src/api/changesets.ts exports them. */
const FORBIDDEN_CALLS = [
  "applyChangeset",
  "confirmChangeset",
  "rollbackChangeset",
  "discardChangeset",
  "reviewApproveChangeset",
];

/** The one write the assistant is allowed: staging a draft. */
const ALLOWED_WRITE = "createChangeset";

function sourceFiles(): { name: string; text: string }[] {
  return readdirSync(ASSISTANT_DIR)
    .filter((f) => (f.endsWith(".ts") || f.endsWith(".tsx")) && !f.endsWith(".test.ts") && !f.endsWith(".test.tsx"))
    .map((name) => ({ name, text: readFileSync(join(ASSISTANT_DIR, name), "utf8") }));
}

describe("AC5 — the assistant cannot reach an apply path", () => {
  it("no assistant module names an apply/confirm/rollback/discard/approve call", () => {
    const files = sourceFiles();
    // Non-vacuity: the scan found a real module, not an empty directory.
    expect(files.length).toBeGreaterThanOrEqual(6);

    for (const file of files) {
      for (const verb of FORBIDDEN_CALLS) {
        expect(
          file.text.includes(verb),
          `web/src/assistant/${file.name} references ${verb}. The assistant stages and hands off; ` +
            "applying is a human's decision through the change engine, and that is the whole point of the surface.",
        ).toBe(false);
      }
    }
  });

  it("NON-VACUITY: those verbs exist and are exported by the api module this scan is about", () => {
    const api = readFileSync(CHANGESETS_API, "utf8");
    for (const verb of FORBIDDEN_CALLS) {
      expect(
        new RegExp(`export (async )?function ${verb}\\b`).test(api),
        `${verb} is not exported by web/src/api/changesets.ts — the scan above is watching for a name that no longer exists`,
      ).toBe(true);
    }
  });

  it("NON-VACUITY: the assistant does reach the STAGE verb, so the scan sees this module's imports", () => {
    const files = sourceFiles();
    const stagingReferences = files.filter((f) => f.text.includes(ALLOWED_WRITE));
    expect(
      stagingReferences.length,
      "no assistant module references createChangeset — either staging was removed (AC4) or this scan is reading the wrong files",
    ).toBeGreaterThan(0);
  });

  it("the panel exposes no apply-shaped control", () => {
    const panel = readFileSync(join(ASSISTANT_DIR, "AssistantPanel.tsx"), "utf8");
    // CONTROL: the staging control is there to be found.
    expect(panel).toContain("Stage for review");
    expect(panel).not.toMatch(/>\s*(Apply|Confirm|Roll back|Rollback)\b/);
  });
});
