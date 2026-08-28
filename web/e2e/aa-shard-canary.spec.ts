// SPDX-License-Identifier: Apache-2.0

// T-2505 AC4, first half: deliberately corrupt this shard's global state.
//
// "Sharding isolates" is a claim about what one spec can do to another, and
// the only way to test it is to have a spec actually do it. This one creates a
// changeset in its shard's store — real, persisted, app-owned state, and the
// exact class of state that leaked across spec files during the T-2108 triage
// — then records the store row's id where zz-shard-witness.spec.ts, running in
// a different shard against a different daemon, can look for it.
//
// A changeset id and not a title: the id is a store primary key, so
// `GET /changesets/{id}` returning 404 on another shard's daemon is an
// unambiguous statement about the store, with no room for a near-miss match.
//
// THE FILE NAME IS LOAD-BEARING. Playwright runs a shard's spec files in path
// order and offers no other ordering control, so `aa-` puts the writer first
// in its shard and `zz-` puts the witness last in its own. Without that, a
// witness that ran before the writer would pass by having nothing to see —
// which is also why the witness refuses to assert until the marker exists.
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { expect, request, test } from "@playwright/test";

import { activeShard, CANARY_DIR, stackURL, WHOLE_SUITE } from "./shards";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** The shape zz-shard-witness.spec.ts reads. Passed through the file system
 * rather than a module import because the two specs run in different
 * processes — that separation is the thing under test. */
export interface CanaryMarker {
  shard: string;
  /** The daemon the canary was created on. The witness compares it with its
   * own: the same URL means the two specs legitimately share a store (a
   * whole-suite run, where the witness becomes a positive control); different
   * URLs mean the row must be invisible. */
  daemonURL: string;
  changesetID: string;
  title: string;
  writtenAt: string;
}

/** pve1's management bridge, present in three-node-vlan.yaml — the same
 * interface history.spec.ts stages a comment update against. The op is never
 * applied; it exists because a changeset is a container for ops and an empty
 * one would be a weaker fixture than a real draft. */
const TARGET_REF = "bridge:pve1:vmbr0";

test("shard isolation: write a canary changeset into this shard's store", async () => {
  const shard = activeShard()?.name ?? WHOLE_SUITE;
  const daemonURL = stackURL("default");
  const title = `T-2505 shard-isolation canary (${shard})`;

  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  let changesetID: string;
  try {
    const login = await ctx.post(daemonURL + "/api/v1/auth/login", {
      data: { username: "root@pam", password: "vnprox-mock" },
    });
    expect(login.ok(), `login on ${daemonURL} returned ${String(login.status())}`).toBe(true);

    const state = await ctx.storageState();
    const csrf = state.cookies.find((c) => c.name === "vnprox_csrf");
    expect(csrf, "no vnprox_csrf cookie after login").toBeDefined();
    const headers = { "X-VNPROX-CSRF": csrf?.value ?? "" };

    const created = await ctx.post(daemonURL + "/api/v1/changesets", {
      headers,
      data: {
        title,
        ops: [{ op: "iface.update", target: TARGET_REF, params: { comments: title } }],
      },
    });
    expect(created.ok(), `creating the canary changeset returned ${String(created.status())}`).toBe(true);
    const body = (await created.json()) as { id?: string };
    changesetID = body.id ?? "";
    expect(changesetID, "the created changeset carries no id").not.toBe("");

    // Read it back through the daemon that created it. A canary its own store
    // does not serve would make the witness's later "cannot see it" vacuous.
    const readBack = await ctx.get(daemonURL + `/api/v1/changesets/${changesetID}`);
    expect(readBack.ok(), "the writer's own daemon does not serve the changeset it just created").toBe(true);
  } finally {
    await ctx.dispose();
  }

  const marker: CanaryMarker = {
    shard,
    daemonURL,
    changesetID,
    title,
    writtenAt: new Date().toISOString(),
  };
  const path = join(REPO_ROOT, CANARY_DIR, `${shard}.json`);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(marker, null, 2));
});
