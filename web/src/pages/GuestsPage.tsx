// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/guests/ (the guest NIC list + bulk
// reattach feature module) — this file only wires it to the routed
// /guests path App.tsx expects, matching pages/TopologyPage.tsx's
// established per-route-file convention.
export { GuestsPage } from "../guests/GuestsPage";
