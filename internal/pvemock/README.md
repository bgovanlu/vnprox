# internal/pvemock

A mock Proxmox VE API server, driven by YAML cluster fixtures under
`testdata/clusters/`. This is the development linchpin (T-004): every later
vnprox task — the PVE client (T-101), host readers (T-102), the change
engine, the SDN/firewall UI, the drift detector (T-305) — develops and is
tested against this server instead of needing real Proxmox hardware.

It implements the subset of the real PVE API surface (`/api2/json/...`)
that vnprox's client calls:

- `POST /access/ticket` — ticket + CSRF issuance, fixture-defined users and
  PVE privilege sets, bad-password → 401. Also supports:
  - **Ticket-as-password renewal**: a still-valid, previously issued ticket
    for the same user is accepted in the `password` field (how PVE clients
    renew without retaining the plaintext password). Expired or
    foreign-user tickets are rejected with 401.
  - **TOTP**: a fixture user with a `totp: "<code>"` field requires
    `otp=<code>` on login; missing/wrong code → 401. This is the simple
    single-step variant — real PVE's two-step TFA ticket-challenge flow is
    not modeled.
  - **Ticket TTL**: `mock.ticket_ttl_ms` in the fixture (or the
    `WithTicketTTL` server option, which wins) makes issued tickets expire
    server-side; an expired ticket gets 401 exactly like an unknown one.
    Default: no expiry.
- **API-token auth**: any endpoint also accepts
  `Authorization: PVEAPIToken=user@realm!tokenid=secret`, checked against
  the owning fixture user's `tokens:` list. Token requests need no cookie
  and no CSRF header (matching real PVE). Token privileges follow the
  owning user — real PVE's token privilege separation ("privsep") is
  intentionally not modeled.
- `GET /access/permissions` — the calling identity's effective privilege
  set in PVE's `{path: {privilege: 0|1}}` shape. Because fixture users
  hold one flat privilege list, everything is reported at path `/`
  (including a literal `"*"` where a fixture uses the wildcard); real PVE
  returns a per-path ACL tree with concrete privilege names.
- `GET /cluster/status`, `GET /cluster/resources`.
- `GET/POST/PUT/DELETE /nodes/{node}/network[/{iface}]` — PVE's real staging
  semantics: writes go to a staged `interfaces.new` equivalent and do not
  take effect until `PUT /nodes/{node}/network` (no iface segment) reloads
  them via an async, pollable task. `DELETE /nodes/{node}/network` reverts
  all staged changes.
- `GET/PUT /nodes/{node}/{qemu,lxc}/{vmid}/config`.
- SDN: `/cluster/sdn/zones`, `/cluster/sdn/vnets`,
  `/cluster/sdn/vnets/{vnet}/subnets` (full CRUD), per-zone
  `/cluster/sdn/zones/{zone}/status` (pending/applied/error per node), and
  `PUT /cluster/sdn` (cluster-wide apply, task-returning).
- IPAM (read-only): `GET /cluster/sdn/ipams` (configured plugin
  instances), `GET /cluster/sdn/ipams/{ipam}`, and
  `GET /cluster/sdn/ipams/{ipam}/status` (the instance's current
  allocation entries: zone/vnet/subnet/ip/mac/hostname/vmid, with the
  gateway marker as a 0/1 int), all driven by the fixture's `sdn.ipams:`
  section. IPAM writes (reserve/release) are phase-4 change-engine work
  and are not implemented.
- Firewall: cluster/node/guest-scope `rules`, `options`, `aliases`,
  `ipset[/{name}]`, plus cluster-scope `groups` (security groups) — full
  CRUD at every scope.
- Tasks: `GET /nodes/{node}/tasks/{upid}/status` and `.../log`, with
  configurable latency and failure injection (fixture defaults, or
  per-request query overrides on `PUT /nodes/{node}/network` and
  `PUT /cluster/sdn`).
- `host.Reader` (`HostReader` in this package): a fixture-backed
  implementation of the host-level reads the PVE API doesn't expose —
  `/etc/network/interfaces` file content (live or pending), netlink-
  equivalent link/bridge/bond state, LLDP JSON, and interface counters.
  T-102 will add a `real` implementation with the same method set against
  actual hardware; nothing here needs to change for that to work (Go
  interfaces are structural).

Not part of the real PVE API, but useful for tests and this walkthrough:

- `GET /mock/mess` — returns the loaded fixture's documented "mess" list
  (only non-empty for `messy-brownfield.yaml`).
- `POST /mock/nodes/{node}/network-reload-fail` `{"fail": true}` — flips
  failure injection for a node's next reload without restarting the server.

## Fixtures

| File | Models |
|---|---|
| `testdata/clusters/single-node.yaml` | One standalone node, no SDN. The baseline every feature must work against. Declares an API token (`root@pam!daemon`) and a TOTP-required user (`totp-user@pve`, static code `246810`). |
| `testdata/clusters/three-node-vlan.yaml` | 3-node cluster, bonded VLAN-aware bridges, one PVE SDN "vlan" zone with two VNets/subnets and a built-in `pve` IPAM with gateway + guest allocations. Users cover all four capability-matrix personas (root, auditor, `sdn-only@pve`, `vm-user@pve`) plus netops, and `root@pam!daemon` is a declared API token. |
| `testdata/clusters/evpn-lab.yaml` | 3-node cluster with a VXLAN/EVPN zone: controller, VRF-VXLAN, an exit node, DHCP-managed subnet, and a built-in `pve` IPAM holding the gateway record plus two DHCP allocations. |
| `testdata/clusters/messy-brownfield.yaml` | A cluster that drifted: staged-but-never-applied network edits, double NIC enslavement, cross-node MTU drift, a stale comment, a partially-realized SDN zone, an abandoned SDN VNet, a dangling firewall object reference, a manually-enslaved NIC outside the interfaces file, and the "datacenter firewall is off" footgun. Every item is enumerated in the fixture's `mess:` list (also served at `GET /mock/mess`) for T-305's drift detector to target. |

Fixture schema, beyond the obvious node/network/guest/firewall trees:

- `users[].tokens: [{tokenid, secret}]` — API tokens for
  `Authorization: PVEAPIToken=` auth; privileges follow the owning user.
- `nodes[].links[].members: [string]` (added by T-305) — overrides a
  bridge/bond's *live* (netlink) port/slave membership independently of the
  declared interfaces file's `bridge_ports`/`slaves`, modeling a manual
  `ip link set <iface> master <bridge>` done outside vnprox/ifupdown2.
  Omitted/empty (every fixture before T-305) falls back to the declared
  membership exactly as before.
- `users[].totp: "<static code>"` — marks the user TOTP-required (see
  above).
- `sdn.ipams: [{id, type, url?, entries: [...]}]` — IPAM plugin instances
  and their allocation entries (`{zone, vnet, subnet, ip, mac?, hostname?,
  vmid?, gateway?}`).
- `mock.ticket_ttl_ms` — server-side ticket expiry (default: none).

