# T-3202 — failure-injection scenario table, expected outcomes declared before any run

T-1804 verbatim, closed by T-3202 now that a real two-node cluster exists (T-3201). Per T-1804
acceptance criterion 1: every scenario's expected outcome is written here, and this file is
committed, **before** any scenario is run — the evidence commit(s) that follow reference this
file's commit hash so the ordering is verifiable from git history, not asserted.

**Target cluster**: `pvecube` (192.168.1.9) + `pve001` (192.168.1.7), `vnprox-dev`, PVE 9.2.10,
vnprox `4.0.0+30+g4b3f20b`. **Console recovery confirmed available** for both nodes (user has
physical/out-of-band access) before any scenario that risks real network lockout is run — this is
the documented recovery path T-1804 requires per scenario.

**Confirm timeout**: every scenario below uses `MinConfirmTimeout` (30s), not the 120s default —
bounds the real-world exposure window for any scenario that actually drops reachability, without
changing what's being proven (the mechanism is timeout-value-agnostic by design; a shorter timeout
is a stronger, faster-converging test of the same code path, not a weaker one).

## Scenarios run live this session

### Scenario 1 — management link dropped mid-apply (the classic lockout)

**Target**: `pve001` (the more expendable node of the two).
**Change**: an `iface.update`/interface-config changeset op that intentionally re-addresses
`vmbr0` — `GET /protected-interfaces/status` confirms this node's detected protected set is
`bridge:pve001:vmbr0` (roles `corosync`, `mgmt`), the same interface carrying the SSH management
session — to a wrong IP, applied through vnprox's real change engine (stage → validate → diff →
apply), **never confirmed**.
**Correction found during setup, recorded before the run**: this changeset is refused outright by
`internal/change/validate_safety.go`'s hard "no override in UI" interlock
(`docs/security.md`'s Safety interlocks section) as soon as `vmbr0` is a detected protected
interface — which it is. The only sanctioned way to exercise this exact scenario for real is the
interlock's own documented escape hatch, `[safety] allow_dangerous_ops` (`docs/deployment.md`),
which downgrades the refusal from a hard error to an audited warning. **Set to `true` on `pve001`
only, for the duration of this one scenario, then reverted to `false` immediately after** — this
is the sanctioned, audited path for a controlled test of exactly this scenario, not a bypass of
it. The daemon on `pve001` is restarted once to pick up the flag change before the run, and once
more after reverting it.
**Confirm-window correction, recorded before the run**: `touchesMgmtPath` changesets carry a
**mandatory 180s floor** (`change.MgmtConfirmTimeoutFloor`), not the 30s this file originally
declared — a lower value is rejected with `400 confirm_window_too_short` before any apply work.
The scenario runs at 180s, not 30s; the failure-verdict margin below is adjusted accordingly. The
apply call also carries the mandatory typed `mgmtAck: {node: "pve001"}` acknowledgement T-703's
ceremony requires for any `touchesMgmtPath` changeset — recorded to the audit log as
`changeset.mgmt_ack` before the apply, per `RecordMgmtAck`'s own contract.
**Mechanism under test**: `internal/change/localtimer.go`'s `LocalTimerAgent` — "each node arms
its own local timer at step start — no cross-node dependency for safety." The revert fires
in-process on `pve001` itself, independent of whether pve001 is reachable from outside during the
window — this is precisely what makes the design safe to test.
**Expected outcome, declared now**: SSH to `192.168.1.7` becomes unreachable within seconds of
apply (proving the lockout is real, not a no-op test). Within the 180s confirm window plus real
`ifreload` time (margin budgeted), SSH access **restores on its own**, with no confirm ever sent
and no manual intervention. The interface's live config after restoration matches its pre-change
state.
**Failure verdict, declared now**: if SSH does not restore within 300s (180s window + 120s
generous margin for `ifreload` + TCP re-establishment), this is a **release blocker** per T-1804
AC2 — not a finding, not a doc note. Recovery path if it fails: user's console access to `pve001`,
manually restore `/etc/network/interfaces` from vnprox's own pre-apply snapshot (or PVE's own
`interfaces.new`/backup if present) and `ifreload -a`.

#### Result: PASSED — self-healed at exactly the configured deadline, hardware-confirmed

