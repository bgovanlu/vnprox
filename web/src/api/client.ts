// SPDX-License-Identifier: Apache-2.0

// Shared fetch wrapper for the whole app. Per docs/development.md's
// TypeScript standards ("no fetch in components"), every network call
// goes through here (usually wrapped again in a TanStack Query
// query/mutation fn) so the documented error envelope
// (docs/api.md: `{"error":{"code","message","details"}}`) is normalized
// into a single ApiError type exactly once, in exactly one place.
import type { ErrorEnvelope } from "./types";
import { getEmbedToken } from "../embed/embedToken";

/** Base path for the versioned REST API, per docs/api.md ("Base:
 * `https://<node>:8007/api/v1`"). The dev server proxies this to vnproxd
 * (see vite.config.ts); in production it's same-origin. */
export const API_BASE = "/api/v1";

/** Thrown for every non-2xx response. `code`/`details` come straight from
 * the documented error envelope when the server sent one; if the body
 * wasn't JSON (e.g. a proxy's plain-text 502) we fall back to a generic
 * `http_error` code so callers can still branch on `.code` uniformly. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: Record<string, unknown>;

  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }

  /** True for the session-expired / not-logged-in case every caller needs
   * to special-case (redirect to /login) rather than surface as an error
   * toast. */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export interface ApiFetchOptions extends Omit<RequestInit, "body"> {
  /** JSON-serializable request body. Set automatically with
   * Content-Type: application/json; use `RequestInit.body` directly (via
   * the base type) for non-JSON payloads instead. */
  json?: unknown;
  /** CSRF token to send as X-VNPROX-CSRF on mutating requests, per
   * docs/api.md's conventions section. Callers that already have a token
   * (e.g. from a cookie) pass it explicitly; apiFetch does not read
   * cookies itself so this module has no DOM/browser-only dependency
   * beyond `fetch`. */
  csrfToken?: string;
}

const MUTATING_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== "object" || value === null || !("error" in value)) {
    return false;
  }
  const err = (value as { error?: unknown }).error;
  return (
    typeof err === "object" &&
    err !== null &&
    typeof (err as { code?: unknown }).code === "string" &&
    typeof (err as { message?: unknown }).message === "string"
  );
}

async function readErrorBody(res: Response): Promise<ErrorEnvelope | undefined> {
  const text = await res.text();
  if (!text) {
    return undefined;
  }
  try {
    const parsed: unknown = JSON.parse(text);
    return isErrorEnvelope(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Fetch wrapper for `/api/v1/*`: JSON in, JSON out, session cookie
 * included, and every non-2xx response normalized into a thrown
 * `ApiError` with the documented `code`/`message`/`details`. Callers
 * (typically TanStack Query query/mutation functions) never need to
 * touch `Response` or parse the error envelope themselves.
 */
export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const { json, csrfToken, headers, method, ...rest } = options;
  const resolvedMethod = method ?? (json !== undefined ? "POST" : "GET");

  const finalHeaders = new Headers(headers);
  if (json !== undefined) {
    finalHeaders.set("Content-Type", "application/json");
  }
  if (csrfToken && MUTATING_METHODS.has(resolvedMethod.toUpperCase())) {
    finalHeaders.set("X-VNPROX-CSRF", csrfToken);
  }

  // Embed mode (T-1706): authenticate with the embed bearer token and never
  // send the session cookie — an embedded read-only view is a distinct auth
  // path from the cookie-session SPA (docs/security.md's embed-token model).
  const embedToken = getEmbedToken();
  if (embedToken) {
    finalHeaders.set("Authorization", `Bearer ${embedToken}`);
  }

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      ...rest,
      method: resolvedMethod,
      credentials: embedToken ? "omit" : "include",
      headers: finalHeaders,
      body: json !== undefined ? JSON.stringify(json) : undefined,
    });
  } catch (cause) {
    // Network failure (backend unreachable, TLS error, offline, ...) —
    // never a real docs/api.md error envelope, but callers should still
    // be able to branch on `.code`.
    throw new ApiError(0, "network_error", "could not reach the server", {
      cause: cause instanceof Error ? cause.message : String(cause),
    });
  }

  if (!res.ok) {
    const envelope = await readErrorBody(res);
    throw new ApiError(
      res.status,
      envelope?.error.code ?? "http_error",
      envelope?.error.message ?? (res.statusText || `request failed with status ${String(res.status)}`),
      envelope?.error.details,
    );
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  if (!text) {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}
