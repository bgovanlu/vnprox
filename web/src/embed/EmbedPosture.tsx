// SPDX-License-Identifier: Apache-2.0

// EmbedPosture (T-1706): a read-only embed of the network posture score &
// report (T-1607). Fetches GET /posture and renders the overall score plus
// the per-factor breakdown — every field the report already exposes, with no
// action chrome. A 404 (no score computed yet, or posture scoring not
// available on this instance) renders the documented "not yet available"
// state rather than an error, matching the backend view route's wired-but-
// dark posture handling.
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api/client";
import { fetchPosture } from "../api/posture";
import type { PostureFactor } from "../api/posture";

function scoreColor(overall: number): string {
  if (overall >= 80) return "text-emerald-600 dark:text-emerald-400";
  if (overall >= 50) return "text-amber-600 dark:text-amber-400";
  return "text-red-600 dark:text-red-400";
}

function FactorRow({ factor }: { factor: PostureFactor }) {
  return (
    <li className="flex items-start justify-between gap-3 border-b border-slate-100 py-1.5 text-sm last:border-0 dark:border-slate-800">
      <div className="min-w-0">
        <div className="font-medium">{factor.name}</div>
        <div className="text-xs text-slate-600 dark:text-slate-400">{factor.detail}</div>
        {factor.caveat ? (
          <div className="text-xs text-amber-600 dark:text-amber-400">{factor.caveat}</div>
        ) : null}
      </div>
      <div className="shrink-0 text-right">
        {factor.evaluated ? (
          <span className="tabular-nums font-semibold">{factor.scorePct}</span>
        ) : (
          <span className="text-xs uppercase tracking-wide text-slate-600 dark:text-slate-400">Not evaluated</span>
        )}
      </div>
    </li>
  );
}

export function EmbedPosture() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["embed", "posture"],
    queryFn: () => fetchPosture(),
    retry: false,
  });

  if (isLoading) {
    return <p className="text-sm text-slate-600 dark:text-slate-400">Loading posture…</p>;
  }

  if (error) {
    // A 404 is the honest "not yet available" state; anything else is a real
    // failure.
    if (error instanceof ApiError && error.status === 404) {
      return (
        <p className="text-sm text-slate-600 dark:text-slate-400" data-embed-state="posture-unavailable">
          Network posture scoring is not available on this instance yet.
        </p>
      );
    }
    return (
      <p className="text-sm text-red-600 dark:text-red-400" data-testid="embed-posture-error">
        Could not load the posture score.
      </p>
    );
  }

  if (!data) {
    return null;
  }

  return (
    <div className="flex flex-col gap-4" data-testid="embed-posture">
      <div className="flex items-baseline gap-3">
        <span className={`text-4xl font-bold tabular-nums ${scoreColor(data.overall)}`}>{data.overall}</span>
        <span className="text-sm text-slate-600 dark:text-slate-400">/ 100 overall</span>
        {data.qualified ? (
          <span className="rounded bg-amber-100 px-2 py-0.5 text-xs text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
            Qualified — some dimensions unknown
          </span>
        ) : null}
      </div>
      <ul className="flex flex-col">
        {data.factors.map((f) => (
          <FactorRow key={f.name} factor={f} />
        ))}
      </ul>
    </div>
  );
}
