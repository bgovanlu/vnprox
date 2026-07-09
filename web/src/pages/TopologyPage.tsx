// The real implementation lives in src/topology/ (the whole feature
// module: canvas, projection logic, queries, layout, inspector, search,
// keyboard wiring, ...) alongside its own tests — this file only wires it
// to the routed /topology path App.tsx expects, per the existing
// per-route-file layout T-005 established.
export { TopologyPage } from "../topology/TopologyPage";
