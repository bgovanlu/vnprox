// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-1005's alert rules CRUD + delivery log
// (docs/api.md's Alert Rules section). Mirrors blueprints/queries.ts's
// convention: one hook per API call, mutations invalidate the queries they
// affect.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createAlertRule,
  deleteAlertRule,
  fetchAlertDeliveries,
  fetchAlertRules,
  testAlertRule,
  updateAlertRule,
} from "../api/alertrules";
import type { AlertRuleRequest } from "../api/types";

export const ALERT_RULES_QUERY_KEY = ["alert-rules"] as const;
export const alertDeliveriesQueryKey = (ruleId?: string, status?: string) =>
  ["alert-deliveries", ruleId ?? "", status ?? ""] as const;

export function useAlertRulesQuery() {
  return useQuery({ queryKey: ALERT_RULES_QUERY_KEY, queryFn: fetchAlertRules, staleTime: 15_000 });
}

export function useAlertDeliveriesQuery(filters: { ruleId?: string; status?: string } = {}) {
  return useQuery({
    queryKey: alertDeliveriesQueryKey(filters.ruleId, filters.status),
    queryFn: () => fetchAlertDeliveries(filters),
    staleTime: 5_000,
  });
}

export function useCreateAlertRuleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: AlertRuleRequest) => createAlertRule(req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALERT_RULES_QUERY_KEY });
    },
  });
}

export function useUpdateAlertRuleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: AlertRuleRequest }) => updateAlertRule(id, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALERT_RULES_QUERY_KEY });
    },
  });
}

export function useDeleteAlertRuleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteAlertRule(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ALERT_RULES_QUERY_KEY });
    },
  });
}

export function useTestAlertRuleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => testAlertRule(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["alert-deliveries"] });
    },
  });
}
