package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func fixedClock(at time.Time) Clock {
	return func() time.Time { return at }
}

// TestLadder_RunsEveryStepInOrder_NeverShortCircuits is T-1307 AC1/AC3's
// core contract: every registered step runs (in registration order) for
// every request, regardless of an earlier step's skip or error — the
// ladder never stops early.
func TestLadder_RunsEveryStepInOrder_NeverShortCircuits(t *testing.T) {
	var order []string
	step := func(name string, out Outcome) Step {
		return Step{Name: name, Run: func(ctx context.Context, req Request) Outcome {
			order = append(order, name)
			return out
		}}
	}
	l := NewLadder([]Step{
		step("config-check", Outcome{Eligible: true, Summary: "ok"}),
		step("live-probe", Outcome{Eligible: false, SkipReason: "no live PVE session"}),
		step("guest-interior", Outcome{Err: errors.New("boom")}),
		step("conntrack", Outcome{Eligible: true, Summary: "3 connections"}),
		step("capture", Outcome{Eligible: false, SkipReason: "escalation not requested"}),
	}, fixedClock(time.Unix(1000, 0)))

	res := l.Run(context.Background(), Request{TargetRef: "guest-nic:pve1:100/net0"})

	wantOrder := []string{"config-check", "live-probe", "guest-interior", "conntrack", "capture"}
	if len(order) != len(wantOrder) {
		t.Fatalf("ran steps = %v, want %v", order, wantOrder)
	}
	for i, n := range wantOrder {
		if order[i] != n {
			t.Fatalf("step %d ran %q, want %q (order=%v)", i, order[i], n, order)
		}
	}

	if len(res.Steps) != 5 {
		t.Fatalf("len(res.Steps) = %d, want 5", len(res.Steps))
	}
	wantStatus := []StepStatus{StatusRan, StatusSkipped, StatusError, StatusRan, StatusSkipped}
	for i, want := range wantStatus {
		if res.Steps[i].Status != want {
			t.Errorf("step %d (%s) status = %q, want %q", i, res.Steps[i].Name, res.Steps[i].Status, want)
		}
		if res.Steps[i].RanAt != 1000 {
			t.Errorf("step %d RanAt = %d, want 1000", i, res.Steps[i].RanAt)
		}
	}
	if res.Steps[1].Summary != "no live PVE session" {
		t.Errorf("skipped step summary = %q, want the stated skip reason", res.Steps[1].Summary)
	}
	if res.Steps[2].Summary != "boom" {
		t.Errorf("errored step summary = %q, want the error's message", res.Steps[2].Summary)
	}
	if res.Target != "guest-nic:pve1:100/net0" {
		t.Errorf("res.Target = %q, want the request's TargetRef echoed back", res.Target)
	}
}

// TestLadder_IneligibleStepIsSkippedNeverErrored covers AC1's other half:
// a target a step doesn't apply to (e.g. no guest-interior step for a bare
// bridge target) is reported StatusSkipped with a stated reason, never
// StatusError.
func TestLadder_IneligibleStepIsSkippedNeverErrored(t *testing.T) {
	l := NewLadder([]Step{
		{Name: "guest-interior", Run: func(ctx context.Context, req Request) Outcome {
			return Outcome{Eligible: false, SkipReason: "no guest interior for a non-guest target"}
		}},
	}, fixedClock(time.Unix(1, 0)))

	res := l.Run(context.Background(), Request{TargetRef: "bridge:pve1:vmbr0"})
	if got := res.Steps[0].Status; got != StatusSkipped {
		t.Fatalf("status = %q, want skipped", got)
	}
	if res.Steps[0].Summary != "no guest interior for a non-guest target" {
		t.Fatalf("summary = %q, want the stated reason", res.Steps[0].Summary)
	}
}

