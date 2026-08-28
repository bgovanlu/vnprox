// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { ApiError, apiFetch } from "./client";

function jsonResponse(status: number, body: unknown, statusText = ""): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText,
    headers: { "Content-Type": "application/json" },
  });
}

/** Awaits a promise expected to reject with an ApiError, returning it
 * typed — cleaner than `.catch((e: unknown) => e as ApiError)` at every
 * call site, which TypeScript can't narrow since apiFetch's success type
 * is generic. */
async function captureApiError(promise: Promise<unknown>): Promise<ApiError> {
  try {
    await promise;
  } catch (err) {
    if (err instanceof ApiError) return err;
    throw err;
  }
  throw new Error("expected the promise to reject with an ApiError");
}

describe("apiFetch", () => {
  it("returns the parsed JSON body on success", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(200, { hello: "world" })),
    );

    await expect(apiFetch<{ hello: string }>("/ping")).resolves.toEqual({ hello: "world" });
  });

  it("calls the versioned base path with credentials included", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, {}));
    vi.stubGlobal("fetch", fetchMock);

    await apiFetch("/auth/me");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/auth/me",
      expect.objectContaining({ credentials: "include", method: "GET" }),
    );
  });

  it("returns undefined for a 204 No Content response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));

    await expect(apiFetch("/changesets/abc")).resolves.toBeUndefined();
  });

  it("normalizes the documented error envelope into an ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(422, {
          error: {
            code: "validation_failed",
            message: "vlan id out of range",
            details: { field: "vlanId" },
          },
        }),
      ),
    );

    const err = await captureApiError(apiFetch("/changesets"));

    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(422);
    expect(err.code).toBe("validation_failed");
    expect(err.message).toBe("vlan id out of range");
    expect(err.details).toEqual({ field: "vlanId" });
  });

  it("marks 401 responses as isUnauthorized", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(401, { error: { code: "not_authenticated", message: "no session" } }),
      ),
    );

    const err = await captureApiError(apiFetch("/auth/me"));
    expect(err.isUnauthorized).toBe(true);
  });

  it("falls back to a generic http_error code when the body isn't a JSON error envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("Bad Gateway", { status: 502, statusText: "Bad Gateway" })),
    );

    const err = await captureApiError(apiFetch("/topology"));
    expect(err.status).toBe(502);
    expect(err.code).toBe("http_error");
    expect(err.message).toBe("Bad Gateway");
  });

  it("wraps fetch-level failures (network down) as a network_error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("fetch failed")));

    const err = await captureApiError(apiFetch("/topology"));
    expect(err.code).toBe("network_error");
    expect(err.status).toBe(0);
  });

  it("sends the CSRF header only for mutating requests when a token is provided", async () => {
    // Response bodies can only be read once, so each call needs a fresh
    // Response instance rather than one shared mockResolvedValue.
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(200, {})));
    vi.stubGlobal("fetch", fetchMock);

    await apiFetch("/changesets", { json: { title: "x", ops: [] }, csrfToken: "tok-123" });
    const mutatingHeaders = (fetchMock.mock.calls[0] as [string, RequestInit])[1].headers as Headers;
    expect(mutatingHeaders.get("X-VNPROX-CSRF")).toBe("tok-123");

    fetchMock.mockClear();
    await apiFetch("/topology", { csrfToken: "tok-123" });
    const readHeaders = (fetchMock.mock.calls[0] as [string, RequestInit])[1].headers as Headers;
    expect(readHeaders.get("X-VNPROX-CSRF")).toBeNull();
  });
});
