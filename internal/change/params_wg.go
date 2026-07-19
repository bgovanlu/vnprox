package change

// params_wg.go defines T-1401's WireGuard op parameter structs. Every wg.* op
// flows through the ordinary stage→validate→diff→apply→confirm/rollback
// changeset lifecycle — there is deliberately no second mutation path for
// WireGuard (CLAUDE.md's change-engine invariant).
//
// Target Refs (docs/data-model.md §3):
//   - wg.tunnel.* target a wg-tunnel Ref: {Node: owning node, ID: tunnel id}.
//   - wg.peer.*   target a wg-peer   Ref: {Node: owning node, ID:
//     "<tunnelID>/<peer public key>"}.
//
// A tunnel's keypair is NOT in any of these params: the private key is
// generated on the owning node as part of applying wg.tunnel.create and never
// travels in an op, a response, or a log line (docs/security.md's WireGuard
// credential-storage note). Key rotation is delete-and-recreate, never an
// in-place update — so WgTunnelUpdateParams intentionally carries no key field.

// WgTunnelCreateParams is op "wg.tunnel.create". IfName is the on-node
// interface (e.g. "wg0"); Carrier is the underlying interface the tunnel's
// endpoint rides on ("vmbr0", "bond0") — a tunnel whose Carrier is on a node's
// resolved management/corosync path makes every wg.* op on it touchesMgmtPath
// (mgmttouch.go), inheriting T-703's ceremony with no override. This holds for
// ops that do NOT carry the carrier in their params too — a standalone
// wg.peer.add/remove, a carrier-less wg.tunnel.update, or a wg.tunnel.delete on
// an already-existing tunnel — because TouchesMgmtPath resolves such a tunnel's
// stored carrier from a tunnelID->carrier lookup (WgCarrierSource), not only
// from the op params.
type WgTunnelCreateParams struct {
	IfName     string   `json:"ifName"`
	Carrier    string   `json:"carrier,omitempty"`
	Addresses  []string `json:"addresses,omitempty"`
	ListenPort int      `json:"listenPort,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
}

func (WgTunnelCreateParams) isChangeParams() {}

// WgTunnelUpdateParams is op "wg.tunnel.update": pointer fields are set only
// for the attributes being changed (nil == leave unchanged). No key field —
// key rotation is delete+recreate.
type WgTunnelUpdateParams struct {
	ListenPort *int      `json:"listenPort,omitempty"`
	Addresses  *[]string `json:"addresses,omitempty"`
	MTU        *int      `json:"mtu,omitempty"`
	Carrier    *string   `json:"carrier,omitempty"`
}

func (WgTunnelUpdateParams) isChangeParams() {}

// WgTunnelDeleteParams is op "wg.tunnel.delete". Deleting a tunnel removes its
// on-node interface/config, its store row (including the sealed private key),
// and every peer — no orphaned key material is left behind (T-1401 AC6).
type WgTunnelDeleteParams struct{}

func (WgTunnelDeleteParams) isChangeParams() {}

// WgPeerAddParams is op "wg.peer.add". External marks a peer vnprox does not
// own (a road-warrior, or a cluster vnprox does not manage): it is modeled
// read-only and config-export-only, and vnprox never runs an apply step
// against its own side (T-1401 AC5) — but it still appears in this tunnel's own
// on-node peer list, which is what lets it connect.
//
// The preshared key is a WireGuard secret and never rides an op or a read
// response in the clear. PresharedKey is a WRITE-ONLY ingest field: a client
// supplies the plaintext on create/update, Service.sealOpSecrets immediately
// seals it into PresharedKeyEnc (AES-256-GCM, the same SessionCipher the
// private key and sessions.pve_ticket_enc use) and clears PresharedKey, so
// only the sealed form is persisted in changesets.ops_json. The read surface
// (GET /changesets) strips both fields entirely (internal/api's
// redactOpSecrets). At apply time the owning node unseals PresharedKeyEnc
// just-in-time and re-seals it into wireguard_peers.preshared_key_enc.
type WgPeerAddParams struct {
	PublicKey       string   `json:"publicKey"`
	Endpoint        string   `json:"endpoint,omitempty"`
	PresharedKey    string   `json:"presharedKey,omitempty"`
	ClusterID       string   `json:"clusterId,omitempty"`
	AllowedIPs      []string `json:"allowedIps,omitempty"`
	PresharedKeyEnc []byte   `json:"presharedKeyEnc,omitempty"`
	KeepaliveSec    int      `json:"keepaliveSec,omitempty"`
	External        bool     `json:"external,omitempty"`
}

func (WgPeerAddParams) isChangeParams() {}

// WgPeerRemoveParams is op "wg.peer.remove": PublicKey identifies the peer
// within the target tunnel (redundant with the target Ref's own encoded
// public key, but carried explicitly so a hand-built op is unambiguous).
type WgPeerRemoveParams struct {
	PublicKey string `json:"publicKey"`
}

func (WgPeerRemoveParams) isChangeParams() {}
