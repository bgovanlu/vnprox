#!/usr/bin/env bash
# vnprox quick-install script — docs/deployment.md "Quick install (script)".
#
#   curl -fsSL https://apt.vnprox.com/install.sh -o install.sh
#   less install.sh   # you're piping root on a hypervisor — read it
#   bash install.sh
#
# Documented flow (docs/deployment.md numbering kept in the comments below):
#   1. Verify PVE version/arch; detect cluster membership and node list.
#   2. Check port 8007 (PBS conflict); ask for the listen port if needed.
#   3. Install the vnprox .deb (apt repo, or --offline <file>).
#   4. Optionally install + enable lldpd on all nodes.
#   5. Create the read-only PVE API token vnprox@pve!daemon.
#   6. Generate the cluster secret in /etc/pve/priv/vnprox/ (first node
#      only) — under priv/ specifically, since that's the one pmxcfs
#      subtree that actually enforces 0600 root-only (see vnprox-setup's
#      step-6 comment for why).
#   7. Write /etc/vnprox/vnprox.toml, generate the session key, enable +
#      start vnprox.service.
#   8. Repeat 3-7 on the remaining nodes via SSH, or print per-node
#      instructions if SSH between nodes is unavailable.
#   9. Print the URL and a first-login checklist.
#
# Steps 2 and 5-7 for *this* node are delegated to vnprox-setup (installed
# by the .deb at /usr/bin/vnprox-setup) rather than duplicated here, so
# there is exactly one implementation of "set up a single node" — see
# packaging/bin/vnprox-setup. Step 8 (SSH rollout below) drives that same
# script remotely, per node.
#
# What this script cannot fully exercise in this sandbox, and why
# (planning/tasks/phase-0.md#T-006's instruction: don't pretend untestable
# things work) — see the T-606 completion report for the exact "needs
# hardware validation" list:
#   - Installing from a real, signed apt repository (no `--offline <deb>`):
#     the repo tooling exists now (packaging/apt-repo.md, release.yml), but
#     there is no live apt.vnprox.com to actually publish to and no real PVE
#     node with real network access to pull from one in this environment.
#   - PVE API token/role creation (step 5, in vnprox-setup): the pveum
#     commands are real, not stubbed, but pveum itself only exists on a
#     real Proxmox VE node — this sandbox skips that block with a clear
#     log line rather than faking success.
#   - Cross-node pmxcfs replication of the cluster secret: generation is
#     real (vnprox-setup step 6); actual replication across joined nodes
#     needs a live pmxcfs mount this sandbox doesn't have.
#   - Multi-node SSH rollout (step 8 below) is implemented for real (scp/ssh
#     to each other cluster node, same root-SSH mechanism pvecm setups rely
#     on) and is exercised by the T-606 container-based 3-node harness
#     (packaging/test/cluster-ssh.sh) against real containers with real SSH
#     keys; it still needs hardware validation against an actual multi-node
#     PVE cluster.

# T-2801 added three things to this script, all in service of one sentence
# on that card: "curl -fsSL <url> | sh detects the platform, verifies a
# signature, installs from the signed apt repository where available and
# falls back to a binary tarball. Signature verification is not skippable;
# there is no --insecure."
#
#   1. SIGNATURE VERIFICATION ON EVERY DOWNLOAD. The apt repo's signing key
#      is pinned by fingerprint (VNPROX_RELEASE_KEY_FPR below), so the key
#      this script imports is checked against a value the script itself
#      carries rather than trusted because the same server served it. The
#      tarball fallback verifies a detached signature over the archive.
#      Neither can be turned off: there is no --insecure, no --no-verify,
#      no environment variable, and packaging/test asserts that by grepping
#      this file.
#
#      --release-key <file> is NOT such an escape hatch and is worth being
#      precise about, because it looks like one. It changes WHICH key is
#      trusted; it cannot change WHETHER a signature is checked. A
#      signature is verified on every path either way, and an attacker who
#      could inject into the download stream cannot also add a flag to the
#      operator's own command line. It exists for air-gapped installs
#      (where the operator carries the key on the same medium as the
#      artifact) and for this repository's tests, which sign with an
#      ephemeral key exactly as packaging/build-apt-repo.sh already does.
#
#   2. A BINARY TARBALL FALLBACK (--tarball, and automatic on a host with
#      no apt-get), so the one-command install works on a machine that is
#      not Debian-derived. Same verification, same refusal.
#
#   3. IDEMPOTENCE (AC5). Running this twice leaves the same versions and
#      one apt sources entry, and says so instead of reinstalling.

