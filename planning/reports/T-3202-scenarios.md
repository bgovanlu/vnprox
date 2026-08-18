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
**Change**: an `iface.update`/interface-config changeset op that intentionally breaks the
interface carrying the SSH management session (an incorrect IP/VLAN change to the same interface
`vnprox-setup`/SSH already uses — 192.168.1.7's own management path), applied through vnprox's
real change engine (stage → validate → diff → apply), **never confirmed**.
**Mechanism under test**: `internal/change/localtimer.go`'s `LocalTimerAgent` — "each node arms
its own local timer at step start — no cross-node dependency for safety." The revert fires
in-process on `pve001` itself, independent of whether pve001 is reachable from outside during the
window — this is precisely what makes the design safe to test.
**Expected outcome, declared now**: SSH to `192.168.1.7` becomes unreachable within seconds of
apply (proving the lockout is real, not a no-op test). Within the 30s confirm window plus real
`ifreload` time (a few seconds of margin budgeted), SSH access **restores on its own**, with no
confirm ever sent and no manual intervention. The interface's live config after restoration
matches its pre-change state.
**Failure verdict, declared now**: if SSH does not restore within 90s (3x the confirm timeout,
generous margin for `ifreload` + TCP re-establishment), this is a **release blocker** per T-1804
AC2 — not a finding, not a doc note. Recovery path if it fails: user's console access to `pve001`,
manually restore `/etc/network/interfaces` from vnprox's own pre-apply snapshot (or PVE's own
`interfaces.new`/backup if present) and `ifreload -a`.

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
