package peer

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AC1 — the headline case
// ---------------------------------------------------------------------------

const (
	ac1ChildEnv     = "VNPROX_T1906_AC1_CHILD"
	ac1ClusterCAEnv = "VNPROX_T1906_CLUSTER_CA"
	ac1MissingCAEnv = "VNPROX_T1906_MISSING_CA"
	ac1PeerAddrEnv  = "VNPROX_T1906_PEER_ADDR"
)

// TestTrust_AC1_RejectsAPeerWhoseCAIsInTheSystemTrustStore is T-1906's
// headline test: a peer presenting a certificate signed by a *different* CA is
// rejected even when that CA is in the system trust store.
//
// It runs its assertions in a **child `go test` process** so the rogue CA can
// genuinely be installed into the operating system trust store crypto/x509
// consults (Linux honours SSL_CERT_FILE/SSL_CERT_DIR, but caches the resulting
// pool behind a sync.Once — a fresh process is the only way to be sure the
// injection actually took, rather than silently no-opping and leaving a
// vacuous test that passes for the wrong reason).
//
// The child asserts four things in order, and the second is what stops this
// test from ever rotting into a tautology:
//
//  1. the rogue CA really is in the system pool;
//  2. a client built exactly the way peer.NewClient built one *before* T-1906
//     (`&http.Client{Timeout: …}`, net/http's inherited trust store) accepts
//     the rogue peer — i.e. the vulnerability is reproduced here, in this
//     process, against this server;
//  3. the cluster-CA-pinned client rejects the very same peer, with
//     ErrPeerUntrusted;
//  4. a pinned client whose CA file does not exist *also* rejects it, rather
//     than silently falling back to the system pool that step 2 just proved
//     would have accepted it.
func TestTrust_AC1_RejectsAPeerWhoseCAIsInTheSystemTrustStore(t *testing.T) {
	if os.Getenv(ac1ChildEnv) == "1" {
		runAC1Child(t)
		return
	}

	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "Rogue Publicly Trusted CA")
	clusterCAPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	rogueCAPath := rogueCA.writePEM(t, dir, "rogue-ca.pem")
	emptyDir := filepath.Join(dir, "no-certs")
	if err := os.MkdirAll(emptyDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// The impostor: a perfectly well-formed HTTPS peer daemon whose
	// certificate is issued by a CA the host trusts, and which the cluster CA
	// never signed.
	srv := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1", "localhost"))

	cmd := exec.Command(os.Args[0], "-test.run", "^"+t.Name()+"$", "-test.v") //nolint:gosec // re-executes this very test binary; no external input.
	cmd.Env = append(os.Environ(),
		ac1ChildEnv+"=1",
		// Install the rogue CA as the host's system trust store.
		"SSL_CERT_FILE="+rogueCAPath,
		"SSL_CERT_DIR="+emptyDir,
		ac1ClusterCAEnv+"="+clusterCAPath,
		ac1MissingCAEnv+"="+filepath.Join(dir, "this-file-does-not-exist.pem"),
		ac1PeerAddrEnv+"="+srv.addr(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AC1 child process failed: %v\n--- child output ---\n%s", err, out)
	}
}

func runAC1Child(t *testing.T) {
	addr := os.Getenv(ac1PeerAddrEnv)
	clusterCAPath := os.Getenv(ac1ClusterCAEnv)
	missingCAPath := os.Getenv(ac1MissingCAEnv)
	if addr == "" || clusterCAPath == "" || missingCAPath == "" {
		t.Fatalf("AC1 child: missing harness environment")
	}
	url := "https://" + addr + "/api/peer/health"
	ctx := context.Background()

	// (1) The premise: the rogue CA is in this process's system trust store.
	sysPool, err := x509.SystemCertPool()
	if err != nil {
		t.Fatalf("AC1 child: loading the system certificate pool: %v", err)
	}
	if len(sysPool.Subjects()) == 0 { //nolint:staticcheck // SA1019: Subjects() is deprecated for system pools, but here it is exactly the introspection the premise check needs and the pool came from SSL_CERT_FILE.
		t.Fatalf("AC1 child: the system certificate pool is empty; SSL_CERT_FILE injection did not take effect")
	}

	// (2) Reproduce the pre-T-1906 client verbatim: no TLS configuration at
	// all, so net/http's default system trust store applies. It must SUCCEED.
	// If it does not, the rogue CA was not really trusted and everything below
	// would be proving nothing.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("AC1 child: building request: %v", err)
	}
	legacy := &http.Client{Timeout: 5 * time.Second} // exactly what NewClient used to build
	resp, err := legacy.Do(req)
	if err != nil {
		t.Fatalf("AC1 child: premise failed — a system-trust-store client did NOT accept the rogue peer, so this test cannot demonstrate anything: %v", err)
	}
	_ = resp.Body.Close()

	// (3) The pinned client must reject that same peer.
	pinned, err := NewTrust(TrustOptions{CAFile: clusterCAPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("AC1 child: NewTrust: %v", err)
	}
	client := newTLSClient(t, pinned)
	err = client.Health(ctx, Peer{Node: "impostor", Addr: addr})
	if err == nil {
		t.Fatalf("AC1 child: pinned client ACCEPTED a peer signed by a system-trusted CA — the pin is not in effect")
	}
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("AC1 child: want ErrPeerUntrusted, got %v", err)
	}

	// (4) No silent fallback: a pinned client whose anchor is missing must
	// also reject, even though the system pool would have accepted (step 2).
	noAnchor, err := NewTrust(TrustOptions{CAFile: missingCAPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("AC1 child: NewTrust(missing anchor): %v", err)
	}
	err = newTLSClient(t, noAnchor).Health(ctx, Peer{Node: "impostor2", Addr: addr})
	if err == nil {
		t.Fatalf("AC1 child: a client with NO trust anchor accepted the peer — it fell back to the system trust store")
	}
	if !errors.Is(err, ErrPeerUntrusted) || !errors.Is(err, ErrTrustAnchorUnavailable) {
		t.Fatalf("AC1 child: want ErrPeerUntrusted+ErrTrustAnchorUnavailable, got %v", err)
	}
}

// TestTrust_AC1_SystemModeAcceptsWhatPinningRejects is AC1's deterministic,
// in-process companion: the same rogue peer, the same two clients, but with
// "the trust store the client would otherwise inherit" injected through this
// package's own seam rather than through the OS. It documents the exact
// difference the pin makes without depending on the host, and it runs even if
// the subprocess test above is ever skipped on a platform whose system pool
// cannot be steered.
func TestTrust_AC1_SystemModeAcceptsWhatPinningRejects(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "Rogue Publicly Trusted CA")
	clusterCAPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")

	srv := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))
	rogueOnly := x509.NewCertPool()
	if !rogueOnly.AppendCertsFromPEM(rogueCA.pem) {
		t.Fatal("seeding the fake system pool")
	}

	// The escape hatch, with the rogue CA "installed on the host": accepted.
	systemTrust, err := NewTrust(TrustOptions{
		Mode:       TrustSystem,
		Ack:        AckSystem,
		Logger:     discardLogger(),
		systemPool: func() (*x509.CertPool, error) { return rogueOnly, nil },
	})
	if err != nil {
		t.Fatalf("NewTrust(system): %v", err)
	}
	if healthErr := newTLSClient(t, systemTrust).Health(context.Background(), srv.peer("rogue")); healthErr != nil {
		t.Fatalf("system-pool mode should accept a peer signed by a CA in that pool: %v", healthErr)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("server hits after the accepted request = %d, want 1", got)
	}

	// The default: pinned to the cluster CA, same peer, rejected.
	pinned, err := NewTrust(TrustOptions{CAFile: clusterCAPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust(pinned): %v", err)
	}
	err = newTLSClient(t, pinned).Health(context.Background(), srv.peer("rogue2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("pinned client: want ErrPeerUntrusted, got %v", err)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1 — the rejected request must never have reached the handler", got)
	}
}

