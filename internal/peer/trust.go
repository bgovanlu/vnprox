package peer

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// DefaultClusterCAPath is the trust anchor peer-API TLS pins by default: the
// PVE cluster's own root CA. Real peer daemons serve the node's PVE
// certificate (docs/architecture.md §9 — vnproxd reuses
// /etc/pve/local/pve-ssl.pem so the browser trust story matches PVE's), which
// is issued by this CA. Pinning it is what makes "a certificate from any
// publicly-trusted CA plus a position on the management network" insufficient
// to impersonate a peer daemon (T-1906).
const DefaultClusterCAPath = "/etc/pve/pve-root-ca.pem"

// DefaultTrustReloadInterval bounds how stale a rotated cluster CA can be
// before the peer client picks it up. PVE's cluster CA is long-lived but does
// get regenerated (`pvecm updatecerts -f`, a cluster rebuild); a pin that
// required a daemon restart to follow that rotation is a pin operators would
// switch off, so the anchor is re-read on this cadence instead (T-1906 AC4).
// It is deliberately short relative to a CA's lifetime and cheap: one ~2 KiB
// file read, only on the first peer request after the interval elapses.
const DefaultTrustReloadInterval = 30 * time.Second

// TrustMode selects how peer-API TLS server certificates are verified.
//
// The zero value ("") means TrustClusterCA. That is load-bearing: every
// degraded posture in this type must be an explicit, spelled-out, acknowledged
// configuration value, never something a missing field, an unset environment
// variable, or an absent file can produce (T-1906 AC3). A silent fallback to
// the system pool would reproduce the exact vulnerability this type exists to
// close while appearing to close it.
type TrustMode string

const (
	// TrustClusterCA pins TrustOptions.CAFile (default DefaultClusterCAPath)
	// as the *sole* trust anchor for peer TLS. The system certificate pool is
	// not consulted at all. This is the default and the only production mode.
	TrustClusterCA TrustMode = "cluster-ca"

	// TrustSystem trusts the host's system certificate pool instead of the
	// cluster CA — the pre-T-1906 posture, kept only for a vnproxd running
	// somewhere that genuinely has no /etc/pve (a dev box, a non-PVE host,
	// a peer fronted by a corporate CA). Any certificate the host trusts is
	// then accepted as a peer daemon's. Requires AckSystem.
	TrustSystem TrustMode = "system"

	// TrustInsecure performs no server-certificate verification whatsoever.
	// For local development against a throwaway self-signed certificate only.
	// Requires AckInsecure.
	TrustInsecure TrustMode = "insecure"

	// TrustExternal is never configurable — ParseTrustMode rejects it. It is
	// only ever *reported* (Client.TrustReport) for a Client built with a
	// caller-supplied ClientOptions.HTTPClient, whose trust decision this
	// package cannot see and therefore will not vouch for.
	TrustExternal TrustMode = "external"
)

// The acknowledgement literals a non-default TrustMode requires alongside it
// ([peer] tls_trust_ack). Two properties are intended, and both are tested:
//
//   - Neither value is guessable-by-accident, a typo of anything, or a boolean
//     that could be flipped by a careless edit. Enabling an escape hatch takes
//     two independent, deliberate edits that each name what is being given up.
//   - The literals differ *per mode*, so a config that legitimately runs in
//     "system" mode cannot be turned into an unverified one by editing the mode
//     line alone — the ack stops matching and the daemon refuses to start.
//     (The same two-key interlock shape [switches] uses for switch push.)
const (
	AckSystem   = "i-accept-unpinned-peer-tls"
	AckInsecure = "i-accept-unverified-peer-tls"
)

// ErrTrustAnchorUnavailable reports that the pinned cluster CA could not be
// read or contained no certificate, so no peer can be authenticated at all.
// It is always wrapped together with ErrPeerUntrusted: the caller-visible
// outcome is "this peer could not be verified", and the message names the
// local cause so an operator can tell a missing /etc/pve mount from an attack.
var ErrTrustAnchorUnavailable = errors.New("cluster CA trust anchor unavailable")

