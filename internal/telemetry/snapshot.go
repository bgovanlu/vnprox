// SPDX-License-Identifier: Apache-2.0

package telemetry

// snapshot.go holds the card's primary trust surface: `telemetry preview`
// prints the exact bytes that would be sent.
//
// "Exact" is a structural claim here, not a habit. A Snapshot is the ONE
// marshalling of a Payload that exists; Preview writes snapshot.raw and
// Submit posts snapshot.raw. Neither re-encodes, neither wraps, and there
// is no second `json.Marshal` of a Payload anywhere in this package — a
// test reads this package's own source and fails if one appears, because
// "they call the same function today" is a property that lasts until
// somebody adds a field, a header or an indent to one of the two paths.
//
// An operator who runs preview is being promised that this is what leaves
// their cluster. The only way to keep that promise is for there to be
// nothing else to leave.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// DefaultTimeout bounds one submission attempt end to end.
const DefaultTimeout = 10 * time.Second

// maxResponseBytes bounds how much of a collector's response is read. A
// collector is a stranger; nothing it says is trusted, and its reply is
// only read at all so an error can quote it.
const maxResponseBytes = 4096

// ContentType is the request's content type.
const ContentType = "application/json"

// ErrDisabled is returned by Submit when telemetry is off. It is a distinct
// error rather than a silent no-op so `telemetry send` can say "this is off"
// instead of pretending it sent something.
var ErrDisabled = errors.New("telemetry is disabled ([telemetry] enabled = false); nothing was sent and no endpoint was contacted")

// ErrNoEndpoint is returned when telemetry is enabled but no collector was
// named. vnprox ships no default endpoint.
var ErrNoEndpoint = errors.New("telemetry is enabled but [telemetry] endpoint is empty; vnprox ships no default collector")

// Snapshot is a built, guarded payload and the exact bytes that represent
// it. Construct it with Build.
type Snapshot struct {
	payload Payload
	raw     []byte
	known   []Known
}

// Build reduces a report, marshals it exactly once, and guards the result.
//
// It returns an error rather than a Snapshot with a warning attached: there
// is no such thing as a payload that is "mostly" safe to send, so a
// violation ends the operation here, before any endpoint, store or network
// is involved.
//
// extraKnown are identifiers the CALLER knows the payload must not contain
// and the report cannot supply — the cluster's name, guest names. See
// KnownFromReport for why those two classes have to be passed in.
func Build(rep verify.Report, installID string, extraKnown ...Known) (*Snapshot, error) {
	if err := rep.Validate(); err != nil {
		// A malformed report is not something to reduce and send; it is
		// something to fix. Reducing it would produce a payload whose
		// verdict counts nobody can reproduce.
		return nil, fmt.Errorf("this verify report is not valid: %w", err)
	}
	if rep.Environment.Mock {
		return nil, fmt.Errorf("%w (%s)", ErrMockReport, rep.Environment.MockReason)
	}

	payload := Reduce(rep, installID)
	raw, err := marshalPayload(payload)
	if err != nil {
		return nil, err
	}
	known := append(KnownFromReport(rep), extraKnown...)
	if err := Guard(raw, known); err != nil {
		return nil, err
	}
	return &Snapshot{payload: payload, raw: raw, known: known}, nil
}

// Preview writes the snapshot's bytes — the same slice Submit posts — to w.
func (s *Snapshot) Preview(w io.Writer) error {
	if _, err := w.Write(s.Bytes()); err != nil {
		return fmt.Errorf("writing the telemetry preview: %w", err)
	}
	return nil
}

// Bytes is the payload as it would be transmitted.
//
// It deliberately returns the snapshot's own slice rather than a copy: the
// identity of this buffer IS the guarantee — preview and send are the same
// bytes because they are the same allocation, which a test asserts by
// comparing element addresses, not just contents.
func (s *Snapshot) Bytes() []byte { return s.raw }

// Payload is the decoded value, for callers that want to report on it
// (`telemetry preview --summary`) without re-parsing the bytes.
func (s *Snapshot) Payload() Payload { return s.payload }

