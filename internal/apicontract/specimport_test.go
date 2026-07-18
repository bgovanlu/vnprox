package apicontract

import (
	"net/http"
	"testing"
)

// TestSpecImportIdempotency is T-1106 acceptance criterion 1's fourth
// required flow, and this card's chosen terraform-plan drift-detection
// primitive (see docs/api.md's new "Automation contract" section): a spec
// exported from live state, immediately re-imported against that same
// live state, must produce a draft changeset with zero ops and an empty
// notInSpec — the "clean plan" a `terraform plan`-equivalent needs to
// report "no changes". Importing the same unchanged content a second time
// must produce the identical zero-ops result (true idempotency, not just
// a one-shot coincidence) — run against both single-node and
// three-node-vlan (T-1101's own round-trip acceptance criterion, exercised
// here over the real HTTP route rather than the internal/spec package
// directly, with a real bearer token).
func TestSpecImportIdempotency(t *testing.T) {
	for _, fx := range []struct {
		name string
		path string
	}{
		{name: "single-node", path: fixtureSingleNode},
		{name: "three-node-vlan", path: fixtureThreeNode},
	} {
		t.Run(fx.name, func(t *testing.T) {
			h := newContractHarness(t, fx.path)
			readToken := h.mintToken("tok-spec-read", "netRead")
			writeToken := h.mintToken("tok-spec-write", "netRead", "netWrite")

			exportResp := h.do(h.newRequest(http.MethodGet, "/api/v1/spec", readToken, nil))
			if exportResp.StatusCode != http.StatusOK {
				t.Fatalf("GET /spec: status = %d, want 200", exportResp.StatusCode)
			}
			var exported specExportResponse
			decodeJSON(t, exportResp, &exported)
			if exported.SpecVersion != 1 {
				t.Fatalf("specVersion = %d, want 1", exported.SpecVersion)
			}

			importOnce := func() specImportResponse {
				t.Helper()
				body := mustJSON(t, map[string]string{"content": exported.Content})
				resp := h.do(h.newRequest(http.MethodPost, "/api/v1/spec/import", writeToken, body))
				if resp.StatusCode != http.StatusCreated {
					t.Fatalf("POST /spec/import: status = %d, want 201", resp.StatusCode)
				}
				var got specImportResponse
				decodeJSON(t, resp, &got)
				return got
			}

			first := importOnce()
			if len(first.Ops) != 0 {
				t.Errorf("first import of an unchanged export produced %d ops, want 0: %+v", len(first.Ops), first.Ops)
			}
			if len(first.NotInSpec) != 0 {
				t.Errorf("first import reported %d notInSpec entities, want 0: %v", len(first.NotInSpec), first.NotInSpec)
			}
			redactedFirst := first
			redactedFirst.changesetResponse = redactedChangeset(first.changesetResponse)
			assertGolden(t, "specimport_"+fx.name+"_import", redactedFirst)

			// Idempotency: importing the exact same content again yields the
			// same "no changes" result, not an accumulating diff.
			second := importOnce()
			if len(second.Ops) != 0 {
				t.Errorf("second import produced %d ops, want 0 (not idempotent): %+v", len(second.Ops), second.Ops)
			}
			if len(second.NotInSpec) != 0 {
				t.Errorf("second import reported %d notInSpec entities, want 0: %v", len(second.NotInSpec), second.NotInSpec)
			}

			// A read-only token must not be able to import (netWrite
			// required) — the write path stays gated even though the
			// "plan" read half only needs netRead.
			roAttempt := h.do(h.newRequest(http.MethodPost, "/api/v1/spec/import", readToken, mustJSON(t, map[string]string{"content": exported.Content})))
			if roAttempt.StatusCode != http.StatusForbidden {
				t.Errorf("POST /spec/import with netRead-only token: status = %d, want 403", roAttempt.StatusCode)
			}
		})
	}
}
