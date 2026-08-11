// T-2804 — Incidents. One window, one timeline, five sources plus the
// operator's own notes.
//
// The page is deliberately plain: under time pressure a reader wants one
// column in time order, not five panels to correlate. What it must never do
// is imply certainty it does not have — hence the source-gap strip (a source
// that contributed nothing says why) and the diff strip (a range the change
// engine cannot cover shows its refusal, never "nothing changed").
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  annotateIncident,
  closeIncident,
  fetchIncidents,
  fetchIncidentTimeline,
  incidentExportUrl,
  openIncident,
  reopenIncident,
  type Incident,
  type IncidentListResponse,
  type IncidentTimeline,
} from "../api/incidents";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Tooltip } from "../components/Tooltip";
import { useToast } from "../components/Toast";
import { diffSummary, orderEvents, sourceGaps, sourceGlyph, sourceLabel, windowLabel } from "./timeline";

const INCIDENTS_QUERY_KEY = ["incidents"] as const;

function formatTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

/** Parses a `<input type="datetime-local">` value into unix seconds, or
 * undefined for a blank/unparsable field — which means "from now" on the
 * start and "runs to now" on the end. */
function parseLocalDateTime(value: string): number | undefined {
  if (value === "") return undefined;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? undefined : Math.floor(ms / 1000);
}

