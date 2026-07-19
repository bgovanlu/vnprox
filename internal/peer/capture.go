package peer

import "context"

// Capture wire types for the additive /api/peer/capture/* routes (T-1301):
// simultaneous multi-point packet capture across nodes. These mirror
// internal/capture's Spec/Result/Caps field-for-field but are peer's own
// copies (internal/peer never imports internal/capture — the same
// import-direction discipline AuditRecord/FlowRecord keep); cmd/vnproxd
// adapts the concrete *capture.Coordinator to CaptureAgent and *peer.Client
// to capture.RemoteCapturer.
//
// The routes are additive and protocol-version-2-compatible (no bump),
// following the flows/links/neighbors precedent.

// CaptureCaps is the server-clamped effective cap set carried on a capture
// spec. The receiving node re-clamps these against its own config before
// running (the un-overridable-cap guarantee holds independently per node).
type CaptureCaps struct {
	MaxDurationSec int   `json:"maxDurationSec"`
	MaxBytes       int64 `json:"maxBytes"`
	MaxPackets     int64 `json:"maxPackets"`
	RetentionHours int   `json:"retentionHours"`
}

// CaptureSpec is POST /api/peer/capture/start's body: everything the
// receiving node needs to run one node-local capture and persist its own
// capture_sessions row. It deliberately carries no on-disk file path — the
// receiving node always derives that from its own [capture] root
// (StartLocalSpec), so a caller can never steer where the .pcap is written.
type CaptureSpec struct {
	SessionID string      `json:"sessionId"`
	GroupID   string      `json:"groupId"`
	TargetRef string      `json:"targetRef"`
	Node      string      `json:"node"`
	Iface     string      `json:"iface"`
	Filter    string      `json:"filter"`
	StartedBy string      `json:"startedBy"`
	Nodes     []string    `json:"nodes,omitempty"`
	Caps      CaptureCaps `json:"caps"`
	StartedAt int64       `json:"startedAt"`
}

// CaptureResult is the accounting reply every /api/peer/capture/* route
// returns — packet/byte counts and status only, never payload bytes.
type CaptureResult struct {
	Status  string `json:"status"`
	Packets int64  `json:"packets"`
	Bytes   int64  `json:"bytes"`
}

// captureStopRequest is POST /api/peer/capture/stop's body.
type captureStopRequest struct {
	SessionID string `json:"sessionId"`
}

// CaptureAgent is the peer-server-side dependency for /api/peer/capture/*:
// the receiving node's own local capture coordinator, scoped to
// local-node-only start/stop/status (no further fan-out). *capture.Coordinator
// is adapted to this shape in cmd/vnproxd. Optional (nil-safe: the routes
// 503 rather than panic, like every other optional ServerOptions dependency).
type CaptureAgent interface {
	StartLocal(ctx context.Context, spec CaptureSpec) (CaptureResult, error)
	StopLocal(ctx context.Context, sessionID string) (CaptureResult, error)
	StatusLocal(ctx context.Context, sessionID string) (CaptureResult, error)
	// DownloadLocal returns the raw pcap bytes of a session this node
	// captured (T-1302). The whole file is returned at once — sessions are
	// already byte-capped by [capture] max_bytes, so this is bounded.
	DownloadLocal(ctx context.Context, sessionID string) ([]byte, error)
}

// captureDownloadResponse is GET /api/peer/capture/download's body
// (T-1302). Content is JSON-encoded as a base64 string (Go's encoding/json
// does this automatically for a []byte field) rather than served as a raw
// octet stream — the peer wire format stays JSON end-to-end, like every
// other peer route in this codebase (DHCPLeases' text-content precedent,
// generalized to binary).
type captureDownloadResponse struct {
	Content []byte `json:"content"`
}