set -euo pipefail

PROG=$(basename "$0")
DEFAULT_PORT=8007

OFFLINE_DEB=""
FORCE_PORT=""
WITH_LLDP=""
ASSUME_YES=0
SKIP_PVE_CHECK=0
APT_REPO_URL="https://apt.vnprox.com"
DIST_URL="https://apt.vnprox.com/dist"
RELEASE_KEY_FILE=""
INSTALL_PREFIX=""
FORCE_TARBALL=0

# VNPROX_RELEASE_KEY_FPR is the pinned fingerprint of the vnprox release
# signing key: the trust anchor this script CARRIES, so that the key it
# imports is not merely "whatever the download host served".
#
# T-3301 (2026-08-18): a real production key now exists and signs
# apt.vnprox.com for real (packaging/apt-repo.md's "Signing key" section —
# it lives only on the apt-repo host, never in this repository, never as a
# GitHub Actions secret since Actions no longer runs releases). This is
# that key's real fingerprint, not a placeholder.
#
# vnprox-release-key-fingerprint {{{
VNPROX_RELEASE_KEY_FPR="F57DDE63ABA03B3BEEEB2DB93BD9CC3B118061BD"
# }}} vnprox-release-key-fingerprint
VNPROX_RELEASE_KEY_PLACEHOLDER="0000000000000000000000000000000000000000"

log() { printf '>> %s\n' "$*" >&2; } # stderr, for consistency with vnprox-setup's log() (see its comment)
warn() { printf '%s: warning: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

usage() {
	cat <<EOF
Usage: $PROG [options]

Options:
  --offline <file>     Install from this local .deb instead of the apt repo.
                        Verified against <file>.asc when that signature is
                        present next to it.
  --apt-repo <url>      Base URL of the vnprox apt repo (default:
                        https://apt.vnprox.com; see packaging/apt-repo.md).
                        Ignored when --offline is given.
  --tarball             Install from the signed binary tarball instead of apt
                        (automatic on a host with no apt-get).
  --dist-url <url>      Base URL of the tarball distribution (default:
                        https://apt.vnprox.com/dist).
  --release-key <file>  Trust anchor for signature verification: an armored
                        public key. Changes WHICH key is trusted; it cannot
                        change whether a signature is checked. There is no
                        way to skip verification.
  --prefix <dir>        Install the tarball under this prefix instead of /usr
                        (binaries land in <dir>/bin). Implies --tarball and
                        skips the node setup steps, for an unprivileged or
                        air-gapped install.
  --port <n>            Force the listen port (skips conflict detection).
  --with-lldp            Install lldpd on this node (default: ask).
  --no-lldp               Skip lldpd.
  -y, --yes               Non-interactive: accept documented defaults.
  --skip-pve-check        Continue even if this doesn't look like a PVE node
                           (useful for CI / container testing).
  -h, --help               Show this help.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--offline)
		OFFLINE_DEB="$2"
		shift 2
		;;
	--apt-repo)
		APT_REPO_URL="$2"
		shift 2
		;;
	--dist-url)
		DIST_URL="$2"
		shift 2
		;;
	--release-key)
		RELEASE_KEY_FILE="$2"
		shift 2
		;;
	--tarball)
		FORCE_TARBALL=1
		shift
		;;
	--prefix)
		INSTALL_PREFIX="$2"
		FORCE_TARBALL=1
		shift 2
		;;
	--port)
		FORCE_PORT="$2"
		shift 2
		;;
	--with-lldp)
		WITH_LLDP=yes
		shift
		;;
	--no-lldp)
		WITH_LLDP=no
		shift
		;;
	-y | --yes)
		ASSUME_YES=1
		shift
		;;
	--skip-pve-check)
		SKIP_PVE_CHECK=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown option: $1 (see --help)"
		;;
	esac
done

if [ "$(id -u)" -ne 0 ] && [ -z "$INSTALL_PREFIX" ]; then
	die "must run as root"
fi

# --- signature verification -----------------------------------------------
#
# One function, used by every path that puts bytes on this machine that came
# off a network. It is not conditional on anything: there is no flag, no
# environment variable and no fallback that reaches an install without
# passing through here.