func TestLadder_Verdict_ConfidenceBranches(t *testing.T) {
	tests := []struct {
		name           string
		wantConfidence string
		wantFixRef     string
		steps          []Step
		wantFindingIDs []string
	}{
		{
			name: "nothing ran",
			steps: []Step{
				{Name: "a", Run: func(context.Context, Request) Outcome { return Outcome{Eligible: false, SkipReason: "n/a"} }},
			},
			wantConfidence: ConfidenceNone,
			wantFindingIDs: []string{},
		},
		{
			name: "an error present dominates",
			steps: []Step{
				{Name: "a", Run: func(context.Context, Request) Outcome { return Outcome{Eligible: true, Summary: "ok"} }},
				{Name: "b", Run: func(context.Context, Request) Outcome { return Outcome{Err: errors.New("x")} }},
			},
			wantConfidence: ConfidenceLow,
			wantFindingIDs: []string{},
		},
		{
			name: "ran with related findings",
			steps: []Step{
				{Name: "a", Run: func(context.Context, Request) Outcome {
					return Outcome{Eligible: true, Summary: "diverges", FindingIDs: []string{"probe:sim_divergence|x", "probe:sim_divergence|x"}}
				}},
			},
			wantConfidence: ConfidenceHigh,
			wantFindingIDs: []string{"probe:sim_divergence|x"},
		},
		{
			name: "ran clean, no findings",
			steps: []Step{
				{Name: "a", Run: func(context.Context, Request) Outcome { return Outcome{Eligible: true, Summary: "ok"} }},
			},
			wantConfidence: ConfidenceMedium,
			wantFindingIDs: []string{},
		},
		{
			name: "suggested fix ref carried through from the first step that offers one",
			steps: []Step{
				{Name: "a", Run: func(context.Context, Request) Outcome {
					return Outcome{Eligible: true, Summary: "ok", SuggestedFixRef: "health:bondslave|bond:pve1:bond0"}
				}},
				{Name: "b", Run: func(context.Context, Request) Outcome {
					return Outcome{Eligible: true, Summary: "ok", SuggestedFixRef: "health:other|x"}
				}},
			},
			wantConfidence: ConfidenceMedium,
			wantFindingIDs: []string{},
			wantFixRef:     "health:bondslave|bond:pve1:bond0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLadder(tt.steps, fixedClock(time.Unix(1, 0)))
			res := l.Run(context.Background(), Request{TargetRef: "t"})
			if res.Verdict.Confidence != tt.wantConfidence {
				t.Errorf("confidence = %q, want %q", res.Verdict.Confidence, tt.wantConfidence)
			}
			if len(res.Verdict.LinkedFindingIDs) != len(tt.wantFindingIDs) {
				t.Errorf("linkedFindingIds = %v, want %v", res.Verdict.LinkedFindingIDs, tt.wantFindingIDs)
			}
			for i, id := range tt.wantFindingIDs {
				if res.Verdict.LinkedFindingIDs[i] != id {
					t.Errorf("linkedFindingIds[%d] = %q, want %q", i, res.Verdict.LinkedFindingIDs[i], id)
				}
			}
			if res.Verdict.SuggestedFixRef != tt.wantFixRef {
				t.Errorf("suggestedFixRef = %q, want %q", res.Verdict.SuggestedFixRef, tt.wantFixRef)
			}
			if res.Verdict.Summary == "" {
				t.Error("verdict summary must never be empty")
			}
		})
	}
}

