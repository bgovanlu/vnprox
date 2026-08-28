// SPDX-License-Identifier: Apache-2.0

// T-3503: the faceplate's port bodies — the small SVG jacks that make a
// Proxmox bridge read as a switch rather than as a card with text in it.
//
// Everything here is presentational: no data fetching, no store, no
// callbacks. The *choice* of body (which jack a port gets, what speed
// marking it carries) lives in portMedia.ts, framework-free and unit-tested
// without rendering — see that module's doc comment for why the drawn shape
// follows the media type and never the negotiated speed.
import clsx from "clsx";
import type { EntityStatus } from "../api/types";
import type { PortBodyKind } from "./portMedia";

// Per-status stroke/fill for the jack itself. A down port is drawn in the
// same red family its LED uses, but the shape carries the signal too (see
// StatusLed's glyphs) so nothing here is colour-only.
const BODY_CLASS: Record<EntityStatus, string> = {
  ok: "text-slate-500 dark:text-slate-400",
  down: "text-red-500 dark:text-red-400",
  degraded: "text-amber-600 dark:text-amber-400",
  unknown: "text-slate-400 dark:text-slate-500",
};

/**
 * One drawn jack, 28×22 in its own coordinate space and sized by the caller.
 *
 * `currentColor` throughout so a single Tailwind text-* class on the <svg>
 * colours the whole body, and every fill is `none` except the contacts —
 * these have to read at ~24px wide, where a filled shape becomes a blob.
 */
export function PortJack({ kind, status, className }: { kind: PortBodyKind; status: EntityStatus; className?: string }) {
  return (
    <svg
      viewBox="0 0 28 22"
      aria-hidden
      focusable="false"
      className={clsx("h-[18px] w-[23px] shrink-0", BODY_CLASS[status], className)}
    >
      {kind === "sfp" ? <SfpCage /> : kind === "unknown" ? <UnknownBody /> : <Rj45Body virtual={kind === "virtual"} />}
    </svg>
  );
}

/** An RJ45 jack seen face-on: the keyed body with the latch-tab slot cut out
 * of the bottom edge, and the eight contacts at the top. `virtual` draws the
 * same outline dashed — a guest NIC's access port is a real port of a real
 * (virtual) switch, so it keeps the jack shape, but the dash says the socket
 * is not one you can put a cable into. */
function Rj45Body({ virtual }: { virtual: boolean }) {
  return (
    <g fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round">
      <path d="M3 3 h22 v11 h-7 v5 h-8 v-5 h-7 z" strokeDasharray={virtual ? "2 1.5" : undefined} />
      {/* Eight contacts. Drawn as one path rather than eight <line>s so the
          whole contact block is a single node in the rendered SVG. */}
      <path d="M6 5v4M8.5 5v4M11 5v4M13.5 5v4M16 5v4M18.5 5v4M21 5v4M23.5 5v4" strokeWidth="0.9" opacity="0.75" />
    </g>
  );
}

/** An SFP/SFP+ cage seen face-on: the outer cage, the transceiver slot, and
 * the bale-clasp bar on the left. */
function SfpCage() {
  return (
    <g fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round">
      <rect x="2.5" y="4" width="23" height="14.5" rx="1.5" />
      <rect x="5.5" y="7" width="17" height="8.5" rx="0.75" strokeWidth="0.9" opacity="0.8" />
      <path d="M7.5 7v8.5" strokeWidth="0.9" opacity="0.8" />
    </g>
  );
}

/** No reading: a plain socket outline with a question of a gap where the
 * keying would be. Distinct in silhouette from both of the above, so an
 * operator can tell "we don't know" from "we know it's copper" without
 * reading the label. */
function UnknownBody() {
  return (
    <g fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round">
      <rect x="3" y="4" width="22" height="14.5" rx="1.5" strokeDasharray="3 2" />
      <path d="M11 11.5h6" strokeWidth="0.9" opacity="0.7" />
    </g>
  );
}

// Status LEDs. Colour AND shape: WCAG 1.4.1 (and T-905/T-3401's "no status
// conveyed by colour alone") means an operator with a colour-vision
// deficiency, or looking at a greyscale-rendered stale faceplate, must still
// be able to tell these four apart. Real switch LEDs cannot do this — these
// are drawn glyphs, so they can.
const LED_FILL: Record<EntityStatus, string> = {
  ok: "bg-emerald-500",
  down: "bg-red-500",
  degraded: "bg-amber-500",
  unknown: "bg-transparent ring-1 ring-slate-400 dark:ring-slate-500",
};

/** The per-port link LED. `ok` is a solid dot, `down` a dot with a cut-out
 * bar, `degraded` a half-filled dot, `unknown` a hollow ring. */
export function StatusLed({ status, className }: { status: EntityStatus; className?: string }) {
  return (
    <span
      aria-hidden
      className={clsx(
        "relative inline-block h-2 w-2 shrink-0 rounded-full",
        LED_FILL[status],
        status === "degraded" &&
          // Half-filled: the amber fill with the top half masked back to the
          // surface colour, so the glyph differs from `ok` in shape as well
          // as hue.
          "before:absolute before:inset-x-0 before:top-0 before:h-1/2 before:rounded-t-full before:bg-white dark:before:bg-slate-900",
        status === "down" &&
          // A horizontal cut through the dot — the "link lost" glyph.
          "after:absolute after:inset-x-0 after:top-1/2 after:h-[1.5px] after:-translate-y-1/2 after:bg-white dark:after:bg-slate-900",
        className,
      )}
    />
  );
}
