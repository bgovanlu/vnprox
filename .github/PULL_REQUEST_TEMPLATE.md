<!--
Thanks for the PR. A few things that make this easy to review, per
CONTRIBUTING.md:

- Commit style: `area: imperative summary` (e.g. `change-engine: add
  commit-confirm timer`).
- Every commit needs a `Signed-off-by:` trailer (`git commit -s`) — see
  CONTRIBUTING.md's "Developer Certificate of Origin" section for why and
  how. `scripts/ci-local.sh dco` checks this locally; the same check runs
  in CI once Actions is funded again (see docs/development.md's CI section
  for why it isn't today).
- Keep a PR to one logical change. Large or architectural changes are
  easier to review as an issue first, then a PR.
-->

## What this changes and why

<!-- The "why" matters more than the "what" here — the diff already shows the what. -->

## How this was tested

<!--
`make check` (or the relevant scripts/ci-local.sh job) is the real gate —
GitHub Actions is unfunded for this repo (docs/development.md's CI
section), so a green CI check on this PR does not mean what it would on a
funded repo. Say what you actually ran and what it showed.
-->

- [ ] `make check` passes locally
- [ ] Relevant table-driven Go tests / Vitest specs added or updated
- [ ] If this touches `internal/pve` or models a PVE object/endpoint: a
      `pvesh usage`/`pvesh get` transcript exists (in
      `planning/reports/evidence/` if this is an agent contribution — see
      CLAUDE.md's rule against modeling PVE objects from docs or the mock
      server alone) or this PR is against `internal/pvemock` fixtures only

## Related task card / issue

<!-- planning/tasks/... if you're working from one, or the GitHub issue this closes -->

## Anything the reviewer should know

<!-- Deviations from a task card and why, known gaps, follow-up needed, etc. -->
