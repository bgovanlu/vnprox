// SPDX-License-Identifier: Apache-2.0

// PBS backup-path awareness (GET /pbs, T-1206): which Proxmox Backup Server
// hosts this cluster backs up to, which link the backup traffic actually
// rides, and whether that link is big enough for the schedule.
//
// Read-only, and read-only in a specific sense: vnprox stores no PBS
// credentials and never talks to a PBS host. Everything here is PVE's own
// knowledge of itself. There is no write route and no `pbs.*` changeset op.
//
// The one honesty rule this panel carries: an unresolved carrier and an
// unknown link speed each render as unknown. `linkSpeedKnown` is a separate
// field from `linkMbps` precisely so a missing speed cannot be shown as 0.
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import type { PbsHost, PbsPath } from "../api/types";
import { MapLink } from "./MapLink";
import { usePbsOverlayQuery } from "./analysisQueries";

export function PbsPanel() {
  const { data, isLoading, error, refetch } = usePbsOverlayQuery();

  return (
    <section aria-labelledby="pbs-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="pbs-heading" className="flex items-center gap-2 text-lg font-semibold">
          Backup paths (PBS)
          <HelpAnchor topic="pbs-awareness" />
        </h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Which link your backup traffic actually crosses, and whether it is sized for the schedule. Discovered from
          PVE&apos;s own storage configuration — vnprox stores no PBS credentials and changes nothing here.
        </p>
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {error && (
        <EmptyState
          icon="static-route"
          variant="failed"
          title="Could not read PBS status"
          description="The daemon could not resolve the PBS overlay from PVE's storage configuration. Try again in a moment."
          density="compact"
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !error && data && (
        <>
          <PbsHostsTable hosts={data.hosts} />
          <PbsPathsTable paths={data.paths} hostCount={data.hosts.length} />
        </>
      )}
    </section>
  );
}

function PbsHostsTable({ hosts }: { hosts: PbsHost[] }) {
  if (hosts.length === 0) {
    return (
      <EmptyState
        icon="static-route"
        variant="unconfigured"
        title="No PBS hosts configured"
        description="No Proxmox Backup Server storage is defined on this cluster, so there is no backup path to draw."
        density="compact"
      />
    );
  }
  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold">Hosts</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Host</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Datastores</TableHead>
            <TableHead>PVE storage</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hosts.map((h) => (
            <TableRow key={h.ref}>
              <TableCell>
                <MapLink entityRef={h.ref} label={h.address} />
              </TableCell>
              <TableCell className="font-mono text-xs">
                {h.address}
                {h.port === undefined ? "" : `:${String(h.port)}`}
              </TableCell>
              <TableCell>{joinOrDash(h.datastores)}</TableCell>
              <TableCell>{joinOrDash(h.storageIds)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function PbsPathsTable({ paths, hostCount }: { paths: PbsPath[]; hostCount: number }) {
  if (paths.length === 0) {
    if (hostCount === 0) return null;
    return (
      <EmptyState
        icon="static-route"
        variant="empty"
        title="No backup path resolved"
        description="PBS hosts are configured, but vnprox could not resolve which interface a node's backup traffic leaves by. The hosts above are still real; the path is what is unknown."
        density="compact"
      />
    );
  }
  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold">Paths</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Node</TableHead>
            <TableHead>Carrier</TableHead>
            <TableHead>Link speed</TableHead>
            <TableHead>Jobs</TableHead>
            <TableHead>Sizing hint</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {paths.map((p) => (
            <TableRow key={`${p.node}-${p.host}`}>
              <TableCell>{p.node}</TableCell>
              <TableCell>
                {p.carrier ? (
                  <MapLink entityRef={p.carrier} />
                ) : (
                  <span className="text-slate-600 dark:text-slate-400">unresolved</span>
                )}
              </TableCell>
              <TableCell className="tabular-nums">
                {p.linkSpeedKnown && p.linkMbps !== undefined ? (
                  `${String(p.linkMbps)} Mbit/s`
                ) : (
                  <span className="text-slate-600 dark:text-slate-400">unknown</span>
                )}
              </TableCell>
              <TableCell>{p.jobs === undefined || p.jobs.length === 0 ? "—" : describeJobs(p)}</TableCell>
              <TableCell className="text-sm">{p.sizingHint}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function describeJobs(p: PbsPath): string {
  return (p.jobs ?? [])
    .map((j) => `${j.storage}${j.schedule ? ` @ ${j.schedule}` : ""} (${j.all ? "all guests" : `${String(j.guests)} guests`})`)
    .join("; ");
}

function joinOrDash(values: string[] | undefined): string {
  return values === undefined || values.length === 0 ? "—" : values.join(", ");
}
