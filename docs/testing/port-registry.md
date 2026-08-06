# Port registry

**Source of truth:** [`testdata/dev-ports.tsv`](../../testdata/dev-ports.tsv)
**Enforced by:** `internal/devports` (runs in `make check`)
**Preflight helper:** `packaging/test/lib/ports.sh`
**Card:** `T-1807-bug-02`

Every TCP/UDP port this repository's development and test tooling binds on a developer's machine
has a row in `testdata/dev-ports.tsv`. Adding a bind without a row fails `make check`.

---

## 1. Why this exists

Four times in a single phase, two pieces of this repo's own tooling reached for the same port and
produced a failure that looked like a product defect:

| # | Collision | Cost |
|---|---|---|
| 1 | `golangci-lint`'s file lock under concurrent worktrees | Hard error, root-caused by hand |
| 2 | The e2e fleet's implicit "quiet machine" precondition, stated nowhere | Documented, not fixed |
| 3 | `upgrade-service.sh` vs. a concurrent Playwright run, both on **8007** | "systemd reports active but nothing is listening" — an agent-hour |
| 4 | The **fix for #3** — moved to 61007, "chosen outside the entire N8006/N8007 family" — landed on `vnproxd-physcollapse`, which is 61007 | Commit `9047685` had to move it again, to 62007 |

Occurrence #4 is the argument for enforcement over documentation. The author of the #3 fix was
being careful, wrote a paragraph explaining the choice, and still collided — because there was no
list to check against. A doc table alone would have had the same outcome, since nothing would have
made anyone read it.

A fifth instance, of a different shape, happened while this card was being written: eight orphaned
`pvemock`/`k8smock` processes from a dead session held 8006/8008/18006/28006/38006/48006/58006/61006
for three hours. Nothing was colliding by design — the machine was simply not as quiet as the
tooling assumed. `ports_require_free` names the holding PID for exactly this case.

---

## 2. Adding a port

1. Pick a port that is not in `dev-ports.tsv` **and not adjacent to a registered pair.** Every e2e
   stack is an `NNN006`/`NNN007` pair; `28017` looks free but sits inside a family that will grow.
   `make ports` prints the current claims.
2. Add a row: `port <TAB> proto <TAB> owner <TAB> binder <TAB> purpose`. Tabs, not spaces.
   - **owner** — short, stable, unique (`vnproxd-flow`).
   - **binder** — the repo-relative path of the file that actually binds it. This is the row's
     authority; a test asserts the port literal still appears there.
   - **purpose** — why *this* port and not another. At least 30 characters; the next author reads
     this to avoid your port.
3. If the binder is a shell script, source the preflight helper:

   ```sh
   . "$(dirname "$0")/lib/ports.sh"
   ports_require_free "$TEST_PORT"
   ```

4. Run `make check`. `internal/devports` will tell you if the port is unregistered, double-claimed,
   or half of an unclaimed pair.

---

## 3. What is enforced

| Check | Fails when |
|---|---|
| `TestScanFindsKnownPorts` | The scan itself breaks — a glob matches nothing, or an extraction pattern stops finding its sentinel port. This is the control; without it every check below could pass by looking at nothing. |
| `TestEveryBoundPortIsRegistered` | A binder file binds a port with no row. **This is the check that would have caught `9047685` at authoring time.** |
| `TestEveryRegisteredPortIsStillBound` | A row's port no longer appears in the file it names — a stale reservation. |
| `TestNoAdjacentPortCollisions` | An `NNN006` row has no `NNN007` sibling, leaving the free half as a trap. |
| `TestParseRejectsBadRegistries` | The parser's duplicate-port/duplicate-owner guards stop working. Includes a control row, so the negative cases cannot pass vacuously. |
| `TestRegistryIsDocumented` | This document or `ports.sh` stops referencing `dev-ports.tsv`. |
| `TestRegistryRowsAreSelfDescribing` | A row's purpose is too terse to be useful. |

### Deliberately out of scope

`planning/validation/harness/**` is **not** scanned. Those scripts run against a real Proxmox node,
where `:8006` is PVE's own pveproxy — a remote endpoint they connect to, not a local port they bind.
Scanning them would make the registry claim ownership of a port it does not own. Fixture and sample
data (`internal/**/testdata`, `web/src/**`) is excluded for the same reason: a WireGuard `51820` in
a decoder test is not a bind.

---

## 4. Diagnosing a busy port

```
make ports
```

prints every registered port with live status and, for anything in use, the holding PID and command
line:

```
PORT    PROTO OWNER                      STATUS   BINDER
8006    tcp   pvemock-default            IN USE   web/playwright.config.ts
                                                    ^ pid 2496324: .../pvemock --addr 127.0.0.1:8006 ...
8007    tcp   vnproxd-default            free     testdata/dev.toml
```

A port held by a process from a session that has since died is the common case. Stop the holder;
the registry is not the problem.

---

## 5. Related

- `T-1807-bug-01` — the finding this registry answers; documents occurrences 1–3 and explicitly
  defers "a single source of truth for which ports this repo's tooling binds" to a future card.
- [`docs/development.md`](../development.md) — the `make` targets that bind these ports.
- [`docs/testing/topology-render-verification.md`](topology-render-verification.md) — the e2e
  suite's own stack layout.
