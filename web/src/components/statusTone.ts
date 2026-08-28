// SPDX-License-Identifier: Apache-2.0

// The semantic status scale's five real health states
// (docs/design-language.md §2.2), shared by Badge.tsx and Banner.tsx so the
// two don't each redeclare the same union and drift apart. "stale" is
// deliberately excluded: it is a freshness qualifier layered on top of one
// of these five, never a state of its own — it has no `-solid`/`-soft`
// token role (index.css, re-asserted by index.css.test.ts's own
// STATUS_STATES list, which omits it for the identical reason). Badge's
// `stale` prop (see its own doc comment) is how that qualifier is applied.
export type StatusTone = "ok" | "degraded" | "critical" | "info" | "unknown";

export const STATUS_TONES: readonly StatusTone[] = ["ok", "degraded", "critical", "info", "unknown"];
