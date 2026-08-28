// SPDX-License-Identifier: Apache-2.0

// T-4110 (LACP hash visualizer, hardware-flagged — see
// planning/reports/needs-hardware-validation.md's T-4110 entry under
// "needs real NICs"). Renders inside the bond inspector's existing "LACP"
// tab, alongside (not replacing) BondLacpSection's live actor/partner
// state (T-804): which slave the kernel's xmit_hash_policy would predict
// for each recently-observed flow, and — where a live per-slave rate
// sample exists (internal/metrics.Sampler, itself reading
// /proc/net/bonding-derived slave counters) — the actual observed rate
// beside it. Divergence between predicted and actual is the useful
// signal: it can reveal a hash policy that isn't distributing traffic the
// way it's assumed to, on this cluster's real traffic mix, without
// needing a real switch to observe that specific claim (confirming the
// kernel's own per-flow decision, and a real switch's independent
// receive-side hash agreeing with it, both still need the hardware pass
// the needs-hardware-validation entry names).
//
// Client-side computation only, mirroring web/src/dashboard/
// serviceClassBreakdown.ts's and topology/layers/k8sFlowAttribution.ts's
// own "kept in sync by hand with the Go implementation, no new backend
// round trip" convention (see lacpHashPredict.ts's own header comment for
// the full reasoning and internal/lacphash for the Go original).
import { useMemo } from "react";
import clsx from "clsx";
import { HelpAnchor } from "../help/HelpAnchor";
import type { WsClient } from "../api/ws";
import { useFlowsQuery } from "../flows/flowsQueries";
import {
  isLacpHashPolicy,
  isPredictablePolicy,
  predictSlaveDistribution,
  type HashSlave,
  type LacpHashPolicy,
  type PredictionResult,
} from "./lacpHashPredict";
import { formatBps } from "./metricsFormat";
import { useLiveMetrics } from "./metricsQueries";

/** The minimal shape this section reads off `fields.SlaveDetail` — a
 * narrower cousin of BondLacpSection.tsx's own BondSlaveDetailRow (this
 * section only needs a slave's name and MII status, not its LACP PDU
 * detail). Same wire-shape caveat applies: internal/topology.Detail
 * marshals *inventory.Bond straight through, so these arrive with
 * capitalized Go field names. */
interface SlaveDetailRow {
  Name: string;
  MIIStatus: string;
}

function isSlaveDetailRows(v: unknown): v is SlaveDetailRow[] {
  return (
    Array.isArray(v) && v.every((row) => typeof row === "object" && row !== null && "Name" in row && "MIIStatus" in row)
  );
}

/** A predicted/actual divergence worth flagging: a slave's actual share
 * of total bond bytes differs from its predicted share by more than this
 * many percentage points. Deliberately generous (not a precision claim —
 * see this file's header) so a genuinely lopsided distribution stands
 * out without every minor sampling wobble lighting up. */
const DIVERGENCE_THRESHOLD_PCT = 20;

export interface LacpHashSectionProps {
  /** The bond's own node (EntityDetail.node) — flow records are
   * attributed to this bond by node match, not by per-interface ifIndex
   * correlation (vnprox maintains no ifIndex-to-bond mapping; see
   * internal/lacphash's predict.go composition, which makes the same
   * approximation server-side). */
  node: string;
  fields: Record<string, unknown>;
  /** Injectable WS client for tests — see MetricsTab's identical prop. */
  wsClient?: WsClient;
}

