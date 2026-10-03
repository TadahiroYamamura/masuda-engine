package engine

import (
	"context"
	"slices"
	"strings"
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

// checkContinues は、fixerが直前に終わったimplementer（developの最終の修正では
// 最後のステップかreworkのもの）に続けて渡され、同梱の他の役は誰も続けない
// ことを確かめる。
func checkContinues(t *testing.T, i int, task *AgentTask, lastImpl string) {
	t.Helper()
	want := ""
	if task.Agent.Name == "fixer" {
		want = lastImpl
		if want == "" {
			t.Fatalf("step %d: a fixer ran before any implementer", i)
		}
	}
	if task.Continues != want {
		t.Fatalf("step %d: %s/%s Continues = %q, want %q", i, task.Workflow, task.Node, task.Continues, want)
	}
}

// TestBundledDevelopReviewsInOneSessionPerRole walks the bundled develop
// with three steps: the first step's interim review is clean and goes
// straight to its commit, the second goes through one fix and recheck, the
// third has nothing the fixer may fix. The final review's clean still passes
// the checker, which sends it back once; the fixer's cannot_fix hands the
// rest to the report. After a rework, a clean review with nothing to fix
// skips the recheck. Every implementer, the rework included, reads the
// investigation.
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

	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]},{"number":2,"title":"t2","description":"b","tests":[],"files":["a.go"]},{"number":3,"title":"t3","description":"c","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[]}`
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
			{Key: "3", Input: "step", Content: []byte(`{"number":3,"title":"t3","description":"c","tests":[],"files":["a.go"]}`)},
		},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}

	type step struct{ at, outcome string }
	var lastImpl string
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
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/implement/interim-review/interim-review", "done"},
		{"workflows/implement/interim-review/interim-check", "done"},
		{"workflows/implement/build-step/fix", "nothing_to_fix"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "inaccurate"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/fix", "cannot_fix"},
		{"workflows/develop/report", "done"},
		{"gate:review", "rejected"},
		{"workflows/develop/rework", "done"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "clean"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "nothing_to_fix"},
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
		if s.Task.Agent.Name == "implementer" {
			if _, ok := s.Task.Inputs["investigation"]; !ok {
				t.Fatalf("step %d: the implementer must read the investigation, got %+v", i, s.Task.Inputs)
			}
		}
		checkContinues(t, i, s.Task, lastImpl)
		if s.Task.Agent.Name == "implementer" {
			lastImpl = s.Occurrence
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
	if !slices.Equal(scopes, []string{"step", "step", "step", "plan", "plan", "plan"}) {
		t.Fatalf("want three step commits, the review commit, the rework commit and the second review commit, got %v", scopes)
	}
}

// TestBundledFixPlansInOneSessionAndPublishes walks the bundled fix with one
// step: the first final review goes through the fixer and the rechecker, the
// review gate sends it back to a rework, and the second review is clean and
// goes straight to the review commit.
func TestBundledFixPlansInOneSessionAndPublishes(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	for path := range set.Workflows {
		reach, err := set.Reachable(path)
		if err != nil {
			t.Fatalf("Reachable(%s): %v", path, err)
		}
		for _, r := range reach {
			if r != path {
				called[r] = true
			}
		}
	}
	if _, ok := set.Workflows["workflows/fix"]; !ok || called["workflows/fix"] {
		t.Fatal("workflows/fix must be a bundled root")
	}
	if !called["workflows/fix/build-step"] {
		t.Fatal("workflows/fix/build-step must be called from workflows/fix")
	}
	if p := set.Check("workflows/fix"); len(p) != 0 {
		t.Fatalf("Check fix: %+v", p)
	}

	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":["x"],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[]}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":  []byte("i"),
			"plan":           []byte(plan),
			"findings":       []byte(`[]`),
			"commit-message": []byte("fix: x"),
		}},
		items: []Item{
			{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":["x"],"files":["a.go"]}`)},
		},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/fix", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}

	type step struct{ at, outcome string }
	var lastImpl string
	script := []step{
		{"workflows/fix/plan", "done"},
		{"gate:plan", "approved"},
		{"workflows/fix/build-step/implement", "done"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/fix/fix", "done"},
		{"workflows/fix/recheck", "done"},
		{"gate:review", "rejected"},
		{"workflows/fix/rework", "done"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "clean"},
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
		if s.Task.Agent.Name == "implementer" {
			if _, ok := s.Task.Inputs["investigation"]; !ok {
				t.Fatalf("step %d: the implementer must read the investigation, got %+v", i, s.Task.Inputs)
			}
		}
		checkContinues(t, i, s.Task, lastImpl)
		if s.Task.Agent.Name == "implementer" {
			lastImpl = s.Occurrence
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
	if !slices.Equal(scopes, []string{"step", "plan", "plan", "plan"}) {
		t.Fatalf("want the step commit, the review commit, the rework commit and the second review commit, got %v", scopes)
	}
}

// TestBundledBuildStepDisputeEndsAtInterimGate は、fixerが反論し続け、
// recheckerが指摘を維持し続ける1ステップを歩かせる。3回の修正の後、4回目の
// 進入がexhaustedになりinterimゲートへ渡る。どの修正もそのステップの
// implementerに続けて渡され、反論（disputed・response）が指摘の台帳に届く。
func TestBundledBuildStepDisputeEndsAtInterimGate(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/develop"); len(p) != 0 {
		t.Fatalf("Check develop: %+v", p)
	}
	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[]}`
	finding := `{"id":"f1","file":"a.go","line":3,"severity":"中","autofix":true,"message":"m"}`
	disputed := `{"id":"f1","file":"a.go","line":3,"severity":"中","autofix":true,"message":"m","disputed":true,"response":"計画のステップ1でこの形にすると決めている"}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":  []byte("i"),
			"plan":           []byte(plan),
			"findings":       []byte("[" + finding + "]"),
			"commit-message": []byte("feat: x"),
		}},
		items:   []Item{{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}`)}},
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
		{"workflows/implement/interim-review/interim-review", "done"},
		{"workflows/implement/interim-review/interim-check", "done"},
		{"workflows/implement/build-step/fix", "done"},
		{"workflows/implement/build-step/recheck", "unresolved"},
		{"workflows/implement/build-step/fix", "done"},
		{"workflows/implement/build-step/recheck", "unresolved"},
		{"workflows/implement/build-step/fix", "done"},
		{"workflows/implement/build-step/recheck", "unresolved"},
		{"gate:interim", "approved"},
		{"workflows/review/perspectives/review", "clean"},
	}
	var lastImpl, fixOcc string
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
		checkContinues(t, i, s.Task, lastImpl)
		switch s.Task.Agent.Name {
		case "implementer":
			lastImpl = s.Occurrence
		case "fixer":
			r.outputs["findings"] = []byte("[" + disputed + "]")
			fixOcc = s.Occurrence
		case "rechecker":
			if in := s.Task.Inputs["findings"]; in.Occurrence != fixOcc {
				t.Fatalf("step %d: the rechecker must read the fixer's findings, got %+v", i, in)
			}
			got := string(r.data[fixOcc+"/findings"])
			if !strings.Contains(got, `"disputed":true`) || !strings.Contains(got, `"response"`) {
				t.Fatalf("step %d: the dispute did not reach the ledger: %s", i, got)
			}
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
}
