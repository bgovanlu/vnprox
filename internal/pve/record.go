// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// Environment variables that turn record mode on. They are the documented
// operator flow (`make record`): recording is something a human does once,
// against a cluster this repository will never have access to, so the
// switch has to work on a build that shipped months earlier rather than
// being a flag in a config file only a developer knows about.
const (
	// EnvRecordDir names the directory cassettes are written under. Set it
	// and every request this client makes is recorded into
	// <dir>/<VNPROX_PVE_VERSION>/.
	EnvRecordDir = "VNPROX_PVE_RECORD"

	// EnvRecordPVEVersion names the PVE release being recorded, e.g.
	// "8.3.5". Required whenever EnvRecordDir is set: see newRecorder.
	EnvRecordPVEVersion = "VNPROX_PVE_VERSION"
)

// recorder is record mode: a hook on the client's raw transport that
// writes every observed request/response pair as a cassette.
//
// What it does NOT do is as important as what it does:
//
//   - It records the response body only. Request headers (which carry the
//     Authorization/PVEAPIToken value and the PVEAuthCookie) and request
//     bodies (which carry the password on a login) are never passed to the
//     writer at all, so no amount of getting the redactor wrong can leak
//     them.
//   - It does not swallow a failed write. If a cassette cannot be written
//     — most importantly because the response carried a credential — the
//     request itself fails with that error. Recording is an explicit,
//     temporary operator mode; a recording session that quietly skipped
//     the responses it could not handle would produce exactly the
//     partial, silently-incomplete fixture set this card exists to
//     replace.
//
// One consequence is worth stating plainly, because an operator will hit
// it within seconds: **a ticket-auth client cannot record.** Its very
// first call is POST /access/ticket, whose response body is a PVE ticket,
// and the writer refuses that body by design. Record with the API-token
// identity (AuthAPIToken) instead — the same identity the daemon polls
// with. The refusal names the field, so the failure explains itself.
type recorder struct {
	w *pvecassette.Writer
}

// newRecorder returns the client's recorder, or nil when record mode is
// off. cfg's explicit fields win over the environment so tests can drive
// record mode without mutating process state.
func newRecorder(cfg Config, log *slog.Logger) (*recorder, error) {
	dir := cfg.RecordDir
	if dir == "" {
		dir = os.Getenv(EnvRecordDir)
	}
	if dir == "" {
		return nil, nil //nolint:nilnil // "no recorder, no error" is the off state, and a sentinel type for it would be read at exactly one call site
	}

	version := cfg.RecordPVEVersion
	if version == "" {
		version = os.Getenv(EnvRecordPVEVersion)
	}
	if version == "" {
		// Refusing here rather than defaulting to "unknown" is deliberate.
		// The one thing a cassette has over a hand-written fixture is that
		// it can say which Proxmox produced it; a directory full of
		// cassettes under `unknown/` has thrown that away and cannot get
		// it back, because by then nobody remembers which cluster was
		// plugged in that afternoon.
		return nil, fmt.Errorf("pve: %s is set but %s is not: a cassette must record which PVE release produced it (e.g. %s=8.3.5)",
			EnvRecordDir, EnvRecordPVEVersion, EnvRecordPVEVersion)
	}

	w, err := pvecassette.NewWriter(dir, version, log)
	if err != nil {
		return nil, fmt.Errorf("pve: enabling record mode: %w", err)
	}
	log.Info("pve: record mode enabled", "dir", w.Dir(), "pveVersion", version)
	return &recorder{w: w}, nil
}

// record writes one exchange. req is read for its method, path and query
// only.
func (r *recorder) record(req *http.Request, status int, body []byte) error {
	if _, err := r.w.Record(req.Method, req.URL.Path, req.URL.Query(), status, body); err != nil {
		return err
	}
	return nil
}

// Recorded returns the cassette files this client has written, sorted. It
// is empty when record mode is off — which is how `make record` reports
// "you recorded nothing" rather than exiting 0 on an empty directory.
func (c *Client) Recorded() []string {
	if c.rec == nil {
		return nil
	}
	return c.rec.w.Written()
}
