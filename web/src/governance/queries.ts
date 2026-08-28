// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-3002's governance surfaces. Every server read on
// the governance screen goes through here; no component fetches.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchComplianceProfiles, fetchComplianceReport } from "../api/compliance";
import type { ComplianceProfileSummary, ComplianceReport } from "../api/compliance";
import { fetchDigestSchedule, putDigestSchedule } from "../api/digest";
import type { DigestSchedule, DigestScheduleUpdate } from "../api/digest";
import { putPolicies } from "../api/policies";
import type { PolicySet, PolicyStatus } from "../api/policies";
import {
  addTenantScope,
  createTenant,
  deleteTenant,
  fetchTenant,
  fetchTenants,
  putTenantMember,
  removeTenantMember,
  removeTenantScope,
} from "../api/tenants";
import type { TenantDetail, TenantListItem, TenantRole } from "../api/tenants";
import { POLICIES_QUERY_KEY } from "../changesets/governanceQueries";

export const COMPLIANCE_PROFILES_KEY = ["compliance", "profiles"] as const;
export const complianceReportKey = (profile: string) => ["compliance", "report", profile] as const;
export const DIGEST_SCHEDULE_KEY = ["digest", "schedule"] as const;
export const TENANTS_KEY = ["tenants"] as const;
export const tenantKey = (id: string) => ["tenants", id] as const;

// ---- policies -------------------------------------------------------------

/** PUT /policies — replaces the installed document wholesale. Invalidates the
 * read AND every open changeset's evaluation, since what a changeset violates
 * is a function of the set that was just replaced. */
export function useInstallPoliciesMutation() {
  const queryClient = useQueryClient();
  return useMutation<PolicyStatus, Error, PolicySet>({
    mutationFn: (set: PolicySet) => putPolicies(set),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: POLICIES_QUERY_KEY });
    },
  });
}

// ---- compliance -----------------------------------------------------------

export function useComplianceProfilesQuery() {
  return useQuery<ComplianceProfileSummary[]>({
    queryKey: COMPLIANCE_PROFILES_KEY,
    queryFn: async () => (await fetchComplianceProfiles()).items,
    staleTime: 60_000,
  });
}

export function useComplianceReportQuery(profile: string | undefined) {
  return useQuery<ComplianceReport>({
    queryKey: complianceReportKey(profile ?? ""),
    queryFn: () => fetchComplianceReport(profile ?? ""),
    enabled: profile !== undefined && profile !== "",
    retry: false,
    staleTime: 30_000,
  });
}

// ---- digest ---------------------------------------------------------------

/** GET /digest/schedule. `retry: false` because the two interesting failures
 * — `501 not_implemented` on a deployment without the digest store, and a
 * `403` — are settled answers, not transient ones. */
export function useDigestScheduleQuery() {
  return useQuery<DigestSchedule>({
    queryKey: DIGEST_SCHEDULE_KEY,
    queryFn: fetchDigestSchedule,
    retry: false,
    staleTime: 30_000,
  });
}

/** PUT /digest/schedule. The response IS the stored value, so it is written
 * straight into the cache: re-rendering from what the daemon stored (rather
 * than from the body we sent) is what makes the round trip observable. */
export function usePutDigestScheduleMutation() {
  const queryClient = useQueryClient();
  return useMutation<DigestSchedule, Error, DigestScheduleUpdate>({
    mutationFn: (update: DigestScheduleUpdate) => putDigestSchedule(update),
    onSuccess: (stored) => {
      queryClient.setQueryData(DIGEST_SCHEDULE_KEY, stored);
      void queryClient.invalidateQueries({ queryKey: DIGEST_SCHEDULE_KEY });
    },
  });
}

// ---- tenants --------------------------------------------------------------

export function useTenantsQuery() {
  return useQuery<TenantListItem[]>({
    queryKey: TENANTS_KEY,
    queryFn: fetchTenants,
    staleTime: 30_000,
  });
}

/** GET /tenants/{id} — the ONLY route that answers a tenant's scopes and
 * members. The list route reports both as empty arrays without reading
 * either table (see api/tenants.ts), so nothing may be concluded about them
 * until this has resolved. */
export function useTenantQuery(id: string | undefined) {
  return useQuery<TenantDetail>({
    queryKey: tenantKey(id ?? ""),
    queryFn: () => fetchTenant(id ?? ""),
    enabled: id !== undefined && id !== "",
    retry: false,
    staleTime: 15_000,
  });
}

export function useCreateTenantMutation() {
  const queryClient = useQueryClient();
  return useMutation<TenantDetail, Error, { name: string; id?: string }>({
    mutationFn: ({ name, id }) => createTenant(name, id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TENANTS_KEY });
    },
  });
}

export function useDeleteTenantMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteTenant(id),
    onSuccess: (_result, id) => {
      void queryClient.invalidateQueries({ queryKey: TENANTS_KEY });
      void queryClient.invalidateQueries({ queryKey: tenantKey(id) });
    },
  });
}

export function useAddScopeMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, scopeRef }: { id: string; scopeRef: string }) => addTenantScope(id, scopeRef),
    onSuccess: (_result, { id }) => {
      void queryClient.invalidateQueries({ queryKey: tenantKey(id) });
    },
  });
}

export function useRemoveScopeMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, scopeRef }: { id: string; scopeRef: string }) => removeTenantScope(id, scopeRef),
    onSuccess: (_result, { id }) => {
      void queryClient.invalidateQueries({ queryKey: tenantKey(id) });
    },
  });
}

export function usePutMemberMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, identity, role }: { id: string; identity: string; role: TenantRole }) =>
      putTenantMember(id, identity, role),
    onSuccess: (_result, { id }) => {
      void queryClient.invalidateQueries({ queryKey: tenantKey(id) });
    },
  });
}

export function useRemoveMemberMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, identity }: { id: string; identity: string }) => removeTenantMember(id, identity),
    onSuccess: (_result, { id }) => {
      void queryClient.invalidateQueries({ queryKey: tenantKey(id) });
    },
  });
}
