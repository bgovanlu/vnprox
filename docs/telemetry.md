# Telemetry — what it is, what it sends, and what it never sends

vnprox ships one opt-in telemetry feature: **compatibility reporting** (T-2503). This page is the
version of `docs/security.md`'s "Compatibility telemetry" section written for a prospective
adopter deciding whether to turn it on, rather than for someone already auditing the security
design. Everything here is either checked by a test on every build, or produced live below by the
real `vnproxctl`/`vnproxtelemetryd` binaries against a sample report — nothing on this page is a
description of behaviour nobody ran.

## TL;DR

- **Off by default, and off is structural**, not a checkbox that ships pre-ticked. `vnprox.toml`
  ships the `[telemetry]` section commented out, `enabled` defaults to `false`, and there is **no
  default collector endpoint at all** — opting in means naming an `https://` endpoint yourself.
  With either half missing, nothing that reads your data, builds a request, or opens a socket ever
  runs.
- **You can see the exact bytes before you decide.** `vnproxctl telemetry preview` prints precisely
  what `vnproxctl telemetry send` would POST — the same buffer, not an equivalent rendering — and
  needs no opt-in to run. A real transcript is below.
- **The payload is a closed, enumerated list**, not a redacted copy of something bigger. A field
  that is not in the table below does not exist in the code, and a field added to the code without
  a matching row here fails the build (see "How this page cannot drift" below).
- **No public collector is running yet.** `registry.vnprox.com` and related hostnames do not
  resolve — DNS for a hosted instance is a deferred owner decision. Nobody is receiving this
  telemetry today, from anyone. The mechanism is real and tested; the "aggregate stats" section
  below is a local worked example, clearly labelled, not live numbers.

## What is collected — field by field

This is the complete list. `Reduce` in `internal/telemetry/payload.go` is a projection that names
every field it produces and reads nothing else — not a copy of a bigger report with some fields
omitted.

<!-- telemetry-fields:begin -->

| Field | Type | What it is | Why it carries no identity |
|---|---|---|---|
| `payloadVersion` | number | The reduction's schema version, currently `1`. | A constant compiled into the build. |
| `installId` | string | A ULID generated locally on first send; the only correlator. | Random (crypto/rand), never derived from the machine, resettable any time with `vnproxctl telemetry reset-id`. |
| `vnproxVersion` | string | The `vnproxctl` build that ran the suite, e.g. `3.0.3`. | The same string for everyone running that release. |
| `pveVersion` | string | What PVE reported, e.g. `pve-manager/9.2.4`. | A package version; identical across every cluster on that release. |
| `kernel` | string | `uname -r` from the node the suite ran on, e.g. `6.8.12-4-pve`. | A kernel package version — scanned before send anyway, because a locally built kernel can carry a hostname in its release string. |
| `nicPciIds` | string list | PCI `vendor:device` ids of the NICs seen, e.g. `0x8086:0x1521`. | Hardware model ids. The interface names and modalias strings that sit beside them in the underlying report are dropped — an interface name like `tap101i0` names a guest. |
| `nodeCount` | number | How many cluster members the run saw. | A count. Node names are never in the payload. |
| `suite` | string | Which suite ran: `hardware`, `multinode`, `destructive`, or `selection`. | One of four fixed words. |
| `checks[].id` | string | The check's registry id, e.g. `drift.config_vs_live`. | Compiled into the binary; the same ids on every install. |
| `checks[].status` | string | `pass`, `fail` or `skip`. | One of three fixed words. |
| `checks[].durationMs` | number | How long that check took. | A duration — useful for spotting a check that has quietly become a timeout on hardware we don't have. |

<!-- telemetry-fields:end -->

The collector adds exactly one field of its own on receipt, `receivedAt` — a server-side timestamp,
never a client-supplied one (see "Why no timestamp of your own" below).

### How this page cannot drift

Two independent, machine-checked gates hold this table to the code, in both directions:

