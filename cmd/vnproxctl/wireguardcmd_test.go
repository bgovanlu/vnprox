// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

// --- wireguardTunnelState (the three-state derivation) -------------------

func TestWireguardTunnelState_ThreeStates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fresh := wireguardTunnelWire{
		Node: "pve1", IfName: "wg0",
		Peers: []wireguardPeerWire{{PublicKey: "peer1", LastHandshakeUnix: now.Add(-30 * time.Second).Unix()}},
	}
	stale := wireguardTunnelWire{
		Node: "pve1", IfName: "wg0",
		Peers: []wireguardPeerWire{{PublicKey: "peer1", LastHandshakeUnix: now.Add(-15 * time.Minute).Unix()}},
	}
	never := wireguardTunnelWire{Node: "pve1", IfName: "wg0"} // no peers at all — state is still known ("down"), not "unknown"

	cases := []struct {
		name        string
		want        string
		tunnel      wireguardTunnelWire
		unavailable bool
	}{
		{name: "fresh handshake is up", tunnel: fresh, want: "up"},
		{name: "stale handshake is down", tunnel: stale, want: "down"},
		{name: "zero peers is down, not unknown — the state IS known: nothing is up", tunnel: never, want: "down"},
		{name: "unavailable read is unknown regardless of tunnel content", tunnel: fresh, unavailable: true, want: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wireguardTunnelState(c.tunnel, c.unavailable, now); got != c.want {
				t.Errorf("wireguardTunnelState() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestWireguardTunnelState_ReusesFindingsThreshold pins that this file's
// derivation moves in lockstep with internal/findings.WgHandshakeStaleThreshold
// (currently 5m) rather than carrying a second, independently-tunable
// number — a peer exactly at the threshold is up, one second past it is
// down, matching WgTunnelHasFreshHandshake's own <= comparison
// (health_wireguard.go).
func TestWireguardTunnelState_ReusesFindingsThreshold(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	atThreshold := wireguardTunnelWire{
		Node: "pve1", IfName: "wg0",
		Peers: []wireguardPeerWire{{PublicKey: "p", LastHandshakeUnix: now.Add(-5 * time.Minute).Unix()}},
	}
	pastThreshold := wireguardTunnelWire{
		Node: "pve1", IfName: "wg0",
		Peers: []wireguardPeerWire{{PublicKey: "p", LastHandshakeUnix: now.Add(-5*time.Minute - time.Second).Unix()}},
	}
	if got := wireguardTunnelState(atThreshold, false, now); got != "up" {
		t.Errorf("exactly at the stale threshold: state = %q, want up", got)
	}
	if got := wireguardTunnelState(pastThreshold, false, now); got != "down" {
		t.Errorf("one second past the stale threshold: state = %q, want down", got)
	}
}

// --- target Ref format -----------------------------------------------

// TestWgTunnelRef_MatchesDocumentedFormat pins the Go side against
// params_wg.go's/wizardOps.ts's documented "wg-tunnel:<node>:<id>" and
// "wg-peer:<node>:<tunnelId>/<publicKey>" wire convention — this CLI and the
// frontend must never disagree about a target Ref's string form.
func TestWgTunnelRef_MatchesDocumentedFormat(t *testing.T) {
	if got, want := wgTunnelRef("pve1", "tun-1").String(), "wg-tunnel:pve1:tun-1"; got != want {
		t.Errorf("wgTunnelRef.String() = %q, want %q", got, want)
	}
}

func TestWgPeerRef_MatchesDocumentedFormat(t *testing.T) {
	if got, want := wgPeerRef("pve1", "tun-1", "PUBKEY=").String(), "wg-peer:pve1:tun-1/PUBKEY="; got != want {
		t.Errorf("wgPeerRef.String() = %q, want %q", got, want)
	}
}

// --- list / show ---------------------------------------------------------

const twoTunnelsFixture = `{"items":[
  {"id":"tun-1","node":"pve1","ifName":"wg0","publicKey":"pubkey1","carrier":"vmbr0","addresses":["10.10.0.1/24"],
   "peers":[{"publicKey":"peerA","endpoint":"1.2.3.4:51820","allowedIps":["10.10.0.2/32"],"rxBytes":10,"txBytes":20,"external":true,"endpointDrifted":false,"lastHandshakeUnix":TS_FRESH}],
   "status":{"interfaceUp":true,"peerCount":1},"listenPort":51820,"mtu":1420},
  {"id":"tun-2","node":"pve2","ifName":"wg1","addresses":["10.20.0.1/24"],
   "peers":[{"publicKey":"peerB","allowedIps":["10.20.0.2/32"],"rxBytes":0,"txBytes":0,"external":true,"endpointDrifted":false}],
   "status":{"interfaceUp":false,"peerCount":1},"listenPort":51821,"mtu":0}
]}`

func fixtureWithFreshHandshake(t *testing.T) string {
	t.Helper()
	fresh := time.Now().Add(-30 * time.Second).Unix()
	return strings.ReplaceAll(twoTunnelsFixture, "TS_FRESH", strconvItoa64(fresh))
}

func TestRunWireguardList_TableShowsStateColumn(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		if r.Method != http.MethodGet || r.URL.Path != "/wireguard/tunnels" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureWithFreshHandshake(t)))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "list", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "tun-1") || !strings.Contains(out, "up") {
		t.Errorf("stdout = %q, want tun-1 reported up (fresh handshake)", out)
	}
	if !strings.Contains(out, "tun-2") || !strings.Contains(out, "down") {
		t.Errorf("stdout = %q, want tun-2 reported down (no handshake)", out)
	}
}

func TestRunWireguardList_OJSONMatchesDoc(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureWithFreshHandshake(t)))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "list", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded wireguardTunnelsWire
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(decoded.Items) != 2 || decoded.Items[0].State != "up" || decoded.Items[1].State != "down" {
		t.Errorf("decoded = %+v, want two items with computed up/down state", decoded.Items)
	}
	assertDocumentedJSON(t, "wireguard list", stdout.Bytes())
}