export function LacpHashSection({ node, fields, wsClient }: LacpHashSectionProps) {
  const policyRaw = typeof fields.XmitHashPolicy === "string" ? fields.XmitHashPolicy : "";
  const slaveDetail = fields.SlaveDetail;
  const slaveRows = useMemo(() => (isSlaveDetailRows(slaveDetail) ? slaveDetail : []), [slaveDetail]);

  const slaves: HashSlave[] = useMemo(
    () =>
      slaveRows.map((s) => ({
        ref: `physnic:${node}:${s.Name}`,
        name: s.Name,
        up: s.MIIStatus.toLowerCase() === "up",
      })),
    [slaveRows, node],
  );
  const slaveRefs = useMemo(() => slaves.map((s) => s.ref), [slaves]);

  const policy: LacpHashPolicy | undefined = isLacpHashPolicy(policyRaw) ? policyRaw : undefined;
  const predictable = policy !== undefined && isPredictablePolicy(policy);

  // GET /flows carries no `node=` filter (docs/api.md's Flows section) —
  // fetch the most recent cluster-wide window and filter to this bond's
  // own node client-side.
  const flowsEnabled = predictable && slaves.some((s) => s.up);
  const { data: flowsPage, isLoading: flowsLoading } = useFlowsQuery({ limit: 500 }, flowsEnabled);
  const nodeFlows = useMemo(() => (flowsPage?.items ?? []).filter((r) => r.node === node), [flowsPage, node]);

  const live = useLiveMetrics(slaveRefs, slaveRefs.length > 0, wsClient);

  const prediction = useMemo(
    () => (policy && predictable ? predictSlaveDistribution(policy, slaves, nodeFlows) : undefined),
    [policy, predictable, slaves, nodeFlows],
  );

  if (slaveRows.length === 0) {
    // BondLacpSection already renders "No slave detail reported for this
    // bond yet" immediately above this section — no second empty state
    // saying the same thing.
    return null;
  }

  return (
    <div className="mt-4 space-y-2 border-t border-slate-200 pt-3 text-xs dark:border-slate-700">
      <p className="flex items-center gap-1 font-medium text-slate-600 dark:text-slate-300">
        Predicted hash distribution
        <HelpAnchor topic="bond-hash-distribution" />
      </p>

      {!policy && (
        <p className="text-slate-600 dark:text-slate-400">
          {policyRaw
            ? `Unrecognized xmit_hash_policy "${policyRaw}" — nothing to predict.`
            : "No xmit_hash_policy configured on this bond — nothing to predict."}
        </p>
      )}

      {policy && !predictable && (
        <p className="text-slate-600 dark:text-slate-400">
          Cannot predict slave selection for <span className="font-mono">{policy}</span>: this policy hashes on
          Ethernet source/destination MAC addresses, which vnprox&apos;s flow records (sFlow/NetFlow/IPFIX/conntrack)
          never carry. Only <span className="font-mono">layer3+4</span>/<span className="font-mono">encap3+4</span>{" "}
          predictions are computable from observed flow data.
        </p>
      )}

      {policy && predictable && !slaves.some((s) => s.up) && (
        <p className="text-slate-600 dark:text-slate-400">No eligible (up) slaves to predict against.</p>
      )}

      {policy && predictable && slaves.some((s) => s.up) && flowsLoading && (
        <p className="text-slate-600 dark:text-slate-400">Loading recent flow data…</p>
      )}

      {policy &&
        predictable &&
        slaves.some((s) => s.up) &&
        !flowsLoading &&
        nodeFlows.length === 0 && (
          <p className="text-slate-600 dark:text-slate-400">
            No flow records observed for this bond&apos;s node yet — flow ingestion may be disabled for this node, or
            no traffic has been sampled in the retained window.
          </p>
        )}

      {prediction && prediction.slaves.length > 0 && nodeFlows.length > 0 && (
        <PredictionTable prediction={prediction} live={live} />
      )}
    </div>
  );
}

function PredictionTable({
  prediction,
  live,
}: {
  prediction: PredictionResult;
  live: ReturnType<typeof useLiveMetrics>;
}) {
  const totalPredictedBytes = prediction.slaves.reduce((sum, s) => sum + s.bytes, 0);
  const rows = prediction.slaves.map((s) => {
    const predictedPct = totalPredictedBytes > 0 ? (s.bytes / totalPredictedBytes) * 100 : 0;
    return { ...s, predictedPct };
  });
  const actualTotalBps = rows.reduce((sum, r) => {
    const m = live.get(r.ref);
    return sum + (m ? m.rates.rxBps + m.rates.txBps : 0);
  }, 0);

  return (
    <div className="space-y-2">
      {prediction.unclassified > 0 && (
        <p className="text-slate-500 dark:text-slate-400">
          {prediction.unclassified} of {prediction.classified + prediction.unclassified} recent flow(s) could not be
          classified{prediction.unclassifiedReason ? ` (${prediction.unclassifiedReason})` : ""}.
        </p>
      )}
      <table className="w-full border-collapse">
        <thead>
          <tr className="text-left text-slate-500 dark:text-slate-400">
            <th className="pb-1 font-normal">Slave</th>
            <th className="pb-1 font-normal">Predicted</th>
            <th className="pb-1 font-normal">Actual</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const m = live.get(r.ref);
            const actualBps = m ? m.rates.rxBps + m.rates.txBps : undefined;
            const actualPct = actualBps !== undefined && actualTotalBps > 0 ? (actualBps / actualTotalBps) * 100 : undefined;
            const divergent =
              actualPct !== undefined && Math.abs(actualPct - r.predictedPct) > DIVERGENCE_THRESHOLD_PCT;
            return (
              <tr key={r.ref} className="border-t border-slate-100 dark:border-slate-800">
                <td className="py-1 pr-2 text-slate-700 dark:text-slate-200">{r.name}</td>
                <td className="py-1 pr-2 text-slate-700 dark:text-slate-200">
                  {r.flows} flow{r.flows === 1 ? "" : "s"} ({r.predictedPct.toFixed(0)}%)
                </td>
                <td
                  className={clsx(
                    "py-1",
                    divergent ? "font-medium text-amber-600 dark:text-amber-400" : "text-slate-700 dark:text-slate-200",
                  )}
                >
                  {actualBps === undefined
                    ? "no live sample yet"
                    : `${formatBps(actualBps)}${actualPct !== undefined ? ` (${actualPct.toFixed(0)}%)` : ""}`}
                  {divergent && <span title="Predicted and actual share diverge by more than 20 points"> ⚠</span>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
