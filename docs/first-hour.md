# Your first hour

You've installed vnprox (`install.md`) or you're running it against the built-in demo cluster
(`vnproxd --demo`). This page is the fast path through what matters first; `user-guide.md` is the
complete reference for everything mentioned here.

## 1. Log in

Browse to `https://<any-node>:8007`. Log in with your **existing Proxmox VE credentials** — same
username, password, realm, and second factor as the PVE UI. There is no separate vnprox account.
(Demo mode: `root` / `vnprox-mock` / realm `pam`.)

## 2. The first-login review

vnprox walks you through four things before anything else, because it just read your whole
cluster's network and wants you to see what it found before you touch anything:

1. **What we found** — your network, drawn. Nothing was changed.
2. **Protected interfaces** — the interfaces vnprox believes carry each node's management IP and
   corosync traffic. Confirm these; vnprox refuses changes that would cut them off.
3. **Physical discovery** — an offer to enable `lldpd` if it isn't running, so the map can show
   real switch names and ports.
4. **Health findings** — anything already inconsistent (MTU mismatches, half-applied configs,
   drift between nodes).

If you'd rather look before you touch anything at all, an administrator can set `read_only = true`
in `vnprox.toml` — the full UI works, editing is disabled. (`deployment.md`'s config reference.)

## 3. Read the map

Topology is home, and it opens on **Switch view** — physical NICs and bonds, VLANs, guest ports,
and SDN VNets, grouped per node, drawn like real gear. A `Switch | Graph` toggle switches to the
node-link canvas for drag-and-drop editing, the path-simulator overlay, and traffic paint.

Three things to try immediately: press `/` and type a VM name, MAC, or IP to jump to it; type a
VLAN ID into the filter box to see where it's trunked; click anything for its full detail,
including the raw config behind it. `user-guide.md` §2 has the rest.

## 4. Make your first change, safely

vnprox never applies anything as you click — every edit collects in the **change drawer**:

1. Make an edit (drag a NIC into a bond, edit a bridge, retag a guest NIC). It becomes a line in
   the drawer.
2. **Review & apply** shows a plain-language summary and the exact per-node file diffs.
3. Apply starts a **countdown banner** (2 minutes by default). vnprox has already applied the
   change — now it wants proof you still have connectivity.
4. Click **Confirm**. If you *can't* — because the change cut you off — vnprox rolls the change
   back automatically at the deadline, no action needed from you.

This stage → validate → diff → apply → confirm/rollback flow is the whole safety guarantee behind
vnprox; see `architecture.md` for how it's implemented and `user-guide.md` §3 for the full
task-by-task guide (creating bonds, VLAN-aware bridges, SDN zones, firewall rules, and the path
simulator that answers "why can't VM A reach VM B?").

## 5. Know the escape hatches

- Your network keeps working if vnprox is down — Proxmox owns the config; vnprox is a (very good)
  way of editing it, not a dependency of it.
- `vnproxctl` works on the node even when the UI is unreachable: list/restore snapshots, trigger a
  rollback (`deployment.md` §Troubleshooting).
- Every applied change is snapshotted. **History → Snapshots** diffs any two points in time and
  restores through the same review flow as any other change.

## Next

- Running more than one cluster, or want SSO? `user-guide.md` §7 (federation, cross-cluster IPAM,
  DNS, switch push, OIDC).
- Extending vnprox, or connecting an AI assistant? `user-guide.md` §8 (MCP, plugins, tenancy, HA,
  the hub).
- Something not working? `support.md`.
