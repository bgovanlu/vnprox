package findings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func testFinding() Finding {
	return Finding{
		ID: "health:bond_slave_down|bond:pve1:bond0", Source: SourceHealth, Check: "bond_slave_down",
		Severity: SeverityWarning, Detail: "bond0's slave eth1 is down", Nodes: []string{"pve1"},
		Refs: []string{"bond:pve1:bond0"}, DocsLink: "docs/features/monitoring.md#5-health-checks",
	}
}

// --- AC1: payload shape per target kind -------------------------------

func TestPayloadFor_TargetShapes(t *testing.T) {
	f := testFinding()

	tests := []struct {
		checkBody  func(t *testing.T, body []byte)
		wantHeader map[string]string
		name       string
		wantCT     string
		rule       AlertRule
	}{
		{
			name: "generic is the Finding shape verbatim",
			rule: AlertRule{ID: "r1", TargetKind: TargetGeneric, TargetURL: "https://example.com/hook", TargetSecret: "s3cr3t"},
			checkBody: func(t *testing.T, body []byte) {
				var got Finding
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if !reflect.DeepEqual(got, f) {
					t.Errorf("generic body = %+v, want %+v", got, f)
				}
			},
			wantCT:     "application/json",
			wantHeader: map[string]string{"Authorization": "Bearer s3cr3t"},
		},
		{
			name: "gotify shapes title/message/priority",
			rule: AlertRule{ID: "r2", TargetKind: TargetGotify, TargetURL: "https://gotify.example/message", TargetSecret: "gotify-token"},
			checkBody: func(t *testing.T, body []byte) {
				var got gotifyMessage
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if got.Title == "" || got.Message == "" {
					t.Errorf("gotify payload missing title/message: %+v", got)
				}
				if got.Priority != severityPriority(SeverityWarning) {
					t.Errorf("gotify priority = %d, want %d", got.Priority, severityPriority(SeverityWarning))
				}
			},
			wantCT:     "application/json",
			wantHeader: map[string]string{"X-Gotify-Key": "gotify-token"},
		},
		{
			name: "ntfy shapes plain-text body + Title/Priority/Tags headers",
			rule: AlertRule{ID: "r3", TargetKind: TargetNtfy, TargetURL: "https://ntfy.sh/vnprox-alerts", TargetSecret: "ntfy-token"},
			checkBody: func(t *testing.T, body []byte) {
				if len(body) == 0 {
					t.Errorf("ntfy body is empty")
				}
			},
			wantCT: "text/plain; charset=utf-8",
			wantHeader: map[string]string{
				"Authorization": "Bearer ntfy-token",
				"Priority":      strconv.Itoa(ntfyPriority(SeverityWarning)),
			},
		},
		{
			name: "slack uses incoming-webhook {text} shape, ignores secret",
			rule: AlertRule{ID: "r4", TargetKind: TargetSlack, TargetURL: "https://hooks.slack.com/services/x", TargetSecret: "unused"},
			checkBody: func(t *testing.T, body []byte) {
				var got slackMessage
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if got.Text == "" {
					t.Errorf("slack payload text is empty")
				}
			},
			wantCT:     "application/json",
			wantHeader: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, ct, headers, err := PayloadFor(tt.rule, f, TransitionNew)
			if err != nil {
				t.Fatalf("PayloadFor: %v", err)
			}
			if ct != tt.wantCT {
				t.Errorf("contentType = %q, want %q", ct, tt.wantCT)
			}
			tt.checkBody(t, body)
			for k, want := range tt.wantHeader {
				if got := headers[k]; got != want {
					t.Errorf("header %s = %q, want %q", k, got, want)
				}
			}
			if headers["X-Vnprox-Transition"] != "new" {
				t.Errorf("X-Vnprox-Transition = %q, want %q", headers["X-Vnprox-Transition"], "new")
			}
			// Slack must not carry the secret anywhere.
			if tt.rule.TargetKind == TargetSlack {
				if _, ok := headers["Authorization"]; ok {
					t.Errorf("slack request must not set Authorization")
				}
			}
		})
	}
}

func TestPayloadFor_UnknownTargetKind(t *testing.T) {
	_, _, _, err := PayloadFor(AlertRule{TargetKind: "carrier-pigeon"}, testFinding(), TransitionNew)
	if err == nil {
		t.Fatal("PayloadFor with unknown target kind: want error, got nil")
	}
}

