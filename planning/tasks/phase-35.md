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

### Delivery record — DONE, 2026-08-20 (`6140a614`, `fef8cca8`)

**The card asked for two facts the graph did not carry**, and the interesting part of this task was
refusing to fake them. "RJ45 for copper, SFP cage for fibre" needs a media type; "negotiated speed
where known" needs a speed. Neither was on a topology node, and the tempting shortcuts — infer fibre
from a 10G link, infer copper from everything else — are exactly the kind of thing CLAUDE.md's
"never model a PVE object from docs/mock" rule exists to stop, with the extra hazard that a
mis-drawn connector is invisible: the faceplate still renders, still passes axe, still looks like a
switch.

Both were observable, and were observed (`planning/reports/evidence/pve-9.2.4-nic-media-and-speed
.txt`, read-only from pvecube). `Port` turned out to be field 4 of `struct ethtool_cmd`, which
`internal/host/ethtool_linux.go` has been filling via `SIOCETHTOOL`/`ETHTOOL_GSET` for speed and
duplex all along and then discarding — so the whole media chain cost one already-issued ioctl, no
new syscall and no new dependency.

**The transcript also settled a design question the card had backwards.** The card says "SFP cage
for fibre/**bond members**", which would have made a bond member's drawn connector depend on whether
it was in a bond rather than on what it physically is. It also implies keying the drawing off
link-time facts. The evidence says otherwise: media type is a property of the *socket* and is
reported even with no carrier (pvecube's down `enp2s0`/`enp4s0` still answer `Port: Twisted Pair`),
while speed is a property of the *link* and vanishes when the port goes down (Linux reports `-1`).
So the body follows media only, a down port keeps its shape and merely loses its speed marking, and
bond members are drawn as whatever they physically are — the *aggregation* is conveyed by the joined
group and rail around them, which is what the card's next bullet actually wanted.

**Deviations, all deliberate:**

1. **A third port body, `unknown`, that the card did not ask for.** A peer node this daemon has
   never host-polled carries no media type at all, and so does the captured fixture below. Drawing
   those as a confident RJ45 would be an invention; they get a visibly distinct "no reading"
   silhouette instead, and the accessible description says "media type unknown" in words.
2. **Guest access ports get a dashed jack, not a solid one.** A guest NIC has no socket. Keeping the
   jack shape makes the virtual switch read as a switch (which is what the request was about);
   dashing it stops the drawing from claiming there is something to plug into.
3. **Two T-2004 contrast assertions updated rather than preserved**, with measurements recorded in
   the tests. One pinned `text-slate-600` on the old `bg-indigo-50` header — correct there, and
   *sub-AA* on the new dark name plate, where the pre-T-2004 value would have passed; the surface
   changed, so the pairing under test had to. The other pinned a DOM child index that a layout
   wrapper shifted, and now locates by content. Neither was a contrast regression.
4. **`internal/pvemock`'s `LinkInfo`/`LinkState` were touched** (one layer below the plan) because
   `internal/host/fixture.go` is fed by them rather than by a fixture format of its own — without
   it, "fixtures can express a media port" is not true.

**AC1 is proved against real data.** `web/src/topology/__fixtures__/pvecube-reference-topology.json`
is a verbatim `GET /topology` capture from the live deployment: six bridges, five guests, four NICs,
`102 opnsense net1/net2/net3` down. It has never been curated to make the renderer look good — and
because it predates the two new fields, it doubles as the honest "no reading" case.

**AC2**: the port field wraps and is height-capped *unconditionally* (no count threshold at which
the layout changes character), so a four-port switch is unaffected and a dense one scrolls instead
of stretching the chassis past the viewport; the silkscreen always prints the full count, so a
scrolled field cannot under-report. Proved at 48, with every port still a tabbable `<button>` —
which is also what satisfies axe's `scrollable-region-focusable` without a `tabindex` of its own.

**AC3**: 288 web test files / 2205 tests green; `make ci` green. LEDs now carry a per-status glyph
(solid / half / cut-through / hollow ring) as well as a hue, so status survives a colour-vision
deficiency and the greyscale stale rendering — an improvement on the pre-existing colour-only dot.

**Left for hardware**: the fibre/DA branch, the `PORT_NONE`/`PORT_OTHER` fallback, and how the
unknown-media body reads on a peer node's ports — all three filed in
`planning/reports/needs-hardware-validation.md`. Every NIC on the one available node is Twisted
Pair, so the cage rendering has never been produced by real hardware.

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

### Delivery record — DONE, 2026-08-20

**Disposition: fold into the guest NIC.** The card called this a product judgement and it was, right
up until the node was asked what an `fwbr` actually is
(`planning/reports/evidence/pve-9.2.4-firewall-bridges.txt`, read-only). After that the other two
options are ruled out by fact rather than by taste:

- `fwbr<vmid>i<netid>` has exactly two members, always: `fwln<vmid>i<netid>` and the guest's own
  `veth`/`tap`. Both are the runtime-owned interfaces vnprox deliberately does not model as entities
  — the same class T-3502 taught `internal/drift` to ignore.
- The guest NIC's `attached-to` edge points at the **logical** bridge (`vmbr0`), never at the
  `fwbr`.

So these chassis are not empty because the collector missed something; they are *structurally*
empty, and no amount of populating fixes them. "Collapse behind a disclosure" and "hide behind a
toggle" would both have kept drawing a device with nothing in it and merely made it harder to reach.
The card's own bullet — "if they remain drawn, their actual members must be shown" — has no
satisfiable reading here, which is what settled it.

Folding is exact rather than approximate: the vmid/netid in the name **is** the guest NIC's
identity, so the mapping is total and lossless. `internal/topology`'s `foldFirewallBridges` drops the
node and gives the owning NIC a `firewall=<name>` badge; the faceplate draws a small `fw` marking
with the bridge's name in its title, and the name reaches assistive tech through `badgeAriaParts`'
verbatim badge list — the same route the Graph view reads it by, so the two views cannot disagree
(AC2).

**Two deliberate refusals to over-reach:**

1. **An orphaned `fwbr` keeps rendering.** The fold is conditional on the owning guest NIC being
   present in the same projection. A bridge left behind after a guest stopped, or one whose NIC a
   `?node=`/`?layers=` filter excluded, is a real interface with no owner — not chrome the operator
   can be told to ignore — and folding it into a NIC that isn't there would have deleted it from the
   map outright. This is per-bridge, not an all-or-nothing bail-out.
2. **The pattern is anchored** (`^fwbr\d+i\d+$`). An operator's own `fwbr-dmz` is not swallowed.
   That case has its own test, because a regression there would be invisible: the bridge would
   simply stop being drawn.

Nothing is hidden that could not be found: the entity is untouched in the inventory and
`GET /inventory/{ref}` still answers for it. Documented in `docs/api.md` and
`docs/features/topology.md` §3.

**Not done here**: `badgeAriaParts` still reads the badge verbatim ("badges: firewall=fwbr103i0")
rather than phrasing it ("firewalled by fwbr103i0"). Phrasing it belongs in `a11yBridge.ts`, where
both views get it at once — folded into T-3505 rather than done twice.

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
