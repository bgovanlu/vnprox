// SPDX-License-Identifier: Apache-2.0

package ha

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// PeerClient is the subset of *peer.Client the HA replicator uses — the
// TLS+HMAC-authenticated opaque-payload push (Client.Replicate). Declared as an
// interface so tests can substitute a double, and so this is the only coupling
// point between HA and the peer transport.
type PeerClient interface {
	Replicate(ctx context.Context, p peer.Peer, payload []byte) ([]byte, error)
}

// PeerReplicator is the production ha.Replicator: it marshals a Batch to JSON
// and pushes it to the standby over internal/peer's existing TLS+HMAC channel,
// decoding the standby's Ack from the reply. Any transport error surfaces as a
// failed pass (the Manager treats it as a partition / dead peer).
type PeerReplicator struct {
	client PeerClient
	peer   peer.Peer
}

// NewPeerReplicator constructs a PeerReplicator targeting the standby peer.
func NewPeerReplicator(client PeerClient, standby peer.Peer) *PeerReplicator {
	return &PeerReplicator{client: client, peer: standby}
}

// Push sends batch to the standby and returns its Ack.
func (r *PeerReplicator) Push(ctx context.Context, batch Batch) (Ack, error) {
	payload, err := json.Marshal(batch)
	if err != nil {
		return Ack{}, fmt.Errorf("ha: marshaling replication batch: %w", err)
	}
	ackBytes, err := r.client.Replicate(ctx, r.peer, payload)
	if err != nil {
		return Ack{}, fmt.Errorf("ha: pushing replication batch to %s: %w", r.peer.Node, err)
	}
	var ack Ack
	if err := json.Unmarshal(ackBytes, &ack); err != nil {
		return Ack{}, fmt.Errorf("ha: decoding replication ack from %s: %w", r.peer.Node, err)
	}
	return ack, nil
}

// ReceiveSink is the peer-server-side adapter (implements
// peer.ReplicationSink structurally): it decodes an incoming batch payload,
// applies it via the Manager's fenced Receive path, and returns the marshaled
// Ack. cmd/vnproxd wires one into peer.ServerOptions.Replication.
type ReceiveSink struct {
	mgr *Manager
}

// NewReceiveSink wraps mgr as a peer replication sink.
func NewReceiveSink(mgr *Manager) *ReceiveSink { return &ReceiveSink{mgr: mgr} }

// Replicate decodes payload as a Batch, applies it through Manager.Receive
// (which enforces the fencing check), and returns the Ack as JSON.
func (s *ReceiveSink) Replicate(ctx context.Context, payload []byte) ([]byte, error) {
	var batch Batch
	if err := json.Unmarshal(payload, &batch); err != nil {
		return nil, fmt.Errorf("ha: decoding incoming replication batch: %w", err)
	}
	ack, err := s.mgr.Receive(ctx, batch)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("ha: marshaling replication ack: %w", err)
	}
	return out, nil
}
