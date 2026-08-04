// TanStack Query mutation hooks for T-2001's federation cluster editor —
// the CRUD half of the T-1201 registry the wizard-facing
// topology/federation/federationQueries.ts already reads. Reuses that
// module's read hook and query key verbatim (one cache entry, one source of
// truth for "which clusters are attached") rather than duplicating it here.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  createFederationCluster,
  deleteFederationCluster,
  updateFederationCluster,
  type FederationCluster,
  type FederationClusterCreateRequest,
  type FederationClusterUpdateRequest,
} from "../api/federation";
import { FEDERATION_CLUSTERS_QUERY_KEY, FEDERATION_TOPOLOGY_QUERY_KEY, useFederationClustersQuery } from "../topology/federation/federationQueries";

export { useFederationClustersQuery };

export function useCreateFederationClusterMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: FederationClusterCreateRequest) => createFederationCluster(req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: FEDERATION_CLUSTERS_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: FEDERATION_TOPOLOGY_QUERY_KEY });
    },
  });
}

export function useUpdateFederationClusterMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: FederationClusterUpdateRequest }): Promise<FederationCluster> =>
      updateFederationCluster(id, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: FEDERATION_CLUSTERS_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: FEDERATION_TOPOLOGY_QUERY_KEY });
    },
  });
}

export function useDeleteFederationClusterMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteFederationCluster(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: FEDERATION_CLUSTERS_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: FEDERATION_TOPOLOGY_QUERY_KEY });
    },
  });
}
