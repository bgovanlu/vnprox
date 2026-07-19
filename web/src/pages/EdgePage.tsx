// The real implementation lives in src/edge/ (the Edge & NAT cockpit
// feature module) alongside its own tests — this file only wires it to the
// routed /edge path App.tsx expects, per the existing per-route-file layout
// (see pages/ConntrackPage.tsx).
export { EdgeCockpit as EdgePage } from "../edge/EdgeCockpit";
