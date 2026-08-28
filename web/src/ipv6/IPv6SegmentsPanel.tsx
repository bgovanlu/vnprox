// SPDX-License-Identifier: Apache-2.0

// IPv6 segment visibility (GET /ipv6/segments, T-1404): which segments are
// actually advertising IPv6, and what they are advertising.
//
// The route is an observation, not a configuration read — one `rdisc6`
// solicit per bridge/VLAN interface on every cluster node. Two consequences
// shape this panel:
//
//   - An interface only appears when an RA was **actually observed**. So an
//     empty table means "no IPv6 seen anywhere", never "IPv6 is off" and
//     never "nothing is configured". The empty state says exactly that.
//   - The fan-out tolerates individual node failures (`partial`/
//     `failedNodes`). A partial read renders the gap alongside the rows, so
//     a node that did not answer is never silently absent.
//
// The dual-stack wizard is mounted here, with its VNet list taken from
// GET /sdn rather than from the segment list — see DualStackWizard.tsx's own
// doc comment for why that inversion matters.
import { useMemo } from "react";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import { useChangesetDrawerStore } from "../changesets/store";
import { useSdnQuery } from "../sdn/queries";
import type { IPv6Segment } from "../api/types";
import { DualStackWizard, type DualStackVnetOption } from "./DualStackWizard";
import { useIPv6SegmentsQuery } from "./ipv6Queries";

export function IPv6SegmentsPanel() {
  const { data, isLoading, error } = useIPv6SegmentsQuery();
  const sdnQuery = useSdnQuery();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);

  const vnets: DualStackVnetOption[] = useMemo(() => {
    const out: DualStackVnetOption[] = [];
    for (const zone of sdnQuery.data?.zones ?? []) {
      for (const vnet of zone.vnets) {
        out.push({ id: vnet.id, alias: vnet.alias, zone: zone.id });
      }
    }
    return out.sort((a, b) => a.id.localeCompare(b.id));
  }, [sdnQuery.data]);

  return (
    <section aria-labelledby="ipv6-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="ipv6-heading" className="flex items-center gap-2 text-lg font-semibold">
          IPv6 segments
          <HelpAnchor topic="ipv6-segments" />
        </h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          What each segment is actually advertising: router advertisements, the prefixes they carry, and whether a
          DHCPv6 server answered. Observed live per node, not read from configuration.
        </p>
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Soliciting router advertisements…</p>}
      {error && (
        <EmptyState
          title="Could not read IPv6 segments"
          description="The daemon could not complete the cluster-wide RA read. This is a failed read, not evidence that no segment advertises IPv6."
          density="compact"
        />
      )}

      {!isLoading && !error && data && (
        <>
          {data.partial && (
            <p role="status" className="text-xs text-amber-700 dark:text-amber-400">
              Partial read: {data.failedNodes && data.failedNodes.length > 0 ? data.failedNodes.join(", ") : "one or more nodes"}{" "}
              did not answer. Segments on those nodes are missing from this table, not absent from the cluster.
            </p>
          )}
          {data.items.length === 0 ? (
            <EmptyState
              title="No router advertisements observed"
              description="No segment on any node that answered is advertising IPv6. An interface appears here only when an RA was actually seen, so this is 'none observed' — it does not mean IPv6 is disabled, and it does not mean nothing is configured."
              density="compact"
            />
          ) : (
            <SegmentsTable items={data.items} />
          )}
        </>
      )}

      <div>
        <h3 className="mb-2 text-sm font-semibold">Dual-stack rollout</h3>
        <p className="mb-3 text-xs text-slate-600 dark:text-slate-400">
          Adds an IPv6 subnet to an existing VLAN/VNet as one reviewable changeset. Idempotent: re-running it against a
          VNet that already has the requested subnet produces a zero-operation changeset, not a duplicate.
        </p>
        {vnets.length === 0 ? (
          <EmptyState
            title="No VNets to roll out to"
            description="The dual-stack wizard adds a subnet to an existing SDN VNet, and vnprox has not discovered one on this cluster yet."
            density="compact"
          />
        ) : (
          <DualStackWizard
            vnets={vnets}
            onInstantiated={(changeset) => {
              // Open the resulting draft in the change drawer — the wizard
              // itself deliberately has no opinion about what "open" means.
              setActiveId(changeset.id);
            }}
          />
        )}
      </div>
    </section>
  );
}

function SegmentsTable({ items }: { items: IPv6Segment[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Node</TableHead>
          <TableHead>Interface</TableHead>
          <TableHead>Segment</TableHead>
          <TableHead>RA</TableHead>
          <TableHead>Prefixes</TableHead>
          <TableHead>Addressing</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((s) => (
          <TableRow key={`${s.node}-${s.iface}`}>
            <TableCell>{s.node}</TableCell>
            <TableCell className="font-mono text-xs">{s.iface}</TableCell>
            <TableCell>{segmentLabel(s)}</TableCell>
            <TableCell>
              {s.raPresent ? (
                <span className="text-emerald-700 dark:text-emerald-400">
                  present
                  {s.routerLifetimeSec !== undefined && s.routerLifetimeSec > 0
                    ? ` (${String(s.routerLifetimeSec)}s lifetime)`
                    : ""}
                </span>
              ) : (
                <span className="text-slate-600 dark:text-slate-400">not present</span>
              )}
            </TableCell>
            <TableCell className="font-mono text-xs">
              {s.prefixes && s.prefixes.length > 0 ? s.prefixes.join(", ") : "—"}
            </TableCell>
            <TableCell className="text-xs">{addressingLabel(s)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** "zone / vnet" where the observation could be correlated to an SDN VNet,
 * the bridge name where it is a plain bridge, and the raw interface name
 * where vnprox has no inventory entity for it at all — the last case is
 * real (an interface polled before its entity exists) and renders as
 * uncorrelated rather than being dropped. */
function segmentLabel(s: IPv6Segment): string {
  if (s.vnet) {
    return s.zone ? `${s.zone} / ${s.vnet}` : s.vnet;
  }
  if (s.kind === "bridge") {
    return s.vid !== undefined && s.vid > 0 ? `bridge (VLAN ${String(s.vid)})` : "bridge";
  }
  return "uncorrelated";
}

/** SLAAC vs DHCPv6, from the RA's own M/O flags. `dhcpv6InferredFromRA`
 * matters: it is the difference between "a DHCPv6 server answered" and "the
 * RA said there ought to be one", and the two must not read alike. */
function addressingLabel(s: IPv6Segment): string {
  if (!s.raPresent) {
    return "—";
  }
  const parts: string[] = [s.managedFlag ? "DHCPv6 (managed)" : "SLAAC"];
  if (s.otherFlag) parts.push("other config via DHCPv6");
  if (s.dhcpv6ServerPresent) {
    parts.push(s.dhcpv6InferredFromRA ? "DHCPv6 server implied by the RA, not observed" : "DHCPv6 server observed");
  }
  return parts.join(" · ");
}
