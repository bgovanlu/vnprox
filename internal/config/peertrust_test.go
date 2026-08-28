// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// peerTOML wraps a [peer] fragment in the minimum [server] section Load
// requires (it validates the daemon's own TLS certificate paths, which have
// nothing to do with peer trust).
func peerTOML(t *testing.T, body string) string {
	t.Helper()
	certPath, keyPath := writeTestCert(t, t.TempDir())
	return "[server]\ntls_cert = \"" + certPath + "\"\ntls_key = \"" + keyPath + "\"\n" + body
}

// TestLoad_PeerTLSTrustDefaultsToPinned is T-1906's most important config
// assertion: a config file that says nothing about peer TLS — which is every
// production config in existence — gets the pinned cluster CA, not the system
// trust store.
func TestLoad_PeerTLSTrustDefaultsToPinned(t *testing.T) {
	for _, body := range []string{
		"", // no sections at all
		"[peer]\n",
		"[peer]\nsecret_path = \"/etc/pve/priv/vnprox/cluster.secret\"\n",
	} {
		path := writeTemp(t, "vnprox.toml", peerTOML(t, body))
		cfg, err := Load(path, discardLogger())
		if err != nil {
			t.Fatalf("Load(%q): %v", body, err)
		}
		if cfg.Peer.TLSTrust != peer.TrustClusterCA {
			t.Fatalf("config %q: tls_trust = %q, want %q", body, cfg.Peer.TLSTrust, peer.TrustClusterCA)
		}
		if !cfg.Peer.TLSTrust.Pinned() {
			t.Fatalf("config %q: default trust mode is not pinned", body)
		}
		if cfg.Peer.CAFile != peer.DefaultClusterCAPath {
			t.Fatalf("config %q: ca_file = %q, want %q", body, cfg.Peer.CAFile, peer.DefaultClusterCAPath)
		}
	}
}

// TestLoad_PeerTLSTrustEscapeHatchInterlock is T-1906 AC3 at the config layer:
// no single edit turns pinning off, and no malformed attempt is silently
// resolved in either direction — the daemon refuses to start instead.
func TestLoad_PeerTLSTrustEscapeHatchInterlock(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantMode peer.TrustMode
		wantErr  bool
	}{
		{
			name:     "mode alone is not enough (system)",
			body:     "[peer]\ntls_trust = \"system\"\n",
			wantErr:  true,
			wantMode: "",
		},
		{
			name:     "mode alone is not enough (insecure)",
			body:     "[peer]\ntls_trust = \"insecure\"\n",
			wantErr:  true,
			wantMode: "",
		},
		{
			name:    "the other mode's ack does not work",
			body:    "[peer]\ntls_trust = \"insecure\"\ntls_trust_ack = \"" + peer.AckSystem + "\"\n",
			wantErr: true,
		},
		{
			name:    "a truthy value is not an acknowledgement",
			body:    "[peer]\ntls_trust = \"system\"\ntls_trust_ack = \"yes\"\n",
			wantErr: true,
		},
		{
			name:    "an unknown mode is fatal, not coerced",
			body:    "[peer]\ntls_trust = \"off\"\n",
			wantErr: true,
		},
		{
			name:     "an ack alone changes nothing and is harmless",
			body:     "[peer]\ntls_trust_ack = \"" + peer.AckInsecure + "\"\n",
			wantMode: peer.TrustClusterCA,
		},
		{
			name:     "system with its own ack is accepted",
			body:     "[peer]\ntls_trust = \"system\"\ntls_trust_ack = \"" + peer.AckSystem + "\"\n",
			wantMode: peer.TrustSystem,
		},
		{
			name:     "insecure with its own ack is accepted",
			body:     "[peer]\ntls_trust = \"insecure\"\ntls_trust_ack = \"" + peer.AckInsecure + "\"\n",
			wantMode: peer.TrustInsecure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTemp(t, "vnprox.toml", peerTOML(t, tc.body)), discardLogger())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load succeeded with mode %q; want a fatal config error", cfg.Peer.TLSTrust)
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("err = %v, want it to wrap ErrInvalidConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Peer.TLSTrust != tc.wantMode {
				t.Fatalf("tls_trust = %q, want %q", cfg.Peer.TLSTrust, tc.wantMode)
			}
		})
	}
}

// TestLoad_PeerTLSTrustErrorNamesTheRequiredAcknowledgement — an operator who
// tripped the interlock has to be told exactly what to write, or they will
// reach for something worse.
func TestLoad_PeerTLSTrustErrorNamesTheRequiredAcknowledgement(t *testing.T) {
	_, err := Load(writeTemp(t, "vnprox.toml", peerTOML(t, "[peer]\ntls_trust = \"system\"\n")), discardLogger())
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), peer.AckSystem) || !strings.Contains(err.Error(), "tls_trust_ack") {
		t.Fatalf("error must name the required tls_trust_ack literal: %v", err)
	}
}

// TestLoad_PeerCAFileOverride — the trust anchor path is configurable, which
// is what lets a dev daemon pin a locally-generated CA instead of turning
// pinning off.
func TestLoad_PeerCAFileOverride(t *testing.T) {
	cfg, err := Load(writeTemp(t, "vnprox.toml", peerTOML(t, "[peer]\nca_file = \"/tmp/my-ca.pem\"\n")), discardLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Peer.CAFile != "/tmp/my-ca.pem" {
		t.Fatalf("ca_file = %q", cfg.Peer.CAFile)
	}
	if cfg.Peer.TLSTrust != peer.TrustClusterCA {
		t.Fatalf("overriding the anchor path must not change the trust mode, got %q", cfg.Peer.TLSTrust)
	}
}
