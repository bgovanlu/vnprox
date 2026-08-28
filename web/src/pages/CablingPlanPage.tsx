// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/cabling/ (T-3907's physical cabling
// plan feature module) alongside its own tests — this file only wires it to
// the routed /cabling path App.tsx expects, per the existing per-route-file
// layout (see pages/PortsPage.tsx).
export { CablingPlanView as CablingPlanPage } from "../cabling/CablingPlanView";
