#!/usr/bin/env bash
# verify-reproducible.sh — T-3806: prove vnprox's own release build is
# byte-reproducible, by actually building it twice and diffing the result,
# rather than asserting it.
#
# Builds the .deb (packaging/Makefile's `deb` target, via the root
# Makefile's `build`+`deb`) from the SAME commit TWICE, each time in its own
# freshly-created, detached git worktree — never in this repo's own working
# tree. Two reasons, both load-bearing:
#
#   1. `git worktree add --detach <dir> <commit>` is the only way to get a
#      build input that is provably identical between the two runs: a
#      worktree checked out from a commit has no uncommitted state to differ
#      by definition, where the ambient working tree does (and this repo's
#      task cards run several agents concurrently in it — see CLAUDE.md).
#   2. It's also what makes "two independent builds" meaningful at all
#      instead of "the same build directory built twice in a row", which
#      would hide any non-determinism that depends on directory contents
#      surviving between builds (stale caches, leftover files an `install`
#      step doesn't overwrite, etc).
#
# Usage:
#   scripts/verify-reproducible.sh                # verify HEAD
#   scripts/verify-reproducible.sh v1.4.0          # verify a tag/commit
#   KEEP_WORKTREES=1 scripts/verify-reproducible.sh
#
# Exit status: 0 if both builds produce byte-identical output, 1 otherwise
# (with the first differing offset reported — see report_diff below), 2 on
# a setup problem (dirty ref, missing tool, build failure).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

REF="${1:-HEAD}"
KEEP_WORKTREES="${KEEP_WORKTREES:-}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/vnprox-repro.XXXXXX")"

die() {
    echo "!! verify-reproducible: $*" >&2
    exit 2
}

# --- helpers (defined before use — this script is executed top to bottom,
# not fully parsed ahead, so every function called below must already have
# been sourced past this point) ---------------------------------------------

build_one() {
    local label="$1" wt="$2" dist="$3" commit="$4" sde="$5"
    echo ">> verify-reproducible: [$label] worktree add --detach $wt $(git rev-parse --short "$commit")"
    git -C "$REPO_ROOT" worktree add --detach --quiet "$wt" "$commit" || return 1
    mkdir -p "$dist"
    echo ">> verify-reproducible: [$label] make build && make deb (make build's web step runs npm ci itself)"
    ( cd "$wt" && SOURCE_DATE_EPOCH="$sde" make build ) || return 1
    ( cd "$wt" && SOURCE_DATE_EPOCH="$sde" make deb DIST_DIR="$dist" ) || return 1
}

diff_uncompressed_tar() {
    local ua="$1" ub="$2"
    if cmp -s "$ua" "$ub"; then
        echo "(decompressed tar contents are identical — the difference was only in compressor-level bytes, e.g. an embedded compressor timestamp)" >&2
        return
    fi
    echo "-- decompressed tar member first differing offset --" >&2
    cmp "$ua" "$ub" >&2 || true
    echo "-- tar listing diff (name/perm/owner/mtime) --" >&2
    diff <(tar tvf "$ua") <(tar tvf "$ub") >&2 || true
}

diff_tar_member() {
    local a="$1" b="$2" ua="$WORK/$(basename "$a").u" ub="$WORK/$(basename "$b").u.b"
    case "$a" in
        *.tar.xz)  xz -dc "$a" > "$ua" 2>/dev/null;  xz -dc "$b" > "$ub" 2>/dev/null ;;
        *.tar.gz)  gzip -dc "$a" > "$ua" 2>/dev/null; gzip -dc "$b" > "$ub" 2>/dev/null ;;
        *.tar.zst) zstd -dc "$a" > "$ua" 2>/dev/null; zstd -dc "$b" > "$ub" 2>/dev/null ;;
        *) echo "(don't know how to decompress $(basename "$a") — leaving the ar-member-level diff above as the answer)" >&2; return ;;
    esac
    diff_uncompressed_tar "$ua" "$ub"
}

