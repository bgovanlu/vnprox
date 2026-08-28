// SPDX-License-Identifier: Apache-2.0

// Guest NIC list + bulk reattach (docs/features/change-management.md §6;
// docs/user-guide.md's common-tasks table: "Move 12 VMs to another bridge |
// Guests view -> filter by bridge -> select all -> Reattach"). Every action
// here drafts guest.nic.update ops into the changeset drawer — nothing
// applies directly (docs/api.md: "Ops use the user's ticket via PVE API...",
// but always behind the changeset apply/confirm flow, never a direct call).
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { Tooltip } from "../components/Tooltip";
import { useToast } from "../components/Toast";
import { capsForNode } from "../changesets/capabilities";
import { buildBulkGuestNicOps, buildGuestNicUpdateOp } from "../changesets/opBuilders";
import { refId } from "../changesets/opSummary";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { HelpAnchor } from "../help/HelpAnchor";
import { useTopologyQuery } from "../topology/queries";
import { guestRefFromNicRef } from "../guest/guestEgo";
import { filterGuestNicRows, targetLabel, type GuestNicFilter } from "./guestNics";
import { useAllGuestNicsQuery } from "./queries";

export function GuestsPage() {
  const { rows, isLoading } = useAllGuestNicsQuery();
  const { data: topology } = useTopologyQuery();
  const { data: session } = useSession();
  const { addOps } = useDrawerActions();
  const { toast } = useToast();

  const [filter, setFilter] = useState<GuestNicFilter>({});
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkTarget, setBulkTarget] = useState("");

  const nodes = useMemo(() => [...new Set(rows.map((r) => r.node))].sort(), [rows]);
  // The filter dropdown offers only the bridges/VNets guests are currently
  // on (so filtering never yields an empty set).
  const filterTargets = useMemo(
    () => [...new Set(rows.map((r) => r.bridgeOrVnet).filter((x): x is string => x !== undefined))].sort(),
    [rows],
  );
  // The bulk-*reattach* dropdown offers every bridge/VNet in the cluster —
  // reattaching to a bridge no selected guest is currently on is the whole
  // point ("Move 12 VMs to another bridge", docs/user-guide.md). Derived
  // from the topology (bridges are per-node; VNets are cluster-scoped and
  // shown once). The value must be the *bare* name a guest net config
  // references: a bridge's own name (refId, e.g. "vmbr0"), or a VNet's own
  // id — the sdn-vnet Ref id is zone-qualified ("vlanz/vnet100"), but PVE
  // guest configs reference the bare vnet id ("vnet100"), so take the last
  // path segment (guest.nic.update's BridgeOrVnet is a name, not a Ref).
  const reattachTargets = useMemo(() => {
    if (!topology) return [];
    const bareName = (id: string): string => {
      const rid = refId(id);
      const slash = rid.lastIndexOf("/");
      return slash === -1 ? rid : rid.slice(slash + 1);
    };
    return [
      ...new Set(
        topology.nodes
          .filter((n) => ["bridge", "ovs-bridge", "sdn-vnet"].includes(n.kind))
          .map((n) => bareName(n.id)),
      ),
    ].sort();
  }, [topology]);
  const filtered = useMemo(() => filterGuestNicRows(rows, filter), [rows, filter]);
  const hasActiveFilter = filter.node !== undefined || filter.bridgeOrVnet !== undefined;

  const allSelected = filtered.length > 0 && filtered.every((r) => selected.has(r.ref));

  // T-605 read-only sweep finding: the bulk reattach button below was
  // gated only on `!bulkTarget` (a target must be picked) with no
  // capability check at all — a read-only session could select rows and
  // draft a bulk guest.nic.update op regardless of guestNet/netWrite.
  // Selected guests can span multiple nodes, so the bulk action is only
  // enabled when *every* currently selected row's node grants write
  // (the same per-row check the individual Disconnect/Connect button
  // below already uses) — the strictest reading, matching this
  // component's existing per-node capsForNode convention rather than a
  // new cluster-wide hasAnyCap shortcut.
  const selectedRows = filtered.filter((r) => selected.has(r.ref));
  const canBulkReattach =
    selectedRows.length > 0 && selectedRows.every((r) => capsForNode(session, r.node).guestNet || capsForNode(session, r.node).netWrite);
  const bulkReattachDisabledReason = canBulkReattach
    ? undefined
    : "You don't have guest-network write on every selected guest's node.";

  function toggleAll(): void {
    setSelected(allSelected ? new Set() : new Set(filtered.map((r) => r.ref)));
  }

  function toggleOne(ref: string): void {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(ref)) next.delete(ref);
      else next.add(ref);
      return next;
    });
  }

  async function handleBulkReattach(): Promise<void> {
    if (!bulkTarget || selected.size === 0 || !canBulkReattach) return;
    const refs = [...selected];
    const ops = buildBulkGuestNicOps(refs, { bridgeOrVnet: bulkTarget });
    try {
      await addOps(ops, `Bulk reattach ${String(refs.length)} guest(s) to ${bulkTarget}`);
      toast({ title: `Drafted reattach for ${String(refs.length)} guest NIC(s)`, description: "Per-guest hotplug is attempted at apply; results are reported per guest." });
      setSelected(new Set());
    } catch {
      toast({ title: "Could not draft bulk reattach", variant: "error" });
    }
  }

  async function handleToggleLink(ref: string, currentlyDown: boolean): Promise<void> {
    const op = buildGuestNicUpdateOp(ref, { linkDown: !currentlyDown });
    try {
      await addOps([op], currentlyDown ? "Reconnect guest NIC" : "Disconnect guest NIC");
      toast({ title: "Added to changeset" });
    } catch {
      toast({ title: "Could not add to changeset", variant: "error" });
    }
  }

  return (
    <div className="flex h-full flex-col gap-3">
      <PageHeader
        title="Guests"
        actions={
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <select
              aria-label="Filter by node"
              className="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={filter.node ?? ""}
              onChange={(e) => { setFilter((f) => ({ ...f, node: e.target.value || undefined })); }}
            >
              <option value="">All nodes</option>
              {nodes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
            <select
              aria-label="Filter by bridge or VNet"
              className="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={filter.bridgeOrVnet ?? ""}
              onChange={(e) => { setFilter((f) => ({ ...f, bridgeOrVnet: e.target.value || undefined })); }}
            >
              <option value="">All bridges/VNets</option>
              {filterTargets.map((t) => (
                <option key={t} value={t}>
                  {targetLabel(t)}
                </option>
              ))}
            </select>
          </div>
        }
      />

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading guest NICs…</p>}
      {!isLoading && filtered.length === 0 && hasActiveFilter && (
        <EmptyState
          icon="guest-nic"
          variant="filtered"
          title="No guest NICs match"
          description="Try clearing a filter, or check that guests exist on this cluster."
          action={
            <Button variant="secondary" size="sm" onClick={() => { setFilter({}); }}>
              Clear filters
            </Button>
          }
        />
      )}
      {!isLoading && filtered.length === 0 && !hasActiveFilter && (
        <EmptyState
          icon="guest-nic"
          variant="empty"
          title="No guest NICs match"
          description="Try clearing a filter, or check that guests exist on this cluster."
        />
      )}

      {!isLoading && filtered.length > 0 && (
        <>
          {selected.size > 0 && (
            <div className="flex flex-wrap items-center gap-2 rounded-md border border-accent-300 bg-accent-50 p-2 text-sm dark:border-accent-700 dark:bg-accent-950">
              <span className="flex items-center gap-1.5">
                {selected.size} selected
                <HelpAnchor topic="guest-bulk-reattach" />
              </span>
              <select
                aria-label="Reattach selected guests to"
                className="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
                value={bulkTarget}
                onChange={(e) => { setBulkTarget(e.target.value); }}
              >
                <option value="" disabled>
                  Reattach to…
                </option>
                {reattachTargets.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
              <Tooltip content={bulkReattachDisabledReason}>
                <span>
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={!bulkTarget || !canBulkReattach}
                    onClick={() => void handleBulkReattach()}
                  >
                    Reattach {selected.size} guest(s)
                  </Button>
                </span>
              </Tooltip>
            </div>
          )}

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <input type="checkbox" checked={allSelected} onChange={toggleAll} aria-label="Select all" />
                </TableHead>
                <TableHead>Guest NIC</TableHead>
                <TableHead>Node</TableHead>
                <TableHead>Bridge/VNet</TableHead>
                <TableHead>VLAN</TableHead>
                <TableHead>Link</TableHead>
                <TableHead></TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((row) => {
                const canWrite = capsForNode(session, row.node).guestNet || capsForNode(session, row.node).netWrite;
                return (
                  <TableRow key={row.ref}>
                    <TableCell>
                      <input
                        type="checkbox"
                        checked={selected.has(row.ref)}
                        onChange={() => { toggleOne(row.ref); }}
                        aria-label={`Select ${row.label}`}
                      />
                    </TableCell>
                    <TableCell>{row.label}</TableCell>
                    <TableCell>{row.node}</TableCell>
                    <TableCell>{targetLabel(row.bridgeOrVnet)}</TableCell>
                    <TableCell>{row.vid ?? "—"}</TableCell>
                    <TableCell>{row.linkDown ? "down" : "up"}</TableCell>
                    <TableCell>
                      <Tooltip content={canWrite ? undefined : "You don't have guest-network write on this node."}>
                        <span>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!canWrite}
                            onClick={() => void handleToggleLink(row.ref, row.linkDown)}
                          >
                            {row.linkDown ? "Connect" : "Disconnect"}
                          </Button>
                        </span>
                      </Tooltip>
                    </TableCell>
                    <TableCell>
                      {/* T-3906: this guest's whole network story — NICs,
                          path evaluation, firewall verdict, flows,
                          findings — on one screen. */}
                      <Link
                        to={`/guest?ref=${encodeURIComponent(guestRefFromNicRef(row.ref))}`}
                        className="text-accent-600 underline hover:no-underline dark:text-accent-400"
                      >
                        Guest view
                      </Link>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </>
      )}
    </div>
  );
}
