// SPDX-License-Identifier: Apache-2.0

// Automation tokens (T-1104 routes, T-2903 semantics) — the panel that ends
// the `curl` + CSRF-double-submit ceremony minting one used to require.
//
// Two things this section exists to make visible, both introduced by T-2903
// with no UI to observe them:
//
//  1. **Expiry.** A token minted today expires in 90 days unless the request
//     says otherwise. The mint form does NOT compute that date — it omits
//     `expiresAt` and shows back whatever the 201 response carried, so the
//     default lives in exactly one place (the daemon's `defaultTokenTTL`) and
//     the UI cannot drift from it.
//  2. **Stored scope vs. effective scope.** In a `[server] read_only`
//     deployment, `internal/auth`'s bearer middleware runs `forceReadOnly`
//     over a token's scopes on every request, so a token whose stored scope
//     says `netWrite` is not a token that can write. The list states both,
//     and says which scopes the deployment is removing — a token whose two
//     scopes differ is exactly the confusion T-2903 existed to end.
//
// The read-only narrowing is deliberately three-valued: until `GET /config`
// answers, the effective scope is *unknown*, and the table says so rather
// than rendering the stored scope as though it were effective.
import { useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { ApiError } from "../api/client";
import type { ApiToken, ApiTokenCreateResponse } from "../api/types";
import { useInstanceConfigQuery } from "./queries";
import { useMintTokenMutation, useRevokeTokenMutation, useTokensQuery } from "./platformQueries";
import {
  canGrantScope,
  tokenExpiry,
  tokenLifecycle,
  tokenScopeNarrowing,
  DEFAULT_TOKEN_TTL_SEC,
  TOKEN_SCOPES,
} from "./tokenScope";
import { PlatformSection, RefusalNotice, ScopeChip, ScopeChips, UnixTime } from "./platformCommon";

type ExpiryChoice = "default" | "never" | "date";

function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}

/** `YYYY-MM-DD` for the date input's default value: today + the daemon's own
 * default TTL, so "on a specific date" starts somewhere sensible rather than
 * empty. Only ever a starting value for a control the operator then edits. */
function defaultExpiryDate(): string {
  const d = new Date((nowSec() + DEFAULT_TOKEN_TTL_SEC) * 1000);
  return d.toISOString().slice(0, 10);
}

/** The three-valued effective-scope cell. */
function EffectiveScopeCell({ token, readOnly }: { token: ApiToken; readOnly: boolean | undefined }) {
  const narrowing = tokenScopeNarrowing(token.scopes, readOnly);

  if (!narrowing.known) {
    return (
      <span className="text-xs italic text-slate-600 dark:text-slate-400" data-testid={`effective-scope-${token.id}`} data-scope-state="unknown">
        Unknown — this instance&rsquo;s configuration has not loaded, so whether{" "}
        <code>read_only</code> is narrowing this token cannot be stated yet.
      </span>
    );
  }

  if (!narrowing.narrowed) {
    return (
      <span className="text-xs text-slate-600 dark:text-slate-300" data-testid={`effective-scope-${token.id}`} data-scope-state="same">
        Same as stored
      </span>
    );
  }

  return (
    <span data-testid={`effective-scope-${token.id}`} data-scope-state="narrowed">
      <span className="inline-flex flex-wrap gap-1">
        {token.scopes.map((s) => (
          <ScopeChip key={s} name={s} tone={narrowing.removed.includes(s) ? "removed" : "neutral"} />
        ))}
      </span>
      <span className="mt-1 block text-xs font-medium text-amber-700 dark:text-amber-300">
        Narrowed by this deployment&rsquo;s <code>[server] read_only</code>: {narrowing.removed.join(", ")} removed on
        every request.
      </span>
    </span>
  );
}

function ExpiryCell({ token }: { token: ApiToken }) {
  const expiry = tokenExpiry(token, nowSec());
  switch (expiry.kind) {
    case "never":
      return (
        <span data-testid={`expiry-${token.id}`} data-expiry-state="never" className="text-slate-600 dark:text-slate-300">
          Never expires
        </span>
      );
    case "expired":
      return (
        <span data-testid={`expiry-${token.id}`} data-expiry-state="expired" className="text-red-700 dark:text-red-300">
          Expired <UnixTime at={expiry.at} />
        </span>
      );
    case "expires":
      return (
        <span data-testid={`expiry-${token.id}`} data-expiry-state="expires" className="text-slate-600 dark:text-slate-300">
          <UnixTime at={expiry.at} />
        </span>
      );
  }
}

function LifecycleBadge({ token }: { token: ApiToken }) {
  const state = tokenLifecycle(token, nowSec());
  const cls =
    state === "active"
      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
      : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400";
  return (
    <span data-testid={`lifecycle-${token.id}`} className={clsx("rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide", cls)}>
      {state}
    </span>
  );
}