// ErrTrustNotAcknowledged reports a non-default TrustMode configured without
// its exact acknowledgement literal. Construction fails; it never degrades to
// either the requested mode or the secure one, because both outcomes would be
// silent — an operator who wrote tls_trust and got something else needs to be
// told, and an operator who typo'd the ack needs to be told too.
var ErrTrustNotAcknowledged = errors.New("peer: TLS trust mode not acknowledged")

// ErrUnknownTrustMode reports an unrecognized [peer] tls_trust value.
var ErrUnknownTrustMode = errors.New("peer: unknown TLS trust mode")

// ParseTrustMode maps a config string onto a TrustMode. The empty string is
// TrustClusterCA (the secure default); anything unrecognized is an error, not
// a fallback.
func ParseTrustMode(s string) (TrustMode, error) {
	switch TrustMode(s) {
	case "", TrustClusterCA:
		return TrustClusterCA, nil
	case TrustSystem:
		return TrustSystem, nil
	case TrustInsecure:
		return TrustInsecure, nil
	default:
		return "", fmt.Errorf("%w %q (want %q, %q, or %q)", ErrUnknownTrustMode, s, TrustClusterCA, TrustSystem, TrustInsecure)
	}
}

// Pinned reports whether m verifies peers against the cluster's own CA rather
// than an inherited or absent trust store.
func (m TrustMode) Pinned() bool { return m == "" || m == TrustClusterCA }

// requiredAck is the exact tls_trust_ack literal m demands, or "" when m needs
// none.
func (m TrustMode) requiredAck() string {
	switch m {
	case TrustSystem:
		return AckSystem
	case TrustInsecure:
		return AckInsecure
	default:
		return ""
	}
}

// TrustOptions configures a Trust.
type TrustOptions struct {
	// Logger receives the startup banner (see NewTrust) and every subsequent
	// trust-anchor state transition. Nil means slog.Default().
	Logger *slog.Logger
	// Now is the clock used for the reload cadence; nil means time.Now.
	Now func() time.Time
	// systemPool overrides how TrustSystem obtains the host trust store.
	// Unexported on purpose: it exists so this package's own tests can put a
	// certificate authority into "the trust store this client would otherwise
	// inherit" deterministically, and must never become a configurable way to
	// substitute a trust store from outside the package.
	systemPool func() (*x509.CertPool, error)
	// Mode selects verification behaviour. Zero value is TrustClusterCA.
	Mode TrustMode
	// CAFile is the pinned trust anchor for TrustClusterCA. Empty means
	// DefaultClusterCAPath.
	CAFile string
	// Ack must equal Mode.requiredAck() for any non-default Mode.
	Ack string
	// ReloadInterval bounds how long a rotated CA file goes unnoticed. Zero
	// means DefaultTrustReloadInterval.
	ReloadInterval time.Duration
}

// Trust is the peer client's TLS trust anchor: an http.RoundTripper that
// verifies every https peer connection against the pinned cluster CA, re-reads
// that CA on a bounded cadence so a rotation needs no daemon restart, and
// fails closed (returning an ErrPeerUntrusted-wrapped error before any bytes
// are sent) when it has no anchor to verify against.
//
// Safe for concurrent use; one Trust is shared by every peer.Client in a
// daemon so there is exactly one trust decision, one file read cadence, and
// one startup banner per process.
type Trust struct {
	now         func() time.Time
	systemPool  func() (*x509.CertPool, error)
	log         *slog.Logger
	rt          *http.Transport
	loadErr     error
	caFile      string
	fingerprint string
	lastLogged  string
	mode        TrustMode
	// checkedAt is the last anchor evaluation, as unix nanoseconds rather than
	// a time.Time purely so this struct carries no extra pointer word.
	checkedAt      int64
	reloadInterval time.Duration
	mu             sync.Mutex
	checked        bool
}

