package capture

import "context"

// Spec fully describes one node-local capture the Agent must run. It carries
// everything a capturing node needs both to run the capture and to persist
// its own capture_sessions row (so a peer that receives this over the peer
// API owns its file and its own retention sweep) — the coordinator resolves
// Iface and FilePath and clamps Caps before building it.
type Spec struct {
	SessionID string
	GroupID   string
	TargetRef string
	Node      string
	Iface     string
	Filter    string
	FilePath  string
	StartedBy string
	Nodes     []string
	Caps      Caps
	StartedAt int64
}

// Result is a point-in-time accounting snapshot of a capture: how many
// packets/bytes have been written and the current status. No payload bytes
// are ever carried here — only counts (docs/security.md's "payload never
// leaves the bounded file").
type Result struct {
	Status  Status
	Packets int64
	Bytes   int64
}

// Process is one running node-local capture. It writes packets to
// Spec.FilePath, stops itself once any server cap is hit (transitioning to
// StatusCompleted), and reports live accounting via Result. Stop halts it
// early and returns the final Result. Implementations must be safe for
// concurrent Result/Stop calls.
type Process interface {
	Result() Result
	Stop(ctx context.Context) Result
}

// Agent starts node-local captures. The production implementation binds a
// real capture backend (AF_PACKET/libpcap/tcpdump — needs-hardware); tests
// and dev use internal/capturemock's scripted agent.
type Agent interface {
	Start(ctx context.Context, spec Spec) (Process, error)
}

// Target is a resolved capture target: its owning node and the concrete host
// interface a capture on it binds to.
type Target struct {
	Ref   string
	Node  string
	Iface string
}

// TargetResolver maps a target Ref (bridge/bond/guest-NIC/SDN-VNet) to the
// node + interface a capture binds to. A ref it cannot resolve to a concrete
// interface yields ErrUnresolvableTarget from the coordinator — the "can't
// be scoped to the target's own interface" rejection, raised before any
// capture process is invoked.
type TargetResolver interface {
	Resolve(ctx context.Context, ref string) (Target, error)
}

// RemoteCapturer runs node-local captures on a *remote* node over the
// HMAC-gated peer API. cmd/vnproxd adapts *peer.Client to this shape
// (dispatching a node name to its discovered peer). Declared here (rather
// than importing internal/peer) so internal/peer never depends on
// internal/capture — the same import-direction discipline every other
// peer-fan-out seam in this codebase follows.
type RemoteCapturer interface {
	Start(ctx context.Context, node string, spec Spec) (Result, error)
	Stop(ctx context.Context, node, sessionID string) (Result, error)
	Status(ctx context.Context, node, sessionID string) (Result, error)
	// Download fetches the raw pcap bytes of one session that lives on a
	// remote node (T-1302: "everything is cluster-aware" — a per-session
	// download must work whether the file is local or on a peer). The whole
	// file is buffered (sessions are already byte-capped by [capture]
	// max_bytes, so this is bounded), never streamed opaquely, so the
	// bytes-in-flight are exactly the bytes a caller with capture+netRead is
	// already entitled to see.
	Download(ctx context.Context, node, sessionID string) ([]byte, error)
}

// AuditEvent is one capture.start / capture.stop audit row the coordinator
// emits. Detail carries the resolved filter and effective caps (never any
// payload). cmd/vnproxd adapts the concrete *store.AuditRepo to Auditor.
type AuditEvent struct {
	Detail    map[string]any
	Actor     string
	Action    string // "capture.start" | "capture.stop"
	TargetRef string
	Result    string // "ok" | "error"
	At        int64
}

// Auditor appends capture audit rows. Declared as a one-method seam (like
// internal/api's lldpInstallAuditor) so this package never imports
// internal/store.
type Auditor interface {
	AppendCapture(ctx context.Context, e AuditEvent) error
}

// SessionStore is the capture_sessions persistence seam. *store.CaptureRepo
// satisfies it directly.
type SessionStore interface {
	Upsert(ctx context.Context, s Session) error
	Get(ctx context.Context, id string) (Session, error)
	ByGroup(ctx context.Context, groupID string) ([]Session, error)
	ListGroups(ctx context.Context) ([]string, error)
	List(ctx context.Context) ([]Session, error)
	Delete(ctx context.Context, id string) error
}
