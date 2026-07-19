// Packet capture API calls (docs/api.md's Captures section, T-1301/T-1302;
// internal/api/captures.go). Every route is netRead + capture gated on the
// server; this module never enforces or re-derives a cap client-side — every
// numeric field a caller sends (durationSec/maxBytes/maxPackets) is a
// *request* the server may clamp down, and every value the UI renders after
// a session starts comes back from the response, never echoed from what was
// asked for (docs/api.md's Captures section, and see BpfBuilder.tsx's own
// doc comment for the regression this discipline exists to prevent).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { CaptureGroup, CaptureListResponse, CaptureStartRequest } from "./types";

/** POST /captures — start a (possibly multi-point) capture. */
export function startCapture(req: CaptureStartRequest): Promise<CaptureGroup> {
  return apiFetch<CaptureGroup>("/captures", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** POST /captures/{id}/stop — stop every session in the group. */
export function stopCapture(groupId: string): Promise<CaptureGroup> {
  return apiFetch<CaptureGroup>(`/captures/${encodeURIComponent(groupId)}/stop`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** GET /captures/{id} — one capture group, with live byte/packet accounting
 * reconciled server-side before returning (used for live-status polling). */
export function fetchCapture(groupId: string): Promise<CaptureGroup> {
  return apiFetch<CaptureGroup>(`/captures/${encodeURIComponent(groupId)}`);
}

/** GET /captures — every capture group (newest first). */
export function fetchCaptures(): Promise<CaptureGroup[]> {
  return apiFetch<CaptureListResponse>("/captures").then((r) => r.items);
}

/** GET /captures/{id}/download?sessionId= — the per-session pcap file, as
 * raw bytes (never JSON-parsed by apiFetch, which is why this bypasses it
 * and calls fetch directly). Used by CaptureDecoder to fetch the bytes to
 * decode in-browser; the download *button* itself is a plain `<a href>` to
 * captureDownloadUrl below rather than a fetch call, so the browser's own
 * save-file flow handles it without buffering the whole file into JS twice. */
export async function fetchCaptureFile(groupId: string, sessionId?: string): Promise<ArrayBuffer> {
  const res = await fetch(captureDownloadUrl(groupId, sessionId), { credentials: "include" });
  if (!res.ok) {
    throw new Error(`could not fetch capture file (status ${String(res.status)})`);
  }
  return res.arrayBuffer();
}

/** The download URL for one session's pcap file — used both as the download
 * button's `href` and as fetchCaptureFile's request target. `sessionId` is
 * omitted for a single-session group (the server defaults to the group's
 * primary session). */
export function captureDownloadUrl(groupId: string, sessionId?: string): string {
  const base = `/api/v1/captures/${encodeURIComponent(groupId)}/download`;
  return sessionId ? `${base}?sessionId=${encodeURIComponent(sessionId)}` : base;
}
