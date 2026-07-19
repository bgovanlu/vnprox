// TanStack Query hook for T-1507's migration pre-flight check. Mirrors
// diagnose.ts/DiagnosisPage.tsx's postDiagnose+useMutation shape: a single
// on-demand mutation (the operator picks a target node and clicks
// "Check"), not a query, since there's nothing meaningful to poll or
// cache — a fresh assessment reflects current mesh/flow state at the
// moment it's requested.
import { useMutation } from "@tanstack/react-query";
import { postMigrationPreflight } from "../api/migration";

export function useMigrationPreflightMutation() {
  return useMutation({
    mutationFn: (vars: { guest: string; targetNode: string }) =>
      postMigrationPreflight({ guest: vars.guest, targetNode: vars.targetNode }),
  });
}
