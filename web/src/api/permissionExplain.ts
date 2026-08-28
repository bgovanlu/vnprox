// SPDX-License-Identifier: Apache-2.0

// T-4105's "why can't I?" answer, read back out of an ApiError. The daemon
// already attaches `details.explanation` to every `403 forbidden` a
// capability-gated route produces (internal/auth's RequireCap, see
// docs/api.md's error-envelope conventions) — this module is the one place
// that turns that untyped `details: Record<string, unknown>` back into the
// typed PermissionExplanation shape and renders it as prose, so no caller
// re-implements the guard or the wording.
import { ApiError } from "./client";
import type { PermissionExplanation, PermissionRequirement } from "./types";

function isRequirement(value: unknown): value is PermissionRequirement {
  if (typeof value !== "object" || value === null) return false;
  const r = value as Record<string, unknown>;
  return typeof r.privilege === "string" && typeof r.path === "string" && typeof r.confirmed === "boolean";
}

function isExplanation(value: unknown): value is PermissionExplanation {
  if (typeof value !== "object" || value === null) return false;
  const e = value as Record<string, unknown>;
  if (typeof e.capability !== "string" || typeof e.granted !== "boolean") return false;
  if (e.missing !== undefined && (!Array.isArray(e.missing) || !e.missing.every(isRequirement))) return false;
  if (e.reason !== undefined && typeof e.reason !== "string") return false;
  return true;
}

/** Extracts the explanation from a denial, if the server sent one. Only
 * ever populated on a `403 forbidden` from a `RequireCap`-gated route —
 * every other error (network failure, a differently-shaped 403 from some
 * other authorization layer, a 5xx) correctly yields `undefined`, so a
 * caller never renders a stale or mismatched explanation for the wrong
 * failure. */
export function permissionExplanation(err: unknown): PermissionExplanation | undefined {
  if (!(err instanceof ApiError) || err.status !== 403) return undefined;
  const raw = err.details?.explanation;
  return isExplanation(raw) ? raw : undefined;
}

/** One requirement, in prose: "Sys.Modify at /nodes/pve1" — or, when not
 * individually confirmed absent (capture's Sys.Console alongside a missing
 * Sys.Modify), qualified so the reader doesn't take it as certain. */
function requirementText(r: PermissionRequirement): string {
  const base = `${r.privilege} at ${r.path}`;
  return r.confirmed ? base : `${base} (also required; not confirmed missing)`;
}

/** Renders a PermissionExplanation as one or two sentences an operator can
 * act on — the reusable half of T-4105's UI surface, consumed by
 * RefusalNotice (settings/platformCommon.tsx) and any other 403 display
 * that wants the precise reason instead of the daemon's plain message
 * alone. Returns `undefined` for a granted capability (nothing to explain)
 * so callers can render conditionally without an extra check. */
export function explainPermission(exp: PermissionExplanation): string | undefined {
  if (exp.granted) return undefined;
  if (exp.reason !== undefined) return exp.reason;
  if (exp.missing !== undefined && exp.missing.length > 0) {
    const list = exp.missing.map(requirementText).join(", ");
    const plural = exp.missing.length > 1 ? "privileges" : "privilege";
    return `Missing PVE ${plural}: ${list}.`;
  }
  return undefined;
}
