// Safe, typed readers over an EntityDetail's `fields` (a plain
// `Record<string, unknown>` on the wire — docs/api.md doesn't type its
// contents beyond "the resolved entity's canonical fields"). Every editor
// prefills its form from these instead of casting `fields[key] as string`
// at each call site (CLAUDE.md: "no unchecked casts").
export function fieldStr(fields: Record<string, unknown> | undefined, key: string, fallback = ""): string {
  const v = fields?.[key];
  return typeof v === "string" ? v : fallback;
}

export function fieldNum(fields: Record<string, unknown> | undefined, key: string, fallback = 0): number {
  const v = fields?.[key];
  if (typeof v === "number") return v;
  if (typeof v === "string" && v !== "" && !Number.isNaN(Number(v))) return Number(v);
  return fallback;
}

export function fieldBool(fields: Record<string, unknown> | undefined, key: string, fallback = false): boolean {
  const v = fields?.[key];
  if (typeof v === "boolean") return v;
  if (typeof v === "string") return v === "true";
  return fallback;
}

/** Reads a comma/space-joined string field (e.g. "portNames") back into an
 * array — internal/inventory's fieldMap() joins slices with a helper that
 * favors comma/space separation; when `fields` already carries a real JSON
 * array (the more likely shape for the actual /inventory/{ref} response,
 * per that route's doc comment), that is used directly instead. */
export function fieldStrArray(fields: Record<string, unknown> | undefined, key: string): string[] {
  const v = fields?.[key];
  if (Array.isArray(v)) {
    return v.filter((x): x is string => typeof x === "string");
  }
  if (typeof v === "string" && v.trim() !== "") {
    return v.split(/[,\s]+/).filter(Boolean);
  }
  return [];
}
