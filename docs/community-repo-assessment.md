# Should vnprox pursue inclusion in a Proxmox community repository?

**Recommendation: no, not in the form that exists today.** The reason is architectural, not
priority or effort: the one real candidate (Proxmox VE Helper-Scripts / Community-Scripts) is
built around a model vnprox structurally cannot fit into, and forcing the fit would make vnprox
less trustworthy to install, not more discoverable. This is a considered "no for now," not a
"haven't gotten to it" — revisit only if the premise below changes.

## What "a Proxmox community repository" could mean, checked rather than assumed

Two distinct things get called this, and only one exists:

1. **A Proxmox-operated apt component alongside `pve-no-subscription`/`pve-enterprise`, open to
   third-party packages.** This does not exist. Proxmox VE's own apt repositories
   (`pve-enterprise`, `pve-no-subscription`, `pve-test`) are all first-party mirrors of Proxmox's
   own packages; there is no Debian-backports-style "extra" or "community" component a third
   party can publish into, and no indication one is planned. There is therefore no repository of
   this kind to seek inclusion in — the decision this repository already made (D6, this project's
   own signed apt repository, `packaging/apt-repo.md`) is not a fallback from this option; it is
   the only option in this category.
2. **[Proxmox VE Helper-Scripts](https://community-scripts.org/) (GitHub `community-scripts/ProxmoxVE`,
   development counterpart `community-scripts/ProxmoxVED`).** This is real, active, and the thing
   most people mean by "the Proxmox community repo" — a large, curated collection of shell scripts
   (`ct/AppName.sh`) that a PVE host admin runs with `bash -c "$(curl ...)"`. Assessed below.

## How Helper-Scripts actually works, and why vnprox doesn't fit it

Every script in that project follows one shape: the entry point (`ct/AppName.sh`) runs **on the
PVE host**, but only to provision a **fresh LXC container**; the actual application install runs
**inside that container**, via a paired `install/AppName-install.sh`. The whole design isolates
the deployed application from the host — that's the point of wrapping every app in its own
container rather than installing packages onto the hypervisor directly.

vnprox is the opposite of that shape by design, not by oversight. It has to run **on the PVE host
itself**: it reads and rewrites `/etc/network/interfaces`, PVE's SDN config, and the firewall; it
mints and holds a PVE API token; the whole safety story (protected management interfaces, the
change engine, commit-confirm rollback) only means anything applied against the real host network
stack, not a container's virtual one (`docs/architecture.md`, `docs/security.md`). There is no
version of "wrap vnprox's install in an LXC container" that produces a working vnprox — it would
either (a) install nothing useful inside the container and actually do its work by reaching back
out to configure the host anyway, which is a *worse* trust story than vnprox's own installer
(a third party's script, outside vnprox's release process, granted a path to configure the host),
or (b) not be vnprox at all, just a demo. Neither is worth pursuing.

## Packaging standards

Community-Scripts requires: the "fork-and-pull" GitHub workflow, a PR template, coding standards
published at `community-scripts.org/docs`, and — specifically — that genuinely **new** scripts go
to the `ProxmoxVED` development repo first and are promoted after review, never opened directly
against the stable `ProxmoxVE` repo. None of this is a blocker on its own merits (vnprox already
has a heavier bar: signed packages, a container test matrix, a conformance suite). It's moot here
because the content that would need to pass that review — a host-provisioning script — is the
thing §2 above rules out, not the review process itself.

## Licensing

vnprox is Apache-2.0 (`../LICENSE`); Community-Scripts' own repositories are MIT-licensed. The two
are compatible for the kind of contribution this would be (a short script referencing vnprox's own
installer, not a code merge), so licensing is not the blocker — recorded here because the card
asked for it to be checked, not skipped as obviously fine.

## Maintenance commitment

Even setting the architecture mismatch aside: inclusion in a third-party script index is a
standing commitment to keep that script in sync with vnprox's own install path forever, reviewed
by maintainers who don't hold vnprox's release keys and don't share its release cadence — a
second packaging surface on top of the one this phase already commits to (D6's signed apt repo).
Given the apt repo doesn't exist yet either (`packaging/apt-repo.md`'s own Status section), taking
on a second, harder-to-fit distribution surface before the first one is live is the wrong order of
operations regardless of the architecture question.

## What to do instead

The distribution channel that actually fits vnprox's architecture is the one already decided:
vnprox's own signed apt repository (D6, `packaging/apt-repo.md`) plus visibility through the
Proxmox forum (`forum-announcement.md`) and this docs site. Neither requires reshaping vnprox to
fit someone else's isolation model.

## Caveat on this assessment

This was researched from Community-Scripts' published documentation and contribution guides, not
from a direct conversation with its maintainers — nobody was asked "would you consider a
host-level daemon in scope if we proposed one." That conversation might be worth having someday,
but the architecture mismatch above (an LXC-isolation project, evaluating a project whose entire
value is *not* being isolated from the host) is severe enough that this recommendation doesn't
turn on their answer. Revisit this document if Community-Scripts' scope changes, or if vnprox ever
grows a genuinely container-scoped mode that doesn't need host network/firewall write access
(no such mode exists or is planned today).

Sources consulted: `community-scripts.org/docs`, `github.com/community-scripts/ProxmoxVE` and
`.../ProxmoxVED` (`CONTRIBUTING.md`), and Proxmox's own package-repository documentation
(`pve.proxmox.com/wiki/Package_Repositories`).
