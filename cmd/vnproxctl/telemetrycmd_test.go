package main

// telemetrycmd_test.go drives `vnproxctl telemetry` through run() — the real
// argv-to-exit-code path — for the same reason verifycmd_test.go does: the
// opt-in gate is a guard at the door of the COMMAND, and a test that called
// telemetry.Submit directly would keep passing after somebody wired the CLI
// to skip it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// --- fixtures ---------------------------------------------------------------

// cliSpyTransport is the one transport these tests use. As in
// internal/telemetry's own tests, the SAME type is used for the
// "nothing must be sent" assertions and for the control legs that prove it
// notices a call — a transport that has never been seen to fire is not
// evidence of anything.
type cliSpyTransport struct {
	report      func(format string, args ...any)
	respond     func() (*http.Response, error)
	bodies      [][]byte
	mu          sync.Mutex
	calls       atomic.Int64
	fatalOnCall bool
}

func (s *cliSpyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls.Add(1)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	if s.fatalOnCall && s.report != nil {
		s.report("an outbound telemetry request was made to %s carrying %d bytes; nothing may be sent here", req.URL, len(body))
	}
	if s.respond != nil {
		return s.respond()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func (s *cliSpyTransport) lastBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return nil
	}
	return s.bodies[len(s.bodies)-1]
}

// installTransport points the telemetry commands at tr for one test.
func installTransport(t *testing.T, tr http.RoundTripper) {
	t.Helper()
	previous := telemetryTransport
	telemetryTransport = tr
	t.Cleanup(func() { telemetryTransport = previous })
}

func cliFailIfCalled(t *testing.T) *cliSpyTransport {
	t.Helper()
	return &cliSpyTransport{fatalOnCall: true, report: func(format string, args ...any) {
		t.Errorf(format, args...)
	}}
}

// telemetryEnv writes a vnprox.toml (with the given [telemetry] section) and
// a verify report next to a scratch store, and returns their paths.
func telemetryEnv(t *testing.T, telemetrySection string) (configPath, reportPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "vnprox.toml")
	toml := "[storage]\ndb_path = \"" + filepath.Join(dir, "vnprox.db") + "\"\n" + telemetrySection
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	raw, err := json.Marshal(cliSampleReport())
	if err != nil {
		t.Fatalf("encoding the report fixture: %v", err)
	}
	reportPath = filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, raw, 0o600); err != nil {
		t.Fatalf("writing the report fixture: %v", err)
	}
	return configPath, reportPath
}

// cliSampleReport is a valid, non-mock report carrying the things the
// payload must not.
func cliSampleReport() verify.Report {
	results := []verify.Result{
		{
			ID:           "drift.config_vs_live",
			MatrixRow:    21,
			Area:         "Drift detection (config-vs-live, node-vs-node)",
			Suite:        verify.SuiteHardware,
			Precondition: "a real PVE node",
			Status:       verify.StatusPass,
			Detail:       "node-alpha matches its staged config",
			Evidence: []verify.Evidence{
				verify.NewEvidence(verify.SourceCommand, "ssh node-alpha ip -j link", "aa:bb:cc:dd:ee:ff on 192.0.2.10"),
			},
			DurationMS: 42,
		},
	}
	return verify.Report{
		ReportVersion: verify.CurrentReportVersion,
		GeneratedAt:   time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Suite:         verify.SuiteHardware,
		Environment: verify.Environment{
			VnproxVersion: "3.0.3",
			PVEVersion:    "pve-manager/9.2.4",
			Kernel:        "6.8.12-4-pve",
			NICModels:     []string{"enp3s0 0x8086:0x1521 pci:v00008086d00001521sv00008086sd00000002bc02sc00i00"},
			Nodes:         []string{"node-alpha", "node-beta"},
			PVEEndpoint:   "https://192.0.2.10:8006",
		},
		Results: results,
		Summary: verify.Summarize(results),
	}
}

const enabledTelemetry = "\n[telemetry]\nenabled = true\nendpoint = \"https://collector.example/vnprox\"\n"

// --- AC1 --------------------------------------------------------------------

