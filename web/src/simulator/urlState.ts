// SPDX-License-Identifier: Apache-2.0

// Shareable simulation state (T-504 AC4: "Simulation URL round-trips
// (paste -> same result rendered)"). Encodes/decodes a SimulateRequest to
// and from the same `/tools` route's query string the Trace-path map
// action (traceLink.ts) also targets — one URL-state mechanism serves both
// AC4's sharing requirement and AC5's map pre-fill, per this codebase's
// established convention of encoding page state in the URL (T-107's
// layout/filter persistence, T-207's changeset-drawer active-id, T-505's
// ruleDeepLinkPath). Deliberately framework-free (no react-router import)
// so it's plain-URLSearchParams-testable without rendering anything.
import type { SimEndpointSpec, SimulateRequest } from "../api/types";

/** The subset of a SimulateRequest the URL can express: `src`/`dst` are
 * optional (an endpoint not yet picked simply has no query params for its
 * side), `proto`/`port` are always optional per the wire contract. */
export interface SimUrlState {
  src?: SimEndpointSpec;
  dst?: SimEndpointSpec;
  proto?: string;
  port?: number;
}

function encodeEndpoint(prefix: "src" | "dst", ep: SimEndpointSpec | undefined, params: URLSearchParams): void {
  if (!ep) return;
  params.set(`${prefix}Kind`, ep.kind);
  if (ep.kind === "guest-nic" && ep.ref) {
    params.set(`${prefix}Ref`, ep.ref);
  } else if (ep.kind === "ip" && ep.ip) {
    params.set(`${prefix}Ip`, ep.ip);
  }
}

function decodeEndpoint(prefix: "src" | "dst", params: URLSearchParams): SimEndpointSpec | undefined {
  const kind = params.get(`${prefix}Kind`);
  if (kind === "external") {
    return { kind: "external" };
  }
  if (kind === "guest-nic") {
    const ref = params.get(`${prefix}Ref`);
    return ref ? { kind: "guest-nic", ref } : undefined;
  }
  if (kind === "ip") {
    const ip = params.get(`${prefix}Ip`);
    return ip ? { kind: "ip", ip } : undefined;
  }
  return undefined;
}

/** Builds the query string for `state` — omits any field that isn't set,
 * so a partial state (e.g. only `src` picked so far) round-trips exactly
 * as partial. */
export function encodeSimState(state: SimUrlState): URLSearchParams {
  const params = new URLSearchParams();
  encodeEndpoint("src", state.src, params);
  encodeEndpoint("dst", state.dst, params);
  if (state.proto) {
    params.set("proto", state.proto);
  }
  if (state.port !== undefined && state.port > 0) {
    params.set("port", String(state.port));
  }
  return params;
}

/** Parses a query string (or an already-constructed URLSearchParams) back
 * into a SimUrlState. Malformed/incomplete endpoint params (e.g. `srcKind`
 * without the matching `srcRef`) decode to `undefined` for that side
 * rather than throwing — a corrupted/hand-edited URL should degrade to
 * "nothing pre-filled", never crash the page. */
export function decodeSimState(search: string | URLSearchParams): SimUrlState {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  const src = decodeEndpoint("src", params);
  const dst = decodeEndpoint("dst", params);
  const proto = params.get("proto") ?? undefined;
  const portStr = params.get("port");
  const port = portStr !== null ? Number(portStr) : undefined;
  return {
    src,
    dst,
    proto: proto !== undefined && proto !== "" ? proto : undefined,
    port: port !== undefined && Number.isFinite(port) && port > 0 ? port : undefined,
  };
}

/** Promotes a SimUrlState to a real SimulateRequest iff both endpoints are
 * known — the request the simulate query fires as soon as the URL (or the
 * picker) has enough to ask a question. */
export function simUrlStateToRequest(state: SimUrlState): SimulateRequest | undefined {
  if (!state.src || !state.dst) return undefined;
  return { src: state.src, dst: state.dst, proto: state.proto, port: state.port };
}

/** Builds a full `<basePath>?<query>` string for `state` — the one
 * function both the "Copy link" affordance and the Trace-path map action
 * (traceLink.ts) go through, so they can never drift from encodeSimState's
 * param names. */
export function simUrlStatePath(basePath: string, state: SimUrlState): string {
  const qs = encodeSimState(state).toString();
  return qs ? `${basePath}?${qs}` : basePath;
}
