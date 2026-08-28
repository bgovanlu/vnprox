# Your first change

This walks one small, real change from an empty diff to a green `make check`, narrating every
step including the parts that don't work the first time. If you haven't yet, run through
`docs/architecture-tour.md` §1 first so `make dev` is running — you won't need the dev server
running for this particular change (it's backend-only and unit-tested), but you'll want a working
toolchain either way.

**What we're building:** a new, offline check for `vnproxctl doctor` — the preflight/self-check
command (`internal/doctor/`; `docs/deployment.md`'s own words for it: "start here when something is
wrong and you do not yet know what"). `doctor` is a stack of small, independent checks, each one a
pure function over injected fakes, each with its own test — which makes it one of the best-scoped
corners of this codebase for a first PR: you can add one check without touching anything the others
depend on.

**Do not land this exact change.** It's real — it was written, tested, and passed `go test`,
`go vet`, `gofmt`, and `golangci-lint` in a scratch `git worktree` while this page was written — but
it's here to teach the loop, not to ship. If you want a first PR, either submit this one for real
(check it isn't already done) or pick a different check; either way, follow the same steps.

## Where the code lives

- `internal/doctor/doctor.go` — check name constants and `AllChecks` (the registry).
- `internal/doctor/checks.go` — each check is a function `func(Facts, Env) Result` (or without
  `Env`, if it only needs config), wired into `Run()`.
- `internal/doctor/checks_test.go` — the tests. Two of them are **meta-tests that apply to every
  check, not just the one you write** — see step 4, they're the part of this loop that isn't
  obvious from reading `checks.go` alone.
- `cmd/vnproxctl/doctorcmd.go` — populates `Facts`/`Env` from real config/filesystem/network calls
  for the CLI. You will probably **not** need to touch this file (see step 2).

## Step 1 — read the pattern before adding to it

Open `internal/doctor/checks.go` and read `checkPmxcfs` (verifies `/etc/pve` exists and is a
directory — the simplest check in the file) and `checkKeyFiles` (verifies key files exist with safe
permissions — more involved, and worth reading for step 4's gotcha). Every check:

- takes `Facts` (what the config says) and, if it touches the filesystem/network/store, `Env`
  (injected side effects — `Env.Stat` stands in for `os.Stat`, `Env.DiskFree` for a real disk-space
  syscall, and so on) — never calls `os.Stat` or similar directly. This is what makes every check
  independently unit-testable against a fake.
- returns one `Result` via `pass(check, detail)`, `warn(check, detail, remediation)`,
  `fail(check, detail, remediation)`, or `skip(check, reason)`. `warn`/`fail` require a remediation
  string — `Report.Validate()` rejects a `Result` that doesn't have one.

The check we're adding: `f.CaptureRoot` (the directory `vnproxd` writes packet captures into,
`docs/user-guide.md`'s capture feature) is created **lazily**, on the first capture — not at daemon
startup. `testdata/dev.toml`'s `[capture]` section documents the real failure this is aimed at: on
a fresh unprivileged dev host, the first capture ever attempted failed with a bare
`mkdir /var/lib/vnprox: permission denied`, and nothing before that point said why. A `doctor` check
that catches "something's already sitting at this path and it isn't a directory" ahead of time is a
small, real improvement.

## Step 2 — write the check

In `internal/doctor/doctor.go`, add the check name next to `CheckDiskHeadroom` (the check it's
conceptually closest to — both reason about `f.CaptureRoot`) and to `AllChecks` in the same
position:

```go
const (
	...
	CheckDiskHeadroom  = "disk_headroom"
	CheckCaptureRoot   = "capture_root"
)

var AllChecks = []string{
	...
	CheckDiskHeadroom,
	CheckCaptureRoot,
	CheckPortConflict,
	...
}
```

In `internal/doctor/checks.go`, register it in `Run()` right after `checkDiskHeadroom`, and add the
function itself (placed near `checkPortConflict`, following the file's existing top-to-bottom
order):

```go
// checkCaptureRoot verifies the packet-capture directory, if one has already
// been created, is actually a directory. vnprox creates it lazily on the
// first packet capture (internal/capture) rather than at daemon startup, so
// its absence here is the normal pre-first-capture state, not a problem to
// report — only a path that exists but is blocked (e.g. a stray regular
// file left where the directory should be) is something doctor can usefully
// catch ahead of the confusing mkdir failure a user would otherwise see
// mid-capture (see testdata/dev.toml's [capture] comment for the real
// failure this mirrors, hit on a fresh unprivileged dev host).
func checkCaptureRoot(f Facts, env Env) Result {
	if env.Stat == nil {
		return skip(CheckCaptureRoot, "no filesystem probe configured")
	}
	if f.CaptureRoot == "" {
		return skip(CheckCaptureRoot, "capture root not configured")
	}
	info, err := env.Stat(f.CaptureRoot)
	if err != nil {
		return pass(CheckCaptureRoot, f.CaptureRoot+" does not exist yet; vnprox creates it on the first packet capture")
	}
	if !info.IsDir() {
		return fail(CheckCaptureRoot,
			fmt.Sprintf("%s exists but is not a directory", f.CaptureRoot),
			fmt.Sprintf("remove it so vnprox can create it as a directory on the next capture: rm %s", f.CaptureRoot))
	}
	return pass(CheckCaptureRoot, f.CaptureRoot+" is present and a directory")
}
```

`cmd/vnproxctl/doctorcmd.go` already populates `Facts.CaptureRoot` (it feeds `checkDiskHeadroom`
today), so — unlike most new checks — this one needs **no wiring change** there. Worth confirming
that for yourself with `grep -n CaptureRoot cmd/vnproxctl/doctorcmd.go` rather than taking it on
faith; a check silently reading an always-empty field would `skip` forever and no test would notice.

**The mistake I made here, left in on purpose:** my first version treated a missing directory as a
`fail`. That's wrong, but it takes a real test failure to see why — which is step 4's real point.

## Step 3 — write the tests

`internal/doctor/checks_test.go` has one baseline fixture (`healthyFacts()`/`healthyEnv()`) that
every check must pass against, and one table (`TestEachCheckFailsOnBrokenInput`) of one-broken-input
case per check. Add the new path to the baseline's `Stat` map so `checkCaptureRoot`'s "exists and is
a directory" branch gets exercised by the healthy control, not just by the broken-input table:

```go
Stat: statFS(map[string]fakeInfo{
    "/etc/vnprox/keys/session.key": {mode: 0o600},
    "/etc/vnprox/keys/pve-token":   {mode: 0o600},
    "/etc/pve":                     {mode: 0o755 | fs.ModeDir, isDir: true},
    "/var/lib/vnprox/captures":     {mode: 0o750 | fs.ModeDir, isDir: true},
}),
```

Then add one case to `TestEachCheckFailsOnBrokenInput`'s table — a real failure, a capture root that
exists but is a file, not a directory:

```go
{
    name:  "capture root exists but is a file, not a directory",
    check: CheckCaptureRoot,
    mutate: func(_ *Facts, e *Env) {
        e.Stat = statFS(map[string]fakeInfo{
            "/etc/vnprox/keys/session.key": {mode: 0o600},
            "/etc/vnprox/keys/pve-token":   {mode: 0o600},
            "/etc/pve":                     {mode: 0o755 | fs.ModeDir, isDir: true},
            "/var/lib/vnprox/captures":     {mode: 0o644, isDir: false},
        })
    },
    wantStatus: StatusFail,
    wantDetail: "not a directory",
},
```

Run just this package, fast, while you iterate:

```
go test ./internal/doctor/... -run 'TestHealthyInstallPassesEverything|TestRunReportsEveryCheck|TestEachCheckFailsOnBrokenInput' -v
```

## Step 4 — the meta-test that catches the real mistake

There is a second table, `TestEveryCheckHasABrokenFixture`, and it is **not** the same list as the
one you just edited — it's a deliberately independent `mutations` slice, re-deriving coverage by
running each mutation and recording which check moved off `pass`, specifically so a check can't be
proven-broken only in the table that also documents it. Add the matching mutation there too:

```go
func(_ *Facts, e *Env) {
    e.Stat = statFS(map[string]fakeInfo{
        "/etc/vnprox/keys/session.key": {mode: 0o600},
        "/etc/vnprox/keys/pve-token":   {mode: 0o600},
        "/etc/pve":                     {mode: 0o755 | fs.ModeDir, isDir: true},
        "/var/lib/vnprox/captures":     {mode: 0o644, isDir: false},
    })
},
```

Skip this and `TestEveryCheckHasABrokenFixture` fails with `check "capture_root" never left pass
under any broken fixture: nothing proves it can fail (AC1)` — the whole package is enforcing that
every check in `AllChecks` has a *proven* failure mode, not just a plausible one.

Now the mistake from step 2. If you write `checkCaptureRoot` so a **missing** directory is a `fail`
(the naive version — it certainly reads like it should be one), two unrelated tests break the moment
you run the full package, not just this one:

```
go test ./internal/doctor/... ./cmd/vnproxctl/...
--- FAIL: TestSessionKeyAbsentBeforeFirstStartIsNotAFailure
    a freshly installed, never-started node reports Failed()
--- FAIL: TestDoctorSucceedsOnAHealthyEnoughConfig
    check "capture_root" failed on a parseable config; only pmxcfs should fail off a PVE node
```

Both are telling you the same thing from different angles: a fresh install, or any machine that
isn't a real PVE node with a real `/var/lib/vnprox/captures`, legitimately has no capture directory
yet — `checkKeyFiles` already has to solve exactly this ambiguity for the session key (see its
`env.DaemonHasRun()` branch and `TestSessionKeyAbsentBeforeFirstStartIsNotAFailure`'s doc comment).
The fix isn't to copy that pattern here, though — it's simpler: since the directory is genuinely
optional and created lazily, "doesn't exist yet" is `pass`, not `warn` or `fail`, full stop. That's
the version in step 2. **This is the actual lesson:** the fast, narrow test loop in step 3 will lie
to you about being done; the full-package run in this step is what a real reviewer (or `make check`)
will see, and it's where cross-check assumptions get caught.

## Step 5 — the full gate

```
gofmt -l internal/doctor/*.go                                   # must print nothing
go vet ./internal/doctor/... ./cmd/vnproxctl/...
go test ./internal/doctor/... ./cmd/vnproxctl/...                # both packages, not just doctor
golangci-lint run --allow-serial-runners ./internal/doctor/... ./cmd/vnproxctl/...
make check                                                        # the real gate — see below
```

`make check` (`docs/development.md` §"CI") runs `gofmt`, `go vet`, `golangci-lint`, the full Go test
suite, `vitest` (frontend — untouched by this change, but it still runs; a Go-only diff does not
skip the frontend gate), `govulncheck`, and the `npm audit` allowlist check. All of it green is what
"done" means here — not just the two packages above. `docs/development.md`'s "A note on CI" explains
why `make check`/`scripts/ci-local.sh` are the gate that matters rather than a GitHub Actions badge:
Actions is unfunded for this repository, so nothing runs on push.

If you're actually opening a PR (rather than following along): commits need a DCO sign-off
(`git commit -s`, `CONTRIBUTING.md`'s "Developer Certificate of Origin" section) and the commit
message style is `area: imperative summary` — for this change, something like
`doctor: catch a capture-root path that isn't a directory`.

## What a careful contributor also does, that nothing enforces

`docs/deployment.md`'s `vnproxctl doctor` section documents the check list by hand — "doctor runs
ten checks," a table naming each one, and an `--live` count ("8 of 10"). Nothing ties that prose to
`AllChecks`; a check added here without updating that doc simply leaves it wrong, silently, the same
class of drift `CLAUDE.md` warns about elsewhere in this repository. `make check` will not catch it.
Updating it anyway — "eleven checks," a new row, "8 of 11" — is part of finishing this change even
though nothing is red without it.

## Next

Once this is green, you've exercised the real loop — read the pattern, write the code, discover
what the tests actually require, run the full gate — for the smallest shape of change this codebase
has. `docs/architecture-tour.md` is the map for anything bigger.