# release_trust_anchor prints the path of an armored public key to verify
# against, or dies. Exactly two sources, in this order:
#
#   1. --release-key <file>, the operator's own explicitly supplied anchor.
#   2. The distribution's published key, ACCEPTED ONLY IF its fingerprint
#      equals VNPROX_RELEASE_KEY_FPR — the value this script carries. That
#      check is the whole point: fetching a key from the same host that
#      serves the artifact and then trusting it because it arrived proves
#      nothing at all.
#
# There is no third source, and in particular no "no key available, carry
# on".
release_trust_anchor() {
	work="$1"

	if [ -n "$RELEASE_KEY_FILE" ]; then
		[ -f "$RELEASE_KEY_FILE" ] || die "--release-key file not found: $RELEASE_KEY_FILE"
		printf '%s\n' "$RELEASE_KEY_FILE"
		return 0
	fi

	if [ "$VNPROX_RELEASE_KEY_FPR" = "$VNPROX_RELEASE_KEY_PLACEHOLDER" ]; then
		die "this build of $PROG carries no release-key fingerprint, so a downloaded artifact cannot be verified, and installing one unverified is not an option this script offers. Install from a local package with --offline <file>, or supply the trust anchor with --release-key <file>. (Maintainers: generate the release key, publish it, and replace the pinned fingerprint — see the vnprox-release-key-fingerprint marker in this file and packaging/apt-repo.md.)"
	fi

	fetched="$work/release-key.asc"
	if ! curl -fsSL "$DIST_URL/vnprox-release-key.asc" -o "$fetched" 2>/dev/null &&
		! curl -fsSL "$APT_REPO_URL/vnprox-archive-keyring.gpg" -o "$fetched" 2>/dev/null; then
		die "could not fetch the vnprox release key from $DIST_URL or $APT_REPO_URL"
	fi

	got="$(GNUPGHOME="$work/gnupg-probe" gpg_probe_fingerprint "$fetched")" ||
		die "could not read a key fingerprint from the fetched release key"
	if [ "$got" != "$VNPROX_RELEASE_KEY_FPR" ]; then
		die "the fetched release key's fingerprint ($got) is not the one this installer pins ($VNPROX_RELEASE_KEY_FPR) — refusing to trust it. This is either a key rotation this installer predates, or someone serving you a different key."
	fi
	printf '%s\n' "$fetched"
}

gpg_probe_fingerprint() {
	keyfile="$1"
	mkdir -p "$GNUPGHOME"
	chmod 700 "$GNUPGHOME"
	gpg --batch --quiet --import "$keyfile" >/dev/null 2>&1 || return 1
	gpg --batch --with-colons --fingerprint 2>/dev/null |
		awk -F: '$1 == "fpr" { print $10; exit }'
}

# verify_signature <artifact> <detached-signature> <trust-anchor>
#
# Dies on ANY failure — a bad signature, a signature by a key that is not
# the anchor, a missing signature, a missing gpg. "Could not check" and
# "checked and it was wrong" get the same treatment on purpose: they are the
# same thing from the point of view of the machine about to run the binary.
verify_signature() {
	artifact="$1"
	sigfile="$2"
	anchor="$3"

	command -v gpg >/dev/null 2>&1 ||
		die "gpg not found, and signature verification is not optional — install gnupg and re-run"
	[ -f "$artifact" ] || die "artifact not found: $artifact"
	[ -f "$sigfile" ] || die "no signature found for $(basename "$artifact") — refusing to install an unverified artifact"

	vwork="$(mktemp -d)"
	export GNUPGHOME="$vwork/gnupg"
	mkdir -p "$GNUPGHOME"
	chmod 700 "$GNUPGHOME"

	if ! gpg --batch --quiet --import "$anchor" >/dev/null 2>&1; then
		rm -rf "$vwork"
		unset GNUPGHOME
		die "could not import the release trust anchor from $anchor"
	fi
	if ! gpg --batch --verify "$sigfile" "$artifact" >/dev/null 2>&1; then
		rm -rf "$vwork"
		unset GNUPGHOME
		die "signature verification FAILED for $(basename "$artifact") — the artifact does not match its signature, or was not signed by the trusted release key. Not installing it."
	fi
	rm -rf "$vwork"
	unset GNUPGHOME
	log "signature verified: $(basename "$artifact")"
}

# --- the binary tarball fallback -------------------------------------------
#
# For a host with no apt-get (or an operator who asked for it with
# --tarball/--prefix). Downloads the architecture's archive AND its detached
# signature, verifies, and only then unpacks.
#
# Idempotent by construction (AC5): the version already installed at the
# prefix is compared against the one about to be installed, and a match is
# reported and skipped rather than re-extracted. Re-extracting would be
# harmless today, but "running it twice leaves the same versions" should be
# something the script knows, not something that happens to be true.

