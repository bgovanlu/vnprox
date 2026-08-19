# Phase 35 — Device-model topology, and the badge that cried drift

Goal: the topology views should read as **network equipment**, not as boxes with text in them. A
bridge should look like a switch, a NIC like a NIC, a bond like a bonded pair, a VNet like an
overlay segment. Alongside that, this phase fixes the reason the current view is actively
misleading: three of four bridges on the reference node pulse continuously with a chip reading
"drift", and for two of them that is simply not what is wrong.

Scoped from a real screenshot of `pvecube` (PVE 9.2.4, single node) taken 2026-08-19, plus the
live `GET /api/v1/findings` from that same instance and its real `/etc/network/interfaces` and
`/sys/class/net`. **Every claim below was checked against that node**, not against
`internal/pvemock` or docs — per `CLAUDE.md`'s rule after the SDN Fabrics incident.

---

## What the reference node actually shows

Switch view, node `pvecube`, six bridges: `fwbr103i0`, `fwbr104i0` (both rendering as large,
completely empty cards), `vmbr0`, `vmbr1`, `vmbr2`, `vmbr3`. `vmbr0`, `vmbr1` and `vmbr3` each
carry a small grey `drift` chip and pulse continuously.

The live findings stream explains it, and does not match what is drawn:

| Entity | What the UI says | What is actually wrong |
|---|---|---|
| `vmbr0` | `drift` (pulsing) | A real `drift/file_runtime_divergence`, **but see the false positive below** — plus a separate `health/mgmt_single_path` warning that gets no distinct treatment |
| `vmbr1` | `drift` (pulsing) | `health/bridge_no_carrier`, severity **error** — no carrier on `enp2s0`. Not drift. |
| `vmbr3` | `drift` (pulsing) | `health/bridge_no_carrier`, severity **error** — no carrier on `enp4s0`. Not drift. |
| `dnsmasq`, `frr` down | nothing | Two `health/service_down` errors with no entity refs, so they paint nothing anywhere |

## Two real defects behind that

**1. The badge vocabulary lies, by documented decision.** `internal/api/topology.go:224`:

```go
const findingBadge = "drift"
```

Its own comment is candid: T-602 generalized this badge to mean "carries an open finding from
**any** unified-stream producer (drift, lldp, ipam, or health)", and deliberately kept the wire
value `"drift"` "for backward compatibility with the frontend's existing check and with
`docs/api.md`'s documented badge vocabulary, rather than introducing a second, parallel badge
string for the exact same dashed-outline visual treatment."

That was a reasonable call when the badge only changed an outline style. It stopped being
reasonable once the frontend started **rendering the literal word "drift"** next to the entity
(`SwitchFaceplate.tsx`) and **pulsing the whole card** on it (`SwitchFaceplate.tsx:375`,
`animate-pulse`). A no-carrier *error* now presents as a drift *warning*, in the wrong words, and
the operator is told an `ip link` change may have been made by hand when the actual problem is an
unplugged cable. Severity is discarded entirely: `error` and `warning` render identically.

**2. `drift/file_runtime_divergence` fires on Proxmox's own firewall veths.** On `pvecube`:

```
/etc/network/interfaces:  bridge-ports enp1s0
/sys/class/net/vmbr0/brif: enp1s0  fwpr103p0  fwpr104p0
/sys/class/net/fwbr103i0, /sys/class/net/fwbr104i0   ← the matching firewall bridges
```

`fwpr*`/`fwln*` are veth pairs `pve-firewall` creates at guest start whenever a guest NIC has
`firewall=1`, bridged to a per-NIC `fwbr*`. **They are never in `/etc/network/interfaces` by
design.** vnprox reports this as "a manual `ip link` change may have been made outside vnprox".

`firewall=1` is the default for a guest NIC added through the PVE GUI, so this fires on a large
share of real installs — the drift checker cries wolf on the single most common Proxmox
configuration there is. No `fwpr`/`fwln`/`fwbr` handling exists anywhere in `internal/drift/`
(grepped, 2026-08-19).

---

## The design direction

Today the Switch view draws a titled card with two text sections, "UPLINK" and "ACCESS PORTS
(n)", each holding small labelled chips. It is a table wearing a card's clothes. The target is a
**faceplate**: something an operator recognises as a device before reading a single word.

- **A bridge is a switch.** A chassis with a name plate, a status LED, and a row of physical-looking
  ports. Ports are drawn as ports — a body, a link LED, a speed/label legend — not as text chips.
