package pvecassette

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// BodyRoot is the name reported findings are rooted at, so an error reads
// "body.data.ticket" rather than ".data.ticket".
const BodyRoot = "body"

// Writer writes cassettes into <dir>/<pveVersion>/.
//
// It is safe for concurrent use: the PVE client records from whatever
// goroutine made the call.
type Writer struct {
	log     *slog.Logger
	written map[string]string // Key -> file path

	dir        string
	pveVersion string

	mu sync.Mutex
}

// NewWriter builds a Writer. Both dir and pveVersion are required:
// recording into an unnamed version directory produces cassettes whose
// provenance cannot be recovered afterwards, which is the one thing a
// cassette has over a hand-written fixture.
func NewWriter(dir, pveVersion string, log *slog.Logger) (*Writer, error) {
	if dir == "" {
		return nil, fmt.Errorf("pvecassette: NewWriter: dir is required")
	}
	if pveVersion == "" {
		return nil, fmt.Errorf("pvecassette: NewWriter: pveVersion is required (a cassette that cannot say which PVE produced it is a guess with a timestamp)")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Writer{dir: dir, pveVersion: pveVersion, log: log, written: map[string]string{}}, nil
}

// Dir is the version directory cassettes land in.
func (w *Writer) Dir() string { return filepath.Join(w.dir, w.pveVersion) }

// Record builds a cassette from one observed exchange and writes it.
func (w *Writer) Record(method, path string, query map[string][]string, status int, body []byte) (string, error) {
	return w.Write(Cassette{
		RecordedAt: time.Now().UTC(),
		PVEVersion: w.pveVersion,
		Method:     method,
		Path:       path,
		Query:      normaliseQuery(query),
		Status:     status,
		Body:       string(body),
	})
}

// Write validates c, refuses it if its body carries a credential, and
// writes it to <dir>/<pveVersion>/<FileName>. It returns the file path.
//
// The refusal is the point of this function. It is not a warning, not a
// redaction, and not skippable by a flag: there is no code path through
// this package that writes a body a scan objected to. That is what makes
// "no cassette in this repository contains a credential" a property of the
// format rather than of the reviewer's attention.
func (w *Writer) Write(c Cassette) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if findings := redact.ScanJSON(BodyRoot, []byte(c.Body)); len(findings) > 0 {
		err := &SecretError{Method: c.Method, Path: c.Path, Findings: findings}
		// Logged as well as returned: in a recording session the caller
		// often surfaces the error at the top of a long run, and the
		// operator needs to know which of a hundred requests it was.
		w.log.Error("pvecassette: refusing to record a response containing a secret",
			"method", c.Method, "path", c.Path, "fields", err.Fields())
		return "", err
	}

	encoded, err := c.MarshalIndentJSON()
	if err != nil {
		return "", err
	}

	dir := w.Dir()
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", fmt.Errorf("pvecassette: creating cassette dir %s: %w", dir, mkErr)
	}
	dest := filepath.Join(dir, c.FileName())

	// Written via a temp file + rename so an interrupted recording session
	// leaves no half-written cassette to be loaded later as if it were
	// whole.
	tmp, err := os.CreateTemp(dir, ".cassette-*.tmp")
	if err != nil {
		return "", fmt.Errorf("pvecassette: creating temp cassette in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err = tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("pvecassette: writing cassette %s: %w", dest, err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("pvecassette: closing cassette %s: %w", dest, err)
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return "", fmt.Errorf("pvecassette: renaming cassette into %s: %w", dest, err)
	}
	// CreateTemp makes 0600 files; cassettes are checked-in test data that
	// every developer reads, and (by the guard above) contain no secret.
	if err = os.Chmod(dest, 0o644); err != nil {
		return "", fmt.Errorf("pvecassette: setting mode on cassette %s: %w", dest, err)
	}

	w.mu.Lock()
	w.written[c.Key()] = dest
	w.mu.Unlock()

	w.log.Debug("pvecassette: recorded", "method", c.Method, "path", c.Path, "status", c.Status, "file", dest)
	return dest, nil
}

// Written returns the cassette files this Writer has produced, sorted.
func (w *Writer) Written() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.written))
	for _, p := range w.written {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// normaliseQuery copies a query map, dropping the empty case so an absent
// query and an empty one are the same cassette.
func normaliseQuery(q map[string][]string) map[string][]string {
	if len(q) == 0 {
		return nil
	}
	out := make(map[string][]string, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}
