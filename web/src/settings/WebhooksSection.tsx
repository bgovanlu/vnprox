// SPDX-License-Identifier: Apache-2.0

// Webhook registrations (T-1104 routes, T-2905 destination policy).
//
// ── READ THIS BEFORE CHANGING THE GATING ──────────────────────────────────
// `GET /webhooks` sits behind `auth.RequireCap("automation")`; `POST
// /webhooks` and `DELETE /webhooks/{id}` sit behind `auth.RequireCap(
// "automationWrite")` (split from a single "automation" flag by
// T-3003-followup-01, 2026-08-19, so `[server] read_only` can clear webhook
// registration/deletion without also taking away this read). Neither
// capability is ever produced by `internal/auth.DeriveCapabilities` — both
// are token-scope-only. docs/api.md states the consequence in as many words:
// "a browser session alone can never reach them, only a token minted via
// POST /tokens with `automation`/`automationWrite` in its scopes can."
//
// So on a normal deployment this section's list query returns 403, and there
// is no client-side arrangement that changes that — the SPA authenticates
// with a session cookie. That is not a bug this card may route around, and it
// is NOT hidden here: the 403 renders as an explicit, named explanation with
// the route family, the capability, and how a caller actually reaches it.
// (Filed as the card-vs-contract disagreement in the T-3003 report.)
//
// The create form is therefore gated on the daemon's own answer rather than
// on a client-side capability guess: it appears only once `GET /webhooks` has
// actually returned 200, which is proof the caller holds `automation` — NOT
// proof of `automationWrite`, so a token scoped read-only can still see this
// form appear and then get a 403 on submit; the submit-time RefusalNotice
// below names `automationWrite` specifically for that case. When the form
// does appear, submitting is the point of this section:
// T-2905 refuses a destination at registration when it is plain http or an
// IP-literal that is loopback/RFC1918/link-local, and refuses it AGAIN at
// dial time against the resolved address. This component performs NO address
// classification of its own — a client-side copy of that policy would drift
// from the enforcement point and would tell an operator the reason a
// *browser* invented rather than the reason the *daemon* gave. The daemon's
// message names both the policy and the config knob; it is rendered verbatim.
import { useState } from "react";
import { Button } from "../components/Button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { ApiError } from "../api/client";
import type { Webhook } from "../api/types";
import { useCreateWebhookMutation, useDeleteWebhookMutation, useWebhooksQuery } from "./platformQueries";
import { PlatformSection, RefusalNotice, ScopeChips, UnixTime } from "./platformCommon";

/** The event-name vocabulary the WS "events" topic carries (docs/api.md's
 * Webhook shape). An empty selection means "every event", which is the same
 * optional-filter convention alert rules use. */
const WEBHOOK_EVENTS: readonly string[] = ["changeset.status", "drift.changed", "findings.changed", "audit.appended"];

function DeliveryHealth({ webhook }: { webhook: Webhook }) {
  // Never attempted is its own state. `consecutiveFailures: 0` with no
  // attempt is not "healthy" — it is "we have not tried", and three
  // consecutive failed sequences are what raise the `webhook_unhealthy`
  // finding, so 0 before any attempt says nothing at all.
  if (webhook.lastAttemptAt === undefined) {
    return (
      <span data-testid={`delivery-${webhook.id}`} data-delivery-state="unattempted" className="text-xs italic text-slate-600 dark:text-slate-400">
        Never attempted — no event has matched this registration yet
      </span>
    );
  }
  if (webhook.consecutiveFailures === 0) {
    return (
      <span data-testid={`delivery-${webhook.id}`} data-delivery-state="ok" className="text-xs text-emerald-700 dark:text-emerald-300">
        Delivering · last success <UnixTime at={webhook.lastSuccessAt} absent="not recorded" />
      </span>
    );
  }
  return (
    <span data-testid={`delivery-${webhook.id}`} data-delivery-state="failing" className="text-xs text-red-700 dark:text-red-300">
      {webhook.consecutiveFailures} consecutive failed{" "}
      {webhook.consecutiveFailures === 1 ? "sequence" : "sequences"}
      {webhook.lastError !== undefined && webhook.lastError !== "" && (
        // The daemon's own last delivery error, unedited — for a target the
        // dial-time guard refuses this is the string naming the policy.
        <span className="mt-0.5 block font-mono text-[11px]" data-testid={`delivery-error-${webhook.id}`}>
          {webhook.lastError}
        </span>
      )}
    </span>
  );
}