// NewTrust validates opts and builds a Trust, emitting the startup banner:
// an informational line naming the pinned anchor in the default mode, or a
// WARN naming exactly what has been given up in either escape-hatch mode.
//
// The WARN is unconditional — it is emitted on **every** construction, not
// only the first, and there is no acknowledgement, state file, or counter that
// can silence it (T-1906 AC3). A cluster running with unpinned peer TLS says
// so in its log on every single start.
func NewTrust(opts TrustOptions) (*Trust, error) {
	mode := opts.Mode
	if mode == "" {
		mode = TrustClusterCA
	}
	if _, err := ParseTrustMode(string(mode)); err != nil {
		return nil, err
	}
	if want := mode.requiredAck(); want != "" && opts.Ack != want {
		return nil, fmt.Errorf("%w: [peer] tls_trust = %q requires [peer] tls_trust_ack = %q (got %q)", ErrTrustNotAcknowledged, mode, want, opts.Ack)
	}

	t := newTrust(mode, opts)

	switch mode {
	case TrustSystem:
		t.log.Warn("peer: CLUSTER CA PINNING IS DISABLED for the peer API — peer daemons are authenticated against the host system trust store, so any certificate from any publicly-trusted CA is accepted as a peer. The peer API carries cluster-wide network mutations. This is a configured escape hatch ([peer] tls_trust), not a default",
			"mode", string(mode), "expected_mode", string(TrustClusterCA), "expected_ca_file", DefaultClusterCAPath)
	case TrustInsecure:
		t.log.Warn("peer: PEER-API TLS VERIFICATION IS DISABLED — peer certificates are not checked at all, so any host able to answer on the peer port impersonates a peer daemon and can drive cluster-wide network mutations. This is a configured escape hatch ([peer] tls_trust) for local development only",
			"mode", string(mode), "expected_mode", string(TrustClusterCA), "expected_ca_file", DefaultClusterCAPath)
	default:
		t.log.Info("peer: pinning the cluster CA for peer-API TLS", "mode", string(mode), "ca_file", t.caFile)
	}
	return t, nil
}

// newPinnedTrust builds the implicit default a Client falls back to when its
// caller supplied neither an HTTPClient nor a Trust: pinned to
// DefaultClusterCAPath, exactly like an explicitly configured production
// daemon. It logs at debug rather than repeating NewTrust's banner, since a
// process is expected to construct its Trust once (cmd/vnproxd does) and share
// it — but a caller that forgets still inherits the *pinned* posture, never
// the system pool.
func newPinnedTrust(logger *slog.Logger) *Trust {
	t := newTrust(TrustClusterCA, TrustOptions{Logger: logger})
	t.log.Debug("peer: no explicit trust anchor configured; defaulting to the pinned cluster CA", "ca_file", t.caFile)
	return t
}

func newTrust(mode TrustMode, opts TrustOptions) *Trust {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	interval := opts.ReloadInterval
	if interval <= 0 {
		interval = DefaultTrustReloadInterval
	}
	caFile := opts.CAFile
	if caFile == "" {
		caFile = DefaultClusterCAPath
	}
	sysPool := opts.systemPool
	if sysPool == nil {
		sysPool = func() (*x509.CertPool, error) { return nil, nil }
	}
	return &Trust{
		mode:           mode,
		caFile:         caFile,
		reloadInterval: interval,
		log:            logger,
		now:            now,
		systemPool:     sysPool,
	}
}

// Mode reports the configured verification mode.
func (t *Trust) Mode() TrustMode { return t.mode }

// CAFile reports the pinned trust anchor path (meaningful in TrustClusterCA
// mode).
func (t *Trust) CAFile() string { return t.caFile }

