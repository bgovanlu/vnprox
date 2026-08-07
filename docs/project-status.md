# vnprox — project status

**As of:** 2026-08-06 · **Commit:** `e8de0b7`+ (phase 19 complete; `T-1807-bug-02`, `T-1904`) · **Latest release:** `v3.0.4` · **Deployed:** `3.0.4+46+gcceb795`

Companion documents: [`status-matrix.md`](status-matrix.md) (the per-feature audit grid and its method) and [`datasheet.md`](datasheet.md) (shipped capability, for external readers).

---

## 1. Headline

vnprox is **feature-complete against three shipped arcs and materially under-validated against real hardware.**

| Dimension | Complete | Basis |
|---|---|---|
| **Feature delivery** | **92%** | 145 of 157 feature cards shipped |
| **Backend implementation** | **97%** | 68 of 77 feature areas complete, 6 partial, 3 not started |
| **GUI coverage** | **99%** | 26 of 26 screens, all with help; 1 open navigation defect |
| **API surface** | **100%** | Contract frozen at v3.0, additive-only since |
| **Docs currency** | **100%** | `features.md` refreshed 2026-08-06 (`T-2107`) |
| **Automated test gate** | **100%** | `make ci` green locally: 4,058 tests, lint, vet, arm64 cross-build, 7 fuzz targets, package. **GitHub Actions is unfunded and runs nothing** — the gate is the dev host |
| **E2E gate** | **required** | Gate landed 2026-08-06, green and blocking 2026-08-07. **29 failed / 59 passed → 89 passed / 0 failed / 2 skipped**, run time 29.6m → ~10m. **Ten real product defects** fixed along the way, four of them with green unit tests sitting on fixtures that invented the shape the code expected (`T-2108`, closed) |
| **Hardware validation** | **5%** | **6 of 123 items validated on real PVE** |

The first six rows describe a mature product. The last two are why the current arc exists. **The gap between "our tests pass" and "this works on your cluster" is the project's dominant risk, and it is not shrinking quickly** — five of the six validated items were validated a day ago; before that the number was one.

---

## 2. Arc and phase status

| Arc | Phases | Target | Feature cards | Done | State |
|---|---|---|---|---|---|
| 1 — Visual network manager | 0–7 | v1.0 | 49 | 49 | ● Shipped |
| 2 — Beyond the cluster | 8–12 | v2.0 | 37 | 37 | ● Shipped |
| 3 — Universal networking tool | 13–17 | v3.0 | 35 | 35 | ● Shipped |
| 4 — **Proven, not just built** | 18–21 | v3.1 → v4.0 | 26 | 14 | ◐ **54%** |
| 22 — Online help | 22 | — | 5 | 5 | ● Shipped (unreleased) |
| 23 — Certificate management | 23 | — | 5 | 5 | ● Shipped (unreleased) |
| **Total** | | | **157** | **145** | **92%** |

Defect cards (`*-bug-*`) sit outside this table; four are open — see §3. `T-1807-bug-01` closed
2026-08-06 (by `T-1807-bug-02`), as did `T-2106` and `T-2107`.

### Arc 4 detail — the only arc in flight

| Phase | Theme | Cards | Done | Open |
|---|---|---|---|---|
| 18 | Prove it on hardware | 8 | 4 | `T-1802`, `T-1803`, `T-1804`, `T-1808` — **all human-blocked** |
| 19 | Operate it | 7 | 7 | — **complete 2026-08-06** |
| 20 | Finish the product | 6 | 3 | `T-2004` (a11y pass 2), `T-2005` (PWA), `T-2006` (i18n) |
| 21 | Distribute it | 5 | 0 | `T-2101`…`T-2105` — none started |

**Phase 21 has not begun.** Its dependency (`T-1902`, support bundles) shipped, so it is unblocked — but it is also the arc's release line: Terraform/Ansible artifacts, a signed apt repository, a compatibility matrix, a hosted registry, and community distribution. Nothing external can consume vnprox until some of it lands.

---

## 3. Open items, ranked

Ranked by *what it costs to leave this alone*, not by effort.

### P0 — blocking a trustworthy release

