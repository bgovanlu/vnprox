import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { login } from "../api/auth";
import { useSession, SESSION_QUERY_KEY } from "../api/useSession";
import { AUTH_STUB_ENABLED, useDemoSessionStore } from "../store/authStub";
import { ApiError } from "../api/client";
import { Button } from "../components/Button";

interface LocationState {
  from?: { pathname: string };
}

/**
 * Login page + session bootstrap entry point. Posts to the real
 * `/auth/login` (docs/api.md §Auth) with Proxmox credentials — T-105's
 * backend implements it end to end (under `make dev`, the pvemock fixture
 * users work, e.g. root / vnprox-mock, realm pam). The demo-mode button
 * below (gated by AUTH_STUB_ENABLED, off by default — see
 * store/authStub.ts) remains only for demoing the SPA with no backend.
 */
export function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [realm, setRealm] = useState("pam");
  const [otp, setOtp] = useState("");

  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const { data: session } = useSession();
  const demoSession = useDemoSessionStore((s) => s.demoSession);
  const enterDemoMode = useDemoSessionStore((s) => s.enterDemoMode);

  const from = (location.state as LocationState | null)?.from?.pathname ?? "/topology";

  useEffect(() => {
    if (session || demoSession) {
      void navigate(from, { replace: true });
    }
    // Only re-run when auth state itself changes, not on every `from`/
    // navigate identity change (react-router recreates both often).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, demoSession]);

  const mutation = useMutation({
    mutationFn: login,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY });
      void navigate(from, { replace: true });
    },
  });

  function handleSubmit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    mutation.mutate({ username, password, realm, otp: otp || undefined });
  }

  const errorMessage =
    mutation.error instanceof ApiError
      ? mutation.error.status === 404
        ? "This backend doesn't serve /auth/login — check that vnproxd is current."
        : mutation.error.message
      : mutation.error
        ? "Login failed."
        : undefined;

  return (
    <div className="flex h-dvh items-center justify-center bg-slate-100 dark:bg-slate-950">
      <div className="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h1 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Sign in to vnprox</h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Use your Proxmox VE credentials — vnprox has no separate accounts.
        </p>

        <form className="mt-5 flex flex-col gap-3" onSubmit={handleSubmit}>
          <label className="flex flex-col gap-1 text-sm">
            Username
            <input
              className="rounded-md border border-slate-300 px-2.5 py-1.5 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={username}
              onChange={(e) => { setUsername(e.target.value); }}
              autoComplete="username"
              required
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Password
            <input
              type="password"
              className="rounded-md border border-slate-300 px-2.5 py-1.5 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={password}
              onChange={(e) => { setPassword(e.target.value); }}
              autoComplete="current-password"
              required
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Realm
            <input
              className="rounded-md border border-slate-300 px-2.5 py-1.5 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={realm}
              onChange={(e) => { setRealm(e.target.value); }}
              required
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            One-time password (if enabled)
            <input
              className="rounded-md border border-slate-300 px-2.5 py-1.5 text-sm dark:border-slate-700 dark:bg-slate-800"
              value={otp}
              onChange={(e) => { setOtp(e.target.value); }}
              autoComplete="one-time-code"
            />
          </label>

          {errorMessage ? (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {errorMessage}
            </p>
          ) : null}

          <Button type="submit" variant="primary" disabled={mutation.isPending}>
            {mutation.isPending ? "Signing in…" : "Sign in"}
          </Button>
        </form>

        {AUTH_STUB_ENABLED ? (
          <div className="mt-4 border-t border-slate-200 pt-4 dark:border-slate-800">
            <p className="mb-2 text-xs text-slate-500 dark:text-slate-400">
              Demo mode: explore the UI without a backend (client-side only).
            </p>
            <Button
              variant="secondary"
              className="w-full"
              onClick={() => {
                enterDemoMode();
              }}
            >
              Continue in demo mode
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
