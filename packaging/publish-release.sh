#!/usr/bin/env bash
# packaging/publish-release.sh — the manual release-cut flow (T-3301),
# replacing what .github/workflows/release.yml did before hosted Actions
# was retired (docs/development.md's "CI" section has the record — this is
# not a stopgap, it's the permanent path).
#
# Mirrors release.yml step for step: build both arches, assemble+sign the
# apt repo, stamp+publish the contract/compat artifacts, publish the repo
# tree to the real host, cut a GitHub release with a changelog. The one
# structural difference: release.yml's signing step ran on a fresh CI
# runner with the key injected from a repo secret; here the production key
# never leaves the apt-repo host (T-3301's own design — see
# packaging/apt-repo.md's "Signing key" section), so the sign-and-assemble
# step runs over SSH on that host instead of locally.
#
# Usage:
#   git tag vX.Y.Z && git push origin vX.Y.Z
#   packaging/publish-release.sh vX.Y.Z
#
# Environment:
#   VNPROX_APT_HOST     SSH target that holds the production signing key
#                        and serves the repo (default: root@192.168.1.7,
#                        i.e. pve001 — apt.vnprox.com once T-3301's edge
#                        proxy is wired; see packaging/apt-repo.md).
#   VNPROX_APT_KEY_PATH Signing key path on that host
#                        (default: /etc/vnprox-release/signing-key.asc).
#   VNPROX_APT_KEY_ID   Key identity (default: security@vnprox.com).
#   VNPROX_APT_REPO_DIR Repo directory on that host, served over HTTP
#                        (default: /srv/vnprox-apt).
#   SKIP_GH_RELEASE=1   Build and publish the apt repo, but don't cut a
#                        GitHub release (useful for a dry run against a tag
#                        that isn't ready to announce yet).

set -euo pipefail

PROG=$(basename "$0")
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

[ $# -eq 1 ] || die "usage: $PROG vX.Y.Z"
TAG="$1"
case "$TAG" in
v*) : ;;
*) die "tag must start with 'v' (got: $TAG)" ;;
esac

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || die "no local tag $TAG — git tag $TAG first"
CURRENT_TAG="$(git describe --tags --exact-match 2>/dev/null || true)"
[ "$CURRENT_TAG" = "$TAG" ] || die "HEAD is not tagged $TAG (git describe --tags --exact-match says '${CURRENT_TAG:-<none>}') — checkout the tag first"

APT_HOST="${VNPROX_APT_HOST:-root@192.168.1.7}"
APT_KEY_PATH="${VNPROX_APT_KEY_PATH:-/etc/vnprox-release/signing-key.asc}"
APT_KEY_ID="${VNPROX_APT_KEY_ID:-security@vnprox.com}"
APT_REPO_DIR="${VNPROX_APT_REPO_DIR:-/srv/vnprox-apt}"

DIST="$ROOT_DIR/dist"
rm -rf "$DIST"
mkdir -p "$DIST"

# --- build (mirrors ci.yml's `package` job, both arches) --------------------

log "make build (production frontend)"
make build

for arch in amd64 arm64; do
	log "packaging deb: $arch"
	make -C packaging deb DIST_DIR="$DIST" ARCH="$arch"
done
ls -l "$DIST"/*.deb

# --- assemble + sign the apt repo, on the host that holds the key -----------

log "assembling + signing the apt repo on $APT_HOST (key never leaves that host)"
REMOTE_STAGE="/tmp/vnprox-apt-stage-$TAG"
# shellcheck disable=SC2029
ssh "$APT_HOST" "rm -rf '$REMOTE_STAGE' && mkdir -p '$REMOTE_STAGE'"
scp -q "$DIST"/*.deb "$APT_HOST:$REMOTE_STAGE/"
scp -q "$ROOT_DIR/packaging/build-apt-repo.sh" "$APT_HOST:$REMOTE_STAGE/build-apt-repo.sh"
# shellcheck disable=SC2029
ssh "$APT_HOST" "chmod +x '$REMOTE_STAGE/build-apt-repo.sh' && \
  VNPROX_SIGNING_KEY_FILE='$APT_KEY_PATH' VNPROX_SIGNING_KEY_ID='$APT_KEY_ID' \
  '$REMOTE_STAGE/build-apt-repo.sh' '$REMOTE_STAGE/repo' '$REMOTE_STAGE'/*.deb"

log "publishing repo tree into $APT_REPO_DIR on $APT_HOST"
# shellcheck disable=SC2029
ssh "$APT_HOST" "mkdir -p '$APT_REPO_DIR' && rsync -a --delete '$REMOTE_STAGE/repo/' '$APT_REPO_DIR/' && rm -rf '$REMOTE_STAGE'"
log "apt repo published: https://apt.vnprox.com (once T-3301's edge proxy points at $APT_HOST)"

# --- contract + compat artifacts (mirrors release.yml verbatim) -------------

mkdir -p "$DIST/contract"
jq --arg v "$TAG" '.info.version = $v' docs/openapi.json >"$DIST/contract/vnprox-openapi-$TAG.json"
cp docs/automation-contract.json "$DIST/contract/vnprox-automation-contract-$TAG.json"

mkdir -p "$DIST/compat"
VNPROX_COMPAT_VERSION="$TAG" go test ./internal/apicontract/compat/... -run TestMatrix_MatchesPublishedArtifact -count=1
cp var/compat-matrix.json "$DIST/compat/vnprox-compat-matrix-$TAG.json"

# --- GitHub release -----------------------------------------------------

if [ -n "${SKIP_GH_RELEASE:-}" ]; then
	log "SKIP_GH_RELEASE set — not cutting a GitHub release for $TAG"
	exit 0
fi

PREV_TAG="$(git describe --tags --abbrev=0 "$TAG^" 2>/dev/null || true)"
CHANGELOG_FILE="$(mktemp)"
trap 'rm -f "$CHANGELOG_FILE"' EXIT
if [ -n "$PREV_TAG" ]; then
	{
		echo "Changes since $PREV_TAG:"
		echo
		git log --pretty='format:- %s (%h)' "$PREV_TAG..$TAG"
	} >"$CHANGELOG_FILE"
else
	echo "Initial release." >"$CHANGELOG_FILE"
fi

PRERELEASE_FLAG=()
case "$TAG" in
*-*) PRERELEASE_FLAG=(--prerelease) ;;
esac

log "creating GitHub release $TAG"
gh release create "$TAG" \
	--title "$TAG" \
	--notes-file "$CHANGELOG_FILE" \
	"${PRERELEASE_FLAG[@]}" \
	"$DIST"/*.deb \
	"$DIST"/contract/*.json \
	"$DIST"/compat/*.json

log "done: $TAG built, signed, published to apt.vnprox.com, and released on GitHub"
