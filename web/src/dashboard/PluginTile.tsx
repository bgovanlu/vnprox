// SPDX-License-Identifier: Apache-2.0

// T-3911: renders one plugin-provided tile (GET /dashboard/tiles,
// docs/plugins/dashboard-tile.md's Tile) through the exact same shared
// shell (DashboardTile.tsx) every built-in tile renders through — no
// bespoke rendering path for plugin tiles, per this card's acceptance
// criterion 2. `Value`/`Detail` are rendered verbatim, exactly as the
// contract promises ("Your tiles render exactly the fields you set, with
// no server-side reinterpretation"); `Link` (optional) becomes the shell's
// deep-link button when present, and no button at all when absent — a
// plugin's `Tile` carries no action of its own (docs/plugins/dashboard-
// tile.md's "What the plugin must not do"), so this component adds none.
import { useNavigate } from "react-router-dom";
import type { DashboardTile as DashboardTileData } from "../api/types";
import { DashboardTile } from "./DashboardTile";

const SEVERITY_DOT: Record<NonNullable<DashboardTileData["severity"]>, string> = {
  info: "bg-slate-400",
  warn: "bg-amber-500",
  critical: "bg-red-500",
};

export interface PluginTileProps {
  tile: DashboardTileData;
}

export function PluginTile({ tile }: PluginTileProps) {
  const navigate = useNavigate();
  const link = tile.link;
  const dot = tile.severity ? SEVERITY_DOT[tile.severity] : undefined;

  return (
    <DashboardTile
      title={tile.title}
      onOpen={link !== undefined ? () => void navigate(link) : undefined}
      openLabel={link !== undefined ? "Open" : undefined}
    >
      <div className="flex items-start gap-2">
        {dot ? <span aria-hidden className={`mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full ${dot}`} /> : null}
        <div className="flex flex-col gap-0.5">
          <p className="text-2xl font-semibold tabular-nums text-slate-800 dark:text-slate-100">{tile.value}</p>
          {tile.detail ? <p className="text-xs text-slate-500 dark:text-slate-400">{tile.detail}</p> : null}
        </div>
      </div>
    </DashboardTile>
  );
}
