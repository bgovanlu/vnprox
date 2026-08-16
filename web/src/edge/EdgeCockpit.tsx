// T-1403's Edge & NAT cockpit: "how does traffic actually leave, and what's
// exposed inbound?" — default routes, PVE-host masquerade/port-forward
// rules (with a port-forward's target guest correlated and a powered-off
// target flagged distinctly, this card's own exit-demo scenario), and PVE
// SDN simple-zone NAT. Read-only: every edit is an ordinary nat.*/
// route.static.* changeset op staged from the regular changeset drawer —
// this page has no apply/mutate affordance of its own.
//
// T-1406 extends this same page with Ingress visibility: the configured
// reverse-proxy discovery targets, and the WAN -> port-forward -> proxy
// guest -> backend guest chain the Edge layer draws whenever a port-forward
// and an ingress target line up. Discovery itself is read-only; the only
// mutations this section makes are POST/DELETE /ingress/targets (adding/
// removing which targets to poll), never a call into the proxy itself.
//
// T-3004 adds the other end of the same question — WAN & upstream health
// (WanHealthPanel.tsx). Default routes say how traffic is *meant* to leave;
// the WAN probes say whether it actually gets anywhere once it has. Neither
// half fails over: no changeset op for WAN uplink switching exists.
import { useState } from "react";
import type { FormEvent } from "react";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import type { EdgeDefaultRoute, EdgePortForward, EdgeStaticRoute, IngressChain, IngressTarget, IngressTargetKind } from "../api/types";
import {
  useCreateIngressTargetMutation,
  useDeleteIngressTargetMutation,
  useEdgeNATQuery,
  useEdgeRoutesQuery,
  useIngressStatusQuery,
  useIngressTargetsQuery,
} from "./edgeQueries";
import { WanHealthPanel } from "./WanHealthPanel";

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
          <IngressSection />
        </>
      )}

      {/* Outside the isLoading/error guard on purpose: the WAN probes are a
       * separate read with a separate failure mode, and an unreadable
       * /edge/nat must not hide a perfectly readable WAN verdict — which is
       * exactly the read an operator wants when the cluster looks unwell. */}
      <WanHealthPanel />
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

const INGRESS_KINDS: IngressTargetKind[] = ["haproxy", "nginx", "caddy", "traefik"];

function IngressSection() {
  const targetsQuery = useIngressTargetsQuery();
  const statusQuery = useIngressStatusQuery();

  const targets = targetsQuery.data?.items ?? [];
  const chains = statusQuery.data?.chains ?? [];

  return (
    <section aria-label="Ingress visibility">
      <h3 className="mb-2 text-sm font-semibold">Ingress visibility</h3>
      <p className="mb-3 text-xs text-slate-500 dark:text-slate-400">
        Read-only reverse-proxy discovery — only for targets added below. Draws the full inbound path when a
        port-forward and a target line up: WAN → port-forward → proxy guest → backend guest.
      </p>
      <IngressChainList chains={chains} />
      <IngressTargetsPanel targets={targets} />
    </section>
  );
}

function IngressChainList({ chains }: { chains: IngressChain[] }) {
  if (chains.length === 0) {
    return (
      <EmptyState
        title="No connected ingress chains"
        description="Add a discovery target whose address matches a port-forward's internal IP to draw the full inbound path."
      />
    );
  }
  return (
    <ul className="mb-4 flex flex-col gap-2" aria-label="Ingress chains">
      {chains.map((c) => (
        <li
          key={c.portForwardId}
          role="group"
          aria-label={`Ingress chain for port forward ${c.portForwardId}`}
          className="flex flex-wrap items-center gap-2 rounded-md border border-slate-200 bg-white px-3 py-2 text-xs dark:border-slate-700 dark:bg-slate-900"
        >
          <ChainHop label="WAN" />
          <ChainArrow />
          <ChainHop label={`${c.proto}/${String(c.extPort)}`} mono />
          <ChainArrow />
          <ChainHop label={c.proxyGuestRef ?? "unresolved proxy"} mono={Boolean(c.proxyGuestRef)} />
          <ChainArrow />
          <ChainHop label={`${c.targetKind} (${c.targetId})`} />
          {c.backends.length === 0 ? (
            <>
              <ChainArrow />
              <span className="text-slate-400">no backends discovered</span>
            </>
          ) : (
            c.backends.map((b) => (
              <span key={b.address} className="flex items-center gap-2">
                <ChainArrow />
                <ChainHop label={b.guestRef ?? b.address} mono={!b.guestRef} healthy={b.healthy} />
              </span>
            ))
          )}
        </li>
      ))}
    </ul>
  );
}

