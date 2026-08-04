# Expected outcomes — host

Backs `planning/validation/harness/host.sh`. See `planning/validation/README.md` for the table
format and how to run triage against a returned blob.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| host-01 | exit_code | equals | 0 | `systemctl show vnprox.service` failed — either `systemctl` is missing (unexpected on PVE) or the unit doesn't exist, which is itself the "systemctl start vnprox from the .deb" checklist item failing before it even gets to state. |
| host-01 | raw | contains | ActiveState=active | The vnprox unit is not active after install/start — this is a release blocker, not an ordinary divergence: file it as such. |
| host-02 | exit_code | equals | 0 | `ip -d link show` failed — confirm `iproute2` is present (it always is on PVE) and the harness ran as a user with enough privilege to read link state. |
| host-03 | exit_code | equals | 0 | No bond interface exists on this node (expected if `pvecube` has no bonds configured) or `PVE_BOND_IFACE` named one that doesn't exist — not itself a divergence, just means this item needs a node/fixture with an actual bond to be meaningful. |
| host-03 | raw | contains | 802.3ad | (Only meaningful when a real LACP bond exists.) `/proc/net/bonding/<iface>`'s actor/partner detail block doesn't match the "details actor lacp pdu:"/"details partner lacp pdu:" shape `internal/host/bonding_test.go`'s golden fixtures assume — capture the full raw block verbatim and file a bug card against `internal/host/netlink_linux.go`'s parser with the real format attached. |
| host-04 | exit_code | equals | 0 | Neither `lldpcli` nor `lldpctl` is present, or the command errored — confirm `lldpd` is actually installed/running on this node before treating LLDP neighbor data as validated either way. |
| host-05 | exit_code | equals | 0 | The TLS probe against pveproxy failed outright — confirm `PVE_CERT_HOST` (default `localhost:8006`) is reachable and `openssl` is present. |
| host-05 | raw | contains | subject= | No certificate subject was captured at all — pveproxy's cert presentation differs from what `internal/pve`'s client expects, or the probe hit the wrong port; capture the full `raw` (subject/issuer/dates) for the "PVE-cert reuse + hot-reload" item's burndown notes. |
