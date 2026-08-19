# vnprox — product datasheet

**Visual networking for Proxmox VE**
Version 4.0.0 · released 2026-08-14

*Figures below are counted from the tree at v4.0.0 plus the unreleased Phase 29 fixes
(`CHANGELOG.md` §Unreleased), and each names the file it can be recounted from. The 2026-08-15
documentation audit found several of them had gone stale by a whole arc; they are now recounted
rather than carried forward.*

---

## What it is

vnprox is a single-binary add-on installed on every Proxmox VE node. It draws your cluster's network as it actually is, and lets you change it through a staged workflow that undoes itself if the change cuts you off.

**Proxmox VE remains the source of truth.** vnprox reads PVE, shows you what is there, and writes back through the same API and files you would edit by hand. If vnprox stops, your network is unaffected — you have lost an editor, not a configuration.

---

## The safety model

Every network change follows the same five steps, with no path around them — the same engine serves the UI, the API, the CLI, plugins, and AI assistants.

```
   stage  ─→  validate  ─→  diff  ─→  apply  ─→  confirm
     │           │            │         │           │
  drawer     5 classes     files +    ordered    or it rolls
  collects   of checks      plan       plan       itself back
```

| Guarantee | How it holds |
|---|---|
| **Nothing applies as you click** | Edits accumulate in a drawer; discarding costs nothing because nothing was written |
| **Cut yourself off and it undoes itself** | Confirmation needs an authenticated round-trip. Can't confirm ⇒ server-side rollback at the deadline (default 120 s, 30–600 s configurable) |
| **You cannot click through an interlock** | Changes that would take out a management IP, a corosync link, or a bridge with running guests are hard errors with no override |
| **Management-path changes cost more ceremony** | Typed node-name acknowledgement, and a confirm window that cannot go below 180 s |
| **A node lost mid-apply recovers alone** | Each node arms its own local timer; safety never depends on one node reaching another |
| **Every applied change is snapshotted first** | That is what makes rollback possible; manual rollback stays available for 7 days |
| **AI can read and draft, never apply** | 9 MCP tools, none mutating — enforced by a guard test that panics if a mutating tool is added |
| **A plugin can stage, never apply** | Declared capability scope is a ceiling the installer enforces |

---

## Capabilities

### See

- **Topology map** — the whole cluster, physical → L2 → SDN → guests. Opens on **Switch view** (a faceplate rendering of your real gear, one appliance per bridge); **Graph view** adds drag-and-drop editing, path overlays, and paint modes.
- **Entity inspector** — every object clickable: configuration, live kernel state, LACP actor/partner detail, sparklines, and the raw config behind it.
- **Guest interior** (opt-in, read-only) — a guest's own view of its network, cross-checked against IPAM rather than taken at face value.
- **Physical discovery** — LLDP switch name and port per NIC, drawn on the map. MAC/FDB browser for "which port is this MAC behind".
- **Paint modes** — traffic heat, latency/loss heatmap (per address family), simulated path, backup path to PBS.
- **Search** — fuzzy across names, MACs, IPs, VMIDs and comments, spanning every attached cluster.

### Understand

- **Path simulator** — "can A reach B on this port, and if not what stops it?" Four verdicts including an honest **indeterminate**; a deny names the exact blocking rule, an unreachable names the missing link. **Verify live** runs a real probe inside a guest and shows it *beside* the simulation, never as a correction.
- **Diagnosis ladder** — starts from a symptom and walks physical → L2 → L3 → firewall, reporting pass, fail, or "couldn't determine" at each rung.
- **Findings stream** — 15 sources, 43 checks: health, drift, LLDP mismatch, IPAM conflicts, rogue-service detection, certificates, WAN, capacity, baseline anomalies. Plain-English explanation, affected objects as map links, and a stageable fix where one is computable.
- **Flow explorer** — sFlow/NetFlow/IPFIX plus host-local conntrack sampling, attributed to a **service class** (migration, backup, Ceph public/cluster, corosync) from metadata alone — never payload inspection — and to Kubernetes services where a cluster is registered.
- **Microsegmentation planner** — proposes the minimal firewall policy covering a guest's observed-good traffic, with coverage stated exactly (99.53% reads 99.53%, never "everything") and a mandatory dry-run sorting every flow into four honest buckets before anything can be staged.

