// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/ipam/ (the IPAM feature module:
// subnet list, allocation grid, conflicts, reserve/release) alongside its
// own tests — this file only wires it to the routed /ipam path App.tsx
// expects, per the existing per-route-file layout T-005 established (see
// pages/SdnPage.tsx).
export { IpamPage } from "../ipam/IpamPage";
