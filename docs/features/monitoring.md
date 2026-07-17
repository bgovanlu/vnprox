# Feature spec — Monitoring & health

Scope discipline: vnprox shows *network-shaped, short-horizon* operational data on the map where it's uniquely useful. It is not a metrics platform.

## 1. Live rates

- Collector samples `/sys/class/net/*/statistics` (via peer API for remote nodes) every 5s; rates computed daemon-side; pushed over WS (`metrics.sample`) for subscribed refs.
- **Map integration**: optional "traffic" paint mode — edge thickness/heat by current utilization %, per-entity sparklines in the inspector, live counters (rx/tx bps, pps, errors, drops) on selection.
- Bond member balance shown per-slave (spot the LACP hash imbalance instantly).

## 2. History

24h ring in SQLite (`metric_samples`, 30s resolution after downsampling). Inspector charts: rate over time, errors/drops over time. Nothing longer — export to real observability instead.

## 3. Top talkers (P1)

Per-bridge approximation from guest NIC counters (PVE exposes per-guest netin/netout): rank guests by throughput on a selected bridge, 5m/1h windows. No packet capture, no flow sampling in v1.

## 4. Prometheus exporter (P2)

`GET /metrics` (OpenMetrics) gated to `Sys.Audit`, exporting per-entity counters + vnprox internals (changesets applied, rollbacks, drift count).

## 5. Health checks

Continuous checks feeding one findings stream (shared with drift, LLDP mismatch, IPAM conflicts): interface error/drop rate thresholds, bond slave down, LACP partner mismatch (T-804), MTU path mismatch, STP topology change bursts, bridge with no carrier uplink, dnsmasq/frr service down on a node, `interfaces.new` pending >1h, and (T-702) a node's management path carrying no redundancy (`mgmt_single_path`). Findings: severity, plain-English explanation, affected refs (map-linked), and remediation (a fixing changeset where computable, docs link otherwise). Notification hooks (webhook/email via PVE notification system) — shipped (`internal/findings/notify.go`, `pvenotify.go`), not just a "P1" aspiration as previously labeled here.

**`lacp_partner_mismatch` (T-804).** Source `health`, severity `warning`, one finding per 802.3ad bond that netlink reports MII-up whose slaves either disagree on the LACP partner's system ID/key (split-brain aggregation — each slave is really talking to a different partner) or aren't fully negotiated (missing synchronized/collecting/distributing on the actor's decoded port-state bits), among the slaves that reported LACP PDU detail at all (`internal/host/bonding.go`'s `/proc/net/bonding` actor/partner parser). A bond not running 802.3ad, or whose kernel/driver never emits that detail, is silently skipped — this check has nothing to say about a bond it never observed negotiating. Standard hysteresis debounce (2 consecutive cycles each way, `internal/findings/health_lacpmismatch.go`) — unlike `mgmt_single_path`, this is live negotiation state, not a structural fact. Detection-only (`docsLink` always set, never `fixable` — no changeset op can fix a switch-side LACP misconfiguration).

**`mgmt_single_path` (T-702).** Source `health`, severity `warning`, one finding per node whose resolved management path (`internal/topology.ResolveMgmtPaths`, the same shared resolver `GET /protected-interfaces/status` and `GET /topology`'s mgmt badges use — see docs/features/topology.md §3) has fewer than two confirmed link-up physical NICs behind a ref carrying the `mgmt` role. Unlike every other check in this section, it is **hysteresis-exempt** (no debounce window): whether a bridge/bond has one NIC or two behind it is a structural property of the current network configuration, not a noisy live counter to debounce — it fires the instant the path stops being redundant and clears the instant a second NIC comes up, with a stable id across polls (`health:mgmt_single_path|<carrier ref>`). Detection-only (`docsLink` always set, never `fixable` in this task) — this is also T-703's entry point: its guided "bond the management uplink" / "add a slave to an existing mgmt-path bond" wizards launch from this exact finding.