1. **`internal/telemetry.TestDocSectionMatchesPayload`** compares `docs/security.md`'s copy of this
   table against `internal/telemetry.Payload` by reflection. A field added to the struct without a
   row fails the build; a row with no field behind it fails too.
2. **`internal/telemetry.TestPublicTelemetryPageMatchesPayload`** runs the exact same comparison
   (`ParseDocTable`/`CompareDoc`, the same two functions, not a second implementation) against
   *this* page, so the public-facing table and the internal one cannot quietly diverge from each
   other either — they are both checked against the one struct, which is the only thing anyone has
   to keep up to date.

Both run on every `make check`. There is no fixture involved — walking `Payload` with `reflect` and
parsing this file's own Markdown table is the whole test.

## What is deliberately NOT collected

Every one of these is present in the `vnproxctl verify` report the payload is built from, and is
dropped in the reduction rather than merely unused — meaning it is never read by the code path that
builds a telemetry payload at all:

- **Your machine's or PVE endpoint's source IP.** `internal/telemetrycollector`'s HTTP handler never
  reads `http.Request.RemoteAddr` — not in the request handlers, not in the panic recoverer, not in
  any log line. Verified directly: `grep -rn RemoteAddr internal/telemetrycollector/` matches
  exactly one line, the comment saying so; there is no `X-Forwarded-For`/`X-Real-IP` handling either,
  so nothing re-derives an IP from proxy headers. Per-submitter rate limiting is keyed on
  `installId` instead — the one correlator the payload already carries — backed by a second,
  unkeyed, IP-free global limiter as defense in depth.
- **Node hostnames.** The payload carries `nodeCount`, a number, never names.
- **The PVE endpoint URL, or any address.** `Guard` scans the marshalled bytes for anything
  IPv4/IPv6-shaped, wherever in the document it appears, and refuses the payload if it finds one.
- **Any MAC address**, similarly shape-scanned and refused.
- **Guest (VM/CT) names, or the cluster name.** Neither has a recognisable shape, so `Guard` also
  searches for the report's own node names and endpoint host as literal substrings — deliberately
  over-matching (a node genuinely named `pve` collides with the `-pve` in a kernel string, and that
  payload is refused rather than risk the miss going the other way).
- **Free-text evidence, `detail`, or `skipReason`.** These routinely quote command output, API
  responses, and addresses in the underlying `verify` report; none of the three appears in
  `Payload` at all.
- **A timestamp of your own clock.** The collector's own receipt time (`receivedAt`) is used
  instead — a local clock is a fingerprint the payload doesn't need to carry.

None of this is asserted only by comment: `internal/telemetry.Guard` re-scans the marshalled bytes
before every preview and every send, and `internal/telemetrycollector`'s handler re-runs the exact
same `Guard` server-side before a byte reaches storage — a collector that only trusted the client's
own check would be one bad client release away from silently accepting anything.

## How to opt in

Nothing here happens without editing a config file yourself. There is no first-run prompt, no
install-time checkbox, and no UI toggle — see "Consent UX review" below for what that trade-off
means in practice.

```toml
[telemetry]
enabled = true
endpoint = "https://your-collector.example/vnprox/compat"
```

`enabled = true` with no endpoint is a **fatal config error**, not a silent no-op — an operator who
opted in should never end up quietly sending nothing. Then:

```
vnproxctl telemetry preview --report <file>   # see the exact bytes first — needs no opt-in
vnproxctl telemetry status                    # on/off, endpoint, this install's id
vnproxctl telemetry send --report <file>      # submit one report in the foreground
```

`<file>` is whatever `vnproxctl verify --out <file>` (signed) or `vnproxctl verify -o json`
(plain) wrote. `vnproxctl verify` also starts a background send automatically once telemetry is
enabled — it never blocks the run, and a send still in flight when the command exits is abandoned
rather than waited on.

## A real preview transcript

This is not a description of the command's output — it is the literal output of a real
`vnproxctl telemetry preview` run against a sample `vnproxctl verify` report, captured verbatim.

