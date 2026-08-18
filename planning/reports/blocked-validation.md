# Blocked-validation register

The register `T-1803` named as Arc 4's authoritative ledger of what remains unproven, referenced
by `implementation-plan-proven.md:26` and never created until now. Written by `T-3201`, the first
card in this project's history with a real two-node PVE cluster (`pvecube` 192.168.1.9 +
`pve001` 192.168.1.7, cluster `vnprox-dev`, PVE 9.2.10, corosync quorate 2/2) to test against.

Per `docs/roadmap-earned.md`'s own standard: **proof is not self-report.** Every "proven" line
below carries the artifact (a `journalctl` excerpt, a command and its real output, a test run) —
narrative claims without evidence don't belong in this file.

Structure: §1 what T-3201 proved, §2 what T-3201 found that nobody predicted, §3 what is still
blocked and needs T-3202/T-3203/more hardware, ordered by severity within each section per
T-1803's original acceptance criteria.

---

## 1. Proven this session, with evidence

### 1.1 Peer API round trips work, bidirectionally, sustained

**First real confirmation in the project's history.** `GET /api/peer/host/links` (~5s cadence)
and `GET /api/peer/firewall/log` (~1s cadence) both succeed (HTTP 200) symmetrically between the
two nodes over a sustained, multi-hour window.

```
$ ssh root@pvecube.localdomain "journalctl -u vnprox.service --since '60 min ago' | grep -c 'peer/host/links\|peer/firewall/log'"
971
```

Sample line (pve001 answering pvecube):
```
Aug 18 09:25:16 pvecube vnproxd[821724]: {"msg":"http request","method":"GET","path":"/api/peer/host/links","status":200,"bytes":30133,"duration_ms":2,"remote_addr":"192.168.1.7:40410"}
```
See §2.5 for the real, separately-tracked degradation this traffic also exhibits.

### 1.2 T-1906-bug-01 — mitigated by existing code, confirmed live, for real

**2026-08-05 finding (single node, predicted only):** pvecube's `pve-ssl.pem` carries a stale IP
SAN (`192.168.100.99`, not its real `192.168.1.9`), so "a peer dialled by IP would fail pinned
hostname verification."

**Verdict: (b) — already mitigated by existing code, now confirmed with a real second node
dialling pvecube by IP continuously for hours with zero TLS verification failures.**

Evidence — pvecube's cert really does carry the stale SAN (openssl, live read):
```
$ ssh root@pvecube.localdomain "openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -text | grep -A1 'Subject Alternative Name'"
X509v3 Subject Alternative Name:
    IP Address:127.0.0.1, IP Address:0:0:0:0:0:0:0:1, DNS:localhost,
    IP Address:192.168.100.99, DNS:pvecube, DNS:pvecube.localdomain.
```
Evidence — pve001's own daemon log, dialling pvecube by IP, resolving verification against the
node NAME instead of the dial address (the T-1906-bug-01 fix, `cmd/vnproxd/certwire.go`'s
`attachCertVerifyNames` / `internal/certs.Service.VerifyNameFor`):
```
Aug 18 09:07:40 pve001 vnproxd[3038]: {"msg":"certs: this peer will be verified against a resolved name rather than its dial address","dial_host":"192.168.1.9","server_name":"pvecube"}
```
Zero `x509`/`handshake`/`verify` errors anywhere in either node's log over the observed window
(`journalctl -u vnprox.service --since '60 min ago' | grep -iE 'x509|handshake|verify|tls:'` →
empty on pve001). Mechanism: `certClusterFacts` derives each peer's verification name from PVE
cluster status (the node name, e.g. `"pvecube"`), and `pve-ssl.pem` *does* carry `DNS:pvecube` as
a SAN even though its IP SAN is stale — so verification succeeds against the name, never touching
the wrong IP SAN at all. The bug is real (the IP SAN genuinely is stale) but the product already
does not rely on it.

### 1.3 `vnproxctl doctor --live` on both real nodes

Ran with a real bearer token (minted via a throwaway PVE test account, see §2.4) against both
daemons.

```
$ ssh root@pvecube.localdomain "VNPROX_TOKEN=... vnproxctl doctor --live -o json"
```
```json
{
  "pve_reachable":  {"status":"pass","detail":"PVE API reachable and the token authenticates"},
  "pve_privileges": {"status":"fail","detail":"the configured PVE token is missing privileges vnprox needs: Sys.Modify (...); SDN.Allocate (...); VM.Config.Network (...)"},
  "peer_secret":    {"status":"skip","detail":"not checked from the CLI — comparing the secret across nodes needs the daemon's peer client"},
  "clock_skew":     {"status":"skip","detail":"PVE did not report a server time"}
}
```
Byte-identical result on pve001. **`clock_skew`/`peer_secret` are STILL `skip` even with `--live`
and a real second node — this is not new. Confirmed by reading `cmd/vnproxd/doctorlive.go`
(`newDoctorLiveRunner`): `Env.Peers` is never wired (no peer-digest route exists —
`T-2406-followup-02`), and `doctorPVEProbe.Ping` deliberately always returns a zero time (no
PVE server-time surface — `T-2406-followup-01`). `docs/deployment.md:812-817` already documents
both as still-skipping; this session is the first time that claim was checked against a real
second node rather than asserted from reading the code, and it holds.** So a 2-node cluster still
answers exactly 8 of 10 checks under `--live`, same as documented for a single node — the second
node did not change this, because the missing piece in both cases is server-side code that was
never written, not a second-node precondition. See §2.4 for a **new**, unrelated `pve_privileges`
finding this run surfaced.

### 1.4 Corosync: real 2-node ring output captured

