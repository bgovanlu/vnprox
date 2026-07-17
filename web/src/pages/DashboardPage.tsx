// The real implementation lives in src/dashboard/ (DashboardPage.tsx plus
// one component per tile) alongside its own tests — this file only wires
// it to the routed "/" index path App.tsx expects, per the existing
// per-route-file layout T-005 established.
export { DashboardPage } from "../dashboard/DashboardPage";