func TestRunWireguardList_EmptyIsCleanExit(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "list", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No WireGuard tunnels.") {
		t.Errorf("stdout = %q, want the no-tunnels message", stdout.String())
	}
}

// TestRunWireguardList_NeverLeaksUnexpectedPrivateKeyField is the
// structural half of "never print private key material": even if a server
// bug (or a future field) put a "privateKey" alongside a tunnel/peer,
// wireguardTunnelWire/wireguardPeerWire have no field to decode it into, so
// it is silently dropped by json.Unmarshal — never rendered in table OR
// json output. The wire type itself makes the leak impossible, the way
// WireGuardTunnelView (docs/api.md) is documented to.
func TestRunWireguardList_NeverLeaksUnexpectedPrivateKeyField(t *testing.T) {
	const secret = "AAAASUPERSECRETPRIVATEKEYVALUE="
	body := `{"items":[{"id":"tun-1","node":"pve1","ifName":"wg0","publicKey":"pub1","privateKey":"` + secret + `",
	  "addresses":["10.10.0.1/24"],"peers":[{"publicKey":"peerA","allowedIps":["10.10.0.2/32"],
	  "presharedKey":"` + secret + `","privateKey":"` + secret + `","rxBytes":0,"txBytes":0,"external":true,"endpointDrifted":false}],
	  "status":{"interfaceUp":true,"peerCount":1},"listenPort":51820,"mtu":0}]}`
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	for _, format := range []string{"table", "json"} {
		var stdout, stderr bytes.Buffer
		args := []string{"wireguard", "list", "--url", srv.URL, "--token", "tok"}
		if format == "json" {
			args = append(args, "-o", "json")
		}
		if code := run(args, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("[%s] exit code = %d, want 0 (stderr: %s)", format, code, stderr.String())
		}
		if strings.Contains(stdout.String(), secret) {
			t.Errorf("[%s] stdout leaked the injected private/preshared key material: %s", format, stdout.String())
		}
	}
}

