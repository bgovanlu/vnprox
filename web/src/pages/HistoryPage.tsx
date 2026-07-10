// The real implementation lives in src/history/ (timeline grouping, diff
// viewer, restore flow) alongside its own tests — this file only wires it
// to the routed /history path App.tsx expects, per the existing
// per-route-file layout T-005 established.
export { HistoryPage } from "../history/HistoryPage";
