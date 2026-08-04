# planning/validation — hardware-validation harness and evidence protocol

This is the human-facing runbook for Phase 18's hardware-validation arc (`docs/roadmap-proven.md`
decisions **D5** and **D7**, implemented by task card `T-1801` in `planning/tasks/phase-18.md`).
You do not need to have read that card, or any chat history, to use this document — everything
you need to run a section and hand results back is below.

**The short version**: an agent cannot touch Proxmox hardware. You can. This directory holds
scripts an agent wrote and tested against a mock PVE server; your job is to run them against a
real node (`pvecube`) and paste back what they print. An agent then compares that output against
a declared expected outcome and tells you what, if anything, needs a second look.

## Layout

```
planning/validation/
  harness/            one script per checklist section — the thing you run
    lib/common.sh      shared library the section scripts source (not run directly)
    pve-api.sh          PVE API auth/permissions/TOTP (read-only)
    host.sh              systemctl, netlink/bond/LLDP, pveproxy cert (read-only)
    change-engine.sh     stage->PUT->reload->confirm round trip (MUTATES=1)
    firewall.sh          firewall rules/groups/aliases/ipset shape (read-only)
    sdn.sh                SDN zone/vnet/subnet status (read-only)
    ipam.sh               IPAM plugin instances + allocations (read-only)
    wireguard.sh          WireGuard sandbox/tooling facts (read-only)
    capture.sh            conntrack/eBPF/corosync/ping host facts (read-only)
  expected/            one table per section — what each item *should* show, and what a
                        divergence would mean. An agent reads these; you don't need to.
  evidence/            committed run output lives here once you've returned it — empty until
                        the first real hardware run.
```

## Running a section

1. Pick a section script, e.g. `pve-api.sh`.
2. Read its header comment (`head -40 planning/validation/harness/pve-api.sh`). It says whether
   the script is read-only or mutating, and lists any environment variables you need to set.
3. **If the script's header says `MUTATES=0` (read-only)** — the common case — just run it over
   SSH and save the output:

   ```sh
   ssh pvecube 'bash -s' < planning/validation/harness/pve-api.sh > pve-api-evidence.json
   ```

   Nothing on `pvecube` is changed by this. It's safe to run repeatedly.

4. **If the script's header says `MUTATES=1`** (today, only `change-engine.sh`) — read the whole
   header comment before running it. It changes state on the target node (deliberately, safely,
   reversibly by design — but it is still a real write, and it still triggers a real `ifreload`).
   It refuses to run at all unless you pass `--i-understand-this-mutates` explicitly:

   ```sh
   PVE_TARGET_IFACE=eno2 ssh pvecube 'bash -s' \
     < planning/validation/harness/change-engine.sh --i-understand-this-mutates \
     > change-engine-evidence.json
   ```

   `change-engine.sh` specifically: **`PVE_TARGET_IFACE` has no default on purpose.** Pick a
   NIC/bridge that is *not* carrying your management connection to `pvecube` — the script re-PUTs
   that interface's current MTU back to itself and reloads it, which briefly reprograms the
   interface even though the value doesn't change. If you're not sure which interface is your
   management path, don't run this script until you are.

5. Look at what came back. Every script prints exactly one JSON object to stdout and nothing
   else (diagnostics go to stderr) — if you see something that isn't valid JSON, something went
   wrong before the script even got to `harness_emit`; check stderr.

## What to paste back

Paste the whole JSON blob (the file you saved in step 3/4) back into the conversation, with:

- which node you ran it against (`pvecube`, presumably — that's the only hardware this arc uses,
  per decision D2),
- the PVE version, if you know it off the top of your head (the blob tries to capture this itself
  via `pveversion`, but a sanity check helps),
- anything you noticed that seemed off, even if you're not sure it matters.

An agent will:

1. Validate the blob's schema (`internal/validation.ParseBlob`).
2. Compare it against `planning/validation/expected/<section>.md` (`internal/validation.Triage`).
3. Tell you which items matched, which diverged, and what a divergence means (the expected file's
   own words — the agent doesn't invent an interpretation).
4. Commit the blob under `planning/validation/evidence/<section>-<pve-version>-<date>.json` and
   tick the corresponding item(s) in `planning/reports/needs-hardware-validation.md`, or open a
   bug card for a genuine divergence — never both silently averaged into a doc edit. **No item
   in that checklist is ever ticked without a blob you returned.**

You don't need to interpret the JSON yourself. If you want to sanity-check it before sending,
`python3 -m json.tool < the-file.json` will at least confirm it's well-formed.

## If a mutating run goes wrong (recovery)

`change-engine.sh` is designed so this shouldn't happen (see step 4 above), but if you lose
connectivity to `pvecube` mid-run:

1. Don't panic — the script triggers exactly one `ifreload`, on exactly the interface you named,
   with its **current** value re-applied. It is not staging a different config; nothing about
   the interface's intended state changed.
2. Reach the node out-of-band: IPMI/iKVM console, or physically if that's what you have for
   `pvecube`.
3. From the console: `cat /etc/network/interfaces` to see the current on-disk config (it should
   be unchanged — the write re-applied the same value), and `ifreload -a` if the interface looks
   like it's in a half-applied state.
4. If the management interface itself somehow got touched despite the header warning: PVE's own
   `/etc/network/interfaces` is still the source of truth and hasn't been corrupted — worst case
   is a transient link flap, not a persistent misconfiguration, because the script only ever
   re-applies the interface's own pre-existing value.
5. Report what happened in the same place you'd paste the evidence blob — a failed mutating run
   is itself useful evidence (see "known limitations" below).

## The evidence-blob schema (what a script prints)

```json
{
  "schema_version": "1.0",
  "harness_version": "1.0.0",
  "section": "pve-api",
  "generated_at": "2026-08-04T12:00:00Z",
  "mutates": false,
  "node": { "hostname": "pve1.example", "identity": "pve1.example" },
  "pve_version": { "source": "pveversion", "raw": "pve-manager/9.2.4/abc123" },
  "items": [
    {
      "id": "pve-api-01",
      "checklist_ref": "PVE API behavior > API-token auth",
      "command": "curl -sS ... /access/permissions",
      "raw": "http_status=200",
      "exit_code": 0,
      "verdict_inputs": { "exit_code": 0 }
    }
  ]
}
```

Notes:

- **`raw` is verbatim, redacted command output** — see "Redaction" below. It is not a summary and
  the script never decides whether it looks "good"; that's `expected/<section>.md` and the triage
  step's job (decision D7). Keep it that way if you ever add an item: capture, don't judge.
- **`pve_version.source`/`raw` can legitimately be `"unknown"/"unknown"`** — that's what you'll
  see if `pveversion` isn't on `PATH` (e.g. a script run against `internal/pvemock` in dev/CI
  rather than a real node). It's an honest answer, not a bug.
- **`schema_version`** is bumped whenever the shape above changes. `internal/validation`'s
  `SupportedSchemaVersion` constant and this document are updated together — if you're looking at
  a blob whose `schema_version` doesn't match what `internal/validation` expects, the triage step
  will say so explicitly rather than silently misreading it.
- **`harness_version`** (currently `1.0.0`) identifies which revision of the scripts produced a
  blob, independent of the PVE version — bump it in `planning/validation/harness/lib/common.sh`
  when you change what a section script actually does (not for comment-only edits), so a blob
  committed months from now can be traced back to the exact script that produced it.

## The expected-outcome table format (what an agent reads)

`planning/validation/expected/<section>.md` files hold one or more markdown tables:

```
| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| pve-api-01 | raw | contains | http_status=200 | ... what a divergence would mean ... |
```

- **`id`** matches an item's `id` in the evidence blob.
- **`pointer`** is where to look in that item: `raw`, `exit_code`, `command`, `checklist_ref`, or
  `verdict_inputs.<key>`.
