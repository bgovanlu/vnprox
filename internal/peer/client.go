// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// DefaultPort is the peer API port when a peer's discovered address carries
// no port of its own (docs/architecture.md §5: "peers are reached at
// https://<node-ip>:8007/api/peer/..."; docs/deployment.md notes all nodes
// in a cluster share one port, installer-enforced).
const DefaultPort = 8007

// Client-side tunables (docs/development.md doesn't pin numeric values for
// these; chosen conservatively so a dead peer is detected well within a
// human-perceived "this is slow" threshold without flapping on a single
// slow-but-alive response).
const (
	DefaultRequestTimeout          = 5 * time.Second
	DefaultBreakerFailureThreshold = 3
	DefaultBreakerResetTimeout     = 15 * time.Second
)

// Peer identifies one other cluster member's peer API endpoint.
type Peer struct {
	Node string // PVE node name
	Addr string // host:port, e.g. "10.0.0.2:8007"
}

// ClusterStatusSource is the discovery data source: PVE's own
// GET /cluster/status (docs/architecture.md §5: "Peer discovery: node list
// from the PVE API (/cluster/status)"). *pve.Client already satisfies this
// exactly (ClusterStatus's existing signature), so no adapter is needed in
// production wiring.
type ClusterStatusSource interface {
	ClusterStatus(ctx context.Context) ([]pve.ClusterStatusEntry, error)
}

// ClientOptions configures a Client.
type ClientOptions struct {
	ClusterStatus ClusterStatusSource
	Secrets       *SecretStore
	// HTTPClient, when set, is used verbatim and Trust is ignored — the
	// caller has taken full responsibility for this client's TLS trust
	// decision. Production wiring must not use it (Client.TrustReport reports
	// such a client as TrustExternal/unpinned, which raises a
	// peer_trust_degraded finding); it exists for tests that point a peer
	// client at an httptest server.
	HTTPClient *http.Client
	// Trust pins peer TLS to the cluster's own CA (T-1906). Nil means the
	// pinned default (DefaultClusterCAPath) — never the system trust store.
	Trust  *Trust
	Logger *slog.Logger
	Now    func() time.Time
	// Metrics (T-1903) records every peer RPC's outcome and duration
	// (vnprox_peer_calls_total / vnprox_peer_call_duration_seconds) —
	// *metrics.Registry satisfies this. Nil (the default) disables
	// recording, the same nil-safe-optional-dependency convention every
	// other ClientOptions field here follows.
	Metrics                 MetricsRecorder
	Scheme                  string
	Port                    int
	RequestTimeout          time.Duration
	BreakerFailureThreshold int
	BreakerResetTimeout     time.Duration
}

// MetricsRecorder is T-1903's self-observability seam for peer RPCs.
// outcome is PeerTrustState's own closed vocabulary ("ok"|"unreachable"|
// "untrusted" — see do's doc comment).
type MetricsRecorder interface {
	ObservePeerCall(node, endpoint, outcome string, dur time.Duration)
}

// Client is the peer API client: discovery, HMAC signing, cluster-CA-pinned
// TLS, and a per-peer circuit breaker. Safe for concurrent use.
type Client struct {
	httpClient *http.Client
	trust      *Trust
	statuses   *peerStatusStore
	breakers   map[string]*circuitBreaker

	// neighborsCache/dhcpLeasesCache (T-3712) coalesce Neighbors/DHCPLeases
	// reads that share a (peer, node) key within PeerReadCoalesceTTL, so
	// this Client's several independent callers — internal/neighbor and
	// internal/ipam both read Neighbors, internal/ipam reads DHCPLeases via
	// internal/dhcp for both its enrichment merge and its GET /sdn/dhcp
	// view — produce one upstream peer request per key per TTL window
	// instead of one each. See resultCache's doc comment for why this is a
	// short-TTL cache rather than a pure in-flight singleflight.
	neighborsCache  *resultCache[[]host.Neighbor]
	dhcpLeasesCache *resultCache[[]byte]

	// opts and mu sit last: govet's fieldalignment counts bytes up to the
	// final pointer, so pointer-free tails (sync.Mutex) belong after every
	// pointer-bearing field.
	opts ClientOptions
	mu   sync.Mutex
}

