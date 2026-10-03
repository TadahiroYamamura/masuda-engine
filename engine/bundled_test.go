package engine

import (
	"context"
	"slices"
	"testing"
)

type bundledRunner struct {
	e5Runner
	published int
}

func (r *bundledRunner) RunCommand(context.Context, CommandTask) (CommandResult, error) {
	return CommandResult{}, nil
}
func (r *bundledRunner) Publish(context.Context, PublishRequest) error { r.published++; return nil }

// TestBundledDevelopReviewsInOneSessionPerRole walks the bundled develop
// with two steps: the first step's interim review is clean and goes straight
// to its commit, the second step's goes through one fix and recheck. The
// final review is sent back once by the checker, and the fixer's cannot_fix
// hands the rest to the report.
func TestBundledDevelopReviewsInOneSessionPerRole(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"workflows/fix-finding", "workflows/review/perspective-review"} {
		if _, ok := set.Workflows[gone]; ok {
			t.Fatalf("%s is no longer bundled", gone)
		}
	}
	if _, ok := set.Agents["agents/trigger-matcher"]; ok {
		t.Fatal("agents/trigger-matcher is no longer bundled")
	}
	if p := set.Check("workflows/develop"); len(p) != 0 {
		t.Fatalf("Check develop: %+v", p)
	}
	if p := set.Check("workflows/review"); len(p) != 0 {
		t.Fatalf("Check review: %+v", p)
	}

	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]},{"number":2,"title":"t2","description":"b","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[]}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":            []byte("i"),
			"plan":                     []byte(plan),
			"findings":                 []byte(`[]`),
			"cross-cutting-candidates": []byte(`[]`),
			"report":                   []byte("# report"),
			"commit-message":           []byte("feat: x"),
		}},
		items: []Item{
			{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}`)},
			{Key: "2", Input: "step", Content: []byte(`{"number":2,"title":"t2","description":"b","tests":[],"files":["a.go"]}`)},
		},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}

	type step struct{ at, outcome string }
	script := []step{
		{"workflows/develop/investigate", "done"},
		{"workflows/develop/plan", "done"},
		{"gate:plan", "approved"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/implement/interim-review/interim-review", "clean"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/implement/interim-review/interim-review", "done"},
		{"workflows/implement/interim-review/interim-check", "done"},
		{"workflows/implement/build-step/fix", "done"},
		{"workflows/implement/build-step/recheck", "done"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "inaccurate"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/fix", "cannot_fix"},
		{"workflows/develop/report", "done"},
		{"gate:review", "approved"},
	}
	for i, want := range script {
		s := mustAdvance(t, e)
		var at string
		switch s.Kind {
		case StatusAgent:
			at = s.Task.Workflow + "/" + s.Task.Node
		case StatusGate:
			at = "gate:" + s.Gate.Gate
		default:
			t.Fatalf("step %d: want %s, got %+v", i, want.at, s)
		}
		if at != want.at {
			t.Fatalf("step %d: want %s, got %s", i, want.at, at)
		}
		if s.Kind == StatusGate {
			if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: want.outcome, TargetHash: s.Gate.TargetHash}); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			continue
		}
		if s.Task.Node == "interim-review" || s.Task.Node == "interim-check" {
			if in := s.Task.Inputs["diff"]; in.Name != "step-diff" {
				t.Fatalf("step %d: the interim review must read the step diff as diff, got %+v", i, in)
			}
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
	if s := mustAdvance(t, e); s.Kind != StatusDone || r.published != 1 {
		t.Fatalf("want published and done, got %+v published=%d", s, r.published)
	}
	var scopes []string
	for _, c := range r.commits {
		scopes = append(scopes, c.Scope)
	}
	if !slices.Equal(scopes, []string{"step", "step", "plan"}) {
		t.Fatalf("want two step commits and the review commit, got %v", scopes)
	}
}
