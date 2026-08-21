// WAN & upstream health (GET /wan/status, GET/PUT /wan/targets, T-1405) —
// the "it's the ISP, not the cluster" answer, on the page that already
// explains how traffic leaves.
//
// Two things this panel is careful about:
//
//   1. **The verdict is the daemon's, not this component's.** `verdict` and
//      `summary` come from internal/api/wan.go's computeWanVerdict, which is
//      the only place that has both the probe results and the findings
//      stream. `likely_isp` is deliberately narrower than `wan_degraded` —
//      it means "WAN is degraded AND the rest of the cluster is quiet". This
//      component renders those as distinct verdicts and never upgrades one
//      into the other, and an unrecognised verdict renders as unknown.
//   2. **A rejected target is named, not swallowed.** T-2905 constrains a
//      target host to an IP literal or an RFC 1123 hostname, because a
//      target is dialed by root-owned probers. The daemon's own
//      `validation_failed` message names the offending host; the form shows
//      it verbatim rather than replacing it with a generic failure.
//
// There is no failover here and there never will be: no changeset op exists
// for WAN uplink switching, and neither GET /wan/status nor the probe loop
// calls a mutating route (T-1405 AC5).
import { useState } from "react";
import type { FormEvent } from "react";
import clsx from "clsx";
import { ApiError } from "../api/client";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import type { WanTarget, WanUplinkStatus } from "../api/types";
import { useEdgeRoutesQuery, useReplaceWanTargetsMutation, useWanStatusQuery, useWanTargetsQuery } from "./edgeQueries";

/** The four verdicts docs/api.md documents, plus the unknown arm. An
 * unrecognised value must not be rendered as one of the four. */
const VERDICT_CLASS: Readonly<Record<string, string>> = {
  healthy: "border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-100",
  likely_isp: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100",
  wan_degraded: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100",
  no_targets: "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300",
};

const VERDICT_LABEL: Readonly<Record<string, string>> = {
  healthy: "WAN healthy",
  likely_isp: "Likely your ISP, not the cluster",
  wan_degraded: "WAN degraded",
  no_targets: "No reference targets configured",
};

const UNKNOWN_VERDICT_CLASS =
  "border-violet-300 bg-violet-50 text-violet-900 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-100";

/** Per-uplink status: `healthy`|`degraded`|`unreachable` on the wire. */
const UPLINK_STATUS_CLASS: Readonly<Record<string, string>> = {
  healthy: "text-emerald-700 dark:text-emerald-400",
  degraded: "text-amber-700 dark:text-amber-400",
  unreachable: "text-red-700 dark:text-red-400",
};