function ChainArrow() {
  return (
    <span aria-hidden="true" className="text-slate-400">
      →
    </span>
  );
}

function ChainHop({ label, mono, healthy }: { label: string; mono?: boolean; healthy?: boolean }) {
  return (
    <span
      className={`rounded bg-slate-100 px-2 py-0.5 dark:bg-slate-800 ${mono ? "font-mono" : ""} ${
        healthy === false ? "text-red-700 dark:text-red-400" : ""
      }`}
    >
      {label}
    </span>
  );
}

function IngressTargetsPanel({ targets }: { targets: IngressTarget[] }) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const deleteMutation = useDeleteIngressTargetMutation();
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
        Discovery targets
      </h4>
      {targets.length === 0 ? (
        <EmptyState title="No ingress targets configured" description="Add a target below to discover it." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Kind</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Credential</TableHead>
              <TableHead>Added by</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {targets.map((t) => (
              <TableRow key={t.id}>
                <TableCell>{t.kind}</TableCell>
                <TableCell className="font-mono text-xs">{t.address}</TableCell>
                <TableCell>{t.hasCredential ? "set" : "—"}</TableCell>
                <TableCell>{t.addedBy}</TableCell>
                <TableCell>
                  <Button
                    variant="destructive"
                    size="sm"
                    disabled={!canWrite || deleteMutation.isPending}
                    title={writeDisabledReason}
                    onClick={() => {
                      deleteMutation.mutate(t.id, {
                        onError: (err) => {
                          toast({ title: "Could not remove target", description: String(err), variant: "error" });
                        },
                      });
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
      <AddIngressTargetForm canWrite={canWrite} writeDisabledReason={writeDisabledReason} />
    </div>
  );
}

function AddIngressTargetForm({ canWrite, writeDisabledReason }: { canWrite: boolean; writeDisabledReason?: string }) {
  const { toast } = useToast();
  const createMutation = useCreateIngressTargetMutation();
  const [kind, setKind] = useState<IngressTargetKind>("haproxy");
  const [address, setAddress] = useState("");
  const [credential, setCredential] = useState("");

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    createMutation.mutate(
      { kind, address, credential: credential || undefined },
      {
        onSuccess: () => {
          setAddress("");
          setCredential("");
        },
        onError: (err) => {
          toast({ title: "Could not add ingress target", description: String(err), variant: "error" });
        },
      },
    );
  }

  return (
    <form onSubmit={handleSubmit} className="mt-3 flex flex-wrap items-end gap-2" aria-label="Add ingress target">
      <label className="flex flex-col gap-1 text-xs">
        Kind
        <select
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as IngressTargetKind);
          }}
          disabled={!canWrite}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
        >
          {INGRESS_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs">
        Address
        <input
          type="text"
          value={address}
          onChange={(e) => {
            setAddress(e.target.value);
          }}
          disabled={!canWrite}
          placeholder="http://10.0.0.5:8404"
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        Credential (optional)
        <input
          type="password"
          value={credential}
          onChange={(e) => {
            setCredential(e.target.value);
          }}
          disabled={!canWrite}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
        />
      </label>
      <Button type="submit" size="sm" disabled={!canWrite || !address || createMutation.isPending} title={writeDisabledReason}>
        Add target
      </Button>
    </form>
  );
}