// ---------------------------------------------------------------------------
// AC2 — the positive case
// ---------------------------------------------------------------------------

func TestTrust_AC2_AcceptsAPeerSignedByThePinnedClusterCA(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "127.0.0.1"))

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	if healthErr := client.Health(context.Background(), srv.peer("pve2")); healthErr != nil {
		t.Fatalf("a peer serving a cluster-CA-issued certificate must be accepted: %v", healthErr)
	}
	v, err := client.Version(context.Background(), srv.peer("pve2"))
	if err != nil {
		t.Fatalf("Version over the pinned connection: %v", err)
	}
	if v.ProtocolVersion != 2 {
		t.Fatalf("protocolVersion = %d, want 2 (the body really came from the peer)", v.ProtocolVersion)
	}
	statuses := client.PeerStatuses()
	if len(statuses) != 1 || statuses[0].State != PeerTrustOK {
		t.Fatalf("PeerStatuses = %+v, want one ok entry", statuses)
	}
}

// TestTrust_AC2_StillVerifiesTheHostname guards the obvious way to "pass" AC2
// by accident: pinning the CA but disabling name verification. A certificate
// the cluster CA really did issue, for a different host, must still be
// refused — otherwise any node in the cluster could impersonate any other.
func TestTrust_AC2_StillVerifiesTheHostname(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "10.9.9.9", "some-other-node"))

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("a cluster-CA certificate for the wrong host must be untrusted, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC3 — the escape hatch
// ---------------------------------------------------------------------------

// TestTrust_AC3_EscapeHatchNeedsItsOwnExactAcknowledgement is the
// "impossible to enable by accident" table. Every row that is not an explicit,
// correctly-acknowledged escape hatch must either be pinned or refuse to
// build — never quietly unpinned.
func TestTrust_AC3_EscapeHatchNeedsItsOwnExactAcknowledgement(t *testing.T) {
	cases := []struct {
		name     string
		mode     TrustMode
		ack      string
		wantErr  error
		wantMode TrustMode
	}{
		{name: "zero value is pinned", wantMode: TrustClusterCA},
		{name: "explicit cluster-ca needs no ack", mode: TrustClusterCA, wantMode: TrustClusterCA},
		{name: "cluster-ca ignores a stray ack", mode: TrustClusterCA, ack: AckInsecure, wantMode: TrustClusterCA},
		{name: "system without an ack is refused", mode: TrustSystem, wantErr: ErrTrustNotAcknowledged},
		{name: "system with an empty-ish ack is refused", mode: TrustSystem, ack: "true", wantErr: ErrTrustNotAcknowledged},
		{name: "system with the insecure ack is refused", mode: TrustSystem, ack: AckInsecure, wantErr: ErrTrustNotAcknowledged},
		{name: "system with a near-miss ack is refused", mode: TrustSystem, ack: AckSystem + " ", wantErr: ErrTrustNotAcknowledged},
		{name: "system with its own ack is allowed", mode: TrustSystem, ack: AckSystem, wantMode: TrustSystem},
		{name: "insecure without an ack is refused", mode: TrustInsecure, wantErr: ErrTrustNotAcknowledged},
		{name: "insecure with the system ack is refused", mode: TrustInsecure, ack: AckSystem, wantErr: ErrTrustNotAcknowledged},
		{name: "insecure with its own ack is allowed", mode: TrustInsecure, ack: AckInsecure, wantMode: TrustInsecure},
		{name: "an unknown mode is refused", mode: "off", ack: AckInsecure, wantErr: ErrUnknownTrustMode},
		{name: "a plausible typo is refused", mode: "clusterca", wantErr: ErrUnknownTrustMode},
		{name: "a boolean-looking mode is refused", mode: "false", wantErr: ErrUnknownTrustMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trust, err := NewTrust(TrustOptions{Mode: tc.mode, Ack: tc.ack, Logger: discardLogger()})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if trust != nil {
					t.Fatal("a refused configuration must not yield a usable Trust")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if trust.Mode() != tc.wantMode {
				t.Fatalf("mode = %q, want %q", trust.Mode(), tc.wantMode)
			}
		})
	}
}

// TestTrust_AC3_EscapeHatchWarnsOnEveryStartup: the WARN is emitted on every
// construction, not just the first, and there is nothing (no ack, no state, no
// counter) that can silence it.
func TestTrust_AC3_EscapeHatchWarnsOnEveryStartup(t *testing.T) {
	for _, tc := range []struct {
		mode TrustMode
		ack  string
	}{
		{TrustSystem, AckSystem},
		{TrustInsecure, AckInsecure},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			cap := &logCapture{}
			for i := 1; i <= 3; i++ { // three successive daemon startups
				if _, err := NewTrust(TrustOptions{Mode: tc.mode, Ack: tc.ack, Logger: cap.logger()}); err != nil {
					t.Fatalf("NewTrust: %v", err)
				}
				warns := cap.at(slog.LevelWarn)
				if len(warns) != i {
					t.Fatalf("after startup %d: %d WARN records, want %d — the escape-hatch warning must not be deduplicated across startups", i, len(warns), i)
				}
			}
			for _, r := range cap.at(slog.LevelWarn) {
				if !strings.Contains(r.Message, "escape hatch") {
					t.Fatalf("WARN message does not name the escape hatch: %q", r.Message)
				}
			}
		})
	}
}

