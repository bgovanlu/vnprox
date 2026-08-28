// SPDX-License-Identifier: Apache-2.0

// The decoded-packet list + detail pane (Wireshark-lite) for one capture
// session (T-1302). Purely presentational over CaptureDecoder.ts's output —
// it never fetches or decodes anything itself.
import { useState } from "react";
import type { DecodedPacket } from "./CaptureDecoder";

export interface PacketListProps {
  packets: DecodedPacket[];
  /** A short label identifying which node/session this list belongs to —
   * shown as a heading, load-bearing for the multi-point side-by-side view
   * (each pane needs to say which node it is). */
  paneLabel?: string;
}

export function PacketList({ packets, paneLabel }: PacketListProps) {
  const [selectedIndex, setSelectedIndex] = useState<number | undefined>(packets.length > 0 ? 0 : undefined);
  const selected = packets.find((p) => p.index === selectedIndex);

  if (packets.length === 0) {
    return <p className="text-xs text-slate-600 dark:text-slate-400">No packets in this capture.</p>;
  }

  return (
    <div className="space-y-1" data-testid="packet-list">
      {paneLabel && <h4 className="text-xs font-semibold text-slate-600 dark:text-slate-300">{paneLabel}</h4>}
      <div className="grid grid-cols-2 gap-2">
        <div className="max-h-64 overflow-y-auto rounded border border-slate-200 dark:border-slate-700">
          <table className="w-full text-left text-[11px]">
            <thead className="sticky top-0 bg-slate-100 dark:bg-slate-800">
              <tr>
                <th className="px-1.5 py-1">#</th>
                <th className="px-1.5 py-1">Time</th>
                <th className="px-1.5 py-1">Summary</th>
              </tr>
            </thead>
            <tbody>
              {packets.map((p) => (
                <tr
                  key={p.index}
                  role="row"
                  aria-selected={p.index === selectedIndex}
                  tabIndex={0}
                  onClick={() => { setSelectedIndex(p.index); }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") setSelectedIndex(p.index);
                  }}
                  className={
                    p.index === selectedIndex
                      ? "cursor-pointer bg-accent-100 dark:bg-accent-900"
                      : "cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800"
                  }
                >
                  <td className="px-1.5 py-0.5 font-mono">{p.index}</td>
                  <td className="px-1.5 py-0.5 font-mono">
                    {p.tsSec}.{String(p.tsUsec).padStart(6, "0")}
                  </td>
                  <td className="px-1.5 py-0.5">
                    {p.summary}
                    {p.truncated && <span className="ml-1 text-amber-600 dark:text-amber-400">(truncated)</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="max-h-64 overflow-y-auto rounded border border-slate-200 p-1.5 text-[11px] dark:border-slate-700">
          {!selected && <p className="text-slate-600 dark:text-slate-400">Select a packet to see its decoded fields.</p>}
          {selected?.layers.map((layer) => (
            <div key={layer.name} className="mb-2">
              <p className="font-semibold text-slate-700 dark:text-slate-200">{layer.name}</p>
              <ul className="ml-2 space-y-0.5">
                {Object.entries(layer.fields).map(([k, v]) => (
                  <li key={k} className="font-mono text-fg-subtle">
                    {k}: {v}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
