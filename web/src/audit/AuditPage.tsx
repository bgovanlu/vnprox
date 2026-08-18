// Audit — filterable table over vnprox's own audit log (T-206,
// docs/features/change-management.md §8: "Filterable table (user, date
// range, target, result) over the merged cluster audit log; each row
// expands to op summaries and links to the changeset and its snapshots").
// The merged (multi-node) view is single-node until T-303; the query
// already fans out through a single /audit endpoint whose backing store
// query is fan-out-ready (cursor + filter over the same shape per node).
import { Fragment, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
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
  if (result.includes("fail") || result.includes("error") || result === "locked" || result === "denied") {
    return "text-red-600 dark:text-red-400";
  }
  if (result.includes("warn")) {
    return "text-amber-600 dark:text-amber-400";
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
        <p className="text-sm text-slate-500">Loading audit log…</p>
      ) : auditQuery.isError ? (
        <EmptyState
          title="Could not load the audit log"
          description={auditQuery.error instanceof ApiError ? auditQuery.error.message : "unexpected error"}
        />
      ) : entries.length === 0 ? (
        <EmptyState
          title="No matching audit entries"
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
            <TableBody>
              {entries.map((entry) => (
                <Fragment key={entry.id}>
                  <TableRow
                    className="cursor-pointer"
                    aria-expanded={expandedId === entry.id}
                    onClick={() => {
                      setExpandedId((prev) => (prev === entry.id ? undefined : entry.id));
                    }}
                  >
                    <TableCell className="whitespace-nowrap">{formatTime(entry.at)}</TableCell>
                    <TableCell>{entry.username}</TableCell>
                    <TableCell>{entry.action}</TableCell>
                    <TableCell className="max-w-48 truncate">{entry.target ?? ""}</TableCell>
                    <TableCell className="max-w-48 truncate font-mono text-xs">
                      {entry.changesetId ?? ""}
                    </TableCell>
                    <TableCell className={resultTone(entry.result)}>{entry.result}</TableCell>
                  </TableRow>
                  {expandedId === entry.id ? (
                    <TableRow>
                      <TableCell colSpan={6} className="bg-slate-50 dark:bg-slate-900/60">
                        <ExpandedDetail entry={entry} />
                      </TableCell>
                    </TableRow>
                  ) : null}
                </Fragment>
              ))}
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
    <label className="flex flex-col gap-1 text-xs text-slate-500 dark:text-slate-400">
      {label}
      <input
        type={type}
        value={value}
        placeholder={placeholder}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        className="h-9 rounded-md border border-slate-300 bg-white px-2.5 text-sm text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
      />
    </label>
  );
}

function ExpandedDetail({ entry }: { entry: AuditEntry }) {
  return (
    <div className="flex flex-col gap-2 py-1 text-sm">
      {entry.changesetId ? (
        <p className="text-slate-600 dark:text-slate-300">
          Changeset: <span className="font-mono text-xs">{entry.changesetId}</span> — its snapshots
          appear on the History page under this changeset.
        </p>
      ) : null}
      {entry.detail ? (
        <pre className="overflow-x-auto rounded-md border border-slate-200 bg-white p-2 text-xs dark:border-slate-800 dark:bg-slate-950">
          {JSON.stringify(entry.detail, null, 2)}
        </pre>
      ) : (
        <p className="text-slate-500">No additional detail recorded.</p>
      )}
    </div>
  );
}
