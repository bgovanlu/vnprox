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

Continuous checks feeding one findings stream (shared with drift, LLDP mismatch, IPAM conflicts): interface error/drop rate thresholds, bond slave down, MTU path mismatch, STP topology change bursts, bridge with no carrier uplink, dnsmasq/frr service down on a node, `interfaces.new` pending >1h. Findings: severity, plain-English explanation, affected refs (map-linked), and remediation (a fixing changeset where computable, docs link otherwise). Notification hooks (webhook/email via PVE notification system) — P1.
