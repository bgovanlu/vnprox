#!/usr/bin/env bash
# planning/validation/harness/host.sh — Host / OS behavior section (T-1801).
#
# Covers `planning/reports/needs-hardware-validation.md`'s "## Host / OS
# behavior" heading: `systemctl start vnprox`, real netlint/LLDP/bonding
# readers, and PVE-cert reuse/hot-reload. Read-only throughout — every
# check here inspects existing state (service status, /proc, netlink,
# a TLS handshake) without changing it. WireGuard-specific items from the
# same doc heading live in wireguard.sh; capture/conntrack/corosync items
# live in capture.sh — split to match T-1802's section grouping.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/host.sh > host-evidence.json
# Against a dev machine (no PVE, no pvemock needed — every check here is
# either always-available Linux tooling or gracefully reports absence):
#   bash planning/validation/harness/host.sh
#
# Env overrides:
#   PVE_BOND_IFACE   a bond interface name to inspect (default: auto-detect
#                     the first entry under /proc/net/bonding, if any).
#   PVE_CERT_HOST    host:port to TLS-probe for the pveproxy cert
#                     (default: localhost:8006).

SECTION="host"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

: "${PVE_BOND_IFACE:=}"
: "${PVE_CERT_HOST:=localhost:8006}"

# --- host-01: systemctl start vnprox from the .deb ------------------------
harness_item "host-01" \
	"Host / OS behavior > systemctl start vnprox from the .deb" \
	'if command -v systemctl >/dev/null 2>&1; then
	systemctl show vnprox.service -p ActiveState,SubState,UnitFileState,MainPID 2>&1
else
	echo "systemctl not present on this host"
fi'

# --- host-02: real netlink link/bond state --------------------------------
harness_item "host-02" \
	"Host / OS behavior > Real netlink/LLDP/bonding readers (link+bond state)" \
	'if command -v ip >/dev/null 2>&1; then
	ip -d link show 2>&1
else
	echo "ip(8) not present on this host"
fi'

# --- host-03: LACP actor/partner detail parsing ---------------------------
harness_item "host-03" \
	"Host / OS behavior > LACP actor/partner detail parsing (T-804)" \
	'iface="$PVE_BOND_IFACE"
if [ -z "$iface" ] && [ -d /proc/net/bonding ]; then
	iface="$(ls /proc/net/bonding 2>/dev/null | head -1)"
fi
if [ -z "$iface" ]; then
	echo "no bond interface found under /proc/net/bonding (define PVE_BOND_IFACE to force one)"
	exit 3
fi
cat "/proc/net/bonding/$iface" 2>&1'

# --- host-04: LLDP neighbor data -------------------------------------------
harness_item "host-04" \
	"Host / OS behavior > Real netlink/LLDP/bonding readers (LLDP)" \
	'if command -v lldpcli >/dev/null 2>&1; then
	lldpcli show neighbors -f json 2>&1
elif command -v lldpctl >/dev/null 2>&1; then
	lldpctl -f json 2>&1
else
	echo "lldpd tooling (lldpcli/lldpctl) not present on this host"
fi'

# --- host-05: pveproxy certificate reuse/hot-reload -----------------------
harness_item "host-05" \
	"Host / OS behavior > PVE-cert reuse + hot-reload" \
	'if command -v openssl >/dev/null 2>&1; then
	echo | openssl s_client -connect "$PVE_CERT_HOST" -servername "${PVE_CERT_HOST%%:*}" 2>/dev/null \
		| openssl x509 -noout -subject -issuer -dates 2>&1
else
	echo "openssl not present on this host"
fi'

harness_emit
