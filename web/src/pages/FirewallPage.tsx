// The real implementation lives in src/firewall/ (T-501's read views) —
// this file only wires it to the routed /firewall path App.tsx expects,
// matching pages/GuestsPage.tsx's established per-route-file convention.
export { FirewallPage } from "../firewall/FirewallPage";
