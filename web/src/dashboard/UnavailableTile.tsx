// SPDX-License-Identifier: Apache-2.0

// T-3911 acceptance criterion 3 / this card's explicit "graceful
// degradation" requirement: a saved layout can reference a plugin tile
// (`kind: "plugin"`) whose provider is no longer present by the time the
// dashboard renders — the plugin was disabled or uninstalled, or its
// `Tiles()` call started erroring (already dropped server-side by
// plugin.Registry.DashboardTiles before GET /dashboard/tiles' response is
// built, per docs/plugins/dashboard-tile.md's degrade-one-provider
// contract) — or, less commonly, a built-in tile id from an older build
// that no longer exists. Either way the grid renders this explicit
// placeholder in the tile's saved slot instead of crashing or silently
// collapsing the grid; the layout itself is untouched until the operator
// removes it via the slot's own "Remove" control (DashboardGrid.tsx).
import { DashboardTile } from "./DashboardTile";

export function UnavailableTile() {
  return (
    <DashboardTile
      title="Tile unavailable"
      empty={{
        title: "Not available right now",
        description:
          "This tile's plugin is disabled, uninstalled, or reporting an error. Remove it below, or it reappears automatically once the plugin is available again.",
      }}
    />
  );
}
