package store

import (
	"context"
	"errors"
	"testing"
)

func newPolicyRepo(t *testing.T) (*PolicySetRepo, context.Context) {
	t.Helper()
	db := openTestDB(t)
	return NewPolicySetRepo(db), context.Background()
}

func TestPolicySetRepo_GetMissingIsNotFound(t *testing.T) {
	repo, ctx := newPolicyRepo(t)
	if _, err := repo.Get(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on an empty table err = %v, want ErrNotFound", err)
	}
}

func TestPolicySetRepo_PutIsAnUpsertPerCluster(t *testing.T) {
	repo, ctx := newPolicyRepo(t)

	local := PolicySet{ClusterID: "", Revision: 1, RulesJSON: `{"rules":[]}`, UpdatedBy: "alice", UpdatedAt: 100}
	other := PolicySet{ClusterID: "dc2", Revision: 5, RulesJSON: `{"rules":[{"id":"x"}]}`, UpdatedBy: "bob", UpdatedAt: 200}
	for _, s := range []PolicySet{local, other} {
		if perr := repo.Put(ctx, s); perr != nil {
			t.Fatalf("Put(%q): %v", s.ClusterID, perr)
		}
	}

	got, err := repo.Get(ctx, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Revision != 1 || got.UpdatedBy != "alice" {
		t.Errorf("local set = %+v, want revision 1 by alice", got)
	}

	// Cluster scoping: writing one cluster's set never disturbs another's.
	got, err = repo.Get(ctx, "dc2")
	if err != nil {
		t.Fatalf("Get(dc2): %v", err)
	}
	if got.Revision != 5 || got.RulesJSON != other.RulesJSON {
		t.Errorf("dc2 set = %+v, want the row it was given", got)
	}

	local.Revision, local.RulesJSON, local.UpdatedBy = 2, `{"rules":[{"id":"y"}]}`, "carol"
	if perr := repo.Put(ctx, local); perr != nil {
		t.Fatalf("Put (update): %v", perr)
	}
	got, err = repo.Get(ctx, "")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Revision != 2 || got.UpdatedBy != "carol" || got.RulesJSON != local.RulesJSON {
		t.Errorf("updated set = %+v, want revision 2 by carol", got)
	}
	if dc2, gerr := repo.Get(ctx, "dc2"); gerr != nil || dc2.Revision != 5 {
		t.Errorf("dc2 set = %+v (err %v), want it untouched at revision 5", dc2, gerr)
	}
}

func TestPolicySetRepo_RecordEvaluationAccumulates(t *testing.T) {
	repo, ctx := newPolicyRepo(t)
	ids := []string{"never", "sometimes"}

	if err := repo.RecordEvaluation(ctx, "", ids, map[string]int{"sometimes": 2}, 1000); err != nil {
		t.Fatalf("RecordEvaluation: %v", err)
	}
	if err := repo.RecordEvaluation(ctx, "", ids, nil, 2000); err != nil {
		t.Fatalf("RecordEvaluation: %v", err)
	}
	if err := repo.RecordEvaluation(ctx, "", ids, map[string]int{"sometimes": 1}, 3000); err != nil {
		t.Fatalf("RecordEvaluation: %v", err)
	}

	stats, err := repo.Stats(ctx, "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	byID := map[string]PolicyRuleStat{}
	for _, s := range stats {
		byID[s.RuleID] = s
	}

	never := byID["never"]
	if never.EvalCount != 3 || never.MatchCount != 0 || never.LastMatchedAt != 0 {
		t.Errorf("never = %+v, want 3 evaluations, 0 matches, never matched", never)
	}
	if never.FirstSeenAt != 1000 {
		t.Errorf("never.FirstSeenAt = %d, want the first evaluation's timestamp 1000", never.FirstSeenAt)
	}

	sometimes := byID["sometimes"]
	if sometimes.EvalCount != 3 || sometimes.MatchCount != 3 {
		t.Errorf("sometimes = %+v, want 3 evaluations and 3 cumulative matches", sometimes)
	}
	if sometimes.LastMatchedAt != 3000 {
		t.Errorf("sometimes.LastMatchedAt = %d, want 3000 (the most recent matching evaluation)", sometimes.LastMatchedAt)
	}
}

func TestPolicySetRepo_ForgetRules(t *testing.T) {
	repo, ctx := newPolicyRepo(t)
	if err := repo.RecordEvaluation(ctx, "", []string{"a", "b"}, map[string]int{"a": 1}, 1000); err != nil {
		t.Fatalf("RecordEvaluation: %v", err)
	}
	if err := repo.ForgetRules(ctx, "", []string{"a"}); err != nil {
		t.Fatalf("ForgetRules: %v", err)
	}
	stats, err := repo.Stats(ctx, "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(stats) != 1 || stats[0].RuleID != "b" {
		t.Fatalf("stats = %+v, want only rule b", stats)
	}

	// A rule id reused later starts from a clean slate rather than
	// inheriting the retired rule's history.
	if rerr := repo.RecordEvaluation(ctx, "", []string{"a"}, nil, 5000); rerr != nil {
		t.Fatalf("RecordEvaluation: %v", rerr)
	}
	stats, err = repo.Stats(ctx, "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	for _, s := range stats {
		if s.RuleID == "a" && (s.FirstSeenAt != 5000 || s.MatchCount != 0 || s.EvalCount != 1) {
			t.Errorf("reused rule a = %+v, want a clean slate from 5000", s)
		}
	}
}
