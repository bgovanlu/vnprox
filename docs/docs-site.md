# The vnprox docs site (T-2105)

How `docs/` is meant to be published as a browsable, versioned site, and what that takes to
actually stand up. Companion to `packaging/apt-repo.md` and `docs/hub-registry.md` — same posture
deliberately: **static hosting, no service to operate** — and this document follows their
"Status: what exists and what does not" convention rather than describing the target state as if
it were live.

## What the site is

`docs/` doubles as both the source-of-truth documentation (read directly on GitHub, as it always
has been) and the source for a browsable site: `docs/README.md` is the landing page, `docs/*.md`
are the pages, and `docs/_sidebar.md` is the reader-oriented navigation tree described below.
`docs/index.html` is a small, dependency-free loader ([docsify](https://docsify.js.org/), pulled
from its public CDN at view time by the visitor's browser — nothing is fetched or built at publish
time) that renders those Markdown files as a site: no static-site generator, no build step, no new
language toolchain in this repository, and nothing added to `go.mod` or `package.json`. A visitor
gets syntax-highlighted pages, full-text search, and the sidebar nav; a contributor still just
edits Markdown.

This is deliberately the lightest mechanism that satisfies "docs site built from `docs/`, static,
consistent with T-2102's [apt repo] posture": GitHub Pages' own "deploy from a branch" mode can
serve this repository directly, with no CI job and nothing this repository's frozen workflow files
need to do (see "Versioning" below for exactly which Pages source setting makes the site's
relative links resolve).

## Reader-facing structure

The card's framing is "organized for a reader, not a contributor." `docs/_sidebar.md` groups pages
that way:

- **Start here** — `docs/README.md` (site home), `docs/install.md`, `docs/first-hour.md`
- **Guides** — task-oriented sections of `docs/user-guide.md`, `docs/deployment.md`'s
  Upgrade/Troubleshooting sections
- **Reference** — `docs/datasheet.md`, `docs/features.md` (+ `docs/features/`), `docs/api.md`,
  `docs/data-model.md`, `docs/architecture.md`, `docs/security.md`, `docs/compatibility.md`
- **Community** — `docs/support.md`, `../CONTRIBUTING.md`, `docs/community-repo-assessment.md`,
  `docs/forum-announcement.md`
- **Project** — `docs/project-status.md`, `docs/roadmap.md` and siblings, `../CHANGELOG.md`

The existing corpus (contributor-dense documents like `docs/architecture.md` and
`docs/data-model.md`) is **linked from Reference, not rewritten** — restructuring the *entry path*
so a reader lands on install → first hour → guides before reference is what this card asked for;
rewriting documents another task card owns (`docs/development.md`, `docs/security.md`,
`docs/api.md`, `docs/compatibility.md`) is out of this one's scope and would risk drifting from
what those documents say.

## Versioning

"Versioned per release so an operator reads the docs matching their install" (the card's own
wording) needs one of two things this repository does not have today:

1. **A CI job that snapshots `docs/` into a per-tag path** (`vX.Y.Z/`) on every release tag, the
   same shape `release.yml` already uses for `docs/openapi.json` and the compatibility matrix
   (see its "Stamp and publish contract artifacts" step). This is not implemented here: the task
   that assigned this document explicitly excludes `.github/workflows/*` from its scope (another
   change just annotated those three files), and GitHub Actions is disabled for this repository
   regardless (billing exhausted since 2026-08-11 — see `release.yml`'s own header comment) so a
   workflow added today would not run anyway.
2. **GitHub Pages enabled for this repository**, deployed from `main`'s repository **root**
   (Settings → Pages → "Deploy from a branch" → `main` → `/ (root)`), which serves the whole
   checkout and puts the site at `.../docs/`. Root, not `/docs`, deliberately: `docs/_sidebar.md`
   links to `../CONTRIBUTING.md` and `../CHANGELOG.md`, which sit one level above `docs/` on disk.
   A `/docs`-scoped Pages source would 404 on both; whole-repo source serves them at the same
   relative paths the links already use, so nothing in the Markdown needs to change to accommodate
   whichever a repository admin picks — it only needs to be the whole-repo option. **Done** (as of
   2026-08-19, verified with `gh api repos/:owner/:repo/pages`): `status: "built"`,
   `source: {branch: "main", path: "/"}` — exactly the whole-repo-root option above — bound to the
   custom domain `docs.vnprox.com` (`cname: "docs.vnprox.com"`, `https_enforced: false`). This was a
   one-time repository-settings change, not a docs-authoring one, so nothing in this working tree
   changed to make it happen; it is recorded here only so this document stops describing a switch
   that has already been flipped as if it hadn't been.

Item 2 now exists; item 1 still does not (GitHub Actions remains disabled for this repository —
billing exhausted since 2026-08-11, see `release.yml`'s own header comment — so a CI job to
snapshot `docs/` per release tag would not run even if added). So "the docs site" today means: the
material in `docs/` is live, publicly reachable, and reflects whatever is on `main` — i.e.,
"latest," not a version picker; there is still no per-release version a reader can pin to. A
worked-out version-switch mechanism (what `docs-versions.json` would look like, where each snapshot
would live) is future work, not specified here, because specifying it without the CI job behind it
would be exactly the kind of aspirational-as-if-live description this task was told not to produce.

## What was verified

- Every internal link this restructure adds was checked to resolve to a real file in this working
  tree (`docs/README.md`, `docs/install.md`, `docs/first-hour.md`, `docs/support.md`,
  `docs/community-repo-assessment.md`, `docs/forum-announcement.md`, `docs/_sidebar.md`,
  `docs/index.html`, and the pre-existing `docs/*.md` / `docs/features/*.md` files it points into)
  — checked by resolving each relative path against the tree, not by rendering the site (no
  network access to a CDN, and no Pages instance, is available in this environment either).
- `docs/index.html`'s docsify config was written against docsify's documented `window.$docsify`
  options (`name`, `loadSidebar`, `homepage`, `search`) — a config shape, not a claim that it has
  been rendered here. `repo` (docsify's GitHub-corner link) is deliberately omitted; see the
  comment in `docs/index.html` itself.
- **Site-live status (2026-08-19):** `gh api repos/:owner/:repo/pages` — read-only, GitHub's own
  record of the Pages deployment, not a render check from inside this working tree (which still has
  no network path to the CDN or to `docs.vnprox.com` itself). `status: "built"`,
  `source: {branch: "main", path: "/"}`, `cname: "docs.vnprox.com"`.

## Status: what exists and what does not

- **Exists in this repository:** the reader-oriented restructure (`docs/README.md`,
  `docs/install.md`, `docs/first-hour.md`, `docs/support.md`,
  `docs/community-repo-assessment.md`, `docs/forum-announcement.md`), the zero-build site loader
  (`docs/index.html`, `docs/_sidebar.md`), and this document.
- **Exists and is live:** GitHub Pages is enabled for this repository and serving the site today,
  at the custom domain `docs.vnprox.com` — confirmed via `gh api repos/:owner/:repo/pages`
  (`status: "built"`, `source: {branch: "main", path: "/"}`), not merely configured-but-unbuilt.
  This corrects an earlier version of this document, which said Pages was not enabled; it was
  written before the repository went public (2026-08-18) and before Pages was switched on.
- **Does not exist yet:** there is no CI job that snapshots `docs/` per release tag, for the
  reasons above (GitHub Actions remains disabled for this repository). There is therefore no
  *versioned* docs site an operator can pin to a specific release — the live site always reflects
  whatever is currently on `main`.
