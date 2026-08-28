// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/sdn/ (the SDN cockpit feature
// module: tree + detail, pending diff, status painting) alongside its own
// tests — this file only wires it to the routed /sdn path App.tsx expects,
// per the existing per-route-file layout T-005 established (see
// pages/TopologyPage.tsx/pages/GuestsPage.tsx).
export { SdnPage } from "../sdn/SdnPage";