// NewClient builds a Client. opts.Secrets must be non-nil.
func NewClient(opts ClientOptions) *Client {
	if opts.Secrets == nil {
		panic("peer: NewClient requires a non-nil SecretStore")
	}
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Scheme == "" {
		opts.Scheme = "https"
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = DefaultRequestTimeout
	}
	if opts.BreakerFailureThreshold <= 0 {
		opts.BreakerFailureThreshold = DefaultBreakerFailureThreshold
	}
	if opts.BreakerResetTimeout <= 0 {
		opts.BreakerResetTimeout = DefaultBreakerResetTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	// TLS trust (T-1906). A caller-supplied HTTPClient wins, but *nothing*
	// else falls back to net/http's default system trust store: an
	// unconfigured Client pins DefaultClusterCAPath, exactly like a configured
	// production daemon. This is the one line that decides whether a
	// certificate from any publicly-trusted CA can impersonate a peer daemon.
	trust := opts.Trust
	hc := opts.HTTPClient
	if hc == nil {
		if trust == nil {
			trust = newPinnedTrust(opts.Logger)
			opts.Trust = trust
		}
		hc = &http.Client{Timeout: opts.RequestTimeout, Transport: trust}
	} else {
		trust = nil
	}
	return &Client{
		opts:            opts,
		httpClient:      hc,
		trust:           trust,
		statuses:        newPeerStatusStore(),
		breakers:        make(map[string]*circuitBreaker),
		neighborsCache:  newResultCache[[]host.Neighbor](PeerReadCoalesceTTL, opts.Now),
		dhcpLeasesCache: newResultCache[[]byte](PeerReadCoalesceTTL, opts.Now),
	}
}

// peerReadKey builds a resultCache key that scopes coalescing to one peer's
// one target node, so a read for node A never gets coalesced with a read
// for node B even when both are routed through the same peer.
func peerReadKey(p Peer, node string) string {
	return p.Node + "\x00" + node
}

// Peers returns the current cluster member list (excluding this node
// itself), derived from PVE's own cluster status. Returns an empty, non-nil
// slice (not an error) when opts.ClusterStatus is nil or PVE reports no
// peer nodes — the documented single-node "zero peers" case.
func (c *Client) Peers(ctx context.Context) ([]Peer, error) {
	if c.opts.ClusterStatus == nil {
		return nil, nil
	}
	entries, err := c.opts.ClusterStatus.ClusterStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("peer: discovering cluster peers: %w", err)
	}
	peers := make([]Peer, 0, len(entries))
	for _, e := range entries {
		if e.Type != "node" || e.Local || e.IP == "" {
			continue
		}
		peers = append(peers, Peer{Node: e.Name, Addr: net.JoinHostPort(e.IP, strconv.Itoa(c.opts.Port))})
	}
	return peers, nil
}

func (c *Client) breakerFor(node string) *circuitBreaker {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.breakers[node]
	if !ok {
		b = newCircuitBreaker(c.opts.BreakerFailureThreshold, c.opts.BreakerResetTimeout, c.opts.Now)
		c.breakers[node] = b
	}
	return b
}

// do sends a signed request to peer and returns the raw response for the
// caller to decode. The circuit breaker fast-fails (without attempting the
// network) when peer's breaker is open; a transport-level failure records a
// breaker failure, while any received HTTP response (whatever its status)
// records a breaker success — the breaker tracks reachability, not
// application-level correctness.
//
// Transport failures are classified two ways (T-1906 AC5). A certificate that
// does not verify against the pinned cluster CA yields an error wrapping
// ErrPeerUntrusted (and, so existing degradation paths are unchanged, also
// ErrPeerUnreachable — see that sentinel's doc comment); anything else
// (refused, timed out, no route) yields ErrPeerUnreachable alone. The
// distinction survives the circuit breaker: once a peer has failed
// verification, the breaker's own fast-fail keeps reporting *untrusted*
// instead of flattening it back to "unreachable", so an impersonation attempt
// does not disappear behind an open breaker after three attempts.
func (c *Client) do(ctx context.Context, p Peer, method, path string, body []byte) (*http.Response, error) {
	// T-1903: endpoint is path with its query string (if any) stripped —
	// every call site above builds path from a literal template
	// ("/api/peer/host/stats", ...) with any dynamic value (node name,
	// changeset id, session id, ...) always appended as a query parameter,
	// never spliced into the path itself, so this is already a small,
	// compile-time-bounded vocabulary (see docs/features/monitoring.md
	// §9's cardinality note for this series) without needing every one of
	// this file's ~30 call sites to pass their own label explicitly.
	endpoint, _, _ := strings.Cut(path, "?")
	start := time.Now()

	breaker := c.breakerFor(p.Node)
	if !breaker.allow() {
		if last, ok := c.statuses.get(p.Node); ok && last.State == PeerTrustUntrusted {
			c.recordCall(p.Node, endpoint, string(PeerTrustUntrusted), start)
			return nil, fmt.Errorf("peer: %s: %w (%w): circuit open after a certificate verification failure: %s", p.Node, ErrPeerUntrusted, ErrPeerUnreachable, last.Error)
		}
		c.recordCall(p.Node, endpoint, string(PeerTrustUnreachable), start)
		return nil, fmt.Errorf("peer: %s: %w: circuit open", p.Node, ErrPeerUnreachable)
	}

	target := fmt.Sprintf("%s://%s%s", c.opts.Scheme, p.Addr, path)
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	// The request context is the caller's ctx, unwrapped: c.httpClient's
	// Timeout (set at construction, see NewClient) already bounds the whole
	// call including the body read, so a second, request-scoped
	// context.WithTimeout here would only buy an extra way to cancel the
	// response body out from under decodeInto before it runs -- which is
	// exactly what used to happen, since do() returns (and any deferred
	// cancel of a request-scoped context fires) before the caller has read
	// the body at all. See planning/reports/audit-2026-08-21-peer-body-cancel.md.
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("peer: building request to %s: %w", p.Node, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	secret := c.opts.Secrets.Current()
	ts := c.opts.Now().Unix()
	// T-3703: a fresh nonce per request is what lets the verifier tell
	// "the same request, legitimately sent twice inside one second" apart
	// from an actual replay — see sign.go's canonicalRequest doc comment.
	// generateNonce only fails if crypto/rand itself is broken (an
	// unreadable entropy source), which is not a condition any retry or
	// fallback here could sensibly recover from, so it's treated the same
	// as any other request-construction failure.
	nonce, err := generateNonce()
	if err != nil {
		return nil, fmt.Errorf("peer: %s: %w", p.Node, err)
	}
	requestURI := req.URL.RequestURI()
	// HeaderSignature always carries the plain pre-T-3703, four-field
	// signature (nonce == "") — never the nonce-bound one — so that
	// pve001, an already-peered node running an older build this project
	// has no credentials to upgrade, keeps verifying every request from
	// this daemon exactly as it does today: it only ever reads this one
	// header. HeaderNonce/HeaderNonceSignature are additive information a
	// build that predates T-3703 simply never looks at. See
	// authMiddleware's doc comment for the verifier side of this.
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, sign(secret, method, requestURI, body, ts, ""))
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderNonceSignature, sign(secret, method, requestURI, body, ts, nonce))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		breaker.recordFailure()
		if isTrustFailure(err) {
			// %w on the transport error too, so a caller can reach the
			// underlying cause (ErrTrustAnchorUnavailable, an
			// *x509.UnknownAuthorityError) through errors.Is/As rather than
			// having to substring-match a message.
			untrusted := fmt.Errorf("peer: %s: %w: certificate verification failed, treating the peer as unreachable (%w): %w", p.Node, ErrPeerUntrusted, ErrPeerUnreachable, err)
			c.statuses.record(p, PeerTrustUntrusted, c.opts.Now(), untrusted)
			c.opts.Logger.Error("peer: refusing a peer whose TLS certificate did not verify against the pinned cluster CA",
				"node", p.Node, "addr", p.Addr, "error", err)
			c.recordCall(p.Node, endpoint, string(PeerTrustUntrusted), start)
			return nil, untrusted
		}
		unreachable := fmt.Errorf("peer: %s: %w: %w", p.Node, ErrPeerUnreachable, err)
		c.statuses.record(p, PeerTrustUnreachable, c.opts.Now(), unreachable)
		c.recordCall(p.Node, endpoint, string(PeerTrustUnreachable), start)
		return nil, unreachable
	}
	breaker.recordSuccess()
	c.statuses.record(p, PeerTrustOK, c.opts.Now(), nil)
	c.recordCall(p.Node, endpoint, string(PeerTrustOK), start)
	return resp, nil
}

