package peer

import (
	"sort"
	"sync"
	"time"
)

// PeerTrustState is the last outcome of talking to one peer, reduced to the
// three things an operator has to be able to tell apart (T-1906 AC5):
//
//   - PeerTrustOK — the peer answered over a connection whose certificate
//     chained to the pinned cluster CA.
//   - PeerTrustUnreachable — nothing answered. A cable, a firewall, a down
//     node. Nobody attacked anything.
//   - PeerTrustUntrusted — something answered on the peer port and presented a
//     certificate this daemon will not accept. That is either a broken
//     certificate rollout or an impersonation attempt, and it is never a
//     network problem.
//
// Collapsing the last two (which the pre-T-1906 client did — every transport
// error became ErrPeerUnreachable) means an operator watching a peer "go
// offline" has no way to see that it is in fact a different machine answering.
type PeerTrustState string

const (
	PeerTrustOK          PeerTrustState = "ok"
	PeerTrustUnreachable PeerTrustState = "unreachable"
	PeerTrustUntrusted   PeerTrustState = "untrusted"
)

// PeerStatus is one peer's last observed trust/reachability outcome.
type PeerStatus struct {
	At    time.Time
	Node  string
	Addr  string
	Error string
	State PeerTrustState
}

// TrustReport is the whole picture of this client's peer-TLS posture: the
// configured trust mode, whether its anchor is currently usable, the scheme it
// dials, and the last verdict for every peer it has actually talked to. It is
// the seam internal/findings consumes (via cmd/vnproxd's peerTrustAdapter) to
// raise the peer_untrusted / peer_unreachable / peer_trust_degraded findings.
//
// Only peers this client has genuinely attempted are listed — a report never
// invents a verdict for a peer nothing has spoken to yet.
type TrustReport struct {
	Mode        TrustMode
	CAFile      string
	AnchorError string
	Scheme      string
	Peers       []PeerStatus
	Pinned      bool
}

// peerStatusStore records the last verdict per peer node.
type peerStatusStore struct {
	byNode map[string]PeerStatus
	mu     sync.Mutex
}

func newPeerStatusStore() *peerStatusStore {
	return &peerStatusStore{byNode: make(map[string]PeerStatus)}
}

func (s *peerStatusStore) record(p Peer, state PeerTrustState, at time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := PeerStatus{Node: p.Node, Addr: p.Addr, State: state, At: at}
	if err != nil {
		st.Error = err.Error()
	}
	s.byNode[p.Node] = st
}

func (s *peerStatusStore) get(node string) (PeerStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byNode[node]
	return st, ok
}

func (s *peerStatusStore) all() []PeerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PeerStatus, 0, len(s.byNode))
	for _, st := range s.byNode {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// PeerStatuses returns the last recorded verdict for every peer this client
// has attempted, ordered by node name.
func (c *Client) PeerStatuses() []PeerStatus { return c.statuses.all() }

// TrustReport snapshots this client's peer-TLS posture. It forces a trust
// anchor re-evaluation (so a CA that has become unreadable, or has rotated,
// shows up in the findings stream on the next cycle rather than only on the
// next peer request).
func (c *Client) TrustReport() TrustReport {
	rep := TrustReport{Scheme: c.opts.Scheme, Peers: c.statuses.all(), Mode: TrustClusterCA, Pinned: true, CAFile: DefaultClusterCAPath}
	if c.trust == nil {
		// A caller-supplied ClientOptions.HTTPClient made its own trust
		// decision that this package cannot inspect — the pre-T-1906 shape,
		// where "the default is unset" was the whole vulnerability. Reported
		// honestly as unpinned rather than asserted to be safe.
		rep.Mode = TrustExternal
		rep.Pinned = false
		rep.CAFile = ""
		return rep
	}
	st := c.trust.Status()
	rep.Mode = st.Mode
	rep.Pinned = st.Pinned
	rep.CAFile = st.CAFile
	rep.AnchorError = st.Error
	return rep
}
