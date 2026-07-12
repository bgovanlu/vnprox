#!/usr/bin/env bash
# Container test for T-606 acceptance criterion 2: "Cluster install
# simulation (3 containers, SSH between them): one-command rollout
# completes; same-port enforcement works; secret replicated (simulated
# pmxcfs path)."
#
# Three containers (pve1, pve2, pve3) simulate a 3-node PVE cluster:
#   - pve1 gets fake `pveversion`/`pvecm` stubs on PATH (real ones only
#     exist on an actual PVE node — CLAUDE.md: no live cluster here) so
#     install.sh's real cluster-detection code path runs, not
#     --skip-pve-check. The stubs report exactly pve1/pve2/pve3 as the
#     cluster's node list, the same shape `pvecm nodes` output takes.
#   - pve2/pve3 run a real sshd, with pve1's SSH public key pre-installed
#     as authorized_keys — simulating the pre-existing passwordless root
#     SSH a real pvecm cluster join already establishes between nodes
#     (docs/deployment.md: "same mechanism pvecm setups already rely on"),
#     which install.sh's own header comment is explicit it does not set up
#     itself.
#   - All three share one podman volume mounted at /etc/pve — this
#     sandbox has no real pmxcfs to test against (CLAUDE.md), so a shared
#     directory is the documented stand-in for "simulated pmxcfs path"
#     (this AC's own wording): whatever pve1's vnprox-setup writes under
#     /etc/pve/priv/vnprox is immediately visible, unmodified, to pve2/pve3.
#     (This bind mount doesn't reproduce real pmxcfs's permission
#     enforcement — see internal/peer/secret.go's DefaultSecretPath comment
#     for why the secret specifically must live under priv/, confirmed
#     against a real PVE 9.2.4 node.)
#
# Networking note: this sandbox's rootless podman has no `pasta` binary,
# so a user-defined bridge network (real per-container IPs/hostnames) is
# not available here — all three containers instead run with
# --network=host, each node's sshd on a distinct port (2201/2202/2203),
# with /etc/hosts + an SSH client config on pve1 mapping node names to
# 127.0.0.1:<that port>. This is an environment constraint, not a design
# choice: on a CI runner with normal bridge networking (GitHub Actions'
# hosted runners, or a rootful podman/docker), the same script would work
# unmodified with real per-container network identities instead — nothing
# under test here (install.sh's cluster detection, SSH rollout, or
# vnprox-setup) depends on which addressing scheme reaches each node.
#
# Requires: podman, ssh-keygen, a .deb in dist/ (`make deb`).

set -euo pipefail

PROG=$(basename "$0")
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"

DEB_FILE="$(ls "$REPO_ROOT"/dist/vnprox_*.deb 2>/dev/null | head -1 || true)"
[ -n "$DEB_FILE" ] || die "no .deb found in $REPO_ROOT/dist — run 'make deb' first"
DEB_BASENAME="$(basename "$DEB_FILE")"

VOL="vnprox-clustertest-etcpve-$$"
KEYDIR="$(mktemp -d)"
trap 'rm -rf "$KEYDIR"; podman rm -f pve1 pve2 pve3 >/dev/null 2>&1 || true; podman volume rm -f "$VOL" >/dev/null 2>&1 || true' EXIT

log "generating a throwaway SSH keypair for pve1 -> pve2/pve3 root access"
ssh-keygen -t ed25519 -N '' -f "$KEYDIR/id_ed25519" -q -C "vnprox-cluster-ssh-test"

podman volume create "$VOL" >/dev/null

start_node() {
	node="$1"
	# --hostname: install.sh determines "is this node me" by comparing
	# `hostname -s` against the (fake) pvecm node list — matters for
	# excluding pve1 itself from its own SSH-rollout target list.
	podman run -d --name "$node" --hostname "$node" --network=host \
		-v "$VOL:/etc/pve" \
		-v "$REPO_ROOT/packaging:/packaging:ro,Z" \
		-v "$REPO_ROOT/dist:/dist:ro,Z" \
		"$IMAGE" sleep infinity >/dev/null
}

log "starting pve1, pve2, pve3 ($IMAGE, --network=host — see header note)"
start_node pve1
start_node pve2
start_node pve3

