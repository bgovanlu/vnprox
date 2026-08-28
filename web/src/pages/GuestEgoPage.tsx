// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/guest/ (T-3906's guest ego view
// feature module) alongside its own tests — this file only wires it to the
// routed /guest path App.tsx expects, per the existing per-route-file
// layout (see pages/ConntrackPage.tsx).
export { GuestEgoView as GuestEgoPage } from "../guest/GuestEgoView";
