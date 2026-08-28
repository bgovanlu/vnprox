# Security policy

## Reporting a vulnerability

**Use GitHub's [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)** —
the "Report a vulnerability" button on this repository's Security tab. It is
**enabled and confirmed working** (`gh api
repos/bgovanlu/vnprox/private-vulnerability-reporting` returns
`{"enabled":true}`). Include what you found, the affected version
(`vnproxd --version`), and reproduction steps. Please don't open a public
GitHub issue for a suspected vulnerability — file it privately first so a
fix can ship before the details are public.

> **Email is currently NOT a working channel.** `security@vnprox.com` is
> published in several places in this project's history, but `vnprox.com`
> **has no MX record** (verified 2026-08-27: `dig MX vnprox.com` returns
> nothing; the zone exists on GoDaddy nameservers and the A records are
> parking IPs). Mail to that address will not be delivered. It is named here
> only so that anyone who finds the address in an older document knows not to
> rely on it. This is tracked with the rest of the project's DNS gap — see
> `planning/roadmap-open-source.md`; `docs.vnprox.com`, `apt.vnprox.com` and
> `registry.vnprox.com` do not resolve either.

You should get an acknowledgment within a few business days. This is a
single-maintainer project (see `docs/support.md`), so "acknowledged" means
a human read it and is looking, not that a fix has a committed date —
severity and reproducibility drive triage order, stated plainly rather than
promised as an SLA that wouldn't be honest.
`docs/security-disclosure-process.md` documents what happens after a report
lands, end to end.

## Supported versions

The latest release only — see [GitHub Releases](https://github.com/bgovanlu/vnprox/releases) for
what that is right now. `docs/deployment.md`'s upgrade flow (`apt-get
update && apt-get install vnprox`) is how you get a fix — there is no
backport process for older versions. This is a stated limit, not an
aspiration: a single-maintainer project (`docs/support.md`) cannot credibly
promise parallel maintenance of multiple release lines, and a security
policy that claimed one would be exactly the kind of enterprise-sounding
commitment this project shouldn't write checks it can't cash.

## Coordinated disclosure

A report gets fixed privately before it gets fixed publicly. The short
version: draft advisory → private fix branch → coordinated release → public
advisory. `docs/security-disclosure-process.md` has the full workflow,
including what "coordinated" means for a solo maintainer with no funded
CI (T-3301) — worth reading before you report if you have your own
disclosure-timeline expectations, so neither side is surprised by the
other's.

## Scope

In scope: `vnproxd`, `vnproxctl`, the web UI, and the packaging/install
tooling in this repository. Proxmox VE itself is out of scope — report
issues in PVE's own networking, firewall, or SDN stack to Proxmox
(`security@proxmox.com`), not here; `docs/security.md` documents where the
boundary between vnprox and PVE actually sits if it's unclear which side a
finding is on.

See `docs/security.md` for the full security design (authentication,
authorization, transport, the threat model) and `docs/support.md` for how
this project handles support requests generally.