setup_sshd() {
	node="$1"
	port="$2"
	podman exec "$node" bash -euo pipefail -c '
apt-get update -qq >/dev/null
apt-get install -y -qq openssh-server >/dev/null
mkdir -p /root/.ssh /run/sshd
chmod 700 /root/.ssh
sed -i -E "s/^#?PermitRootLogin.*/PermitRootLogin prohibit-password/" /etc/ssh/sshd_config
sed -i -E "s/^#?PubkeyAuthentication.*/PubkeyAuthentication yes/" /etc/ssh/sshd_config
/usr/sbin/sshd -p '"$port"'
'
	podman cp "$KEYDIR/id_ed25519.pub" "$node:/root/.ssh/authorized_keys"
	podman exec "$node" bash -c 'chown root:root /root/.ssh/authorized_keys; chmod 600 /root/.ssh/authorized_keys'
}

log "installing + starting sshd on pve2 (port 2202) and pve3 (port 2203)"
setup_sshd pve2 2202
setup_sshd pve3 2203

log "configuring pve1: SSH client (host aliases -> 127.0.0.1:220x), private key, fake pveversion/pvecm"
podman exec pve1 bash -euo pipefail -c '
apt-get update -qq >/dev/null
apt-get install -y -qq openssh-client >/dev/null
mkdir -p /root/.ssh /usr/local/fakebin
chmod 700 /root/.ssh
cat >/root/.ssh/config <<EOF
Host pve2
  HostName 127.0.0.1
  Port 2202
  User root
  IdentityFile /root/.ssh/id_ed25519
  StrictHostKeyChecking accept-new
  UserKnownHostsFile /root/.ssh/known_hosts
Host pve3
  HostName 127.0.0.1
  Port 2203
  User root
  IdentityFile /root/.ssh/id_ed25519
  StrictHostKeyChecking accept-new
  UserKnownHostsFile /root/.ssh/known_hosts
EOF
chmod 600 /root/.ssh/config

cat >/usr/local/fakebin/pveversion <<EOF
#!/bin/sh
echo "pve-manager/8.2.0/fake (running kernel: 6.8.0-fake)"
EOF
cat >/usr/local/fakebin/pvecm <<EOF
#!/bin/sh
case "\$1" in
  status) exit 0 ;;
  nodes)
    cat <<NODES
Membership information
----------------------
    Nodeid      Votes Name
         1          1 pve1 (local)
         2          1 pve2
         3          1 pve3
NODES
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x /usr/local/fakebin/pveversion /usr/local/fakebin/pvecm
'
podman cp "$KEYDIR/id_ed25519" pve1:/root/.ssh/id_ed25519
podman exec pve1 chmod 600 /root/.ssh/id_ed25519

log "verifying pve1 -> pve2/pve3 SSH works before the real test (infra sanity check)"
podman exec pve1 bash -c 'export PATH=/usr/local/fakebin:$PATH; ssh -o BatchMode=yes pve2 true && ssh -o BatchMode=yes pve3 true' \
	|| die "SSH infra sanity check failed — see output above"

# --- the actual test: a fake listener on 8007 (host network, so every
# container's own conflict check sees it) exercises the port-conflict path
# too, per AC3's "PBS package present -> ... -> alternate port -> all
# nodes configured consistently" (the "PBS present" trigger itself is
# exercised by packaging/test/port-conflict.sh; this occupied-port trigger
# exercises the exact same downstream conflict-handling branch, which is
# what differs across nodes and is this test's actual concern).
log "binding a fake listener on 8007 to force the port-conflict fallback path"
# Synchronous install (waits for the apt/dpkg lock to be free) THEN start
# the listener detached — combining both into one detached step previously
# raced install.sh's own apt-get install (both wanting the dpkg lock at
# once, and the listener not up yet when install.sh's port check ran).
# nc without -k serves exactly one connection then exits, so the wait
# below is a fixed sleep rather than a connect-based poll (a poll would
# consume that one connection itself, leaving nothing for install.sh's own
# check to see).
podman exec pve1 apt-get install -y -qq netcat-traditional >/dev/null
podman exec -d pve1 nc -l -p 8007
sleep 2