All four fixtures self-validate on load (`LoadFixture`): dangling
structural references (a bridge port or bond slave naming a NIC that
doesn't exist, a VLAN parent that doesn't exist, an SDN VNet/subnet
pointing at a zone/VNet that doesn't exist, an IPAM entry naming a
zone/vnet/subnet-CIDR that doesn't exist, an empty token id/secret) fail
loading with a specific error. Cross-entity *drift* (an SDN zone listing a node whose bridge is
missing, a firewall rule citing a deleted ipset) is intentionally NOT a
load error — that's exactly what `messy-brownfield.yaml` exists to model.

## Running it

```sh
make mockpve
# or directly:
go run ./cmd/pvemock --addr :8006 --fixture testdata/clusters/single-node.yaml
```

## Curl walkthrough (acceptance criterion 1)

This is the exact sequence verified end-to-end while building this package.
Start the server in one terminal (`make mockpve`), then run these from
another:

```sh
# 1. Authenticate — get a ticket + CSRF token, matching real PVE.
curl -s -c /tmp/pve-cookies.txt http://localhost:8006/api2/json/access/ticket \
  -d "username=root@pam&password=vnprox-mock"
# {"data":{"CSRFPreventionToken":"...","ticket":"PVE:root@pam:...","username":"root@pam"}}
#
# Save the CSRF token for later steps:
CSRF=$(curl -s -c /tmp/pve-cookies.txt http://localhost:8006/api2/json/access/ticket \
  -d "username=root@pam&password=vnprox-mock" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['CSRFPreventionToken'])")

# 2. Authenticated reads.
curl -s -b /tmp/pve-cookies.txt http://localhost:8006/api2/json/cluster/status
curl -s -b /tmp/pve-cookies.txt http://localhost:8006/api2/json/nodes/pve1/network

# 3. Staged network write — PUT one iface. This does NOT take effect yet;
# the response's network list will show it with "pending":"changed".
curl -s -b /tmp/pve-cookies.txt -H "CSRFPreventionToken: $CSRF" \
  -H "Content-Type: application/json" \
  -X PUT http://localhost:8006/api2/json/nodes/pve1/network/vmbr0 \
  -d '{"mtu":9000}'
curl -s -b /tmp/pve-cookies.txt http://localhost:8006/api2/json/nodes/pve1/network
# vmbr0 now shows "mtu":9000, "pending":"changed" — every other field
# (address, gateway, bridge_ports, ...) is untouched: PUT merges, it does
# not replace.

# 4. Reload — apply the staged change. Returns a UPID task immediately.
UPID=$(curl -s -b /tmp/pve-cookies.txt -H "CSRFPreventionToken: $CSRF" \
  -X PUT http://localhost:8006/api2/json/nodes/pve1/network \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data'])")
echo "$UPID"

# 5. Poll the task to completion.
curl -s -b /tmp/pve-cookies.txt "http://localhost:8006/api2/json/nodes/pve1/tasks/$UPID/status"
# {"data":{"upid":"...","status":"stopped","exitstatus":"OK",...}}

# 6. Confirm it's live: vmbr0 now shows mtu 9000 with no "pending" field.
curl -s -b /tmp/pve-cookies.txt http://localhost:8006/api2/json/nodes/pve1/network
```

### Permission model (acceptance criterion 2)

`single-node.yaml` also defines `auditor@pve` (privileges `Sys.Audit`,
`VM.Audit`, `SDN.Audit` only — no `Sys.Modify`). The same PUT above, as
that user, is rejected with a real 403:

```sh
curl -s -c /tmp/auditor-cookies.txt http://localhost:8006/api2/json/access/ticket \
  -d "username=auditor@pve&password=readonly"
AUDITOR_CSRF=$(curl -s -c /tmp/auditor-cookies.txt http://localhost:8006/api2/json/access/ticket \
  -d "username=auditor@pve&password=readonly" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['CSRFPreventionToken'])")

curl -s -o /dev/null -w "%{http_code}\n" -b /tmp/auditor-cookies.txt \
  -H "CSRFPreventionToken: $AUDITOR_CSRF" -H "Content-Type: application/json" \
  -X PUT http://localhost:8006/api2/json/nodes/pve1/network/vmbr0 -d '{"mtu":9000}'
# 403
```

### Failure injection and rollback (acceptance criterion 4)

Force the next reload on a node to fail, either per-request:

```sh
curl -s -b /tmp/pve-cookies.txt -H "CSRFPreventionToken: $CSRF" \
  -X PUT "http://localhost:8006/api2/json/nodes/pve1/network?mock_fail=1&mock_fail_reason=ifupdown2%20error"
```

or by flipping the node's default via the mock control endpoint:

```sh
curl -s -H "Content-Type: application/json" \
  -X POST http://localhost:8006/mock/nodes/pve1/network-reload-fail -d '{"fail":true}'
```

Either way, the resulting task's `exitstatus` starts with `failed:`, and a
subsequent `GET /nodes/pve1/network` shows the staged edit discarded — the
node rolls back to exactly its pre-staging state (no `pending` markers, no
partially-applied fields), mirroring real ifupdown2/PVE semantics where a
failed apply never leaves the host half-configured.

Query-string overrides also work on the SDN apply endpoint
(`PUT /cluster/sdn?mock_fail=1`).

## host.Reader contract

```go
type HostReader interface {
    InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)
    Links(ctx context.Context, node string) ([]LinkState, error)
    LLDP(ctx context.Context, node string) ([]byte, error)
    Stats(ctx context.Context, node string) (map[string]IfaceStats, error)
}
```

`FixtureHostReader` (`NewFixtureHostReader(srv)`) implements it against a
running mock `Server`'s state — the same fixture backing the HTTP API, so
a test exercising both sees one consistent view of the world. T-102's
`real` implementation (netlink, `/etc/network/interfaces`, `lldpctl`,
`/proc/net`) only needs to match this method set; Go's structural interface
typing means it never needs to import this package to satisfy it.