detect_arch() {
	a="$(dpkg --print-architecture 2>/dev/null || uname -m)"
	case "$a" in
	amd64 | x86_64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) echo "$a" ;;
	esac
}

installed_version() {
	bin="$1/bin/vnproxd"
	[ -x "$bin" ] || return 1
	# "vnproxd 1.2.3" -> "1.2.3"
	"$bin" --version 2>/dev/null | awk '{print $2}'
}

install_tarball() {
	prefix="${INSTALL_PREFIX:-/usr}"
	arch="$(detect_arch)"
	case "$arch" in
	amd64 | arm64) ;;
	*) die "unsupported architecture: $arch (vnprox ships amd64 and arm64 builds only)" ;;
	esac

	work="$(mktemp -d)"
	trap 'rm -rf "$work"' EXIT

	log "resolving the current vnprox version from $DIST_URL"
	if ! curl -fsSL "$DIST_URL/latest.txt" -o "$work/latest.txt"; then
		die "could not fetch $DIST_URL/latest.txt"
	fi
	version="$(tr -d '[:space:]' <"$work/latest.txt")"
	[ -n "$version" ] || die "$DIST_URL/latest.txt is empty"
	# latest.txt is an unsigned POINTER, and that is a deliberate, bounded
	# trust decision rather than an oversight: it names a version, and the
	# only versions it can name are ones whose archive carries a valid
	# signature by the pinned release key, because that signature is checked
	# below before anything is unpacked. A tampered pointer can therefore
	# select a different *genuine* release (a rollback), not an artifact of
	# the attacker's choosing. Rollback protection needs a signed manifest
	# with monotonic version metadata; it is not solved here and is not
	# claimed to be.
	log "installing vnprox $version ($arch) under $prefix"

	if current="$(installed_version "$prefix")" && [ "$current" = "$version" ]; then
		log "vnprox $version is already installed at $prefix — nothing to do"
		return 0
	fi

	tarball="vnprox_${version}_${arch}.tar.gz"
	curl -fsSL "$DIST_URL/$tarball" -o "$work/$tarball" ||
		die "could not download $DIST_URL/$tarball"
	curl -fsSL "$DIST_URL/$tarball.asc" -o "$work/$tarball.asc" ||
		die "could not download the signature $DIST_URL/$tarball.asc — refusing to install an unverified artifact"

	anchor="$(release_trust_anchor "$work")"
	verify_signature "$work/$tarball" "$work/$tarball.asc" "$anchor"

	# Unpacked only after the signature has been checked. Into a staging
	# directory first, so a truncated or hostile archive cannot leave a
	# half-installed prefix behind.
	stage="$work/stage"
	mkdir -p "$stage"
	tar -xzf "$work/$tarball" -C "$stage" || die "could not unpack $tarball"

	install -d -m 0755 "$prefix/bin"
	found=0
	for binary in vnproxd vnproxctl vnprox-setup; do
		src="$(find "$stage" -type f -name "$binary" -print -quit)"
		if [ -n "$src" ]; then
			install -m 0755 "$src" "$prefix/bin/$binary"
			found=$((found + 1))
		fi
	done
	[ "$found" -gt 0 ] || die "$tarball contained none of vnproxd/vnproxctl/vnprox-setup"

	log "installed $found binaries into $prefix/bin"
	rm -rf "$work"
	trap - EXIT
}

if [ -n "$INSTALL_PREFIX" ]; then
	# --prefix is the unprivileged/air-gapped shape: install the binaries and
	# stop. Node setup (the PVE token, the cluster secret, systemd) needs root
	# and a real PVE node, and neither is implied by "put the binaries here".
	install_tarball
	cat <<EOF

vnprox binaries installed under $INSTALL_PREFIX/bin.

This was a binaries-only install: no systemd unit, no PVE API token, no
config. Run '$INSTALL_PREFIX/bin/vnproxd --demo' to explore vnprox against
its built-in synthetic cluster, or re-run this installer as root without
--prefix to set up a real node.
EOF
	exit 0
fi

# --- step 1: PVE version/arch + cluster membership ------------------------

log "step 1/9: checking Proxmox VE version and cluster membership"

if command -v pveversion >/dev/null 2>&1; then
	PVE_VERSION="$(pveversion 2>/dev/null || true)"
	log "detected: $PVE_VERSION"
