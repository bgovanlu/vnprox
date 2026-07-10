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
	interfacesCalls int
	lldpCalls       int
	statsCalls      int
}

func newSpyHostReader() *spyHostReader {
	return &spyHostReader{
		interfaces: map[string]string{},
		lldp:       map[string][]byte{},
		stats:      map[string]map[string]host.IfaceStats{},
	}
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

// spyHostWriter records every call it receives and its arguments.
type spyHostWriter struct {
	failNext error
	staged   map[string]string
	restored map[string]string
	reloaded []string
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