// TestTrust_AC3_DefaultConstructionIsPinnedNotSystem: the posture a caller
// gets when it configures nothing at all. This is the specific regression the
// card names — a client that inherits net/http's system trust store because
// nobody set anything.
func TestTrust_AC3_DefaultConstructionIsPinnedNotSystem(t *testing.T) {
	trust, err := NewTrust(TrustOptions{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	if !trust.Mode().Pinned() {
		t.Fatalf("default mode %q is not pinned", trust.Mode())
	}
	if trust.CAFile() != DefaultClusterCAPath {
		t.Fatalf("default CA file = %q, want %q", trust.CAFile(), DefaultClusterCAPath)
	}

	// And the same for a Client that was handed neither a Trust nor an
	// HTTPClient — the shape every pre-T-1906 call site had.
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Logger: discardLogger()})
	rep := client.TrustReport()
	if !rep.Pinned || rep.Mode != TrustClusterCA || rep.CAFile != DefaultClusterCAPath {
		t.Fatalf("unconfigured Client trust report = %+v, want pinned cluster-ca at %s", rep, DefaultClusterCAPath)
	}
}

// TestTrust_AC3_ParseTrustModeNeverFallsBack pins ParseTrustMode's contract:
// "" is the secure default, and *nothing* else is silently coerced.
func TestTrust_AC3_ParseTrustModeNeverFallsBack(t *testing.T) {
	for _, in := range []string{"off", "none", "no", "disabled", "System", "INSECURE", "cluster_ca", " "} {
		if mode, err := ParseTrustMode(in); err == nil {
			t.Fatalf("ParseTrustMode(%q) = %q, want an error", in, mode)
		}
	}
	for in, want := range map[string]TrustMode{"": TrustClusterCA, "cluster-ca": TrustClusterCA, "system": TrustSystem, "insecure": TrustInsecure} {
		got, err := ParseTrustMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseTrustMode(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	if _, err := ParseTrustMode(string(TrustExternal)); err == nil {
		t.Fatal("TrustExternal is a report-only value and must not be configurable")
	}
}

// ---------------------------------------------------------------------------
// AC4 — rotation without a restart
// ---------------------------------------------------------------------------

// TestTrust_AC4_RotatedClusterCAIsPickedUpWithoutARestart drives one Trust and
// one Client, never reconstructing either, across a CA rotation: peer A
// (signed by the old CA) works and peer B (signed by the new one) does not,
// then the file is rewritten and the verdicts swap.
//
// The pooled-keep-alive case is the interesting half — after the rotation,
// peer A must be rejected even though a verified connection to it is already
// in the transport's idle pool.
func TestTrust_AC4_RotatedClusterCAIsPickedUpWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	oldCA := newTestCA(t, "cluster CA (old)")
	newCA := newTestCA(t, "cluster CA (new)")
	caPath := oldCA.writePEM(t, dir, "pve-root-ca.pem")

	srvOld := newTLSPeerServer(t, oldCA.issue(t, "127.0.0.1"))
	srvNew := newTLSPeerServer(t, newCA.issue(t, "127.0.0.1"))

	clock := &fixedClock{t: time.Now()}
	trust, err := NewTrust(TrustOptions{
		CAFile:         caPath,
		ReloadInterval: time.Minute,
		Now:            clock.now,
		Logger:         discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	ctx := context.Background()

	if err := client.Health(ctx, srvOld.peer("old")); err != nil {
		t.Fatalf("before rotation: the old-CA peer must be accepted: %v", err)
	}
	if err := client.Health(ctx, srvNew.peer("new")); !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("before rotation: the new-CA peer must be untrusted, got %v", err)
	}

	// Rotate the anchor on disk. No restart, no reconstruction.
	if err := os.WriteFile(caPath, newCA.pem, 0o600); err != nil {
		t.Fatalf("rotating the CA file: %v", err)
	}

	// Not yet past the reload cadence: still the old anchor. (This also proves
	// the reload is genuinely cadence-driven rather than a per-request read.)
	if err := client.Health(ctx, srvNew.peer("new")); !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("within the reload interval the old anchor must still apply, got %v", err)
	}
	clock.advance(2 * time.Minute)

	if err := client.Health(ctx, srvNew.peer("new")); err != nil {
		t.Fatalf("after rotation: the new-CA peer must be accepted without a restart: %v", err)
	}
	if err := client.Health(ctx, srvOld.peer("old")); !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("after rotation: the old-CA peer must now be untrusted (a pooled keep-alive connection must not survive the rotation), got %v", err)
	}
}

