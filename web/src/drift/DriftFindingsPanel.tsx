// T-305's drift findings stream: fetches GET /drift, stays live via the
// drift.changed WS bridge, and wires "Create fixing changeset" to
// POST /drift/{id}/fix + opening the resulting draft in the changeset
// drawer for review (never applies anything itself — see FindingsList's
// doc comment on why this container/presentational split exists).
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import { FindingsList } from "../findings/FindingsList";
import { DRIFT_QUERY_KEY, useDriftQuery, useDriftWsBridge, useFixDriftMutation } from "./queries";

export function DriftFindingsPanel() {
  const { data: findings, isLoading, error } = useDriftQuery();
  const fixMutation = useFixDriftMutation();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [fixingId, setFixingId] = useState<string | undefined>(undefined);

  useDriftWsBridge();

  async function handleFix(id: string): Promise<void> {
    setFixingId(id);
    try {
      const changeset = await fixMutation.mutateAsync(id);
      setActiveId(changeset.id);
      void queryClient.invalidateQueries({ queryKey: DRIFT_QUERY_KEY });
      toast({ title: "Fixing changeset created", description: "Review it in the drawer before applying.", variant: "success" });
    } catch {
      toast({ title: "Could not create fixing changeset", variant: "error" });
    } finally {
      setFixingId(undefined);
    }
  }

  if (isLoading) {
    return <p className="text-sm text-slate-500 dark:text-slate-400">Loading drift findings…</p>;
  }
  if (error) {
    return <p className="text-sm text-red-600 dark:text-red-400">Could not load drift findings.</p>;
  }

  return (
    <FindingsList
      findings={(findings ?? []).map((f) => ({
        id: f.id,
        severity: f.severity,
        detail: f.detail,
        nodes: f.nodes,
        refs: f.refs,
        fixable: f.fixable,
        category: f.check,
      }))}
      onFix={(id) => { void handleFix(id); }}
      fixingId={fixingId}
      emptyTitle="No drift detected"
      emptyDescription="The cluster's configuration is consistent across nodes."
    />
  );
}
