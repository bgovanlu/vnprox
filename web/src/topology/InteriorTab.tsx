// SPDX-License-Identifier: Apache-2.0

// T-1304's guest network interior inspector: an opt-in inspector tab
// showing a guest's own inside view of its network — interfaces,
// addresses, routing table, DNS config, listening sockets, default-gateway
// reachability — plus an IPAM cross-check annotation. Off by default (the
// read reaches into the guest, or an lxc container's host-side netns) —
// this component always shows the toggle and its plain-English "why it's
// off by default" copy; the interior view itself only renders once opted
// in.
import { useState } from "react";
import { Button } from "../components/Button";
import {
  useGuestInteriorQuery,
  useGuestInteriorToggleQuery,
  useSetGuestInteriorToggleMutation,
} from "./guestInteriorQueries";
import type { GuestInteriorAddress } from "../api/types";

export interface InteriorTabProps {
  /** The guest's own Ref string (guest:node:vmid). */
  entityRef: string;
}

function SectionHeading({ children }: { children: React.ReactNode }) {
  return <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">{children}</h3>;
}

function ipamBadge(addr: GuestInteriorAddress, ipamDiff: { ip: string; matches: boolean }[]) {
  const entry = ipamDiff.find((d) => d.ip === addr.ip);
  if (!entry) return null;
  return entry.matches ? (
    <span className="ml-2 rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] text-emerald-700 dark:bg-emerald-900 dark:text-emerald-300">
      IPAM match
    </span>
  ) : (
    <span
      className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-[10px] text-amber-700 dark:bg-amber-900 dark:text-amber-300"
      title="This guest claims this address, but IPAM has no matching allocation for it."
    >
      no IPAM record
    </span>
  );
}

