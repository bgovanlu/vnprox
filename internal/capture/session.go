// SPDX-License-Identifier: Apache-2.0

package capture

// Status is the lifecycle state of one node-local capture session.
type Status string

const (
	// StatusRunning: the capture is live, no cap yet hit.
	StatusRunning Status = "running"
	// StatusCompleted: the capture stopped on its own because a server cap
	// (duration/bytes/packets) was reached.
	StatusCompleted Status = "completed"
	// StatusStopped: the capture was explicitly stopped by an operator
	// before any cap was hit.
	StatusStopped Status = "stopped"
	// StatusError: the capture failed to start or errored while running.
	StatusError Status = "error"
	// StatusPurged: the capture's on-disk file has been deleted by the
	// auto-purge sweep (retention age reached). The row is kept for audit
	// continuity but the payload file is gone.
	StatusPurged Status = "purged"
)

// Terminal reports whether s is a state a capture never leaves.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusStopped, StatusError, StatusPurged:
		return true
	default:
		return false
	}
}

// Caps is the server-enforced ceiling set for a capture session: a hard
// duration, byte, and packet cap (whichever is hit first stops the
// capture), plus the retention window the auto-purge sweep enforces on the
// resulting file. These are never client-overridable upward — a request may
// only ask for values at or below the configured ceilings (clampCaps).
type Caps struct {
	MaxDurationSec int   `json:"maxDurationSec"`
	MaxBytes       int64 `json:"maxBytes"`
	MaxPackets     int64 `json:"maxPackets"`
	RetentionHours int   `json:"retentionHours"`
}

// Session is one node-local capture — one row of the capture_sessions table
// (docs/data-model.md). A multi-point capture is a set of Sessions sharing a
// GroupID.
type Session struct {
	ID        string `json:"id"`
	GroupID   string `json:"groupId"`
	TargetRef string `json:"targetRef"`
	Node      string `json:"node"`
	Filter    string `json:"filter"`
	Status    Status `json:"status"`
	StartedBy string `json:"startedBy"`
	FilePath  string `json:"-"` // on-disk payload file; never serialized to API clients
	// Nodes is the full set of nodes participating in this session's group,
	// carried on every row (docs/data-model.md's nodes_json column) so any
	// one row knows its siblings without a second query.
	Nodes     []string `json:"nodes,omitempty"`
	Caps      Caps     `json:"caps"`
	StartedAt int64    `json:"startedAt"`
	StoppedAt int64    `json:"stoppedAt"`
	FileBytes int64    `json:"fileBytes"`
	Packets   int64    `json:"packets"`
}

// Group is the aggregate view of a multi-point capture: the correlated set
// of Sessions sharing one GroupID, plus a derived group-level status. A
// single-node capture is a Group of one.
type Group struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	StartedBy string    `json:"startedBy"`
	Sessions  []Session `json:"sessions"`
	Caps      Caps      `json:"caps"`
	StartedAt int64     `json:"startedAt"`
}

// StartRequest is the coordinator's Start input: a primary target plus any
// additional per-node targets (multi-point). Cap fields are *requests*,
// clamped down to the server ceilings — a zero value means "use the
// configured ceiling".
type StartRequest struct {
	TargetRef string
	Filter    string
	StartedBy string
	// PeerTargets names additional targets (typically on other nodes) to
	// capture the same logical flow at, correlated into one group.
	PeerTargets []string
	MaxBytes    int64
	MaxPackets  int64
	DurationSec int
}

// groupStatus derives a Group's overall status from its member sessions:
// running if any member is still running; otherwise the "most complete"
// terminal state, preferring completed/stopped over error/purged so a group
// where the capture actually ran reads as such.
func groupStatus(sessions []Session) Status {
	if len(sessions) == 0 {
		return StatusError
	}
	anyRunning := false
	anyCompleted := false
	anyStopped := false
	anyError := false
	for _, s := range sessions {
		switch s.Status {
		case StatusRunning:
			anyRunning = true
		case StatusCompleted:
			anyCompleted = true
		case StatusStopped:
			anyStopped = true
		case StatusError:
			anyError = true
		}
	}
	switch {
	case anyRunning:
		return StatusRunning
	case anyCompleted:
		return StatusCompleted
	case anyStopped:
		return StatusStopped
	case anyError:
		return StatusError
	default:
		return StatusPurged
	}
}
