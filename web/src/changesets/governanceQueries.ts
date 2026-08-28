// SPDX-License-Identifier: Apache-2.0

// T-3002's server state for the review screen's governance panels: the
// installed policy set, this changeset's standing against it, and the
// break-glass mutation. Kept in its own module rather than folded into
// queries.ts so the review screen's existing apply/validate cache keys are
// untouched.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchPolicies, testPolicy } from "../api/policies";
import type { PolicyResult, PolicyStatus } from "../api/policies";
import { invokeBreakGlass } from "./breakGlass";
import { changesetKey } from "./queries";

export const POLICIES_QUERY_KEY = ["policies"] as const;

export const policyVerdictKey = (changesetId: string) => ["policies", "test", changesetId] as const;

/** GET /policies. `retry: false` on purpose: a `503 policy_unavailable` from
 * a daemon with no policy store is a settled answer, not a transient one, and
 * retrying it three times only delays saying so. */
export function usePoliciesQuery(enabled = true) {
  return useQuery<PolicyStatus>({
    queryKey: POLICIES_QUERY_KEY,
    queryFn: fetchPolicies,
    enabled,
    retry: false,
    staleTime: 30_000,
  });
}

/** POST /policies/test against an existing changeset, with no `policy` — so
 * the INSTALLED set is what evaluates. It stages nothing and mutates nothing
 * (docs/api.md: it is `netRead` for that reason), which is why it is modelled
 * as a query rather than a mutation. */
export function usePolicyVerdictQuery(changesetId: string | undefined, enabled = true) {
  return useQuery<PolicyResult>({
    queryKey: policyVerdictKey(changesetId ?? ""),
    queryFn: () => testPolicy({ changesetId: changesetId ?? "" }),
    enabled: enabled && changesetId !== undefined && changesetId !== "",
    retry: false,
    staleTime: 15_000,
  });
}

/** POST /changesets/{id}/break-glass. Invalidates the changeset so the
 * two-person state the screen renders afterwards is the server's, not an
 * optimistic guess — the override is only effective because the server
 * recorded it. */
export function useBreakGlassMutation(changesetId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (reason: string) => invokeBreakGlass(changesetId, reason),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(changesetId) });
    },
  });
}