export function WanHealthPanel() {
  const statusQuery = useWanStatusQuery();
  const targetsQuery = useWanTargetsQuery();

  return (
    <section aria-label="WAN health" className="flex flex-col gap-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        WAN &amp; upstream health
        <HelpAnchor topic="wan-health" />
      </h3>
      <p className="text-xs text-slate-500 dark:text-slate-400">
        Continuous reachability, latency and loss against reference hosts you name, per uplink. This node probes its own
        targets only — the cluster-wide picture is the union of each node&apos;s own view. Nothing here fails over or
        switches an uplink; it is a diagnosis, not an actuator.
      </p>

      {statusQuery.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {statusQuery.error && (
        <EmptyState
          title="Could not read WAN status"
          description="The daemon could not read the probe results. Try again in a moment."
          density="compact"
        />
      )}

      {!statusQuery.isLoading && !statusQuery.error && statusQuery.data && (
        <>
          <VerdictBanner verdict={statusQuery.data.verdict} summary={statusQuery.data.summary} />
          <UplinksTable uplinks={statusQuery.data.uplinks ?? []} />
        </>
      )}

      <TargetsEditor node={targetsQuery.data?.node} targets={targetsQuery.data?.targets ?? []} />
    </section>
  );
}

function VerdictBanner({ verdict, summary }: { verdict: string; summary: string }) {
  const known = VERDICT_LABEL[verdict];
  return (
    <div
      role={verdict === "healthy" ? "status" : "alert"}
      data-testid="wan-verdict"
      className={clsx(
        "flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm",
        VERDICT_CLASS[verdict] ?? UNKNOWN_VERDICT_CLASS,
      )}
    >
      <span className="font-semibold">{known ?? "Verdict not recognised"}</span>
      {/* The daemon's own sentence, rendered verbatim — it is the one place
       * the probe results and the findings stream have both been seen. */}
      <span>{summary}</span>
    </div>
  );
}

function UplinksTable({ uplinks }: { uplinks: WanUplinkStatus[] }) {
  if (uplinks.length === 0) {
    return (
      <EmptyState
        title="No uplink readings yet"
        description="An uplink appears once at least one of its configured targets has produced a probe reading. A configured target with no reading yet has no entry at all, rather than a placeholder."
        density="compact"
      />
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Node</TableHead>
          <TableHead>Uplink</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Availability</TableHead>
          <TableHead>RTT</TableHead>
          <TableHead>Loss</TableHead>
          <TableHead>Targets</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {uplinks.map((u) => (
          <TableRow key={`${u.node}-${u.uplink}`}>
            <TableCell>{u.node}</TableCell>
            <TableCell className="font-mono text-xs">{u.uplink}</TableCell>
            <TableCell className={UPLINK_STATUS_CLASS[u.status] ?? "text-violet-700 dark:text-violet-400"}>
              {u.status}
            </TableCell>
            <TableCell className="tabular-nums">{u.availabilityPct.toFixed(1)}%</TableCell>
            <TableCell className="tabular-nums">{u.rttMs.toFixed(1)} ms</TableCell>
            <TableCell className="tabular-nums">{u.lossPct.toFixed(1)}%</TableCell>
            <TableCell className="text-xs">{describeTargets(u)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function describeTargets(u: WanUplinkStatus): string {
  const targets = u.targets ?? [];
  if (targets.length === 0) return "—";
  return targets
    .map((t) => `${t.host} ${t.reachable ? `${t.rollingRttMs.toFixed(0)}ms` : "unreachable"}`)
    .join(", ");
}

/** The one write in T-3004's whole set: PUT /wan/targets, a full-set
 * replace of this node's own reference targets. */
function TargetsEditor({ node, targets }: { node: string | undefined; targets: WanTarget[] }) {
  const { data: session } = useSession();
  const routesQuery = useEdgeRoutesQuery();
  const mutation = useReplaceWanTargetsMutation();
  const [uplink, setUplink] = useState("");
  const [host, setHost] = useState("");
  const [refusal, setRefusal] = useState<string | undefined>(undefined);

  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");
  // docs/api.md: `uplink` is a caller-chosen label, and "in practice the web
  // client populates it from GET /edge/routes' DefaultRoute.Iface list".
  // The route does not validate it, so an operator can still type one in.
  const uplinkOptions = [...new Set((routesQuery.data?.defaultRoutes ?? []).map((r) => r.iface))].sort();
  const chosenUplink = uplink !== "" ? uplink : (uplinkOptions[0] ?? "");

  function submit(next: WanTarget[]) {
    setRefusal(undefined);
    mutation.mutate(next, {
      onError: (err) => {
        // A rejected host is a named refusal, not a generic failure: the
        // daemon's own message names the offending value.
        setRefusal(err instanceof ApiError ? err.message : String(err));
      },
    });
  }

  function handleAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!chosenUplink || host.trim() === "") return;
    submit([...targets, { uplink: chosenUplink, host: host.trim() }]);
    setHost("");
  }

  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
        Reference targets{node ? ` on ${node}` : ""}
      </h4>
      {targets.length === 0 ? (
        <EmptyState
          title="No reference targets configured"
          description="Name a well-known host per uplink — a resolver, a gateway — and vnprox will probe it continuously so an upstream problem can be told apart from a cluster one."
          density="compact"
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Uplink</TableHead>
              <TableHead>Host</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {targets.map((t) => (
              <TableRow key={`${t.uplink}-${t.host}`}>
                <TableCell className="font-mono text-xs">{t.uplink}</TableCell>
                <TableCell className="font-mono text-xs">{t.host}</TableCell>
                <TableCell>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={!canWrite || mutation.isPending}
                    title={writeDisabledReason}
                    onClick={() => {
                      // PUT is always a full-set replace, never a partial
                      // patch — removing means sending the rest.
                      submit(targets.filter((x) => !(x.uplink === t.uplink && x.host === t.host)));
                    }}
                  >
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <form onSubmit={handleAdd} className="mt-3 flex flex-wrap items-end gap-2" aria-label="Add WAN reference target">
        <label className="flex flex-col gap-1 text-xs">
          Uplink
          {uplinkOptions.length > 0 ? (
            <select
              value={chosenUplink}
              onChange={(e) => {
                setUplink(e.target.value);
              }}
              disabled={!canWrite}
              className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
            >
              {uplinkOptions.map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </select>
          ) : (
            <input
              type="text"
              value={uplink}
              onChange={(e) => {
                setUplink(e.target.value);
              }}
              disabled={!canWrite}
              placeholder="vmbr0"
              className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
            />
          )}
        </label>
        <label className="flex flex-col gap-1 text-xs">
          Host
          <input
            type="text"
            value={host}
            onChange={(e) => {
              setHost(e.target.value);
            }}
            disabled={!canWrite}
            placeholder="1.1.1.1"
            className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        <Button type="submit" size="sm" disabled={!canWrite || host.trim() === "" || mutation.isPending} title={writeDisabledReason}>
          Add target
        </Button>
      </form>

      {refusal !== undefined && (
        <p role="alert" data-testid="wan-target-refusal" className="mt-2 text-xs text-red-700 dark:text-red-400">
          {refusal}
        </p>
      )}
      <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
        A target is dialed by a root-owned prober, so it must be an IP address or a DNS name — nothing option-shaped or
        shell-hostile is accepted, and a refusal names the value it rejected.
      </p>
    </div>
  );
}