export function InteriorTab({ entityRef }: InteriorTabProps) {
  const [pendingToggle, setPendingToggle] = useState<boolean | null>(null);
  const toggleQuery = useGuestInteriorToggleQuery(entityRef);
  const setToggle = useSetGuestInteriorToggleMutation(entityRef);
  const enabled = pendingToggle ?? toggleQuery.data?.enabled ?? false;
  const interiorQuery = useGuestInteriorQuery(entityRef, enabled);

  function handleToggle(next: boolean): void {
    setPendingToggle(next);
    setToggle.mutate(next, {
      onSettled: () => {
        setPendingToggle(null);
      },
    });
  }

  return (
    <div className="space-y-4 text-xs">
      <div className="flex items-start gap-2 rounded border border-slate-200 p-2 dark:border-slate-700">
        <input
          id={`interior-toggle-${entityRef}`}
          type="checkbox"
          className="mt-0.5"
          checked={enabled}
          disabled={toggleQuery.isLoading || setToggle.isPending}
          onChange={(e) => {
            handleToggle(e.target.checked);
          }}
        />
        <label htmlFor={`interior-toggle-${entityRef}`} className="flex-1">
          <span className="block font-medium text-slate-700 dark:text-slate-200">
            Show this guest's network interior
          </span>
          <span className="block text-slate-600 dark:text-slate-400">
            Off by default: turning this on reads inside the guest itself (via the QEMU guest agent, or — for a
            container — directly from the host side) every time this tab is open. vnprox never trusts what a guest
            reports about itself as ground truth; addresses are cross-checked against IPAM below.
          </span>
        </label>
      </div>

      {!enabled && (
        <p className="text-slate-600 dark:text-slate-400">Enable the toggle above to read this guest's interfaces, routes, DNS, and listening sockets.</p>
      )}

      {enabled && interiorQuery.isLoading && <p className="text-slate-600 dark:text-slate-400">Reading guest interior…</p>}

      {enabled && interiorQuery.isError && (
        <p className="text-amber-600 dark:text-amber-400">
          Could not read this guest's interior right now — it may be unreachable, or no live PVE session is
          available. Try again shortly.
        </p>
      )}

      {enabled &&
        interiorQuery.data &&
        (() => {
          const data = interiorQuery.data;
          return (
            <div className="space-y-4" data-testid="interior-view">
              <p className="text-slate-600 dark:text-slate-400">
                Source: <span className="font-mono">{data.source}</span> · Default gateway:{" "}
                <span className={data.defaultGatewayReachable ? "text-emerald-600 dark:text-emerald-400" : "text-amber-600 dark:text-amber-400"}>
                  {data.defaultGatewayReachable ? "reachable" : "not reachable"}
                </span>
              </p>

              <section>
                <SectionHeading>Interfaces</SectionHeading>
                <ul className="space-y-1">
                  {data.interfaces.map((iface) => (
                    <li key={iface.name} className="flex items-center justify-between rounded border border-slate-200 px-2 py-1 dark:border-slate-700">
                      <span className="font-mono text-slate-700 dark:text-slate-200">{iface.name}</span>
                      <span className="text-slate-600 dark:text-slate-400">
                        {iface.mac ?? "—"} {iface.mtu ? `mtu ${String(iface.mtu)}` : ""} {iface.up ? "up" : "down"}
                      </span>
                    </li>
                  ))}
                  {data.interfaces.length === 0 && <li className="text-slate-600 dark:text-slate-400">No interfaces reported.</li>}
                </ul>
              </section>

              <section>
                <SectionHeading>Addresses</SectionHeading>
                <ul className="space-y-1">
                  {data.addresses.map((addr) => (
                    <li key={`${addr.interface}-${addr.ip}`} className="flex items-center rounded border border-slate-200 px-2 py-1 dark:border-slate-700">
                      <span className="font-mono text-slate-700 dark:text-slate-200">
                        {addr.ip}
                        {addr.prefix ? `/${String(addr.prefix)}` : ""}
                      </span>
                      <span className="ml-2 text-slate-600 dark:text-slate-400">on {addr.interface}</span>
                      {ipamBadge(addr, data.ipamDiff)}
                    </li>
                  ))}
                  {data.addresses.length === 0 && <li className="text-slate-600 dark:text-slate-400">No addresses reported.</li>}
                </ul>
              </section>

              <section>
                <SectionHeading>Routes</SectionHeading>
                <ul className="space-y-1">
                  {data.routes.map((route, i) => (
                    <li key={`${route.destination}-${String(i)}`} className="rounded border border-slate-200 px-2 py-1 font-mono text-slate-700 dark:border-slate-700 dark:text-slate-200">
                      {route.destination} {route.gateway ? `via ${route.gateway}` : ""} {route.dev ? `dev ${route.dev}` : ""}
                    </li>
                  ))}
                  {data.routes.length === 0 && <li className="text-slate-600 dark:text-slate-400">No routes reported.</li>}
                </ul>
              </section>

              <section>
                <SectionHeading>DNS</SectionHeading>
                {data.dns.nameservers && data.dns.nameservers.length > 0 ? (
                  <p className="text-slate-700 dark:text-slate-200">
                    Nameservers: <span className="font-mono">{data.dns.nameservers.join(", ")}</span>
                    {data.dns.searchDomains && data.dns.searchDomains.length > 0 && (
                      <>
                        {" "}
                        · Search: <span className="font-mono">{data.dns.searchDomains.join(", ")}</span>
                      </>
                    )}
                  </p>
                ) : (
                  <p className="text-slate-600 dark:text-slate-400">No DNS configuration reported.</p>
                )}
              </section>

              <section>
                <SectionHeading>Listening sockets</SectionHeading>
                <ul className="space-y-1">
                  {data.listeningSockets.map((sock, i) => (
                    <li key={`${sock.proto}-${sock.localAddr}-${String(sock.localPort)}-${String(i)}`} className="rounded border border-slate-200 px-2 py-1 font-mono text-slate-700 dark:border-slate-700 dark:text-slate-200">
                      {sock.proto} {sock.localAddr}:{sock.localPort}
                    </li>
                  ))}
                  {data.listeningSockets.length === 0 && <li className="text-slate-600 dark:text-slate-400">No listening sockets reported.</li>}
                </ul>
              </section>
            </div>
          );
        })()}

      <Button
        size="sm"
        variant="ghost"
        disabled={!enabled || interiorQuery.isFetching}
        onClick={() => {
          void interiorQuery.refetch();
        }}
      >
        Refresh
      </Button>
    </div>
  );
}
