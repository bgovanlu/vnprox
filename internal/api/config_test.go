package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigHandler_ReturnsNonSecretInstanceInfo(t *testing.T) {
	info := InstanceInfo{
		Version:                  "1.2.3-test",
		Listen:                   "0.0.0.0:8007",
		PVEAPIURL:                "https://127.0.0.1:8006",
		ProtectedPath:            "/etc/pve/vnprox/protected.json",
		PVEInterval:              "10s",
		HostInterval:             "5s",
		LLDPInterval:             "30s",
		ConfirmTimeoutDefaultSec: 120,
		SnapshotKeepDays:         90,
		SnapshotPinDays:          7,
		ReadOnly:                 true,
		AllowDangerousOps:        false,
		HostSampler:              "conntrack",
	}

	rec := httptest.NewRecorder()
	configHandler(info)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != info {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, info)
	}

	// Guard against a future field leaking a secret: the serialized body
	// must never contain a token/key/password field name.
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"token", "password", "secret", "sessionkey", "tlskey", "session_key"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("GET /config body contains forbidden substring %q — a secret may be leaking: %s", forbidden, rec.Body.String())
		}
	}
}
