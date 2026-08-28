// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/settings/ (the settings feature
// module) alongside its own tests — this file only wires it to the routed
// /settings/alert-rules path App.tsx expects, per the existing
// per-route-file layout (see pages/BlueprintsPage.tsx/pages/SettingsPage.tsx).
export { AlertRules as AlertRulesPage } from "../settings/AlertRules";
