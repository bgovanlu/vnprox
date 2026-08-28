// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/routeexplorer/ (T-3903's route
// explorer feature module) alongside its own tests — this file only wires
// it to the routed /route-explorer path App.tsx expects, per the existing
// per-route-file layout (see pages/ConntrackPage.tsx).
export { RouteExplorerPage } from "../routeexplorer/RouteExplorerPage";
