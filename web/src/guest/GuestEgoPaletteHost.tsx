// SPDX-License-Identifier: Apache-2.0

// T-3906's third entry point: "reachable ... from the topology map's guest
// entities". The natural place for that is a guest-kind action button in
// the map inspector's header, next to Diagnose/Trace path
// (topology/InspectorPanel.tsx) — but that file is concurrently owned by
// other in-flight topology work this task was told to stay out of, so this
// reaches the same guest via the command palette (⌘K) instead: whenever a
// guest is the map's current selection, an "Open guest view" verb appears,
// mirroring simulator/SimulatorPage.tsx's own "Simulate path from <entity>"
// palette verb, which reads the same selection the same way.
// Mounted once, app-wide, in AppShell.tsx (alongside ChangesetDrawer,
// MgmtWizardHost, etc.) — not itself part of web/src/topology, though it
// reads topology/store.ts's already-exported selection state (a read-only
// import, the same one SimulatorPage.tsx already makes).
//
// Renders nothing: this is a palette-action registrar, not a visible
// surface, so it carries no help topic of its own (nothing here is a
// screen or panel an operator could point a cursor at independent of the
// palette itself).
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useTopologyStore } from "../topology/store";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { isGuestRef } from "./guestEgo";

export function GuestEgoPaletteHost() {
  const navigate = useNavigate();
  const selectedId = useTopologyStore((s) => s.selectedId);

  const actions = useMemo<PaletteAction[]>(() => {
    if (!selectedId || !isGuestRef(selectedId)) return [];
    return [
      {
        id: `guest-ego-${selectedId}`,
        label: `Open guest view for ${selectedId}`,
        hint: "Guest view",
        keywords: ["guest", "ego", "nics", "path", "firewall", "flows"],
        perform: () => {
          void navigate(`/guest?ref=${encodeURIComponent(selectedId)}`);
        },
      },
    ];
  }, [selectedId, navigate]);

  usePaletteActions("guest-ego", actions);

  return null;
}