// TrustStatus is a snapshot of a Trust's current posture, for the findings
// producer and for `vnproxctl status`.
type TrustStatus struct {
	// Error is non-empty when the anchor is currently unusable, in which case
	// every https peer request fails closed.
	Error string
	// CAFile is the pinned anchor path.
	CAFile string
	// Mode is the configured verification mode.
	Mode TrustMode
	// Pinned reports whether peers are verified against the cluster CA.
	Pinned bool
}

// Status forces an anchor re-evaluation and reports the result. Calling it at
// startup is what turns "the pinned CA is missing" into a loud log line before
// the first peer request rather than after it.
func (t *Trust) Status() TrustStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked(true)
	st := TrustStatus{Mode: t.mode, CAFile: t.caFile, Pinned: t.mode.Pinned()}
	if t.loadErr != nil {
		st.Error = t.loadErr.Error()
	}
	return st
}

// RoundTrip implements http.RoundTripper.
//
// Plain-http requests (only ClientOptions.Scheme == "http", which exists for
// this package's own tests and for a local mock peer) bypass the anchor
// entirely: there is no certificate to verify, and requiring a cluster CA to
// exist before an http request could be made would make the test harness
// depend on /etc/pve. That bypass is *reported* rather than hidden — see
// Client.TrustReport, which surfaces a non-https scheme as a degraded trust
// posture in the findings stream.
func (t *Trust) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" {
		return http.DefaultTransport.RoundTrip(req)
	}
	rt, err := t.transport()
	if err != nil {
		return nil, err
	}
	return rt.RoundTrip(req)
}

func (t *Trust) transport() (http.RoundTripper, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked(false)
	if t.loadErr != nil {
		return nil, t.loadErr
	}
	return t.rt, nil
}

// refreshLocked re-evaluates the trust anchor when the reload cadence has
// elapsed (or force is set).
func (t *Trust) refreshLocked(force bool) {
	now := t.now().UnixNano()
	if t.checked && !force && time.Duration(now-t.checkedAt) < t.reloadInterval {
		return
	}
	t.checkedAt = now
	t.checked = true

	switch t.mode {
	case TrustSystem:
		t.loadSystemLocked()
	case TrustInsecure:
		t.loadInsecureLocked()
	default:
		t.loadClusterCALocked()
	}
}

// loadClusterCALocked reads the pinned CA and rebuilds the transport when its
// bytes changed.
//
// Two failure shapes, deliberately different:
//
//   - Nothing has ever loaded successfully: fail closed. loadErr is set, rt
//     stays nil, and every https peer request returns an ErrPeerUntrusted-
//     wrapped error without opening a connection. There is no fallback to the
//     system pool, which is the entire point of this file.
//   - A previously-loaded anchor is momentarily unreadable (a pmxcfs blip, an
//     unmounted /etc/pve during a restart of pve-cluster): keep serving the
//     last known-good pool and log a WARN. This trusts nothing that was not
//     already verified against a real cluster CA, and it avoids converting a
//     transient filesystem error into a cluster-wide peer outage. A CA file is
//     an anchor, not a revocation list; dropping it on a read error would buy
//     no security and cost availability.
func (t *Trust) loadClusterCALocked() {
	pemBytes, err := os.ReadFile(t.caFile)
	if err != nil {
		t.anchorProblemLocked(fmt.Errorf("peer: %w: %w: reading %s: %w", ErrPeerUntrusted, ErrTrustAnchorUnavailable, t.caFile, err))
		return
	}
	sum := sha256.Sum256(pemBytes)
	fp := hex.EncodeToString(sum[:])
	if t.rt != nil && t.loadErr == nil && fp == t.fingerprint {
		return
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.anchorProblemLocked(fmt.Errorf("peer: %w: %w: no PEM certificate found in %s", ErrPeerUntrusted, ErrTrustAnchorUnavailable, t.caFile))
		return
	}

	rotated := t.fingerprint != "" && t.fingerprint != fp
	t.swapTransportLocked(newTLSTransport(pool, false))
	t.fingerprint = fp
	t.loadErr = nil
	msg := "peer: cluster CA trust anchor loaded; peer TLS is pinned to it"
	if rotated {
		msg = "peer: cluster CA rotated on disk; peer TLS re-pinned to the new anchor without a restart"
	}
	t.logOnceLocked("ok:"+fp, slog.LevelInfo, msg, "ca_file", t.caFile)
}

