// SPDX-License-Identifier: Apache-2.0

// T-703 AC6's post-commit protected-set refresh prompt: after flow C moves
// a node's management address to a new carrier and the changeset commits,
// GET /protected-interfaces/status reports staleProtected (the confirmed
// set no longer names the live carrier). This banner offers to refresh the
// protected set from GET /protected-interfaces/suggest (accepting PUTs the
// new carrier ref, audited by the backend). Declining leaves protected.json
// untouched — the staleProtected warning stays visible (in the inspector's
// Management path section) until the admin acts. Mounted app-wide in the
// shell, so it appears wherever the user is when the status goes stale.
import { useState } from "react";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { useMgmtStatusQuery, MGMT_STATUS_QUERY_KEY } from "../topology/queries";
import { fetchProtectedInterfacesSuggest } from "../api/protectedInterfaces";
import { useSaveProtectedInterfacesMutation } from "../onboarding/queries";
import { useQueryClient } from "@tanstack/react-query";
import { mgmtStrings } from "./strings";

const S = mgmtStrings.refreshPrompt;

export function MgmtProtectedRefreshPrompt() {
  const { data: status } = useMgmtStatusQuery();
  const save = useSaveProtectedInterfacesMutation();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [dismissed, setDismissed] = useState(false);
  const [busy, setBusy] = useState(false);

  if (!status?.staleProtected || dismissed) return null;

  async function handleAccept(): Promise<void> {
    setBusy(true);
    try {
      const suggest = await fetchProtectedInterfacesSuggest();
      await save.mutateAsync({ nodes: suggest.nodes });
      await queryClient.invalidateQueries({ queryKey: MGMT_STATUS_QUERY_KEY });
      toast({ title: S.updated, variant: "success" });
      setDismissed(true);
    } catch {
      toast({ title: S.failed, variant: "error" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      role="alert"
      className="fixed inset-x-0 top-0 z-40 flex flex-wrap items-center justify-center gap-3 border-b border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
    >
      <span className="max-w-2xl">
        <span className="font-medium">{S.title}</span> {S.body}
      </span>
      <Button size="sm" variant="primary" disabled={busy} onClick={() => void handleAccept()}>
        {S.accept}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => {
          setDismissed(true);
        }}
      >
        {S.decline}
      </Button>
    </div>
  );
}