// TestTelemetrySendContactsNothingWhenOff is AC1 at the command level.
func TestTelemetrySendContactsNothingWhenOff(t *testing.T) {
	cases := []struct {
		name    string
		section string
	}{
		{name: "no [telemetry] section at all — the shipped state", section: ""},
		{name: "explicitly disabled with an endpoint sitting right there", section: "\n[telemetry]\nenabled = false\nendpoint = \"https://collector.example/vnprox\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configPath, reportPath := telemetryEnv(t, tc.section)
			tr := cliFailIfCalled(t)
			installTransport(t, tr)

			var stdout, stderr bytes.Buffer
			code := run([]string{"telemetry", "send", "--config", configPath, "--report", reportPath}, &stdout, &stderr)

			if code == ExitSuccess {
				t.Fatalf("`telemetry send` reported success with telemetry off:\n%s", stdout.String())
			}
			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d (an opt-in that was never given is a usage problem)", code, ExitUsage)
			}
			if got := tr.calls.Load(); got != 0 {
				t.Fatalf("%d outbound request(s) were made with telemetry off", got)
			}
			if !strings.Contains(stderr.String(), "disabled") {
				t.Errorf("the refusal does not say telemetry is disabled:\n%s", stderr.String())
			}
		})
	}

	// The control leg. Same transport type, same wiring, same fatalOnCall:
	// with telemetry on, it registers exactly one call and reports it — so
	// the zero counts above are evidence rather than an untested assumption.
	t.Run("control: the same fail-if-called transport DOES fire when telemetry is on", func(t *testing.T) {
		configPath, reportPath := telemetryEnv(t, enabledTelemetry)
		var reported string
		tr := &cliSpyTransport{fatalOnCall: true, report: func(format string, args ...any) {
			reported = fmt.Sprintf(format, args...)
		}}
		installTransport(t, tr)

		var stdout, stderr bytes.Buffer
		code := run([]string{"telemetry", "send", "--config", configPath, "--report", reportPath}, &stdout, &stderr)
		if code != ExitSuccess {
			t.Fatalf("`telemetry send` failed with telemetry on: %d\n%s", code, stderr.String())
		}
		if got := tr.calls.Load(); got != 1 {
			t.Fatalf("the spy recorded %d calls, want 1", got)
		}
		if !strings.Contains(reported, "an outbound telemetry request was made") {
			t.Fatalf("the spy did not report the call it saw (%q)", reported)
		}
		if len(tr.lastBody()) == 0 {
			t.Fatal("the request carried no body")
		}
	})
}

// TestTelemetryPreviewSendsNothing: the command an operator runs precisely
// because they have not decided yet must not itself be the decision.
func TestTelemetryPreviewSendsNothing(t *testing.T) {
	configPath, reportPath := telemetryEnv(t, enabledTelemetry) // even with it ON
	tr := cliFailIfCalled(t)
	installTransport(t, tr)

	var stdout, stderr bytes.Buffer
	code := run([]string{"telemetry", "preview", "--config", configPath, "--report", reportPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("preview exited %d:\n%s", code, stderr.String())
	}
	if got := tr.calls.Load(); got != 0 {
		t.Fatalf("preview made %d outbound request(s)", got)
	}
	if stdout.Len() == 0 {
		t.Fatal("preview printed nothing")
	}
}

// --- AC2 --------------------------------------------------------------------

// TestPreviewPrintsExactlyWhatSendPosts is AC2 at the command level: two
// separate invocations, two separately built payloads, captured and
// compared byte for byte.
func TestPreviewPrintsExactlyWhatSendPosts(t *testing.T) {
	configPath, reportPath := telemetryEnv(t, enabledTelemetry)

	var previewOut, previewErr bytes.Buffer
	installTransport(t, cliFailIfCalled(t))
	if code := run([]string{"telemetry", "preview", "--config", configPath, "--report", reportPath}, &previewOut, &previewErr); code != ExitSuccess {
		t.Fatalf("preview exited %d:\n%s", code, previewErr.String())
	}

	tr := &cliSpyTransport{}
	installTransport(t, tr)
	var sendOut, sendErr bytes.Buffer
	if code := run([]string{"telemetry", "send", "--config", configPath, "--report", reportPath}, &sendOut, &sendErr); code != ExitSuccess {
		t.Fatalf("send exited %d:\n%s", code, sendErr.String())
	}

	sent := tr.lastBody()
	if len(sent) == 0 {
		t.Fatal("nothing was transmitted, so this comparison would be vacuous")
	}
	if !bytes.Equal(previewOut.Bytes(), sent) {
		t.Fatalf("`telemetry preview` and the transmitted bytes differ.\npreview (%d bytes):\n%s\nsent (%d bytes):\n%s",
			previewOut.Len(), previewOut.String(), len(sent), sent)
	}

	// And what was printed is a payload, not an empty document that would
	// match a send of nothing.
	var decoded map[string]any
	if err := json.Unmarshal(previewOut.Bytes(), &decoded); err != nil {
		t.Fatalf("the preview output is not JSON: %v\n%s", err, previewOut.String())
	}
	for _, key := range []string{"payloadVersion", "installId", "kernel", "checks"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the previewed payload has no %q", key)
		}
	}
	// The identifying values in the source report are not in what left.
	for _, forbidden := range []string{"node-alpha", "192.0.2.10", "aa:bb:cc:dd:ee:ff", "enp3s0"} {
		if bytes.Contains(sent, []byte(forbidden)) {
			t.Errorf("the transmitted payload contains %q", forbidden)
		}
	}
}

