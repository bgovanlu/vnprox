# Proxmox forum announcement — DRAFT, NOT YET POSTED

This is drafted, ready-to-post text for the Proxmox community forum
([forum.proxmox.com](https://forum.proxmox.com/)) — most likely under **Proxmox VE: Installation
and configuration**, since as of this writing the forum has no dedicated third-party-software or
add-ons subforum to post it under instead. **It has not been posted.** Nobody in this project's
current working session has forum access; posting it, and then actually monitoring and answering
replies (see `support.md`'s "what response to expect" — there is no support team, just
best-effort), is a step for whoever has that access and is ready to take on that follow-up.

Everything the draft claims below was checked against this repository's own documentation at the
time of writing (`docs/status-matrix.md`, `docs/hub-registry.md`, `packaging/apt-repo.md`,
`docs/features/demo-mode.md`) rather than restated from memory.

---

## Draft post

**Title:** vnprox — an open-source visual networking add-on for Proxmox VE (early, honest about
what's proven and what isn't)

Hi all,

I'd like to introduce **vnprox**, an open-source add-on that runs alongside `pve-manager` on your
PVE nodes and gives you one visual interface for the networking that's currently scattered across
node → Network, Datacenter → SDN, Datacenter → Firewall, per-VM NICs, and hand-edited
`/etc/network/interfaces`: a live cluster-wide topology map (physical NICs → bonds → bridges →
VLANs → SDN overlays → guest NICs, built from LLDP plus PVE's own config), a change engine that
stages, diffs, and applies every edit with an automatic rollback if the change breaks connectivity,
a config time machine, and a path simulator that names the exact rule or missing route blocking a
connection between two VMs.

**Try it with no install and no cluster:** `vnproxd --demo` runs the whole product against a
synthetic three-node cluster built into the binary — no PVE endpoint, no outbound network, no
root. Log in with `root` / `vnprox-mock` / realm `pam`.

### Where this stands, plainly

This is early, and I'd rather say so here than have you find out the hard way:

- **No hosted distribution exists yet.** There is no live apt repository, no downloadable release
  binary, and no hosted plugin/blueprint registry. Today, running vnprox on real hardware means
  building it from source. `docs/install.md` in the repository says exactly what works today
  versus what's built but not yet reachable — I'd rather link that than restate it here and have
  the two drift.
- **Single-node behavior is well exercised; multi-node behavior mostly isn't, on real hardware.**
  Everything cluster-aware (multi-node changeset apply, distributed rollback, node-vs-node drift,
  HA failover, federation across two real clusters, physical switch config push) has been
  developed and tested against a mock Proxmox API and a single real node, not against a real
  multi-node cluster. Specifically still unproven on hardware: multi-node changeset apply/rollback,
  failure-injection recovery from a mid-apply lockout, node-vs-node drift detection, cross-cluster
  federation and WireGuard interconnect, cross-cluster IPAM conflict detection, physical switch
  config push, SR-IOV VF lifecycle, HA active/standby failover, the NetBox/phpIPAM write-back
  client, the eBPF flow sampler, and the packet-capture backend. A formal register of exactly why
  each is blocked and what could go wrong was planned but hasn't been written yet — the list above
  is the project's own current, mechanically-derived accounting of it
  (`docs/status-matrix.md`'s hardware-validation column), not a polished summary written to sound
  better than the underlying state.
- **What *is* real:** everything above is fully implemented and covered by an extensive
  table-driven test suite against a mock PVE server, plus real single-node validation (deployed
  and running against a live PVE 9.2.4 node — schema migration, backup, support-bundle secret
  redaction, and the cluster-secret file-permission behavior were all confirmed there). It is not
  vaporware; it is unproven at scale, and I'm telling you that up front rather than after you find
  it out on a production cluster.

If you run vnprox on real multi-node hardware and hit one of the gaps above, that report is
unusually valuable — please attach a support bundle (`vnproxctl support-bundle`; it's built to
redact secrets, so it's safe to post or send) and open an issue. See `docs/support.md` for exactly
where — as of this post that section may still say "nowhere public yet"; check it for the current
answer rather than assuming GitHub Issues is live.

License: Apache-2.0.

Happy to answer questions here.

---

## Checklist before this is actually posted

- [ ] The source repository is confirmed public and reachable (`docs/support.md` currently isn't
      able to confirm this — see that file).
- [ ] `docs/install.md`'s "what works today" section is re-checked against whatever has shipped by
      posting time; this draft was written before any of that changed.
- [ ] Someone is signed up to actually watch the thread — an announcement with no one reading
      replies is worse than no announcement (`docs/support.md`'s support-posture note applies here
      too).