func TestRunWireguardShow_FiltersByIDAndOJSON(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureWithFreshHandshake(t)))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "show", "--url", srv.URL, "--token", "tok", "tun-2"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "tun-2") || strings.Contains(stdout.String(), "tun-1") {
		t.Errorf("stdout = %q, want only tun-2's detail", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"wireguard", "show", "--url", srv.URL, "--token", "tok", "-o", "json", "tun-1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded wireguardTunnelWire
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded.ID != "tun-1" {
		t.Fatalf("decoded = %+v, err=%v, want tun-1", decoded, err)
	}
	assertDocumentedJSON(t, "wireguard show", stdout.Bytes())
}

func TestRunWireguardShow_NotFound(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "show", "--url", srv.URL, "--token", "tok", "nope"}, &stdout, &stderr)
	if code != ExitError {
		t.Fatalf("exit code = %d, want ExitError", code)
	}
	if !strings.Contains(stderr.String(), "no such WireGuard tunnel") {
		t.Errorf("stderr = %q, want a not-found message", stderr.String())
	}
}

func TestRunWireguardShow_RequiresExactlyOneID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"wireguard", "show"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("no id: exit code = %d, want ExitUsage", code)
	}
}

// --- create ----------------------------------------------------------

// wireOpBody mirrors the {op, target, params} envelope Op.MarshalJSON
// (internal/change/op.go) produces, used by these tests to inspect exactly
// what this CLI sent without importing internal/change's unexported decode
// internals.
type wireOpBody struct {
	Op     string          `json:"op"`
	Target string          `json:"target"`
	Params json.RawMessage `json:"params"`
}

type wireCreateChangesetBody struct {
	Title string       `json:"title"`
	Ops   []wireOpBody `json:"ops"`
}

const changesetResponseFixture = `{"id":"cs1","title":"t","author":"a","status":"draft","ops":[{}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`

// TestRunWireguardCreate_StagesDraftAndNeverApplies is this command's core
// safety assertion (CLAUDE.md: the change engine is the sole mutation
// path): it must call POST /changesets exactly once, with a single
// wg.tunnel.create op, and nothing else — in particular never
// POST /changesets/{id}/apply.
func TestRunWireguardCreate_StagesDraftAndNeverApplies(t *testing.T) {
	var createCalls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/changesets":
			createCalls++
			var body wireCreateChangesetBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			if len(body.Ops) != 1 {
				t.Fatalf("ops = %+v, want exactly one op", body.Ops)
			}
			op := body.Ops[0]
			if op.Op != "wg.tunnel.create" {
				t.Errorf("op = %q, want wg.tunnel.create", op.Op)
			}
			if op.Target != "wg-tunnel:pve1:tun-1" {
				t.Errorf("target = %q, want wg-tunnel:pve1:tun-1", op.Target)
			}
			if !strings.Contains(string(op.Params), `"ifName":"wg0"`) {
				t.Errorf("params = %s, want ifName wg0", op.Params)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(changesetResponseFixture))
		default:
			t.Fatalf("unexpected request %s %s — wireguard create must call nothing but POST /changesets", r.Method, r.URL.Path)
		}
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"wireguard", "create", "--url", srv.URL, "--token", "tok",
		"--node", "pve1", "--id", "tun-1", "--if-name", "wg0", "--addresses", "10.10.0.1/24", "--listen-port", "51820",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if createCalls != 1 {
		t.Fatalf("POST /changesets called %d times, want 1", createCalls)
	}
	for _, needle := range []string{"cs1", "draft", "Nothing was applied"} {
		if !strings.Contains(stdout.String(), needle) {
			t.Errorf("stdout missing %q:\n%s", needle, stdout.String())
		}
	}
}

