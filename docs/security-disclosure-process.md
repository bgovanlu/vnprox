# Coordinated disclosure process

This is the workflow [`SECURITY.md`](../SECURITY.md) points to: what actually happens between a
report landing and a fix being public. It exists so a reporter knows what to expect and a future
maintainer (if this ever stops being a single-maintainer project — see `docs/support.md`) has a
process to follow rather than improvising one under pressure.

Written the same way the rest of this project's docs try to be written: concrete steps, and no
promise this project can't keep. There is no security team, no SLA, and no legal department behind
this — see "What this project can and can't promise," below, before reading anything above it as a
commitment.

## The channels, restated from `SECURITY.md`

**One way in, because only one works today.**

**GitHub private vulnerability reporting** — confirmed enabled on `bgovanlu/vnprox`
(`gh api repos/bgovanlu/vnprox/private-vulnerability-reporting` → `{"enabled":true}`). Use the
"Report a vulnerability" button under the repo's Security tab, with what you found, the affected
version (`vnproxd --version`), and reproduction steps. This path creates a private fork
automatically and keeps the whole exchange — discussion, patch, advisory — in one GitHub-native
place.

> **`security@vnprox.com` does not work and must not be offered as a second channel.** `vnprox.com`
> has **no MX record** (verified 2026-08-27: `dig MX vnprox.com` returns nothing; the zone exists
> on GoDaddy nameservers, the A records are parking IPs). Mail sent there is not delivered. An
> earlier draft of this document listed it as channel 1 of 2 — on a public repository, that is worse
> than publishing no address at all, because a reporter who uses it believes they have disclosed
> responsibly while nobody has received anything. Restoring email as a channel requires MX records
> and a mailbox, which is part of the same deferred DNS work as `docs.`/`apt.`/`registry.vnprox.com`
> (see `planning/roadmap-open-source.md`).

Do not open a public issue for a suspected vulnerability.

## The workflow

### 1. Acknowledge

The maintainer reads the report and replies — "acknowledged" means a human is looking, not that a
fix has a committed date (see `SECURITY.md`'s note on why this project won't promise an SLA it
can't honor). Best-effort, typically within a few business days.

### 2. Triage and reproduce

The maintainer reproduces the issue (or asks for what's needed to). Severity and reproducibility
drive triage order — a report with clear repro steps and a plausible impact gets looked at before a
vague one, regardless of arrival order. If the report turns out to be about Proxmox VE itself
rather than vnprox (`SECURITY.md`'s "Scope" section has the boundary), the reporter is redirected to
`security@proxmox.com` rather than silently dropped.

### 3. Draft advisory, privately

For anything confirmed as a real vulnerability, a **GitHub Security Advisory** is drafted in draft
state (private by construction — not visible until published). This is where the write-up lives:
affected versions, severity (CVSS if it's meaningful for the finding), and the eventual public
description. If the finding warrants a CVE, GitHub's advisory flow can request one directly from
the draft — there's no separate process to run for that.

### 4. Private fix branch

The fix is developed on a private branch tied to the advisory (GitHub's private-fork-per-advisory
mechanism, reached from the draft advisory's "Start a temporary private fork" action) — never on a
public branch or in a public PR, which would leak the vulnerability before a fix exists. Given this
project's actual CI situation (`docs/development.md`'s CI section — Actions is retired, not paused,
per T-3301), the fix is validated the same way every other change here is: `scripts/ci-local.sh`
(or `make ci`/`make check`) run locally against the private branch. There is no hosted CI to
validate a private security fork even if there were funded Actions minutes, since GitHub Actions on
a private security advisory fork has its own separate constraints — local validation is not a
compromise made for this workflow specifically, it's the same gate every other change in this repo
already goes through.

### 5. Coordinated release

Once the fix is validated:

- The fix lands on `main` (a normal, reviewed commit — the advisory stays in draft until this
  step, so the commit itself doesn't have to spell out "this fixes a security issue" in a way that
  tips off anyone watching commit history before the advisory is ready).
- A release is cut following the normal release process (`docs/development.md`'s CI section:
  build the `.deb` from a clean worktree at the tag, run the apt-repo publish step manually since
  `release.yml` doesn't run today).
- **The advisory is published at the same time the fixed release becomes available**, not before —
  publishing the advisory first would tell an attacker exactly what to look for in a codebase that
  doesn't have a fix out yet. "Coordinated" means the disclosure and the fix's availability move
  together.

### 6. Public advisory and credit

The advisory is published (GitHub Security Advisories, cross-posted to the CVE if one was
requested). The reporter is credited by name unless they ask not to be — say so in the initial
report if you'd rather stay anonymous.

## Embargo length — stated honestly

There is no fixed embargo period (e.g. "90 days") promised here, because a fixed number implies a
process that can reliably hit it, and a single maintainer with no funded CI and no backup on-call
cannot honestly promise that. In practice: severity drives urgency, a straightforward fix ships in
days, and a reporter who needs a firm date for their own disclosure timeline (a bug bounty program,
an employer's policy, a conference talk) should say so in the initial report — that context changes
triage order and is worth stating explicitly rather than assumed.

## What this project can and can't promise

Restated plainly, because a security policy that reads like it came from a company with a security
team is a security policy this project can't actually back:

- **Can promise:** the two report channels above are real, reach a real person, and reports are not
  ignored. Fixes for the latest release only (`SECURITY.md`'s "Supported versions" — no backport
  process for older releases). The private-fix-branch → coordinated-release → public-advisory
  sequence above, followed in that order, every time.
- **Cannot promise:** a fixed acknowledgment or fix-time SLA, a dedicated security team, 24/7
  on-call, backported fixes to older releases, or a bug bounty. This is the same honesty
  `docs/support.md` already applies to ordinary support requests, extended to security reports.

## Cross-references

- [`SECURITY.md`](../SECURITY.md) — the entry point: where to report, scope, supported versions.
- [`docs/security.md`](security.md) — the security *design* (authN/authZ, transport, threat model,
  what's encrypted at rest) — not the disclosure process this document covers.
- [`docs/support.md`](support.md) — the same "single-maintainer, best-effort" framing applied to
  ordinary (non-security) support requests.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md)'s "Reporting a security issue" section — the contributor-
  facing pointer into this document.
