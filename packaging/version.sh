#!/usr/bin/env bash
# Prints the Debian package version to use for this build.
#
# Kept as a standalone script (rather than inline in the Makefile) because
# embedding a shell `case` statement's parentheses directly inside a
# GNU Make $(shell ...) call confuses Make's own paren-matching for the
# function call.
#
# Debian version fields must start with a digit, so a bare commit hash
# (what `git describe --always` returns when there are no tags yet) isn't
# usable as-is.
set -euo pipefail

repo_dir="${1:-.}"
describe="$(git -C "$repo_dir" describe --tags --always 2>/dev/null || true)"

# Append "-dirty" ourselves (rather than `git describe --dirty`, which
# checks the whole working tree) excluding web/dist: hardware validation
# (T-608) found every real release — including the actual published
# v1.0.0 GitHub release — got tagged "+dirty" in its version string and
# .deb filename, because `make build`'s `npm run build` step (which
# release.yml and `make deb` both now always run first) legitimately
# rewrites the git-tracked web/dist/index.html placeholder (see
# .gitignore's comment on why it's tracked at all) with real Vite output.
# That's expected, intentional churn in a build artifact, not an unclean
# checkout — excluding it here is what makes a real, clean release tag
# actually produce a clean version string again.
if [ -n "$(git -C "$repo_dir" status --porcelain -- . ':!web/dist' 2>/dev/null)" ]; then
	describe="${describe}-dirty"
fi

case "$describe" in
"")
	echo "0.0.0+dev"
	;;
v[0-9]*)
	# Strip a leading "v" and normalize "-" (git describe's
	# <tag>-<n>-g<hash>[-dirty] separator) to "+" for dpkg's version syntax.
	echo "${describe#v}" | tr '-' '+'
	;;
[0-9]*)
	echo "$describe" | tr '-' '+'
	;;
*)
	# No tag at all: a bare short hash (or "<hash>-dirty").
	echo "0.0.0+g$(echo "$describe" | tr '-' '+')"
	;;
esac
