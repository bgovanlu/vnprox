# Support

## Before you file anything

Two commands, both read-only and safe to run on a live node:

```bash
vnproxctl doctor            # preflight/self-check: names the file, port, privilege, or
                             # command involved instead of a bare "it doesn't work"
vnproxctl support-bundle    # everything below, in one archive
```

`vnproxctl doctor` works even with the daemon down or the install incomplete — every check that
can't run reports `skip` with a reason, never a false `pass`. Run it first; it answers a
surprising fraction of "why isn't this working" on its own.

`vnproxctl support-bundle` collects what a maintainer would actually ask you for: recent
changesets, daemon logs (`journalctl` by default, or `--log-file`), a parsed and allowlisted view
of `/etc/network/interfaces` and `corosync.conf` (never the raw files verbatim), and — unless you
pass `--no-probe` — a live read of peer reachability, daemon health, and the listen port. It is
written to `/var/lib/vnprox/support/` by default (`--out`/`--out-dir` to choose elsewhere), or use
`--dry-run` to see exactly what would be collected without writing anything.

**It cannot carry key material, structurally, not by a flag you might forget.** Unlike
`vnproxctl backup`, there is no `--include-keys` and no equivalent — the bundle collectors
implement an interface with no method that could emit one. This was checked against a real
install's real credentials, not just fixtures: a bundle built from a live deployment and scanned
after decompression contained neither the session key nor the PVE API token, using a scan first
proven to *find* an unrelated real secret in the same bundle as a control (see
`planning/reports/needs-hardware-validation.md`, 2026-08-05 entry). Attach the bundle to whatever
you file — it's built to be safe to hand to someone else.

## Where to file

**Today, honestly: there is nowhere public yet.** This project's source lives at
`github.com/bgovanlu/vnprox` — the same path its Go module uses — and that repository is
**private**. It exists and is actively developed; an anonymous request returns 404 because GitHub
answers that way for a private repository rather than revealing one exists. So there is no public
issue tracker and no public clone URL, and "open an issue on GitHub" is not yet a real
instruction. Making it public is a decision for the maintainer, not a missing piece of
infrastructure. Two channels are prepared for when that changes:

- **GitHub Issues**, once the repository above is public. Include your `vnproxctl doctor` output
  and (unless it's not applicable — e.g. a docs issue) a support bundle.
- **The Proxmox community forum**, once the announcement in `forum-announcement.md` is actually
  posted — it isn't yet; that file is a drafted, unpublished announcement, not a live thread. Once
  it exists, replying on that thread is a second valid channel, better suited to "is this expected
  behavior" questions than to bug reports with attachments.

If you're reading this because you already have access to the source tree some other way (an
internal build, a fork, a colleague who sent you a `.deb`), the honest answer is: ask whoever gave
it to you where they want issues routed. This document describes the intended path once
distribution is real, not a channel that exists today.

## What response to expect

Say this plainly rather than implying a support organization that doesn't exist: vnprox does not
have a support team, an SLA, or a paid tier. It is maintained by whoever is maintaining it at any
given time, best-effort. A well-formed report — `vnproxctl doctor` output, a support bundle when
relevant, and your PVE version (`pveversion`, cross-checked against `compatibility.md`'s matrix) —
is what makes a best-effort response possible at all; the maintainers have no live Proxmox cluster
of their own beyond what's noted in `planning/reports/needs-hardware-validation.md`, so a report
that includes real evidence from your hardware is often worth more than a long description.

## Known maturity gaps, stated plainly

Before filing "this doesn't work on my N-node cluster," check whether the behavior in question is
one of the ones vnprox already discloses as unproven on real multi-node hardware — it may be a
known gap, not a new bug. `docs/status-matrix.md` §2 marks each feature area's hardware-validation
state (`V` validated, `M` mock-only, `B` blocked pending multi-node hardware); as of the matrix's
last mechanical sweep the **B**-marked areas are: multi-node changeset apply/rollback,
failure-injection proof of commit-confirm, the NetBox/phpIPAM production write client, node-vs-node
drift detection, the eBPF flow sampler (probe/scaffolding only), the packet-capture AF_PACKET
backend, cross-cluster federation and WireGuard interconnect, cross-cluster IPAM conflict
detection, physical switch config push, SR-IOV VF lifecycle, and HA active/standby failover. A
formal blocked-validation register (task `T-1803`, `planning/tasks/phase-18.md`) was planned to
document each of these with its specific failure mode and severity; that register has not been
produced yet — the list above, from `docs/status-matrix.md`, is the most current accounting that
exists. If you can reproduce a problem in one of these areas on real hardware, that report is
unusually valuable — it's exactly the evidence this list is missing.
