// A two-rule inline formatter for help prose.
//
// Help bodies need to emphasise UI labels ("hit **Review & apply**") and
// set config keys in a monospace face (`[mcp] enabled`). Pulling a
// markdown renderer in for that would add a parser — and an HTML sink —
// to the locked frontend stack in docs/development.md for two rules'
// worth of formatting. This tokenizes instead, and HelpText renders the
// tokens as React elements, so no help string is ever handed to
// dangerouslySetInnerHTML.

export type InlineToken =
  | { readonly kind: "text"; readonly text: string }
  | { readonly kind: "bold"; readonly text: string }
  | { readonly kind: "code"; readonly text: string };

// Alternation order matters: `**` must be tried before a single `*` would
// be, and neither marker may span a newline (an unclosed marker should
// degrade to literal text rather than swallow the rest of a paragraph).
const MARKER = /\*\*([^*\n]+)\*\*|`([^`\n]+)`/g;

/** Splits help prose into renderable tokens. Text with no markers comes
 * back as a single "text" token, unchanged — including any stray `*` or
 * backtick that didn't form a complete pair. */
export function tokenizeInline(input: string): readonly InlineToken[] {
  const tokens: InlineToken[] = [];
  let cursor = 0;
  // A fresh regex per call: MARKER is /g, so a shared instance would carry
  // lastIndex between calls and skip matches non-deterministically.
  const re = new RegExp(MARKER.source, "g");
  let match: RegExpExecArray | null;
  while ((match = re.exec(input)) !== null) {
    if (match.index > cursor) {
      tokens.push({ kind: "text", text: input.slice(cursor, match.index) });
    }
    if (match[1] !== undefined) {
      tokens.push({ kind: "bold", text: match[1] });
    } else if (match[2] !== undefined) {
      tokens.push({ kind: "code", text: match[2] });
    }
    cursor = match.index + match[0].length;
  }
  if (cursor < input.length) {
    tokens.push({ kind: "text", text: input.slice(cursor) });
  }
  return tokens;
}

/** The text a reader actually sees, markers stripped. Used by search so a
 * query for "review apply" matches prose written as "**Review & apply**". */
export function plainText(input: string): string {
  return tokenizeInline(input)
    .map((t) => t.text)
    .join("");
}