// anchorProblemLocked records a failed anchor read. See loadClusterCALocked
// for why a *previously* good pool survives it.
func (t *Trust) anchorProblemLocked(err error) {
	if t.rt != nil && t.loadErr == nil {
		t.logOnceLocked("degraded:"+err.Error(), slog.LevelWarn,
			"peer: pinned cluster CA is currently unreadable; continuing to verify peers against the last successfully loaded anchor",
			"ca_file", t.caFile, "error", err)
		return
	}
	t.loadErr = err
	t.swapTransportLocked(nil)
	t.fingerprint = ""
	t.logOnceLocked("failclosed:"+err.Error(), slog.LevelError,
		"peer: no usable cluster CA trust anchor — every peer is treated as unverifiable and the peer API is unavailable until this is fixed. There is no fallback to the system trust store by design",
		"ca_file", t.caFile, "error", err)
}

func (t *Trust) loadSystemLocked() {
	if t.rt != nil {
		return
	}
	pool, err := t.systemPool()
	if err != nil {
		t.loadErr = fmt.Errorf("peer: %w: %w: loading system certificate pool: %w", ErrPeerUntrusted, ErrTrustAnchorUnavailable, err)
		t.logOnceLocked("sysfail:"+err.Error(), slog.LevelError, "peer: system certificate pool unavailable", "error", err)
		return
	}
	// A nil pool means "use the host's own system roots" — crypto/tls's
	// documented behaviour for a nil RootCAs.
	t.swapTransportLocked(newTLSTransport(pool, false))
	t.loadErr = nil
}

func (t *Trust) loadInsecureLocked() {
	if t.rt != nil {
		return
	}
	t.swapTransportLocked(newTLSTransport(nil, true))
	t.loadErr = nil
}

// swapTransportLocked installs next and retires the previous transport's idle
// connections. Closing them is what makes rotation real: an already-pooled
// keep-alive connection was verified against the *old* anchor and would
// otherwise keep serving requests that the new anchor would reject.
func (t *Trust) swapTransportLocked(next *http.Transport) {
	if t.rt != nil {
		t.rt.CloseIdleConnections()
	}
	t.rt = next
}

// logOnceLocked emits msg only when the trust state actually changed, so a
// steady state does not reprint every reload interval while a real transition
// is never swallowed.
func (t *Trust) logOnceLocked(sig string, level slog.Level, msg string, args ...any) {
	if t.lastLogged == sig {
		return
	}
	t.lastLogged = sig
	t.log.Log(context.Background(), level, msg, args...)
}

func newTLSTransport(pool *x509.CertPool, insecure bool) *http.Transport {
	tr := baseTransport()
	tr.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		RootCAs:            pool,
		InsecureSkipVerify: insecure, //nolint:gosec // only ever true for TrustInsecure, which NewTrust refuses to build without the exact AckInsecure literal (T-1906 AC3).
	}
	return tr
}

func baseTransport() *http.Transport {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	return tr.Clone()
}

// isTrustFailure reports whether err is a TLS server-certificate verification
// failure (or this package's own "no anchor" refusal) rather than a
// reachability problem. It is the whole of the untrusted/unreachable
// distinction an operator needs to tell an attack from a cable (T-1906 AC5).
//
// Deliberately *not* included: tls.RecordHeaderError (a peer answering plain
// HTTP on the TLS port), a handshake timeout, or a protocol/version
// negotiation failure. Those are misconfiguration or reachability problems,
// and calling them "untrusted" would cry wolf.
func isTrustFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPeerUntrusted) || errors.Is(err, ErrTrustAnchorUnavailable) {
		return true
	}
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid)
}
