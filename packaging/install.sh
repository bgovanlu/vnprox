#!/usr/bin/env bash
# vnprox quick-install script — docs/deployment.md "Quick install (script)".
#
#   curl -fsSL https://get.vnprox.io/install.sh -o install.sh
#   less install.sh   # you're piping root on a hypervisor — read it
#   bash install.sh
#
# Documented flow (docs/deployment.md numbering kept in the comments below):
#   1. Verify PVE version/arch; detect cluster membership and node list.
#   2. Check port 8007 (PBS conflict); ask for the listen port if needed.
#   3. Install the vnprox .deb (apt repo, or --offline <file>).
#   4. Optionally install + enable lldpd on all nodes.
#   5. Create the read-only PVE API token vnprox@pve!daemon.
#   6. Generate the cluster secret in /etc/pve/vnprox/ (first node only).
#   7. Write /etc/vnprox/vnprox.toml, generate the session key, enable +
#      start vnprox.service.
#   8. Repeat 3-7 on the remaining nodes via SSH, or print per-node
#      instructions if SSH between nodes is unavailable.
#   9. Print the URL and a first-login checklist.
#
# Steps 2 and 5-7 for *this* node are delegated to vnprox-setup (installed
# by the .deb at /usr/bin/vnprox-setup) rather than duplicated here, so
# there is exactly one implementation of "set up a single node" — see
# packaging/bin/vnprox-setup.
#
# What this script deliberately stubs, and why (planning/tasks/
# phase-0.md#T-006's instruction: don't pretend untestable things work):
#   - Installing from a real apt repository: no signed vnprox apt repo
#     exists yet (release.yml / repo tooling is T-606's job per
#     planning/tasks/phase-6.md#T-606) — use --offline <deb> for now.
#   - PVE API token creation: needs a live `pveum` against a real PVE
#     cluster (see vnprox-setup's own TODO(T-606) for the exact commands).
#   - Multi-node SSH rollout: needs real inter-node root SSH the way
#     `pvecm` setups rely on, which this sandbox cannot exercise — falls
#     back to printing per-node manual instructions, as the doc allows
#     ("or prints per-node instructions if SSH between nodes is
#     unavailable").
#
# planning/tasks/phase-6.md#T-606 explicitly owns finishing all of the
# above: "cluster rollout and PVE token creation are completed in T-606."

set -euo pipefail

PROG=$(basename "$0")
DEFAULT_PORT=8007

OFFLINE_DEB=""
FORCE_PORT=""
WITH_LLDP=""
ASSUME_YES=0
SKIP_PVE_CHECK=0

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
  --offline <file>     Install from this local .deb instead of an apt repo
                        (required for now — see the TODO(T-606) note in
                        this script's header; there is no vnprox apt repo
                        yet).
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

if [ "$(id -u)" -ne 0 ]; then
	die "must run as root"
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
	# `pvecm nodes` output includes a header; node names are the last
	# whitespace-separated field of each data row.
	while IFS= read -r node; do
		[ -n "$node" ] && NODE_LIST+=("$node")
	done < <(pvecm nodes 2>/dev/null | awk 'NR>2 && NF{print $NF}')
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

log "step 3/9: installing the vnprox package"

if [ -n "$OFFLINE_DEB" ]; then
	[ -f "$OFFLINE_DEB" ] || die "--offline file not found: $OFFLINE_DEB"
	if command -v apt-get >/dev/null 2>&1; then
		apt-get install -y "$OFFLINE_DEB"
	else
		dpkg -i "$OFFLINE_DEB" || die "dpkg -i $OFFLINE_DEB failed"
	fi
else
	log "TODO(T-606): no signed vnprox apt repository exists yet; this script cannot"
	log "  'apt install vnprox' from one. Re-run with --offline <path-to-vnprox.deb>"
	log "  using a package built by 'make deb' (dist/vnprox_*.deb) in the meantime."
	die "no install source given: pass --offline <file> (see TODO(T-606) above)"
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

log "step 8/9: remaining cluster nodes"
if [ "${#NODE_LIST[@]}" -le 1 ]; then
	log "single-node install: nothing more to roll out"
else
	log "TODO(T-606): automatic multi-node SSH rollout is not implemented in this"
	log "  installer skeleton (it needs the same root-SSH access pvecm setups rely"
	log "  on, which cannot be exercised in this sandbox). Per-node manual"
	log "  instructions, as the doc allows when SSH rollout isn't available:"
	for node in "${NODE_LIST[@]}"; do
		log "    - on $node: apt install ./vnprox_<version>_${ARCH}.deb && vnprox-setup --port $LISTEN_PORT --yes"
	done
fi

# --- step 9: URL + checklist -----------------------------------------------

log "step 9/9: done"
cat <<EOF

vnprox installed on $(hostname -f 2>/dev/null || hostname).

  URL: https://$(hostname -f 2>/dev/null || hostname):${LISTEN_PORT}

First-login checklist:
  - Log in with your existing Proxmox VE credentials.
  - Restrict port ${LISTEN_PORT} to management networks (docs/security.md
    "Firewalling vnprox itself"); allow node<->node traffic on the same port.
  - All nodes in a cluster must use the same port — re-run this installer
    (or vnprox-setup --port ${LISTEN_PORT}) on every other node.
  - Remaining TODO(T-606) items printed above (apt repo, PVE token, cluster
    secret cross-node verification, SSH rollout) still need a real PVE
    cluster to finish and verify.
EOF
