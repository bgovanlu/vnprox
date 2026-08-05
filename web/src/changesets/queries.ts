// TanStack Query hooks for the changeset drawer/review/apply flow, plus the
// WS bridge that keeps an open changeset's status (and, crucially, the
// commit-confirm countdown) live — the same "targeted invalidation on a WS
// event" pattern topology/queries.ts's useTopologyWsBridge established.
import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  addChangesetComment,
  applyChangeset,
  confirmChangeset,
  createChangeset,
  deleteChangesetComment,
  diffChangeset,
  discardChangeset,
  getChangeset,
  listChangesets,
  reviewApproveChangeset,
  reviewRejectChangeset,
  rollbackChangeset,
  updateChangeset,
  validateChangeset,
} from "../api/changesets";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { Changeset, ChangesetDiff, ChangesetStatusEvent, Op } from "../api/types";

export const changesetKey = (id: string) => ["changesets", id] as const;
export const changesetListKey = (status?: string) => ["changesets", "list", status ?? ""] as const;
export const changesetDiffKey = (id: string) => ["changesets", id, "diff"] as const;

export function useChangesetQuery(id: string | undefined) {
  return useQuery<Changeset>({
    queryKey: changesetKey(id ?? ""),
    queryFn: () => getChangeset(id ?? ""),
    enabled: id !== undefined,
    staleTime: 5_000,
  });
}

/** Drafts (and validated-but-not-yet-applied changesets) available to
 * "park and resume" (docs/features/change-management.md §1). The server has
 * no combined "draft or validated" filter, so this fetches both statuses
 * and merges — small lists (one user's own in-progress changesets), so two
 * round trips is a reasonable tradeoff against widening the API contract
 * for this one UI need. */
export function useResumableDraftsQuery() {
  return useQuery<Changeset[]>({
    queryKey: ["changesets", "resumable"],
    queryFn: async () => {
      const [drafts, validated] = await Promise.all([listChangesets("draft"), listChangesets("validated")]);
      return [...drafts, ...validated].sort((a, b) => b.updatedAt - a.updatedAt);
    },
    staleTime: 5_000,
  });
}

export function useChangesetDiffQuery(id: string | undefined, enabled: boolean) {
  return useQuery<ChangesetDiff>({
    queryKey: changesetDiffKey(id ?? ""),
    queryFn: () => diffChangeset(id ?? ""),
    enabled: enabled && id !== undefined,
    staleTime: 5_000,
  });
}

export function useCreateChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { title: string; ops: Op[] }) => createChangeset(input),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
      void queryClient.invalidateQueries({ queryKey: ["changesets", "resumable"] });
    },
  });
}

export function useUpdateChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, title, ops }: { id: string; title?: string; ops: Op[] }) =>
      updateChangeset(id, { title, ops }),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
      void queryClient.invalidateQueries({ queryKey: changesetDiffKey(c.id) });
      void queryClient.invalidateQueries({ queryKey: ["changesets", "resumable"] });
    },
  });
}

export function useDiscardChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => discardChangeset(id),
    onSuccess: (_void, id) => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(id) });
      void queryClient.invalidateQueries({ queryKey: ["changesets", "resumable"] });
    },
  });
}

export function useValidateChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => validateChangeset(id),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
    },
  });
}

export function useApplyChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirmTimeoutSec, mgmtAck }: { id: string; confirmTimeoutSec: number; mgmtAck?: { node: string } }) =>
      applyChangeset(id, { confirmTimeoutSec, ...(mgmtAck ? { mgmtAck } : {}) }),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
    },
  });
}

export function useConfirmChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => confirmChangeset(id),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
    },
  });
}

export function useRollbackChangesetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => rollbackChangeset(id),
    onSuccess: (c) => {
      queryClient.setQueryData(changesetKey(c.id), c);
    },
  });
}

/** T-2003 review surface: comments and approve/reject all invalidate the
 * canonical changeset query (the only response that carries `comments`/
 * `approval`) so the review screen's next read reflects the change —
 * simpler and safer than trying to hand-patch the nested array/object in
 * place from each mutation's own narrow response shape. */
export function useAddCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, opId, body }: { id: string; opId?: string; body: string }) => addChangesetComment(id, opId, body),
    onSuccess: (_comment, { id }) => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(id) });
    },
  });
}

export function useDeleteCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, commentId }: { id: string; commentId: string }) => deleteChangesetComment(id, commentId),
    onSuccess: (_void, { id }) => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(id) });
    },
  });
}

export function useReviewApproveMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => reviewApproveChangeset(id),
    onSuccess: (_approval, id) => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(id) });
    },
  });
}

export function useReviewRejectMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) => reviewRejectChangeset(id, reason),
    onSuccess: (_approval, { id }) => {
      void queryClient.invalidateQueries({ queryKey: changesetKey(id) });
    },
  });
}

function isChangesetStatusEvent(evt: WsServerEvent): evt is WsServerEvent & ChangesetStatusEvent {
  return (
    evt.event === "changeset.status" &&
    typeof evt.id === "string" &&
    typeof evt.status === "string" &&
    (evt.confirmDeadline === undefined || typeof evt.confirmDeadline === "number")
  );
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/**
 * Subscribes to the `/api/ws` "changesets" topic for the component's
 * lifetime and invalidates the affected changeset's query on every
 * `changeset.status` event — this is what makes the countdown banner
 * "WS-driven" (T-207 acceptance criterion 3) rather than polled: apply,
 * confirm, and the auto-rollback timer all push a status change the very
 * moment the server-side state machine moves, and the banner/drawer
 * re-render from the next GET /changesets/{id} this triggers. Pass
 * `client` in tests to inject a client wired to a real `ws` test server.
 */
export function useChangesetWsBridge(client?: WsClient): void {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["changesets"], (evt) => {
      if (isChangesetStatusEvent(evt)) {
        void queryClientRef.current.invalidateQueries({ queryKey: changesetKey(evt.id) });
        void queryClientRef.current.invalidateQueries({ queryKey: ["changesets", "resumable"] });
      }
    });
    return unsubscribe;
  }, [client]);
}
