// The real implementation lives in src/conntrack/ (the conntrack feature
// module) alongside its own tests — this file only wires it to the routed
// /conntrack path App.tsx expects, per the existing per-route-file layout
// (see pages/FlowExplorerPage.tsx).
export { ConntrackExplorer as ConntrackPage } from "../conntrack/ConntrackExplorer";