### Change

- **Bridges, bonds, VLANs, interfaces** — form editors with inline guidance; OVS supported.
- **Guest NICs** — reattach, retag, rate-limit, firewall flag, connect/disconnect. **Bulk mode** produces one changeset for twenty guests, with per-guest results.
- **SDN cockpit** — zones → VNets → subnets with per-node realization status, and staged-vs-running shown as a real diff rather than an opaque "pending" flag. Guided wizards for Simple, VLAN, QinQ, VXLAN and EVPN, each with a live preview and the MTU arithmetic done for you.
- **Firewall** — datacenter/node/guest as one hierarchy with the resolved evaluation order made explicit; drag-to-reorder rules, macro expansion preview, alias/IPSet/security-group editors with usage tracking; the "datacenter firewall is off" footgun surfaced as a banner.
- **IPAM** — subnets with utilization, a sparse address list that serves a /30 and a /16 without paging, conflict detection (duplicates, squatters, stale records), external subnets, IPv6 planning grid, and two-way NetBox/phpIPAM sync with a dry-run preview.
- **Edge & NAT** — masquerade, port forwards, static routes, SDN SNAT, WAN uplink health.
- **Raw editor** — full `/etc/network/interfaces` editing with linting, still diffed and commit-confirmed. The escape hatch that keeps power users inside the safety envelope.
- **Blueprints** — parameterized topology templates, signature-verified on import, idempotent on instantiation.

### Operate

- **Change review** — summary, per-node file diffs, the ordered plan, and a discussion thread. Comments, approvals (enforced server-side, not by the UI), and a shareable review link that works from a phone.
- **Scheduled apply** — stage now, apply inside a maintenance window, re-validated fresh at fire time. Management-path changes are excluded, unconditionally.
- **History & time machine** — timeline, diff any two points, restore any of them through the normal review flow. Playback scrubs the map through recent history.
- **Audit** — every action, cluster-merged, with AI-originated changes distinguishable by actor.
- **Metrics** — live rates on the map, 24 h history, Prometheus exporter and Grafana panels.
- **Alert rules** — route findings to a webhook or Proxmox's own notification system.
- **Certificates** — cluster-wide TLS inventory with expiry, SAN coverage, and chain verdicts; read from pmxcfs so one node shows the whole cluster, even when peers are unreachable.
- **Packet capture** — bounded, audited, with a guided BPF builder.
- **Backup / restore / support bundle** — integrity-checked archives of vnprox's own state; bundles are secret-redacted.

### Scale out

- **Federation** — attach other PVE clusters for one map, one search, one audit log. A changeset always belongs to exactly one cluster and is rejected if it would reach across the boundary: federation federates views and workflows, **never ownership**.
- **WireGuard interconnect** — guided tunnel setup between clusters.
- **Multi-tenancy** — tenants scoped to guests/VLANs/subnets, where out-of-scope means genuinely invisible (a lookup returns "not found"). Members request; approvers approve; applying stays a separate step. Tenant records themselves were an exception to the invisibility rule until 2026-08-19 (`T-3002-followup-01`); they are now membership-scoped like everything else. Creating a tenant and editing any tenant's scope boundary are fleet-administrator actions (`T-3002-followup-02`), since scope refs are not validated against the caller's own access.
- **High availability** — active/standby pair where **in-flight rollback deadlines survive failover**: a change due to auto-revert at 12:03:30 still does, on the standby, at 12:03:30.
- **Switch config push** — opt-in, enabled twice, limited to LLDP-confirmed PVE-facing ports and to VLAN membership, descriptions and LACP only. Ships with its residual risk stated: a switch made unreachable **cannot be rolled back remotely**.
- **AI operators (MCP)** — read surfaces plus stage-only drafting, capability-token scoped, every action audited with an `mcp:` actor.
- **Plugins** — five extension points (switch drivers, flow ingestors, finding packs, ingress discoverers, dashboard tiles) as sandboxed subprocesses with no access to vnprox's database or files.
- **Embeds** — read-only, token-authenticated views for wikis and NOC screens. You cannot mint a write-capable embed, even as an administrator.

