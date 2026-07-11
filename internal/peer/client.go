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
	body, err := json.Marshal(nodeRequest{Node: node})
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