// TestTrust_AC4_RotationIsLogged: an operator can tell from the log that the
// daemon followed a rotation, and a steady state does not reprint every
// reload interval.
func TestTrust_AC4_RotationIsLogged(t *testing.T) {
	dir := t.TempDir()
	oldCA := newTestCA(t, "cluster CA (old)")
	newCA := newTestCA(t, "cluster CA (new)")
	caPath := oldCA.writePEM(t, dir, "pve-root-ca.pem")

	cap := &logCapture{}
	clock := &fixedClock{t: time.Now()}
	trust, err := NewTrust(TrustOptions{CAFile: caPath, ReloadInterval: time.Minute, Now: clock.now, Logger: cap.logger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	for i := 0; i < 5; i++ { // steady state, several reload cadences
		trust.Status()
		clock.advance(2 * time.Minute)
	}
	infos := cap.at(slog.LevelInfo)
	if len(infos) != 2 { // the startup banner + the one "anchor loaded" line
		t.Fatalf("steady state produced %d INFO records, want 2 (no per-cadence spam): %v", len(infos), messages(infos))
	}

	if err := os.WriteFile(caPath, newCA.pem, 0o600); err != nil {
		t.Fatalf("rotating: %v", err)
	}
	clock.advance(2 * time.Minute)
	trust.Status()
	if !anyContains(cap.at(slog.LevelInfo), "rotated") {
		t.Fatalf("a CA rotation must be logged: %v", messages(cap.at(slog.LevelInfo)))
	}
}

// ---------------------------------------------------------------------------
// AC5 — untrusted vs unreachable
// ---------------------------------------------------------------------------

// TestTrust_AC5_UntrustedAndUnreachableAreDistinguishable covers the error
// classification an operator's finding is derived from: a live impostor and a
// dead node must not produce the same verdict.
func TestTrust_AC5_UntrustedAndUnreachableAreDistinguishable(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "rogue CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")

	impostor := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))

	// A deterministically dead peer: bind, then close.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	ctx := context.Background()

	untrustedErr := client.Health(ctx, impostor.peer("impostor"))
	if !errors.Is(untrustedErr, ErrPeerUntrusted) {
		t.Fatalf("impostor: want ErrPeerUntrusted, got %v", untrustedErr)
	}
	deadErr := client.Health(ctx, Peer{Node: "dead", Addr: deadAddr})
	if errors.Is(deadErr, ErrPeerUntrusted) {
		t.Fatalf("a dead peer must NOT be reported as untrusted: %v", deadErr)
	}
	if !errors.Is(deadErr, ErrPeerUnreachable) {
		t.Fatalf("dead peer: want ErrPeerUnreachable, got %v", deadErr)
	}
	// "An unverifiable peer is unreachable, never trusted": every existing
	// graceful-degradation path keys off ErrPeerUnreachable and must keep
	// working for an untrusted peer too.
	if !errors.Is(untrustedErr, ErrPeerUnreachable) {
		t.Fatalf("an untrusted peer must also satisfy errors.Is(err, ErrPeerUnreachable): %v", untrustedErr)
	}

	statuses := map[string]PeerTrustState{}
	for _, s := range client.PeerStatuses() {
		statuses[s.Node] = s.State
	}
	if statuses["impostor"] != PeerTrustUntrusted || statuses["dead"] != PeerTrustUnreachable {
		t.Fatalf("recorded states = %+v, want impostor=untrusted dead=unreachable", statuses)
	}
}

