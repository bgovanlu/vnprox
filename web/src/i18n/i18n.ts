// SPDX-License-Identifier: Apache-2.0

// T-3106: minimal i18n scaffolding. This module is the framework's single
// initialization point — every consumer either imports it for its
// side-effect (wiring the shared i18next instance react-i18next's
// useTranslation() falls back to when no <I18nextProvider> is present, the
// same "import once, use anywhere" pattern this repo's other
// composition-root singletons follow — see lib/queryClient.ts) or, in
// main.tsx, imports the default export to pass into <I18nextProvider>
// explicitly.
//
// Resources are static JSON imports (Vite's resolveJsonModule bundling),
// never a runtime fetch: web/public/sw.js is a real service worker and the
// app's CSP forbids external hosts (docs/architecture.md §9,
// CLAUDE.md), so a CDN-hosted translation backend was never on the table.
//
// SCOPE (T-3106's bounded subset): only the "onboarding" namespace exists
// today, covering web/src/onboarding/OnboardingWalkthrough.tsx — see that
// file's own doc comment and the task's report for why that's the chosen
// boundary. Extending localization to a new area means adding that area's
// own namespace here (its own locales/<lang>/<namespace>.json pair) rather
// than growing "onboarding" past its own screen.
//
// SHIPPED LOCALE: English only. `fr` exists to prove the pipeline
// round-trips end-to-end (translated JSON, real i18next pluralization,
// <Trans/> interpolation) and is exercised only by tests calling
// `i18n.changeLanguage("fr")` directly — there is no browser-language
// auto-detection and no user-facing locale switcher, so a French-speaking
// visitor's browser never sees the (machine-quality, unreviewed) `fr`
// strings in production. Shipping `fr` for real would need a native
// review pass and a switcher; neither is this card's job.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import onboardingEn from "./locales/en/onboarding.json";
import onboardingFr from "./locales/fr/onboarding.json";

export const defaultNS = "onboarding" as const;

export const resources = {
  en: { onboarding: onboardingEn },
  fr: { onboarding: onboardingFr },
} as const;

// i18next's .init() returns a Promise for consistency with backend
// plugins that fetch resources asynchronously; with every resource bundled
// synchronously (no i18next-http-backend, no CDN) it resolves on the same
// tick, so callers don't need to await it before rendering — the same
// "fire and forget, the effect is synchronous in practice" shape this
// codebase already uses for e.g. registerServiceWorker() in main.tsx.
void i18n.use(initReactI18next).init({
  lng: "en",
  fallbackLng: "en",
  ns: [defaultNS],
  defaultNS,
  resources,
  interpolation: { escapeValue: false }, // React already escapes
  returnNull: false,
});

export default i18n;
