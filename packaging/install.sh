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
# packaging/bin/vnprox-setup. Step 8 (SSH rollout below) drives that same
# script remotely, per node.
#
# What this script cannot fully exercise in this sandbox, and why
# (planning/tasks/phase-0.md#T-006's instruction: don't pretend untestable
# things work) — see the T-606 completion report for the exact "needs
# hardware validation" list:
#   - Installing from a real, signed apt repository (no `--offline <deb>`):
#     the repo tooling exists now (packaging/apt-repo.md, release.yml), but
#     there is no live get.vnprox.io to actually publish to and no real PVE
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

set -euo pipefail

PROG=$(basename "$0")
DEFAULT_PORT=8007

OFFLINE_DEB=""
FORCE_PORT=""
WITH_LLDP=""
ASSUME_YES=0
SKIP_PVE_CHECK=0
APT_REPO_URL="https://get.vnprox.io/apt"

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
  --apt-repo <url>      Base URL of the vnprox apt repo (default:
                        https://get.vnprox.io/apt; see packaging/apt-repo.md).
                        Ignored when --offline is given.
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
	if command -v apt-get >/dev/null 2>&1; then
		apt-get install -y "$OFFLINE_DEB"
	else
		dpkg -i "$OFFLINE_DEB" || die "dpkg -i $OFFLINE_DEB failed"
	fi
else
	log "configuring the vnprox apt repo at $APT_REPO_URL"
	if ! command -v apt-get >/dev/null 2>&1; then
		die "apt-get not found and no --offline package given"
	fi
	if ! command -v gpg >/dev/null 2>&1; then
		die "gpg not found (needed to install the apt repo signing key) — install gnupg, or use --offline <file>"
	fi

	install -d -m 0755 "$(dirname "$KEYRING_PATH")"
	if ! curl -fsSL "$APT_REPO_URL/vnprox-archive-keyring.gpg" | gpg --dearmor >"$KEYRING_PATH.tmp" 2>/dev/null; then
		rm -f "$KEYRING_PATH.tmp"
		die "could not fetch/import the vnprox apt signing key from $APT_REPO_URL (no live vnprox apt repo reachable from this host? use --offline <path-to-deb> instead — see 'make deb' / dist/vnprox_*.deb)"
	fi
	mv "$KEYRING_PATH.tmp" "$KEYRING_PATH"
	chmod 0644 "$KEYRING_PATH"

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

# --- step 9: URL + checklist -----------------------------------------------

log "step 9/9: done"
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
