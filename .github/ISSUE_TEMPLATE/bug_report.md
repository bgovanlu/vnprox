---
name: Bug report
about: Something in vnprox didn't behave the way the docs say it should
title: ""
labels: bug
---

<!--
Before filing: run `vnproxctl doctor` first — it often names the exact file, port, privilege, or
command involved rather than leaving you to guess. See docs/support.md for the full support path,
including what a support bundle contains and why it's safe to attach (it cannot carry key
material, structurally).
-->

**vnprox version:** (`vnproxd --version`)
**Proxmox VE version:** (`pveversion`)
**Single node or cluster?** (If a cluster: how many nodes, and is this specific to one of them?)

**What happened:**

**What you expected instead:**

**`vnproxctl doctor` output:**

```
paste here
```

**Support bundle attached?** `vnproxctl support-bundle` — yes/no, and why not if not (e.g. the
daemon won't start at all; in that case, attach whatever `--dry-run` or a partial run produces
instead).

**If this touches multi-node/cluster behavior:** please say so explicitly. Several cluster-aware
behaviors are still only mock-validated, not proven on real multi-node hardware
(`docs/status-matrix.md`'s hardware-validation column, `docs/support.md`'s "Known maturity gaps"
section) — a real-hardware report against one of those areas is unusually useful.
