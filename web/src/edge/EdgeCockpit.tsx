// T-1403's Edge & NAT cockpit: "how does traffic actually leave, and what's
// exposed inbound?" — default routes, PVE-host masquerade/port-forward
// rules (with a port-forward's target guest correlated and a powered-off
// target flagged distinctly, this card's own exit-demo scenario), and PVE
// SDN simple-zone NAT. Read-only: every edit is an ordinary nat.*/
// route.static.* changeset op staged from the regular changeset drawer —
// this page has no apply/mutate affordance of its own.
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { EdgeDefaultRoute, EdgePortForward, EdgeStaticRoute } from "../api/types";
import { useEdgeNATQuery, useEdgeRoutesQuery } from "./edgeQueries";

function nodeSummary(defaultRoutes: EdgeDefaultRoute[], portForwards: EdgePortForward[]): Map<string, number> {
  const nodes = new Map<string, number>();
  for (const r of defaultRoutes) nodes.set(r.node, nodes.get(r.node) ?? 0);
  for (const pf of portForwards) nodes.set(pf.node, (nodes.get(pf.node) ?? 0) + 1);
  return nodes;
}

export function EdgeCockpit() {
  const routesQuery = useEdgeRoutesQuery();
  const natQuery = useEdgeNATQuery();

  const isLoading = routesQuery.isLoading || natQuery.isLoading;
  const error = routesQuery.error ?? natQuery.error;

  const defaultRoutes = routesQuery.data?.defaultRoutes ?? [];
  const staticRoutes = routesQuery.data?.staticRoutes ?? [];
  const masquerade = natQuery.data?.masquerade ?? [];
  const portForwards = natQuery.data?.portForwards ?? [];
  const sdnSimpleZoneNat = natQuery.data?.sdnSimpleZoneNat ?? [];

  const summary = nodeSummary(defaultRoutes, portForwards);
  const exposedCount = portForwards.length;
  const poweredOffCount = portForwards.filter((pf) => pf.targetGuestPoweredOff).length;

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-base font-semibold">Edge & NAT cockpit</h2>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          How traffic actually leaves the cluster, and what&apos;s exposed inbound. Read-only — every rule here is
          edited via an ordinary staged changeset, never from this page directly.
        </p>
      </div>

      {isLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {error && <EmptyState title="Could not load edge data" description="Try again in a moment." />}

      {!isLoading && !error && (
        <>
          <section aria-label="Inbound exposure summary" className="flex flex-wrap gap-3">
            {Array.from(summary.entries()).map(([node, count]) => (
              <div
                key={node}
                className="rounded-md border border-slate-200 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
              >
                <span className="font-medium">{node}</span>: {count} port{count === 1 ? "" : "s"} exposed
              </div>
            ))}
            {exposedCount > 0 && (
              <div className="rounded-md border border-slate-200 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900">
                {exposedCount} port-forward{exposedCount === 1 ? "" : "s"} total
                {poweredOffCount > 0 && (
                  <span className="ml-1 text-amber-700 dark:text-amber-400">
                    ({poweredOffCount} to a powered-off guest)
                  </span>
                )}
              </div>
            )}
          </section>

          <DefaultRoutesTable routes={defaultRoutes} />
          <StaticRoutesTable routes={staticRoutes} />
          <MasqueradeTable rules={masquerade} />
          <PortForwardsTable rules={portForwards} />
          <SDNSimpleZoneNATTable rows={sdnSimpleZoneNat} />
        </>
      )}
    </div>
  );
}

function DefaultRoutesTable({ routes }: { routes: EdgeDefaultRoute[] }) {
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">Default routes</h3>
      {routes.length === 0 ? (
        <EmptyState title="No default routes configured" description="Set a gateway on a node's uplink interface." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Interface</TableHead>
              <TableHead>Gateway</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {routes.map((r) => (
              <TableRow key={`${r.node}-${r.iface}`}>
                <TableCell>{r.node}</TableCell>
                <TableCell className="font-mono text-xs">{r.iface}</TableCell>
                <TableCell className="font-mono text-xs">{r.gateway}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function StaticRoutesTable({ routes }: { routes: EdgeStaticRoute[] }) {
  if (routes.length === 0) return null;
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">Static routes</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Node</TableHead>
            <TableHead>Interface</TableHead>
            <TableHead>Destination</TableHead>
            <TableHead>Gateway</TableHead>
            <TableHead>Metric</TableHead>
            <TableHead>Comment</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {routes.map((r) => (
            <TableRow key={r.id}>
              <TableCell>{r.node}</TableCell>
              <TableCell className="font-mono text-xs">{r.iface}</TableCell>
              <TableCell className="font-mono text-xs">{r.destCidr}</TableCell>
              <TableCell className="font-mono text-xs">{r.gateway}</TableCell>
              <TableCell>{r.metric ?? "—"}</TableCell>
              <TableCell>{r.comment ?? "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  );
}

function MasqueradeTable({ rules }: { rules: { id: string; node: string; iface: string; sourceCidr: string; comment?: string }[] }) {
  if (rules.length === 0) return null;
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">Masquerade (SNAT)</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Node</TableHead>
            <TableHead>Outbound interface</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Comment</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((r) => (
            <TableRow key={r.id}>
              <TableCell>{r.node}</TableCell>
              <TableCell className="font-mono text-xs">{r.iface}</TableCell>
              <TableCell className="font-mono text-xs">{r.sourceCidr}</TableCell>
              <TableCell>{r.comment ?? "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  );
}

function PortForwardsTable({ rules }: { rules: EdgePortForward[] }) {
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">Port forwards (inbound exposure)</h3>
      {rules.length === 0 ? (
        <EmptyState title="No port forwards configured" description="Nothing on this cluster is exposed inbound." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Interface</TableHead>
              <TableHead>External</TableHead>
              <TableHead>Target</TableHead>
              <TableHead>Guest</TableHead>
              <TableHead>Comment</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((r) => (
              <TableRow key={r.id}>
                <TableCell>{r.node}</TableCell>
                <TableCell className="font-mono text-xs">{r.iface}</TableCell>
                <TableCell className="font-mono text-xs">
                  {r.proto}/{r.extPort}
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {r.intIp}:{r.intPort}
                </TableCell>
                <TableCell>
                  {r.targetGuestRef ? (
                    r.targetGuestPoweredOff ? (
                      <span
                        role="status"
                        className="rounded-md bg-red-50 px-2 py-0.5 text-xs font-medium text-red-800 dark:bg-red-950/40 dark:text-red-300"
                      >
                        {r.targetGuestRef} — powered off
                      </span>
                    ) : (
                      <span className="text-slate-600 dark:text-slate-300">{r.targetGuestRef}</span>
                    )
                  ) : (
                    <span className="text-slate-400">unresolved</span>
                  )}
                </TableCell>
                <TableCell>{r.comment ?? "—"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function SDNSimpleZoneNATTable({ rows }: { rows: { zone: string; vnet: string; subnet: string; gateway?: string }[] }) {
  if (rows.length === 0) return null;
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">SDN simple-zone NAT</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Zone</TableHead>
            <TableHead>VNet</TableHead>
            <TableHead>Subnet</TableHead>
            <TableHead>Gateway</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={`${r.zone}-${r.subnet}`}>
              <TableCell>{r.zone}</TableCell>
              <TableCell>{r.vnet}</TableCell>
              <TableCell className="font-mono text-xs">{r.subnet}</TableCell>
              <TableCell className="font-mono text-xs">{r.gateway ?? "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  );
}
