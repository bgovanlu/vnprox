package host

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultCorosyncConfPath is the standard location of PVE's corosync
// config. Unlike every other read in this package, this one file needs no
// per-node routing: /etc/pve is itself pmxcfs, the cluster's distributed
// filesystem (docs/architecture.md, docs/security.md), so corosync.conf is
// byte-identical on every node's local disk — any node's daemon can read
// its own local copy to learn every node's corosync ring addresses, rather
// than fanning out to a peer.
const DefaultCorosyncConfPath = "/etc/pve/corosync.conf"

// CorosyncNode is one corosync.conf nodelist entry: a cluster member's
// name, corosync node id, and ring addresses (ring0_addr is the primary
// corosync link; ring1_addr etc. are optional redundant rings — PVE's
// "safety interlocks" treat every configured ring as a protected link,
// docs/security.md).
type CorosyncNode struct {
	Name      string
	RingAddrs []string
	NodeID    int
}

// CorosyncConfig is a parsed corosync.conf's nodelist.
type CorosyncConfig struct {
	Nodes []CorosyncNode
}

// NodeByName returns the CorosyncNode entry named name, or ok=false. Safe
// to call on a nil *CorosyncConfig (a node with no configured corosync at
// all, e.g. a not-yet-clustered single node, has no ring addresses to
// protect).
func (c *CorosyncConfig) NodeByName(name string) (CorosyncNode, bool) {
	if c == nil {
		return CorosyncNode{}, false
	}
	for _, n := range c.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return CorosyncNode{}, false
}

// ParseCorosyncConf parses corosync.conf's brace-delimited "block {" /
// "key: value" / "}" syntax, extracting only the nodelist { node { ... } }
// stanzas this package cares about (name, nodeid, ring*_addr). This is a
// deliberately narrow, tolerant parser, not a general corosync.conf
// grammar: every other section (totem, quorum, logging) and every other
// key inside a node stanza is silently skipped, so an unrelated corosync
// feature this package doesn't understand never breaks safety-interlock
// detection (see internal/change.DetectProtected, the caller).
func ParseCorosyncConf(data []byte) (*CorosyncConfig, error) {
	cfg := &CorosyncConfig{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	// path tracks the brace-nesting block-name stack, e.g. ["nodelist", "node"].
	var path []string
	var cur *CorosyncNode

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasSuffix(line, "{"):
			name := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			if name == "node" && len(path) > 0 && path[len(path)-1] == "nodelist" {
				cfg.Nodes = append(cfg.Nodes, CorosyncNode{})
				cur = &cfg.Nodes[len(cfg.Nodes)-1]
			}
			path = append(path, name)

		case line == "}":
			if len(path) > 0 {
				closed := path[len(path)-1]
				path = path[:len(path)-1]
				if closed == "node" {
					cur = nil
				}
			}

		default:
			if cur == nil {
				continue
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), ";"))
			switch {
			case key == "name":
				cur.Name = val
			case key == "nodeid":
				if n, err := strconv.Atoi(val); err == nil {
					cur.NodeID = n
				}
			case strings.HasPrefix(key, "ring") && strings.HasSuffix(key, "_addr"):
				cur.RingAddrs = append(cur.RingAddrs, val)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("host: scanning corosync.conf: %w", err)
	}
	return cfg, nil
}

// ReadCorosyncConf reads and parses corosync.conf from path (typically
// DefaultCorosyncConfPath). The returned error wraps the underlying
// os.PathError (checkable with errors.Is(err, os.ErrNotExist) — wrapping
// via %w preserves Unwrap, but the *os.PathError is no longer the error's
// dynamic type, so os.IsNotExist itself would not recognize it) so callers
// that tolerate corosync not being configured at all (a single, not-yet-
// clustered node has no /etc/pve/corosync.conf) can distinguish "not
// clustered" from a real read failure.
func ReadCorosyncConf(path string) (*CorosyncConfig, error) {
	if path == "" {
		path = DefaultCorosyncConfPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("host: reading corosync.conf %s: %w", path, err)
	}
	return ParseCorosyncConf(data)
}