// --- AC4 --------------------------------------------------------------------

func TestTelemetryResetIDFromTheCommandLine(t *testing.T) {
	configPath, reportPath := telemetryEnv(t, "")
	installTransport(t, cliFailIfCalled(t))

	// A preview generates the id (and says so).
	var first, firstErr bytes.Buffer
	if code := run([]string{"telemetry", "preview", "--config", configPath, "--report", reportPath}, &first, &firstErr); code != ExitSuccess {
		t.Fatalf("preview exited %d:\n%s", code, firstErr.String())
	}
	before := installIDFromStatus(t, configPath)
	if before == "" {
		t.Fatal("no install-id exists after a preview")
	}
	if !strings.Contains(firstErr.String(), "generated a new install-id") {
		t.Errorf("the command did not mention that it created an id:\n%s", firstErr.String())
	}

	var resetOut, resetErr bytes.Buffer
	if code := run([]string{"telemetry", "reset-id", "--config", configPath}, &resetOut, &resetErr); code != ExitSuccess {
		t.Fatalf("reset-id exited %d:\n%s", code, resetErr.String())
	}
	after := installIDFromStatus(t, configPath)
	if after == before {
		t.Fatal("reset-id did not change the install-id")
	}
	if strings.Contains(resetOut.String(), before) || strings.Contains(resetErr.String(), before) {
		t.Errorf("reset-id printed the OLD install-id, which is the one thing it must forget:\n%s%s", resetOut.String(), resetErr.String())
	}
	if !strings.Contains(resetOut.String(), after) {
		t.Errorf("reset-id did not print the new id:\n%s", resetOut.String())
	}
}

func installIDFromStatus(t *testing.T, configPath string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"telemetry", "status", "--config", configPath, "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("status exited %d:\n%s", code, stderr.String())
	}
	var st telemetryStatus
	if err := json.Unmarshal(stdout.Bytes(), &st); err != nil {
		t.Fatalf("decoding status: %v\n%s", err, stdout.String())
	}
	return st.InstallID
}

// TestTelemetryStatusDoesNotCreateAnInstallID: `status` is what an operator
// runs to find out whether they have a correlator. Running it must not be
// what gives them one.
func TestTelemetryStatusDoesNotCreateAnInstallID(t *testing.T) {
	configPath, _ := telemetryEnv(t, "")
	installTransport(t, cliFailIfCalled(t))

	if id := installIDFromStatus(t, configPath); id != "" {
		t.Fatalf("status reported an install-id (%q) on a fresh install", id)
	}
	if id := installIDFromStatus(t, configPath); id != "" {
		t.Fatalf("a second status call reported an install-id (%q), so the first one created it", id)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"telemetry", "status", "--config", configPath}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("status exited %d:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "disabled") {
		t.Errorf("status does not say telemetry is disabled:\n%s", stdout.String())
	}
}

// --- AC5 --------------------------------------------------------------------

// TestVerifyTelemetryDoesNotWaitForAHangingCollector is AC5 on the path
// `vnproxctl verify` actually takes.
func TestVerifyTelemetryDoesNotWaitForAHangingCollector(t *testing.T) {
	configPath, _ := telemetryEnv(t, enabledTelemetry)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	tr := &cliSpyTransport{respond: func() (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("collector went away")
	}}
	installTransport(t, tr)

	start := time.Now()
	done := startVerifyTelemetry(configPath, cliSampleReport())
	elapsed := time.Since(start)
	if done == nil {
		t.Fatal("startVerifyTelemetry did nothing with telemetry enabled")
	}

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the transport, so no hang was exercised")
	}
	select {
	case err := <-done:
		t.Fatalf("the verify path waited for the collector (err %v)", err)
	default:
	}
	if elapsed > 2*time.Second {
		t.Errorf("startVerifyTelemetry took %s while the collector hung; it must not wait at all", elapsed)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the failed send reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the background send never finished")
	}
}

