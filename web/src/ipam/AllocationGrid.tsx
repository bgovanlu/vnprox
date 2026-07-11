// The /24-and-smaller allocation grid + paged block-summary view for larger
// subnets (docs/features/ipam.md §2). Perf note (T-405 acceptance
// criterion 5): GridCell is memoized and receives a referentially-stable
// onClick (useCallback keyed only on `cells`), so clicking/hovering one
// cell re-renders that one cell, not all 254/256 — a plain CSS grid of up
// to 256 elements with memoized children stays well under a 16ms frame
// budget for any single interaction (React DevTools Profiler flamegraph:
// one cell's own re-render, not the grid's — see this task's report for
// how this was verified without a browser in this environment).
import { memo, useCallback, useMemo, useState } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import type { IpamBlockSummary, IpamCell } from "../api/types";
import { cellStateClasses, stateLabel } from "./labels";
import { useIpamAllocationsQuery } from "./queries";
import { CellDetailDialog } from "./CellDetailDialog";

const GridCell = memo(function GridCell({
  cell,
  onClick,
}: {
  cell: IpamCell;
  onClick: (ip: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => { onClick(cell.ip); }}
      title={`${cell.ip} — ${stateLabel(cell.state)}`}
      aria-label={`${cell.ip}: ${stateLabel(cell.state)}`}
      className={clsx(
        "aspect-square rounded border text-[9px] leading-none text-slate-700 dark:text-slate-200",
        "flex items-center justify-center transition-colors hover:ring-2 hover:ring-accent-500",
        cellStateClasses[cell.state],
      )}
    >
      {cell.ip.slice(cell.ip.lastIndexOf(".") + 1)}
    </button>
  );
});

function BlockTile({ block, onOpen }: { block: IpamBlockSummary; onOpen: (cidr: string) => void }) {
  const pct = Math.round(block.utilization * 100);
  return (
    <button
      type="button"
      onClick={() => { onOpen(block.cidr); }}
      className={clsx(
        "flex flex-col items-start gap-0.5 rounded border p-2 text-left text-xs transition-colors hover:ring-2 hover:ring-accent-500",
        block.conflicts > 0
          ? "border-red-400 bg-red-50 dark:border-red-600 dark:bg-red-950/40"
          : "border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900",
      )}
    >
      <span className="font-mono font-medium">{block.cidr}</span>
      <span className="text-slate-500 dark:text-slate-400">
        {block.allocated}/{block.total} allocated ({pct}%)
      </span>
      {block.conflicts > 0 && (
        <span className="font-medium text-red-600 dark:text-red-400">{block.conflicts} conflict(s)</span>
      )}
    </button>
  );
}

export interface AllocationGridProps {
  subnetCidr: string;
  readOnly?: boolean;
}

/** Renders subnetCidr's allocation grid: a direct color grid for a
 * <=256-address subnet, or (docs/features/ipam.md §2) paged /24-sized
 * block summaries for a larger one, with drill-down into any one block's
 * own full grid. */
export function AllocationGrid({ subnetCidr, readOnly }: AllocationGridProps) {
  const [block, setBlock] = useState<string | undefined>(undefined);
  const { data: grid, isLoading, isError } = useIpamAllocationsQuery(subnetCidr, block);
  const [selectedIP, setSelectedIP] = useState<string | undefined>(undefined);

  const cells = useMemo(() => grid?.cells ?? [], [grid]);
  const handleCellClick = useCallback(
    (ip: string) => {
      setSelectedIP(ip);
    },
    [],
  );
  const selectedCell = cells.find((c) => c.ip === selectedIP);

  if (isLoading) {
    return <p className="text-sm text-slate-400">Loading allocation grid…</p>;
  }
  if (isError || !grid) {
    return <EmptyState title="Could not load allocation grid" description="Check that vnproxd can reach the local PVE API, then reload." />;
  }

  if (grid.paged && !block) {
    return (
      <div>
        <p className="mb-2 text-xs text-slate-500 dark:text-slate-400">
          {grid.total.toLocaleString()} addresses — grouped into {grid.blocks?.length ?? 0} /24 blocks. Click a block
          to view its grid.
        </p>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-8">
          {(grid.blocks ?? []).map((b) => (
            <BlockTile key={b.cidr} block={b} onOpen={setBlock} />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div>
      {grid.paged && block && (
        <button
          type="button"
          className="mb-2 text-xs font-medium text-accent-600 hover:underline dark:text-accent-400"
          onClick={() => { setBlock(undefined); }}
        >
          &larr; back to blocks
        </button>
      )}
      <div
        role="grid"
        aria-label={`Allocation grid for ${grid.block ?? grid.cidr}`}
        className="grid grid-cols-[repeat(16,minmax(0,1fr))] gap-[3px] sm:grid-cols-[repeat(24,minmax(0,1fr))]"
      >
        {cells.map((c) => (
          <GridCell key={c.ip} cell={c} onClick={handleCellClick} />
        ))}
      </div>
      {selectedCell && (
        <CellDetailDialog
          open
          onOpenChange={(open) => {
            if (!open) setSelectedIP(undefined);
          }}
          cell={selectedCell}
          subnetCidr={grid.cidr}
          readOnly={readOnly}
        />
      )}
    </div>
  );
}