// --- Deliver against a captured request (AC1's "captured request on the
// test webhook receiver") -----------------------------------------------

func TestDeliver_SendsDocumentedShapePerTarget(t *testing.T) {
	tests := []struct {
		name       string
		targetKind string
	}{
		{"generic", TargetGeneric},
		{"gotify", TargetGotify},
		{"ntfy", TargetNtfy},
		{"slack", TargetSlack},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod string
			var capturedBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				capturedBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			rule := AlertRule{ID: "r-" + tt.name, TargetKind: tt.targetKind, TargetURL: srv.URL, TargetSecret: "secret"}
			if err := Deliver(context.Background(), srv.Client(), rule, testFinding(), TransitionEscalated); err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %s, want POST", gotMethod)
			}
			if len(capturedBody) == 0 {
				t.Errorf("captured body is empty")
			}
		})
	}
}

func TestDeliver_NonSuccessStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rule := AlertRule{ID: "r1", TargetKind: TargetGeneric, TargetURL: srv.URL}
	if err := Deliver(context.Background(), srv.Client(), rule, testFinding(), TransitionNew); err == nil {
		t.Fatal("Deliver against a 500: want error, got nil")
	}
}

// --- AC2: routing filters -----------------------------------------------

type fakeRuleProvider struct {
	err   error
	rules []AlertRule
}

func (f fakeRuleProvider) AlertRules(context.Context) ([]AlertRule, error) {
	return f.rules, f.err
}

type recordedDelivery struct {
	AlertDelivery
}

type fakeRecorder struct {
	rows []recordedDelivery
	mu   sync.Mutex
}

func (r *fakeRecorder) RecordDelivery(_ context.Context, d AlertDelivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, recordedDelivery{d})
	return nil
}

func (r *fakeRecorder) snapshot() []recordedDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedDelivery, len(r.rows))
	copy(out, r.rows)
	return out
}

func TestWebhookNotifier_RoutingFilters(t *testing.T) {
	errorFinding := Finding{ID: "f1", Source: SourceHealth, Severity: SeverityError, Check: "c1", Detail: "d"}
	warningFinding := Finding{ID: "f2", Source: SourceDrift, Severity: SeverityWarning, Check: "c2", Detail: "d"}

	tests := []struct {
		name    string
		rule    AlertRule
		finding Finding
		want    bool
	}{
		{"no filters matches anything", AlertRule{Enabled: true}, errorFinding, true},
		{"severity filter matches", AlertRule{Enabled: true, SeverityFilter: []string{"error"}}, errorFinding, true},
		{"severity filter excludes", AlertRule{Enabled: true, SeverityFilter: []string{"error"}}, warningFinding, false},
		{"source filter matches", AlertRule{Enabled: true, SourceFilter: []string{"health"}}, errorFinding, true},
		{"source filter excludes", AlertRule{Enabled: true, SourceFilter: []string{"health"}}, warningFinding, false},
		{"both filters ANDed, both match", AlertRule{Enabled: true, SourceFilter: []string{"health"}, SeverityFilter: []string{"error"}}, errorFinding, true},
		{"both filters ANDed, one mismatches", AlertRule{Enabled: true, SourceFilter: []string{"health"}, SeverityFilter: []string{"warning"}}, errorFinding, false},
		{"disabled rule never matches", AlertRule{Enabled: false}, errorFinding, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			rule := tt.rule
			rule.ID = "r1"
			rule.TargetKind = TargetGeneric
			rule.TargetURL = srv.URL

			rec := &fakeRecorder{}
			n := NewWebhookNotifier(WebhookNotifierConfig{
				Rules:    fakeRuleProvider{rules: []AlertRule{rule}},
				Recorder: rec,
				Client:   srv.Client(),
				Sleep:    func(context.Context, time.Duration) {},
			})
			if err := n.Notify(context.Background(), tt.finding, TransitionNew); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			gotFired := requests > 0
			if gotFired != tt.want {
				t.Errorf("fired = %v, want %v (requests=%d)", gotFired, tt.want, requests)
			}
		})
	}
}

// --- AC3: retry/backoff --------------------------------------------------