func TestRunWireguardCreate_OJSONMatchesChangesetDoc(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(changesetResponseFixture))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"wireguard", "create", "--url", srv.URL, "--token", "tok",
		"--node", "pve1", "--id", "tun-1", "--if-name", "wg0", "-o", "json",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "changeset", stdout.Bytes())
}

func TestRunWireguardCreate_RequiredFlags(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) { dialed = true })

	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "create", "--url", srv.URL, "--token", "tok", "--node", "pve1"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("missing --id/--if-name: exit code = %d, want ExitUsage", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted with missing required flags")
	}
}

// TestRunWireguardCreate_NeverBuildsAPrivateKeyField mirrors
// wgTunnelOps.test.ts's "never carries a privateKey/private_key field"
// assertion: the op this subcommand builds (before it ever leaves the
// process) has no field a private key could occupy.
func TestRunWireguardCreate_NeverBuildsAPrivateKeyField(t *testing.T) {
	op := change.Op{Type: change.OpWgTunnelCreate, Target: wgTunnelRef("pve1", "tun-1"), Params: &change.WgTunnelCreateParams{IfName: "wg0"}}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("marshaling op: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "private") {
		t.Errorf("built op contains \"private\": %s", raw)
	}
}

// --- update ------------------------------------------------------------

func TestRunWireguardUpdate_OnlySetFlagsChangeParams(t *testing.T) {
	var body wireCreateChangesetBody
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(changesetResponseFixture))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"wireguard", "update", "--url", srv.URL, "--token", "tok",
		"--node", "pve1", "--mtu", "1400", "tun-1",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if len(body.Ops) != 1 || body.Ops[0].Op != "wg.tunnel.update" {
		t.Fatalf("ops = %+v, want exactly one wg.tunnel.update op", body.Ops)
	}
	var params map[string]any
	if err := json.Unmarshal(body.Ops[0].Params, &params); err != nil {
		t.Fatalf("decoding params: %v", err)
	}
	if _, has := params["mtu"]; !has {
		t.Errorf("params = %+v, want mtu set (it was passed)", params)
	}
	for _, unset := range []string{"listenPort", "carrier", "addresses"} {
		if _, has := params[unset]; has {
			t.Errorf("params = %+v, want %s omitted (it was not passed)", params, unset)
		}
	}
}

func TestRunWireguardUpdate_RequiresAtLeastOneField(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) { dialed = true })

	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "update", "--url", srv.URL, "--token", "tok", "--node", "pve1", "tun-1"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("no update field flags: exit code = %d, want ExitUsage", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted with no update field")
	}
}

// --- delete --------------------------------------------------------------

func TestRunWireguardDelete_StagesDeleteOp(t *testing.T) {
	var body wireCreateChangesetBody
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(changesetResponseFixture))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "delete", "--url", srv.URL, "--token", "tok", "--node", "pve1", "tun-1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if len(body.Ops) != 1 || body.Ops[0].Op != "wg.tunnel.delete" || body.Ops[0].Target != "wg-tunnel:pve1:tun-1" {
		t.Fatalf("ops = %+v, want one wg.tunnel.delete targeting wg-tunnel:pve1:tun-1", body.Ops)
	}
}

// --- peer-add / peer-remove ---------------------------------------------

