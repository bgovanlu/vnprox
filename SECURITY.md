# Security policy

## Reporting a vulnerability

Email **security@vnprox.com** with what you found, the affected version
(`vnproxd --version`), and reproduction steps. Please don't open a public
GitHub issue for a suspected vulnerability — file it privately first so a
fix can ship before the details are public.

You should get an acknowledgment within a few business days. This is a
single-maintainer project (see `docs/support.md`), so "acknowledged" means
a human read it and is looking, not that a fix has a committed date —
severity and reproducibility drive triage order, stated plainly rather than
promised as an SLA that wouldn't be honest.

If GitHub's own [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)
is enabled on this repository (see the Security tab), that path works too
and keeps the whole exchange, including any patch, in one place.

## Supported versions

The latest release only. `docs/deployment.md`'s upgrade flow (`apt-get
update && apt-get install vnprox`) is how you get a fix — there is no
backport process for older versions.

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
