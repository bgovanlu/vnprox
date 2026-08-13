// T-2802: the hosted demo's per-visitor state surface.
//
// A public demo refuses every mutating route at the edge — including the
// /layouts routes the SPA normally persists UI state through. The edge
// serves a visitor-scoped scratch surface of its own instead
// (internal/publicdemo/visitorapi.go), and this module is its client.
//
// It does NOT go through api/client.ts's apiFetch: that wrapper is pinned to
// `/api/v1` (API_BASE) and to the documented error envelope of the product's
// API. This surface is deliberately not part of that API — it is not in
// docs/openapi.json, it never reaches the daemon, and giving it a base-path
// escape hatch inside apiFetch would make every other caller's paths one
// typo away from leaving the API. It reuses ApiError so callers branch on
// `.status` uniformly.
import { ApiError } from "../api/client";

/** GET /demo/visitor/session's body. */
export interface VisitorSession {
  publicDemo: boolean;
  visitor: string;
  caps: {
    requestBurst: number;
    maxStateBytes: number;
    maxStateEntries: number;
  };
}

const VISITOR_BASE = "/demo/visitor";

async function visitorFetch<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${VISITOR_BASE}${path}`, { credentials: "include", ...init });
  } catch (cause) {
    throw new ApiError(0, "network_error", "could not reach the demo edge", {
      cause: cause instanceof Error ? cause.message : String(cause),
    });
  }
  if (!res.ok) {
    throw new ApiError(res.status, "visitor_state_error", res.statusText || `request failed with status ${String(res.status)}`);
  }
  const text = await res.text();
  return (text ? (JSON.parse(text) as T) : (undefined as T));
}

/** GET /demo/visitor/session.
 *
 * Resolves to null on any failure — including the 404 a normal daemon
 * serves, which is exactly how the SPA learns it is NOT in a public demo.
 * There is deliberately no config flag and no /health field for this: a
 * route that only the edge serves cannot be got wrong by a daemon that has
 * no edge. */
export async function fetchVisitorSession(): Promise<VisitorSession | null> {
  try {
    const session = await visitorFetch<VisitorSession>("/session");
    return session.publicDemo ? session : null;
  } catch {
    return null;
  }
}

/** GET /demo/visitor/state/{name}. Resolves to null when this visitor has
 * nothing saved under the key (the edge answers 404), which callers treat
 * as "start fresh" — the same convention api/layouts.ts documents for a
 * never-saved layout. */
export async function fetchVisitorState<T>(name: string): Promise<T | null> {
  try {
    const body = await visitorFetch<{ name: string; state: T }>(`/state/${encodeURIComponent(name)}`);
    return body.state;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

/** PUT /demo/visitor/state/{name}. Rejects with an ApiError whose status is
 * 413 when this visitor is past its own scratch cap — that is a cap on the
 * visitor, never on the instance, so the right UI response is to tell this
 * visitor their place may not be remembered, not to report an outage. */
export async function saveVisitorState(name: string, state: unknown): Promise<void> {
  await visitorFetch(`/state/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ state }),
  });
}