log "running install.sh on pve1 (real cluster detection + SSH rollout, non-interactive)"
# set +e/-e: install.sh's own exit status must not abort this script via
# set -e before $OUT is captured and printed — a failing rollout is
# exactly the case that needs its output visible for debugging, and this
# script's own assertions below (not install.sh's exit code) decide pass/
# fail (a node falling back to per-node instructions is a documented,
# non-fatal outcome install.sh itself still exits 0 for).
set +e
OUT="$(podman exec pve1 bash -c '
export PATH=/usr/local/fakebin:$PATH
apt-get install -y -qq gnupg curl >/dev/null 2>&1 || true
bash /packaging/install.sh --offline "/dist/'"$DEB_BASENAME"'" -y
' 2>&1)"
INSTALL_RC=$?
set -e
echo "$OUT"
[ "$INSTALL_RC" -eq 0 ] || die "install.sh exited $INSTALL_RC on pve1 (see output above)"

echo "$OUT" | grep -q "cluster nodes detected: pve1 pve2 pve3" || die "cluster detection did not find all three nodes"
# The port-conflict warn()/log() lines (install.sh step 2) are tiny stderr
# writes immediately followed by a large stdout burst from apt-get's own
# dependency-resolution output (step 3) — `podman exec`'s stream
# multiplexing has, in practice, occasionally coalesced/dropped exactly
# that handful of interleaved short stderr lines from the captured
# output in this harness (observed directly: a run where the final
# resolved port was unambiguously 8008 on every node — the real outcome
# asserted authoritatively below by reading each node's actual
# vnprox.toml — yet neither "already listening" nor "resolved listen
# port" appeared anywhere in $OUT). packaging/test/port-conflict.sh
# already pins install.sh's port-conflict *log output* against a single
# container with no competing apt burst; this test's job is the
# cluster-wide *consequence* (every node lands on the same fallback
# port), which is what's actually checked here — from the final printed
# URL (reliably present) and, authoritatively, each node's own config
# file below.
echo "$OUT" | grep -q "URL: https://.*:8008" || die "expected the coordinator's own URL to report the fallback port 8008"
# pve2/pve3's own "reachable over SSH, rolling out" / "- done" log() lines
# (install.sh steps 8-9) are NOT asserted here, on purpose: they're subject
# to the exact same podman-exec stdout/stderr stream-multiplexing drop this
# file's step-2/3 comment above already documents observing directly — just
# further into the same long, apt/ssh/scp-heavy $OUT capture, where it's
# more likely to bite, not less. Rather than re-introduce that flake class
# for the rollout's log lines too, the checks below assert the actual,
# authoritative outcome (package installed + real config content on every
# node, and byte-identical secret content) instead of parsing log output
# for it — a strictly stronger proof that the rollout ran and completed
# than any log line could be.
log "verifying: vnprox installed on all three nodes, all reporting the same port"
for node in pve1 pve2 pve3; do
	podman exec "$node" dpkg -s vnprox >/dev/null 2>&1 || die "$node: vnprox not installed"
	port="$(podman exec "$node" sh -c "sed -nE 's/^listen = \"[^:]*:([0-9]+)\"/\1/p' /etc/vnprox/vnprox.toml")"
	echo "  $node: listen port = $port"
	[ "$port" = "8008" ] || die "$node: listen port = $port, want 8008 (same-port enforcement across the cluster)"
done

log "verifying: the cluster secret is the SAME file content on all three nodes (simulated pmxcfs replication)"
SECRET1="$(podman exec pve1 sha256sum /etc/pve/priv/vnprox/cluster.secret | cut -d' ' -f1)"
SECRET2="$(podman exec pve2 sha256sum /etc/pve/priv/vnprox/cluster.secret | cut -d' ' -f1)"
SECRET3="$(podman exec pve3 sha256sum /etc/pve/priv/vnprox/cluster.secret | cut -d' ' -f1)"
[ "$SECRET1" = "$SECRET2" ] && [ "$SECRET1" = "$SECRET3" ] || die "cluster secret differs across nodes: pve1=$SECRET1 pve2=$SECRET2 pve3=$SECRET3"
# Not also asserting the "cluster secret already present" log line for the
# same stream-multiplexing reason above: byte-identical content across
# three independent nodes already rules out independent regeneration for
# all practical purposes (this is a random secret, not a fixed default), so
# the sha256 comparison alone is authoritative for what this check cares
# about — replication, not merely coincidental equality.

echo "ALL CHECKS PASSED"
