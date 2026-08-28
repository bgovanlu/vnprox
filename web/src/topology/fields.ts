// SPDX-License-Identifier: Apache-2.0

// Pure field-flattening logic for the inspector's Fields tab, kept out of
// InspectorPanel.tsx so the component file only exports a component
// (react-refresh) and this stays directly unit-testable.

function stringifyFieldValue(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

/**
 * Flattens an EntityDetail's `fields` for display, applying the tri-state
 * boolean rule (docs/api.md / api/types.ts's EntityDetail doc comment): a
 * `<Field>Set: false` companion means the source never reported `<Field>`,
 * so its value renders as "unknown" — never as a fabricated `false`. The
 * `*Set` companion keys themselves are metadata, not entity fields, so they
 * are folded into that rendering rather than listed as rows of their own
 * (only when their base field actually exists — a field that merely ends in
 * "Set" is left alone).
 */
export function fieldRows(fields: Record<string, unknown>): [string, string][] {
  return Object.entries(fields)
    .filter(([k]) => !(k.endsWith("Set") && k.slice(0, -"Set".length) in fields))
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .map(([k, v]) => [k, fields[`${k}Set`] === false ? "unknown" : stringifyFieldValue(v)]);
}