| # | Item | Card | Why it blocks | Who can do it |
|---|---|---|---|---|
| 1 | **Hardware-validation burndown** — 117 unvalidated items | `T-1802` | Every behavioural claim rests on a mock | **Human only** (needs real PVE) |
| 2 | **Multi-node proof**: apply, distributed rollback, drift, federation, HA failover | `T-1803` | The product's core safety guarantee is unproven where it matters | **Human only** (needs 2+ nodes) |
| 3 | **Failure-injection proof of commit-confirm** | `T-1804` | "It rolls back if it cuts you off" has never been observed doing so on hardware | **Human only** |
| 4 | ~~No `LICENSE` file~~ | `T-2106` | **Closed 2026-08-06** — Apache-2.0, with `NOTICE`, a generated `THIRD-PARTY-LICENSES.md`, Debian `copyright`, and a test that fails if any of them is dropped | Done |
| 5 | ~~E2E suite runs in no gate~~ → ~~triage the backlog~~ | `T-1806-bug-01` → `T-2108` | **Closed 2026-08-07: 29 failed / 59 passed → 89 passed / 0 failed**, and the job is required. Ten real product defects found and fixed, including a feature that returned 400 to every browser request since it shipped, an app-wide navigation dead end, and two SDN wizards reading a field shape the API has never sent | Done |
| 6 | **Packaging matrix red** (`cluster-ssh`) | `T-1806-bug-02` | Cannot tag a release with a red pipeline in good conscience | Agent-completable |

### P1 — user-visible or operationally important

| # | Item | Card | Note |
|---|---|---|---|
| 7 | ~~Nav-rail dead-end from the Graph view~~ | `T-2003-bug-01` | **Closed 2026-08-07** — an infinite render loop in `HistoryTimeline` starved react-router v7's navigation transition, so the URL changed and the page never did. Not the inspector, as reported and twice re-reported: the precondition is the Graph view being mounted, which is why the original regression spec passed against the live bug. It was also the single cause of three separate e2e failures |
| 8 | ~~`vnproxctl doctor`~~ | `T-1904` | **Closed 2026-08-06** — ten checks, each with a broken fixture proving it can fail; remediation enforced structurally by `Report.Validate()`, not merely asserted. Four checks await a live-daemon wiring (`T-1904-followup-02`), which is also where the certificate/SAN preflight belongs |
| 9 | ~~`docs/features.md` is stale~~ | `T-2107` | **Closed 2026-08-06** — rewritten to cover all four arcs; the non-goals section now states what is genuinely still out of scope |
| 10 | Accessibility second pass | `T-2004` | Pass 1 shipped (WCAG AA, axe-gated) |
| 11 | Terraform provider + Ansible collection | `T-2101` | API contract and conformance suite already exist |
| 12 | Signed apt repository | `T-2102` | Decision made (GitHub Pages); unimplemented |
| 13 | ~~Test tooling assumes an exclusive machine~~ | `T-1807-bug-01` → `T-1807-bug-02` | **Closed 2026-08-06** — enforced port registry (`testdata/dev-ports.tsv` + `internal/devports` + `make ports`). Proven against the real historical collision: replaying commit `9047685` now fails `make check`. Surfaced 3 unregistered binds nobody had written down |
| 14 | Extend help anchors beyond the 6 placed | `T-2202-followup-01` | 20+ panel topics reachable only via search/index |
| 15 | Field-level inline help in editors | `T-2202-followup-02` | `change-management.md` §5 asks for it |

### P2 — wanted, cut first

| # | Item | Card |
|---|---|---|
| 16 | PVE compatibility matrix + automated compat testing | `T-2103` |
| 17 | Hosted blueprint/plugin registry | `T-2104` |
| 18 | Mobile PWA with push | `T-2005` |
| 19 | Localization (German first) | `T-2006` |
| 20 | Community distribution + docs site | `T-2105` |
| 21 | Standalone map SVG/PNG export | *flagged, T-607* |

---

## 4. What is genuinely strong

Worth stating plainly, because an audit that only lists gaps misrepresents the artifact.

