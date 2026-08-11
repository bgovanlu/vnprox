// T-2804's incident view: `GET/POST /incidents` and friends.
//
// An incident is a VIEW, not a mode — opening one starts no collection and
// copies no event, so the client can open one over a window that closed hours
// ago and get exactly what a live one would have contained. Nothing in this
// module polls or subscribes; the timeline is a plain read.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { TopologyDiffResponse } from "./topologyDiff";

/** The closed source vocabulary (docs/api.md's Incidents section). */
export type IncidentSource = "finding" | "changeset" | "diagnosis" | "capture" | "flow" | "annotation";

export const INCIDENT_SOURCES: readonly IncidentSource[] = [
  "finding",
  "changeset",
  "diagnosis",
  "capture",
  "flow",
  "annotation",
] as const;

export type IncidentStatus = "open" | "closed";

/** Per-source status. `unavailable` ("that source is not wired on this node")
 * is deliberately distinct from a count of zero ("nothing happened"). */
export type IncidentSourceStatus = "ok" | "unavailable" | "error" | "truncated";

export interface IncidentAnnotation {
  id: string;
  author: string;
  body: string;
  at: number;
}

export interface Incident {
  id: string;
  title: string;
  status: IncidentStatus;
  openedBy: string;
  openedAt: number;
  startedAt: number;
  endedAt?: number;
  closedAt?: number;
  /** Derived server-side: the record was created after its window began. */
  retroactive: boolean;
  annotations: IncidentAnnotation[];
}

export interface IncidentEvent {
  id: string;
  at: number;
  source: IncidentSource;
  kind: string;
  summary: string;
  actor?: string;
  node?: string;
  ref?: string;
  result?: string;
  findingId?: string;
  transition?: string;
  changesetId?: string;
  action?: string;
  captureId?: string;
  annotationId?: string;
}

export interface IncidentWindow {
  from: number;
  to: number;
  /** True when the incident is still open and `to` came from the clock. */
  live: boolean;
}

export interface IncidentSourceReport {
  source: IncidentSource;
  status: IncidentSourceStatus;
  count: number;
  detail?: string;
}

export interface IncidentTimeline {
  incident: Incident;
  window: IncidentWindow;
  events: IncidentEvent[];
  sources: IncidentSourceReport[];
  /** The T-2704 diff across the window, absent when it could not be computed
   * — in which case `diffError` carries the change engine's own message,
   * which names the snapshots that DO exist. An empty diff is never
   * substituted for a refusal. */
  diff?: TopologyDiffResponse;
  diffError?: string;
  diffErrorCode?: string;
  caveats: string[];
}

export interface IncidentListResponse {
  items: Incident[];
}

export interface OpenIncidentRequest {
  title: string;
  /** Omitted: from now (a live incident). Supplied with `endedAt`: the
   * retroactive case, which is the same route and no special mode. */
  startedAt?: number;
  endedAt?: number;
}

export function fetchIncidents(): Promise<IncidentListResponse> {
  return apiFetch<IncidentListResponse>("/incidents");
}

export function fetchIncident(id: string): Promise<Incident> {
  return apiFetch<Incident>(`/incidents/${encodeURIComponent(id)}`);
}

export function fetchIncidentTimeline(id: string): Promise<IncidentTimeline> {
  return apiFetch<IncidentTimeline>(`/incidents/${encodeURIComponent(id)}/timeline`);
}

export function openIncident(req: OpenIncidentRequest): Promise<Incident> {
  return apiFetch<Incident>("/incidents", { json: req, csrfToken: readCsrfCookie() });
}

export function annotateIncident(id: string, body: string, at?: number): Promise<IncidentAnnotation> {
  return apiFetch<IncidentAnnotation>(`/incidents/${encodeURIComponent(id)}/annotations`, {
    json: at === undefined ? { body } : { body, at },
    csrfToken: readCsrfCookie(),
  });
}

export function closeIncident(id: string): Promise<Incident> {
  return apiFetch<Incident>(`/incidents/${encodeURIComponent(id)}/close`, {
    json: {},
    csrfToken: readCsrfCookie(),
  });
}

export function reopenIncident(id: string): Promise<Incident> {
  return apiFetch<Incident>(`/incidents/${encodeURIComponent(id)}/reopen`, {
    json: {},
    csrfToken: readCsrfCookie(),
  });
}

/** The export is a plain download, so it is a URL rather than a fetch: the
 * browser streams the archive straight to disk. */
export function incidentExportUrl(id: string): string {
  return `/api/v1/incidents/${encodeURIComponent(id)}/export`;
}
