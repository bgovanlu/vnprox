// T-1706 AC4 (event-annotation half): the Grafana live-event-annotation
// panel renders against a fixture T-1104 WS event stream, with the transport
// mocked (events passed in as a prop) — no live socket or Grafana.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WsServerEvent } from "../api/ws";
import { EventAnnotationsPanel } from "./EventAnnotationsPanel";

// Fixture events shaped like the T-1104 "events" topic envelope
// (api/ws.ts's WsServerEvent: an `event` name plus payload fields).
const events: WsServerEvent[] = [
  { event: "changeset.status", ts: 1_700_000_000, message: "changeset cs_1 committed" },
  { event: "finding.opened", at: 1_700_000_050, detail: "bond degraded on pve2" },
  { event: "drift.changed" },
];

describe("EventAnnotationsPanel", () => {
  it("renders each WS event as an annotation (AC4)", () => {
    render(<EventAnnotationsPanel events={events} />);
    expect(screen.getByTestId("event-annotations")).toBeInTheDocument();
    expect(screen.getByText("changeset.status")).toBeInTheDocument();
    expect(screen.getByText("changeset cs_1 committed")).toBeInTheDocument();
    expect(screen.getByText("bond degraded on pve2")).toBeInTheDocument();
    // An event with no message/detail falls back to its event name as summary.
    expect(screen.getAllByText("drift.changed").length).toBeGreaterThan(0);
  });

  it("shows an empty state with no events", () => {
    render(<EventAnnotationsPanel events={[]} />);
    expect(screen.getByTestId("event-annotations-empty")).toBeInTheDocument();
  });
});
