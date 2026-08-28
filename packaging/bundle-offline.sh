#!/usr/bin/env bash
# packaging/bundle-offline.sh — T-4009: assembles a self-contained offline
# install bundle for a cluster with no outbound network: the .deb, its
# install script, checksums, a detached signature, and (optionally) a
# `vnproxctl hub mirror` of the signed blueprint/plugin registry.
#
# What this does NOT reinvent:
#   - Package installation logic: packaging/install.sh already has a fully
#     worked-out `--offline <file>` path (verify a detached .asc if present,
#     `apt-get install --no-install-recommends` or `dpkg -i`, per-node scp
#     rollout) — see that script's own header. This bundle just gives it the
#     inputs it already knows how to consume, laid out the way it expects
#     them (a `<deb>.asc` detached signature next to the .deb).
#   - Registry fetch/verify: `vnproxctl hub mirror` (cmd/vnproxctl/hubcmd_
#     mirror.go, T-4009) does the actual fetching and signature verification;
#     this script only calls it.
#   - Signing convention: the same VNPROX_SIGNING_KEY_FILE / ephemeral-key
#     fallback packaging/build-apt-repo.sh already uses for the apt repo's
#     Release file, applied here to SHA256SUMS and the .deb itself, so an
#     operator (or release.yml) manages exactly one signing-key secret for
#     both artifact families rather than two.
#
# Usage:
#   packaging/bundle-offline.sh <out-dir> <deb-file> [<deb-file> ...]
#
# Environment:
#   VNPROX_SIGNING_KEY_FILE    Path to an ASCII-armored GPG private key to
#                              sign with. If unset, a throwaway key is
#                              generated (dev/test only — see
#                              build-apt-repo.sh's own note; the exported
#                              public key still ships in the bundle either
#                              way, as release-key.asc, so `install.sh
#                              --offline ... --release-key release-key.asc`
#                              always has a matching anchor to verify
#                              against).
#   VNPROX_SIGNING_KEY_ID      Key ID/email once imported. Defaults to
#                              "vnprox-packaging" (matches the ephemeral
#                              key's generated identity).
#   VNPROX_MIRROR_REGISTRY     If set, `vnproxctl hub mirror` this hosted
#                              registry into <out-dir>/hub-mirror.
#   VNPROX_MIRROR_SIGNERS      Comma-separated trusted index-signer
#                              fingerprints — REQUIRED with
#                              VNPROX_MIRROR_REGISTRY (hub mirror refuses to
#                              run without it: an air-gapped bundle must
#                              never carry a catalog nobody has verified).
#   VNPROX_MIRROR_BIN          Path to the vnproxctl binary to run `hub
#                              mirror` with. Defaults to `vnproxctl` on
#                              $PATH, falling back to ../bin/vnproxctl
#                              relative to this script (the `make build`
#                              output).
set -euo pipefail

PROG=$(basename "$0")
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