- **`op`** is `equals`, `contains`, `not_contains`, or `regex`.
- **`expected`** is compared against the pointed-to value using `op`.
- **`meaning`** is prose explaining what it implies if this row diverges — written for a human,
  quoted verbatim by the triage step rather than paraphrased, so the interpretation you get back
  is traceable to something an agent wrote *before* seeing your evidence (decision D7's whole
  point: the expectation predates the run).

You do not need to write or edit these files to use the harness — they're an agent's
responsibility. They're documented here so you can read one if you're curious what a script is
actually being checked against.

## Redaction

Every `raw` field is scrubbed **before** it's ever assembled into the blob (`common.sh`'s
`redact()` function, called from inside `harness_item` — not a separate pass you have to
remember to run). Treat every blob as something that will be pasted into a chat transcript,
because it will be: it's scrubbed for PVE tickets (`PVE:...`), API-token secrets
(`user@realm!id=secret` — the `secret` half), WireGuard/PSK-shaped 32-byte base64 keys, and
`Authorization:`/`Cookie:` header values.

**This is best-effort, not a guarantee.** If a script's output ever includes something that looks
like a credential and *isn't* one of the shapes above, don't paste it — flag it instead, and
we'll add a pattern for it. Over-redaction is the accepted failure mode here (a false positive
just means slightly less raw evidence); under-redaction is not.

## Adding a new item to a section, or a new section

- New item in an existing section script: add a `harness_item "id" "checklist_ref" 'command'`
  call, and a matching row in that section's `expected/<section>.md`. Bump `HARNESS_VERSION` in
  `lib/common.sh` if the change is more than a comment/wording tweak.
- New section: add `harness/<name>.sh` (copy an existing read-only section's structure — set
  `SECTION`/`MUTATES`, source `lib/common.sh`, call `harness_item` per checklist observation, call
  `harness_emit` last) and its `expected/<name>.md` companion. `internal/validation`'s tests
  (`go test ./internal/validation/...`) will tell you if either is missing or malformed.
- A script that needs to mutate state: set `MUTATES=1` and call
  `harness_require_mutation_flag "$@"` before doing anything mutating. A test
  (`TestNoHarnessScriptMutatesWithoutBanner` in `internal/validation`) greps every
  `harness/*.sh` for the verbs `set`, `create`, `delete`, `ifreload`, `ifup`, `ifdown` and fails
  the build if one appears without a `MUTATES=1` banner in the same file — this catches an
  accidental mutation added to a script that's supposed to be read-only, including one hidden
  inside a `pvesh set` call.

## Known limitations (read before assuming a clean triage means "validated")

- **A script's own success only proves the harness ran, not that the underlying checklist item is
  actually fine.** `exit_code == 0` rows catch "the command couldn't even complete"; the
  `raw`-content rows are a first-pass approximation of the real question, not a replacement for a
  human/agent actually reading `raw`. Several `expected/*.md` files say so explicitly per-row —
  that's not a template you should feel obligated to fill in more strongly than the evidence
  supports.
- **`pvesh`'s exact JSON output shape (vs. the raw HTTP API envelope) is itself unconfirmed** as
  of this card. Every section script prefers `pvesh` on a real node (nothing else needed
  besides a stock install) and falls back to HTTP+curl only when `pvesh` is absent (true of
  `internal/pvemock`, which is how these scripts are testable without hardware at all — see
  `internal/validation`'s `TestHarness_*` tests). If `pvesh --output-format json` turns out to
  wrap or unwrap the JSON differently than the HTTP fallback does, some `contains`-op expected
  rows may need adjusting after the first real run — that's expected, not a sign anything here is
  broken (see decision D4's accepted "cards get revised after they run" cost).
- **"Validated" and "mock-validated" are different words in this arc** (docs/roadmap-proven.md).
  A blob captured by running a script against `internal/pvemock` (as `internal/validation`'s own
  tests do, to prove the harness works without a cluster) is *not* hardware evidence and must
  never be committed under `planning/validation/evidence/` as if it were — only blobs a human
  actually returned from `pvecube` belong there.
