// DHCP reservations + live leases view (docs/features/sdn.md §5): "static
// reservations bound to guest MACs ... and a live leases view (parsed
// per-node via peer API)". Lives alongside T-401's tree/detail panels and
// T-404's EvpnView in the same SDN cockpit page (SdnPage.tsx's tab
// switcher). Reservations are rendered directly from GET /sdn/dhcp's
// `reservations` array — the exact same PVE-IPAM records the IPAM grid
// renders as allocated cells (internal/ipam/dhcp.go's doc comment: "one
// dataset"), not a separate reservation store.
import { useMemo, useState } from "react";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import { inputClass } from "../changesets/editors/EditorDialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { DhcpLease, DhcpReservation } from "../api/types";
import { zoneOptions } from "./dhcpView";
import { useDhcpViewQuery, useSdnQuery } from "./queries";

function GuestCell({ guestRef }: { guestRef?: string }) {
  if (!guestRef) {
    return <span className="text-slate-500 dark:text-slate-400">unmatched</span>;
  }
  return <span className="text-emerald-600 dark:text-emerald-400">{guestRef}</span>;
}

function ReservationsTable({ reservations }: { reservations: DhcpReservation[] }) {
  if (reservations.length === 0) {
    return (
      <EmptyState
        title="No reservations"
        description="No MAC-bound IPAM allocation exists on a DHCP-enabled subnet — reserve one from the IPAM grid's cell detail."
      />
    );
  }
  return (
    <Table aria-label="DHCP reservations">
      <TableHeader>
        <TableRow>
          <TableHead>IP</TableHead>
          <TableHead>MAC</TableHead>
          <TableHead>Hostname</TableHead>
          <TableHead>Zone / VNet</TableHead>
          <TableHead>Guest</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {reservations.map((r) => (
          <TableRow key={`${r.cidr}:${r.ip}`}>
            <TableCell className="font-mono">{r.ip}</TableCell>
            <TableCell className="font-mono">{r.mac}</TableCell>
            <TableCell>{r.hostname ?? "—"}</TableCell>
            <TableCell>
              {r.zone}/{r.vnet}
            </TableCell>
            <TableCell>
              <GuestCell guestRef={r.guestRef} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function LeasesTable({ leases }: { leases: DhcpLease[] }) {
  if (leases.length === 0) {
    return (
      <EmptyState
        title="No active leases"
        description="No dnsmasq lease was observed on any cluster node for a DHCP-enabled subnet."
      />
    );
  }
  return (
    <Table aria-label="DHCP leases">
      <TableHeader>
        <TableRow>
          <TableHead>IP</TableHead>
          <TableHead>MAC</TableHead>
          <TableHead>Hostname</TableHead>
          <TableHead>Zone / VNet</TableHead>
          <TableHead>Guest</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {leases.map((l) => (
          <TableRow key={`${l.cidr}:${l.ip}:${l.mac}`}>
            <TableCell className="font-mono">{l.ip}</TableCell>
            <TableCell className="font-mono">{l.mac}</TableCell>
            <TableCell>{l.hostname ?? "—"}</TableCell>
            <TableCell>
              {l.zone}/{l.vnet}
            </TableCell>
            <TableCell>
              <GuestCell guestRef={l.guestRef} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function DhcpView() {
  const { data: tree } = useSdnQuery();
  const [zone, setZone] = useState("");
  const { data, isLoading, isError } = useDhcpViewQuery(zone || undefined);
  const zones = useMemo(() => zoneOptions(tree), [tree]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-lg font-semibold">
          DHCP
          <HelpAnchor topic="sdn-dhcp" />
        </h2>
        <select
          aria-label="Filter by zone"
          className={inputClass + " w-auto"}
          value={zone}
          onChange={(e) => {
            setZone(e.target.value);
          }}
        >
          <option value="">All zones</option>
          {zones.map((z) => (
            <option key={z} value={z}>
              {z}
            </option>
          ))}
        </select>
      </div>

      {isLoading && <p className="text-sm text-slate-400">Loading DHCP data…</p>}
      {isError && (
        <EmptyState
          title="Could not load DHCP data"
          description="Check that vnproxd can reach the local PVE API, then reload."
        />
      )}

      {data && (
        <>
          <section>
            <h3 className="mb-2 text-sm font-medium text-slate-600 dark:text-slate-300">Static reservations</h3>
            <ReservationsTable reservations={data.reservations} />
          </section>

          <section>
            <h3 className="mb-2 text-sm font-medium text-slate-600 dark:text-slate-300">Live leases</h3>
            <LeasesTable leases={data.leases} />
          </section>
        </>
      )}
    </div>
  );
}
