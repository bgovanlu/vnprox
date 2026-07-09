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
describe="$(git -C "$repo_dir" describe --tags --always --dirty 2>/dev/null || true)"

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
