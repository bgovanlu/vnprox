// Pure param-input validation/parsing for the blueprint instantiate param
// form (T-603 AC4: "Param validation: bad CIDR/VID rejected at the
// form"). Framework-free (no React import) so it's directly Vitest-able;
// ParamForm.tsx is the only consumer. Mirrors the backend's own
// validation (internal/blueprint/validate.go's validateParamValue) so the
// form rejects the same inputs the server would — this module is the
// defense-in-depth's *first* line, not the only one (the server still
// validates on submit).
import type { BlueprintParamDef, BlueprintParamValue } from "../api/types";

const MIN_VID = 1;
const MAX_VID = 4094;

export interface ParamValidationResult {
  valid: boolean;
  error?: string;
  /** The parsed, typed value — only present when valid. */
  value?: BlueprintParamValue;
}

function isValidCIDR(raw: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/.exec(raw.trim());
  if (!m) return false;
  const octets = [m[1], m[2], m[3], m[4]];
  if (!octets.every((o) => Number(o) >= 0 && Number(o) <= 255)) return false;
  const prefix = Number(m[5]);
  return prefix >= 0 && prefix <= 32;
}

function isValidIPv4(raw: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(raw.trim());
  if (!m) return false;
  return [m[1], m[2], m[3], m[4]].every((o) => Number(o) >= 0 && Number(o) <= 255);
}

/** Parses a comma/whitespace-separated list of integers (the param form's
 * plain-text input for a `vidList`/`nodeList` field, e.g. "10, 20, 30"). */
function splitList(raw: string): string[] {
  return raw
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/** Validates and parses one param form field's raw string input against
 * its ParamDef.type. An empty raw value is valid iff the param isn't
 * required (an omitted optional param falls back to its Default
 * server-side) — required-but-empty is reported as its own error rather
 * than a type mismatch, so the form can distinguish "fill this in" from
 * "that's not a valid CIDR". */
export function validateParamInput(def: BlueprintParamDef, raw: string): ParamValidationResult {
  const trimmed = raw.trim();
  if (trimmed === "") {
    if (def.required) {
      return { valid: false, error: `${def.label ?? def.name} is required` };
    }
    return { valid: true };
  }

  switch (def.type) {
    case "string":
    case "iface":
      return { valid: true, value: trimmed };

    case "bool": {
      const lowered = trimmed.toLowerCase();
      if (lowered !== "true" && lowered !== "false") {
        return { valid: false, error: `${def.label ?? def.name} must be true or false` };
      }
      return { valid: true, value: lowered === "true" };
    }

    case "int": {
      const n = Number(trimmed);
      if (!Number.isInteger(n)) {
        return { valid: false, error: `${def.label ?? def.name} must be a whole number` };
      }
      return { valid: true, value: n };
    }

    case "vid": {
      const n = Number(trimmed);
      if (!Number.isInteger(n) || n < MIN_VID || n > MAX_VID) {
        return { valid: false, error: `${def.label ?? def.name} must be a VLAN id between ${String(MIN_VID)} and ${String(MAX_VID)}` };
      }
      return { valid: true, value: n };
    }

    case "vidList": {
      const parts = splitList(trimmed);
      const vids: number[] = [];
      for (const p of parts) {
        const n = Number(p);
        if (!Number.isInteger(n) || n < MIN_VID || n > MAX_VID) {
          return { valid: false, error: `"${p}" is not a valid VLAN id (${String(MIN_VID)}-${String(MAX_VID)})` };
        }
        vids.push(n);
      }
      if (vids.length === 0) {
        return { valid: false, error: `${def.label ?? def.name} must list at least one VLAN id` };
      }
      return { valid: true, value: vids };
    }

    case "nodeList": {
      const parts = splitList(trimmed);
      if (parts.length === 0) {
        return { valid: false, error: `${def.label ?? def.name} must list at least one node` };
      }
      return { valid: true, value: parts };
    }

    case "cidr":
      if (!isValidCIDR(trimmed)) {
        return { valid: false, error: `${def.label ?? def.name} must be a CIDR address, e.g. 192.168.1.10/24` };
      }
      return { valid: true, value: trimmed };

    case "ip":
      if (!isValidIPv4(trimmed)) {
        return { valid: false, error: `${def.label ?? def.name} must be an IP address, e.g. 192.168.1.1` };
      }
      return { valid: true, value: trimmed };

    default:
      return { valid: false, error: `${def.label ?? def.name} has an unknown param type` };
  }
}

/** Validates every param in defs against a raw string-keyed form-values
 * map, returning per-field errors (keyed by param name) and, iff every
 * field is valid, the fully parsed/typed params object ready to send as
 * InstantiateBlueprintRequest.params. */
export function validateParamForm(
  defs: BlueprintParamDef[],
  rawValues: Record<string, string>,
): { errors: Record<string, string>; params?: Record<string, BlueprintParamValue> } {
  const errors: Record<string, string> = {};
  const params: Record<string, BlueprintParamValue> = {};

  for (const def of defs) {
    const raw = rawValues[def.name] ?? "";
    const result = validateParamInput(def, raw);
    if (!result.valid) {
      errors[def.name] = result.error ?? "invalid value";
      continue;
    }
    if (result.value !== undefined) {
      params[def.name] = result.value;
    }
  }

  if (Object.keys(errors).length > 0) {
    return { errors };
  }
  return { errors, params };
}

/** Default raw-string form value for a param — Default rendered as text
 * (arrays joined with ", "), or "" if it has none. */
export function defaultRawValue(def: BlueprintParamDef): string {
  if (def.default === undefined) return "";
  if (Array.isArray(def.default)) return def.default.join(", ");
  return String(def.default);
}