elif [ "$SKIP_PVE_CHECK" -eq 1 ]; then
	warn "pveversion not found; continuing anyway (--skip-pve-check)"
else
	die "pveversion not found — this doesn't look like a Proxmox VE node (docs/deployment.md: PVE 8.2+/9.x only). Pass --skip-pve-check to override for testing."
fi

ARCH="$(dpkg --print-architecture 2>/dev/null || uname -m)"
case "$ARCH" in
amd64 | arm64) log "architecture: $ARCH (supported)" ;;
*)
	if [ "$SKIP_PVE_CHECK" -eq 1 ]; then
		warn "architecture $ARCH is not amd64/arm64; continuing anyway (--skip-pve-check)"
	else
		die "unsupported architecture: $ARCH (vnprox ships amd64 and arm64 builds only)"
	fi
	;;
esac

NODE_LIST=()
if command -v pvecm >/dev/null 2>&1 && pvecm status >/dev/null 2>&1; then
	# `pvecm nodes` output is a short banner ("Membership information", a
	# "----" rule, and a "Nodeid Votes Name" column header — the exact
	# line count isn't treated as a stable contract here) followed by one
	# data row per node, e.g.:
	#   Membership information
	#   ----------------------
	#       Nodeid      Votes Name
	#            1          1 pve1 (local)
	#            2          1 pve2
	# Matched by shape instead of by skipping a fixed number of header
	# lines: a data row's first two whitespace-separated fields are both
	# plain integers (nodeid, votes) — nothing in the banner looks like
	# that. The node name is then the last field, EXCEPT this node's own
	# row, which pvecm suffixes with a literal " (local)" — stripped
	# before taking the last field, or "(local)" itself would be read as
	# a node name (T-606's container-cluster test caught this against a
	# fixture shaped like real `pvecm nodes` output).
	while IFS= read -r node; do
		[ -n "$node" ] && NODE_LIST+=("$node")
	done < <(pvecm nodes 2>/dev/null | awk '$1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { sub(/ \(local\)$/, ""); print $NF }')
fi
if [ "${#NODE_LIST[@]}" -eq 0 ]; then
	log "no cluster detected (or pvecm unavailable): treating this as a single-node install"
else
	log "cluster nodes detected: ${NODE_LIST[*]}"
fi

# --- step 2: port conflict detection (delegated) --------------------------
#
# Kept identical in spirit to vnprox-setup's resolve_port(), duplicated
# here (not sourced) because install.sh is documented to be a single
# stand-alone file fetched via curl before any package — including
# vnprox-setup — is on disk.

log "step 2/9: checking port $DEFAULT_PORT for conflicts"

port_listening() {
	port="$1"
	if command -v ss >/dev/null 2>&1 && ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[.:]${port}\$"; then
		return 0
	fi
	if command -v netstat >/dev/null 2>&1 && netstat -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[.:]${port}\$"; then
		return 0
	fi
	if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
		exec 3<&- 2>/dev/null || true
		exec 3>&- 2>/dev/null || true
		return 0
	fi
	return 1
}

pbs_installed() {
	command -v dpkg-query >/dev/null 2>&1 &&
		dpkg-query -W -f='${Status}' proxmox-backup-server 2>/dev/null | grep -q "install ok installed"
}

if [ -n "$FORCE_PORT" ]; then
	LISTEN_PORT="$FORCE_PORT"
	log "using explicitly requested port $LISTEN_PORT"
else
	conflict=0
	if port_listening "$DEFAULT_PORT"; then
		warn "something is already listening on port $DEFAULT_PORT"
		conflict=1
	fi
	if pbs_installed; then
		warn "proxmox-backup-server is installed (it also serves its web UI on $DEFAULT_PORT)"
		conflict=1
	fi

	if [ "$conflict" -eq 0 ]; then
		LISTEN_PORT="$DEFAULT_PORT"
	elif [ "$ASSUME_YES" -eq 1 ]; then
		LISTEN_PORT=8008
		log "port $DEFAULT_PORT unavailable; using fallback port $LISTEN_PORT (non-interactive)"
	else
		read -r -p "Port $DEFAULT_PORT looks unavailable. Use alternative port [8008]: " chosen || true
		LISTEN_PORT="${chosen:-8008}"
	fi
fi
log "resolved listen port: $LISTEN_PORT"

# --- step 3: install the .deb ---------------------------------------------
#
# docs/deployment.md: "Installs the vnprox .deb (from the apt repo it
# configures, or a bundled offline .deb with --offline <file>)." See
# packaging/apt-repo.md for the repo layout and signing key this configures
# a client for; packaging/build-apt-repo.sh / release.yml build and sign it.

