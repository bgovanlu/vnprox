// SPDX-License-Identifier: Apache-2.0

// T-4208, promoted from two hand-rolled linear bars that were already the
// same `h-1.5 rounded-full` track-plus-fill shape with different colour
// logic bolted on:
//
//   - ipam/IpamPage.tsx's `UtilizationBar` — single value, accent fill that
//     turns red on conflict. Grounds the single-`value` mode below.
//   - ipam/AddressList.tsx's `UtilizationStrip` — several `stateSwatchClasses`
//     segments side by side summing to the whole. Grounds the `segments`
//     mode below; segment colouring stays the caller's own taxonomy
//     palette (ipam/labels.ts's free/allocated/reserved/observed/gateway
//     colours are a KIND of address, not a health state — see that file's
//     T-4204 comment — so Progress does not try to force them onto the
//     status scale itself).
import clsx from "clsx";
import type { StatusTone } from "./statusTone";

export type ProgressTone = "accent" | StatusTone;

const TONE_CLASSES: Record<ProgressTone, string> = {
  accent: "bg-accent-600",
  ok: "bg-status-ok-solid",
  degraded: "bg-status-degraded-solid",
  critical: "bg-status-critical-solid",
  info: "bg-status-info-solid",
  unknown: "bg-status-unknown-solid",
};

export interface ProgressSegment {
  /** Percent of the track (0-100). Segments are drawn in order, left to
   * right; the caller is responsible for keeping the total at or under
   * 100 (a total under 100 just leaves the remainder as bare track, which
   * is a legitimate "free space" rendering — see AddressList's own use). */
  percent: number;
  /** A caller-owned fill class, e.g. one of ipam/labels.ts's
   * `stateSwatchClasses` entries — Progress does not invent a palette for
   * segment mode, only the track/segment layout. */
  className: string;
  /** Used as the segment's `title` (hover tooltip) when given. */
  label?: string;
}

export interface ProgressProps {
  size?: "sm" | "md";
  /** Accessible label for the bar as a whole (`aria-label`). */
  label?: string;
  className?: string;
}

export interface SingleProgressProps extends ProgressProps {
  /** 0-100. Values outside that range are clamped. */
  value: number;
  tone?: ProgressTone;
  segments?: undefined;
  /** Render "NN%" to the right of the bar, IpamPage.tsx's own rendering. */
  showValueText?: boolean;
}

export interface SegmentedProgressProps extends ProgressProps {
  value?: undefined;
  segments: readonly ProgressSegment[];
  tone?: undefined;
  showValueText?: undefined;
}

const TRACK_HEIGHT: Record<"sm" | "md", string> = { sm: "h-1.5", md: "h-2" };

/** A linear progress/utilization bar. Two modes, picked by which of
 * `value`/`segments` is passed: a single filled value (a percentage,
 * `role="progressbar"`), or several caller-coloured segments side by side
 * summing to a whole (a breakdown — free/used/reserved, and similar). */
export function Progress(props: SingleProgressProps | SegmentedProgressProps) {
  const { size = "sm", label, className } = props;
  const track = clsx("w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700", TRACK_HEIGHT[size]);

  if (props.segments) {
    return (
      <div className={clsx("flex items-center gap-2", className)}>
        <div className={clsx(track, "flex")} role={label ? "img" : undefined} aria-label={label}>
          {props.segments.map((seg, i) => (
            // A fixed-count, order-stable list of caller-supplied segments.
            <div
              key={i}
              className={clsx("h-full", seg.className)}
              style={{ width: `${String(Math.max(0, seg.percent))}%` }}
              title={seg.label}
            />
          ))}
        </div>
      </div>
    );
  }

  const pct = Math.min(100, Math.max(0, props.value));
  return (
    <div className={clsx("flex items-center gap-2", className)}>
      <div
        className={track}
        role="progressbar"
        aria-label={label}
        aria-valuenow={Math.round(pct)}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className={clsx(
            "h-full rounded-full transition-[width] duration-[var(--motion-base)] ease-standard",
            TONE_CLASSES[props.tone ?? "accent"],
          )}
          style={{ width: `${String(pct)}%` }}
        />
      </div>
      {props.showValueText ? <span className="text-xs text-slate-600 dark:text-slate-400">{Math.round(pct)}%</span> : null}
    </div>
  );
}