export function IncidentsPage() {
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const { data: session } = useSession();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [title, setTitle] = useState("");
  const [startedAt, setStartedAt] = useState("");
  const [endedAt, setEndedAt] = useState("");
  const [note, setNote] = useState("");

  // Every route in this section is `audit`-gated (docs/api.md), including the
  // reads — the timeline re-exposes audit_log rows.
  const canUse = hasAnyCap(session, "audit");
  const disabledReason = canUse ? undefined : missingCapTooltip(session, "", "audit");

  const listQuery = useQuery<IncidentListResponse>({
    queryKey: INCIDENTS_QUERY_KEY,
    queryFn: fetchIncidents,
    enabled: canUse,
  });
  const incidents = useMemo(() => listQuery.data?.items ?? [], [listQuery.data]);

  const timelineQuery = useQuery<IncidentTimeline>({
    queryKey: ["incident-timeline", selectedId ?? ""],
    queryFn: () => fetchIncidentTimeline(selectedId ?? ""),
    enabled: canUse && Boolean(selectedId),
  });

  const invalidate = (id?: string) => {
    void queryClient.invalidateQueries({ queryKey: INCIDENTS_QUERY_KEY });
    if (id) {
      void queryClient.invalidateQueries({ queryKey: ["incident-timeline", id] });
    }
  };

  const openMutation = useMutation({
    mutationFn: () =>
      openIncident({
        title,
        startedAt: parseLocalDateTime(startedAt),
        endedAt: parseLocalDateTime(endedAt),
      }),
    onSuccess: (incident: Incident) => {
      setTitle("");
      setStartedAt("");
      setEndedAt("");
      setSelectedId(incident.id);
      invalidate(incident.id);
      toast({
        variant: "success",
        title: incident.retroactive ? "Retroactive incident opened" : "Incident opened",
      });
    },
    onError: (err: Error) => {
      toast({ variant: "error", title: "Could not open the incident", description: err.message });
    },
  });

  const annotateMutation = useMutation({
    mutationFn: () => annotateIncident(selectedId ?? "", note),
    onSuccess: () => {
      setNote("");
      invalidate(selectedId);
    },
    onError: (err: Error) => {
      toast({ variant: "error", title: "Could not add the note", description: err.message });
    },
  });

  const closeMutation = useMutation({
    mutationFn: () => closeIncident(selectedId ?? ""),
    onSuccess: () => {
      invalidate(selectedId);
      toast({ variant: "success", title: "Incident closed", description: "Its timeline is unchanged." });
    },
    onError: (err: Error) => {
      toast({ variant: "error", title: "Could not close the incident", description: err.message });
    },
  });

  const reopenMutation = useMutation({
    mutationFn: () => reopenIncident(selectedId ?? ""),
    onSuccess: () => {
      invalidate(selectedId);
      toast({ variant: "success", title: "Incident reopened" });
    },
    onError: (err: Error) => {
      toast({ variant: "error", title: "Could not reopen the incident", description: err.message });
    },
  });

  const timeline = timelineQuery.data;
  const events = useMemo(() => (timeline ? orderEvents(timeline.events) : []), [timeline]);
  const gaps = useMemo(() => (timeline ? sourceGaps(timeline.sources) : []), [timeline]);
  const diff = timeline ? diffSummary(timeline) : undefined;

  if (!canUse) {
    return (
      <div className="p-6">
        <h1 className="text-xl font-semibold text-slate-100">Incidents</h1>
        <EmptyState
          title="You cannot read incidents"
          description={disabledReason ?? "This view needs the audit capability."}
        />
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <header>
        <h1 className="text-xl font-semibold text-slate-100">Incidents</h1>
        <p className="text-sm text-slate-400">
          One timeline over a window: findings, changesets, diagnosis runs, captures, flows and your own notes. An
          incident is a view — opening one collects nothing new, so you can open one over a window that has already
          passed.
        </p>
      </header>

      <section aria-label="Open an incident" className="rounded border border-slate-700 p-3">
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col text-xs text-slate-400">
            Title
            <input
              className="mt-1 w-64 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100"
              value={title}
              onChange={(e) => {
                setTitle(e.target.value);
              }}
              placeholder="vmbr0 down on pve2"
            />
          </label>
          <label className="flex flex-col text-xs text-slate-400">
            From (blank = now)
            <input
              type="datetime-local"
              className="mt-1 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100"
              value={startedAt}
              onChange={(e) => {
                setStartedAt(e.target.value);
              }}
            />
          </label>
          <label className="flex flex-col text-xs text-slate-400">
            To (blank = still unfolding)
            <input
              type="datetime-local"
              className="mt-1 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100"
              value={endedAt}
              onChange={(e) => {
                setEndedAt(e.target.value);
              }}
            />
          </label>
          <Button
            variant="primary"
            disabled={title.trim() === "" || openMutation.isPending}
            onClick={() => {
              openMutation.mutate();
            }}
          >
            Start incident
          </Button>
        </div>
      </section>

      <div className="flex min-h-0 flex-1 gap-4">
        <nav aria-label="Incidents" className="w-72 shrink-0 overflow-y-auto rounded border border-slate-700">
          {incidents.length === 0 ? (
            <EmptyState density="compact" title="No incidents yet" description="Start one above, live or over a past window." />
          ) : (
            <ul>
              {incidents.map((incident) => (
                <li key={incident.id}>
                  <button
                    type="button"
                    onClick={() => {
                      setSelectedId(incident.id);
                    }}
                    aria-current={incident.id === selectedId}
                    className="w-full border-b border-slate-800 px-3 py-2 text-left hover:bg-slate-800"
                  >
                    <span className="block text-sm text-slate-100">{incident.title}</span>
                    <span className="block text-xs text-slate-400">
                      {incident.status === "open" ? "Open" : "Closed"} · {formatTime(incident.startedAt)}
                      {incident.retroactive ? " · retroactive" : ""}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </nav>

        <section aria-label="Incident timeline" className="min-w-0 flex-1 overflow-y-auto rounded border border-slate-700 p-3">
          {!selectedId ? (
            <EmptyState title="Select an incident" description="Its timeline appears here." />
          ) : timelineQuery.isPending ? (
            <p className="text-sm text-slate-400">Assembling the timeline…</p>
          ) : timelineQuery.isError ? (
            <EmptyState title="Could not load this timeline" description={timelineQuery.error.message} />
          ) : timeline ? (
            <div className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className="text-lg font-semibold text-slate-100">{timeline.incident.title}</h2>
                  <p className="text-xs text-slate-400">{windowLabel(timeline)}</p>
                </div>
                <div className="flex gap-2">
                  {timeline.incident.status === "open" ? (
                    <Button
                      onClick={() => {
                        closeMutation.mutate();
                      }}
                      disabled={closeMutation.isPending}
                    >
                      Close
                    </Button>
                  ) : (
                    <Button
                      onClick={() => {
                        reopenMutation.mutate();
                      }}
                      disabled={reopenMutation.isPending}
                    >
                      Reopen
                    </Button>
                  )}
                  <a
                    className="inline-flex items-center rounded border border-slate-600 px-3 py-1 text-sm text-slate-100 hover:bg-slate-800"
                    href={incidentExportUrl(timeline.incident.id)}
                    download
                  >
                    Export
                  </a>
                </div>
              </div>

              {diff ? (
                <p
                  data-testid="incident-diff"
                  className={
                    diff.available
                      ? "rounded border border-slate-700 p-2 text-sm text-slate-300"
                      : "rounded border border-amber-700 p-2 text-sm text-amber-200"
                  }
                >
                  {diff.available ? diff.message : `No point-in-time diff for this window: ${diff.message}`}
                </p>
              ) : null}

              {gaps.length > 0 ? (
                <ul data-testid="incident-source-gaps" className="rounded border border-amber-700 p-2 text-xs text-amber-200">
                  {gaps.map((gap) => (
                    <li key={gap.source}>
                      {gap.label}: {gap.status} — {gap.detail}
                    </li>
                  ))}
                </ul>
              ) : null}

              {timeline.caveats.length > 0 ? (
                <ul data-testid="incident-caveats" className="rounded border border-slate-700 p-2 text-xs text-slate-400">
                  {timeline.caveats.map((caveat) => (
                    <li key={caveat}>{caveat}</li>
                  ))}
                </ul>
              ) : null}

              <div className="flex items-end gap-2">
                <label className="flex flex-1 flex-col text-xs text-slate-400">
                  Add a note
                  <input
                    className="mt-1 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100"
                    value={note}
                    onChange={(e) => {
                      setNote(e.target.value);
                    }}
                    placeholder="pulled the cable on eno1"
                  />
                </label>
                <Button
                  disabled={note.trim() === "" || annotateMutation.isPending}
                  onClick={() => {
                    annotateMutation.mutate();
                  }}
                >
                  Add note
                </Button>
              </div>

              {events.length === 0 ? (
                <EmptyState
                  density="compact"
                  title="No events in this window"
                  description="Nothing vnprox records happened between these two times."
                />
              ) : (
                <ol data-testid="incident-events" className="flex flex-col">
                  {events.map((event) => (
                    <li key={event.id} data-source={event.source} className="flex gap-3 border-b border-slate-800 py-1 text-sm">
                      <span className="w-40 shrink-0 text-xs text-slate-400">{formatTime(event.at)}</span>
                      <Tooltip content={sourceLabel(event.source)}>
                        <span aria-label={sourceLabel(event.source)} className="w-5 shrink-0 text-slate-400">
                          {sourceGlyph(event.source)}
                        </span>
                      </Tooltip>
                      <span className="min-w-0 flex-1 text-slate-200">{event.summary}</span>
                      {event.actor ? <span className="shrink-0 text-xs text-slate-500">{event.actor}</span> : null}
                    </li>
                  ))}
                </ol>
              )}
            </div>
          ) : null}
        </section>
      </div>
    </div>
  );
}
