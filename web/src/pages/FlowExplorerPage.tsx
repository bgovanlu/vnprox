// The real implementation lives in src/flows/ (the flows feature module)
// alongside its own tests — this file only wires it to the routed /flows
// path App.tsx expects, per the existing per-route-file layout (see
// pages/AlertRulesPage.tsx/pages/BlueprintsPage.tsx).
export { FlowExplorer as FlowExplorerPage } from "../flows/FlowExplorer";