The sample report below is deliberately full of the things the payload must never contain —
fictional node names (`node-alpha`, `node-beta`), a MAC address, a documentation-range IP
(`192.0.2.0/24`, RFC 5737), and a guest name in a free-text detail line — so the reduction is
visible rather than assumed:

```json
{
  "reportVersion": 1,
  "generatedAt": "2026-03-01T12:00:00Z",
  "suite": "hardware",
  "environment": {
    "vnproxVersion": "3.0.3",
    "pveVersion": "pve-manager/9.2.4",
    "kernel": "6.8.12-4-pve",
    "nicModels": [
      "enp3s0 0x8086:0x1521 pci:v00008086d00001521sv00008086sd00000002bc02sc00i00",
      "enp4s0 0x15b3:0x1017 pci:v000015B3d00001017sv000015B3sd00000001bc02sc00i00"
    ],
    "nodes": ["node-alpha", "node-beta"],
    "pveEndpoint": "https://192.0.2.10:8006",
    "mock": false
  },
  "results": [
    {
      "id": "drift.config_vs_live", "matrixRow": 21,
      "area": "Drift detection (config-vs-live, node-vs-node)",
      "suite": "hardware", "precondition": "a real PVE node",
      "status": "pass", "detail": "node-alpha matches its staged config",
      "evidence": [{"source": "command", "ref": "ssh node-alpha ip -j link",
        "output": "enp3s0 link/ether aa:bb:cc:dd:ee:ff, address 192.0.2.10/24"}],
      "durationMs": 412
    },
    {
      "id": "iface.lacp_partner_observed", "matrixRow": 6,
      "area": "Bridges, bonds, VLANs, interfaces",
      "suite": "hardware", "precondition": "a real 802.3ad bond",
      "status": "fail",
      "detail": "bond0 on node-beta reports no LACP partner; guest web-prod-01 is on it",
      "evidence": [{"source": "file", "ref": "/proc/net/bonding/bond0",
        "output": "Partner Mac Address: 00:00:00:00:00:00"}],
      "durationMs": 1203
    },
    {
      "id": "ha.standby_promotes", "matrixRow": 30,
      "area": "Daemon HA", "suite": "hardware",
      "precondition": "two daemons in an active/standby pair",
      "status": "skip", "detail": "only one node online (node-alpha)",
      "skipReason": "only one node online (node-alpha)",
      "evidence": [{"source": "state", "ref": "cluster membership",
        "output": "1 of 2 nodes online"}],
      "durationMs": 3
    }
  ],
  "summary": {"passed": 1, "failed": 1, "skipped": 1}
}
```

Running the real command against it:

```
$ vnproxctl telemetry preview --config vnprox.toml --report sample-report.json
vnproxctl telemetry preview: generated a new install-id (01M12W1ESB4QBF68B97VMQPNJ1); reset it any time with `vnproxctl telemetry reset-id`
{
  "payloadVersion": 1,
  "installId": "01M12W1ESB4QBF68B97VMQPNJ1",
  "vnproxVersion": "3.0.3",
  "pveVersion": "pve-manager/9.2.4",
  "kernel": "6.8.12-4-pve",
  "nicPciIds": [
    "0x15b3:0x1017",
    "0x8086:0x1521"
  ],
  "nodeCount": 2,
  "suite": "hardware",
  "checks": [
    {
      "id": "drift.config_vs_live",
      "status": "pass",
      "durationMs": 412
    },
    {
      "id": "iface.lacp_partner_observed",
      "status": "fail",
      "durationMs": 1203
    },
    {
      "id": "ha.standby_promotes",
      "status": "skip",
      "durationMs": 3
    }
  ]
}
```

