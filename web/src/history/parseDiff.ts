// SPDX-License-Identifier: Apache-2.0

// Pure unified-diff line classification, in its own module (not
// DiffView.tsx) so the component file only exports components
// (react-refresh) and the parser is directly unit-testable.

export type DiffLineKind = "header" | "hunk" | "add" | "remove" | "context";

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
}

/** Classifies each line of a unified diff (as produced by the server's
 * differ: `--- a`, `+++ b`, `@@ -l,c +l,c @@` hunks, then +/-/space
 * lines). Unknown-prefixed lines (shouldn't happen in well-formed input)
 * fall back to context so nothing is ever dropped from view. */
export function parseUnifiedDiff(unified: string): DiffLine[] {
  if (!unified) {
    return [];
  }
  const lines = unified.split("\n");
  // A trailing empty element from a final newline is rendering noise.
  if (lines.length > 0 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines.map((text): DiffLine => {
    if (text.startsWith("--- ") || text.startsWith("+++ ")) {
      return { kind: "header", text };
    }
    if (text.startsWith("@@")) {
      return { kind: "hunk", text };
    }
    if (text.startsWith("+")) {
      return { kind: "add", text };
    }
    if (text.startsWith("-")) {
      return { kind: "remove", text };
    }
    return { kind: "context", text };
  });
}
