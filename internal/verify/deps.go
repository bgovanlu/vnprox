// SPDX-License-Identifier: Apache-2.0

package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Deps is every interaction a check has with the outside world.
//
// Nothing in checks_*.go calls the network, the filesystem or a subprocess
// directly. That is not style: it is the mechanism behind AC2. A check that
// dials PVE itself can only be tested against a real cluster, which is the
// exact thing this package does not have — so it would ship with no failing
// fixture, which is to say untested.
//
// Every probe is a pointer-or-interface that may be nil. A nil probe makes
// the checks that need it skip with a reason naming what is missing; it never
// makes them fail, because "we could not look" is not "it is broken".
type Deps struct {
	// Now is the clock, injected so a report's timestamps are deterministic
	// under test.
	Now func() time.Time
	// Wait pauses between polls in the checks that watch for a state
	// transition (an unattended rollback firing, a standby promoting).
	//
	// It is injected for the same reason Now is: a check whose failing
	// fixture takes a real two minutes to prove is a check whose failing
	// fixture nobody runs. Run fills in a real, context-aware sleep.
	Wait func(ctx context.Context, d time.Duration) error
	// Cluster reads PVE. Nil when no PVE endpoint could be built.
	Cluster ClusterProbe
	// Daemon reads the local vnprox daemon's /api/v1 surface. Nil when no
	// daemon URL/token was available.
	Daemon DaemonProbe
	// Root reads the daemon's ROOT surface — the SPA shell, its manifest,
	// its service worker, and the security headers on all three. Nil only
	// when no daemon URL could be determined at all.
	//
	// It is separate from Daemon on purpose, and the reason is a defect this
	// separation exists to prevent recurring. `pwa.servable` originally read
	// its RootProbe off Daemon, so it skipped whenever no bearer token was
	// configured — which is the default state of a freshly installed node.
	// The check built to catch the v4.0.0 CSP defect therefore could not run
	// on precisely the deployment that had it (found 2026-08-16, deploying
	// Phase 29 to pvecube). None of the three fetches is authenticated, so
	// requiring a token was never anything but an accident of wiring.
	Root RootProbe
	// Host reads local (and, cluster-aware, peer) host state. Nil when the
	// suite is running somewhere that cannot reach a node shell.
	Host HostProbe
	// Mutator is the write half of the daemon API. The CLI leaves it nil
	// unless --i-understand was passed, so a hardware-suite check has no code
	// path by which it could change anything even if it tried. See
	// MutatorProbe in registry.go.
	Mutator MutatorProbe
	// Nodes is the cluster membership discovered before the run, so a
	// multinode check can state the count it saw rather than re-deriving it.
	Nodes []Node
	// Endpoint is the PVE API the run was pointed at, and whether it was
	// identified as a mock.
	//
	// It is carried into the report rather than only checked at the door: a
	// report is read long after the run that produced it, and "this was
	// produced against a replay server" has to travel with the document. A
	// guard that only refuses at the door leaves nothing behind on the runs
	// it let through.
	Endpoint Endpoint
	// Consent records what the operator explicitly agreed to.
	Consent Consent
}

// Endpoint is the PVE API a run was pointed at.
type Endpoint struct {
	URL        string
	MockReason string
	Mock       bool
}

// Consent is the set of "yes, I know" flags the CLI collected. Nothing here
// defaults to true.
type Consent struct {
	// AllowMock permits a run against an endpoint identified as a mock.
	AllowMock bool
	// Destructive permits the destructive suite (--i-understand).
	Destructive bool
}

// Node is one cluster member, reduced to what the checks care about.
type Node struct {
	Name    string
	Address string
	Online  bool
	Local   bool
}

// Iface is one interface from PVE's own view of a node, reduced to the fields
// the checks assert on. It deliberately mirrors internal/pve.NetworkInterface
// rather than re-deriving anything: PVEAdapter does the conversion in one
// place so a wire-shape surprise (of which T-2502 exists to find more) lands
// in one file.
type Iface struct {
	Name      string
	Type      string
	Method    string
	Address   string
	BondMode  string
	Slaves    string
	Comments  string
	MTU       int
	VlanAware bool
	Autostart bool
}

// ClusterProbe is the PVE-facing half of the world.
type ClusterProbe interface {
	// Nodes returns cluster membership from GET /cluster/status.
	Nodes(ctx context.Context) ([]Node, error)
	// PVEVersion returns the cluster's reported PVE release string.
	PVEVersion(ctx context.Context) (string, error)
	// Interfaces returns one node's interfaces from GET /nodes/{node}/network.
	Interfaces(ctx context.Context, node string) ([]Iface, error)
}

