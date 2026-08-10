package verify

// mockdetect.go answers one question at the door: is the thing on the other
// end of this URL a real Proxmox VE?
//
// It exists because of the single most dangerous way this whole card could
// fail. Every other test in this repository runs against internal/pvemock;
// the natural thing for a developer, a CI job, or a hurried operator to do is
// point `vnproxctl verify` at the mock, watch it go green, and file that
// green run as hardware validation. The resulting report would be
// indistinguishable from a real one — same schema, same signature, same
// confident summary — while proving nothing at all. That is worse than having
// no suite, because the number in the matrix would go up.
//
// So the refusal is the default and --allow-mock is the escape hatch, and the
// detection uses three independent signals rather than one, because a mock
// that is not recognised is a mock that gets recorded as hardware:
//
//  1. the X-Pvemock header both mock servers now set on every response;
//  2. the replay server's own X-Pvemock-Replay* headers (T-2502's author
//     asked for this explicitly: a replay server is a mock endpoint too, and
//     a cassette recorded on real hardware is still not real hardware being
//     exercised now);
//  3. a version string that says "mock", which is what a cassette recorded
//     from the mock carries in its pveVersion.
//
// Any one of them is enough. None of them can be produced by a real pveproxy.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Mock-identifying response headers. The names mirror internal/pvemock's own
// constants; they are re-spelled here rather than imported so that this
// package — which ships in the production binary — does not depend on the
// mock server package.
const (
	mockHeader             = "X-Pvemock"
	replayHeader           = "X-Pvemock-Replay"
	replayMatchedKeyHeader = "X-Pvemock-Replay-Key"
)

// MockVerdict is what DetectMock concluded.
type MockVerdict struct {
	// Reason names the signal, for the report's Environment.MockReason and
	// for the refusal message. Empty iff IsMock is false.
	Reason string
	// Version is the endpoint's reported PVE version, when it could be read.
	Version string
	// IsMock is true when any signal fired.
	IsMock bool
}

// AllowMockFlag is the flag name the refusal message must name, in one place,
// so the message and the flag cannot drift apart.
const AllowMockFlag = "--allow-mock"

// DetectMock probes baseURL and reports whether it is a mock.
//
// A probe that cannot reach the endpoint at all returns an error rather than
// "not a mock": failing open here would mean an unreachable endpoint sails
// past the guard and every check then skips, producing a report whose
// environment claims real hardware.
func DetectMock(ctx context.Context, client *http.Client, baseURL string) (MockVerdict, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base := strings.TrimSuffix(baseURL, "/")

	// /api2/json/version is the cheapest real endpoint, and the one a replay
	// server built from a mock recording will either answer (with a mock
	// version string) or refuse distinctively.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api2/json/version", nil)
	if err != nil {
		return MockVerdict{}, fmt.Errorf("verify: building mock-detection probe for %s: %w", baseURL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return MockVerdict{}, fmt.Errorf("verify: probing %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if v := resp.Header.Get(mockHeader); v != "" {
		return MockVerdict{IsMock: true, Reason: fmt.Sprintf("the endpoint identifies itself as internal/pvemock (%s: %s)", mockHeader, v)}, nil
	}
	if v := resp.Header.Get(replayHeader); v != "" {
		return MockVerdict{IsMock: true, Reason: fmt.Sprintf("the endpoint is a cassette replay server (%s: %s) — recorded PVE traffic, not a live cluster", replayHeader, v)}, nil
	}
	if v := resp.Header.Get(replayMatchedKeyHeader); v != "" {
		return MockVerdict{IsMock: true, Reason: fmt.Sprintf("the endpoint answered from a recorded cassette (%s: %s) — recorded PVE traffic, not a live cluster", replayMatchedKeyHeader, v)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return MockVerdict{}, fmt.Errorf("verify: reading the version response from %s: %w", baseURL, err)
	}
	version := extractPVEVersion(body)
	if strings.Contains(strings.ToLower(version), "mock") {
		return MockVerdict{IsMock: true, Version: version,
			Reason: fmt.Sprintf("the endpoint reports PVE version %q, which names a mock", version)}, nil
	}
	return MockVerdict{Version: version}, nil
}

// extractPVEVersion pulls the version string out of PVE's own
// {"data":{"version":...,"release":...}} envelope, tolerating the shapes a
// mock or a replayed cassette may present instead.
func extractPVEVersion(body []byte) string {
	var envelope struct {
		Data struct {
			Version string `json:"version"`
			Release string `json:"release"`
			RepoID  string `json:"repoid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		parts := make([]string, 0, 3)
		for _, p := range []string{envelope.Data.Version, envelope.Data.Release, envelope.Data.RepoID} {
			if strings.TrimSpace(p) != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "/")
		}
	}
	return ""
}

// MockRefusalMessage is the message a run against an unallowed mock ends
// with. It names the flag (AC4) and it says why the flag exists, because an
// operator who is told only "pass --allow-mock" will pass --allow-mock.
func MockRefusalMessage(endpoint string, v MockVerdict) string {
	return fmt.Sprintf(
		"refusing to run the hardware validation suite against %s: %s.\n"+
			"A green run against a mock is indistinguishable from a green run against real Proxmox, and would raise the hardware-validated count in docs/status-matrix.md without validating any hardware.\n"+
			"Point --pve-url at a real cluster, or pass %s to run against the mock anyway — the report will be stamped as a mock run and is not hardware evidence.",
		endpoint, v.Reason, AllowMockFlag)
}
