// SPDX-License-Identifier: Apache-2.0

// EventAnnotationsPanel (T-1706): the render body of the vnprox Grafana
// live-event-annotation panel. It consumes the T-1104 WS "events" topic —
// the same `{event, ...}` envelope api/ws.ts defines (WsServerEvent) — and
// renders each event as a timeline annotation. Like MetricsPanel, the
// Grafana plugin wrapper (event-annotation source, module.ts) lives in the
// external plugin repo (web/grafana-panel/README.md); this component and the
// event-shape contract live in-repo and are tested against a fixture event
// stream with a mocked transport (AC4). The caller (the Grafana wrapper, or a
// test) owns the WS subscription and passes the accumulated events in as a
// prop, so this component has no live-socket dependency of its own.
import type { WsServerEvent } from "../api/ws";

interface EventAnnotationsPanelProps {
  /** Events accumulated from the T-1104 WS "events" topic, newest last. */
  events: WsServerEvent[];
}

/** Best-effort human summary of one event envelope: a `message`/`detail`
 * field if present, else the event name. */
function summarize(evt: WsServerEvent): string {
  for (const key of ["message", "detail", "summary"]) {
    const v = evt[key];
    if (typeof v === "string" && v !== "") return v;
  }
  return evt.event;
}

/** The `ts`/`at` timestamp field if the envelope carries one (unix seconds),
 * else undefined. */
function eventTs(evt: WsServerEvent): number | undefined {
  for (const key of ["ts", "at", "time"]) {
    const v = evt[key];
    if (typeof v === "number") return v;
  }
  return undefined;
}

export function EventAnnotationsPanel({ events }: EventAnnotationsPanelProps) {
  if (events.length === 0) {
    return (
      <p className="text-sm text-slate-600 dark:text-slate-400" data-testid="event-annotations-empty">
        No events yet.
      </p>
    );
  }

  return (
    <ul className="flex flex-col gap-1" data-testid="event-annotations">
      {events.map((evt, i) => {
        const ts = eventTs(evt);
        return (
          <li key={`${evt.event}-${String(i)}`} className="flex items-center gap-2 text-sm">
            <span className="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">
              {evt.event}
            </span>
            {ts !== undefined ? (
              <time className="text-xs text-slate-600 dark:text-slate-400" dateTime={new Date(ts * 1000).toISOString()}>
                {new Date(ts * 1000).toISOString()}
              </time>
            ) : null}
            <span className="text-slate-700 dark:text-slate-200">{summarize(evt)}</span>
          </li>
        );
      })}
    </ul>
  );
}
