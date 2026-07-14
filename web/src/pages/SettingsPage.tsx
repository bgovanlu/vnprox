// The real implementation lives in src/settings/ (the settings feature
// module) alongside its own tests — this file only wires it to the routed
// /settings path App.tsx expects, per the existing per-route-file layout
// (see pages/SdnPage.tsx/pages/ManagementPage.tsx).
export { SettingsPage } from "../settings/SettingsPage";
