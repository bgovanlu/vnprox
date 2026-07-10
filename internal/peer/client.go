package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	ClusterStatus           ClusterStatusSource
	Secrets                 *SecretStore
	HTTPClient              *http.Client
	Logger                  *slog.Logger
	Now                     func() time.Time
	Scheme                  string
	Port                    int
	RequestTimeout          time.Duration
	BreakerFailureThreshold int
	BreakerResetTimeout     time.Duration
}

// Client is the peer API client: discovery, HMAC signing, and a per-peer
// circuit breaker. Safe for concurrent use.
type Client struct {
	httpClient *http.Client
	breakers   map[string]*circuitBreaker
	opts       ClientOptions
	mu         sync.Mutex
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
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: opts.RequestTimeout}
	}
	return &Client{opts: opts, httpClient: hc, breakers: make(map[string]*circuitBreaker)}
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
// caller to decode. The circuit breaker fast-fails (ErrPeerUnreachable,
// without attempting the network) when peer's breaker is open; a
// transport-level failure (dial/timeout/TLS) records a breaker failure and
// also returns ErrPeerUnreachable, while any received HTTP response
// (whatever its status) records a breaker success — the breaker tracks
// reachability, not application-level correctness.
func (c *Client) do(ctx context.Context, p Peer, method, path string, body []byte) (*http.Response, error) {
	breaker := c.breakerFor(p.Node)
	if !breaker.allow() {
		return nil, fmt.Errorf("peer: %s: %w: circuit open", p.Node, ErrPeerUnreachable)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()

	target := fmt.Sprintf("%s://%s%s", c.opts.Scheme, p.Addr, path)
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, target, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("peer: building request to %s: %w", p.Node, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	secret := c.opts.Secrets.Current()
	ts := c.opts.Now().Unix()
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, sign(secret, method, req.URL.RequestURI(), body, ts))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		breaker.recordFailure()
		return nil, fmt.Errorf("peer: %s: %w: %v", p.Node, ErrPeerUnreachable, err)
	}
	breaker.recordSuccess()
	return resp, nil
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

// StageInterfaces asks peer p to stage content as node's interfaces.new.
func (c *Client) StageInterfaces(ctx context.Context, p Peer, node, content string) error {
	body, err := json.Marshal(stageRequest{Node: node, Content: content})
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
	body, err := json.Marshal(nodeRequest{Node: node})
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
	body, err := json.Marshal(stageRequest{Node: node, Content: content})
	if err != nil {
		return fmt.Errorf("peer: encoding restore request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/restore", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
}

// InstallLLDPD asks peer p to install and enable lldpd on its node
// (docs/features/lldp-discovery.md §1's guided-install flow). confirm must
// be true or the peer rejects the request with validation_failed.
func (c *Client) InstallLLDPD(ctx context.Context, p Peer, confirm bool) error {
	body, err := json.Marshal(installLLDPRequest{Confirm: confirm})
	if err != nil {
		return fmt.Errorf("peer: encoding lldp install request: %w", err)
	}
	resp, err := c.do(ctx, p, http.MethodPost, "/api/peer/host/lldp/install", body)
	if err != nil {
		return err
	}
	return decodeInto(resp, nil)
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
