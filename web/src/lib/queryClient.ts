import { QueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/client";

/** Shared TanStack Query client. Per docs/development.md's TypeScript
 * standards ("server state via TanStack Query only — no fetch in
 * components"), this is the one QueryClient for the app; every query/
 * mutation fn should throw the shared ApiError type (see api/client.ts)
 * so this default retry policy applies uniformly. */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // Client errors (4xx) are never transient — retrying a
        // validation_failed or not-authenticated response wastes a
        // round trip and delays surfacing the error to the user.
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
          return false;
        }
        return failureCount < 2;
      },
      staleTime: 10_000,
    },
    mutations: {
      retry: false,
    },
  },
});