func TestWebhookNotifier_RetriesThenDelivers(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	failUntil := 3 // fail attempts 1 and 2, succeed on 3.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < failUntil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := &fakeRecorder{}
	rule := AlertRule{ID: "r1", Enabled: true, TargetKind: TargetGeneric, TargetURL: srv.URL}
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules:    fakeRuleProvider{rules: []AlertRule{rule}},
		Recorder: rec,
		Client:   srv.Client(),
		Sleep:    func(context.Context, time.Duration) {}, // no real waiting in tests
	})

	if err := n.Notify(context.Background(), testFinding(), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	mu.Lock()
	gotAttempts := attempts
	mu.Unlock()
	if gotAttempts != failUntil {
		t.Errorf("server saw %d attempts, want %d", gotAttempts, failUntil)
	}

	rows := rec.snapshot()
	if len(rows) != failUntil {
		t.Fatalf("recorded %d delivery rows, want %d", len(rows), failUntil)
	}
	for i, row := range rows[:len(rows)-1] {
		if row.Status != StatusRetrying {
			t.Errorf("row %d status = %q, want %q", i, row.Status, StatusRetrying)
		}
	}
	last := rows[len(rows)-1]
	if last.Status != StatusDelivered {
		t.Errorf("final row status = %q, want %q", last.Status, StatusDelivered)
	}
	if last.Attempt != failUntil {
		t.Errorf("final row attempt = %d, want %d", last.Attempt, failUntil)
	}
}

func TestWebhookNotifier_ExhaustsRetriesThenFails(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rec := &fakeRecorder{}
	rule := AlertRule{ID: "r1", Enabled: true, TargetKind: TargetGeneric, TargetURL: srv.URL}
	const maxAttempts = 3
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules:       fakeRuleProvider{rules: []AlertRule{rule}},
		Recorder:    rec,
		Client:      srv.Client(),
		Sleep:       func(context.Context, time.Duration) {},
		MaxAttempts: maxAttempts,
	})

	err := n.Notify(context.Background(), testFinding(), TransitionNew)
	if err == nil {
		t.Fatal("Notify against an always-500 receiver: want error, got nil")
	}

	mu.Lock()
	gotAttempts := attempts
	mu.Unlock()
	if gotAttempts != maxAttempts {
		t.Errorf("server saw %d attempts, want exactly %d (never retried past the max)", gotAttempts, maxAttempts)
	}

	rows := rec.snapshot()
	if len(rows) != maxAttempts {
		t.Fatalf("recorded %d delivery rows, want %d", len(rows), maxAttempts)
	}
	last := rows[len(rows)-1]
	if last.Status != StatusFailed {
		t.Errorf("final row status = %q, want %q", last.Status, StatusFailed)
	}
	if last.Error == "" {
		t.Errorf("final row Error is empty, want the delivery error recorded")
	}
}

func TestWebhookNotifier_DisabledRuleNeverFires(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := AlertRule{ID: "r1", Enabled: false, TargetKind: TargetGeneric, TargetURL: srv.URL}
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules:  fakeRuleProvider{rules: []AlertRule{rule}},
		Client: srv.Client(),
		Sleep:  func(context.Context, time.Duration) {},
	})
	if err := n.Notify(context.Background(), testFinding(), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if requests != 0 {
		t.Errorf("disabled rule fired %d requests, want 0", requests)
	}
}

func TestWebhookNotifier_RuleProviderErrorIsReturned(t *testing.T) {
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules: fakeRuleProvider{err: errors.New("boom")},
	})
	if err := n.Notify(context.Background(), testFinding(), TransitionNew); err == nil {
		t.Fatal("Notify with a failing rule provider: want error, got nil")
	}
}

func TestBackoffDuration_DoublesAndCaps(t *testing.T) {
	base := 1 * time.Second
	capD := 8 * time.Second
	got := []time.Duration{
		backoffDuration(base, capD, 1),
		backoffDuration(base, capD, 2),
		backoffDuration(base, capD, 3),
		backoffDuration(base, capD, 4),
		backoffDuration(base, capD, 5),
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("backoffDuration(attempt=%d) = %v, want %v", i+1, got[i], want[i])
		}
	}
}

func TestWebhookNotifier_NilRulesIsNoop(t *testing.T) {
	n := NewWebhookNotifier(WebhookNotifierConfig{})
	if err := n.Notify(context.Background(), testFinding(), TransitionNew); err != nil {
		t.Errorf("Notify with no Rules provider: got %v, want nil", err)
	}
}
