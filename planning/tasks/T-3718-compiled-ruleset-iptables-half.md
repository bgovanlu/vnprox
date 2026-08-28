# T-3718 · The compiled-ruleset inspector reads nftables; default PVE 9.2.4 compiles to iptables

**Found by:** T-3904's mandatory node observation, 2026-08-28 · **size:** M ·
**depends:** T-3904 (landed) · **affects:** the compiled-ruleset inspector's usefulness on any
default-configured host

## The observation

T-3904's card assumed PVE compiles its firewall to nftables. The node says otherwise. **PVE 9.2.4
ships two firewall compile engines side by side:**

| Engine | Language | Compiles to | Default |
|---|---|---|---|
| `pve-firewall` | Perl, legacy | **iptables** | **on** |
| `proxmox-firewall` | Rust, newer | nftables | **off** — "tech preview", opt-in per host via `host.fw`'s `nftables: 1` |

On pvecube the nftables engine is *not* effective: the option is unset and
`proxmox-firewall`'s own force-disable flag file `/run/proxmox-nftables-firewall-force-disable` is
present. Separately, the datacenter firewall is administratively disabled (`cluster.fw:
enable: 0`), so both engines currently produce zero rules — which is why the inspector's empty
state has to distinguish *disabled* from *compiled-elsewhere*, and does.

Evidence: `planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt`, including
the installed `proxmox-firewall` binary's confirmed table/chain vocabulary read via `strings` and
pinned by sha256.

## Why this needs its own card

T-3904 shipped correctly and honestly: it reads nftables, labels the ambiguous empty state, and
refuses to guess attribution it cannot establish. But on a **default** PVE 9.2.4 host — which is
every host that has not opted into a tech preview — the inspector will correctly show nothing,
forever. The feature's whole value is showing an operator the gap between the rules they wrote and
the chains that got installed, and for most installs those chains are in iptables.

This is not a defect in T-3904. It is the scope that observing the node revealed, which the card
could not have known when it was written from documentation.

## Deliverables

- An iptables reader (`iptables-save`, or `iptables -S` per table) behind the same
  `GET /firewall/compiled` contract, following `internal/host/nftables.go`'s fetch/parse split.
- **Engine detection, surfaced in the response**: report which engine is effective on that node —
  read `host.fw`'s `nftables` option and the force-disable flag file, do not infer from which
  ruleset happens to be non-empty. An operator looking at an empty inspector must be able to tell
  "my firewall is off", "my firewall compiles to the other engine", and "my firewall is on and
  produced no rules" apart. All three are different facts.
- Attribution for iptables chains against `internal/fw`'s snapshot, with the same honesty limit
  T-3904 established: PVE built-in chains labelled as such, ambiguous matches reported as
  ambiguous, guest/vnet scope left unattributed while chains remain shared rather than per-guest.
- A test that the engine-detection logic reports correctly for each of the three states, using
  fixtures derived from the evidence file rather than invented.

## What must NOT happen

- Do not enable the firewall, or the nftables engine, on pvecube to get better test data. It is a
  live production host and that is a real firewall change. T-3904 declined the same shortcut.
- Do not synthesise a populated ruleset fixture from imagination. If no populated real ruleset can
  be observed anywhere, say so and keep the fixtures conservative — that is what T-3904 did, and
  it is why its parser is trustworthy.
- Do not turn this into an editor. The PVE firewall engine boundary is permanent.

## Acceptance criteria

1. On a host running the legacy engine, `GET /firewall/compiled` returns the installed iptables
   rules, and the UI names the engine it read.
2. The three empty states (firewall off / other engine / on-but-empty) are distinguishable in the
   API response and in the UI, with a test for each.
3. The evidence file for whichever host is used is checked in, per `CLAUDE.md`.