// recordCall is a nil-safe wrapper around opts.Metrics.ObservePeerCall.
func (c *Client) recordCall(node, endpoint, outcome string, start time.Time) {
	if c.opts.Metrics != nil {
		c.opts.Metrics.ObservePeerCall(node, endpoint, outcome, time.Since(start))
	}
}

// decodeInto reads and closes resp.Body, decoding it into out on a 2xx
// status (out may be nil to just drain/close the body) or returning a
// *ResponseError built from the peer's error envelope otherwise.
func decodeInto(resp *http.Response, out any) error {
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		var env errorEnvelope
		if err := json.Unmarshal(data, &env); err == nil && env.Error.Code != "" {
			return &ResponseError{StatusCode: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
		}
		return &ResponseError{StatusCode: resp.StatusCode, Code: "peer_error", Message: string(data)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Interfaces fetches node's /etc/network/interfaces content from peer p
// (or interfaces.new when includePending).
func (c *Client) Interfaces(ctx context.Context, p Peer, node string, includePending bool) (string, error) {
	path := fmt.Sprintf("/api/peer/host/interfaces?node=%s&pending=%s", url.QueryEscape(node), strconv.FormatBool(includePending))
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var out interfacesResponse
	if err := decodeInto(resp, &out); err != nil {
		return "", err
	}
	return out.Content, nil
}

// LLDP fetches node's raw LLDP neighbor JSON from peer p.
func (c *Client) LLDP(ctx context.Context, p Peer, node string) ([]byte, error) {
	path := "/api/peer/host/lldp?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeInto(resp, nil)
	}
	return io.ReadAll(resp.Body)
}

// Stats fetches node's interface counters from peer p.
func (c *Client) Stats(ctx context.Context, p Peer, node string) (map[string]host.IfaceStats, error) {
	path := "/api/peer/host/stats?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out statsResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Stats, nil
}

// Services fetches node's systemd unit status (T-602's
// host.WatchedServices) from peer p.
func (c *Client) Services(ctx context.Context, p Peer, node string) (map[string]bool, error) {
	path := "/api/peer/host/services?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out servicesResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

// StageInterfaces asks peer p to stage content as node's interfaces.new.
func (c *Client) StageInterfaces(ctx context.Context, p Peer, node, content string) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(stageRequest{Node: node, Content: content,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding stage-interfaces request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/stage-interfaces", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// Ifreload asks peer p to apply node's staged interfaces.new and reload.
func (c *Client) Ifreload(ctx context.Context, p Peer, node string) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(nodeRequest{Node: node,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding ifreload request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/ifreload", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// Restore asks peer p to directly write content as node's committed
// interfaces file and reload (the rollback path).
func (c *Client) Restore(ctx context.Context, p Peer, node, content string) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(stageRequest{Node: node, Content: content,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding restore request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/restore", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// Links fetches node's netlink-equivalent link state from peer p (T-303:
// the remote-node counterpart of a local host.Reader.Links call, so
// internal/collect's host poller can treat "poll this node" uniformly
// regardless of whether node is local or reached through a peer).
func (c *Client) Links(ctx context.Context, p Peer, node string) ([]host.LinkState, error) {
	path := "/api/peer/host/links?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out linksResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Links, nil
}

// FDB fetches node's flattened, bridge-tagged forwarding-database tables
// from peer p (T-306's MAC/FDB browser).
func (c *Client) FDB(ctx context.Context, p Peer, node string) ([]host.FDBRow, error) {
	path := "/api/peer/host/fdb?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out fdbResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

// Neighbors fetches node's resolved ARP/IPv6-neighbor table from peer p
// (T-805): the remote-node counterpart of a local host.Reader.Neighbors
// call, so internal/ipam.NeighborSource's fan-out can treat "read this
// node's neighbor table" uniformly regardless of whether node is local or
// reached through a peer — the same shape Links/FDB above already
// establish.
//
// T-3712: routed through neighborsCache, so internal/neighbor's own
// fan-out and internal/ipam's enrichment merge (and, before them,
// cmd/vnproxd's rogueScanAdapter — all three read the *same*
// neighbor.Service instance, see cmd/vnproxd/server.go) asking for the
// same (p, node) within PeerReadCoalesceTTL of each other produce exactly
// one signed request to peer, not one per caller. ctx is intentionally not
// part of the cache key or wired into the shared call: a coalesced read
// that outlives the first caller's ctx must still complete for the callers
// still waiting on it, and do()'s own fn closure carries whichever ctx
// happened to start the call — the same trade-off any singleflight-style
// coalescing makes.
func (c *Client) Neighbors(ctx context.Context, p Peer, node string) ([]host.Neighbor, error) {
	return c.neighborsCache.do(peerReadKey(p, node), func() ([]host.Neighbor, error) {
		path := "/api/peer/host/neighbors?node=" + url.QueryEscape(node)
		resp, err := c.do(ctx, p, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var out neighborsResponse
		if err := decodeInto(resp, &out); err != nil {
			return nil, err
		}
		return out.Neighbors, nil
	})
}

// ContainerInterior fetches an lxc guest's raw host-side
// network-namespace read set from peer p (T-1304), the remote-node
// counterpart of a local host.Reader.ContainerInterior call.
func (c *Client) ContainerInterior(ctx context.Context, p Peer, node string, vmid int) (host.ContainerInteriorRaw, error) {
	path := fmt.Sprintf("/api/peer/host/container-interior?node=%s&vmid=%d", url.QueryEscape(node), vmid)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return host.ContainerInteriorRaw{}, err
	}
	var out containerInteriorResponse
	if err := decodeInto(resp, &out); err != nil {
		return host.ContainerInteriorRaw{}, err
	}
	return host.ContainerInteriorRaw{
		AddrJSON: []byte(out.AddrJSON), RouteJSON: []byte(out.RouteJSON),
		ResolvConf: []byte(out.ResolvConf), Sockets: []byte(out.Sockets),
	}, nil
}

// ContainerPing fetches whether targetIP answered a single best-effort
// ping issued from inside vmid's network namespace on node, via peer p
// (T-1304), the remote-node counterpart of a local host.Reader.
// ContainerPing call.
func (c *Client) ContainerPing(ctx context.Context, p Peer, node string, vmid int, targetIP string) (bool, error) {
	path := fmt.Sprintf("/api/peer/host/container-ping?node=%s&vmid=%d&ip=%s", url.QueryEscape(node), vmid, url.QueryEscape(targetIP))
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	var out containerPingResponse
	if err := decodeInto(resp, &out); err != nil {
		return false, err
	}
	return out.Reachable, nil
}

// Conntrack fetches node's live conntrack/NAT table from peer p (T-1305):
// the remote-node counterpart of a local host.Reader.Conntrack call, used
// by GET /conntrack's cluster fan-out.
func (c *Client) Conntrack(ctx context.Context, p Peer, node string) ([]host.ConntrackEntry, error) {
	path := "/api/peer/host/conntrack?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out conntrackResponse
	if decErr := decodeInto(resp, &out); decErr != nil {
		return nil, mapConntrackUnavailable(decErr)
	}
	return out.Entries, nil
}

// mapConntrackUnavailable rewraps a *ResponseError carrying the
// conntrack_unavailable code (T-3711) so callers can
// errors.Is(err, host.ErrConntrackUnavailable) across the wire — the same
// convention mapTimerNotFound already establishes for this client.
func mapConntrackUnavailable(err error) error {
	var respErr *ResponseError
	if errors.As(err, &respErr) && respErr.Code == errCodeConntrackUnavailable {
		return fmt.Errorf("peer: %w: %s", host.ErrConntrackUnavailable, respErr.Message)
	}
	return err
}

// IPv6RA fetches node's bounded, host-local IPv6 RA/DHCPv6 observation from
// peer p (T-1404): the remote-node counterpart of a local
// host.Reader.IPv6RA call, used by GET /ipv6/segments' cluster fan-out.
func (c *Client) IPv6RA(ctx context.Context, p Peer, node string) ([]host.IPv6RAObservation, error) {
	path := "/api/peer/host/ipv6-ra?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ipv6RAResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// FRRBGPSummary fetches node's raw `vtysh -c "show bgp summary json"`
// output from peer p (T-404). available is false (raw is nil) when node
// runs no FRR at all — the peer-routed counterpart of
// errors.Is(err, host.ErrFRRUnavailable) for a local read, translated back
// out of the wire's {available,content} envelope (see frrResponse's doc
// comment) rather than as an error, so callers handle "local" and "peer"
// nodes identically by checking the bool.
func (c *Client) FRRBGPSummary(ctx context.Context, p Peer, node string) (available bool, raw []byte, err error) {
	return c.frrRequest(ctx, p, "/api/peer/host/frr/bgp-summary", node)
}

// FRREVPNVNI fetches node's raw `vtysh -c "show evpn vni json"` output
// from peer p (T-404). Same available/raw convention as FRRBGPSummary.
func (c *Client) FRREVPNVNI(ctx context.Context, p Peer, node string) (available bool, raw []byte, err error) {
	return c.frrRequest(ctx, p, "/api/peer/host/frr/evpn-vni", node)
}

func (c *Client) frrRequest(ctx context.Context, p Peer, path, node string) (available bool, raw []byte, err error) {
	resp, err := c.do(ctx, p, http.MethodGet, path+"?node="+url.QueryEscape(node), nil)
	if err != nil {
		return false, nil, err
	}
	var out frrResponse
	if err := decodeInto(resp, &out); err != nil {
		return false, nil, err
	}
	if !out.Available {
		return false, nil, nil
	}
	return true, []byte(out.Content), nil
}

// DHCPLeases fetches node's raw dnsmasq DHCP lease-file content from peer
// p (T-406). Unlike FRRBGPSummary/FRREVPNVNI there is no available/raw
// split — an empty result is itself a clean "no leases" answer, not a
// distinct absent condition (see HostReader.DHCPLeases' doc comment).
//
// T-3712: routed through dhcpLeasesCache, the same (peer, node)-keyed
// short-TTL coalescing Neighbors above uses — internal/ipam calls
// internal/dhcp's fan-out from both its enrichment merge and its DHCP view
// (internal/ipam/dhcp.go), so the same duplicate-poll shape applies here
// too (T-3712's card: "the 34 dhcp-leases rejections suggest it does, at a
// lower rate").
func (c *Client) DHCPLeases(ctx context.Context, p Peer, node string) ([]byte, error) {
	return c.dhcpLeasesCache.do(peerReadKey(p, node), func() ([]byte, error) {
		path := "/api/peer/host/dhcp-leases?node=" + url.QueryEscape(node)
		resp, err := c.do(ctx, p, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var out dhcpLeasesResponse
		if err := decodeInto(resp, &out); err != nil {
			return nil, err
		}
		return []byte(out.Content), nil
	})
}

// MDB fetches node's raw `bridge -d -j mdb show` output from peer p
// (T-3902's multicast/MDB browser) — the remote-node counterpart of a
// local host.Reader.MDB call, GET /mdb's cluster fan-out dependency. Like
// DHCPLeases, there is no available/raw split: an empty result is itself a
// clean "no MDB entries" answer, not a distinct absent condition (see
// mdbResponse's doc comment).
func (c *Client) MDB(ctx context.Context, p Peer, node string) ([]byte, error) {
	path := "/api/peer/host/mdb?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out mdbResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return []byte(out.Content), nil
}

// NftRuleset fetches node's raw `nft -j list ruleset` output from peer p
// (T-3904's compiled-ruleset inspector) — the remote-node counterpart of
// a local host.Reader.NftRuleset call, GET /firewall/compiled's cross-node
// routing dependency. Like MDB, there is no available/raw split: an empty
// result is itself a clean (if ambiguous, per HostReader.NftRuleset's doc
// comment) answer, not a distinct absent condition.
func (c *Client) NftRuleset(ctx context.Context, p Peer, node string) ([]byte, error) {
	path := "/api/peer/host/nftables?node=" + url.QueryEscape(node)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out nftRulesetResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return []byte(out.Content), nil
}

// RouteTableV4/RouteTableV6 fetch node's raw kernel FIB (`ip -j route
// show table all` / its `-6` counterpart) from peer p (T-3903's route
// explorer) — the remote-node counterpart of a local internal/route.
// Fetcher.RouteTableV4/V6 call, satisfying internal/route.PeerSource
// directly. Like MDB/DHCPLeases, no available/raw split: `ip` is a hard
// OS dependency, so a route table (even an apparently sparse one) is
// always a clean answer, never a distinct absent condition.
func (c *Client) RouteTableV4(ctx context.Context, p Peer, node string) ([]byte, error) {
	return c.routeContentRequest(ctx, p, "/api/peer/host/route/fib-v4", node)
}

func (c *Client) RouteTableV6(ctx context.Context, p Peer, node string) ([]byte, error) {
	return c.routeContentRequest(ctx, p, "/api/peer/host/route/fib-v6", node)
}

// RouteRulesV4/RouteRulesV6 fetch node's raw policy-routing rules
// (`ip -j rule show` / its `-6` counterpart) from peer p.
func (c *Client) RouteRulesV4(ctx context.Context, p Peer, node string) ([]byte, error) {
	return c.routeContentRequest(ctx, p, "/api/peer/host/route/rules-v4", node)
}

func (c *Client) RouteRulesV6(ctx context.Context, p Peer, node string) ([]byte, error) {
	return c.routeContentRequest(ctx, p, "/api/peer/host/route/rules-v6", node)
}

func (c *Client) routeContentRequest(ctx context.Context, p Peer, path, node string) ([]byte, error) {
	resp, err := c.do(ctx, p, http.MethodGet, path+"?node="+url.QueryEscape(node), nil)
	if err != nil {
		return nil, err
	}
	var out routeContentResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return []byte(out.Content), nil
}

// FRRRIBV4/FRRRIBV6 fetch node's raw FRR RIB JSON (`vtysh -c "show ip
// route json"` / "show ipv6 route json") from peer p (T-3903), satisfying
// internal/route.PeerSource directly. Same available/raw convention as
// FRRBGPSummary/FRREVPNVNI above: available is false (raw is nil) when
// node runs no FRR at all.
func (c *Client) FRRRIBV4(ctx context.Context, p Peer, node string) (available bool, raw []byte, err error) {
	return c.frrRequest(ctx, p, "/api/peer/host/route/frr-rib-v4", node)
}

func (c *Client) FRRRIBV6(ctx context.Context, p Peer, node string) (available bool, raw []byte, err error) {
	return c.frrRequest(ctx, p, "/api/peer/host/route/frr-rib-v6", node)
}

// FirewallLog fetches new pve-firewall log lines for node from peer p,
// either from the start (cursor == "") or appended since cursor (T-505:
// internal/fwlog.Service.Tick calls this once per known peer, per poll
// tick, exactly the way it calls its local Source.Tail — see that
// package's PeerSource interface, which this method satisfies directly).
func (c *Client) FirewallLog(ctx context.Context, p Peer, node, cursor string, maxLines int) ([]string, string, error) {
	q := url.Values{}
	q.Set("node", node)
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if maxLines > 0 {
		q.Set("maxLines", strconv.Itoa(maxLines))
	}
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/firewall/log?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out firewallLogResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Lines, out.NextCursor, nil
}

// Audit fetches one page of peer p's own local audit log (T-303: the
// per-peer fetch internal/api's cluster audit merge issues once per known
// peer, per page, with the same filter/cursor/limit it uses locally).
func (c *Client) Audit(ctx context.Context, p Peer, filter AuditFilter, cursor string, limit int) ([]AuditRecord, string, error) {
	q := url.Values{}
	if filter.User != "" {
		q.Set("user", filter.User)
	}
	if filter.Action != "" {
		q.Set("action", filter.Action)
	}
	if filter.Target != "" {
		q.Set("target", filter.Target)
	}
	if filter.Result != "" {
		q.Set("result", filter.Result)
	}
	if filter.ChangesetID != "" {
		q.Set("changesetId", filter.ChangesetID)
	}
	if filter.From != 0 {
		q.Set("from", strconv.FormatInt(filter.From, 10))
	}
	if filter.To != 0 {
		q.Set("to", strconv.FormatInt(filter.To, 10))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/audit?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out auditPageResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.NextCursor, nil
}

// Flows fetches one page of peer p's own local flow_samples ring (T-1002),
// filtered exactly like docs/api.md's GET /flows, for internal/api's
// cluster fan-out (fetchClusterFlows).
func (c *Client) Flows(ctx context.Context, p Peer, filter FlowFilter, cursor string, limit int) ([]FlowRecord, string, error) {
	q := url.Values{}
	if filter.Guest != "" {
		q.Set("guest", filter.Guest)
	}
	if filter.Subnet != "" {
		q.Set("subnet", filter.Subnet)
	}
	if filter.Source != "" {
		q.Set("source", filter.Source)
	}
	if filter.VLAN != 0 {
		q.Set("vlan", strconv.Itoa(filter.VLAN))
	}
	if filter.Port != 0 {
		q.Set("port", strconv.Itoa(filter.Port))
	}
	if filter.Proto != 0 {
		q.Set("proto", strconv.Itoa(filter.Proto))
	}
	if filter.FromTs != 0 {
		q.Set("fromTs", strconv.FormatInt(filter.FromTs, 10))
	}
	if filter.ToTs != 0 {
		q.Set("toTs", strconv.FormatInt(filter.ToTs, 10))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/flows?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out flowPageResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.NextCursor, nil
}

// NeighborBindingHistory fetches one page of peer p's own local
// neighbor_bindings ring (T-3905), filtered exactly like docs/api.md's
// GET /neighbors/history, for internal/api's cluster fan-out
// (fetchClusterNeighborBindings).
func (c *Client) NeighborBindingHistory(ctx context.Context, p Peer, filter NeighborBindingFilter, cursor string, limit int) ([]NeighborBindingRecord, string, error) {
	q := url.Values{}
	if filter.IP != "" {
		q.Set("ip", filter.IP)
	}
	if filter.MAC != "" {
		q.Set("mac", filter.MAC)
	}
	if filter.FromTs != 0 {
		q.Set("fromTs", strconv.FormatInt(filter.FromTs, 10))
	}
	if filter.ToTs != 0 {
		q.Set("toTs", strconv.FormatInt(filter.ToTs, 10))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/host/neighbors/history?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out neighborBindingPageResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.NextCursor, nil
}

// Snapshots fetches one page of peer p's own local snapshot list (T-303).
func (c *Client) Snapshots(ctx context.Context, p Peer, cursor string, limit int) ([]SnapshotRecord, string, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/snapshots?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out snapshotPageResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.NextCursor, nil
}

// Port returns the peer API port this Client dials when a discovered peer
// address needs one synthesized (ClientOptions.Port, defaulted to
// DefaultPort) — exported so callers that build their own Peer values from
// data they already fetched (T-303's collector, which learns node IPs from
// the same GET /cluster/status poll it already issues, rather than paying
// for a second discovery round-trip via Peers) use the exact same port this
// Client would.
func (c *Client) Port() int { return c.opts.Port }

// DiscardStaged asks peer p to drop node's staged interfaces.new, leaving
// the committed file untouched.
func (c *Client) DiscardStaged(ctx context.Context, p Peer, node string) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(nodeRequest{Node: node,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding discard-staged request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/discard-staged", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// InstallLLDPD asks peer p to install and enable lldpd on its node
// (docs/features/lldp-discovery.md §1's guided-install flow). confirm must
// be true or the peer rejects the request with validation_failed.
func (c *Client) InstallLLDPD(ctx context.Context, p Peer, confirm bool) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(installLLDPRequest{Confirm: confirm,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding lldp install request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/lldp/install", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// StartService asks peer p to start a watched systemd unit on its node
// (T-3604). The unit name is re-validated by the receiving node — this
// client's own caller having checked it is not what makes it safe.
func (c *Client) StartService(ctx context.Context, p Peer, unit string, confirm bool) error {
	a := AttributionFromContext(ctx)
	body, err := json.Marshal(startServiceRequest{Unit: unit, Confirm: confirm,
		writeAttribution: writeAttribution(a)})
	if err != nil {
		return fmt.Errorf("peer: encoding start-service request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/service/start", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// ArmTimer asks peer p to arm node's local commit-confirm rollback timer for
// changesetID: content is the byte-exact pre-apply state to restore if
// deadline (unix seconds) elapses uncancelled (T-304's local-timer
// protocol, docs/features/change-management.md §4).
func (c *Client) ArmTimer(ctx context.Context, p Peer, changesetID, node, content string, deadline int64) (TimerRecord, error) {
	body, err := json.Marshal(armTimerRequest{ChangesetID: changesetID, Node: node, Content: content, Deadline: deadline})
	if err != nil {
		return TimerRecord{}, fmt.Errorf("peer: encoding arm-timer request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/timer/arm", body)
	if err != nil {
		return TimerRecord{}, err
	}
	var out timerResponse
	if err := decodeInto(resp, &out); err != nil {
		return TimerRecord{}, err
	}
	return out.Record, nil
}

// CancelTimer asks peer p to cancel node's armed rollback timer for
// changesetID (the changeset was confirmed before the deadline).
func (c *Client) CancelTimer(ctx context.Context, p Peer, changesetID, node string) (TimerRecord, error) {
	return c.timerRequest(ctx, p, "/api/peer/timer/cancel", changesetID, node)
}

// TimerStatus fetches peer p's current record of node's rollback timer for
// changesetID — the coordinator's reconciliation-on-reconnect read. Returns
// *ResponseError with Code == "timer_not_found" (checkable via
// errors.Is(err, peer.ErrTimerNotFound) after ResponseError's own doc, see
// decodeInto) if p never armed one.
func (c *Client) TimerStatus(ctx context.Context, p Peer, changesetID, node string) (TimerRecord, error) {
	path := fmt.Sprintf("/api/peer/timer/status?changesetId=%s&node=%s", url.QueryEscape(changesetID), url.QueryEscape(node))
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return TimerRecord{}, err
	}
	var out timerResponse
	if decErr := decodeInto(resp, &out); decErr != nil {
		return TimerRecord{}, mapTimerNotFound(decErr)
	}
	return out.Record, nil
}

func (c *Client) timerRequest(ctx context.Context, p Peer, path, changesetID, node string) (TimerRecord, error) {
	body, err := json.Marshal(timerRequest{ChangesetID: changesetID, Node: node})
	if err != nil {
		return TimerRecord{}, fmt.Errorf("peer: encoding %s request: %w", path, err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, path, body)
	if err != nil {
		return TimerRecord{}, err
	}
	var out timerResponse
	if decErr := decodeInto(resp, &out); decErr != nil {
		return TimerRecord{}, mapTimerNotFound(decErr)
	}
	return out.Record, nil
}

// mapTimerNotFound rewraps a *ResponseError carrying the timer_not_found
// code so callers can errors.Is(err, ErrTimerNotFound) across the wire, the
// same convention ErrPeerUnreachable/ErrPeerIncompatible already establish
// for this client.
func mapTimerNotFound(err error) error {
	var respErr *ResponseError
	if errors.As(err, &respErr) && respErr.Code == errCodeTimerNotFound {
		return fmt.Errorf("peer: %w: %s", ErrTimerNotFound, respErr.Message)
	}
	return err
}

// CaptureStart asks peer p to run one node-local capture (T-1301). The peer
// re-validates the filter and re-clamps the caps against its own config
// before running — this call never overrides the peer's own ceilings.
func (c *Client) CaptureStart(ctx context.Context, p Peer, spec CaptureSpec) (CaptureResult, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("peer: encoding capture start request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/capture/start", body)
	if err != nil {
		return CaptureResult{}, err
	}
	var out CaptureResult
	if err := decodeInto(resp, &out); err != nil {
		return CaptureResult{}, err
	}
	return out, nil
}

// CaptureStop asks peer p to stop node-local capture sessionID.
func (c *Client) CaptureStop(ctx context.Context, p Peer, sessionID string) (CaptureResult, error) {
	body, err := json.Marshal(captureStopRequest{SessionID: sessionID})
	if err != nil {
		return CaptureResult{}, fmt.Errorf("peer: encoding capture stop request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/capture/stop", body)
	if err != nil {
		return CaptureResult{}, err
	}
	var out CaptureResult
	if err := decodeInto(resp, &out); err != nil {
		return CaptureResult{}, err
	}
	return out, nil
}

// CaptureStatus fetches peer p's current accounting for node-local capture
// sessionID.
func (c *Client) CaptureStatus(ctx context.Context, p Peer, sessionID string) (CaptureResult, error) {
	path := "/api/peer/capture/status?sessionId=" + url.QueryEscape(sessionID)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return CaptureResult{}, err
	}
	var out CaptureResult
	if err := decodeInto(resp, &out); err != nil {
		return CaptureResult{}, err
	}
	return out, nil
}

// CaptureDownload fetches the raw pcap bytes of node-local capture
// sessionID from peer p (T-1302) — the whole file, buffered (sessions are
// already byte-capped by [capture] max_bytes).
func (c *Client) CaptureDownload(ctx context.Context, p Peer, sessionID string) ([]byte, error) {
	path := "/api/peer/capture/download?sessionId=" + url.QueryEscape(sessionID)
	resp, err := c.do(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out captureDownloadResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return out.Content, nil
}

// Replicate pushes one HA replication batch (opaque JSON) to peer p and
// returns the ack payload (T-1704). The batch/ack shapes are internal/ha's
// concern — this method carries them as raw bytes so internal/peer stays free
// of an internal/ha or internal/store import, the same decoupling AuditReader/
// FlowReader use on the server side.
func (c *Client) Replicate(ctx context.Context, p Peer, payload []byte) ([]byte, error) {
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/ha/replicate", payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeInto(resp, nil)
	}
	return io.ReadAll(resp.Body)
}

// Health checks peer p's /api/peer/health.
func (c *Client) Health(ctx context.Context, p Peer) error {
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/health", nil)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// Version fetches peer p's VersionInfo.
func (c *Client) Version(ctx context.Context, p Peer) (VersionInfo, error) {
	resp, err := c.do(ctx, p, http.MethodGet, "/api/peer/version", nil)
	if err != nil {
		return VersionInfo{}, err
	}
	var out VersionInfo
	if err := decodeInto(resp, &out); err != nil {
		return VersionInfo{}, err
	}
	return out, nil
}

// CheckCompatible fetches peer p's version and returns ErrPeerIncompatible
// (wrapped, with both protocol versions in the message) if it doesn't
// match ProtocolVersion — the "incompatible peer -> coordination refusal"
// surfacing docs/architecture.md §5 documents.
func (c *Client) CheckCompatible(ctx context.Context, p Peer) error {
	v, err := c.Version(ctx, p)
	if err != nil {
		return err
	}
	if v.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("peer: %s: %w: local protocol %d, peer protocol %d", p.Node, ErrPeerIncompatible, ProtocolVersion, v.ProtocolVersion)
	}
	return nil
}
