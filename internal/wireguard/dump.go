package wireguard

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDump parses the output of `wg show <ifName> dump` into an
// ObservedTunnel — the live, authoritative on-node WireGuard state the
// monitoring findings read, never persisted as truth (docs/architecture.md
// §7's storage rule; T-1401's "computed fresh from a live wg show <if> dump-
// equivalent poll" deliverable).
//
// The dump format (one tab-separated record per line): the first line is the
// interface itself — `<private-key> <public-key> <listen-port> <fwmark>` —
// and every subsequent line is a peer — `<public-key> <preshared-key>
// <endpoint> <allowed-ips> <latest-handshake> <rx> <tx> <keepalive>`.
//
// The interface line's private-key column is deliberately DISCARDED here and
// never stored on ObservedTunnel: this parser is used on a live poll, and a
// tunnel's private key must never leave the owning node's sealed store row
// (docs/security.md's WireGuard credential-storage note). ConfiguredEnd /
// ConfiguredExtern are left zero — they are intent, filled in by the caller
// merging the app-store Peer records, not read from the kernel dump.
func ParseDump(node, ifName, raw string) (ObservedTunnel, error) {
	t := ObservedTunnel{Node: node, IfName: ifName}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if i == 0 {
			// Interface line: fields[0] is the private key — discarded, never
			// captured. We take only the public key and listen port.
			if len(fields) < 3 {
				return ObservedTunnel{}, fmt.Errorf("wireguard: malformed interface dump line %q", line)
			}
			t.PublicKey = fields[1]
			if port, err := strconv.Atoi(fields[2]); err == nil {
				t.ListenPort = port
			}
			continue
		}
		peer, err := parsePeerLine(fields)
		if err != nil {
			return ObservedTunnel{}, err
		}
		t.Peers = append(t.Peers, peer)
	}
	return t, nil
}

func parsePeerLine(fields []string) (ObservedPeer, error) {
	if len(fields) < 8 {
		return ObservedPeer{}, fmt.Errorf("wireguard: malformed peer dump line with %d fields", len(fields))
	}
	p := ObservedPeer{PublicKey: fields[0]}
	if ep := fields[2]; ep != "(none)" {
		p.Endpoint = ep
	}
	if ips := fields[3]; ips != "(none)" && ips != "" {
		p.AllowedIPs = strings.Split(ips, ",")
	}
	if hs, err := strconv.ParseInt(fields[4], 10, 64); err == nil && hs > 0 {
		p.LastHandshake = time.Unix(hs, 0)
	}
	if rx, err := strconv.ParseInt(fields[5], 10, 64); err == nil {
		p.RxBytes = rx
	}
	if tx, err := strconv.ParseInt(fields[6], 10, 64); err == nil {
		p.TxBytes = tx
	}
	if ka := fields[7]; ka != "off" {
		if secs, err := strconv.Atoi(ka); err == nil {
			p.PersistentKeep = secs
		}
	}
	return p, nil
}
