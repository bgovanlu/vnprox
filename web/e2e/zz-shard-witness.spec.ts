// SPDX-License-Identifier: Apache-2.0

// T-2505 AC4, second half: the shard that did NOT corrupt anything proves it
// cannot see the corruption.
//
// aa-shard-canary.spec.ts (shard-1) writes a real changeset row into its
// shard's store and records the id. This spec runs in shard-2, against a
// different vnproxd with a different SQLite file, and asks that daemon for the
// same id. A 404 is the whole proof: it is a statement about the store, made
// against a row that demonstrably exists somewhere else.
//
// IT REFUSES TO BE VACUOUS in three separate ways, because a green isolation
// test that never had anything to see is worse than no test:
//
//  1. It waits for the writer's marker and FAILS if it never appears, rather
//     than passing because nothing was written.
//  2. When the writer's daemon URL equals its own — a whole-suite run, where
//     both specs share one stack — it inverts the assertion and requires the
//     changeset to BE visible. That is the positive control: the same code
//     path proves the canary is detectable when the store really is shared.
//  3. Running one shard on its own (a CI matrix leg) cannot see the other
//     shard at all, so it skips with that reason stated. `make e2e` always
//     runs the whole set and always executes it.
//
// HOW TO MAKE IT GO RED: put both specs in the same shard in
// web/e2e/shards.ts. The witness then shares the writer's daemon, takes the
// positive-control branch, and would fail the moment isolation actually
// worked — which is exactly the mutation this file's assertion is calibrated
// against. T-2505's report records the run.
import { readFileSync } from "node:fs";
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { expect, request, test } from "@playwright/test";

import type { CanaryMarker } from "./aa-shard-canary.spec";
import { activeShard, CANARY_DIR, CANARY_WRITER_SPEC, SHARDS, stackURL, WHOLE_SUITE } from "./shards";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** How long to wait for the writer's marker.
 *
 * The writer is the first spec in its shard and this is the last in its own,
 * so in a full run the marker is minutes old by the time this runs. The wait
 * covers the case where the writer's shard boots the slower scale-lab stack
 * and starts late — not an ordering the manifest relies on. */
const MARKER_WAIT_MS = 90_000;

/** The shard that owns the writer, read from the manifest so a rebalance
 * cannot leave this spec naming the wrong one. */
const writerShard = SHARDS.find((s) => s.specs.includes(CANARY_WRITER_SPEC))?.name ?? WHOLE_SUITE;

function markerPath(shard: string): string {
  return join(REPO_ROOT, CANARY_DIR, `${shard}.json`);
}

async function waitForMarker(shard: string): Promise<CanaryMarker> {
  const path = markerPath(shard);
  const deadline = Date.now() + MARKER_WAIT_MS;
  for (;;) {
    if (existsSync(path)) {
      const raw = readFileSync(path, "utf8");
      // A partially-written file is possible for a few milliseconds; retry
      // rather than failing on a JSON parse error that means nothing.
      try {
        return JSON.parse(raw) as CanaryMarker;
      } catch {
        // fall through to the retry below
      }
    }
    if (Date.now() > deadline) {
      throw new Error(
        `${CANARY_WRITER_SPEC} (shard ${shard}) never wrote ${path} within ${String(MARKER_WAIT_MS / 1000)}s. ` +
          `This spec has nothing to look for, so it is failing rather than passing vacuously.`,
      );
    }
    await new Promise((r) => setTimeout(r, 250));
  }
}

test("shard isolation: another shard's canary changeset is invisible to this one", async () => {
  const shard = activeShard()?.name ?? WHOLE_SUITE;
  const runningAllShards = process.env.VNPROX_E2E_ALL_SHARDS === "1" || shard === WHOLE_SUITE;
  test.skip(
    !runningAllShards,
    `only shard ${shard} is running, so ${writerShard}'s canary is never written; run scripts/e2e-shards.sh (or the whole suite) to exercise this`,
  );

  const marker = await waitForMarker(writerShard);
  const mine = stackURL("default");

  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const login = await ctx.post(mine + "/api/v1/auth/login", {
      data: { username: "root@pam", password: "vnprox-mock" },
    });
    expect(login.ok(), `login on ${mine} returned ${String(login.status())}`).toBe(true);

    const resp = await ctx.get(mine + `/api/v1/changesets/${marker.changesetID}`);

    if (marker.daemonURL === mine) {
      // Positive control: one stack, so the row must be there. This is the
      // branch a whole-suite run takes, and the branch that would fire if a
      // rebalance ever put both specs in one shard.
      expect(
        resp.status(),
        `${marker.shard} and ${shard} share ${mine}, so its changeset ${marker.changesetID} must be visible`,
      ).toBe(200);
      return;
    }

    expect(
      resp.status(),
      `changeset ${marker.changesetID} was created by ${marker.shard} on ${marker.daemonURL} and is visible ` +
        `on ${shard}'s own daemon ${mine}: the two shards are sharing a store, so a spec that corrupts state ` +
        `does not fail only its own shard`,
    ).toBe(404);
  } finally {
    await ctx.dispose();
  }
});
