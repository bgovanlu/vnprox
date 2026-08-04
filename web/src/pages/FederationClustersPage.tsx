// The real implementation lives in src/settings/ (the settings feature
// module) alongside its own tests — this file only wires it to the routed
// /settings/federation path App.tsx expects, per the existing
// per-route-file layout (see pages/AlertRulesPage.tsx).
export { FederationClusters as FederationClustersPage } from "../settings/FederationClusters";
