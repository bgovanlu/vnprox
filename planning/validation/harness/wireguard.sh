#!/usr/bin/env bash
# planning/validation/harness/wireguard.sh — WireGuard section (T-1801).
# Read-only.
#
# Covers `planning/reports/needs-hardware-validation.md`'s "## Host / OS
# behavior" heading's two WireGuard/pmxcfs-sandbox bullets: "WireGuard
# changeset apply under ProtectSystem=strict (v3.0.2)" and "Is /etc/pve
# (pmxcfs FUSE) even read-only under ProtectSystem=strict?". Deliberately
# static/inspection-only — it does not trigger a real WireGuard apply
# (that is a real network mutation through the change engine, T-1804's
# territory) or attempt a write into /etc/pve (which would itself be the
# mutation the second bullet is asking whether the sandbox prevents).
# Instead it gathers the systemd/filesystem facts a human/triage step needs
# to answer both questions from the outside.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/wireguard.sh > wireguard-evidence.json
# Against a dev machine (no PVE, no pvemock needed):
#   bash planning/validation/harness/wireguard.sh

SECTION="wireguard"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

# --- wireguard-01: wg/wg-quick tooling presence ----------------------------
harness_item "wireguard-01" \
	"Host / OS behavior > WireGuard changeset apply under ProtectSystem=strict (tooling)" \
	'for bin in wg wg-quick; do
	if command -v "$bin" >/dev/null 2>&1; then
		echo "$bin: $(command -v "$bin") ($($bin --version 2>&1 | head -1))"
	else
		echo "$bin: not present"
	fi
done'

# --- wireguard-02: /etc/wireguard existence + permissions ------------------
harness_item "wireguard-02" \
	"Host / OS behavior > WireGuard changeset apply under ProtectSystem=strict (/etc/wireguard)" \
	'if [ -d /etc/wireguard ]; then
	stat -c "path=%n mode=%a owner=%U group=%G" /etc/wireguard 2>&1
else
	echo "/etc/wireguard does not exist"
fi'

# --- wireguard-03: vnprox.service sandbox directives -----------------------
harness_item "wireguard-03" \
	"Host / OS behavior > WireGuard changeset apply under ProtectSystem=strict (unit sandbox)" \
	'if command -v systemctl >/dev/null 2>&1; then
	systemctl show vnprox.service -p ProtectSystem,ReadWritePaths,ReadOnlyPaths,InaccessiblePaths 2>&1
else
	echo "systemctl not present on this host"
fi'

# --- wireguard-04: /etc/pve (pmxcfs) mount + read-only-under-sandbox facts -
harness_item "wireguard-04" \
	"Host / OS behavior > Is /etc/pve (pmxcfs FUSE) even read-only under ProtectSystem=strict?" \
	'if command -v mount >/dev/null 2>&1; then
	mount | grep -E " /etc/pve | on /etc/pve " 2>&1 || echo "/etc/pve not found in mount table (not a PVE node, or pmxcfs not running)"
else
	echo "mount(8) not present on this host"
fi
if [ -e /etc/pve/priv/vnprox/cluster.secret ]; then
	stat -c "path=%n mode=%a owner=%U group=%G" /etc/pve/priv/vnprox/cluster.secret 2>&1
else
	echo "/etc/pve/priv/vnprox/cluster.secret not present"
fi'

harness_emit
