# T-3719 · `GET /ports` and docexport's wiring table omit NICs with no LLDP neighbour

**Found by:** T-3907's cabling-plan work, 2026-08-28 · **size:** S ·
**depends:** — · **affects:** `GET /ports`, `internal/docexport`'s "LLDP wiring" table, and any
consumer that reads either as a NIC inventory

## The observation

`internal/topology`'s `Ports` projection (served by `GET /ports`) emits a row **only when an LLDP
neighbour entity exists** for that NIC. A physical NIC whose far end is an unmanaged switch, a
directly-attached host, or a device with LLDP disabled produces **no row at all** — it is not
listed as undiscovered, it is simply absent.

`internal/docexport`'s "LLDP wiring" table has the same shape and therefore the same gap: an
exported config document silently under-reports the machine's physical connectivity.

T-3907 did not hit this because `web/src/topology/switchModel.ts`'s `buildSwitchModel` enumerates
every physical NIC independently (bridge members, bond members, and free NICs) and *left-joins*
any discovered neighbour, so the cabling plan could render a three-way
`discovered` / `unknown` / `grouped` state. That client-side path is correct; the two server-side
paths are not.

## Why this matters

**Absent and "not discovered" are different facts, and the difference is the whole point of a
cabling document.** An operator reading an exported wiring table sees the NICs that happen to face
LLDP-speaking equipment and has no way to tell the rest exist. That is the same failure mode as an
empty panel meaning both "disabled" and "nothing found" — a distinction T-3906 spent a card
getting right on the flows/conntrack side.

It also makes the export actively misleading in the common homelab and small-office case, where
some or all switches do not speak LLDP.

## Deliverables

- Change `Ports` to enumerate physical NICs and left-join LLDP neighbours, so every NIC appears
  with an explicit discovered/undiscovered state rather than being dropped. `buildSwitchModel` is
  the reference implementation for the enumeration — port it, do not re-invent the join.
- Do the same for `internal/docexport`'s wiring table, or have it consume the corrected `Ports`
  projection so there is one enumeration rather than two that can drift apart.
- Keep the state a **three-way** value, not a boolean: a NIC inside a collapsed physical group is
  a third case and must not be confused with an unresolved link (T-3907's `linkState` is the
  precedent).
- A test that a NIC with no LLDP neighbour appears in both outputs, marked undiscovered. This is
  the assertion whose absence let the gap ship.

## Acceptance criteria

1. `GET /ports` lists every physical NIC on every node, each carrying an explicit link state.
2. `internal/docexport`'s wiring table lists the same set, with undiscovered far ends rendered as
   such rather than omitted.
3. Both have a test that fails if a NIC without a neighbour is dropped.
4. `web/src/topology/cablingPlan.ts` can be simplified to consume the corrected server projection,
   or a note records why keeping the client-side enumeration is still preferable.