**A real, previously-hidden bug blocked the first attempt and was fixed before this result** (see
`packaging/systemd/vnprox.service`'s `/etc/iproute2/rt_tables.d` `ReadWritePaths` addition and
`docs/security.md`'s Host footprint section, both 2026-08-18): `ifreload -a` writes a VRF
route-table cache file on every reload regardless of whether VRFs are involved, and
`ProtectSystem=strict` made that path read-only, so the first attempt's apply failed synchronously
before ever reaching `awaiting_confirm` — filed and fixed as its own finding, rebuilt, redeployed,
retried.

**Evidence — the apply response, in full** (`POST /changesets/{id}/apply`, real HTTP response
body, node `pve001`, changeset `01M0AQW6ACEAH23HBD0Z79MG95`):
```json
{"applyLog":{"steps":[{"kind":"stage_file","status":"ok","startedAt":1787066988,"endedAt":1787066988},
{"kind":"reload","status":"ok","startedAt":1787066988,"endedAt":1787066988}],
"nodeTimers":[{"node":"pve001","status":"armed","deadline":1787067168}]},
"confirmDeadline":1787067168,
"unattendedRevert":{"coversUntil":1787067168,"required":false,"available":true,"fullWindow":true},
"status":"awaiting_confirm"}
```
Both apply steps `"ok"` — the bad address really was written and really was reloaded onto the live
interface (not a validation-time no-op). `nodeTimers` shows the local timer armed with a deadline
exactly 180s (the confirm window) after the apply started.

**Evidence — the connection genuinely broke.** The very `ssh`/`curl` invocation that sent the
apply request itself hung for over 100s and had to be moved to a background job — the TCP
connection carrying it was disrupted by the address change it had just triggered. A parallel
monitor polling fresh SSH connection attempts confirmed sustained unreachability before
reconnecting.

**Evidence — the timer fired, unattended, at the deadline, and reverted successfully.** `GET
/changesets/{id}` read back after reconnection:
```json
{"applyLog":{
  "rolledBackBy":"system:rollback",
  "rollback":[{"summary":"Restore /etc/network/interfaces on pve001 from pre-apply snapshot and reload","status":"ok","at":1787067168}],
  "nodeTimers":[{"node":"pve001","status":"rolled_back","resolvedAt":1787067168}]},
"status":"rolled_back"}
```
`resolvedAt` (`1787067168`) minus the apply's own `startedAt` (`1787066988`) is **exactly 180
seconds** — the timer fired at precisely its configured deadline, not early, not late.
`rolledBackBy: "system:rollback"` confirms this was the unattended path, not a manual confirm/
rollback call (none was ever sent — verified: no `POST .../confirm` or `.../rollback` call was
made from this session at any point during the window). The rollback step itself is `"ok"` — the
same node-local timer agent that armed the timer also executed and verified the restore.

**Evidence — the live interface actually came back correct**, checked immediately after
reconnection: `ip -4 addr show vmbr0` → `192.168.1.7/24` (the original, correct address, not the
bad `10.99.99.99/24`); `/etc/network/interfaces` → the original `address 192.168.1.7/24` /
`gateway 192.168.1.1` stanza, byte-identical to pre-apply; `systemctl is-active vnprox.service` →
`active`; `curl https://127.0.0.1:8007/` → `200`.

**Verdict: PASSED, unambiguously.** This is the first time in this project's history "if the
change locks you out, it reverts itself" has been observed on real hardware against a real
lockout, rather than asserted from mock tests or code inspection. `allow_dangerous_ops` reverted
to `false` on `pve001` immediately after (confirmed via config read + service restart + health
check), closing the controlled exception used to run this one test.

### Scenario 5 — firewall-only changeset lockout (the headline result, T-1805's real acceptance test)

**Target**: `pvecube` (the primary node — deliberately the harder case, since this exercises the
apply-time sealed revert ticket, T-1805, closing the one gap T-502 flagged: firewall/SDN writes
use the *user's* PVE ticket, not a node-local file write, so before T-1805 this specific scenario
would **not** have reverted).
**Change**: an `fw.rule.create` changeset op adding a cluster-scope (or node-scope) DROP rule that
blocks inbound TCP to vnprox's own management port (8007) and/or SSH (22) from this operator's
source, applied through the real change engine, **never confirmed**.
**Mechanism under test**: `internal/change/apply.go`'s `sealRevertTicket`/`wipeRevertTicket` and
`apply_exec.go`'s firewall-scope revert path (`needsRevertTicket`) — the sealed PVE ticket
captured at apply time, used unattended to restore the firewall scope's pre-apply ruleset when the
confirm window lapses with nobody there to supply a fresh ticket.
**Expected outcome, declared now**: HTTPS access to `https://192.168.1.9:8007` (and/or SSH, per
which port the rule targets) becomes unreachable within seconds of apply. Within the 30s confirm
window plus revert-apply time, access **restores on its own**. The cluster (or node) firewall
ruleset after restoration matches its pre-apply state (no orphaned DROP rule).
**Failure verdict, declared now**: same as Scenario 1 — non-recovery within 90s is a release
blocker, not a finding. This is explicitly the scenario T-1804 AC3 names as the card's headline
result, so a failure here is the single most important thing this card can find.
**Recovery path if it fails**: user's console access to `pvecube`; delete the DROP rule directly
via `pve-firewall`/editing `/etc/pve/firewall/cluster.fw` (or the relevant node/scope file) and
`systemctl restart pve-firewall` if the daemon doesn't pick up the edit within its own poll cycle.

#### Result: PASSED on the third attempt — two real, previously-unknown bugs found and fixed live

**Attempt 1** staged a single `fw.rule.create` DROP rule (cluster/node scope's own `enable` was
still `0` from a clean node, so the rule was inert — no lockout, no useful signal) but never
reached apply: `fw_verify` (`GET /nodes/{node}/firewall/status`) hard-failed. `pvesh ls
/nodes/pvecube/firewall` confirms real PVE 9.2.10 exposes only `log`/`options`/`rules` under that
path — `firewall/status` **does not exist on real PVE**, despite this codebase modeling it as a
real endpoint. Every firewall-touching changeset's apply was hard-failing this step against any
real node, unconditionally, regardless of whether `pve-firewall` actually compiled the change
cleanly. **Fixed**: `cmd/vnproxd/changeagent.go`'s `pveGateway.FirewallCompileStatus` now catches
`*pve.ErrPVEServer{StatusCode: 501}` via `errors.As` and degrades to `FwCompileStatus{OK: true,
Message: "...unavailable on this PVE build..."}` instead of propagating the error — the same
"degrade, don't block on unobservable infrastructure" pattern already used elsewhere in this
codebase. Regression tests: `TestFirewallCompileStatus_501DegradesToUnverifiedOK`,
`TestFirewallCompileStatus_OtherErrorsStillFail` (`cmd/vnproxd/changeagent_test.go`).

**Attempt 2** (single DROP rule, `enable: true` on the rule itself, cluster/node ruleset `enable`
still untouched) staged, validated, and applied cleanly — `fw_verify` now returned `"ok"`. But
because the ruleset-level firewall was still disabled (`pve-firewall status` → `disabled/running`),
the rule was inert and no lockout occurred — not a useful test of the revert mechanism, only of the
501 fix. The 30s timer fired and rollback ran, but the rollback's own "restore firewall scope
options" step failed: `restoring options: pve: request error (status 400): Parameter verification
failed.` — reproducing a failure mode first seen (unexplained) during the pre-fix run that caused
this scenario's original lockout incident. Root cause, found by reading `pvesh get
/nodes/pvecube/firewall/options` directly: a node whose in/out policy was never explicitly set
reports back only `{digest, enable}` — no `policy_in`/`policy_out` field at all — so the pre-apply
snapshot captures both as `""`. `cmd/vnproxd/changeagent.go`'s `reconcileFwScope` sent
`PolicyIn`/`PolicyOut` to the restore `PUT` unconditionally whenever the scope supports them, with
no empty-string guard — unlike the `PolicyForward`/`LogLevelForward` restore right below it, which
already had exactly this guard (and whose comment, before this fix, wrongly claimed `""` "round-
trips harmlessly" for `PolicyIn`/`PolicyOut` — real PVE rejects it with 400). **Fixed**: the same
non-empty guard `PolicyForward`/`LogLevelForward` already used, applied to `PolicyIn`/`PolicyOut`.
Regression test: `TestRestoreFirewallScope_OmitsEmptyPolicyInOut`
(`cmd/vnproxd/changeagent_test.go`), asserting the restore `PUT` body omits both keys when the
snapshot captured them empty. The DROP rule itself was, in fact, correctly removed by both failed
attempts — only the ruleset-options restore step was broken, so no rule was ever left orphaned.

**Attempt 3** (real result), changeset `01M0ATXD7X4JTZV7Q02AVXHK9Y`, three ops: `fw.options.update`
(cluster scope, `enabled: true`), `fw.options.update` (node scope, `enabled: true`), `fw.rule.create`
(node scope, DROP tcp dport 8007 from this operator's own source `192.168.1.45/32`). Applied with
`confirmTimeoutSec: 30`, never confirmed.

**Evidence — the apply response, in full** (`POST /changesets/{id}/apply`):
```json
{"applyLog":{"steps":[
  {"kind":"fw_apply","summary":"Apply 1 firewall op(s) to the datacenter firewall","status":"ok"},
  {"kind":"fw_apply","node":"pvecube","summary":"Apply 2 firewall op(s) to node pvecube's firewall","status":"ok"},
  {"kind":"fw_verify","node":"pvecube","summary":"Verify firewall compiled cleanly on pvecube","status":"ok"}],
  "nodeTimers":[{"node":"pvecube","status":"armed","deadline":1787070244}]},
 "confirmDeadline":1787070244,
 "unattendedRevert":{"coversUntil":1787070244,"required":true,"available":true,"fullWindow":true},
 "status":"awaiting_confirm"}
```
All three steps `"ok"`, including `fw_verify` (the 501 fix holding under a real apply, not just the
regression test).

**Evidence — the lockout was real.** `curl -k --max-time 4 https://192.168.1.9:8007/` from this
operator's own machine (`192.168.1.45`, the exact source the DROP rule matched) returned `HTTP:000`
(connection refused/timed out) immediately after apply — genuine, not simulated. `pve-firewall
status` on the node independently confirmed the ruleset was live.

**Evidence — the timer fired, unattended, and connectivity restored on its own.** A poll loop
against `https://192.168.1.9:8007/` (no vnprox session involved — a bare TCP/TLS probe) recorded
`HTTP:000` continuously from apply until `2026-08-18T16:24:05Z`, then `HTTP:200` — apply started at
unix `1787070214`, deadline `1787070244` (exactly 30s later), first `200` observed at `1787070245`.
`GET /changesets/{id}` read back afterward:
```json
{"applyLog":{
  "rolledBackBy":"system:rollback",
  "rollback":[
    {"node":"pvecube","summary":"Restore /etc/network/interfaces on pvecube from pre-apply snapshot and reload","status":"ok","at":1787070244},
    {"summary":"Restore firewall scope fw-ruleset::cluster from pre-apply snapshot","status":"ok","at":1787070244},
    {"node":"pvecube","summary":"Restore firewall scope fw-ruleset:pvecube:node from pre-apply snapshot","status":"ok","at":1787070244}],
  "nodeTimers":[{"node":"pvecube","status":"rolled_back","resolvedAt":1787070244}]},
 "status":"rolled_back"}
```
All three rollback steps `"ok"` this time — including both firewall-scope restores, the step
Attempt 2 found broken. This is `unattendedRevert`'s sealed-PVE-ticket path (T-1805) actually
exercised end-to-end on real hardware for the first time: the revert used the *user's* PVE ticket
sealed at apply time, not a node-local file write, and it fired with nobody watching.

**Evidence — live state after restoration, checked directly on the node**: `pvesh get
/cluster/firewall/options` → `{"digest":"2c1759ebec624b1e511ba7f635915ab2df354cba","enable":0}`;
`pvesh get /nodes/pvecube/firewall/options` → the identical digest/`enable:0`; `pvesh get
/nodes/pvecube/firewall/rules` → `[]`; `pve-firewall status` → `disabled/running`. Every value byte-
identical to the pre-test state — no orphaned rule, no orphaned enable flag, nothing left behind.

**Verdict: PASSED.** This is the first time this project has observed, on real hardware, the exact
gap T-502 originally flagged and T-1805 closed in code: a firewall-only lockout — the harder case,
since the revert needs the *user's* PVE ticket, not a file write vnprox's own daemon identity can
always perform — reverting itself with nobody there to supply a fresh credential. Two real,
previously-unknown bugs were found and fixed by this run (`fw_verify`'s 501 assumption; the
rollback's `PolicyIn`/`PolicyOut` empty-string restore), both now covered by regression tests, both
shipped to `pvecube` and `pve001` before this result was recorded.

## Scenarios deferred to a separate, lower-risk pass (not run live in this session)

These don't require deliberately breaking network reachability to observe, so they carry
materially less risk and are handled separately (either later in this session or a follow-up):

- **Scenario 2** — `vnproxd` killed inside the confirm window (crash recovery re-arms the timer
  via `ArmPendingOnStartup`/the `node_timers` DB table). Testable without ever losing network
  access: apply a change, `systemctl kill`/`kill -9` the vnproxd process mid-window, restart it,
  confirm the timer re-arms from the DB with the *original* deadline (not a fresh window) and
  still fires correctly.
- **Scenario 3** — node hard-reset inside the confirm window. Needs an actual reboot; deferred
  because it's the least differentiated from Scenario 2's crash-recovery proof (both exercise
  `ArmPendingOnStartup`) and carries its own unrelated risk (a reboot of a real PVE node with
  live guests) not worth stacking onto this pass.
- **Scenario 4** — confirm window expires with no session alive at all (the "nobody's watching"
  case). Low-risk (only reachable via changes that don't threaten connectivity), deferred for
  time in this pass, not for risk reasons.
- **Scenario 6** — apply interrupted between the write and `ifreload`. Needs a deliberate mid-step
  crash/kill timed precisely between two sub-steps of one op's execution — a more surgical,
  code-level test than a live-cluster scenario; better suited to an injected-fault unit/integration
  test than a hardware run, and not attempted here.

Per T-1804 AC2's "a scenario that does not self-heal is a release blocker" standard, Scenarios 1
and 5 are the ones that most directly test the product's headline claim under the conditions an
actual operator would hit it (a bad interface change, a bad firewall rule) — they are prioritized
accordingly. Scenarios 2-4 and 6 remain open per T-1804's original scope and are filed as
still-unproven in `planning/reports/blocked-validation.md`, not silently dropped.