- **Uplinks sit apart from access ports**, the way an uplink bay does on real hardware: different
  position, different port styling (SFP-shaped for a bond/fibre, RJ45-shaped for copper).
- **A NIC is a NIC**: carrier state, speed and duplex read off the port itself.
- **A bond is visibly bonded** — two or more member ports drawn joined, LACP state on the group.
- **A VNet/overlay is not a physical thing and must not pretend to be**: overlays render as a
  segment band behind or across the devices they span, never as another RJ45.
- **A firewall bridge (`fwbr*`) is infrastructure, not a device the operator configured.** Today
  they render as two large empty cards, which is worse than not drawing them.

Constraint carried from Phase 34: this must survive light, dark and demo-amber, and every status
must be legible without relying on colour alone (a red LED and a green LED are the same LED to a
colourblind operator — shape or glyph must carry it too).

---

## T-3501 · Say what is actually wrong: split the finding badge by source and severity
**model:** sonnet-5 · **size:** M · **depends:** — · **context:** `internal/api/topology.go` (`findingBadge`, `paintDrift`, `paintFindings`), `docs/api.md` badge vocabulary, `internal/findings/catalog.go`, `web/src/topology/{EntityNode,EntityEdge,SwitchFaceplate,canvasDraw,a11yBridge}.tsx|ts`

**Objective:** An entity carrying a finding should say which kind of finding, and how bad. This is
a prerequisite for the visual work — there is no point drawing a beautiful status LED that is wired
to the wrong signal.

**Deliverables:**
- Replace the single `"drift"` wire badge with a source-and-severity-bearing form. `docs/api.md`'s
  badge vocabulary is a frozen contract: update it in the same change, and keep a documented
  transition for any consumer reading the old token (the existing comment's backward-compatibility
  concern is legitimate — honour it deliberately rather than breaking it silently).
- Frontend renders the real source (`drift`, `health`, `lldp`, `ipam`) and reflects severity:
  `error` must not present identically to `warning`.
- The pulse becomes meaningful: reserve motion for the severity that warrants it, and keep the
  `prefers-reduced-motion` behaviour Phase 34 and T-905 already established.
- Hovering/selecting an entity surfaces the finding's own `detail` text — the operator should not
  have to leave the map to learn that `enp4s0` has no carrier.
- Findings with **no entity refs** (`service_down` for dnsmasq/frr on the reference node) currently
  paint nothing anywhere. Decide and implement where those surface; do not leave them invisible.

**Acceptance criteria:**
1. Against a fixture reproducing `pvecube`'s finding set, `vmbr1`/`vmbr3` present as a carrier
   **error** naming the NIC, and `vmbr0` as drift — no entity presents a health finding as drift.
2. `docs/api.md`'s badge vocabulary matches what the handler emits; a test pins the two together.
3. axe passes in light, dark and demo-amber; status is distinguishable without colour.

## T-3502 · Stop reporting Proxmox's own firewall veths as drift
**model:** sonnet-5 · **size:** S · **depends:** — · **context:** `internal/drift/reconcile.go`, `internal/drift/filerun_test.go`, `internal/drift/messybrownfield_test.go`, real `pvecube` evidence in this card

**Objective:** `drift/file_runtime_divergence` must not fire on interfaces Proxmox creates and
manages itself.

**Deliverables:**
- Treat `fwpr*` / `fwln*` / `fwbr*` (and `tap*` / `veth*` guest NIC devices, if the same reasoning
  applies — verify against the node rather than assuming) as runtime-owned, not file-declared.
- **Verify the real naming and lifecycle against `pvecube` first** (`pvesh usage`, `/sys/class/net`,
  `ip -d link show`) and check the transcript into `planning/reports/evidence/`. Do not model this
  from `internal/pvemock` or from documentation — the mock is exactly where this class of error
  has bitten before.
- A regression test built from the real observed membership (`enp1s0`, `fwpr103p0`, `fwpr104p0`
  live vs `enp1s0` declared) that **fails before the fix**.
- Confirm the fix does not blind the checker to the thing it exists for: a genuine hand-made
  `ip link add`/`brctl addif` must still be reported. Both cases in one table-driven test.

**Acceptance criteria:**
1. The reference node's `vmbr0` no longer raises `file_runtime_divergence`.
2. A hand-added bridge member still does.
3. `internal/drift`'s existing brownfield tests still pass unmodified.

