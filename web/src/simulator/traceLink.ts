// "Trace path" from the map (T-504 AC5): builds the /tools URL that
// pre-fills one endpoint of the Path simulator from an entity the user
// right-clicked or opened in the inspector. Reuses the exact same URL
// state (urlState.ts) the simulator page itself reads on mount and the
// "Copy link" affordance writes — one mechanism serves sharing (AC4) and
// map pre-fill (AC5) alike.
//
// Only `guest-nic`-kind entities are traceable: docs/api.md's EndpointSpec
// only resolves a guest NIC ref, a literal IP, or "external" — a plain
// guest/bridge/bond/etc. topology node has no direct mapping onto one of
// those three forms (a Guest entity, in particular, has no traversable
// inventory edge to its own NIC — see internal/inventory/link.go's
// EdgeKind vocabulary — so it is deliberately not offered here rather
// than guessing which of a multi-NIC guest's NICs was meant).
import type { SimEndpointSpec } from "../api/types";
import { simUrlStatePath } from "./urlState";

const SIMULATOR_PATH = "/tools";

export function isTraceableEntityKind(kind: string): boolean {
  return kind === "guest-nic";
}

function endpointFor(kind: string, ref: string): SimEndpointSpec | undefined {
  return isTraceableEntityKind(kind) ? { kind: "guest-nic", ref } : undefined;
}

/** "Trace path from here": pre-fills `ref` as the source, leaving the
 * destination for the user to pick in the simulator's own picker (covers
 * guest->guest and guest->IP once the user fills in the other side). */
export function traceFromPath(kind: string, ref: string): string | undefined {
  const src = endpointFor(kind, ref);
  return src ? simUrlStatePath(SIMULATOR_PATH, { src }) : undefined;
}

/** "Trace path to here": pre-fills `ref` as the destination (covers
 * IP->guest once the user types a source IP in the picker). */
export function traceToPath(kind: string, ref: string): string | undefined {
  const dst = endpointFor(kind, ref);
  return dst ? simUrlStatePath(SIMULATOR_PATH, { dst }) : undefined;
}

/** "Trace path to external": pre-fills both sides at once (source = this
 * entity, destination = external) so guest->external needs exactly one
 * map action, matching AC5's "pre-fills and runs" — the simulator page
 * auto-runs as soon as both endpoints are present on mount. */
export function traceToExternalPath(kind: string, ref: string): string | undefined {
  const src = endpointFor(kind, ref);
  return src ? simUrlStatePath(SIMULATOR_PATH, { src, dst: { kind: "external" } }) : undefined;
}