// TestVerifyStartsNoTelemetryWhenOff: the hook returns having contacted
// nothing, which is the shipped state of every install.
func TestVerifyStartsNoTelemetryWhenOff(t *testing.T) {
	configPath, _ := telemetryEnv(t, "")
	tr := cliFailIfCalled(t)
	installTransport(t, tr)

	if done := startVerifyTelemetry(configPath, cliSampleReport()); done != nil {
		t.Fatal("startVerifyTelemetry started a send with telemetry off")
	}
	if got := tr.calls.Load(); got != 0 {
		t.Fatalf("%d outbound request(s) were made with telemetry off", got)
	}
}

// TestVerifyAgainstAMockSendsNothingEvenWhenTelemetryIsOn drives the whole
// `verify` command against internal/pvemock with telemetry enabled. A run
// against a mock is not compatibility evidence, and a matrix polluted with
// mock runs would look larger than it is.
func TestVerifyAgainstAMockSendsNothingEvenWhenTelemetryIsOn(t *testing.T) {
	ts := mockPVE(t)
	configPath, _ := telemetryEnv(t, enabledTelemetry)
	tr := cliFailIfCalled(t)
	installTransport(t, tr)

	var stdout, stderr bytes.Buffer
	_ = run([]string{
		"verify", "--suite=hardware", "--allow-mock",
		"--config", configPath, "--pve-url", ts.URL, "--pve-token", "u@pve!t=x",
	}, &stdout, &stderr)

	// The exit code is deliberately not asserted: against a mock with no
	// daemon behind it nearly everything skips, which AC3 of T-2501 makes a
	// non-zero exit. What matters here is that nothing was transmitted.
	if got := tr.calls.Load(); got != 0 {
		t.Fatalf("%d outbound telemetry request(s) were made for a mock run", got)
	}
	if !strings.Contains(stdout.String(), "passed") && !strings.Contains(stdout.String(), "skipped") {
		t.Fatalf("verify does not appear to have run at all, so the assertion above proves nothing:\n%s\n%s", stdout.String(), stderr.String())
	}
}

// --- wiring -----------------------------------------------------------------

func TestTelemetryIsRegisteredInTheUsage(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"--help"}, &stdout, io.Discard); code != ExitSuccess {
		t.Fatalf("--help exited %d", code)
	}
	if !strings.Contains(stdout.String(), "vnproxctl telemetry") {
		t.Errorf("`telemetry` is missing from the usage text:\n%s", stdout.String())
	}
}

func TestTelemetryUsageErrors(t *testing.T) {
	configPath, _ := telemetryEnv(t, "")
	installTransport(t, cliFailIfCalled(t))

	cases := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: []string{"telemetry"}},
		{name: "unknown subcommand", args: []string{"telemetry", "beam-it-up"}},
		{name: "preview with no report", args: []string{"telemetry", "preview", "--config", configPath}},
		{name: "send with no report", args: []string{"telemetry", "send", "--config", configPath}},
		{name: "preview of a file that is not a report", args: []string{"telemetry", "preview", "--config", configPath, "--report", configPath}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != ExitUsage {
				t.Errorf("exit code = %d, want %d\n%s%s", code, ExitUsage, stdout.String(), stderr.String())
			}
		})
	}
}

// TestTelemetryPreviewReadsASignedArtifact: `verify --out` writes a signed
// report, and that is the file an operator is most likely to have.
func TestTelemetryPreviewReadsASignedArtifact(t *testing.T) {
	configPath, _ := telemetryEnv(t, "")
	installTransport(t, cliFailIfCalled(t))

	key, err := verify.EphemeralSigningKey()
	if err != nil {
		t.Fatalf("EphemeralSigningKey: %v", err)
	}
	artifact, err := verify.SignReport(cliSampleReport(), key)
	if err != nil {
		t.Fatalf("SignReport: %v", err)
	}
	signedPath := filepath.Join(t.TempDir(), "report.signed.json")
	if err := os.WriteFile(signedPath, artifact, 0o600); err != nil {
		t.Fatalf("writing the signed artifact: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"telemetry", "preview", "--config", configPath, "--report", signedPath}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("preview of a signed artifact exited %d:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "drift.config_vs_live") {
		t.Errorf("the preview does not carry the report's check id:\n%s", stdout.String())
	}
}
