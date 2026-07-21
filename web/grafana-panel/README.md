# vnprox Grafana panels — contract & boundary (T-1706)

This repository ships the **panel render components and their data
contracts**, not a packaged/signed Grafana plugin. The split follows the
same "contract, not source" boundary T-1106 (Terraform/Ansible collections)
and T-1705 (blueprint/plugin hub) use: the thing third parties install lives
in a separate repository; what lives here is the stable contract that repo
builds against, plus the in-repo, unit-tested render logic.

## What is in this repo

- `web/src/grafana/MetricsPanel.tsx` — the render body of the **metrics
  panel**. It consumes vnprox's Prometheus exporter (T-1001,
  `GET /metrics`, `internal/api/metrics_exporter.go`) exposition text and
  renders the `vnprox_findings_open` / `vnprox_drift_open` /
  `vnprox_changesets` / `vnprox_build_info` families. Parser:
  `web/src/grafana/promParse.ts`. Tested against a fixture scrape in
  `web/src/grafana/MetricsPanel.test.tsx` (AC4) — no live Prometheus or
  Grafana.
- `web/src/grafana/EventAnnotationsPanel.tsx` — the render body of the
  **live event-annotation panel**. It consumes the T-1104 WS `"events"`
  topic envelope (`web/src/api/ws.ts`'s `WsServerEvent`: an `event` name plus
  payload fields) and renders each event as a timeline annotation. Tested
  against a fixture event stream with the transport mocked in
  `web/src/grafana/EventAnnotationsPanel.test.tsx` (AC4).

These components are deliberately framework-agnostic React (no `@grafana/*`
dependency is added to this repo — see `docs/development.md`'s
dependency-addition rule): the Grafana wrapper imports them unchanged.

## What lives in the external plugin repo

The published plugin (`vnprox-grafana-panel`, expected at a sibling
repository — see `planning/reports/T-1706.md`) provides only the Grafana
glue and is **not** vendored here:

- `plugin.json` (plugin id, type `panel`, Grafana version constraints).
- `module.ts` registering a `PanelPlugin` whose `setPanelOptions`/render
  delegate to `MetricsPanel` / `EventAnnotationsPanel` imported from this
  repo (published as an npm package, or git-vendored).
- The Prometheus datasource wiring for the metrics panel and the WS
  event-annotation datasource for the annotation panel.
- Grafana plugin signing and catalog submission.

## Auth

Both panels are read surfaces. The metrics panel authenticates to
`GET /metrics` with the scrape token (`[metrics]` key, T-1001 /
`docs/security.md`), configured on Grafana's Prometheus datasource. The
event-annotation panel authenticates the WS `"events"` subscription with a
T-1104 automation token carrying the `automation` scope. Neither uses, nor
can use, a write scope — consistent with T-1706's read-only embed ceiling.
