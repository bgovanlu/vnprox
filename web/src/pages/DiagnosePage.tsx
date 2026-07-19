// The real implementation lives in src/diagnose/ (the diagnose feature
// module) alongside its own tests — this file only wires it to the routed
// /diagnose path App.tsx expects, per the existing per-route-file layout
// (see pages/ConntrackPage.tsx).
export { DiagnosisPage as DiagnosePage } from "../diagnose/DiagnosisPage";
