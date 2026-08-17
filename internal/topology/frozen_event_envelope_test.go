package topology

// T-3204: the WebSocket "events"/"topology" envelope (docs/api.md's
// WebSocket section, docs/architecture.md §13.3 — frozen v1) had no
// regression guard before this file, unlike the changeset API half of the
// same freeze (internal/apicontract's golden-fixture suite). The single
// invariant every producer across every topic promises — "the flat
// {"event": "<name>", ...payload} envelope (no nested payload wrapper)" —
// is cheap to golden-check directly on this package's own producer
// (deltaEvent/BroadcastDelta), which is exactly the shape hub.go's own
// "all future event producers must keep this envelope" comment (referenced
// by docs/architecture.md §13.3 verbatim) is warning a future editor about.
// Package-internal (not topology_test) because deltaEvent is unexported —
// this is a payload-shape guard, not a public-API behavior test, so it does
// not need the black-box view the rest of this package's tests use.
import (
	"encoding/json"
	"testing"
)

func TestDeltaEvent_JSONSchema_Stable(t *testing.T) {
	evt := deltaEvent{
		Event:   "topology.delta",
		Added:   []string{"bridge:pve1:vmbr1"},
		Updated: []string{"bridge:pve1:vmbr0"},
		Removed: []string{"bridge:pve1:vmbr2"},
	}
	got, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// The flat envelope: docs/api.md's WebSocket section reads
	// `{"event": "<name>", ...payload}` — every payload field a TOP-LEVEL
	// key alongside "event", never nested under a "payload"/"data" wrapper.
	if _, ok := generic["payload"]; ok {
		t.Errorf("deltaEvent JSON carries a nested \"payload\" wrapper — the envelope is documented flat (got %s)", got)
	}
	if _, ok := generic["data"]; ok {
		t.Errorf("deltaEvent JSON carries a nested \"data\" wrapper — the envelope is documented flat (got %s)", got)
	}
	for _, field := range []string{"event", "added", "updated", "removed"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("deltaEvent JSON missing frozen top-level field %q (got %s)", field, got)
		}
	}
	if generic["event"] != "topology.delta" {
		t.Errorf(`deltaEvent JSON "event" = %v, want "topology.delta" — the frozen event name (got %s)`, generic["event"], got)
	}
}
