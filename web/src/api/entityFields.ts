// Readers for GET /inventory/{ref}'s `fields` map.
//
// `fields` is not a designed API shape: internal/topology.Detail builds it
// with `json.Marshal(entity)` over the inventory struct, so the keys are Go
// field names (`Addresses`, `ChassisName`, `TaggedVLANs`) and the values are
// Go types (a []string is a JSON array, a []int is an array of numbers).
// Nothing on the TypeScript side types it — `EntityDetail.fields` is
// `Record<string, unknown>` — so a wrong key or a wrong type guard produces
// no error at all, just a silent `undefined` that the caller falls back on.
//
// That is not hypothetical. Two shipped SDN wizards read this map, and both
// read it wrongly for months (T-2108):
//
//   - peerSuggest.ts looked for `addresses` (a string). The VXLAN wizard's
//     peer auto-suggest therefore suggested nothing, ever, leaving its Next
//     button permanently disabled behind "An address is required."
//   - useLldpTrunkCheck.ts looked for `chassisName`, `portId` and a
//     comma-joined `taggedVlans`. Every LLDP neighbor came back with an
//     empty trunk list, so the VLAN wizard warned that the chosen VID was
//     not trunked on every neighbor — naming an empty switch and port.
//
// Both had passing unit tests, because both fixtures invented the shape the
// code expected. These readers exist so there is one place that knows the
// real shape, tolerant of case and of the comma-joined form a fieldMap-
// derived value would use, and one place to test it.

/** Reads the first present key, case-tolerantly (`Name` then `name`). */
function pick(fields: Record<string, unknown>, name: string): unknown {
  const lower = name.charAt(0).toLowerCase() + name.slice(1);
  return fields[name] ?? fields[lower] ?? fields[name.toLowerCase()];
}

/** A string field, or undefined if absent or of another type. */
export function readString(fields: Record<string, unknown>, name: string): string | undefined {
  const v = pick(fields, name);
  return typeof v === "string" ? v : undefined;
}

/** A list-of-strings field: the JSON array the API sends, or a comma-joined
 * string. Non-string array entries are dropped rather than coerced. */
export function readStringList(fields: Record<string, unknown>, name: string): string[] {
  const v = pick(fields, name);
  if (Array.isArray(v)) return v.filter((e): e is string => typeof e === "string");
  if (typeof v === "string") return v.split(",").map((s) => s.trim()).filter((s) => s.length > 0);
  return [];
}

/** A list-of-numbers field: the JSON array the API sends, or a comma-joined
 * string. Anything that is not a finite number is dropped. */
export function readNumberList(fields: Record<string, unknown>, name: string): number[] {
  const v = pick(fields, name);
  const raw: unknown[] = Array.isArray(v) ? v : typeof v === "string" ? v.split(",") : [];
  const out: number[] = [];
  for (const entry of raw) {
    if (typeof entry === "number") {
      if (Number.isFinite(entry)) out.push(entry);
      continue;
    }
    // A blank segment means "nothing here" — `"100, ,200"` is two values, not
    // three. Number("") is 0, so converting first would smuggle a zero in.
    if (typeof entry !== "string" || entry.trim() === "") continue;
    const n = Number(entry.trim());
    if (Number.isFinite(n)) out.push(n);
  }
  return out;
}
