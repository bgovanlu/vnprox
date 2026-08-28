// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/nftables/ (T-3904's compiled-
// ruleset inspector feature module) alongside its own tests — this file
// only wires it to the routed /firewall/compiled path App.tsx expects,
// per the existing per-route-file layout (see pages/RouteExplorerPage.tsx).
export { CompiledRulesetPage } from "../nftables/CompiledRulesetPage";
