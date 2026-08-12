// Advisory-lock and presence reads (docs/api.md's "Advisory locks and
// presence", T-2805).
//
// There is deliberately no mutating call in this file, and there is no route
// to write one against: a lock is taken by staging a draft and released by
// discarding it, by disconnecting, or by expiring. Nothing here can refuse
// anything — a lock warns, it never gates.
import { apiFetch } from "./client";
import type { LocksResponse, PresenceResponse } from "./types";

/** GET /locks — every advisory lock currently held, expired ones excluded.
 * `holder` is absent when this session lacks the `audit` capability. */
export function listLocks(): Promise<LocksResponse> {
  return apiFetch<LocksResponse>("/locks");
}

/** GET /presence?scope= — who is viewing one changeset or entity. A named
 * scope always answers: "nobody else is looking at this" is the answer, not
 * an empty result. `viewers` is absent when this session lacks the `audit`
 * capability; `count` is always present. */
export function getPresence(scope?: string): Promise<PresenceResponse> {
  const qs = scope ? `?scope=${encodeURIComponent(scope)}` : "";
  return apiFetch<PresenceResponse>(`/presence${qs}`);
}

/** The WS subscription topic for one presence scope. Subscribing to it is
 * both the declaration "I am looking at this" and the delivery channel for
 * its `presence.changed` events — presence rides the existing stream. */
export function presenceTopic(scope: string): string {
  return `presence:${scope}`;
}

/** `changeset:<id>` — one changeset's presence scope. */
export function changesetScope(id: string): string {
  return `changeset:${id}`;
}

/** `entity:<ref>` — one entity's presence scope. */
export function entityScope(ref: string): string {
  return `entity:${ref}`;
}
