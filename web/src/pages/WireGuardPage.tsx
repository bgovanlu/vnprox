// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/wireguard/ (the WireGuard feature
// module: tunnel/peer list, create/edit/delete, key viewer) alongside its
// own tests — this file only wires it to the routed /wireguard path
// App.tsx expects, per the existing per-route-file layout (see
// pages/SdnPage.tsx/pages/ManagementPage.tsx).
export { WireGuardPage } from "../wireguard/WireGuardPage";