// TestRunWireguardPeerAdd_NeverPrintsPresharedKey pins the server-side
// redaction contract (params_wg.go, internal/api/changesets.go's
// redactOpSecrets): the mock server here mimics that redaction — the same
// behavior the real daemon guarantees for every changeset read, including a
// fresh POST /changesets response — and this test confirms neither table
// nor json output ever surfaces the PSK the operator passed on the command
// line, end to end.
func TestRunWireguardPeerAdd_NeverPrintsPresharedKey(t *testing.T) {
	const psk = "SUPERSECRETPSKVALUE"
	var sawPSKInRequest bool
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		var body wireCreateChangesetBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if len(body.Ops) != 1 || body.Ops[0].Op != "wg.peer.add" {
			t.Fatalf("ops = %+v, want one wg.peer.add", body.Ops)
		}
		if strings.Contains(string(body.Ops[0].Params), psk) {
			sawPSKInRequest = true
		}
		// The real daemon's toChangesetResponse always runs ops through
		// redactOpSecrets before echoing them back — simulate that here
		// rather than echoing the request verbatim.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(changesetResponseFixture))
	})

	for _, format := range []string{"table", "json"} {
		var stdout, stderr bytes.Buffer
		args := []string{
			"wireguard", "peer-add", "--url", srv.URL, "--token", "tok",
			"--node", "pve1", "--public-key", "peerPUB=", "--preshared-key", psk,
		}
		if format == "json" {
			args = append(args, "-o", "json")
		}
		args = append(args, "tun-1")
		if code := run(args, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("[%s] exit code = %d, want 0 (stderr: %s)", format, code, stderr.String())
		}
		if strings.Contains(stdout.String(), psk) {
			t.Errorf("[%s] stdout leaked the preshared key: %s", format, stdout.String())
		}
	}
	if !sawPSKInRequest {
		t.Error("the request to the daemon never carried the PSK — it must, so the daemon can seal it")
	}
}

func TestRunWireguardPeerAdd_MarksExternalTrue(t *testing.T) {
	var body wireCreateChangesetBody
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(changesetResponseFixture))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"wireguard", "peer-add", "--url", srv.URL, "--token", "tok",
		"--node", "pve1", "--public-key", "peerPUB=", "--allowed-ips", "10.0.0.2/32,10.0.0.3/32", "tun-1",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if len(body.Ops) != 1 {
		t.Fatalf("ops = %+v, want one op", body.Ops)
	}
	if body.Ops[0].Target != "wg-peer:pve1:tun-1/peerPUB=" {
		t.Errorf("target = %q, want wg-peer:pve1:tun-1/peerPUB=", body.Ops[0].Target)
	}
	var params map[string]any
	if err := json.Unmarshal(body.Ops[0].Params, &params); err != nil {
		t.Fatalf("decoding params: %v", err)
	}
	if external, _ := params["external"].(bool); !external {
		t.Errorf("params = %+v, want external:true", params)
	}
	ips, _ := params["allowedIps"].([]any)
	if len(ips) != 2 {
		t.Errorf("params = %+v, want two allowedIps", params)
	}
}

func TestRunWireguardPeerAdd_RequiredFlags(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) { dialed = true })
	var stdout, stderr bytes.Buffer
	code := run([]string{"wireguard", "peer-add", "--url", srv.URL, "--token", "tok", "--node", "pve1", "tun-1"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("missing --public-key: exit code = %d, want ExitUsage", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted with missing required flags")
	}
}

func TestRunWireguardPeerRemove_StagesOp(t *testing.T) {
	var body wireCreateChangesetBody
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(changesetResponseFixture))
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"wireguard", "peer-remove", "--url", srv.URL, "--token", "tok",
		"--node", "pve1", "--public-key", "peerPUB=", "tun-1",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if len(body.Ops) != 1 || body.Ops[0].Op != "wg.peer.remove" || body.Ops[0].Target != "wg-peer:pve1:tun-1/peerPUB=" {
		t.Fatalf("ops = %+v, want one wg.peer.remove targeting wg-peer:pve1:tun-1/peerPUB=", body.Ops)
	}
}

// --- dispatch --------------------------------------------------------

func TestRunWireguard_NoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"wireguard"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("bare `wireguard` exit code = %d, want ExitUsage", code)
	}
}

func TestRunWireguard_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"wireguard", "nope"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit code = %d, want ExitUsage", code)
	}
}