log "step 3/9: installing the vnprox package"

KEYRING_PATH="/usr/share/keyrings/vnprox-archive-keyring.gpg"
APT_SOURCE_PATH="/etc/apt/sources.list.d/vnprox.list"

if [ -n "$OFFLINE_DEB" ]; then
	[ -f "$OFFLINE_DEB" ] || die "--offline file not found: $OFFLINE_DEB"
	# A local file the operator chose and already holds is a different trust
	# decision from a download: nothing about it passed through a network on
	# this run. It is still verified when a detached signature sits next to
	# it, which is what a release download unpacked by hand looks like. When
	# there is none — a `make deb` artifact built on this machine, which is
	# what this repository's own container tests install — the operator is
	# told plainly what they are trusting instead of being stopped.
	if [ -f "$OFFLINE_DEB.asc" ]; then
		owork="$(mktemp -d)"
		verify_signature "$OFFLINE_DEB" "$OFFLINE_DEB.asc" "$(release_trust_anchor "$owork")"
		rm -rf "$owork"
	else
		warn "no $OFFLINE_DEB.asc next to the package: installing a local file on your say-so, unverified. A release download ships a .asc alongside the .deb; put it next to the package to have this verified."
	fi
	if command -v apt-get >/dev/null 2>&1; then
		apt-get install -y "$OFFLINE_DEB"
	else
		dpkg -i "$OFFLINE_DEB" || die "dpkg -i $OFFLINE_DEB failed"
	fi
elif [ "$FORCE_TARBALL" -eq 1 ] || ! command -v apt-get >/dev/null 2>&1; then
	# "installs from the signed apt repository where available and falls back
	# to a binary tarball" — this is the fallback, taken automatically on a
	# host with no apt-get rather than dying the way this script used to.
	if [ "$FORCE_TARBALL" -ne 1 ]; then
		log "no apt-get on this host: falling back to the signed binary tarball"
	fi
	install_tarball