// TestLadder_EscalateToCaptureNeverImpliedByLadder is a package-level
// sanity check for AC2's contract shape: Ladder itself carries
// req.EscalateToCapture through to every step unchanged (it is the step's
// own job to gate on it, per internal/api/diagnose.go's capture step) —
// this test just confirms the field survives Run() untouched, since the
// capture-specific "zero capture calls" regression lives in
// internal/api's own test (it needs a real capture.Coordinator spy).
func TestLadder_EscalateToCaptureFlowsThroughToSteps(t *testing.T) {
	var seen []bool
	l := NewLadder([]Step{
		{Name: "capture", Run: func(ctx context.Context, req Request) Outcome {
			seen = append(seen, req.EscalateToCapture)
			return Outcome{Eligible: false, SkipReason: "n/a"}
		}},
	}, fixedClock(time.Unix(1, 0)))

	l.Run(context.Background(), Request{TargetRef: "t", EscalateToCapture: false})
	l.Run(context.Background(), Request{TargetRef: "t", EscalateToCapture: true})

	if len(seen) != 2 || seen[0] != false || seen[1] != true {
		t.Fatalf("EscalateToCapture seen by step = %v, want [false true]", seen)
	}
}

// TestResult_JSONSchema_Stable is the "verdict-shape stability" contract
// test (T-1307 AC4): the exact field set/names/nesting of Result, byte for
// byte, since this is the scaffolding T-1701's MCP AI operator drives next
// arc — a field rename here is a breaking API change, not a refactor.
func TestResult_JSONSchema_Stable(t *testing.T) {
	res := Result{
		Target: "guest-nic:pve1:100/net0",
		Steps: []StepResult{
			{Name: "config-check", Status: StatusRan, Summary: "simulated path to gateway 10.0.0.1: allow", Detail: map[string]any{"verdict": "allow"}, RanAt: 1000},
			{Name: "live-probe", Status: StatusSkipped, Summary: "no live PVE session available", RanAt: 1000},
			{Name: "guest-interior", Status: StatusSkipped, Summary: "no guest interior for a non-guest target", RanAt: 1000},
			{Name: "conntrack", Status: StatusRan, Summary: "3 connection(s) for guest guest:pve1:100", Detail: map[string]any{"items": []any{}}, RanAt: 1000},
			{Name: "capture", Status: StatusSkipped, Summary: "capture escalation was not requested for this ladder run", RanAt: 1000},
		},
		Verdict: Verdict{
			Summary:          "2 of 5 step(s) ran; no related findings surfaced",
			Confidence:       ConfidenceMedium,
			LinkedFindingIDs: []string{},
		},
	}

	got, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, field := range []string{"target", "steps", "verdict"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("Result JSON missing top-level field %q (got %v)", field, generic)
		}
	}
	steps, ok := generic["steps"].([]any)
	if !ok || len(steps) != 5 {
		t.Fatalf("steps = %v, want a 5-element array", generic["steps"])
	}
	step0, ok := steps[0].(map[string]any)
	if !ok {
		t.Fatalf("steps[0] not an object: %v", steps[0])
	}
	for _, field := range []string{"name", "status", "summary", "ranAt"} {
		if _, has := step0[field]; !has {
			t.Errorf("StepResult JSON missing field %q (got %v)", field, step0)
		}
	}
	if _, has := step0["detail"]; !has {
		t.Errorf("a ran step's detail must be present (got %v)", step0)
	}
	step1, ok := steps[1].(map[string]any)
	if !ok {
		t.Fatalf("steps[1] not an object: %v", steps[1])
	}
	if _, present := step1["detail"]; present {
		t.Errorf("a skipped step's detail must be omitted, got %v", step1["detail"])
	}

	verdict, ok := generic["verdict"].(map[string]any)
	if !ok {
		t.Fatalf("verdict not an object: %v", generic["verdict"])
	}
	for _, field := range []string{"summary", "confidence", "linkedFindingIds"} {
		if _, ok := verdict[field]; !ok {
			t.Errorf("Verdict JSON missing field %q (got %v)", field, verdict)
		}
	}
	if _, present := verdict["suggestedFixRef"]; present {
		t.Errorf("an empty suggestedFixRef must be omitted, got %v", verdict)
	}
}

func TestSortedUnique(t *testing.T) {
	got := sortedUnique([]string{"b", "a", "", "b", "c", ""})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("sortedUnique = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedUnique = %v, want %v", got, want)
		}
	}
}
