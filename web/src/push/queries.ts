// TanStack Query hooks over api/push.ts — kept separate from the
// browserPush.ts wrapper (which knows nothing about the server) and from
// PushSettingsSection.tsx (which composes both).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createPushSubscription,
  deletePushSubscription,
  fetchPushSubscriptions,
  fetchVapidPublicKey,
  type PushCategory,
  type PushSubscriptionJSON,
} from "../api/push";

export const VAPID_PUBLIC_KEY_QUERY_KEY = ["push", "vapid-public-key"] as const;
export const PUSH_SUBSCRIPTIONS_QUERY_KEY = ["push", "subscriptions"] as const;

export function useVapidPublicKeyQuery() {
  return useQuery({
    queryKey: VAPID_PUBLIC_KEY_QUERY_KEY,
    queryFn: fetchVapidPublicKey,
    // The daemon's VAPID identity never changes without an operator
    // deleting the key file (vapid.go's doc comment: "rotation is an
    // explicit operator action"), so this is safe to treat as effectively
    // static for the lifetime of a session.
    staleTime: Infinity,
  });
}

export function usePushSubscriptionsQuery() {
  return useQuery({
    queryKey: PUSH_SUBSCRIPTIONS_QUERY_KEY,
    queryFn: fetchPushSubscriptions,
  });
}

export function useCreatePushSubscriptionMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: { subscription: PushSubscriptionJSON; categories: PushCategory[]; deviceLabel?: string }) =>
      createPushSubscription(vars.subscription, vars.categories, vars.deviceLabel),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PUSH_SUBSCRIPTIONS_QUERY_KEY });
    },
  });
}

export function useDeletePushSubscriptionMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deletePushSubscription(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PUSH_SUBSCRIPTIONS_QUERY_KEY });
    },
  });
}