export function WebhooksSection() {
  const webhooksQuery = useWebhooksQuery();
  const create = useCreateWebhookMutation();
  const remove = useDeleteWebhookMutation();
  const { toast } = useToast();

  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [createError, setCreateError] = useState<unknown>(null);

  function toggleEvent(name: string): void {
    setEvents((prev) => (prev.includes(name) ? prev.filter((e) => e !== name) : [...prev, name]));
  }

  function handleCreate(): void {
    setCreateError(null);
    create.mutate(
      { url: url.trim(), secret, ...(events.length > 0 ? { events } : {}) },
      {
        onSuccess: (wh) => {
          setUrl("");
          setSecret("");
          setEvents([]);
          toast({ title: "Webhook registered", description: wh.url, variant: "success" });
        },
        onError: (err: unknown) => {
          setCreateError(err);
        },
      },
    );
  }

  function handleDelete(webhook: Webhook): void {
    remove.mutate(webhook.id, {
      onSuccess: () => {
        toast({ title: "Webhook removed", description: webhook.url });
      },
      onError: (err: unknown) => {
        toast({
          title: "Could not remove webhook",
          description: err instanceof ApiError ? err.message : "unexpected error",
          variant: "error",
        });
      },
    });
  }

  const listError = webhooksQuery.error;
  const forbidden = listError instanceof ApiError && listError.status === 403;
  // `isSuccess` — not "no error" — is the only proof this caller holds
  // `automation`, and it comes from the daemon rather than from a rule this
  // file re-derives.
  const reachable = webhooksQuery.isSuccess;
  const webhooks = webhooksQuery.data ?? [];

  return (
    <PlatformSection
      title="Webhooks"
      helpTopic="platform-webhooks"
      description={
        <>
          HTTP delivery targets for the same event envelope the WebSocket <code>events</code> topic carries. Every
          delivery is HMAC-signed with the secret you register.
        </>
      }
    >
      {webhooksQuery.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading webhooks…</p>}

      {listError !== null && (
        <RefusalNotice
          error={listError}
          testId="webhooks-error"
          forbiddenHint={
            <>
              <p>
                <code>GET /webhooks</code> is gated on the <code>automation</code> capability;{" "}
                <code>POST</code>/<code>DELETE</code> additionally require <code>automationWrite</code>.{" "}
                <code>automation</code> and <code>automationWrite</code> are never derived from a Proxmox privilege
                — logging in with a browser session can never grant either one. Only a request authenticated by a
                bearer token minted with those scopes reaches these routes.
              </p>
              <p className="mt-1">
                Mint such a token in the Automation tokens section above, then use it from{" "}
                <code>vnproxctl</code> or your own client. This panel shows the refusal rather than hiding the
                section, because &ldquo;you may not look&rdquo; and &ldquo;there is nothing here&rdquo; are different
                facts.
              </p>
            </>
          }
          unavailableHint="This daemon does not mount the webhook routes — no webhook store or secret cipher is wired."
        />
      )}

      {reachable && (
        <>
          <div className="mb-4 rounded-md border border-slate-200 p-3 dark:border-slate-700">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-400">
              Register a target
            </h3>
            <p className="mt-1 text-xs text-slate-600 dark:text-slate-400">
              The daemon decides whether a destination is permitted — at registration, and again against the resolved
              address at every delivery. Whatever it refuses, it says why.
            </p>

            <label className="mt-2 block text-sm">
              <span className="text-slate-600 dark:text-slate-300">Destination URL</span>
              <input
                type="url"
                value={url}
                onChange={(e) => {
                  setUrl(e.target.value);
                }}
                placeholder="https://hooks.example.com/vnprox"
                className="mt-1 block w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-900"
              />
            </label>

            <label className="mt-2 block text-sm">
              <span className="text-slate-600 dark:text-slate-300">Signing secret</span>
              <input
                type="password"
                value={secret}
                onChange={(e) => {
                  setSecret(e.target.value);
                }}
                className="mt-1 block w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-900"
              />
            </label>

            <fieldset className="mt-3">
              <legend className="text-sm text-slate-600 dark:text-slate-300">
                Events <span className="text-xs text-slate-600 dark:text-slate-400">(none selected = every event)</span>
              </legend>
              <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
                {WEBHOOK_EVENTS.map((name) => (
                  <label key={name} className="flex items-center gap-1.5 text-sm">
                    <input
                      type="checkbox"
                      checked={events.includes(name)}
                      onChange={() => {
                        toggleEvent(name);
                      }}
                    />
                    <code>{name}</code>
                  </label>
                ))}
              </div>
            </fieldset>

            <div className="mt-3">
              <Button
                size="sm"
                variant="primary"
                disabled={url.trim() === "" || secret === "" || create.isPending}
                onClick={handleCreate}
                data-testid="register-webhook"
              >
                Register
              </Button>
            </div>

            {createError !== null && (
              <div className="mt-3">
                <RefusalNotice
                  error={createError}
                  testId="register-webhook-error"
                  forbiddenHint="Registering a webhook needs the automationWrite capability."
                />
              </div>
            )}
          </div>

          {webhooks.length === 0 ? (
            <p className="text-sm text-slate-600 dark:text-slate-400" data-testid="webhooks-empty">
              No webhooks are registered.
            </p>
          ) : (
            <Table density="compact">
              <TableHeader>
                <TableRow>
                  <TableHead>Destination</TableHead>
                  <TableHead>Events</TableHead>
                  <TableHead>Delivery</TableHead>
                  <TableHead>Registered</TableHead>
                  <TableHead>{""}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {webhooks.map((webhook) => (
                  <TableRow key={webhook.id} data-testid={`webhook-row-${webhook.id}`}>
                    <TableCell>
                      <span className="break-all font-mono text-xs">{webhook.url}</span>
                    </TableCell>
                    <TableCell>
                      <ScopeChips names={webhook.events ?? []} empty="every event" />
                    </TableCell>
                    <TableCell>
                      <DeliveryHealth webhook={webhook} />
                    </TableCell>
                    <TableCell>
                      <UnixTime at={webhook.createdAt} />
                      <span className="block text-[10px] text-slate-600 dark:text-slate-400">{webhook.createdBy}</span>
                    </TableCell>
                    <TableCell>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={remove.isPending}
                        onClick={() => {
                          handleDelete(webhook);
                        }}
                        data-testid={`delete-webhook-${webhook.id}`}
                      >
                        Remove
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </>
      )}

      {forbidden && (
        <p className="mt-3 text-xs text-slate-600 dark:text-slate-400" data-testid="webhooks-no-form">
          The registration form is not shown because this session cannot reach the route it would post to. A control
          that could only ever produce a 403 is worse than no control.
        </p>
      )}
    </PlatformSection>
  );
}