else
	log "configuring the vnprox apt repo at $APT_REPO_URL"
	if ! command -v gpg >/dev/null 2>&1; then
		die "gpg not found (needed to verify the apt repo signing key) — install gnupg, or use --offline <file>"
	fi

	# The pinned-fingerprint check happens HERE, before the key is installed
	# into the system keyring — apt will happily verify the repo against
	# whatever key it is given, so "apt verified the signature" says nothing
	# unless the key itself was checked first.
	awork="$(mktemp -d)"
	anchor="$(release_trust_anchor "$awork")"

	install -d -m 0755 "$(dirname "$KEYRING_PATH")"
	if ! gpg --dearmor <"$anchor" >"$KEYRING_PATH.tmp" 2>/dev/null; then
		rm -f "$KEYRING_PATH.tmp"
		rm -rf "$awork"
		die "could not convert the verified release key into an apt keyring"
	fi
	mv "$KEYRING_PATH.tmp" "$KEYRING_PATH"
	chmod 0644 "$KEYRING_PATH"
	rm -rf "$awork"

	# AC5, the "no duplicate sources entry" half. Writing our own file with
	# > is already idempotent, but an entry added by hand to another file
	# (or by an older version of this script to another path) would leave
	# apt with two sources for the same repo, which is the exact symptom the
	# criterion names. Strip those first, then write exactly one.
	for other in /etc/apt/sources.list /etc/apt/sources.list.d/*.list; do
		[ -f "$other" ] || continue
		[ "$other" = "$APT_SOURCE_PATH" ] && continue
		if grep -qF "$APT_REPO_URL" "$other" 2>/dev/null; then
			warn "removing a duplicate vnprox apt entry from $other"
			grep -vF "$APT_REPO_URL" "$other" >"$other.vnprox-tmp" && mv "$other.vnprox-tmp" "$other"
		fi
	done
	echo "deb [signed-by=$KEYRING_PATH] $APT_REPO_URL stable main" >"$APT_SOURCE_PATH"
	log "wrote $APT_SOURCE_PATH"

	if ! apt-get update; then
		die "'apt-get update' failed against $APT_REPO_URL — check network reachability, or use --offline <path-to-deb>"
	fi
	if ! apt-get install -y vnprox; then
		die "'apt-get install vnprox' failed — check $APT_SOURCE_PATH, or use --offline <path-to-deb>"
	fi
fi

# --- step 4: lldpd ---------------------------------------------------------

log "step 4/9: lldpd"
if [ -z "$WITH_LLDP" ]; then
	if [ "$ASSUME_YES" -eq 1 ]; then
		WITH_LLDP=yes
	else
		read -r -p "Install and enable lldpd for physical topology discovery? [Y/n] " ans || true
		case "$ans" in
		[nN]*) WITH_LLDP=no ;;
		*) WITH_LLDP=yes ;;
		esac
	fi
fi
log "lldpd choice: $WITH_LLDP (applied per-node by vnprox-setup below)"

# --- steps 5-7: delegate this node's setup to vnprox-setup -----------------

log "steps 5-7/9: running vnprox-setup for this node"
if ! command -v vnprox-setup >/dev/null 2>&1; then
	die "vnprox-setup not found on PATH after package install — packaging bug, please report"
fi

setup_args=(--port "$LISTEN_PORT" --yes)
if [ "$WITH_LLDP" = yes ]; then
	setup_args+=(--with-lldp)
else
	setup_args+=(--no-lldp)
fi
vnprox-setup "${setup_args[@]}"

# --- step 8: remaining cluster nodes ---------------------------------------
#
# docs/deployment.md: "the script offers to roll out to all cluster nodes
# via SSH" / "Repeats 3-7 on the remaining nodes (via SSH root, same
# mechanism pvecm setups already rely on), or prints per-node instructions
# if SSH between nodes is unavailable." Rollout is evaluated *per node*
# (not all-or-nothing): a node this script can reach over SSH gets the full
# automated flow; a node it can't falls back to printed instructions for
# that node alone, so one unreachable node doesn't block the rest.

log "step 8/9: remaining cluster nodes"

SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new)

# ssh_reachable checks root@$1 is reachable non-interactively (BatchMode:
# never prompts for a password/passphrase — if key-based root SSH isn't
# already set up, as pvecm cluster joins require, this fails fast rather
# than hanging).
ssh_reachable() {
	ssh "${SSH_OPTS[@]}" "root@$1" true >/dev/null 2>&1
}

# remote_lldp_flag mirrors this node's own $WITH_LLDP choice onto the
# remote vnprox-setup invocation, so a cluster rollout applies one
# consistent lldpd decision everywhere rather than re-asking per node.
remote_lldp_flag() {
	if [ "$WITH_LLDP" = yes ]; then
		echo "--with-lldp"
	else
		echo "--no-lldp"
	fi
}

print_manual_instructions() {
	node="$1"
	reason="$2"
	log "  - $node: $reason; manual install required:"
	if [ -n "$OFFLINE_DEB" ]; then
		log "      scp $OFFLINE_DEB root@$node:/root/$(basename "$OFFLINE_DEB")"
		log "      ssh root@$node apt-get install -y /root/$(basename "$OFFLINE_DEB")"
	else
		log "      apt install ./vnprox_<version>_${ARCH}.deb   # or 'apt install vnprox' once an apt repo is configured"
	fi
	log "      vnprox-setup --port $LISTEN_PORT --yes $(remote_lldp_flag)"
}

rollout_to_node() {
	node="$1"
	log "  - $node: reachable over SSH, rolling out"

	if [ -n "$OFFLINE_DEB" ]; then
		remote_deb="/root/$(basename "$OFFLINE_DEB")"
		if ! scp "${SSH_OPTS[@]}" "$OFFLINE_DEB" "root@$node:$remote_deb" >/dev/null; then
			warn "$node: scp of $OFFLINE_DEB failed"
			print_manual_instructions "$node" "package transfer failed"
			return 1
		fi
		if ! ssh "${SSH_OPTS[@]}" "root@$node" "apt-get install -y '$remote_deb'"; then
			warn "$node: remote package install failed"
			print_manual_instructions "$node" "package install failed"
			return 1
		fi
	else
		# No --offline package given: this only succeeds once a real apt
		# repository is configured on the remote node (T-606's apt-repo
		# tooling — packaging/apt-repo.md, release.yml — publishes one; see
		# that doc for the client-side `apt` source line). Until then this
		# is expected to fail on a fresh node with no vnprox repo configured
		# yet, which is exactly why --offline exists as the documented
		# fallback (docs/deployment.md: "from the apt repo it configures,
		# or a bundled offline .deb with --offline <file>").
		if ! ssh "${SSH_OPTS[@]}" "root@$node" "apt-get update && apt-get install -y vnprox"; then
			warn "$node: 'apt-get install vnprox' failed (no apt repo configured there yet?)"
			print_manual_instructions "$node" "no apt repo configured and no --offline package given"
			return 1
		fi
	fi

	if ! ssh "${SSH_OPTS[@]}" "root@$node" "vnprox-setup --port '$LISTEN_PORT' --yes $(remote_lldp_flag)"; then
		warn "$node: remote vnprox-setup failed"
		return 1
	fi
	log "  - $node: done"
	return 0
}

if [ "${#NODE_LIST[@]}" -le 1 ]; then
	log "single-node install: nothing more to roll out"
else
	other_nodes=()
	self_name="$(hostname -s 2>/dev/null || hostname)"
	for node in "${NODE_LIST[@]}"; do
		[ "$node" = "$self_name" ] || other_nodes+=("$node")
	done

	proceed=1
	if [ "$ASSUME_YES" -ne 1 ]; then
		read -r -p "Roll out vnprox to the other ${#other_nodes[@]} cluster node(s) via SSH now? [Y/n] " ans || true
		case "$ans" in
		[nN]*) proceed=0 ;;
		*) proceed=1 ;;
		esac
	fi

	if [ "$proceed" -ne 1 ]; then
		log "skipping SSH rollout by request; per-node manual instructions:"
		for node in "${other_nodes[@]}"; do
			print_manual_instructions "$node" "rollout declined"
		done
	elif ! command -v ssh >/dev/null 2>&1; then
		log "ssh not found on this node; per-node manual instructions:"
		for node in "${other_nodes[@]}"; do
			print_manual_instructions "$node" "ssh not available on this node"
		done
	else
		failed_nodes=()
		for node in "${other_nodes[@]}"; do
			if ssh_reachable "$node"; then
				if ! rollout_to_node "$node"; then
					failed_nodes+=("$node")
				fi
			else
				print_manual_instructions "$node" "not reachable over root SSH (BatchMode)"
			fi
		done
		if [ "${#failed_nodes[@]}" -gt 0 ]; then
			warn "rollout did not complete on: ${failed_nodes[*]} (see messages above)"
		fi
	fi
fi

# --- step 9: self-check + URL + checklist -----------------------------------

# `vnproxctl doctor` (T-1904) verifies what this installer just did: config
# sanity, key-file permissions, pmxcfs, schema version, disk headroom, and the
# listen port. It ships in the package, so it only exists from step 3 onward —
# this is a post-install verification, not a pre-install preflight.
#
# It reports; it does not abort. Two reasons, both deliberate:
#
#  1. Aborting *after* the package is installed and the cluster rollout has run
#     would leave a half-configured cluster and no clear way back. The operator
#     is better served by a complete install plus an accurate list of what is
#     wrong.
#  2. `packaging/test/cluster-ssh.sh` and `port-conflict.sh` drive this script
#     inside plain Debian containers where some checks legitimately fail (no
#     pmxcfs). Making doctor fatal here would fail those tests for a correct
#     reason and change the behaviour of a script that is currently the subject
#     of an open, unexplained CI failure (T-1806-bug-02) — see that card on why
#     the subject of an unexplained failure is not perturbed mid-investigation.
#
# Turning this into a hard gate is tracked as T-1904-followup-01.
if command -v vnproxctl >/dev/null 2>&1; then
	log "step 9/9: verifying this install (vnproxctl doctor)"
	if vnproxctl doctor; then
		log "self-check passed"
	else
		warn "self-check reported problems — see the report above. vnprox is installed; each line names what to do."
	fi
else
	log "step 9/9: done"
fi
cat <<EOF

vnprox installed on $(hostname -f 2>/dev/null || hostname).

  URL: https://$(hostname -f 2>/dev/null || hostname):${LISTEN_PORT}

First-login checklist:
  - Log in with your existing Proxmox VE credentials.
  - Restrict port ${LISTEN_PORT} to management networks (docs/security.md
    "Firewalling vnprox itself"); allow node<->node traffic on the same port.
  - All nodes in a cluster must use the same port — this installer's SSH
    rollout (or the per-node instructions it printed above for any node it
    could not reach) applies the same port everywhere.
  - Verify each node's PVE API token was provisioned: vnproxctl status
    reports "PVE API health" once vnprox.service is running.
  - Cross-node items this sandbox cannot fully verify itself (pmxcfs
    replication of the cluster secret to every joined node, and a real
    apt-repo-backed rollout with no --offline package) are noted in the
    T-606 report as needing hardware validation; the mechanisms above are
    real, not stubbed.
EOF
