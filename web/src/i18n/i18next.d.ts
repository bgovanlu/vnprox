// SPDX-License-Identifier: Apache-2.0

// Module augmentation so `useTranslation("onboarding").t(...)` is checked
// against the real resource shape at compile time — the strict-TS
// alternative to typing `t()`'s key/value arguments as `string`/`any`.
// A typo'd or removed key is a type error here, not a silent runtime
// fallback to the raw key string.
//
// Named i18next.d.ts rather than i18n.d.ts deliberately: tsc treats a
// same-basename `.d.ts`/`.ts` pair in one directory as a declaration/
// implementation companion pair and silently drops the `.d.ts` from its
// root file list (verified — `i18n.d.ts` next to `i18n.ts` here was never
// type-checked at all, and `t()` calls happily accepted typo'd keys with
// no error; `tsc --showConfig`'s resolved "files" list is what caught it).
// A distinct basename avoids the collision entirely.
import "i18next";
import type { defaultNS, resources } from "./i18n";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: typeof defaultNS;
    resources: (typeof resources)["en"];
  }
}