// DaemonProbe reads the vnprox daemon's documented HTTP surface.
//
// It is deliberately one untyped Get rather than twenty typed methods. Each
// check parses the response it asked for, with a struct spelled from
// docs/api.md, and that is the point: the check is asserting the *real*
// response shape. A shared typed client would let a check pass because a
// convenient intermediate struct had the field the check wanted, which is
// precisely the defect class T-2108 found four instances of.
type DaemonProbe interface {
	// Get performs an authenticated GET against the daemon's /api/v1 base.
	// path starts with "/". A transport failure returns err; an HTTP error
	// status returns the status and the body, because a 403's body is
	// evidence too.
	Get(ctx context.Context, path string) (status int, body []byte, err error)
}

// HostProbe reads host state on a named node.
//
// node is the cluster node to read from; "" means the local node. It is a
// parameter rather than an assumption because CLAUDE.md requires every reader
// of node state to work when that node is a peer — a host probe that only
// ever reads localhost would make every multinode check a lie on the node it
// did not run on.
type HostProbe interface {
	// Run executes a read-only command and returns its combined output.
	Run(ctx context.Context, node, name string, args ...string) (string, error)
	// ReadFile reads a file.
	ReadFile(ctx context.Context, node, path string) ([]byte, error)
}

// --- helpers the checks share ----------------------------------------------

// daemonJSON performs a daemon GET, records the raw body as evidence, and
// decodes it into out.
//
// The evidence is the raw body, not the decoded value: a decoded struct has
// already dropped every field the struct does not know about, and the fields
// a struct does not know about are where the surprises live.
func daemonJSON(ctx context.Context, d Deps, path string, out any) (Evidence, error) {
	if d.Daemon == nil {
		return Evidence{}, errNoDaemon
	}
	status, body, err := d.Daemon.Get(ctx, path)
	if err != nil {
		return Evidence{}, fmt.Errorf("GET %s: %w", path, err)
	}
	ev := NewEvidence(SourceDaemonAPI, fmt.Sprintf("GET %s -> %d", path, status), string(body))
	if status != http.StatusOK {
		return ev, fmt.Errorf("GET %s returned %d", path, status)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return ev, fmt.Errorf("decoding GET %s: %w", path, err)
		}
	}
	return ev, nil
}

// errNoDaemon is the sentinel a check turns into a skip naming the daemon.
var errNoDaemon = fmt.Errorf("no vnprox daemon client is configured")

// skipNoDaemon is the one spelling of "this check needs the running daemon".
//
// It states what was not checked and what would make it checkable, and it
// does NOT assert a cause. doctor learned that distinction on real hardware
// (docs/status-matrix.md §5.10): a skip that confidently diagnosed "no PVE
// credentials configured" on a fully-configured node was a claim about
// something it had never looked at.
func skipNoDaemon(what string) Outcome {
	return Skip(fmt.Sprintf("not checked: %s needs the running vnprox daemon's API. Re-run with --url and --token (or VNPROX_TOKEN) pointing at this node's daemon", what))
}

// skipNoCluster is the same for the PVE endpoint.
func skipNoCluster(what string) Outcome {
	return Skip(fmt.Sprintf("not checked: %s needs a PVE API client. Re-run with --pve-url and a readable PVE token file", what))
}

// skipNoHost is the same for host reads.
func skipNoHost(what string) Outcome {
	return Skip(fmt.Sprintf("not checked: %s needs to run commands on the node itself. Run vnproxctl verify on the PVE node rather than remotely", what))
}

// onlineNodes filters to the cluster members PVE reports as online, which is
// the count every multinode precondition is actually about — a second node
// that is powered off cannot demonstrate distributed anything.
func onlineNodes(nodes []Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Online {
			out = append(out, n)
		}
	}
	return out
}

// nodeNames is the display form used in skip reasons and in the report's
// Environment.
func nodeNames(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

// localNode picks the node to read host state from: the one PVE marks local,
// falling back to the first online member.
func localNode(nodes []Node) string {
	for _, n := range nodes {
		if n.Local {
			return n.Name
		}
	}
	for _, n := range nodes {
		if n.Online {
			return n.Name
		}
	}
	return ""
}

// firstLine trims a command's output to its first line, for one-line details.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
