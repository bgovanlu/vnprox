# Expected outcomes — change-engine

Backs `planning/validation/harness/change-engine.sh` (**MUTATES=1**). See
`planning/validation/README.md` for the table format, how to run triage against a returned blob,
and the mutation-flag/recovery guidance specific to this section.

Every row below assumes the human running the harness chose a genuinely inert
`PVE_TARGET_IFACE` (not the management path) — if that wasn't respected, any divergence here is
secondary to the incident itself; follow `planning/validation/README.md`'s recovery guidance
first.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| change-engine-01 | exit_code | equals | 0 | The baseline `GET .../network/<iface>` failed — confirm `PVE_TARGET_IFACE` names a real interface on this node before going further. |
| change-engine-02 | exit_code | equals | 0 | The staged PUT (re-applying the same mtu) was rejected. If using the HTTP fallback, check `raw`'s `http_status` line: anything other than `200` means real PVE's JSON PUT encoding isn't accepted the way `internal/pve`'s client sends it — this is the checklist's "PUT request encoding" item, and a real divergence here is a release blocker, not a routine finding. |
| change-engine-03 | exit_code | equals | 0 | The post-stage `GET` failed. |
| change-engine-03 | raw | contains | pending | Real PVE did **not** mark the interface as staged/pending after the PUT — either PVE's staging semantics differ from `interfaces.new` as `internal/pvemock` models them (confirm the exact field name/value if `pending` isn't it), or the write silently applied immediately with no staging step at all. Either is worth its own bug card; this is the checklist's core "interfaces.new staging semantics" question. |
| change-engine-04 | exit_code | equals | 0 | The reload command itself failed to run (distinct from the reload *task* failing — see the next row). |
| change-engine-04 | raw | contains | stopped | The reload task never reached a terminal `stopped` state within the harness's ~30s poll window — either real ifreload takes meaningfully longer than that on this node (capture the real `elapsed_seconds` and consider whether `internal/change`'s own apply-timeout assumptions need revisiting), or the task API's polling contract differs from what `internal/pvemock` models. |
| change-engine-04 | raw | contains | exitstatus":"OK | The reload task completed but did not report `OK` — capture the full task status/log for the "real elapsed-time behavior... across an actual ifreload" item; this also feeds T-304's replay-cache timing question (how close together two ifreload-triggering requests can land in practice). |
| change-engine-05 | exit_code | equals | 0 | The post-reload `GET` failed. |
| change-engine-05 | raw | not_contains | pending | Real PVE still shows a `pending` marker after a completed, successful reload — PVE never cleared the staged-change flag the way `internal/pvemock`'s `handleNetworkReload` does. This is the checklist's "post-reload, no pending" question and a real divergence here is worth its own bug card (it would mean vnprox's UI could show a changeset as applied while PVE still considers it staged). |
