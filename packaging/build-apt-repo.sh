#!/usr/bin/env bash
# packaging/build-apt-repo.sh — assembles a signed, flat apt repository from
# one or more built .deb files. Used by:
#
#   - release.yml (CI): on a tag push, after `make deb` for each supported
#     arch, this script builds the repo tree that gets published to
#     get.vnprox.io/apt/ (docs/deployment.md's documented install source).
#   - packaging/test/apt-repo.sh (local/CI test): builds a throwaway repo
#     with an ephemeral signing key and serves it over plain HTTP for
#     install.sh's `--apt-repo <url>` path to install from end to end.
#
# See packaging/apt-repo.md for the repo layout this produces and the
# signing-key story (production vs. this script's ephemeral-key fallback).
#
# Usage:
#   build-apt-repo.sh <repo-dir> <deb-file> [<deb-file> ...]
#
# Environment:
#   VNPROX_SIGNING_KEY_FILE   Path to an ASCII-armored GPG private key to
#                             sign with. If unset, a throwaway key is
#                             generated in a temp GNUPGHOME (dev/test only —
#                             release.yml always sets this from a repo
#                             secret; see apt-repo.md).
#   VNPROX_SIGNING_KEY_ID     Key ID/email to use once imported. Defaults to
#                             "vnprox-packaging" (matches the ephemeral
#                             key's generated identity below).

set -euo pipefail

PROG=$(basename "$0")
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

[ $# -ge 2 ] || die "usage: $PROG <repo-dir> <deb-file> [<deb-file> ...]"

REPO_DIR="$1"
shift
DEBS=("$@")
for f in "${DEBS[@]}"; do
	[ -f "$f" ] || die "no such file: $f"
done

SUITE="stable"
COMPONENT="main"
KEY_ID="${VNPROX_SIGNING_KEY_ID:-vnprox-packaging}"

GNUPGHOME_TMP=""
cleanup() { [ -n "$GNUPGHOME_TMP" ] && rm -rf "$GNUPGHOME_TMP"; }
trap cleanup EXIT

if [ -n "${VNPROX_SIGNING_KEY_FILE:-}" ]; then
	[ -f "$VNPROX_SIGNING_KEY_FILE" ] || die "VNPROX_SIGNING_KEY_FILE not found: $VNPROX_SIGNING_KEY_FILE"
	GNUPGHOME_TMP="$(mktemp -d)"
	export GNUPGHOME="$GNUPGHOME_TMP"
	chmod 0700 "$GNUPGHOME"
	gpg --batch --import "$VNPROX_SIGNING_KEY_FILE" >/dev/null 2>&1
	log "imported signing key from \$VNPROX_SIGNING_KEY_FILE into a scratch keyring"
else
	log "VNPROX_SIGNING_KEY_FILE not set — generating an EPHEMERAL signing key (dev/test only; never used for a real release, see apt-repo.md)"
	GNUPGHOME_TMP="$(mktemp -d)"
	export GNUPGHOME="$GNUPGHOME_TMP"
	chmod 0700 "$GNUPGHOME"
	cat >"$GNUPGHOME_TMP/keygen.batch" <<EOF
%no-protection
Key-Type: EDDSA
Key-Curve: ed25519
Name-Real: vnprox packaging (ephemeral, test-only)
Name-Email: $KEY_ID@vnprox.invalid
Expire-Date: 1d
%commit
EOF
	gpg --batch --gen-key "$GNUPGHOME_TMP/keygen.batch" >/dev/null 2>&1
	KEY_ID="$(gpg --batch --list-secret-keys --with-colons | awk -F: '/^sec/{print $5; exit}')"
fi

# --- pool/ + Packages -------------------------------------------------------

install -d -m 0755 "$REPO_DIR/pool/main/v/vnprox"
for f in "${DEBS[@]}"; do
	cp -f "$f" "$REPO_DIR/pool/main/v/vnprox/"
done

ARCHES=()
for f in "${DEBS[@]}"; do
	arch="$(dpkg-deb -f "$f" Architecture)"
	case " ${ARCHES[*]:-} " in
	*" $arch "*) ;;
	*) ARCHES+=("$arch") ;;
	esac
done

DISTS_DIR="$REPO_DIR/dists/$SUITE"
for arch in "${ARCHES[@]}"; do
	bindir="$DISTS_DIR/$COMPONENT/binary-$arch"
	install -d -m 0755 "$bindir"
	(cd "$REPO_DIR" && dpkg-scanpackages --arch "$arch" pool/ >"dists/$SUITE/$COMPONENT/binary-$arch/Packages")
	gzip -9 -k -f "$bindir/Packages"
	log "wrote $bindir/Packages(.gz)"
done

# --- Release (+ detached and inline signatures) -----------------------------

(
	cd "$REPO_DIR"
	cat >"dists/$SUITE/Release.tmp" <<EOF
Origin: vnprox
Label: vnprox
Suite: $SUITE
Codename: $SUITE
Components: $COMPONENT
Architectures: ${ARCHES[*]}
Date: $(date -Ru)
Description: vnprox package repository
EOF
	{
		echo "MD5Sum:"
		find "dists/$SUITE" -type f \( -name 'Packages' -o -name 'Packages.gz' \) | sort | while read -r f; do
			rel="${f#dists/$SUITE/}"
			printf ' %s %16d %s\n' "$(md5sum "$f" | cut -d' ' -f1)" "$(wc -c <"$f")" "$rel"
		done
		echo "SHA256:"
		find "dists/$SUITE" -type f \( -name 'Packages' -o -name 'Packages.gz' \) | sort | while read -r f; do
			rel="${f#dists/$SUITE/}"
			printf ' %s %16d %s\n' "$(sha256sum "$f" | cut -d' ' -f1)" "$(wc -c <"$f")" "$rel"
		done
	} >>"dists/$SUITE/Release.tmp"
	mv "dists/$SUITE/Release.tmp" "dists/$SUITE/Release"
)

gpg --batch --yes --local-user "$KEY_ID" --armor --detach-sign \
	-o "$DISTS_DIR/Release.gpg" "$DISTS_DIR/Release"
gpg --batch --yes --local-user "$KEY_ID" --clear-sign \
	-o "$DISTS_DIR/InRelease" "$DISTS_DIR/Release"
log "signed $DISTS_DIR/Release (detached: Release.gpg, inline: InRelease)"

# --- public keyring for clients (install.sh fetches this) -------------------

# ASCII-armored: install.sh's `curl ... | gpg --dearmor` expects armored
# input (matching the documented client-side convention for third-party apt
# keyrings — see apt-repo.md).
gpg --batch --yes --armor --export "$KEY_ID" >"$REPO_DIR/vnprox-archive-keyring.gpg"
log "exported public keyring to $REPO_DIR/vnprox-archive-keyring.gpg"

log "apt repo built at $REPO_DIR (suite=$SUITE component=$COMPONENT arches=${ARCHES[*]})"