[ $# -ge 2 ] || die "usage: $PROG <out-dir> <deb-file> [<deb-file> ...]"
OUT_DIR="$1"
shift
DEBS=("$@")
for f in "${DEBS[@]}"; do
	[ -f "$f" ] || die "no such file: $f"
done

KEY_ID="${VNPROX_SIGNING_KEY_ID:-vnprox-packaging}"

install -d -m 0755 "$OUT_DIR"

# --- payload: the .deb(s) + a self-contained install script -----------------
#
# install.sh is copied IN, not fetched by the operator separately — the
# documented quick-install flow ("curl ... install.sh") is itself a network
# call this bundle must not require.
for f in "${DEBS[@]}"; do
	cp -f "$f" "$OUT_DIR/"
done
install -m 0755 "$SCRIPT_DIR/install.sh" "$OUT_DIR/install.sh"
log "copied ${#DEBS[@]} .deb(s) and install.sh into $OUT_DIR"

# --- signing key (ephemeral fallback mirrors build-apt-repo.sh exactly) -----

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
	log "VNPROX_SIGNING_KEY_FILE not set — generating an EPHEMERAL signing key (dev/test only; never used for a real release, matching build-apt-repo.sh's own fallback)"
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

gpg --batch --yes --armor --export "$KEY_ID" >"$OUT_DIR/release-key.asc"
log "exported public trust anchor to $OUT_DIR/release-key.asc (pass --release-key to install.sh)"

# --- per-.deb detached signature: <deb>.asc, exactly what install.sh's
# --offline path looks for next to the package -------------------------------
for f in "${DEBS[@]}"; do
	base="$(basename "$f")"
	gpg --batch --yes --local-user "$KEY_ID" --armor --detach-sign \
		-o "$OUT_DIR/$base.asc" "$OUT_DIR/$base"
done
log "signed each .deb (detached, armored, <deb>.asc)"

# --- optional hub mirror (T-4009) -------------------------------------------

MIRROR_URL=""
if [ -n "${VNPROX_MIRROR_REGISTRY:-}" ]; then
	[ -n "${VNPROX_MIRROR_SIGNERS:-}" ] ||
		die "VNPROX_MIRROR_REGISTRY is set but VNPROX_MIRROR_SIGNERS is not — hub mirror refuses to run without a trusted signer list; an air-gapped bundle must never carry an unverified catalog"
	VNPROXCTL_BIN="${VNPROX_MIRROR_BIN:-}"
	if [ -z "$VNPROXCTL_BIN" ]; then
		if command -v vnproxctl >/dev/null 2>&1; then
			VNPROXCTL_BIN="$(command -v vnproxctl)"
		elif [ -x "$SCRIPT_DIR/../bin/vnproxctl" ]; then
			VNPROXCTL_BIN="$SCRIPT_DIR/../bin/vnproxctl"
		else
			die "VNPROX_MIRROR_REGISTRY is set but no vnproxctl binary was found (set VNPROX_MIRROR_BIN, or run 'make build' first)"
		fi
	fi
	log "mirroring $VNPROX_MIRROR_REGISTRY into $OUT_DIR/hub-mirror"
	"$VNPROXCTL_BIN" hub mirror --registry "$VNPROX_MIRROR_REGISTRY" \
		--signers "$VNPROX_MIRROR_SIGNERS" --out "$OUT_DIR/hub-mirror"
	MIRROR_URL="file://$(cd "$OUT_DIR/hub-mirror" && pwd)"
fi

# --- checksums + their own detached signature -------------------------------

(
	cd "$OUT_DIR"
	find . -type f ! -name 'SHA256SUMS' ! -name 'SHA256SUMS.asc' -print0 |
		sort -z | xargs -0 sha256sum >SHA256SUMS
)
gpg --batch --yes --local-user "$KEY_ID" --armor --detach-sign \
	-o "$OUT_DIR/SHA256SUMS.asc" "$OUT_DIR/SHA256SUMS"
log "wrote $OUT_DIR/SHA256SUMS(.asc)"

# --- README: the exact install command for this bundle ----------------------

cat >"$OUT_DIR/README-OFFLINE.md" <<EOF
# vnprox offline install bundle

Generated $(date -u +%Y-%m-%dT%H:%M:%SZ). Contents:

$(for f in "${DEBS[@]}"; do echo "- \`$(basename "$f")\` (+ \`.asc\` detached signature)"; done)
- \`install.sh\` — the same installer \`docs/deployment.md\`'s quick-install
  documents, copied in so nothing has to be fetched separately.
- \`release-key.asc\` — the trust anchor for the signatures above (and for
  \`SHA256SUMS.asc\`).
- \`SHA256SUMS\` / \`SHA256SUMS.asc\` — checksums of every file in this bundle,
  signed.
$([ -n "$MIRROR_URL" ] && printf -- '- `hub-mirror/` — a `vnproxctl hub mirror` of `%s`, verified at mirror time against the signers given, and re-verified again on every later read (internal/hubreg.Verify runs whether the index came from the network or from this directory — see docs/hub-registry.md).\n' "$VNPROX_MIRROR_REGISTRY")

## Install (no network required)

On the target node, from this directory:

\`\`\`
sha256sum -c SHA256SUMS                 # optional but recommended: confirm nothing in transit corrupted the bundle
sudo ./install.sh --offline ./$(basename "${DEBS[0]}") --release-key release-key.asc
\`\`\`

(The leading \`./\` on the \`--offline\` argument matters: without it,
\`apt-get\` treats the value as a package NAME to look up rather than a local
file path, and fails with "Unable to locate package" — confirmed against
this exact bundle in a \`--network=none\` container while building it.)

\`install.sh\` verifies the \`.deb\`'s detached signature against
\`release-key.asc\` before installing, then runs \`apt-get install -y
--no-install-recommends\` (or \`dpkg -i\` where apt is absent) — no network
call is made either way. \`Recommends: lldpd, ifupdown2\` (optional LLDP
discovery / network-reload integration) are skipped; install them from your
own offline mirror separately if you want them.

$([ -n "$MIRROR_URL" ] && cat <<MIRROREOF
## Blueprint/plugin hub (offline)

Point vnprox at the mirrored registry instead of a hosted one — a config
value, not a different code path (\`internal/hub\`'s file:// support, T-4009):

\`\`\`
# /etc/vnprox/vnprox.toml
[hub]
registry_url   = "$MIRROR_URL"
index_signers  = ["<the fingerprint 'vnproxctl hub mirror' printed>"]
\`\`\`

Or fetch one artifact by hand with no daemon running at all:

\`\`\`
vnproxctl hub pull --registry $MIRROR_URL --signers <fp> \\
  --type blueprint --id <id> --version <version> --out artifact.json
\`\`\`

**Revocation staleness, stated plainly:** a mirror is a snapshot. Revocations
published by the registry *after* this mirror was made are invisible to an
air-gapped installation until someone re-runs \`hub mirror\` and physically
carries the new snapshot in — there is no push channel across an air gap.
This is not a bug this bundle can fix; it is the actual shape of "no
network," and docs/hub-registry.md's "Air-gapped operation" section says so
in the same words.
MIRROREOF
)

## Building vnprox from source, offline

This bundle ships prebuilt binaries; you do not need Go to install them. If
you separately need to rebuild from source (e.g. to verify reproducibility,
\`scripts/verify-reproducible.sh\`) on a host with no network, install Go
1.25+ **natively** first — \`docs/development.md\`'s "Toolchain pinning"
section already documents that \`GOTOOLCHAIN\`'s automatic version download
needs network access and fails air-gapped otherwise.
EOF
log "wrote $OUT_DIR/README-OFFLINE.md"

log "offline bundle assembled at $OUT_DIR"