// Destination is where a payload may go, and whether it may go at all.
//
// Transport leads and Endpoint follows: govet's fieldalignment, which wants
// the pointer-bearing prefix as short as possible.
type Destination struct {
	// Transport is the HTTP transport. Nil means http.DefaultTransport.
	// Injected so tests can supply one that fails if it is ever called —
	// which is the only way to assert "nothing was sent" without trusting
	// the code under test to have read its own config correctly.
	Transport http.RoundTripper
	// Endpoint is the collector URL. Required when Enabled.
	Endpoint string
	// Timeout bounds one attempt. Zero means DefaultTimeout.
	Timeout time.Duration
	// Enabled is the master switch, mapped from [telemetry] enabled.
	Enabled bool
}

// Submit sends the snapshot, synchronously.
//
// Order matters and is the acceptance criteria in code:
//
//  1. Disabled → return ErrDisabled having touched nothing. Not "build a
//     request and skip it", not "call a no-op transport": the function
//     returns before an http.Client exists.
//  2. No endpoint → ErrNoEndpoint, same.
//  3. Re-guard the bytes. Build already guarded them, and this runs again
//     anyway, because "checked at construction" is a claim about the
//     constructor and this is a claim about the send.
//  4. Only then is a request built.
func Submit(ctx context.Context, dst Destination, snap *Snapshot) error {
	if !dst.Enabled {
		return ErrDisabled
	}
	if dst.Endpoint == "" {
		return ErrNoEndpoint
	}
	if snap == nil || len(snap.raw) == 0 {
		return errors.New("telemetry: nothing to send")
	}
	if err := Guard(snap.raw, snap.known); err != nil {
		return fmt.Errorf("refusing to send: %w", err)
	}

	timeout := dst.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dst.Endpoint, bytes.NewReader(snap.Bytes()))
	if err != nil {
		return fmt.Errorf("building the telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)
	req.ContentLength = int64(len(snap.Bytes()))

	client := &http.Client{Transport: dst.Transport, Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending the compatibility report to %s: %w", dst.Endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the collector at %s answered %s: %s", dst.Endpoint, resp.Status, bytes.TrimSpace(body))
	}
	return nil
}

// Start submits in the background and returns immediately.
//
// This is what `vnproxctl verify` uses, and the reason it exists is AC5: a
// send must never block or delay a verify run. It does not wait, it does not
// wait "briefly", and it has no drain step — a collector that accepts the
// connection and then hangs forever costs the operator nothing, because
// nobody is waiting on it.
//
// The consequence is stated plainly rather than hidden: an in-flight send
// that has not finished when the process exits is abandoned. Delivery from
// `verify` is therefore best-effort. The reliable path is the operator's own
// foreground `vnproxctl telemetry send`, which waits and reports what
// happened; a compatibility data point that does not arrive costs nobody
// anything, and a verify run that hangs on a stranger's server costs the
// operator the terminal they were using.
func Start(ctx context.Context, dst Destination, snap *Snapshot, logger *slog.Logger) <-chan error {
	done := make(chan error, 1)
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	go func() {
		err := Submit(ctx, dst, snap)
		switch {
		case err == nil:
			logger.Info("telemetry: compatibility report sent", "endpoint", dst.Endpoint, "bytes", len(snap.Bytes()))
		case errors.Is(err, ErrDisabled), errors.Is(err, ErrNoEndpoint):
			logger.Debug("telemetry: not sending", "reason", err.Error())
		default:
			// Non-fatal, always. Nothing about a verify run's verdict,
			// output or exit code depends on this.
			logger.Warn("telemetry: sending the compatibility report failed (this does not affect the verify run)", "error", err.Error())
		}
		done <- err
		close(done)
	}()
	return done
}

// marshalPayload is the ONE place a Payload becomes bytes. Everything that
// prints, previews, hashes or sends a payload goes through the Snapshot this
// produces. Keep it that way: snapshot_test.go fails the build if a second
// json.Marshal of anything appears in this package's non-test source.
func marshalPayload(p Payload) ([]byte, error) {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the telemetry payload: %w", err)
	}
	// A trailing newline so `telemetry preview` does not leave the cursor
	// mid-line. It is part of the buffer, so it is also part of what is
	// posted — preview and send stay byte-identical rather than differing
	// by "just" a newline, which is exactly the kind of small divergence
	// that makes an operator's diff of the two meaningless.
	return append(raw, '\n'), nil
}
