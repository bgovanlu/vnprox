package peer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
)

// testSecret is a fixed, valid-length secret used throughout this
// package's tests wherever the exact bytes don't matter.
var testSecret = bytes.Repeat([]byte{0x42}, secretLen)

// newStaticSecretStore builds a *SecretStore around secret without touching
// disk, for tests that only care about the signing/verification behavior,
// not file loading itself (secret_test.go covers file loading directly).
func newStaticSecretStore(secret []byte) *SecretStore {
	return &SecretStore{secret: secret, path: "<static, no file backing>"}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixedClock returns a func() time.Time that always reports t, plus a
// setter for tests that need to move the clock forward mid-test.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// spyHostReader records every call it receives (so tests can assert a
// rejected request never reached the handler layer at all) and serves
// canned per-node fixture data.
type spyHostReader struct {
	interfaces      map[string]string
	lldp            map[string][]byte
	stats           map[string]map[string]host.IfaceStats
	links           map[string][]host.LinkState
	bgpSummary      map[string][]byte
	evpnVNI         map[string][]byte
	dhcpLeases      map[string][]byte
	neighbors       map[string][]host.Neighbor
	interfacesCalls int
	lldpCalls       int
	statsCalls      int
	linksCalls      int
	bgpSummaryCalls int
	evpnVNICalls    int
	dhcpLeasesCalls int
	neighborsCalls  int
}

func newSpyHostReader() *spyHostReader {
	return &spyHostReader{
		interfaces: map[string]string{},
		lldp:       map[string][]byte{},
		stats:      map[string]map[string]host.IfaceStats{},
		links:      map[string][]host.LinkState{},
		bgpSummary: map[string][]byte{},
		evpnVNI:    map[string][]byte{},
		dhcpLeases: map[string][]byte{},
		neighbors:  map[string][]host.Neighbor{},
	}
}

func (r *spyHostReader) Links(_ context.Context, node string) ([]host.LinkState, error) {
	r.linksCalls++
	l, ok := r.links[node]
	if !ok {
		return nil, errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	return l, nil
}

func (r *spyHostReader) InterfacesFile(_ context.Context, node string, _ bool) (string, error) {
	r.interfacesCalls++
	content, ok := r.interfaces[node]
	if !ok {
		return "", errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	return content, nil
}

func (r *spyHostReader) LLDP(_ context.Context, node string) ([]byte, error) {
	r.lldpCalls++
	data, ok := r.lldp[node]
	if !ok {
		return nil, errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	return data, nil
}

func (r *spyHostReader) Stats(_ context.Context, node string) (map[string]host.IfaceStats, error) {
	r.statsCalls++
	s, ok := r.stats[node]
	if !ok {
		return nil, errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	return s, nil
}

func (r *spyHostReader) FRRBGPSummary(_ context.Context, node string) ([]byte, error) {
	r.bgpSummaryCalls++
	b, ok := r.bgpSummary[node]
	if !ok {
		return nil, errors.Join(host.ErrFRRUnavailable, errors.New("node "+node))
	}
	return b, nil
}

func (r *spyHostReader) FRREVPNVNI(_ context.Context, node string) ([]byte, error) {
	r.evpnVNICalls++
	b, ok := r.evpnVNI[node]
	if !ok {
		return nil, errors.Join(host.ErrFRRUnavailable, errors.New("node "+node))
	}
	return b, nil
}

func (r *spyHostReader) DHCPLeases(_ context.Context, node string) ([]byte, error) {
	r.dhcpLeasesCalls++
	// Unlike Links/InterfacesFile/etc., an unconfigured node is simply "no
	// leases" (empty, nil error), not host.ErrNotFound -- matching
	// DHCPLeases' doc comment: absent/empty is the common, non-error case.
	return r.dhcpLeases[node], nil
}

func (r *spyHostReader) Services(_ context.Context, node string) (map[string]bool, error) {
	return nil, nil
}

func (r *spyHostReader) Neighbors(_ context.Context, node string) ([]host.Neighbor, error) {
	r.neighborsCalls++
	n, ok := r.neighbors[node]
	if !ok {
		return nil, errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	return n, nil
}

func (r *spyHostReader) CorosyncStatus(_ context.Context, node string) ([]byte, error) {
	return nil, host.ErrCorosyncUnavailable
}

// spyHostWriter records every call it receives and its arguments.
type spyHostWriter struct {
	failNext  error
	staged    map[string]string
	restored  map[string]string
	reloaded  []string
	discarded []string
}

func newSpyHostWriter() *spyHostWriter {
	return &spyHostWriter{staged: map[string]string{}, restored: map[string]string{}}
}

func (w *spyHostWriter) StageInterfaces(_ context.Context, node, content string) error {
	if w.failNext != nil {
		err := w.failNext
		w.failNext = nil
		return err
	}
	w.staged[node] = content
	return nil
}

func (w *spyHostWriter) ReloadInterfaces(_ context.Context, node string) error {
	if w.failNext != nil {
		err := w.failNext
		w.failNext = nil
		return err
	}
	w.reloaded = append(w.reloaded, node)
	return nil
}

func (w *spyHostWriter) RestoreInterfaces(_ context.Context, node, content string) error {
	if w.failNext != nil {
		err := w.failNext
		w.failNext = nil
		return err
	}
	w.restored[node] = content
	return nil
}

func (w *spyHostWriter) DiscardStaged(_ context.Context, node string) error {
	if w.failNext != nil {
		err := w.failNext
		w.failNext = nil
		return err
	}
	w.discarded = append(w.discarded, node)
	return nil
}

func newTestServer(t *testing.T, now func() time.Time) (*Server, *spyHostReader, *spyHostWriter) {
	t.Helper()
	reader := newSpyHostReader()
	writer := newSpyHostWriter()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Reader:  reader,
		Writer:  writer,
		Version: "test",
		Logger:  discardLogger(),
		Now:     now,
	})
	return srv, reader, writer
}

// spyAuditReader is an in-memory AuditReader stand-in for T-303's peer
// audit fan-out tests: pages is served verbatim (already paginated by the
// caller's fixture setup), applyFilter records what filter this Server
// received so tests can assert query params were parsed and forwarded
// correctly.
type spyAuditReader struct {
	err        error
	pages      map[string]auditPageResponse
	lastFilter AuditFilter
	lastLimit  int
}

func newSpyAuditReader() *spyAuditReader {
	return &spyAuditReader{pages: map[string]auditPageResponse{}}
}

func (r *spyAuditReader) ListAuditPage(_ context.Context, filter AuditFilter, cursor string, limit int) ([]AuditRecord, string, error) {
	r.lastFilter = filter
	r.lastLimit = limit
	if r.err != nil {
		return nil, "", r.err
	}
	page := r.pages[cursor]
	return page.Items, page.NextCursor, nil
}

// spySnapshotReader is the snapshot-list analogue of spyAuditReader.
type spySnapshotReader struct {
	err       error
	pages     map[string]snapshotPageResponse
	lastLimit int
}

func newSpySnapshotReader() *spySnapshotReader {
	return &spySnapshotReader{pages: map[string]snapshotPageResponse{}}
}

func (r *spySnapshotReader) ListSnapshotPage(_ context.Context, cursor string, limit int) ([]SnapshotRecord, string, error) {
	r.lastLimit = limit
	if r.err != nil {
		return nil, "", r.err
	}
	page := r.pages[cursor]
	return page.Items, page.NextCursor, nil
}

// newTestServerFull is newTestServer plus T-303's Audit/Snapshots reader
// seams wired to spies, for tests that exercise GET /api/peer/{audit,snapshots}.
func newTestServerFull(t *testing.T, now func() time.Time) (*Server, *spyHostReader, *spyHostWriter, *spyAuditReader, *spySnapshotReader) {
	t.Helper()
	reader := newSpyHostReader()
	writer := newSpyHostWriter()
	auditR := newSpyAuditReader()
	snapR := newSpySnapshotReader()
	srv := NewServer(ServerOptions{
		Secrets:   newStaticSecretStore(testSecret),
		Reader:    reader,
		Writer:    writer,
		Audit:     auditR,
		Snapshots: snapR,
		Version:   "test",
		Logger:    discardLogger(),
		Now:       now,
	})
	return srv, reader, writer, auditR, snapR
}

// spyTimerAgent is an in-memory TimerAgent test double: no real timers, just
// the state transitions arm/cancel/status would produce, so server_test.go
// and client_test.go can exercise the /api/peer/timer/* routes without
// pulling in internal/change's real LocalTimerAgent (which would be a
// package-boundary violation — see HostWriter's doc comment on the intended
// dependency direction).
type spyTimerAgent struct {
	records map[string]TimerRecord // key: changesetID+"/"+node
}

func newSpyTimerAgent() *spyTimerAgent {
	return &spyTimerAgent{records: map[string]TimerRecord{}}
}

func timerKey(changesetID, node string) string { return changesetID + "/" + node }

func (a *spyTimerAgent) ArmTimer(_ context.Context, changesetID, node, _ string, deadline int64) (TimerRecord, error) {
	rec := TimerRecord{ChangesetID: changesetID, Node: node, Status: TimerArmed, Deadline: deadline, ArmedAt: 1}
	a.records[timerKey(changesetID, node)] = rec
	return rec, nil
}

func (a *spyTimerAgent) CancelTimer(_ context.Context, changesetID, node string) (TimerRecord, error) {
	rec, ok := a.records[timerKey(changesetID, node)]
	if !ok {
		return TimerRecord{}, ErrTimerNotFound
	}
	rec.Status = TimerCancelled
	rec.ResolvedAt = 2
	a.records[timerKey(changesetID, node)] = rec
	return rec, nil
}

func (a *spyTimerAgent) TimerStatus(_ context.Context, changesetID, node string) (TimerRecord, error) {
	rec, ok := a.records[timerKey(changesetID, node)]
	if !ok {
		return TimerRecord{}, ErrTimerNotFound
	}
	return rec, nil
}