---

## Interfaces

| Surface | Detail |
|---|---|
| **Web UI** | 27 routed screens (`grep -cE 'path="' web/src/App.tsx`), React 18 + TypeScript. Keyboard-driven (`/` search, `⌘K` palette, `g`+key navigation, `F1` help). WCAG AA, axe-gated. Dark by default. Responsive triage layout at phone width. |
| **Online help** | Every screen and panel explains itself in-app; full-text searchable. Coverage is enforced by a build gate, not asserted. |
| **REST API** | **250 operations over 211 paths** (`docs/openapi.json`, generated by walking the router and gated both directions; the generator's fixture config leaves the MCP transport and plugin hub off, so that is a floor). Contract-frozen at v3.0 and additive-only since. Capability-gated from PVE ACLs. |
| **WebSocket** | Live topology deltas, metrics, changeset status, findings, firewall log. |
| **CLI** | `vnproxctl` — status, snapshots, rollback, apply, backup, restore, support-bundle, certs. Several subcommands read state directly and work with the daemon down. |
| **MCP** | 9 read/stage tools for AI assistants. |
| **Metrics** | Prometheus/OpenMetrics on the same TLS listener, bearer-token gated. |

---

## Requirements

| | |
|---|---|
| **Platform** | Proxmox VE 8.2+ (Debian 12) or 9.x (Debian 13) |
| **Architectures** | amd64, arm64 |
| **Install** | One `.deb` per node; single static binary + embedded SPA |
| **Port** | 8007/tcp (UI, API, WebSocket, peer API). Installer offers 8008 if Proxmox Backup Server holds 8007 |
| **Accounts** | None. Authentication is your Proxmox login (realm + 2FA supported); optional OIDC SSO |
| **Permissions** | Derived from your PVE ACLs — a read-only PVE user gets a read-only vnprox |
| **State** | SQLite at `/var/lib/vnprox`, root-owned `0600`. App-owned data only: sessions, changesets, snapshots, audit, layout. Never a shadow copy of PVE config |
| **Runtime deps** | None required. `lldpd` recommended (physical discovery), `ifupdown2` recommended |
| **Third-party Go modules** | 8 direct |
| **Licence** | Apache-2.0 — freely redistributable, attribution via `NOTICE` |

---

## Security posture

- **TLS everywhere**, reusing the node's PVE certificate by default, so there is one certificate story. Strict CSP, HSTS, `X-Frame-Options: DENY`. The single exception is `/embed/*`, which drops `X-Frame-Options` and relaxes `frame-ancestors` to exactly the origins listed in `[server] embed_frame_ancestors` (empty by default, each entry origin-validated).
- **Peer API** — TLS with the **cluster CA pinned as the sole anchor** (never the system trust pool), plus HMAC-SHA256 with a ±30 s replay window. Escape hatches require per-mode acknowledgement literals and log a warning on every startup that cannot be silenced.
- **Certificate awareness** — expiry, SAN coverage and chain are checked continuously; peers are verified against a name their certificate actually carries, so a stale IP SAN no longer takes a cluster's peer mesh down.
- **Sealed at rest** — PVE tickets, federation credentials, and apply-time revert tickets are AES-256-GCM encrypted; a revert ticket is destroyed the moment a change is confirmed or rolled back.
- **Systemd hardening** — `ProtectSystem=strict` with explicit writable paths, a 7-capability bounding set instead of full root, `SystemCallFilter=@system-service`, kernel modules/tunables/logs protected.
- **No shell interpolation** — external commands use fixed argv arrays; dynamic arguments are validated, never shell-interpolated.
- **Everything is audited**, including AI and plugin activity, with the actor distinguishable.

---

## Engineering facts

| | |
|---|---|
| Go (production / test) | 186,918 / 163,557 lines |
| TypeScript (production / test) | 57,965 / 35,656 lines |
| Automated tests | **5,358** (3,551 Go `Test`/`Fuzz` functions, 1,807 web across 247 files) |
| Packages with tests | 93 / 102 — the nine without are mock servers and generated/entrypoint packages |
| Changeset operation types | 65 (`internal/change/op.go`; `internal/change/ifaces` re-declares 23 of them, it does not add any) |
| Findings sources / checks | 15 / 43 |
| Schema migrations | 48, forward-only (`internal/store/migrations/`) |
| End-to-end specs | 41 Playwright spec files, sharded by `scripts/e2e-shards.sh` |
| Web feature modules with tests | *Row retired 2026-08-16* — it read "38 / 38" against a `web/src/features/` directory that does not exist in this tree, so it was unrecomputable rather than merely stale. Per-file coverage is the 247-test-file figure above |
| Quality gate | `make check`: gofmt, vet, golangci-lint, eslint, tsc, all tests, govulncheck, npm audit. Run on the dev host via `scripts/ci-local.sh` — see *Known limits* |

---

## Known limits — stated deliberately

A datasheet that lists only strengths is marketing. These are the boundaries as they actually stand.

| Area | Limit |
|---|---|
| **Hardware validation** | Development is mock-first against `internal/pvemock`. **15 of 151 hardware-validation items are confirmed on real Proxmox** (~10%) — count the `[x]` and `[ ]` marks in `planning/reports/needs-hardware-validation.md` to recheck; the total moves as cards add items, and Phase 29's `T-2901` added more. Multi-node behaviour — distributed apply, rollback, drift, federation, HA failover, switch push — is unproven on physical clusters |
| **End-to-end suite** | 41 Playwright specs, sharded and run by `make e2e` on the dev host. One spec (`scale.spec.ts › v2 canvas`) is **quarantined** with an unexplained mechanism — `web/e2e/quarantine.json`, `T-2505-followup-01`, **expires 2026-09-15**, after which an expired entry fails the build |
| **Where the gate runs** | GitHub Actions workflows have been `disabled_manually` since **2026-08-13** (Actions billing exhausted). The full seven-job gate runs on the dev host via `scripts/ci-local.sh`; nothing runs on push. Any doc that says CI runs on GitHub is describing the intent, not the present |
| **Partial backends** | Six features ship with a deliberately absent real backend and say so: external-IPAM production write client, eBPF flow sampling (probe + scaffolding only), packet-capture AF_PACKET path, switch-driver hardware path, SR-IOV VF lifecycle, hosted plugin registry |
| **Firewall fidelity** | Rule resolution order is a documented simplification of pve-firewall's real chain traversal; firewall-log rule correlation is heuristic, because PVE does not log rule references |
| **Simulator** | Evaluates configured state, not live packets. Node/cluster-scope host-chain rules are disclosed as off-path rather than evaluated |
| **Switch push** | A switch made unreachable by a bad push cannot be rolled back remotely — there is no agent on the switch |
| **Retention** | Short-horizon by design: 24 h metrics, bounded flow and latency rings. Export to real observability for anything longer |
| **Not yet shipped** | **Localization** — `T-2006` was never delivered despite v4.0.0's "phases 20 and 21 complete"; rescheduled as `T-3106`. **Signed apt repository** — the tooling and signing pipeline exist, the *hosting* does not (`T-2102` → `T-3301`); `apt install vnprox` still resolves to nothing. **Hosted blueprint/plugin registry** and **public demo instance** — built, not reachable. **Terraform provider / Ansible collection** — the versioned automation contract and release-asset wiring ship here; the provider and collection themselves live in downstream repositories that do not yet exist |
| **Shipped since this row last said otherwise** | `vnproxctl doctor` (incl. `--live`), the mobile PWA, and the PVE compatibility matrix all ship as of v4.0.0. The PWA was **non-functional in any real browser until Phase 29** — the CSP refused its service worker and manifest (`T-2901`); installability and push on real devices are still owed a hardware check (`planning/reports/needs-hardware-validation.md` §T-2901) |
| **Licensing** | Apache-2.0. See `LICENSE`, `NOTICE`, and `THIRD-PARTY-LICENSES.md` — the last enumerates every bundled third-party component, notably `elkjs` (EPL-2.0) which ships inside the SPA |

Current status, open items and roadmap: [`project-status.md`](project-status.md). Full audit grid and method: [`status-matrix.md`](status-matrix.md).
