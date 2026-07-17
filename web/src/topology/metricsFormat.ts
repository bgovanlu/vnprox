// Rate-formatting helper shared between the Inspector's Metrics tab
// (MetricsTab.tsx) and the Home dashboard's top-talkers tile
// (dashboard/TopTalkersTile.tsx, T-904) — split into its own module rather
// than exported from MetricsTab.tsx so a plain formatting function doesn't
// live inside (and trip the react-refresh/only-export-components lint rule
// on) a component file.
export function formatBps(bps: number): string {
  const abs = Math.abs(bps);
  if (abs >= 1e9) return `${(bps / 1e9).toFixed(2)} Gbps`;
  if (abs >= 1e6) return `${(bps / 1e6).toFixed(2)} Mbps`;
  if (abs >= 1e3) return `${(bps / 1e3).toFixed(1)} Kbps`;
  return `${bps.toFixed(0)} bps`;
}
