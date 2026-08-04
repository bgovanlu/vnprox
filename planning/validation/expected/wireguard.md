# Expected outcomes — wireguard

Backs `planning/validation/harness/wireguard.sh`. See `planning/validation/README.md` for the
table format. This section is deliberately static/inspection-only (see the script's own header
comment) — these rows answer the two "Host / OS behavior" checklist bullets about the WireGuard
sandbox from the outside, without triggering a real apply.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| wireguard-01 | raw | contains | wg-quick | Neither `wg` nor `wg-quick` is installed — a WireGuard changeset apply would fail on this node for a much more basic reason than the sandbox question this section is otherwise investigating. File as a packaging/prerequisite finding, not a WireGuard-apply bug. |
| wireguard-02 | raw | contains | mode=700 | `/etc/wireguard` exists but isn't `0700 root:root` (v3.0.2's `postinst`-created target) — either `postinst` didn't run as expected on this install, or something else changed the directory's mode/ownership after install. |
| wireguard-03 | raw | contains | ReadWritePaths | The unit's `ReadWritePaths` doesn't list `/etc/wireguard` (or the property is empty) — v3.0.2's fix (adding it to the systemd unit) did not land as expected on this build; a WireGuard apply under `ProtectSystem=strict` would fail here exactly as the pre-v3.0.2 crash did. This is a release blocker if confirmed. |
| wireguard-04 | raw | contains | fuse.pmxcfs | `/etc/pve` isn't mounted as a `fuse.pmxcfs` filesystem at all — either this isn't a real PVE node, or pmxcfs isn't running; the read-only-under-sandbox question doesn't apply until this is confirmed. |