```
$ ssh root@pvecube.localdomain "corosync-cfgtool -s"
Local node ID 1, transport knet
LINK ID 0 udp
	addr	= 192.168.1.9
	status:
		nodeid:          1:	localhost
		nodeid:          2:	connected
```
Byte-shape-identical on pve001 (own node ID/addr swapped). Confirms the cluster is quorate and
both links are up. See §2.1 for what this evidence actually proves about
`internal/host.ParseCorosyncStatus` — the scope note in `docs/features/monitoring.md` §5
("reports only this daemon's own local node's ring status... cluster-wide peer fan-out needs a
new peer API route") is confirmed unchanged: no `/api/peer/corosync*` route exists in
`internal/peer/client.go`'s full 39-method RPC surface.

### 1.5 T-2805 presence/locks fan-out gap: confirmed still unfilled

`internal/peer/client.go`'s `*Client` exposes 39 RPC methods (interfaces, LLDP, stats, services,
links, FDB, neighbors, container interior/ping, conntrack, IPv6 RA, FRR BGP/EVPN, DHCP leases,
firewall log, audit, flows, **snapshots**, timers, capture, replicate, health/version/compat —
enumerated exhaustively via `grep -n '^func (c \*Client)' internal/peer/client.go`). None of them
is a lock or presence read. `internal/presence/doc.go` and its structural test
(`TestChangeEngineDoesNotImportPresence`) confirm the package is still in-process only. The
gap `docs/project-status.md:244` documents ("locks and presence are node-local; a peer-API
fan-out for cross-node presence is a stated, unfilled gap") is real and, being an absence of code
rather than a behavior, does not need a second node to observe — a full method-surface audit is
sufficient and is now on record.

### 1.6 Certificate cluster inventory

```
"certs: cluster certificate inventory","certificates":3,"nodes":2,"cluster_ca":true,"issues":0
```
on both nodes, steady-state, confirming pmxcfs-backed certificate inventory correctly enumerates
a real 2-node cluster's certificates with zero blocking issues.

---

## 2. New findings this card surfaced, unanticipated by the card brief

Ordered by how much a real operator would care.

### 2.1 `internal/host.ParseCorosyncStatus` cannot parse a real, modern (knet) cluster at all — `corosync_link_degraded` is silently a permanent no-op

**Severity: high.** `needs-hardware-validation.md`'s own T-803 entry already flagged this as an
open question ("corosync's knet transport (the PVE default since 6.x) may report link status as
`LINK ID`/`addr`/per-node `link enabled`/`link connected` fields instead of the classic
ring/udpu shape modeled here"). This session answers it, and it is worse than "different wording
for FAULTY" — the parser finds **zero rings at all** against real output, silently.

Real captured output from both nodes (`corosync-cfgtool -s`, PVE 9.2.10, knet transport):
```
Local node ID 1, transport knet
LINK ID 0 udp
	addr	= 192.168.1.9
	status:
		nodeid:          1:	localhost
		nodeid:          2:	connected
```
`internal/host/corosync.go`'s `parseRingIDHeader` only recognizes a line beginning
`"ring id"` (case-insensitive) as a block header (`internal/host/corosync.go:236-252`).
Real knet output says `"LINK ID 0 udp"`, never `"RING ID n"` at all — so `cur` never gets set,
every subsequent line (`addr`, `status:`, both `nodeid:` lines) is silently skipped as
"preamble before any RING ID block" (`internal/host/corosync.go:212-214`), and
`ParseCorosyncStatus` returns `(nil, nil)` — no rings, no error.

Reproduced directly against the real captured text (not inferred from reading the code):
```go
rings, err := host.ParseCorosyncStatus([]byte(realKnetOutput))
// rings == host.RingStatus(nil), err == nil
```
run via a temporary test in `internal/host` (`go test ./internal/host/... -run TestT3201RealKnetOutput -v`),
confirmed, then removed — not left in the tree.

**Consequence:** `corosync_link_degraded` (`internal/findings/health_corosync.go`) is fed by
`CorosyncStatus()` returning an empty ring map on **every real PVE node running corosync's
default knet transport** — which per this codebase's own comment is every PVE cluster since 6.x.
The check can never fire, healthy or faulty, on any real deployment today. This is functionally
identical to "corosync not installed" (`ErrCorosyncUnavailable`'s degraded case) even though
corosync is installed, running, and quorate — the check silently reports nothing rather than
silently reporting a false negative, which is the better of two bad outcomes, but it means this
entire health check has never actually protected a real cluster.

**Not fixed in this session** — this is a real code defect but outside T-3201's explicit code-fix
mandate (only `certs.NewService`'s nil-panic was authorized). Flagged here with the exact
location and root cause for whoever picks it up: `internal/host/corosync.go`'s
`parseRingIDHeader`/`ParseCorosyncStatus` need a second recognized header shape (`"link id"` /
`"LINK ID"`) and the nested `nodeid: N: <status>` lines need their own field mapping — the
current `id=`/`status=` two-key model doesn't fit knet's block shape at all, so this is closer to
a second parser branch than a one-line fix. Whoever fixes it should re-run against **both**
node's real output (captured above) as the fixture, not invent one.

**Fixed and confirmed (2026-08-18, same-day follow-up session).** `ParseCorosyncStatus` gained a
second recognized header (`parseLinkIDHeader`, "LINK ID n \<transport\>") and a nested-line parser
(`parseNodeIDLine`, "nodeid: N: \<state\>"); `RingStatus.Faulty` for a knet link is derived
permissively (state != "localhost" and state != "connected" => faulty), following the exact
philosophy this file's own `RingStatus.Faulty` doc comment already established for the older
shape. The real FAULTY-state wording was **not** live-observed this session either (option (b) from
the candidate-fix list, not (a) — no corosync link disruption was attempted against the live
cluster) — still open, tracked in `needs-hardware-validation.md`.

New tests use the exact captured text above as fixture
(`TestParseCorosyncStatus_RealKnetOutput_Healthy`, `internal/host/corosync_test.go`) and prove the
new parser returns one correctly-populated, non-faulty `RingStatus` where the old parser returned
`(nil, nil)`. Confirmed on real hardware, not just the unit test: after deploying the fix
(`4.0.0+29+g947a709+dirty`) to both nodes, a fresh `corosync-cfgtool -s` capture from each
(byte-identical to the text above, own IP/order swapped) was fed through the exact deployed
`ParseCorosyncStatus` code (via a temporary `go run` against the module, removed after) —
```
pvecube-corosync.txt: rings=[{Addr:192.168.1.9 StatusText:nodeid 1: localhost; nodeid 2: connected RingID:0 Faulty:false}] err=<nil>
pve001-corosync.txt:  rings=[{Addr:192.168.1.7 StatusText:nodeid 1: connected; nodeid 2: localhost RingID:0 Faulty:false}] err=<nil>
```
— one ring each, correctly populated, `Faulty:false`, where the pre-fix code returned zero rings.
Also corrected: `internal/pvemock/corosync_render.go`'s mock renderer was (incorrectly) commented
as rendering "knet transport" while actually rendering the older `RING ID` shape — the comment now
says so plainly (CLAUDE.md's "a fixture's job is to match what pvecube says" — rendering the real
knet shape too is a further follow-up, since it needs `CorosyncSpec`/`RingSpec` to model per-peer
connection state, not just one `Faulty` bool per ring).

### 2.2 `vnprox@pve!daemon`'s PVE token is a single, cluster-wide credential — regenerating it on one node silently breaks every other node

**Severity: high, already fixed by hand this session.** `vnprox@pve!daemon` (the PVE API token
`packaging/bin/vnprox-setup` provisions) is **cluster-wide** (PVE stores tokens on the user, not
per-node), but each node's `/etc/vnprox/keys/pve-token` is a local, independent copy of the
secret. Regenerating the token on one node (`pveum user token remove vnprox@pve daemon &&
vnprox-setup` — the exact remediation `vnprox-setup` itself prints when it finds a token file
missing/stale) silently invalidates every OTHER node's on-disk copy, with **no detection or
warning from either `vnprox-setup` or the running daemon**.

Diagnosed via `pveproxy`'s own log line `authentication failure: invalid token value!`, isolated
by creating throwaway tokens and testing auth directly with curl, resolved by copying pvecube's
current valid token file onto pve001 byte-for-byte.

**Candidate fix directions, not implemented (out of this session's authorized code-fix scope,
which was only the certs nil-panic):**
- `vnprox-setup` could detect "token already exists for this user" vs. "token file present
  locally" as two independent facts and warn explicitly when they disagree, rather than treating
  a missing local file as license to regenerate.
- `docs/deployment.md`'s "repeat this setup on every other cluster node" guidance should say,
  explicitly, that `vnprox-setup` must never be re-run with `pveum user token remove` on a node
  that isn't the very first one, and that a lost/corrupted token file on node N should be
  recovered by copying a *known-good* node's file, never by regenerating.
- The daemon could detect an authentication failure against its own configured token and log at
  ERROR with wording that names "this token may have been regenerated on another cluster node,"
  rather than the current graceful-but-generic PVE-unreachable degradation.

**Fixed (2026-08-18, same-day follow-up session), all three candidate directions above taken.**
`vnprox-setup` gained `cluster_peer_count()` (tries `pvecm status`'s `Nodes:` line, falls back to
counting `ring0_addr:` entries in `/etc/pve/corosync.conf`) and now distinguishes the two real
scenarios in its "token already exists but the local file is missing/empty" branch: 2+ detected
cluster peers gets an explicit "copy the token file from a known-good node, do NOT regenerate"
recommendation (with `pveum user token remove ... && vnprox-setup` demoted to an
accept-the-consequences-explicitly last resort); an apparent single/first node gets the original
"regeneration is safe here" guidance, with a caveat for the case detection itself is wrong. Traced
by hand against both real nodes' current state without executing destructively (both already have
matching, valid tokens from this session's earlier by-hand fix, so the branch that would exercise
this code path is not naturally reachable without deliberately breaking a working install) —
`cluster_peer_count`'s own two detection paths were each run for real against both live nodes
confirming they report `2` correctly:
```
$ ssh root@pvecube.localdomain "pvecm status 2>/dev/null | awk -F': *' '/^Nodes:/ {print \"[\" \$2 \"]\"}'"
[2]
$ ssh root@pvecube.localdomain "grep -c 'ring0_addr:' /etc/pve/corosync.conf"
2
```
`docs/deployment.md` updated at its step-5 line (token creation), its step-8 line (now flags that
the SSH multi-node rollout does NOT copy the token file automatically — a real, separate gap found
while writing this fix, left open below), and its "Manual install (per node)" section, all stating
the cluster-wide/first-node-only rule explicitly. `internal/collect.recordResult`
(`internal/collect/collector.go`) now logs a second, distinct WARN line naming "this token may
have been regenerated on another cluster node" specifically for `*pve.ErrPVEAuth` (401) failures,
leaving the existing generic "poll failed" line unchanged for every other failure class (unit
tests: `TestRecordResult_PVEAuthFailureLogsRegenerationHint`,
`TestRecordResult_NonAuthFailureOmitsRegenerationHint`, `internal/collect/authhint_internal_test.go`).
Real-cluster token files confirmed byte-identical (same sha256) before and after this session's
`.deb` redeploys to both nodes, and no peer-auth 401s were introduced by the deploy (the peer-API
401s observed post-restart are a separate, pre-existing "replayed peer request" pattern, confirmed
present in the journal before this session's changes too — unrelated to the PVE token).

**New gap found, not fixed (out of this fix's stated scope of `vnprox-setup` + docs):**
`packaging/install.sh`'s multi-node SSH rollout (step 8) re-runs `vnprox-setup` on every remaining
node but never copies `/etc/vnprox/keys/pve-token` from the first node — so a fresh multi-node
`install.sh` run today would hit exactly the warn-and-stop path this fix adds (safe, no longer
silent, but still requires a manual token-file copy afterward). Documented in `docs/deployment.md`
step 8's note; fixing `install.sh` itself to `scp` the token automatically is a real, separate
follow-up.

### 2.3 `internal/certs.NewService` nil-pointer panic on a fresh install with no PVE token yet — **fixed in this session**

**Severity: high, fixed and tested.** On pve001's very first cold start (before `vnprox-setup`
had run, `/etc/vnprox/keys/pve-token` didn't exist), vnproxd panicked instead of degrading:

```
panic: runtime error: invalid memory address or nil pointer dereference
github.com/bgovanlu/vnprox/internal/pve.(*Client).do(0x0, ...)
github.com/bgovanlu/vnprox/internal/pve.(*Client).ClusterStatus(0x0, ...)
main.runDaemon.certClusterFacts.func27(...)   cmd/vnproxd/certwire.go:33
internal/certs.(*Service).currentFacts(...)   internal/certs/service.go:168
internal/certs.(*Service).Refresh(...)        internal/certs/service.go:108
internal/certs.NewService(...)                internal/certs/service.go:101
```

**Root cause: Go's typed-nil-interface gotcha, not a missing nil check.** `certClusterFacts`
already had `if src == nil { return nil }` — but `server.go` called it as
`certClusterFacts(sdnPVEClient)` where `sdnPVEClient` is a **concrete** `*pve.Client`, nil when
`setupCollect` fails to build a PVE client (a documented, normal state on a fresh install per
`cmd/vnproxd/collect.go`'s own doc comment). Passing a nil concrete pointer into an
interface-typed parameter (`clusterStatusSource`) produces a **non-nil interface value** — Go's
classic "an interface holding a nil concrete pointer is not itself nil" trap — so the existing
`src == nil` check compiled but never matched. `certs.NewService`'s synchronous first `Refresh()`
then called `ClusterStatus` on the nil `*pve.Client`, and `internal/pve.(*Client).do`'s first
line (`c.auth != nil`) dereferences the nil receiver.

**Fix:** `cmd/vnproxd/certwire.go` gained `certClusterFactsFor(client *pve.Client)
certs.ClusterFactsFunc`, which nil-checks the **concrete** pointer before ever boxing it into the
interface, mirroring the collector's own established "check the concrete pointer, not the
interface" discipline (`setupCollect`'s `if sdnPVEClient != nil { ... }` pattern used throughout
`server.go`). `server.go`'s one call site now reads `Facts: certClusterFactsFor(sdnPVEClient)`
instead of `certClusterFacts(sdnPVEClient)` directly.

**Tested:**
- `internal/cmd/vnproxd/certwire_test.go` (new): `TestCertClusterFactsFor_NilClientDegradesGracefully`
  builds a real `certs.Service` with `certClusterFactsFor(nil)` exactly as `server.go` does and
  proves the synchronous first `Refresh()`/`Preflight()` do not panic.
  `TestCertClusterFactsFor_TypedNilPVEClientGotcha` documents the Go pitfall directly: boxing a
  nil `*pve.Client` into the bare interface produces a non-nil interface value, proving
  `certClusterFacts`'s own guard is structurally unable to catch this case (which is why the fix
  lives at the caller, not inside `certClusterFacts`).
- Confirmed the regression is real by temporarily reverting `server.go`'s one-line call-site
  change (`git stash`) and rebuilding — `certClusterFactsFor` alone (unused) still compiles, and
  the reverted code is the exact code that crashed on pve001; restored immediately after.
- `go build ./...`, `go vet ./...`, `go test ./internal/certs/... ./cmd/vnproxd/...`,
  full `go test ./...` (every package `ok`), `gofmt -l .` (empty) — all green.
- **Not yet redeployed to either node** in this session (both nodes are already past first-run
  and have valid tokens, so the crash can't be reproduced live without wiping a node's token
  file, which is destructive and out of this card's scope) — verified by unit test and by
  reading the exact crashing call chain, not by re-triggering the crash on hardware.

**Confirmed still intact (2026-08-18, same-day follow-up session).** `cmd/vnproxd/certwire.go`'s
`certClusterFactsFor` and `server.go`'s call site are unchanged by this follow-up session's four
other fixes; `certwire_test.go`'s two tests still pass, and both nodes now run a `.deb` built from
a tree that includes this fix (`4.0.0+29+g947a709+dirty`), active and healthy on both — still not
re-triggered via a real fresh-install repro (still destructive, still out of scope), but the fix
has now shipped to real hardware without incident, which a purely-unit-tested fix had not yet
done as of the original session.

### 2.4 `pve_privileges` under `--live` fails on every correctly-provisioned install, by design mismatch

**Severity: medium — a real false-positive, first observed because `--live` had never actually
been run with a valid token before this session.** `vnprox-setup` deliberately provisions
`vnprox@pve!daemon` with only `Sys.Audit,VM.Audit,SDN.Audit,Mapping.Audit` — read-only,
auditor-level, exactly as `docs/deployment.md:59` documents ("Creates the read-only PVE API
token `vnprox@pve!daemon` (privilege: auditor-level on `/`)"). This is intentional: writes go
through the applying user's own sealed PVE ticket (`docs/security.md`'s "Apply-time revert
ticket" section), never through the daemon's own token.

`internal/doctor`'s `pve_privileges` check (`checkPVEPrivileges`, `internal/doctor/checks.go:366`)
compares this same read-only token against `internal/auth.RequiredPrivileges()` — a list that
includes `Sys.Modify`, `SDN.Allocate`, and `VM.Config.Network` as **required, non-optional**.
Since `vnprox-setup` never grants those to this token by design, `--live`'s `pve_privileges`
check will `fail` (a real, gating, non-zero-exit result — `Report.Failed()` counts fails, not
warns) on every node set up exactly as documented:

```json
{"check":"pve_privileges","status":"fail","detail":"the configured PVE token is missing privileges vnprox needs: Sys.Modify (...); SDN.Allocate (...); VM.Config.Network (...)"}
```
Observed identically on both pvecube and pve001. `RequiredPrivileges()`'s own doc comment
describes it as "the privileges vnprox's capability mapping actually consults" — i.e. what a
fully-capable *operator* needs, via `DeriveCapabilities`, for the UI's write features to work —
not what the daemon's own deliberately-read-only polling identity needs. `checkPVEPrivileges`
reuses the same list for both purposes, so a documented-correct install (per `vnprox-setup`'s own
provisioning quoted above) permanently fails a check that is supposed to catch misconfiguration. Not fixed this
session (a design question, not a one-line bug — flagged per CLAUDE.md rather than
re-litigated); candidate fix directions: split `RequiredPrivileges()` into an "operator" list and
a "daemon token" list, or have `checkPVEPrivileges` only warn (not fail) on the three
write-privilege entries when checking the daemon's own token specifically.

(To get a bearer token for this and the doctor run in §1.3 without touching `root@pam`: a
throwaway PVE user `vnproxt3201@pve` with the `PVEAuditor` role was created, used to mint two
audit-scoped vnprox tokens — one per node — then both tokens revoked and the PVE user deleted
immediately after. No trace left on either cluster.)

**Fixed and confirmed (2026-08-18, same-day follow-up session) — the first candidate direction
above was taken.** `internal/auth.DaemonTokenPrivileges()` is a new, independently-declared list
(`internal/auth/caps.go`) mirroring `vnprox-setup`'s actual `VNPROX_PVE_PRIVS` grant
(`Sys.Audit,VM.Audit,SDN.Audit,Mapping.Audit`) byte-for-byte — not derived by filtering
`RequiredPrivileges()`, since `VM.Audit`/`Mapping.Audit` are privileges the daemon's token needs
that `RequiredPrivileges()`/`DeriveCapabilities` never consult at all (neither backs a UI
capability flag). `checkPVEPrivileges` (`internal/doctor/checks.go`) now gates on this new list
instead. `TestDaemonTokenPrivilegesMatchesSetupGrant` (`internal/auth/caps_test.go`) reads
`packaging/bin/vnprox-setup`'s `VNPROX_PVE_PRIVS=` line directly (the same "read the real source,
not a second hand-written copy" discipline `TestRequiredPrivilegesCoversMapping` already applies
to `caps.go`'s own mapping table) so the two cannot silently drift again.
`TestCheckPVEPrivileges_DaemonTokenGrant` (`internal/doctor/checks_test.go`) proves a token
holding exactly those four privileges passes, and that dropping any single one of the four fails
by name.

**Confirmed on real hardware, not just the unit tests**, using the exact same throwaway-account
technique as the original session (a new PVE user, `vnproxdoc@pve`, `PVEAuditor` role; a
vnprox-scoped bearer token minted per node; both tokens revoked and the user deleted immediately
after — zero trace left). After deploying the fix (`.deb` `4.0.0+29+g947a709+dirty`) to both
nodes:
```
$ VNPROX_TOKEN=... vnproxctl doctor --live -o json   # pvecube
{"check":"pve_reachable","status":"pass", ...}
{"check":"pve_privileges","status":"pass","detail":"the PVE token holds every privilege vnprox's own daemon needs"}
```
Byte-identical result on pve001 (`8 passed, 0 warned, 0 failed, 2 skipped` on both — the same
`clock_skew`/`peer_secret` `skip`s the original session already confirmed are a still-missing-code
gap, unrelated to this fix). `pve_privileges` now `pass`, not `fail`, on both real nodes' own
actually-provisioned tokens; `pve_reachable` unaffected.

### 2.5 `collect: peer host poll failed... context canceled` — real, frequent (≈50% of attempts), root-caused to TCP-level retransmission on the shared keep-alive connection, not a timeout budget

**Severity: low (already degrades gracefully — "keeping last-known state" — but genuinely
reproducible and previously unobservable).** Every ~5s, `hostPollOnce`'s outbound
`GET /api/peer/host/links` call to the peer intermittently fails:
```
{"level":"WARN","msg":"collect: peer host poll failed, keeping last-known state","node":"pve001","peer_addr":"192.168.1.7:8007","error":"host links (pve001): context canceled"}
```
Over a sampled hour: **261 failures against 224 successes for the SAME outbound call** — this is
not rare, it's closer to a coin flip. The tiny, 1s-cadence `GET /api/peer/firewall/log` call over
the same connection to the same peer **never** shows this failure in the same window.

Investigation, in order:
1. **Not a runGroup-wide cancellation.** `RunHostLoop`/`RunPVELoop`/`RunLLDPLoop` share one
   `subCtx` (`cmd/vnproxd/rungroup.go`), but a shared-context cancel would stop every loop, not
   just one call every 5s while everything else (peer round trips, the HTTP server) keeps
   running for hours. Ruled out.
2. **Text is literally `context.Canceled`, not `context.DeadlineExceeded`.** `internal/peer/client.go`'s
   `c.do` builds `reqCtx` via `context.WithTimeout(ctx, c.opts.RequestTimeout)` **and** the
   `http.Client` itself is constructed with `Timeout: opts.RequestTimeout` (`client.go:139`) — the
   *same* duration set two ways. `http.Client.Timeout`'s own internal mechanism cancels via a
   timer-driven `context.WithCancel`, not `WithDeadline`, which is why an exceeded budget through
   *that* path reads as `"context canceled"` rather than `"context deadline exceeded"` — a
   well-known Go footgun, and consistent with the literal text observed.
3. **Tight timing correlation with a genuinely fast server-side response, not a slow one.**
   Correlated one failure/success pair by wall clock across both nodes: pve001's server answered
   the exact request in `duration_ms: 1` at `09:25:15.160144336`, essentially simultaneous
   (~0.67ms) with pvecube logging the client-side cancellation at `09:25:15.160811795`. The
   server was never slow; the client gave up at (or very near) whatever it thought "wait budget
   exhausted" meant, right as a fast answer arrived.
4. **Real TCP-level retransmission confirmed on the shared connection**, via `ss -tin` on the
   live long-lived socket:
   ```
   ESTAB 0 0 192.168.1.9:39030 192.168.1.7:8007
   rtt:11.578/16.715 ... retrans:0/9 dsack_dups:9 bytes_retrans:350 ...
   ```
   9 duplicate SACKs and 350 retransmitted bytes on one connection — real, even though raw ICMP
   ping between the two hosts is a clean 0.2ms (`ping -c5 192.168.1.7` → 0% loss,
   `rtt avg 0.218ms`), so this is not a saturated or lossy physical link in the simple sense.
5. **Payload-size correlation.** `/api/peer/host/links` responses are 11–30 KB (many TCP
   segments); `/api/peer/firewall/log` responses are 31–32 bytes (one segment). The failure
   pattern tracks the larger, less-frequent transfer exactly, consistent with occasional
   segment loss/retransmission on a connection whose congestion window resets between the
   sparser large requests (Linux's default `tcp_slow_start_after_idle` behavior) mattering far
   more for a multi-segment transfer than a single-packet one.

**Root-cause confidence: high but not 100% packet-capture-confirmed.** The evidence in points
2–5 above is a coherent, internally-consistent explanation (double-timeout-mechanism footgun +
real, measured TCP retransmission disproportionately hitting the larger/rarer transfer), and was
reached without needing a code change. What would fully close it: a `tcpdump` capture bracketing
a live failure, or (cheaper) removing the redundant double-timeout — `internal/peer/client.go`
sets *both* `http.Client.Timeout` and its own `context.WithTimeout` to the identical
`RequestTimeout`; picking one (the explicit context deadline is the more debuggable of the two,
since it produces the honest `"context deadline exceeded"` string) would at minimum make the
*next* occurrence's log line tell the truth about which budget actually fired, and is a small,
low-risk candidate fix for whoever picks this up — not made in this session (outside the
authorized code-fix scope).

### 2.6 `ping` fails under vnproxd's own shipped systemd hardening (`cap_set_proc: Operation not permitted`) — breaks `internal/mtuprobe` AND `internal/latmesh` identically, causing false `path_loss`/`path_latency_degraded`/MTU findings on every hardened install

**Severity: high — root-caused definitively, not fixed.** `internal/mtuprobe`'s DF-probe floor
check reported the real corosync ring0 path (`pvecube` → `pve001`, real IP `192.168.1.7`) could
not carry even 552 bytes, every ~5 minutes, sustained:
```
{"level":"WARN","msg":"mtuprobe: path could not carry even the minimum MTU, keeping prior reading","linkId":"corosync:ring0|pvecube->pve001","minMtu":552}
```
Running the exact same command by hand, as root, over SSH succeeded cleanly every time
(`ping -M do -c3 -W2 -s524 -- 192.168.1.7` → 0% loss, `rtt avg 0.498ms`) — the daemon's own
probe of the identical target failed while an identical manual invocation did not.

**Root cause, confirmed directly.** Built a debug build of `internal/mtuprobe.dfProbe` that logs
the exact target/argv/exit-error/raw-output to stderr on every probe, deployed it to pvecube only
(binary swap + `systemctl restart`, reverted immediately after capturing one data point — no
diff left in the tree, confirmed via `git status`/`git diff`), and captured the daemon's own
`ping` invocation failing with:
```
TEMP-T-3201-DEBUG dfProbe target="192.168.1.7" size=552 payload=524 err=exit status 255 text="ping: cap_set_proc: Operation not permitted\n"
```
Modern iputils-ping (Debian's shipped version) opens its raw ICMP socket, then calls
`cap_set_proc()` to drop capabilities it no longer needs as its own defense-in-depth measure —
and **aborts hard, exit 255, if that call itself fails**, even though the raw socket it actually
needs was already opened successfully. `cap_set_proc()` requires `CAP_SETPCAP`, and
`vnprox.service`'s own `CapabilityBoundingSet=` (`cmd/vnproxd`'s shipped systemd unit) is
```
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_DAC_OVERRIDE CAP_DAC_READ_SEARCH CAP_CHOWN CAP_FOWNER
```
— **no `CAP_SETPCAP`.** `CapabilityBoundingSet=` bounds every process in the unit's scope,
including any child `ping` it execs, so `ping`'s own privilege-drop can never succeed under this
daemon's own shipped hardening, on any node, always. My manual reproduction succeeded only
because I ran `ping` directly over SSH, entirely outside `vnprox.service`'s systemd scope — a
different execution environment than the daemon ever runs `ping` in, which is exactly why it did
not reproduce manually and is exactly the kind of gap this card's "you have real root access,
observe the real thing" mandate exists to catch.

**This is not `internal/mtuprobe`-specific — `internal/latmesh.RealProber` (the RTT/loss
mesh backing `path_loss`/`path_latency_degraded` findings, and `internal/wan`'s own reference-target
probing) shells out to `ping` the identical way** (`internal/latmesh/prober.go:89`,
`exec.CommandContext(ctx, "ping", "-c", ..., "-W", ..., "--", target)` — no `-M do`, but the same
binary, the same systemd scope, the same missing `CAP_SETPCAP`). This session independently
observed a real, currently-firing finding that is very likely the SAME root cause manifesting a
different way:
```
{"level":"INFO","msg":"findings: notification delivered","target":"mail-to-root","finding_id":"health:path_loss|corosync:ring0|pvecube->pve001","severity":"warning","transition":"new"}
```
fired twice during this session, for a link independently confirmed completely healthy (0.2ms
ICMP RTT, 0% loss, manually). **If `internal/latmesh.parsePingSummary` treats a hard exec failure
(exit 255, `cap_set_proc` error text) the same way it treats a real "0% received"/timeout result,
every latency-mesh probe on every hardened vnprox install is reporting 100% loss for links that
are actually perfectly healthy — a false positive on the product's own headline continuous-health
feature, on every node running the daemon exactly as packaged.** This was not independently
re-verified with its own debug capture in this session (budget went to the DF-probe`/mtuprobe`
side first) — flagged as very likely the same cause, not fully confirmed for `latmesh`
specifically, and belongs at the top of whoever picks this up next.

**Not fixed this session** (outside the authorized code-fix scope — this is a systemd-unit
security-hardening tradeoff, not a Go bug, and deserves its own considered review rather than a
reflexive `CAP_SETPCAP` addition). Candidate fix directions, roughly in order of how surgical
they are:
- Add `CAP_SETPCAP` to `vnprox.service`'s `CapabilityBoundingSet=` — the direct fix, but widens
  the bounding set with a capability that (per the unit's own extensive comments on why each
  capability is present) grants the ability to pass capabilities to further children; needs a
  security-review pass, not a one-line change, given `docs/security.md`'s "Host footprint"
  section explicitly measured every capability currently listed against real syscall paths.
- Have `internal/mtuprobe`/`internal/latmesh` detect this exact failure shape (`exit status 255`
  + `"cap_set_proc"` in the output) and report it distinctly (a real, actionable "ping cannot run
  under this daemon's own sandboxing" diagnosis) rather than folding it into "path unreachable" —
  mirroring `dfProbe`'s own existing "Frag needed"/"Message too long" honest-classification
  pattern for a different negative-result shape.
- Confirm whether Debian/PVE ships an iputils-ping build old enough to predate the
  `cap_set_proc()` privilege-drop-or-abort behavior on some supported PVE version, in which case
  this may be version-dependent rather than universal — not checked this session.

**Ruled out along the way** (kept for whoever continues this, so the same dead ends aren't
re-walked): raw-ICMP capability presence (`CAP_NET_RAW`/`CAP_NET_ADMIN` ARE present),
`RestrictAddressFamilies` (`AF_INET` is allowed), `SystemCallFilter` (`socket()` is in
`@network-io`, part of the allowed `@system-service`), a context-deadline mismatch
(`mtuprobe.Service.Tick` passes the unmodified long-lived loop `ctx` straight through), and a
bare-hostname DNS resolution gap (real and separately worth fixing — `getent hosts pve001` is
empty on pvecube — but not the cause of *this* failure, since `discoverCorosyncPairs` populates
`ToAddr` with the real IP from `corosync.conf`, never reaching the by-name fallback).

**Correction (2026-08-18, same-day follow-up session): the `SystemCallFilter` ruling-out above
was incomplete, not wrong about what it checked — it only checked `socket()`, and that genuinely
is allowed. It never checked `capset()`, the syscall `cap_set_proc()` actually makes, which turned
out to be the other half of this bug** — see the fixed-and-confirmed writeup immediately below.

**Fixed and confirmed on real hardware (2026-08-18, same-day follow-up session) — CAP_SETPCAP
alone was NOT sufficient, the first deploy attempt this session proved it live.** Added
`CAP_SETPCAP` to `vnprox.service`'s `CapabilityBoundingSet=` (the first candidate fix direction
above), built a `.deb`, deployed it to both nodes, confirmed the capability landed
(`cat /proc/<pid>/status` on the live daemon: `CapPrm`/`CapEff`/`CapBnd` all include
`cap_setpcap`) — and the exact same `mtuprobe: path could not carry even the minimum MTU` warning
fired again five minutes later anyway. Root-caused with a second debug-build capture (identical
technique to the original session's, reverted the same way): the daemon's own `ping` invocation
was *still* failing, just five minutes after a restart that had `CAP_SETPCAP` correctly in its
bounding set.

**The real, complete root cause: `vnprox.service`'s `SystemCallFilter` denies `@privileged`, and
`capset` — the actual syscall `cap_set_proc()` makes — is a member of that denied group**
(confirmed directly: `systemd-analyze syscall-filter @privileged | grep capset` lists it). This
seccomp filter is inherited across exec by every child process, the same mechanism the T-608
`AF_UNIX`/`lldpctl` finding already established for `RestrictAddressFamilies` — so `capset(2)` was
blocked at the syscall level regardless of which capabilities the calling thread held.
`CapabilityBoundingSet` and `SystemCallFilter` are two independent gates; this bug needed both
addressed, and the original session's "ruled out" pass only checked the syscall filter's effect on
`socket()`, not on `capset()`.

Diagnosed and fixed via a `systemd-run` reproduction ladder against the real unit's exact
directives, one addition at a time, live on pvecube:
```
# CAP_SETPCAP present, SystemCallFilter denying @privileged (matches the first, insufficient fix):
$ systemd-run --uid=0 --pipe -p NoNewPrivileges=yes -p CapabilityBoundingSet="...cap_setpcap..." \
    -p "SystemCallFilter=@system-service" \
    -p "SystemCallFilter=~@mount @reboot @swap @privileged @resources @raw-io @debug @module @cpu-emulation @obsolete @clock" \
    -p SystemCallErrorNumber=EPERM -- ping -M do -c3 -W2 -s524 -- 192.168.1.7
/usr/bin/ping: cap_set_proc: Operation not permitted

# + SystemCallFilter=capset (allow capset specifically after the @privileged deny):
...same directives, plus -p "SystemCallFilter=capset" ...
/usr/bin/ping: setuid: Operation not permitted   # progressed — iputils-ping's privilege-drop
                                                  # sequence also calls setuid() as a second,
                                                  # UID-level defense-in-depth step after capset()

# + SystemCallFilter=capset setuid:
...
3 packets transmitted, 3 received, 0% packet loss, time 2067ms   # succeeds cleanly
```
A quick bisection (trying `setgid`/`setresuid`/`setresgid`/`setgroups` individually and in
combination) confirmed `capset setuid` is the *minimal* addition — none of the GID-related
syscalls were needed (they turned out to already be outside `@privileged`, allowed via
`@system-service` already, consistent with `systemd-analyze syscall-filter` output).

`packaging/systemd/vnprox.service` now carries both fixes together: `CapabilityBoundingSet=` gains
`CAP_SETPCAP`, and a new `SystemCallFilter=capset setuid` line re-permits exactly those two
syscalls after the `@privileged` denial — not a re-opening of the whole group (which also holds
`mount`/`pivot_root`/`reboot`/module-loading syscalls vnproxd has no documented need for). Neither
addition widens what a compromised child could do: `capset` only rearranges bits within a
process's own already-held capability sets (the `CapabilityBoundingSet` ceiling is unchanged), and
`setuid` without `CAP_SETUID` in the bounding set (deliberately still absent) can only set a
process's UID to one it already holds — a no-op for every vnproxd child, which stays at UID 0
throughout. Both `packaging/systemd/vnprox.service` and `docs/security.md`'s Host footprint
section carry the full reasoning inline, dated and attributed per this repo's existing
capability-correction convention (the T-608 `AF_UNIX` precedent).

**Confirmed on the real, restarted daemon itself, not just the `systemd-run` reproduction.** After
redeploying the corrected `.deb` to both nodes and restarting: `systemctl show vnprox.service -p
SystemCallFilter` on both lists `capset`/`setuid` among the allowed syscalls; a `systemd-run`
invocation built entirely from the live unit's own reported `CapabilityBoundingSet`/
`SystemCallFilter` values succeeds on both nodes; and the real, running daemon's own `GET
/latmesh/heatmap` shows the most recent real sample at `lossPct: 0` (`rttMs: 0.298`) for
`corosync:ring0|pvecube->pve001`, with `rollingLossPct` visibly recovering from the pre-fix
100%-loss samples still aging out of the 5-minute rolling window at the moment of capture — no
further `mtuprobe: path could not carry even the minimum MTU` warning logged in the observation
window either. See this file's evidence index / the session's final report for the fully-settled
post-window numbers.

**§3's open item on `internal/latmesh`'s exposure is now independently confirmed, not just
inferred by code similarity**: this session's redeploy triggered fresh `health:path_loss`
`"transition":"new"` notifications for `corosync:ring0` **and all four `guest:vmbrN` fabrics** on
pve001 immediately after the (pre-fix) restart, and `internal/latmesh/prober.go`'s
`parsePingSummary` was confirmed by direct code reading to fall through to `Reading{LossPct: 100}`
for a hard `cap_set_proc`/`capset` exec failure (non-empty output, so the `len(out)==0` exec-error
branch is never reached; no `"packet loss"` line to match, so `lossFound` stays `false`) — the
exact same false-positive mechanism as `mtuprobe`, now proven end-to-end on real hardware rather
than inferred from shared code shape.

A bare PVE node name (`"pve001"`, `"pvecube"`) also does not resolve via DNS on either host
(`getent hosts pve001` → empty; `ping pve001` → `Name or service not known`) — a genuine,
separate hazard for `internal/mtuprobe`'s/`internal/latmesh`'s documented fallback ("dial
`pair.ToNode` by name when `ToAddr` is empty") on any pair that hits it, found while tracing this
finding but not its cause (this pair's `ToAddr` is the real IP from `corosync.conf`, never
reaching that fallback) — worth its own fix but not folded into the `CAP_SETPCAP` finding above.

---

## 3. Still genuinely blocked — needs T-3202, T-3203, or more hardware

This is the honest boundary: what T-3201 did NOT prove, and why two real nodes still isn't
enough.

- **Failure injection / commit-confirm self-heal on real hardware** — explicitly T-3202's job.
  Nothing in this session broke connectivity mid-apply or watched a real rollback timer fire; the
  product's headline safety guarantee is still validated only against `internal/pvemock`.
- **Distributed rollback timers, cross-node coordination under partial failure** — needs
  failure injection (T-3202), not just two healthy nodes.
- **Federation transport** — not exercised this session; no federation target was configured
  against either node. Left for a future card scoped to it.
- **Scale & performance on real cluster data** — T-3203's job; this cluster's dataset (2 nodes,
  handful of guests, no real production traffic volume) says nothing about the T-1808→T-1907
  threshold question.
- **HA lease fencing, node join/leave, 3+-node quorum behavior** — this is a 2-node cluster with
  no HA resources configured; anything needing a third node or a real HA group is still
  unobservable. `internal/pvemock`'s multi-node fixtures remain the only tested surface for
  quorum-loss/partition scenarios.
- **SDN Fabrics/Controllers/IPAM convergence** (`T-3101`/`T-3102`/`T-3104`'s own
  `needs-hardware-validation.md` entries) — explicitly NOT attempted this session. Configuring a
  real fabric/controller/IPAM instance is a mutating PVE SDN change, outside T-3201's read-mostly
  validation scope (T-3201's brief did not include SDN object configuration) and outside "stay
  inside vnprox's own daemon" for this card. These remain open, still filed under the `T-3201`
  pointer in `needs-hardware-validation.md`, genuinely waiting on a future card that specifically
  works with SDN config on real hardware.
- **§2.5's peer-poll `context canceled` root cause** — high-confidence hypothesis, not
  packet-capture-confirmed. Needs `tcpdump` bracketing a live occurrence, or the double-timeout
  simplification proposed above, to fully close.
- **A real fresh-install repro of the certs nil-panic (§2.3)** — the fix was verified by unit
  test and by reading the exact crashing call chain, not by re-triggering the crash on hardware
  (both nodes are already past first-run with valid tokens; wiping a token file to re-trigger it
  would be destructive and out of scope).

**Resolved this follow-up session (2026-08-18), removed from this list**: §2.1's corosync-knet
parser gap, §2.4's `pve_privileges` false-fail, and §2.6's `CAP_SETPCAP`+`SystemCallFilter`
ping/`internal/latmesh` false-positive exposure are now fixed and confirmed on real hardware — see
each section above for the evidence. Two narrower items surfaced *by* fixing them, still open:

- **§2.1's real knet FAULTY-state wording** — still not hardware-observed (no corosync link
  disruption was attempted against the live cluster this session either); the new parser's
  permissive default (anything other than `localhost`/`connected` counts as faulty) is design, not
  confirmed wording. A future session with the same live cluster and appetite for a brief,
  monitored corosync disruption could close this.
- **§2.2's `install.sh` multi-node token gap** — found while writing the `vnprox-setup` fix, not
  itself fixed (out of this fix's stated scope): the SSH-based multi-node rollout re-runs
  `vnprox-setup` on every remaining node without ever copying the first node's
  `/etc/vnprox/keys/pve-token` over, so a fresh multi-node `install.sh` run today would hit (now
  safely, not silently) the same warn-and-stop path this session's fix adds, requiring a manual
  follow-up copy. Documented in `docs/deployment.md`; `install.sh` itself is unchanged.

---

## Evidence index

Every command/log excerpt quoted above was run live against `pvecube.localdomain` (192.168.1.9)
and/or `192.168.1.7` (pve001) during this session (2026-08-18), via root SSH, and is reproducible
by re-running the same command. No file under `planning/reports/evidence/` was added this session
— all evidence above is inline (`journalctl`/`openssl`/`ping`/`ss`/`corosync-cfgtool` output and
one temporary, since-removed Go test), per this card's "paste real command output into the
report" instruction rather than a separate blob directory (unlike T-1802's evidence-blob
convention, which this card does not extend — no code change here needed a new evidence-blob
test).
