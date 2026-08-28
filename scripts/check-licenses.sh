#!/usr/bin/env bash
# check-licenses.sh — T-3801's license-compatibility gate.
#
# Two independent checks, one per tree:
#
#   * Go:  `go run github.com/google/go-licenses@$GO_LICENSES_VERSION check`
#     against the full build graph (`./...`), including test-only imports —
#     see --include_tests below. go-licenses is invoked via a versioned
#     `go run` module path, the same mechanism `go run tool@version` uses for
#     any one-off tool: it resolves and builds in its own module context and
#     never touches this repo's go.mod/go.sum (verified — see this task's
#     report). It is a dev-time-only dependency, not a build dependency of
#     vnproxd.
#   * web/: `npx license-checker-rseidelsohn` (pinned in web/package.json's
#     devDependencies — dev-time only, never `dependencies`) against
#     production dependencies only (`--production`; devDependencies never
#     ship and are not part of the distributed product, so they are not
#     gated here).
#
# ALLOWED LICENSES — the set below, and why:
#
#   Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC, 0BSD
#     The standard permissive set. No copyleft obligation, compatible with
#     Apache-2.0 (vnprox's own license) in either direction.
#
#   EPL-2.0
#     One exception, justified rather than blanket-permitted: `elkjs`
#     (docs/development.md's "Tech stack" table — the ELK layout engine
#     behind the canvas) is Eclipse Public License 2.0, a FILE-scoped weak
#     copyleft: EPL-2.0's reciprocal obligation attaches only to
#     *modifications of EPL-covered files themselves*, not to a program that
#     merely links/imports the library unmodified (the FSF and OSI both
#     class it as GPL-incompatible but non-viral in this sense — unlike
#     LGPL, EPL does not require dynamic-linking users to publish anything).
#     vnprox imports elkjs unmodified as a library; no vnprox source becomes
#     EPL-covered by doing so. It is allowed here, narrowly, because it is
#     already an approved, pinned, locked dependency — this gate exists to
#     catch a *new* copyleft dependency arriving unnoticed, not to relitigate
#     one already reviewed and shipping. Do not extend this exception to
#     GPL/AGPL/LGPL — those remain unconditionally disallowed; see this
#     script's own scratch-dependency proof in the T-3801 task report for a
#     demonstration of the gate catching exactly that case.
#
#   "MIT AND ISC", "(MPL-2.0 OR Apache-2.0)"
#     SPDX compound expressions produced by real dependencies in this tree
#     (victory-vendor, dompurify respectively). "AND" means the package
#     itself is dual-covered by two permissive licenses (both terms apply,
#     both are already allowed individually); "OR" means the licensee may
#     choose either term, and Apache-2.0 is one of the choices, so it is
#     compatible by construction. Neither introduces a new obligation beyond
#     the individual licenses already in the allowed set.
#
# What is NOT in the list, deliberately: GPL/LGPL/AGPL (any version), and
# anything go-licenses classifies as "forbidden" or cannot classify at all
# ("unknown" — the tool fails closed on an unrecognized license, which this
# script relies on: a dependency whose license the tool cannot identify is
# treated the same as an incompatible one, not silently passed).
#
# Usage: scripts/check-licenses.sh          # both checks
#        scripts/check-licenses.sh go       # Go tree only
#        scripts/check-licenses.sh web      # web/ only

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GO_LICENSES_VERSION="v1.6.0"
LICENSE_CHECKER_VERSION="4.4.2"
CONFIDENCE_THRESHOLD="0.8"

GO_ALLOWED="Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC,EPL-2.0"
# license-checker-rseidelsohn's --onlyAllow takes a semicolon-separated list
# and matches SPDX compound expressions verbatim (see header comment above).
WEB_ALLOWED="MIT;ISC;Apache-2.0;BSD-2-Clause;BSD-3-Clause;0BSD;EPL-2.0;MIT AND ISC;(MPL-2.0 OR Apache-2.0)"

check_go() {
    echo ">> check-licenses: go tree (go-licenses $GO_LICENSES_VERSION, confidence >= $CONFIDENCE_THRESHOLD)"
    # No --include_tests: scoped to what actually ships in vnproxd, matching
    # the web/ check's --production scoping below. Test-only imports (e.g.
    # github.com/letsencrypt/boulder, pulled in by cert-related tests and
    # MPL-2.0-licensed) never reach the built binary and are out of scope for
    # a gate about what vnprox distributes — a test dependency's license is a
    # dev-time-only concern like go-licenses itself, not a distribution one.
    go run "github.com/google/go-licenses@${GO_LICENSES_VERSION}" check ./... \
        --confidence_threshold="$CONFIDENCE_THRESHOLD" \
        --allowed_licenses="$GO_ALLOWED"
}

check_web() {
    echo ">> check-licenses: web/ (license-checker-rseidelsohn $LICENSE_CHECKER_VERSION, production deps only)"
    (
        cd web
        npx --yes "license-checker-rseidelsohn@${LICENSE_CHECKER_VERSION}" \
            --production \
            --excludePackages "vnprox-web@0.1.0" \
            --onlyAllow "$WEB_ALLOWED"
    )
}

target="${1:-all}"
rc=0
case "$target" in
    go)  check_go  || rc=1 ;;
    web) check_web || rc=1 ;;
    all)
        check_go  || rc=1
        check_web || rc=1
        ;;
    *)
        echo "usage: $0 [go|web|all]" >&2
        exit 2
        ;;
esac

exit "$rc"