Notice everything that did not survive: `node-alpha`/`node-beta`, the `aa:bb:cc:dd:ee:ff` MAC, the
`192.0.2.10` addresses, `web-prod-01`, `/proc/net/bonding/bond0`, and every `detail`/`evidence`
string are gone. What's left is two PCI hardware ids, a node **count** (`2`), and three check
verdicts by id. The install-id line went to **stderr**, not the JSON on stdout — the two are kept
separate so nothing about "a new id was generated" ever ends up inside the bytes that get sent.

Run this yourself, on your own report, any time — `preview` needs no opt-in and contacts nothing.

## How to opt out and revoke what was already sent

- **Turn it off:** delete or comment out `[telemetry]`, or set `enabled = false`. Nothing further is
  read, built, or sent from that point on.
- **Stop being correlatable across future sends:** `vnproxctl telemetry reset-id` deletes the
  stored id and generates a fresh one. The old value is not recorded anywhere — no audit row, no log
  line, no "previous" column — though SQLite may retain the freed page containing the old string
  until it's reused or the store is compacted; this is a promise about what the store *returns*, not
  about raw bytes on disk, and is documented rather than implied away.
- **Delete submissions you already sent**, keyed on the one correlator you control (`installId`,
  shown by `vnproxctl telemetry status`):
  - `curl -X DELETE https://<collector>/v1/installs/<installId>` against the running collector; or
  - `vnproxtelemetryd revoke --db <path> --install-id <installId>` run directly against the
    collector's database file.

  Both delete every row for that id and report how many were removed. **Both are idempotent** —
  deleting an id with nothing left, or one never seen, reports zero removed rather than an error, so
  the response can't be used to probe whether an id was ever submitted. Demonstrated against a real,
  local collector below:

  ```
  $ vnproxtelemetryd revoke --db demo-collector.db --install-id 01M12W1ESB4QBF68B97VMQPNJ1
  deleted 1 submission(s) for install-id 01M12W1ESB4QBF68B97VMQPNJ1
  $ vnproxtelemetryd revoke --db demo-collector.db --install-id 01M12W1ESB4QBF68B97VMQPNJ1
  deleted 0 submission(s) for install-id 01M12W1ESB4QBF68B97VMQPNJ1
  ```

## Retention

Submissions older than a configurable window (`--retention-window`, default **180 days**) are
removed by a real `DELETE`, run on a loop (`--retention-interval`, default hourly) for as long as
the collector runs. `vnproxtelemetryd retention-run --window <duration>` runs one pass immediately —
useful for an operator who wants to demonstrate the deletion without waiting for the loop, rather
than only reading that it happens.

## Aggregate stats

**No public collector instance exists today.** `registry.vnprox.com` and the other hostnames a
hosted instance would live under do not resolve — DNS for a hosted collector is a deferred owner
decision (tracked outside this page). No numbers below are real submission data, and none are
estimated or fabricated to fill the gap — this section shows the *mechanism* only, run against a
disposable local database seeded with two synthetic submissions for the purpose of this page.

The mechanism itself is real and already exercised end-to-end
(`planning/reports/evidence/T-3710-collector-e2e-2026-08-24.txt`): `GET /v1/summary` on a running
collector, or `vnproxtelemetryd report [--json]` against its database file directly, both return an
aggregate — submission and distinct-install counts, `pveVersion`/`vnproxVersion`/`suite` tallies,
and per-check pass/fail/skip counts. Neither exposes a per-submission row list; there is no
dashboard and no query surface beyond this aggregate, which is deliberate — the compatibility
question this exists to answer ("which PVE versions is vnprox actually running against") is
answered by an aggregate, and a wider surface would be a second place every promise on this page
would need to be re-checked against.

Worked example — two synthetic reports (one built from the sample above, one a copy with a
different `installId` and `pveVersion`) submitted to a local `vnproxtelemetryd`, then read back:

```
$ vnproxtelemetryd report --db demo-collector.db
submissions:        2
distinct installs:  2
oldest:             2026-08-28T00:25:25Z
newest:             2026-08-28T00:25:25Z

PVE versions:
  pve-manager/8.4.1              1
  pve-manager/9.2.4              1

vnprox versions:
  3.0.3                          2

suites:
  hardware                       2

checks (pass/fail/skip):
  drift.config_vs_live                     2/0/0
  ha.standby_promotes                      0/0/2
  iface.lacp_partner_observed              0/2/0
```

`vnproxtelemetryd report --db demo-collector.db --json` returns the same data as the JSON object
`GET /v1/summary` serves on a running collector — `generatedAt`, `oldestReceivedAt`/
`newestReceivedAt`, and the three per-field tallies plus per-check pass/fail/skip counts, all
computed live from the `submissions` table with no cached or precomputed values.

If and when a public collector is stood up, this section will be replaced with a link to its own
`/v1/summary`, or a periodically-regenerated copy of its output — never hand-typed numbers.

## Consent UX review (T-3812)

This card's own acceptance criteria require this review to end in "no change needed, stated why" or
a flagged follow-up — and explicitly forbid loosening the default. **No default changed here.**
Findings:

**What already meets or exceeds open-source norms:**

- Off by default *structurally* (no default endpoint to opt into, not just a `false` flag) is
  stricter than most "anonymous usage analytics" opt-outs, which usually ship a working default
  endpoint behind a flag that defaults to on or is easy to miss.
- `preview` showing the *exact* bytes before any opt-in, with a test enforcing that preview and send
  marshal the same buffer once, is stronger than the norm — most CLI tools with telemetry (even
  privacy-conscious ones like Next.js's `next telemetry status` or the .NET CLI) describe what they
  send in prose; very few let you print the literal request body without opting in first.
- A payload that is *enumerated* (closed schema, scanned for hostname/IP/MAC shapes on every send,
  both client- and server-side) rather than "redacted" is a stronger guarantee than most projects
  make, and it's backed by tests rather than only documentation.
- Revocation keyed on a resettable, non-derived id, and idempotent, matches or exceeds what most
  hosted telemetry collectors offer an end user without contacting support.

**The one real gap: near-zero discoverability.** There is no first-run notice, no installer prompt,
no debconf question (checked: `packaging/debian/postinst` and `packaging/build/pkgroot/DEBIAN/postinst`
have no telemetry-related step), and no mention of telemetry anywhere in the web UI (checked:
`grep -rl telemetry web/src` finds nothing, including `web/src/onboarding/`, which walks a new
operator through the rest of first-run setup). An operator finds out this feature exists only by
reading `vnprox.toml`'s comments or this documentation. Contrast with the common OSS pattern (Next.js,
Angular CLI, Homebrew, the .NET CLI) of a one-time, non-interactive notice on first CLI run —
_"vnprox collects no telemetry by default; see `vnproxctl telemetry status`"_ — which improves
discoverability without nagging and without changing the opt-in default.

Given the feature exists specifically to grow `docs/status-matrix.md`'s hardware-validated coverage
beyond one dev cluster, and near-nobody will discover an opt-in they don't know exists, the
**recommended follow-up** (not implemented by this page, and not a default change) is: a one-time,
printed-once notice from `vnproxctl verify` when telemetry is unconfigured, pointing at `telemetry
status`/`telemetry preview` — never a prompt that blocks or nags, and never anything that flips a
default. This is a discoverability fix, not a consent-model change, and is left for a follow-up
task rather than done here, since it touches `cmd/vnproxctl`'s `verify` command path rather than the
telemetry surface this page documents.

## See also

- `docs/security.md`, "Compatibility telemetry (T-2503)" — the same facts, written for someone
  auditing the security design rather than deciding whether to opt in, and including the collector's
  own privacy statement in full (source-IP handling, re-validation, rate limiting, TLS posture).
- `docs/deployment.md`, "`vnproxctl telemetry` — opt-in compatibility reporting" — the `[telemetry]`
  config reference and command flags.
- `docs/status-matrix.md` — the compatibility matrix this data is meant to grow beyond one dev
  cluster.
