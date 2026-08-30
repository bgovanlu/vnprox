// SPDX-License-Identifier: Apache-2.0

// Audit — filterable table over vnprox's own audit log (T-206,
// docs/features/change-management.md §8: "Filterable table (user, date
// range, target, result) over the merged cluster audit log; each row
// expands to op summaries and links to the changeset and its snapshots").
// The merged (multi-node) view is single-node until T-303; the query
// already fans out through a single /audit endpoint whose backing store
// query is fan-out-ready (cursor + filter over the same shape per node).
import { Fragment, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight } from "lucide-react";
import { fetchAudit, type AuditEntry, type AuditFilter, type AuditListResponse } from "../api/audit";
import { emptyForm, toAuditFilter, type FilterForm } from "./filters";
import { ApiError } from "../api/client";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";

const PAGE_SIZE = 50;

function formatTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

function resultTone(result: string): string {
  // T-3406-followup-01: these render straight on TableCell/AppShell's
  // bg-slate-100 canvas (Table's own wrapper carries only a border, no
  // background — see Table.tsx's TableHeader comment for the same shape of
  // bug). Computed (Tailwind v4 OKLCH -> sRGB): red-600 #e7000b measures
  // 4.35:1 there and amber-600 #e17100 measures 2.91:1, both under the
  // 4.5:1 floor; red-700 #c10007 clears it at 5.86:1 and amber-800 #973c00
  // at 6.50:1 (amber-700's 4.61:1 was too close to the floor to trust, per
  // this codebase's own T-3406 lesson about thin margins). Dark mode is
  // unaffected — red-400/amber-400 already clear 6:1+ against slate-900.
  if (result.includes("fail") || result.includes("error") || result === "locked" || result === "denied") {
    return "text-red-700 dark:text-red-400";
  }
  if (result.includes("warn")) {
    return "text-amber-800 dark:text-amber-400";
  }
  return "text-emerald-700 dark:text-emerald-400";
}