## T-3503 · Faceplate v2: draw devices as devices
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-3501 · **context:** `web/src/topology/SwitchFaceplate.tsx` (502 lines), `SwitchView.tsx`, `docs/features/topology.md` §2–§4, Phase 34's `docs/development.md` "Visual language" section

**Objective:** Replace the card-with-text-sections rendering with a real faceplate.

**Deliverables:**
- Chassis: name plate, status LED, device-kind marking, and a port field.
- Ports drawn as ports: RJ45 body for copper, SFP cage for fibre/bond members, with a link LED,
  and negotiated speed where known. Port identity (`enp1s0`, guest `net0`) legible at rest.
- Uplink bay visually distinct from the access-port field, positioned as on real hardware.
- Bond members drawn as a joined group carrying LACP state, not as adjacent independent ports.
- Guest NICs on access ports keep their VMID and guest name, and their status LED reflects link
  state (the reference node has `102 opnsense net1/net2/net3` down, red).
- Density: this must survive a node with far more ports than the reference node's 3–4. Define the
  wrap/scroll behaviour and prove it at 48 ports.
- Keep every accessibility affordance Phase 34 and T-905 established: `switchAriaDescription`'s
  phrase list, keyboard reachability of each port, and no status conveyed by colour alone.

**Acceptance criteria:**
1. Renders `pvecube`'s real six-bridge topology recognisably as switches with populated ports.
2. A 48-port fixture renders without overflow or unreadable collapse.
3. Existing `SwitchFaceplate` tests pass or are updated with the reasoning recorded; axe clean in
   all three variants; keyboard traversal reaches every port.

## T-3504 · Firewall bridges: stop drawing empty boxes
**model:** sonnet-5 · **size:** S · **depends:** T-3503 · **context:** the screenshot's `fwbr103i0`/`fwbr104i0` cards, `web/src/topology/SwitchView.tsx`, real `pvecube` `/sys/class/net/fwbr*`

**Objective:** `fwbr*` bridges currently render as two large, entirely empty cards — the biggest
single waste of space on the screen, and confusing, since the operator did not create them.

**Deliverables:**
- Decide, from what these actually are (per-guest-NIC firewall bridges auto-created by
  `pve-firewall`), whether they should be: folded into the guest NIC they belong to as a firewall
  indicator; collapsed behind a disclosure; or hidden by default with a toggle. Pick one, and
  write down why in the card's delivery record.
- Whatever is chosen, an operator must still be able to find them — they are real and can break.
- If they remain drawn, their actual members (`fwln*`, `tap*`) must be shown; an empty box is not
  an acceptable rendering of a bridge that has ports.

**Acceptance criteria:**
1. No empty device renders anywhere in the Switch view on the reference topology.
2. The firewall relationship for a guest NIC with `firewall=1` is discoverable in the UI.

## T-3505 · Graph view parity
**model:** sonnet-5 · **size:** M · **depends:** T-3501, T-3503 · **context:** `web/src/topology/canvasDraw.ts`, `TopologyCanvasV2.tsx`, `EntityNode.tsx`, `EntityEdge.tsx`

**Objective:** The Graph (canvas) view shares the badge vocabulary and status language with the
Switch view and must not diverge from T-3501/T-3503 — two views disagreeing about what an entity's
state is would be worse than the current honest-but-wrong single story.

**Deliverables:**
- Canvas node rendering adopts the same device vocabulary at whatever fidelity the zoom level
  supports, degrading to a recognisable silhouette rather than a generic rectangle.
- `canvasDraw.ts`'s drift dashing and `pulseAlphaForPhase` follow T-3501's severity model.
- The T-901 accessibility bridge keeps parity: whatever the faceplate says about an entity, the
  canvas's DOM proxy layer says too.

**Acceptance criteria:**
1. The same entity reports the same status, source and severity in both views — pinned by a test
   over one fixture, not by inspection.
2. Existing canvas parity/perf specs still pass; `perf/budgets.json`'s `topology.scale.*` budgets
   still hold.

---

## Open question for the owner

**T-3504's disposition of `fwbr*` bridges** is a product judgement, not a technical one: they are
real objects, but ones Proxmox created on the operator's behalf. Fold, collapse, or hide-by-default
are all defensible and they lead to different screens. Flagging rather than choosing, per
`CLAUDE.md`.
