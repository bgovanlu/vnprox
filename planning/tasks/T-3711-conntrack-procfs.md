# T-3711 · Conntrack sampling reads a file that cannot exist on PVE 9

**Found by:** T-3706, while enabling the flow stack on the nested lab. · **size:** M ·
**depends:** — · **affects:** T-1004 (host-local flow sampling), T-1305 (`internal/host/conntrack.go`)

## The defect

`conntrack_sampling_enabled` turns on a sampler that reads **`/proc/net/nf_conntrack`**. That file
does not exist on PVE 9, and cannot be made to exist. Verified on the running kernel, on both the
lab node and pvecube:

```
$ ls -la /proc/net/nf_conntrack
ls: cannot access '/proc/net/nf_conntrack': No such file or directory

$ lsmod | grep -c nf_conntrack
3                                   <- the module IS loaded

$ grep NF_CONNTRACK_PROCFS /boot/config-$(uname -r)
# CONFIG_NF_CONNTRACK_PROCFS is not set      <- compiled out, not a runtime toggle

$ conntrack -C
0                                   <- the netlink interface works fine
```

So the subsystem is present and healthy; only the **procfs interface** is gone. Modern kernels drop
`CONFIG_NF_CONNTRACK_PROCFS`; the supported interfaces are the `conntrack` CLI and the netlink
socket it wraps.

Symptom when enabled: `hostsample: conntrack poll failed … no such file or directory`, every 10
seconds, forever. The feature cannot ever produce a sample.

## Why this was not caught

The same shape as T-3701 and the sFlow `frame_length` bug, and it should be recorded as the third
instance:

- Every sysctl the code checks is present, so a configuration check passes.
- The module is loaded, so a "is conntrack available?" probe passes.
- The only thing missing is the specific *file*, and nothing in the test suite reads a real one —
  the fixtures are hand-written `/proc/net/nf_conntrack` text, which will parse forever regardless
  of whether the kernel still emits it.

A parser tested only against a fixture the project authored cannot discover that the kernel stopped
producing that format. **The fixture is not wrong here — the assumption that the file exists is.**

## Deliverables

- Replace the procfs read with the **netlink** interface (`conntrack -L`, or a direct netlink
  socket — check what the daemon's existing capability set allows before choosing; `internal/host`
  already deals with capability-constrained reads and there is prior art there).
- **Detect and report** rather than retry forever. If the interface is unavailable, the feature
  should say so once, clearly, and stop — not log an error every 10 seconds indefinitely. Check how
  other unavailable-subsystem cases in `internal/host` report themselves and match that.
- `internal/host/conntrack.go` (T-1305) shares the same assumption and must be fixed with it. They
  are one defect in two places, not two defects.
- A test that would fail against the current code. Given the lesson above, prefer a test that
  exercises the real interface on a machine that has it over one that parses a longer fixture.

## Acceptance criteria

1. With `conntrack_sampling_enabled` on the lab, real conntrack-derived flow records appear —
   observed in the explorer, not inferred from a unit test.
2. On a host where the interface is genuinely unavailable, the feature reports that once and stops,
   with no repeating error.
3. T-1004 and T-1305 move from their current status to `Live` **with evidence**, or the reason they
   cannot is stated.

## Status of the affected features

`docs/audit-matrix-2026-08-23.md` scored T-1004 and T-1305 as `Shipped, inert` — switched off by
configuration. That was generous, and this card corrects it: they are **Degraded**. Inert implies
"turn the key and it runs". Turning the key produces an error loop and no data.

## Note

Do **not** enable conntrack sampling on pvecube to test this. The lab exists for exactly this, and
pvecube runs the deployed product. See `scripts/pve-lab.sh`.