export function AuditPage() {
  const [form, setForm] = useState<FilterForm>(emptyForm);
  const [applied, setApplied] = useState<AuditFilter>({});
  const [expandedId, setExpandedId] = useState<number | undefined>(undefined);

  const auditQuery = useInfiniteQuery<AuditListResponse>({
    queryKey: ["audit", applied],
    queryFn: ({ pageParam }) =>
      fetchAudit(applied, typeof pageParam === "string" && pageParam !== "" ? pageParam : undefined, PAGE_SIZE),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });

  const entries = useMemo(
    () => (auditQuery.data?.pages ?? []).flatMap((p) => p.items),
    [auditQuery.data],
  );

  // T-4209: distinguishes "nothing has ever been logged" from "a filter the
  // operator set matched nothing" — `applied` starts as `{}` (truly no
  // keys) and toAuditFilter always sets every key, just possibly to
  // `undefined`, so "any key holds a defined value" is exactly "a filter is
  // in effect".
  const hasActiveFilter = Object.values(applied).some((v) => v !== undefined);

  const setField = (field: keyof FilterForm) => (value: string) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  return (
    <div className="flex h-full flex-col gap-4">
      <PageHeader title="Audit" />

      <form
        className="flex flex-wrap items-end gap-2"
        aria-label="Audit filters"
        onSubmit={(e) => {
          e.preventDefault();
          setExpandedId(undefined);
          setApplied(toAuditFilter(form));
        }}
      >
        <FilterInput label="User" value={form.user} onChange={setField("user")} placeholder="root@pam" />
        <FilterInput label="Result" value={form.result} onChange={setField("result")} placeholder="failed" />
        <FilterInput label="Target" value={form.target} onChange={setField("target")} placeholder="bridge:pve1:vmbr0" />
        <FilterInput label="From" value={form.from} onChange={setField("from")} type="datetime-local" />
        <FilterInput label="To" value={form.to} onChange={setField("to")} type="datetime-local" />
        <Button type="submit" variant="primary">
          Filter
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setForm(emptyForm);
            setApplied({});
            setExpandedId(undefined);
          }}
        >
          Clear
        </Button>
      </form>

      {auditQuery.isLoading ? (
        <p className="text-sm text-fg-muted">Loading audit log…</p>
      ) : auditQuery.isError ? (
        <EmptyState
          icon="node"
          variant="failed"
          title="Could not load the audit log"
          description={auditQuery.error instanceof ApiError ? auditQuery.error.message : "unexpected error"}
          action={
            <Button variant="secondary" size="sm" onClick={() => void auditQuery.refetch()}>
              Retry
            </Button>
          }
        />
      ) : entries.length === 0 && hasActiveFilter ? (
        <EmptyState
          icon="node"
          variant="filtered"
          title="No matching audit entries"
          description="No recorded change attempt matches the current filter."
          action={
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setForm(emptyForm);
                setApplied({});
                setExpandedId(undefined);
              }}
            >
              Clear filters
            </Button>
          }
        />
      ) : entries.length === 0 ? (
        <EmptyState
          icon="node"
          variant="empty"
          title="No audit entries yet"
          description="Every change attempt — including denied and rolled-back ones — is recorded here."
        />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>User</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Changeset</TableHead>
                <TableHead>Result</TableHead>
              </TableRow>
            </TableHeader>
            {/* T-4213: masked out of the visual gate. This table is a live
                feed of actions taken against the daemon — and the e2e suite's
                own logins are among them, so the rows differ between any two
                runs in count, order and timestamp. Masking only the timestamp
                cell was measured and was not enough (8.2% -> 8.0%): the ROWS
                differ, not just their times. The page's chrome, filters and
                table header stay gated; only the feed is painted over. */}
            <TableBody data-volatile-time>
              {entries.map((entry) => {
                const expanded = expandedId === entry.id;
                const detailId = `audit-detail-${String(entry.id)}`;
                return (
                  <Fragment key={entry.id}>
                    <TableRow
                      className="cursor-pointer"
                      onClick={() => {
                        setExpandedId((prev) => (prev === entry.id ? undefined : entry.id));
                      }}
                    >
                      {/* T-3406-followup-01: aria-expanded belongs on a control
                       * that accepts it — a plain `<tr>` (role="row" outside a
                       * treegrid) does not, per ARIA's aria-expanded spec, and
                       * axe's aria-conditional-attr rule flags it regardless of
                       * theme. The row keeps its own onClick for the existing
                       * click-anywhere-in-the-row behavior; this button carries
                       * the actual expand/collapse semantics for assistive
                       * tech, and is a real keyboard-operable control the bare
                       * row never was. */}
                      <TableCell className="whitespace-nowrap">
                        <span className="flex items-center gap-1.5">
                          <button
                            type="button"
                            aria-expanded={expanded}
                            aria-controls={detailId}
                            aria-label={expanded ? "Collapse entry details" : "Expand entry details"}
                            className="shrink-0 rounded text-fg-muted hover:text-slate-900 dark:hover:text-slate-100"
                            onClick={(e) => {
                              e.stopPropagation();
                              setExpandedId((prev) => (prev === entry.id ? undefined : entry.id));
                            }}
                          >
                            {expanded ? (
                              <ChevronDown aria-hidden className="h-4 w-4" />
                            ) : (
                              <ChevronRight aria-hidden className="h-4 w-4" />
                            )}
                          </button>
                          {formatTime(entry.at)}
                        </span>
                      </TableCell>
                      <TableCell>{entry.username}</TableCell>
                      <TableCell>{entry.action}</TableCell>
                      <TableCell className="max-w-48 truncate">{entry.target ?? ""}</TableCell>
                      <TableCell className="max-w-48 truncate font-mono text-xs">
                        {entry.changesetId ?? ""}
                      </TableCell>
                      <TableCell className={resultTone(entry.result)}>{entry.result}</TableCell>
                    </TableRow>
                    {expanded ? (
                      <TableRow id={detailId}>
                        <TableCell colSpan={6} className="bg-slate-50 dark:bg-slate-900/60">
                          <ExpandedDetail entry={entry} />
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
          {auditQuery.hasNextPage ? (
            <Button
              variant="secondary"
              onClick={() => {
                void auditQuery.fetchNextPage();
              }}
              disabled={auditQuery.isFetchingNextPage}
            >
              {auditQuery.isFetchingNextPage ? "Loading…" : "Load older entries"}
            </Button>
          ) : null}
        </div>
      )}
    </div>
  );
}

interface FilterInputProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
}

function FilterInput({ label, value, onChange, placeholder, type = "text" }: FilterInputProps) {
  return (
    <label className="flex flex-col gap-1 text-xs text-fg-muted">
      {label}
      <input
        type={type}
        value={value}
        placeholder={placeholder}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        className="h-9 rounded-md border border-border-strong bg-white px-2.5 text-sm text-fg dark:bg-slate-900"
      />
    </label>
  );
}

function ExpandedDetail({ entry }: { entry: AuditEntry }) {
  return (
    <div className="flex flex-col gap-2 py-1 text-sm">
      {entry.changesetId ? (
        <p className="text-fg-muted">
          Changeset: <span className="font-mono text-xs">{entry.changesetId}</span> — its snapshots
          appear on the History page under this changeset.
        </p>
      ) : null}
      {entry.detail ? (
        <pre className="overflow-x-auto rounded-md border border-border bg-white p-2 text-xs dark:bg-slate-950">
          {JSON.stringify(entry.detail, null, 2)}
        </pre>
      ) : (
        <p className="text-fg-muted">No additional detail recorded.</p>
      )}
    </div>
  );
}