export function TokensSection() {
  const { data: session } = useSession();
  const { data: config } = useInstanceConfigQuery();
  const tokensQuery = useTokensQuery();
  const mint = useMintTokenMutation();
  const revoke = useRevokeTokenMutation();
  const { toast } = useToast();

  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [expiryChoice, setExpiryChoice] = useState<ExpiryChoice>("default");
  const [expiryDate, setExpiryDate] = useState(defaultExpiryDate);
  const [minted, setMinted] = useState<ApiTokenCreateResponse | null>(null);
  const [mintError, setMintError] = useState<unknown>(null);

  function toggleScope(scope: string): void {
    setScopes((prev) => (prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]));
  }

  function handleMint(): void {
    setMintError(null);
    const base = { name: name.trim(), scopes };
    // Three-valued on purpose: `expiresAt` omitted selects the daemon's
    // 90-day default, `null` mints a non-expiring token, a number pins an
    // instant. See ApiTokenCreateRequest.
    const req =
      expiryChoice === "default"
        ? base
        : expiryChoice === "never"
          ? { ...base, expiresAt: null }
          : { ...base, expiresAt: Math.floor(new Date(`${expiryDate}T00:00:00`).getTime() / 1000) };

    mint.mutate(req, {
      onSuccess: (resp) => {
        setMinted(resp);
        setName("");
        setScopes([]);
        setExpiryChoice("default");
        toast({ title: "Token minted", description: resp.name, variant: "success" });
      },
      onError: (err: unknown) => {
        setMintError(err);
      },
    });
  }

  function handleRevoke(token: ApiToken): void {
    revoke.mutate(token.id, {
      onSuccess: () => {
        toast({ title: "Token revoked", description: token.name });
      },
      onError: (err: unknown) => {
        toast({
          title: "Could not revoke token",
          description: err instanceof ApiError ? err.message : "unexpected error",
          variant: "error",
        });
      },
    });
  }

  const tokens = tokensQuery.data ?? [];
  const readOnly = config?.readOnly;
  const canMint = name.trim().length > 0 && !mint.isPending;

  return (
    <PlatformSection
      title="Automation tokens"
      helpTopic="platform-tokens"
      description={
        <>
          Capability-scoped bearer credentials you mint for automation. A token can never carry a scope you do not
          hold yourself, and its raw value is shown exactly once — at creation.
        </>
      }
    >
      {readOnly === true && (
        <p
          data-testid="tokens-read-only-banner"
          className="mb-3 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200"
        >
          This deployment runs with <code>[server] read_only = true</code>. Every token below has its write scopes (
          <code>netWrite</code>, <code>sdnWrite</code>, <code>fwWrite</code>, <code>guestNet</code>,{" "}
          <code>capture</code>, <code>automationWrite</code>) removed on every request, whatever its stored scope
          says. <code>audit</code> and <code>automation</code> (which still covers the WS &quot;events&quot; topic and{" "}
          <code>GET /webhooks</code>) are not affected.
        </p>
      )}

      {/* --- mint form ------------------------------------------------- */}
      <div className="mb-4 rounded-md border border-slate-200 p-3 dark:border-slate-700">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-400">
          Mint a token
        </h3>

        <label className="mt-2 block text-sm">
          <span className="text-slate-600 dark:text-slate-300">Name</span>
          <input
            type="text"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            placeholder="terraform-ci"
            className="mt-1 block w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-900"
          />
        </label>

        <fieldset className="mt-3">
          <legend className="text-sm text-slate-600 dark:text-slate-300">Scopes</legend>
          <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
            {TOKEN_SCOPES.map((scope) => {
              const grantable = canGrantScope(session, scope);
              const disabled = grantable !== true;
              return (
                <label
                  key={scope}
                  className={clsx("flex items-center gap-1.5 text-sm", disabled && "text-slate-400 dark:text-slate-500")}
                  title={
                    grantable === true
                      ? undefined
                      : grantable === false
                        ? `You do not hold ${scope} on any node, so a token you mint cannot carry it.`
                        : "Your capabilities have not loaded yet."
                  }
                >
                  <input
                    type="checkbox"
                    checked={scopes.includes(scope)}
                    disabled={disabled}
                    onChange={() => {
                      toggleScope(scope);
                    }}
                  />
                  <code>{scope}</code>
                </label>
              );
            })}
          </div>
        </fieldset>

        <fieldset className="mt-3">
          <legend className="text-sm text-slate-600 dark:text-slate-300">Expiry</legend>
          <div className="mt-1 space-y-1 text-sm">
            <label className="flex items-center gap-1.5">
              <input
                type="radio"
                name="token-expiry"
                checked={expiryChoice === "default"}
                onChange={() => {
                  setExpiryChoice("default");
                }}
              />
              <span>
                Default — the daemon expires it 90 days from now
                <span className="ml-1 text-xs text-slate-600 dark:text-slate-400">
                  (the request omits <code>expiresAt</code>; the exact instant comes back in the response)
                </span>
              </span>
            </label>
            <label className="flex items-center gap-1.5">
              <input
                type="radio"
                name="token-expiry"
                checked={expiryChoice === "never"}
                onChange={() => {
                  setExpiryChoice("never");
                }}
              />
              <span>
                Never expires
                <span className="ml-1 text-xs text-slate-600 dark:text-slate-400">
                  (an explicit opt-out — the credential stays valid until revoked)
                </span>
              </span>
            </label>
            <label className="flex flex-wrap items-center gap-1.5">
              <input
                type="radio"
                name="token-expiry"
                checked={expiryChoice === "date"}
                onChange={() => {
                  setExpiryChoice("date");
                }}
              />
              <span>Expires on</span>
              <input
                type="date"
                aria-label="Expiry date"
                value={expiryDate}
                onChange={(e) => {
                  setExpiryDate(e.target.value);
                  setExpiryChoice("date");
                }}
                className="rounded border border-slate-300 px-2 py-0.5 text-sm dark:border-slate-600 dark:bg-slate-900"
              />
            </label>
          </div>
        </fieldset>

        <div className="mt-3">
          <Button
            size="sm"
            variant="primary"
            disabled={!canMint}
            onClick={handleMint}
            data-testid="mint-token"
          >
            Mint token
          </Button>
        </div>

        {mintError !== null && (
          <div className="mt-3">
            <RefusalNotice
              error={mintError}
              testId="mint-token-error"
              forbiddenHint="A token's scopes can never exceed your own derived capabilities at creation time."
            />
          </div>
        )}
      </div>

      {/* --- one-time reveal -------------------------------------------- */}
      {minted !== null && (
        <div
          data-testid="minted-token"
          className="mb-4 rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm dark:border-emerald-500/40 dark:bg-emerald-500/10"
        >
          <p className="font-medium text-emerald-900 dark:text-emerald-200">
            Copy this now — it is never shown again.
          </p>
          <code
            data-testid="minted-token-value"
            className="mt-1 block break-all rounded bg-white/70 px-2 py-1 font-mono text-xs dark:bg-slate-900/60"
          >
            {minted.token}
          </code>
          <p className="mt-2 text-xs text-emerald-900/90 dark:text-emerald-100/90" data-testid="minted-token-expiry">
            {minted.expiresAt === undefined ? (
              <>This token does not expire.</>
            ) : (
              <>
                Expires <UnixTime at={minted.expiresAt} /> — the daemon&rsquo;s answer, not a value this page computed.
              </>
            )}
          </p>
          <div className="mt-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                setMinted(null);
              }}
            >
              Dismiss
            </Button>
          </div>
        </div>
      )}

      {/* --- list -------------------------------------------------------- */}
      {tokensQuery.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading tokens…</p>}

      {tokensQuery.error !== null && (
        <RefusalNotice
          error={tokensQuery.error}
          testId="tokens-error"
          unavailableHint="This daemon does not mount the token routes — its store or auth service is not wired for them."
        />
      )}

      {!tokensQuery.isLoading && tokensQuery.error === null && tokens.length === 0 && (
        <p className="text-sm text-slate-600 dark:text-slate-400" data-testid="tokens-empty">
          You have not minted any tokens. This list shows only your own — a token another user minted is neither
          listed nor revocable here.
        </p>
      )}

      {tokens.length > 0 && (
        <Table density="compact">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Stored scope</TableHead>
              <TableHead>Effective scope</TableHead>
              <TableHead>Expiry</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead>State</TableHead>
              <TableHead>{""}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.map((token) => (
              <TableRow key={token.id} data-testid={`token-row-${token.id}`}>
                <TableCell>
                  <span className="font-medium">{token.name}</span>
                  <span className="block font-mono text-[10px] text-slate-600 dark:text-slate-400">{token.id}</span>
                </TableCell>
                <TableCell>
                  <ScopeChips names={token.scopes} empty="no scopes" />
                </TableCell>
                <TableCell>
                  <EffectiveScopeCell token={token} readOnly={readOnly} />
                </TableCell>
                <TableCell>
                  <ExpiryCell token={token} />
                </TableCell>
                <TableCell>
                  <UnixTime at={token.lastUsedAt} absent="never used" />
                </TableCell>
                <TableCell>
                  <LifecycleBadge token={token} />
                </TableCell>
                <TableCell>
                  {token.revokedAt === undefined && (
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={revoke.isPending}
                      onClick={() => {
                        handleRevoke(token);
                      }}
                      data-testid={`revoke-${token.id}`}
                    >
                      Revoke
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </PlatformSection>
  );
}
