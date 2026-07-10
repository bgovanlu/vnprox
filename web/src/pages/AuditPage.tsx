// The real implementation lives in src/audit/ (filter form, paginated
// table, expandable rows) alongside its own tests — this file only wires
// it to the routed /audit path App.tsx expects, per the existing
// per-route-file layout T-005 established.
export { AuditPage } from "../audit/AuditPage";
