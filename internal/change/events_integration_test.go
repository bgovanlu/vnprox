package change_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestEventsTopic_ObservesChangesetStatusThenAuditAppended is T-1104's
// acceptance criterion 3: apply a changeset over the normal service API
// (internal/change.Service, real pvemock-backed harness — this package's
// own newHarness/newHarness's fixtureSingleNode) while an automation-
// scoped "events" WS subscriber is connected. It must observe
// changeset.status transitions (internal/change.Broadcaster, reused
// unchanged — the real production Service.broadcastStatus call, not a
// fake) followed by audit.appended (T-1104's new producer, wired here
// exactly the way cmd/vnproxd/events.go's wireAuditAppendedEvents wires
// production: store.AuditRepo.SetOnAppend -> topology.Service.Broadcast)
// for the same changeset, in order — this is the one acceptance criterion
// that requires the real Service + real AuditRepo + real Hub wired
// together, not each package's own isolated unit tests.
func TestEventsTopic_ObservesChangesetStatusThenAuditAppended(t *testing.T) {
	topoSvc := topology.NewService(inventory.NewGraph(), nil)

	// Wire audit.appended exactly like cmd/vnproxd/events.go's
	// wireAuditAppendedEvents does: every audit_log append (via the same
	// *store.AuditRepo instance change.Config.Audit uses) broadcasts
	// directly onto "events".
	wireTestAuditAppended := func(audit *store.AuditRepo) {
		audit.SetOnAppend(func(e store.AuditEntry) {
			evt := map[string]any{"event": "audit.appended", "action": e.Action}
			if e.ChangesetID.Valid {
				evt["changesetId"] = e.ChangesetID.String
			}
			data, err := json.Marshal(evt)
			if err != nil {
				t.Errorf("marshaling audit.appended: %v", err)
				return
			}
			topoSvc.Broadcast("events", data)
		})
	}

	h := newHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.WS = topoSvc
	})
	wireTestAuditAppended(h.auditRepo)

	// Serve the real Hub behind a fake auth middleware granting the
	// automation scope (mirroring internal/topology's own
	// serveWSWithIdentity test helper) — real bearer-token auth is
	// internal/auth's own tested responsibility (bearer_test.go); this
	// test only needs a connection the Hub itself treats as
	// automation-scoped.
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := auth.Identity{Caps: map[string]auth.Capabilities{"": {Automation: true}}}
		topoSvc.ServeWS(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	}))
	defer wsServer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer subCancel()
	if err := conn.Write(subCtx, websocket.MessageText, []byte(`{"subscribe":["events"]}`)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Give the server a moment to process the subscribe frame (no ack in
	// the protocol) before driving the changeset through its lifecycle.
	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Validate(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// Drain events until both the terminal changeset.status(committed) and
	// the changeset.confirm audit.appended for this changeset have been
	// seen, recording the order they arrived in.
	type seen struct {
		kind string // "status:<status>" or "audit:<action>"
		idx  int
	}
	var order []seen
	sawCommittedStatus, sawConfirmAudit := -1, -1
	deadline := time.Now().Add(5 * time.Second)
	for (sawCommittedStatus == -1 || sawConfirmAudit == -1) && time.Now().Before(deadline) {
		readCtx, readCancel := context.WithTimeout(context.Background(), 1*time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal event %s: %v", data, err)
		}
		switch msg["event"] {
		case "changeset.status":
			if msg["id"] == cs.ID {
				order = append(order, seen{kind: "status:" + msg["status"].(string), idx: len(order)})
				if msg["status"] == "committed" {
					sawCommittedStatus = len(order) - 1
				}
			}
		case "audit.appended":
			if msg["changesetId"] == cs.ID {
				order = append(order, seen{kind: "audit:" + msg["action"].(string), idx: len(order)})
				if msg["action"] == "changeset.confirm" {
					sawConfirmAudit = len(order) - 1
				}
			}
		}
	}

	if sawCommittedStatus == -1 {
		t.Fatalf("never observed changeset.status(committed) for %s; order so far: %+v", cs.ID, order)
	}
	if sawConfirmAudit == -1 {
		t.Fatalf("never observed audit.appended(changeset.confirm) for %s; order so far: %+v", cs.ID, order)
	}
	if sawCommittedStatus >= sawConfirmAudit {
		t.Fatalf("changeset.status(committed) at index %d did not precede audit.appended(changeset.confirm) at index %d; full order: %+v", sawCommittedStatus, sawConfirmAudit, order)
	}
}
