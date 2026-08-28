// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/ports/ (the LLDP ports feature
// module) alongside its own tests — this file only wires it to the routed
// /ports path App.tsx expects, per the existing per-route-file layout (see
// pages/SdnPage.tsx/pages/ManagementPage.tsx).
export { PortsPage } from "../ports/PortsPage";