// TestTrust_AC5_UntrustedSurvivesTheCircuitBreaker: once the breaker opens,
// the fast-fail path must keep reporting *untrusted* rather than collapsing
// back to a generic "unreachable" — otherwise an impersonation attempt
// disappears from the findings stream after three attempts.
func TestTrust_AC5_UntrustedSurvivesTheCircuitBreaker(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "rogue CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	impostor := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := NewClient(ClientOptions{
		Secrets:                 newStaticSecretStore(testSecret),
		Trust:                   trust,
		Logger:                  discardLogger(),
		Scheme:                  "https",
		RequestTimeout:          5 * time.Second,
		BreakerFailureThreshold: 1,
		BreakerResetTimeout:     time.Hour,
	})
	ctx := context.Background()
	p := impostor.peer("impostor")

	if firstErr := client.Health(ctx, p); !errors.Is(firstErr, ErrPeerUntrusted) {
		t.Fatalf("first call: want ErrPeerUntrusted, got %v", firstErr)
	}
	err = client.Health(ctx, p) // breaker is now open: no network attempt
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("open-breaker call: want ErrPeerUntrusted, got %v", err)
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("open-breaker error should say so: %v", err)
	}
	if !errors.Is(err, ErrPeerUnreachable) {
		t.Fatalf("open-breaker call must still satisfy ErrPeerUnreachable: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Fail-closed and degradation
// ---------------------------------------------------------------------------

// TestTrust_FailsClosedWithNoAnchor: a missing anchor makes every peer
// unverifiable and sends nothing on the wire. The peer server here is signed
// by a CA that IS in the injected system pool, so a fallback would be visible
// as a success.
func TestTrust_FailsClosedWithNoAnchor(t *testing.T) {
	dir := t.TempDir()
	rogueCA := newTestCA(t, "rogue CA")
	srv := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))
	rogueOnly := x509.NewCertPool()
	if !rogueOnly.AppendCertsFromPEM(rogueCA.pem) {
		t.Fatal("seeding pool")
	}

	trust, err := NewTrust(TrustOptions{
		CAFile:     filepath.Join(dir, "absent.pem"),
		Logger:     discardLogger(),
		systemPool: func() (*x509.CertPool, error) { return rogueOnly, nil },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("p"))
	if !errors.Is(err, ErrPeerUntrusted) || !errors.Is(err, ErrTrustAnchorUnavailable) {
		t.Fatalf("want ErrPeerUntrusted+ErrTrustAnchorUnavailable, got %v", err)
	}
	if got := srv.hits.Load(); got != 0 {
		t.Fatalf("server hits = %d, want 0 — a request must never leave the client without a trust anchor", got)
	}
	st := trust.Status()
	if st.Error == "" || !strings.Contains(st.Error, "absent.pem") {
		t.Fatalf("Status().Error should name the missing anchor, got %q", st.Error)
	}
}

// TestTrust_FailClosedIsLoggedAtError: the fail-closed condition is not silent.
func TestTrust_FailClosedIsLoggedAtError(t *testing.T) {
	cap := &logCapture{}
	trust, err := NewTrust(TrustOptions{CAFile: filepath.Join(t.TempDir(), "absent.pem"), Logger: cap.logger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	trust.Status()
	errs := cap.at(slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("want exactly one ERROR record, got %d: %v", len(errs), messages(errs))
	}
	if !strings.Contains(errs[0].Message, "no fallback to the system trust store") {
		t.Fatalf("the fail-closed log must say there is no fallback: %q", errs[0].Message)
	}
}

// TestTrust_RejectsAnAnchorFileWithNoCertificate: a file that exists but holds
// no PEM certificate is a broken anchor, not an empty (== trust-nothing-but-
// also-fail-open) one.
func TestTrust_RejectsAnAnchorFileWithNoCertificate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pve-root-ca.pem")
	if err := os.WriteFile(path, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	trust, err := NewTrust(TrustOptions{CAFile: path, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	st := trust.Status()
	if st.Error == "" || !strings.Contains(st.Error, "no PEM certificate") {
		t.Fatalf("Status().Error = %q, want a 'no PEM certificate' failure", st.Error)
	}
}

// TestTrust_KeepsTheLastKnownGoodAnchorOnATransientReadFailure documents the
// one place this file deliberately does not fail closed, and why it is still
// safe: peers stay verified against a real cluster CA that was actually
// loaded. Dropping it on a filesystem blip would convert a transient pmxcfs
// problem into a cluster-wide peer outage and buy no security — a CA file is
// an anchor, not a revocation list.
func TestTrust_KeepsTheLastKnownGoodAnchorOnATransientReadFailure(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "rogue CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	good := newTLSPeerServer(t, clusterCA.issue(t, "127.0.0.1"))
	bad := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))

	cap := &logCapture{}
	clock := &fixedClock{t: time.Now()}
	trust, err := NewTrust(TrustOptions{CAFile: caPath, ReloadInterval: time.Minute, Now: clock.now, Logger: cap.logger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	ctx := context.Background()
	if err := client.Health(ctx, good.peer("good")); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	if err := os.Remove(caPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	clock.advance(2 * time.Minute)

	if err := client.Health(ctx, good.peer("good")); err != nil {
		t.Fatalf("a transient anchor read failure must not break a peer that verifies against the last known-good CA: %v", err)
	}
	if err := client.Health(ctx, bad.peer("bad")); !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("the last known-good anchor must still reject a foreign CA, got %v", err)
	}
	if !anyContains(cap.at(slog.LevelWarn), "last successfully loaded anchor") {
		t.Fatalf("the degraded re-read must be logged at WARN: %v", messages(cap.at(slog.LevelWarn)))
	}
}

// TestTrust_PlainHTTPBypassesTheAnchorButIsReported: the test/dev http scheme
// must not require /etc/pve to exist, and must not be able to hide.
func TestTrust_PlainHTTPBypassesTheAnchorButIsReported(t *testing.T) {
	trust, err := NewTrust(TrustOptions{CAFile: filepath.Join(t.TempDir(), "absent.pem"), Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Trust:   trust,
		Logger:  discardLogger(),
		Scheme:  "http",
	})
	rep := client.TrustReport()
	if rep.Scheme != "http" {
		t.Fatalf("TrustReport().Scheme = %q, want http", rep.Scheme)
	}
}

// TestClient_TrustReportFlagsACallerSuppliedHTTPClient: the pre-T-1906 shape
// (a caller-supplied client whose trust decision this package cannot see) is
// reported as unpinned rather than quietly vouched for.
func TestClient_TrustReportFlagsACallerSuppliedHTTPClient(t *testing.T) {
	client := NewClient(ClientOptions{
		Secrets:    newStaticSecretStore(testSecret),
		HTTPClient: &http.Client{Timeout: time.Second},
		Logger:     discardLogger(),
	})
	rep := client.TrustReport()
	if rep.Pinned || rep.Mode != TrustExternal {
		t.Fatalf("TrustReport = %+v, want an unpinned external report", rep)
	}
}

func messages(rs []slog.Record) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Message)
	}
	return out
}

func anyContains(rs []slog.Record, sub string) bool {
	for _, r := range rs {
		if strings.Contains(r.Message, sub) {
			return true
		}
	}
	return false
}
