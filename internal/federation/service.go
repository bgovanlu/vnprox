// Package federation implements T-1201's federation core: a cluster registry
// (CRUD over the app-owned clusters table, with each attached cluster's PVE
// credential encrypted at rest) plus an Aggregator that fans reads out to N
// clusters' own internal/pve.Clients concurrently, with per-cluster failure
// isolation.
//
// Federation federates *views and workflows*, never config ownership
// (docs/roadmap-next.md's Phase 12 invariants). Proxmox stays each cluster's
// own source of truth: this package never persists a shadow copy of any
// attached cluster's network config — the registry holds only which clusters
// to attach and how to authenticate to them, and the Aggregator recomputes
// every aggregate read fresh. There is deliberately no cross-cluster mutation
// primitive here; per-cluster changeset scoping (internal/change's
// ClusterMembership seam, which Aggregator.NodeClusters satisfies) keeps a
// changeset from ever spanning clusters.
package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Cipher is the subset of *store.SessionCipher the registry needs to seal /
// unseal a cluster's PVE credential at rest — declared as an interface (the
// same seam pattern internal/api's SecretCipher uses) so tests can substitute
// a fake without real AES-GCM key material. It is the identical AES-256-GCM
// primitive sessions.pve_ticket_enc uses, not a second key pair
// (docs/security.md's federation credential-storage note).
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(sealed []byte) ([]byte, error)
}

// CredentialKind selects how a cluster's stored credential authenticates to
// its PVE API.
const (
	// CredentialTicket is username/password (+realm) ticket-bridge auth —
	// PVE issues a ticket, exactly like an interactive user login.
	CredentialTicket = "ticket"
	// CredentialToken is a PVE API token ("user@realm!tokenid=secret").
	CredentialToken = "token"
)

