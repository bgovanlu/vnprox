// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/incidents/ (T-2804) alongside its own
// tests — this file only wires it to the routed /incidents path App.tsx
// expects, per the per-route-file layout T-005 established.
export { IncidentsPage } from "../incidents/IncidentsPage";
