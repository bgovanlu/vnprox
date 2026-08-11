package incident

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// harness_test.go wires a Service against the REAL repositories on a real
// (temporary) SQLite file.
//
// In-memory fakes would have been less code, and would also have made the
// acceptance criteria assert my own fakes' time filtering rather than the
// queries that actually run in production. AC1 in particular — a retroactive
// incident contains what a live one did — is a statement about `WHERE at
// BETWEEN ? AND ?` behaving identically for both, so it is worth running
// against SQLite.
//
// The counting fakes live in ac2_test.go instead, where "how many times was a
// source called" is the property under test and a real repo would answer a
// different question.

//nolint:govet // fieldalignment: a test harness read top-to-bottom by humans.
type harness struct {
	t        *testing.T
	svc      *Service
	db       *store.DB
	dbPath   string
	findings *store.FindingEventRepo
	audit    *store.AuditRepo
	captures *store.CaptureRepo
	flows    *store.FlowSampleRepo
	diff     *fakeDiff
	now      atomic.Int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	h := &harness{
		t:        t,
		db:       db,
		dbPath:   dbPath,
		findings: store.NewFindingEventRepo(db),
		audit:    store.NewAuditRepo(db),
		captures: store.NewCaptureRepo(db),
		flows:    store.NewFlowSampleRepo(db),
		diff:     &fakeDiff{},
	}
	h.now.Store(1_700_000_000)
	h.svc = New(Config{
		Store:         store.NewIncidentRepo(db),
		FindingEvents: h.findings,
		Audit:         h.audit,
		Captures:      h.captures,
		Flows:         h.flows,
		Diff:          h.diff,
		Now:           func() time.Time { return time.Unix(h.now.Load(), 0) },
	})
	return h
}

func (h *harness) setNow(at int64) { h.now.Store(at) }

// --- seeding ---------------------------------------------------------------

func (h *harness) seedFinding(at int64, id, transition string) {
	h.t.Helper()
	if err := h.findings.Insert(context.Background(),
		store.FindingEvent{FindingID: id, At: at, Transition: transition}); err != nil {
		h.t.Fatalf("seeding finding event: %v", err)
	}
}

func (h *harness) seedAudit(at int64, action, target, changesetID, result string) {
	h.t.Helper()
	e := store.AuditEntry{
		At: at, Username: "brian@pam", Action: action, Result: result,
		Target: sql.NullString{String: target, Valid: target != ""},
	}
	if changesetID != "" {
		e.ChangesetID = sql.NullString{String: changesetID, Valid: true}
	}
	if _, err := h.audit.Append(context.Background(), e); err != nil {
		h.t.Fatalf("seeding audit row: %v", err)
	}
}

func (h *harness) seedCapture(id string, startedAt, stoppedAt int64, status string) {
	h.t.Helper()
	if err := h.captures.Upsert(context.Background(), store.CaptureSession{
		ID: id, GroupID: id, TargetRef: "bridge:pve1:vmbr0", Node: "pve1",
		Status: status, StartedBy: "brian@pam", StartedAt: startedAt, StoppedAt: stoppedAt,
		Packets: 12, Nodes: []string{"pve1"},
	}); err != nil {
		h.t.Fatalf("seeding capture session: %v", err)
	}
}

func (h *harness) seedFlow(at int64, srcIP, dstIP string) {
	h.t.Helper()
	if err := h.flows.InsertBatch(context.Background(), []store.FlowSample{{
		At: at, Node: "pve1", SrcIP: srcIP, DstIP: dstIP, SrcPort: 51820, DstPort: 443,
		Proto: 6, Bytes: 1200, Packets: 3, Source: "netflow9",
	}}); err != nil {
		h.t.Fatalf("seeding flow sample: %v", err)
	}
}

// seedInterleavedHistory writes one event per source, INTERLEAVED in time
// rather than in same-source runs — T-2804 acceptance criterion 4 asks for
// exactly that, because a timeline that sorts each source's block correctly
// but concatenates the blocks would pass a same-source-run fixture.
//
// The returned slice is the expected source order, oldest first.
func (h *harness) seedInterleavedHistory() []Source {
	h.t.Helper()
	h.seedFinding(1000, "health:carrier_down|iface:pve1:eno1", "new")
	h.seedAudit(1010, "changeset.create", "bridge:pve1:vmbr0", "cs-1", "success")
	h.seedAudit(1020, "diagnose.run", "guest-nic:pve1:101/net0", "", "ok")
	h.seedCapture("cap-1", 1030, 1070, "stopped")
	h.seedFlow(1040, "10.0.0.5", "10.0.0.9")
	h.seedFinding(1050, "health:carrier_down|iface:pve1:eno1", "resolved")
	h.seedAudit(1060, "changeset.apply", "bridge:pve1:vmbr0", "cs-1", "success")
	// 1070: the capture stop seeded above.
	h.seedFlow(1080, "10.0.0.9", "10.0.0.5")
	h.seedAudit(1090, "diagnose.run", "guest-nic:pve1:101/net0", "", "ok")

	return []Source{
		SourceFinding,   // 1000
		SourceChangeset, // 1010
		SourceDiagnosis, // 1020
		SourceCapture,   // 1030
		SourceFlow,      // 1040
		SourceFinding,   // 1050
		SourceChangeset, // 1060
		SourceCapture,   // 1070
		SourceFlow,      // 1080
		SourceDiagnosis, // 1090
	}
}

// --- fake diff -------------------------------------------------------------

// fakeDiff stands in for change.Service's TopologyDiff. It records its
// arguments (the window the timeline asked about) and returns whatever the
// test set — including a typed refusal, which is the case that matters most.
//
//nolint:govet // fieldalignment: a test double; readability beats packing.
type fakeDiff struct {
	calls  atomic.Int32
	from   string
	to     string
	result *change.TopologyDiff
	err    error
}

func (f *fakeDiff) TopologyDiff(_ context.Context, from, to string) (*change.TopologyDiff, error) {
	f.calls.Add(1)
	f.from, f.to = from, to
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &change.TopologyDiff{
		Added: []topology.EntityDiff{}, Removed: []topology.EntityDiff{}, Modified: []topology.EntityDiff{},
		Coverage: change.DiffCoverage{Nodes: []string{"pve1"}, Paths: []string{"/etc/network/interfaces"}},
	}, nil
}

// --- assertions ------------------------------------------------------------

func sourcesOf(events []Event) []Source {
	out := make([]Source, 0, len(events))
	for _, e := range events {
		out = append(out, e.Source)
	}
	return out
}

func machineEvents(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Source != SourceAnnotation {
			out = append(out, e)
		}
	}
	return out
}
