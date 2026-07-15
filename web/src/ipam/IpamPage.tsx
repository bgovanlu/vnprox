// IPAM cockpit (docs/features/ipam.md §2): subnet list with utilization,
// the address list for whichever subnet is selected, and CSV export.
import { useEffect, useState } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import type { IpamSubnet } from "../api/types";
import { ipamAllocationsCsvUrl } from "../api/ipam";
import { AddressList } from "./AddressList";
import { useIpamSubnetsQuery } from "./queries";

function UtilizationBar({ utilization, conflicts }: { utilization: number; conflicts: number }) {
  const pct = Math.min(100, Math.round(utilization * 100));
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">
        <div
          className={clsx("h-full rounded-full", conflicts > 0 ? "bg-red-500" : "bg-accent-500")}
          style={{ width: `${String(pct)}%` }}
        />
      </div>
      <span className="text-xs text-slate-500 dark:text-slate-400">{pct}%</span>
    </div>
  );
}

function SubnetRow({ subnet, selected, onSelect }: { subnet: IpamSubnet; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={clsx(
        "flex w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors",
        selected
          ? "border-accent-500 bg-accent-50 dark:bg-accent-950/40"
          : "border-slate-200 bg-white hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:hover:bg-slate-800/60",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-sm font-medium">{subnet.cidr}</span>
        {subnet.readOnly && (
          <span className="rounded bg-slate-200 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:bg-slate-700 dark:text-slate-300">
            read-only
          </span>
        )}
        {subnet.conflicts > 0 && (
          <span className="rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
            {subnet.conflicts} conflict{subnet.conflicts === 1 ? "" : "s"}
          </span>
        )}
      </div>
      <div className="text-xs text-slate-500 dark:text-slate-400">
        {subnet.source === "sdn" ? `${subnet.zone ?? "?"} / ${subnet.vnet ?? "?"}` : subnet.node ? `bridge on ${subnet.node}` : "detected"}
        {subnet.gateway ? ` · gw ${subnet.gateway}` : ""}
        {subnet.dhcpEnabled ? " · DHCP" : ""}
      </div>
      <UtilizationBar utilization={subnet.utilization} conflicts={subnet.conflicts} />
    </button>
  );
}

/** The selected subnet's context row under its CIDR heading: zone/VNet (SDN)
 * or owning node (bridge), gateway, and DHCP status. The live per-state
 * counts live in AddressList's summary strip, not here. */
function SubnetFacts({ subnet }: { subnet: IpamSubnet }) {
  const facts: { label: string; value: string }[] = [];
  if (subnet.source === "sdn") {
    if (subnet.zone) facts.push({ label: "Zone", value: subnet.zone });
    if (subnet.vnet) facts.push({ label: "VNet", value: subnet.vnet });
  } else if (subnet.node) {
    facts.push({ label: "Bridge on", value: subnet.node });
  }
  if (subnet.gateway) facts.push({ label: "Gateway", value: subnet.gateway });
  facts.push({ label: "DHCP", value: subnet.dhcpEnabled ? "on" : "off" });

  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
      {facts.map((f) => (
        <span key={f.label}>
          {f.label} <span className="font-medium text-slate-700 dark:text-slate-200">{f.value}</span>
        </span>
      ))}
      {subnet.readOnly && (
        <span className="rounded bg-slate-200 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:bg-slate-700 dark:text-slate-300">
          read-only
        </span>
      )}
    </div>
  );
}

export function IpamPage() {
  const { data, isLoading, isError } = useIpamSubnetsQuery();
  const [selected, setSelected] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (data && !selected && data.items.length > 0) {
      const first = data.items[0];
      if (first) setSelected(first.cidr);
    }
    // Only re-run when the subnet list reference changes — a manual
    // selection is never overridden by a background refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const selectedSubnet = data?.items.find((s) => s.cidr === selected);

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">IPAM</h1>
      </div>

      {isLoading && <p className="text-sm text-slate-400">Loading IPAM subnets…</p>}
      {isError && (
        <EmptyState title="Could not load IPAM subnets" description="Check that vnproxd can reach the local PVE API, then reload." />
      )}
      {!isLoading && !isError && data && data.items.length === 0 && (
        <EmptyState title="No subnets found" description="Configure an SDN subnet in Proxmox, or a bridge address, to see it here." />
      )}

      {!isLoading && !isError && data && data.items.length > 0 && (
        <div className="grid min-h-0 flex-1 grid-cols-[minmax(240px,340px)_1fr] gap-3">
          <div className="flex min-h-0 flex-col gap-2 overflow-y-auto rounded-lg border border-slate-200 p-2 dark:border-slate-800">
            {data.items.map((s) => (
              <SubnetRow key={s.cidr} subnet={s} selected={s.cidr === selected} onSelect={() => { setSelected(s.cidr); }} />
            ))}
          </div>
          <div className="min-h-0 overflow-y-auto rounded-lg border border-slate-200 p-4 dark:border-slate-800">
            {!selectedSubnet && <EmptyState title="Nothing selected" description="Pick a subnet from the list." />}
            {selectedSubnet && (
              <div className="flex flex-col gap-4">
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <h2 className="font-mono text-lg font-semibold">{selectedSubnet.cidr}</h2>
                    <SubnetFacts subnet={selectedSubnet} />
                  </div>
                  <a href={ipamAllocationsCsvUrl(selectedSubnet.cidr)} download>
                    <Button variant="secondary" size="sm">
                      Export CSV
                    </Button>
                  </a>
                </div>
                <AddressList subnetCidr={selectedSubnet.cidr} readOnly={selectedSubnet.readOnly} />
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
