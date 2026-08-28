// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/mgmt/ (the management-interface
// feature module) alongside its own tests — this file only wires it to the
// routed /management path App.tsx expects, per the existing per-route-file
// layout (see pages/SdnPage.tsx/pages/TopologyPage.tsx).
export { ManagementPage } from "../mgmt/ManagementPage";