// Credential is the sealed-at-rest authentication material for one attached
// cluster. Exactly one form is populated per credential: ticket
// (Username/Password/Realm) or token (Token). The whole struct is JSON-
// serialized and sealed as clusters.credential_enc; it is never returned by
// any API response.
type Credential struct {
	Kind     string `json:"kind"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Realm    string `json:"realm,omitempty"`
	Token    string `json:"token,omitempty"`
}

func (c Credential) validate() error {
	switch c.Kind {
	case CredentialTicket:
		if c.Username == "" || c.Password == "" {
			return fmt.Errorf("federation: ticket credential requires username and password")
		}
	case CredentialToken:
		if c.Token == "" {
			return fmt.Errorf("federation: token credential requires a token value")
		}
	default:
		return fmt.Errorf("federation: unknown credential kind %q", c.Kind)
	}
	return nil
}

// How a Cluster's WgTunnelID was arrived at — see Service.resolveLinkage.
const (
	// TunnelLinkExplicit: the operator set clusters.wg_tunnel_id directly
	// (PUT /federation/clusters/{id}'s wgTunnelId). Always wins.
	TunnelLinkExplicit = "explicit"
	// TunnelLinkPeer: derived from a WireGuard peer annotated with this
	// cluster (wireguard_peers.cluster_id), staged through an ordinary
	// wg.peer.add changeset — the wizard path.
	TunnelLinkPeer = "peer"
)

// Cluster is the app-facing, credential-free registry entry — everything
// GET /federation/clusters may report. The sealed credential never appears
// here.
//
// WgTunnelID is the cluster's *effective* tunnel linkage, not necessarily the
// stored clusters.wg_tunnel_id column: an empty column is filled in from the
// peer-level wireguard_peers.cluster_id annotation when a TunnelLinker is
// configured (see resolveLinkage). WgTunnelSource says which of the two it
// came from, so a reader can tell an operator override from a derived link;
// it is empty exactly when WgTunnelID is.
type Cluster struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	APIURL         string `json:"apiUrl"`
	Status         string `json:"status"`
	AddedBy        string `json:"addedBy"`
	WgTunnelID     string `json:"wgTunnelId,omitempty"`
	WgTunnelSource string `json:"wgTunnelSource,omitempty"`
	AddedAt        int64  `json:"addedAt"`
}

func toCluster(row store.Cluster) Cluster {
	c := Cluster{ID: row.ID, Name: row.Name, APIURL: row.APIURL, Status: row.Status, AddedBy: row.AddedBy, AddedAt: row.AddedAt, WgTunnelID: row.WgTunnelID}
	if c.WgTunnelID != "" {
		c.WgTunnelSource = TunnelLinkExplicit
	}
	return c
}

// TunnelLinker resolves the peer-level half of the tunnel<->cluster linkage:
// which WireGuard tunnel carries a peer annotated as this federated cluster
// (*store.WireGuardRepo.TunnelIDForCluster satisfies it). Optional — a nil
// linker means only the explicit clusters.wg_tunnel_id column is consulted,
// which is exactly T-1407's shipped behaviour.
//
// This seam is what keeps the two columns from drifting. They record the same
// fact at different granularities (a peer *is* a cluster; a cluster is
// *reached over* a tunnel), so rather than letting an operator set both and
// pick a winner per read site, there is one effective answer resolved here and
// consumed by everything downstream — internal/federation.Aggregator's
// splitTunnelDown and internal/findings' tunnel_down_peer_unreachable producer
// both read Cluster.WgTunnelID and therefore cannot disagree.
type TunnelLinker interface {
	TunnelIDForCluster(ctx context.Context, clusterID string) (string, error)
}

// ClusterRepo is the subset of *store.ClusterRepo the Service needs.
type ClusterRepo interface {
	Insert(ctx context.Context, c store.Cluster) error
	Get(ctx context.Context, id string) (store.Cluster, error)
	List(ctx context.Context) ([]store.Cluster, error)
	Update(ctx context.Context, c store.Cluster) error
	UpdateStatus(ctx context.Context, id, status string) error
	Delete(ctx context.Context, id string) error
}

// Config configures a Service. Clusters and Cipher are required; Now/Logger
// default sensibly, and TLS/PVERequestTimeout tune the per-cluster PVE
// clients ClientFor builds (a self-signed real-PVE deployment sets TLS; the
// mock/http test path leaves it zero).
type Config struct {
	Clusters          ClusterRepo
	Cipher            Cipher
	Logger            *slog.Logger
	Now               func() time.Time
	TunnelLinker      TunnelLinker
	TLS               pve.TLSConfig
	PVERequestTimeout time.Duration
}

// Service is the cluster registry: CRUD over the clusters table plus the
// factory that turns a stored, sealed credential into an authenticated
// *pve.Client for the Aggregator to fan out through.
type Service struct {
	repo    ClusterRepo
	cipher  Cipher
	log     *slog.Logger
	now     func() time.Time
	linker  TunnelLinker
	tls     pve.TLSConfig
	timeout time.Duration
}

// NewService constructs a Service. Config.Clusters and Config.Cipher are
// required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Clusters == nil {
		return nil, fmt.Errorf("federation: Config.Clusters is required")
	}
	if cfg.Cipher == nil {
		return nil, fmt.Errorf("federation: Config.Cipher is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: cfg.Clusters, cipher: cfg.Cipher, log: logger, now: now, linker: cfg.TunnelLinker, tls: cfg.TLS, timeout: cfg.PVERequestTimeout}, nil
}

// resolveLinkage fills c's effective WireGuard tunnel linkage. An explicit
// clusters.wg_tunnel_id (already set by toCluster) always wins: it is the
// operator's deliberate override, including for a tunnel that has no peer
// annotated for this cluster at all. Otherwise the linkage is derived from the
// peer-level wireguard_peers.cluster_id annotation — which is how the ordinary
// path sets it, since the connect-clusters wizard tags the peer inside the same
// changeset that creates the tunnel (one mutation path, no side-channel write
// to the clusters table).
//
// A linker error is logged and swallowed: an unresolvable linkage degrades to
// "not tunnel-linked", the same fail-open direction every other T-1407 path
// takes (a dangling link must never hide a cluster's data or break a cluster
// read).
func (s *Service) resolveLinkage(ctx context.Context, c Cluster) Cluster {
	if s.linker == nil || c.WgTunnelID != "" {
		return c
	}
	tunnelID, err := s.linker.TunnelIDForCluster(ctx, c.ID)
	if err != nil {
		s.log.Debug("federation: resolving peer-derived tunnel linkage", "cluster", c.ID, "error", err)
		return c
	}
	if tunnelID != "" {
		c.WgTunnelID, c.WgTunnelSource = tunnelID, TunnelLinkPeer
	}
	return c
}

// Add validates and seals cred, then registers a new attached cluster. The
// returned Cluster carries no credential material.
func (s *Service) Add(ctx context.Context, name, apiURL string, cred Credential, addedBy string) (Cluster, error) {
	if strings.TrimSpace(name) == "" {
		return Cluster{}, fmt.Errorf("federation: cluster name is required")
	}
	if strings.TrimSpace(apiURL) == "" {
		return Cluster{}, fmt.Errorf("federation: cluster apiUrl is required")
	}
	if err := cred.validate(); err != nil {
		return Cluster{}, err
	}
	enc, err := s.sealCredential(cred)
	if err != nil {
		return Cluster{}, err
	}
	row := store.Cluster{
		ID: store.NewULID(), Name: name, APIURL: apiURL, CredentialEnc: enc,
		Status: "unknown", AddedBy: addedBy, AddedAt: s.now().Unix(),
	}
	if err := s.repo.Insert(ctx, row); err != nil {
		return Cluster{}, fmt.Errorf("federation: adding cluster: %w", err)
	}
	return toCluster(row), nil
}

// Get returns one registered cluster (credential-free), with its effective
// tunnel linkage resolved (resolveLinkage), wrapping store.ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Cluster, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return Cluster{}, fmt.Errorf("federation: getting cluster %s: %w", id, err)
	}
	return s.resolveLinkage(ctx, toCluster(row)), nil
}

// List returns every registered cluster (credential-free), each with its
// effective tunnel linkage resolved (resolveLinkage).
func (s *Service) List(ctx context.Context) ([]Cluster, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("federation: listing clusters: %w", err)
	}
	out := make([]Cluster, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.resolveLinkage(ctx, toCluster(row)))
	}
	return out, nil
}

// Update rewrites a cluster's name/apiUrl, and — only when cred is non-nil —
// re-seals a replacement credential. A nil cred leaves the existing sealed
// credential untouched (an edit that just renames the cluster must never
// force the operator to re-enter the token). wgTunnelID follows the same
// nil-leaves-unchanged convention (T-1407): nil leaves the existing linkage
// untouched, a non-nil pointer (including one pointing at "") replaces it —
// so clearing the linkage is an explicit `&""`, not "omit the field".
//
// wgTunnelID writes the *explicit override* column only. Clearing it therefore
// does not necessarily leave the cluster unlinked: the returned Cluster runs
// through resolveLinkage, so a cluster that still has a WireGuard peer tagged
// with its id comes back linked with WgTunnelSource == TunnelLinkPeer. Undoing
// a peer-derived link means removing (or retagging) that peer through an
// ordinary wg.peer.* changeset, not editing the cluster.
//
// Returns store.ErrNotFound if the cluster doesn't exist.
func (s *Service) Update(ctx context.Context, id, name, apiURL string, cred *Credential, wgTunnelID *string) (Cluster, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return Cluster{}, fmt.Errorf("federation: getting cluster %s for update: %w", id, err)
	}
	if strings.TrimSpace(name) != "" {
		row.Name = name
	}
	if strings.TrimSpace(apiURL) != "" {
		row.APIURL = apiURL
	}
	if cred != nil {
		if err := cred.validate(); err != nil {
			return Cluster{}, err
		}
		enc, err := s.sealCredential(*cred)
		if err != nil {
			return Cluster{}, err
		}
		row.CredentialEnc = enc
	}
	if wgTunnelID != nil {
		row.WgTunnelID = strings.TrimSpace(*wgTunnelID)
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return Cluster{}, fmt.Errorf("federation: updating cluster %s: %w", id, err)
	}
	return s.resolveLinkage(ctx, toCluster(row)), nil
}

// Delete deregisters a cluster (idempotent — deleting an absent cluster is
// not an error, per the store repo's convention).
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("federation: deleting cluster %s: %w", id, err)
	}
	return nil
}

// SetStatus records the last aggregation pass's reachability cache for a
// cluster. Best-effort: a failure is logged, never surfaced, since a stale
// cache only affects the summary GET, never correctness.
func (s *Service) SetStatus(ctx context.Context, id, status string) {
	if err := s.repo.UpdateStatus(ctx, id, status); err != nil {
		s.log.Debug("federation: updating cluster status cache", "cluster", id, "error", err)
	}
}

// ClientFor builds an authenticated *pve.Client for cluster id by unsealing
// its stored credential. *pve.Client is lazy (no network call until the
// first request), so this is cheap; the Aggregator calls it once per cluster
// per fan-out.
func (s *Service) ClientFor(ctx context.Context, id string) (*pve.Client, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("federation: getting cluster %s for client: %w", id, err)
	}
	return s.clientForRow(row)
}

func (s *Service) clientForRow(row store.Cluster) (*pve.Client, error) {
	cred, err := s.openCredential(row.CredentialEnc)
	if err != nil {
		return nil, fmt.Errorf("federation: unsealing credential for cluster %s: %w", row.ID, err)
	}
	cfg := pve.Config{APIURL: row.APIURL, TLS: s.tls, RequestTimeout: s.timeout}
	switch cred.Kind {
	case CredentialToken:
		cfg.Auth = pve.AuthAPIToken
		cfg.TokenValue = cred.Token
	default:
		cfg.Auth = pve.AuthTicket
		cfg.Username = cred.Username
		cfg.Password = cred.Password
		cfg.Realm = cred.Realm
	}
	client, err := pve.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("federation: building PVE client for cluster %s: %w", row.ID, err)
	}
	return client, nil
}

func (s *Service) sealCredential(cred Credential) ([]byte, error) {
	plain, err := json.Marshal(cred)
	if err != nil {
		return nil, fmt.Errorf("federation: marshaling credential: %w", err)
	}
	enc, err := s.cipher.Encrypt(plain)
	if err != nil {
		return nil, fmt.Errorf("federation: sealing credential: %w", err)
	}
	return enc, nil
}

func (s *Service) openCredential(sealed []byte) (Credential, error) {
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return Credential{}, err
	}
	var cred Credential
	if err := json.Unmarshal(plain, &cred); err != nil {
		return Credential{}, fmt.Errorf("federation: decoding credential: %w", err)
	}
	return cred, nil
}
