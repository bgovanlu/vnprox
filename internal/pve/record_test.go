// SPDX-License-Identifier: Apache-2.0

package pve_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// singleNodeToken is single-node.yaml's own root@pam!daemon API token.
//
// Record mode uses token auth because it cannot use ticket auth: see
// TestRecord_TicketLoginIsRefused, which is not a limitation to work
// around but the guard working.
const singleNodeToken = "root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42"

func recordingClient(t *testing.T, apiURL, dir, version string) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Config{
		APIURL:           apiURL,
		Auth:             pve.AuthAPIToken,
		TokenValue:       singleNodeToken,
		RecordDir:        dir,
		RecordPVEVersion: version,
	})
	if err != nil {
		t.Fatalf("pve.New (record mode): %v", err)
	}
	return c
}

// TestRecord_WritesWhatTheServerActuallySent is the recorder's core
// claim: the cassette holds the bytes that came off the wire, not a
// re-encoding of the typed struct the client decoded them into.
//
// The distinction is the entire point of the card. A cassette rebuilt
// from pve.NetworkInterface would contain exactly the fields vnprox
// already knows about, which is the property a hand-written fixture
// already has, and would be silent about the field PVE sends that no Go
// struct here mentions.
func TestRecord_WritesWhatTheServerActuallySent(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	dir := t.TempDir()
	c := recordingClient(t, ts.URL, dir, "8.3.5")

	if _, err := c.ListNodeNetwork(context.Background(), "pve1"); err != nil {
		t.Fatalf("ListNodeNetwork: %v", err)
	}

	written := c.Recorded()
	if len(written) != 1 {
		t.Fatalf("Recorded() = %v, want exactly one cassette", written)
	}
	if got, want := filepath.Dir(written[0]), filepath.Join(dir, "8.3.5"); got != want {
		t.Errorf("cassette landed in %s, want %s", got, want)
	}

	got, err := pvecassette.Load(written[0])
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Key() != "GET /api2/json/nodes/pve1/network" {
		t.Errorf("Key() = %q", got.Key())
	}
	if got.PVEVersion != "8.3.5" {
		t.Errorf("PVEVersion = %q, want 8.3.5", got.PVEVersion)
	}

	// The comparison that matters: the same request, made again with no
	// client in the way at all.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api2/json/nodes/pve1/network", nil)
	if err != nil {
		t.Fatalf("building raw request: %v", err)
	}
	req.Header.Set("Authorization", "PVEAPIToken="+singleNodeToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("raw GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading raw body: %v", err)
	}
	if got.Body != string(raw) {
		t.Errorf("the cassette is not what the server sent:\n  cassette: %q\n  wire:     %q", got.Body, raw)
	}
	if got.Status != resp.StatusCode {
		t.Errorf("Status = %d, want %d", got.Status, resp.StatusCode)
	}
}

// TestRecord_TicketLoginIsRefused is T-2502 AC2 seen from the client: the
// guard is not a library function somebody has to remember to call, it is
// on the only path a response can take to disk, and it fires on the most
// obvious thing an operator will try first.
//
// The request fails rather than the recording being skipped. A recording
// session that quietly dropped the responses it could not write would
// produce a fixture set with holes in it, and nothing downstream would
// ever know which holes.
func TestRecord_TicketLoginIsRefused(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	dir := t.TempDir()

	c, err := pve.New(pve.Config{
		APIURL:           ts.URL,
		Auth:             pve.AuthTicket,
		Username:         "root@pam",
		Password:         "vnprox-mock",
		RecordDir:        dir,
		RecordPVEVersion: "8.3.5",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	_, _, err = c.Login(context.Background())
	if err == nil {
		t.Fatal("recording a ticket login succeeded; the login response IS a credential")
	}
	if !errors.Is(err, pvecassette.ErrSecretInCassette) {
		t.Errorf("error is not ErrSecretInCassette: %v", err)
	}
	if !strings.Contains(err.Error(), "body.data.ticket") {
		t.Errorf("error does not name the field that caused the refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "/access/ticket") {
		t.Errorf("error does not name the request that caused the refusal: %v", err)
	}

	// Nothing reached disk — not the ticket, not an empty placeholder.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		sub, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if len(sub) != 0 {
			t.Errorf("refused recording still wrote %d file(s) into %s", len(sub), e.Name())
		}
	}
	if written := c.Recorded(); len(written) != 0 {
		t.Errorf("Recorded() = %v, want none", written)
	}
}

// TestRecord_EnvironmentTurnsItOn covers the documented operator flow
// (`make record`), which is an environment variable on a released binary
// rather than a config field only this repository's tests can reach.
func TestRecord_EnvironmentTurnsItOn(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	dir := t.TempDir()
	t.Setenv(pve.EnvRecordDir, dir)
	t.Setenv(pve.EnvRecordPVEVersion, "8.4.1")

	c, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthAPIToken, TokenValue: singleNodeToken})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	if _, statusErr := c.ClusterStatus(context.Background()); statusErr != nil {
		t.Fatalf("ClusterStatus: %v", statusErr)
	}

	set, err := pvecassette.LoadDir(filepath.Join(dir, "8.4.1"))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if _, ok := set["GET /api2/json/cluster/status"]; !ok {
		t.Errorf("no cassette for the request that was made; got %v", pvecassette.Keys(set))
	}
}

// TestRecord_RequiresThePVEVersion: the one thing a cassette has over a
// hand-written fixture is that it can say which Proxmox produced it, and
// an `unknown/` directory cannot be repaired after the fact.
func TestRecord_RequiresThePVEVersion(t *testing.T) {
	t.Setenv(pve.EnvRecordDir, t.TempDir())
	t.Setenv(pve.EnvRecordPVEVersion, "")

	_, err := pve.New(pve.Config{APIURL: "https://127.0.0.1:8006", Auth: pve.AuthAPIToken, TokenValue: singleNodeToken})
	if err == nil {
		t.Fatal("pve.New enabled record mode with no PVE version")
	}
	if !strings.Contains(err.Error(), pve.EnvRecordPVEVersion) {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
}

// TestRecord_OffByDefault: record mode is a diagnostic mode an operator
// opts into. A daemon that recorded every PVE response it ever saw would
// be a data-retention problem, not a feature.
func TestRecord_OffByDefault(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthAPIToken, TokenValue: singleNodeToken})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	if _, err := c.ClusterStatus(context.Background()); err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if written := c.Recorded(); written != nil {
		t.Errorf("Recorded() = %v with record mode off", written)
	}
}