# report_diff narrows a mismatch down as far as it can, rather than just
# printing "differ": the top-level .deb is an ar(1) archive of three
# members (debian-binary, control.tar.*, data.tar.*), each of which is
# itself a compressed tar. A byte offset in the compressed .deb is close to
# useless (one differing input byte rewrites the entire rest of an
# xz/gzip/zstd stream), so this drills into whichever member first differs
# and reports the offset THERE — and, if that member's contents (once
# decompressed) still don't line up, one more level into the tar member's
# own bytes.
report_diff() {
    local a="$1" b="$2"
    local dir_a dir_b
    dir_a="$(mktemp -d "$WORK/diff-a.XXXXXX")"
    dir_b="$(mktemp -d "$WORK/diff-b.XXXXXX")"
    ( cd "$dir_a" && ar x "$a" )
    ( cd "$dir_b" && ar x "$b" )

    echo "-- ar members --" >&2
    local f base
    for f in "$dir_a"/*; do
        base="$(basename "$f")"
        if ! cmp -s "$f" "$dir_b/$base"; then
            echo "first differing member: $base" >&2
            cmp "$f" "$dir_b/$base" >&2 || true
            diff_tar_member "$f" "$dir_b/$base"
            return
        fi
    done
    echo "(ar members all matched individually — the difference is in ar container metadata itself, e.g. a member's recorded mtime/uid/gid/mode)" >&2
}

cleanup() {
    if [ -n "$KEEP_WORKTREES" ]; then
        echo ">> verify-reproducible: KEEP_WORKTREES set, leaving $WORK in place"
        return
    fi
    git -C "$REPO_ROOT" worktree remove --force "$WORK/a" >/dev/null 2>&1 || true
    git -C "$REPO_ROOT" worktree remove --force "$WORK/b" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

# --- main --------------------------------------------------------------

COMMIT="$(git rev-parse --verify "${REF}^{commit}" 2>/dev/null)" || die "'$REF' does not resolve to a commit"
COMMIT_SHORT="$(git rev-parse --short "$COMMIT")"

# A single SOURCE_DATE_EPOCH, derived once and handed to both builds
# explicitly. packaging/Makefile derives the same value on its own from the
# checked-out commit when SOURCE_DATE_EPOCH isn't already set (so a bare
# `make -C packaging deb` reproduces on its own too) — passing it explicitly
# here just removes any doubt that both builds used the same one.
SOURCE_DATE_EPOCH="$(git log -1 --format=%ct "$COMMIT")"

# Node: same pinned major CI and packaging expect, via the shared toolchain
# pin (T-3806) — see scripts/lib/versions.sh. A build under a different
# Node major is not evidence about whether the PINNED build is reproducible.
# shellcheck disable=SC1091
. "$REPO_ROOT/scripts/lib/versions.sh"
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
if [ -s "$NVM_DIR/nvm.sh" ]; then
    # shellcheck disable=SC1091
    . "$NVM_DIR/nvm.sh"
    nvm use --delete-prefix "$NODE_MAJOR" >/dev/null 2>&1 || die "nvm has no Node $NODE_MAJOR installed (nvm install $NODE_MAJOR)"
fi
NODE_ACTUAL="$(node --version 2>/dev/null || echo '<none>')"
case "$NODE_ACTUAL" in
    v"$NODE_MAJOR".*) : ;;
    *) die "node is $NODE_ACTUAL; expected v$NODE_MAJOR.x (scripts/lib/versions.sh) — install/select it before running this" ;;
esac

GO_VERSION_ACTUAL="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
if [ "$GO_VERSION_ACTUAL" != "$GO_VERSION_EXPECTED" ]; then
    echo "!! verify-reproducible: go $GO_VERSION_ACTUAL, pinned version is $GO_VERSION_EXPECTED — a PASS here is not evidence the pinned toolchain reproduces" >&2
fi

echo ">> verify-reproducible: commit $COMMIT_SHORT, SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH ($(date -u -d "@$SOURCE_DATE_EPOCH" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null))"
echo ">> verify-reproducible: go $GO_VERSION_ACTUAL, node $NODE_ACTUAL"

build_one A "$WORK/a" "$WORK/dist-a" "$COMMIT" "$SOURCE_DATE_EPOCH" || die "build A failed — see output above"
# A deliberate gap so a build that (wrongly) stamps wall-clock "now" anywhere
# would provably disagree between A and B — a fast machine could otherwise
# finish both builds inside the same wall-clock second and hide that bug.
sleep 2
build_one B "$WORK/b" "$WORK/dist-b" "$COMMIT" "$SOURCE_DATE_EPOCH" || die "build B failed — see output above"

DEB_A="$(find "$WORK/dist-a" -maxdepth 1 -name '*.deb' | head -1)"
DEB_B="$(find "$WORK/dist-b" -maxdepth 1 -name '*.deb' | head -1)"
[ -n "$DEB_A" ] || die "build A produced no .deb in $WORK/dist-a"
[ -n "$DEB_B" ] || die "build B produced no .deb in $WORK/dist-b"

SUM_A="$(sha256sum "$DEB_A" | awk '{print $1}')"
SUM_B="$(sha256sum "$DEB_B" | awk '{print $1}')"

echo
echo "build A: $(basename "$DEB_A")  $SUM_A"
echo "build B: $(basename "$DEB_B")  $SUM_B"

if [ "$SUM_A" = "$SUM_B" ]; then
    echo ">> verify-reproducible: PASS — byte-identical .deb from two independent builds of $COMMIT_SHORT"
    exit 0
fi

echo "!! verify-reproducible: FAIL — builds differ" >&2
report_diff "$DEB_A" "$DEB_B"
exit 1