- **Every safety invariant the product claims has a test whose failure is loud.** An AI operator cannot apply a change — not by policy but because adding a mutating tool name makes a guard test *panic*. A plugin's capability scope is a ceiling the installer enforces. Peer TLS cannot silently fall back to the system trust pool. Certificate scanning cannot read a private key, because the type has nowhere to put one. See `status-matrix.md` §4 — eleven invariants, none failing.
- **93% of Go packages and 100% of web feature modules have tests**, and the five untested packages are all mock servers. 4,058 automated tests, 0.82 test:prod LOC ratio on the backend.
- **The v3.0 API contract has held.** Frozen, additive-only, and the one change that removed a frozen field was caught at review and reworked with regression guards added.
- **Documentation is unusually honest.** Simplifications are labelled as simplifications (firewall resolve order, firewall-log rule correlation, the simulator's fourth "indeterminate" verdict). Partial implementations say they are partial. `needs-hardware-validation.md` exists at all, which most projects skip.
- **Recent process discipline held under pressure.** A speculative CI fix was written, failed to reproduce at three sizes, and was **reverted rather than shipped**. A vacuous secret scan was caught by its own author and redone with a control. A wrong reading of certificate SANs was corrected in the commit message rather than quietly dropped.

---

## 5. Trajectory and recommended sequence

### 5.1 The one thing that changes the risk profile

Arc 4 is titled *"proven, not just built"* and is **54% complete by card count but ~5% complete by its own premise.** Phases 19 and 20 delivered agent-completable operability work — phase 19 is now **complete** — but phase 18's four hardware cards, the reason the arc exists, remain untouched because no agent can touch them.

`T-1801` shipped the machinery for this: eight harness scripts, an evidence schema, and a standalone runbook at `planning/validation/README.md`, designed to cost roughly **eight human turns rather than sixty**. It has been ready since before the last three phases were built.

### 5.2 Recommended order

Revised 2026-08-06. Items 1, 4, and half of 3 from the previous list are done; what remains is
almost entirely the same shape as before, which is the point — the agent-completable backlog keeps
shrinking while the hardware number does not move.

1. **Run the phase-18 validation loop** (`T-1802`, then `T-1804`). **This is now the only item on
   this list that improves the headline number, and the only one no agent can do.** Turns ~100
   mock-validated claims into evidence.
2. ~~**Triage the e2e backlog** (`T-2108`) and make the gate blocking.~~ **Done 2026-08-07** — the
   suite is green and the job is required.
3. **Explain `T-1806-bug-02`.** The packaging matrix is what proves the `.deb` installs; a release
   cut over an unexplained red signal is the habit `T-1806-bug-01` exists to break.
4. **Phase 21**, starting with `T-2102` (signed apt repo) and `T-2101` (Terraform/Ansible) — the two
   items that make vnprox consumable by anyone who is not building it. Now unblocked in every
   respect: its licence dependency closed with `T-2106`.
5. Multi-node work (`T-1803`) whenever a second node exists.

Closed since the previous revision: the licence decision (`T-2106`), the e2e gate landing
(`T-1806-bug-01`), `docs/features.md` (`T-2107`), the port-collision class (`T-1807-bug-02`), and
`vnproxctl doctor` (`T-1904`).

### 5.3 Release readiness

| Cut | Ready? | Blocking |
|---|---|---|
| `v3.1` (help + certificates + doctor) | **Nearly** | `Packaging matrix` red (`T-1806-bug-02`) is the only remaining blocker; the e2e suite is green and required |
| `v4.0` (arc 4 complete) | No | Phase 21 not started; phase 18 unvalidated |
| Public/community release | No | ~~No license~~ (closed); no signed repository; no compatibility matrix |

A `v3.1` tag is defensible today once `T-1806-bug-02` is understood. `T-2003-bug-01` is fixed (2026-08-07).

---

## 6. Changes since the last status snapshot

| Date | Change |
|---|---|
| 2026-08-05 | Deploy-time validation: **5 items moved from unvalidated to validated** (CA path, migration chain, `backup`, `support-bundle` redaction, pmxcfs permissions) |
| 2026-08-05 | `T-1906-bug-01` filed — pinned peer TLS vs. a stale IP SAN, found on hardware |
| 2026-08-05 | Phase 22 shipped: online help on all 26 screens, with an enforced coverage gate |
| 2026-08-06 | Phase 23 shipped: certificate management, and `T-1906-bug-01` **fixed** |
| 2026-08-06 | This audit: `LICENSE` gap and `docs/features.md` staleness identified — neither previously tracked |
| 2026-08-06 | `T-2106` (Apache-2.0 + attribution) and `T-2107` (`features.md`) closed; e2e gate landed observe-only, `T-2108` filed |
| 2026-08-07 | `T-2108` and `T-1806-bug-01` closed: e2e suite green (89 passed / 0 failed) and the job is required. `T-2003-bug-01` root-caused and fixed. Ten product defects found by the gate, four of which had green unit tests over invented fixtures |
| 2026-08-06 | `T-1807-bug-02`: enforced port registry closes the collision class that had recurred five times in one phase. Also eliminated two candidate explanations for `T-1806-bug-02` (recorded on that card so they are not re-derived) |
| 2026-08-06 | `T-1904` (`vnproxctl doctor`) shipped — **phase 19 complete**. Ten checks, remediation structurally enforced; two follow-ups filed rather than left implicit |
