package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
)

type e5Runner struct {
	fakeRunner
	items   []Item
	changed []string
	commits []CommitRequest
	diffs   []SnapshotRef
	snaps   []string
}

func (r *e5Runner) Snapshot(_ context.Context, _ RunID, occ string) (SnapshotRef, error) {
	r.snaps = append(r.snaps, occ)
	return SnapshotRef("snap-" + occ), nil
}
func (r *e5Runner) Items(context.Context, RunID, string, DataRef) ([]Item, error) {
	return r.items, nil
}
func (r *e5Runner) ChangedSince(context.Context, RunID, SnapshotRef) ([]string, string, error) {
	return r.changed, "h:" + strings.Join(r.changed, ","), nil
}
func (r *e5Runner) Commit(_ context.Context, c CommitRequest) (CommitResult, error) {
	r.commits = append(r.commits, c)
	return CommitResult{Commit: "c1"}, nil
}
func (r *e5Runner) Diff(_ context.Context, _ RunID, kind DiffKind, from SnapshotRef, into DataRef) error {
	if kind == DiffFromRef {
		r.diffs = append(r.diffs, from)
	}
	r.data[into.Occurrence+"/"+into.Name] = []byte("diff")
	return nil
}

const e5Plan = `{"summary":"s","steps":[{"number":1,"description":"a","files":["a.go"]},{"number":2,"description":"b","files":["b.go"]},{"number":3,"description":"c","files":["c.go"]}],"expected_byproducts":[]}`

func e5Engine(t *testing.T, onIncomplete string) (*Engine, *e5Runner) {
	t.Helper()
	next := "{done: end, gave_up: end:gave_up}"
	if onIncomplete == "continue" {
		next = "{done: end, incomplete: end:partial}"
	}
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md":     agentDef("planner", "Read", []string{"plan"}),
		"agents/implementer.md": agentDef("implementer", "Read, Edit", nil, "give_up"),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
			"  plan: {type: agent, role: agents/planner, inputs: [instructions], next: approve}\n" +
			"  approve: {type: approval, gate: plan, target: plan, next: {approved: steps, rejected: plan}}\n" +
			"  steps: {type: foreach, over: steps, body: workflows/step, on_incomplete: " + onIncomplete + ", next: " + next + "}\n",
		"workflows/step.yaml": "version: 1\ninputs: [step]\nstart: implement\nnodes:\n" +
			"  implement: {type: agent, role: agents/implementer, inputs: [step], max: 1, next: {done: commit, give_up: end:gave_up, exhausted: end:gave_up}}\n" +
			"  commit: {type: commit, scope: step, next: {done: end, rejected: implement}}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r := &e5Runner{fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{"plan": []byte(e5Plan)}}}
	r.items = []Item{
		{Key: "1", Input: "step", Content: []byte(`{"number":1,"description":"a","files":["a.go"]}`), Done: true},
		{Key: "2", Input: "step", Content: []byte(`{"number":2,"description":"b","files":["b.go"]}`)},
		{Key: "3", Input: "step", Content: []byte(`{"number":3,"description":"c","files":["c.go"]}`)},
	}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/x", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	s, _ := e.Advance(ctx, "r")
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash}); err != nil {
		t.Fatal(err)
	}
	return e, r
}

func TestForeachStopSkipsDoneAndRejectedDeviationResetsCount(t *testing.T) {
	e, r := e5Engine(t, "stop")
	ctx := context.Background()
	s, _ := e.Advance(ctx, "r")
	if s.Kind != StatusAgent || !strings.HasSuffix(s.Task.Inputs["step"].Occurrence, ".2") {
		t.Fatalf("the done item 1 is skipped; want item 2's implementer, got %+v", s.Task)
	}
	r.changed = []string{"b.go", "x.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusGate || s.Gate.Gate != GateDeviation || string(s.Gate.Subject) != "x.go" {
		t.Fatalf("want deviation gate on x.go, got %+v", s)
	}
	if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "rejected", Comment: "revert it"}); err != nil {
		t.Fatal(err)
	}
	// max: 1, but a human decided in between: the implementer runs again.
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusAgent || !strings.Contains(s.Task.Feedback, "x.go") || !strings.Contains(s.Task.Feedback, "revert it") {
		t.Fatalf("want implementer again with the rejection as feedback, got %+v", s)
	}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "give_up", "")
	if s, _ = e.Advance(ctx, "r"); s.Kind != StatusDone || s.Outcome != "gave_up" {
		t.Fatalf("stop: the body's end is the foreach's outcome; got %+v", s)
	}
	if len(r.commits) != 0 {
		t.Fatalf("nothing should have been committed: %+v", r.commits)
	}
}

func TestForeachContinueRunsAllAndCommitsWithStepScope(t *testing.T) {
	e, r := e5Engine(t, "continue")
	ctx := context.Background()
	s, _ := e.Advance(ctx, "r")
	_ = e.ReportResult(ctx, "r", s.Occurrence, "give_up", "")
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusAgent || !strings.HasSuffix(s.Task.Inputs["step"].Occurrence, ".3") {
		t.Fatalf("continue: item 3 runs after item 2 gave up, got %+v", s)
	}
	r.changed = []string{"c.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	if s, _ = e.Advance(ctx, "r"); s.Kind != StatusDone || s.Outcome != "partial" {
		t.Fatalf("want incomplete routed to end:partial, got %+v", s)
	}
	if len(r.commits) != 1 || r.commits[0].Step != "3" || !slices.Equal(r.commits[0].Allowed, []string{"c.go"}) || r.commits[0].Message != "c" {
		t.Fatalf("step commit: %+v", r.commits)
	}
}

func TestReadOnlyDeviationRejectedBlocks(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": agentDef("planner", "Read", []string{"plan"}),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
			"  plan: {type: agent, role: agents/planner, inputs: [instructions], next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	r := &e5Runner{fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{"plan": []byte(e5Plan)}}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", []string{"instructions"})
	s, _ := e.Advance(ctx, "r")
	r.changed = []string{"sneaky.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	if st, _ := e.Status(ctx, "r"); st.Kind != StatusPending {
		t.Fatalf("the gate is not open before Advance: %+v", st)
	}
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusGate || s.Gate.Gate != GateDeviation {
		t.Fatalf("want deviation gate, got %+v", s)
	}
	if st, _ := e.Status(ctx, "r"); st.Kind != StatusGate || st.Occurrence != s.Occurrence {
		t.Fatalf("Status differs from Advance: %+v", st)
	}
	_ = e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "rejected"})
	if s, _ = e.Advance(ctx, "r"); s.Kind != StatusBlocked {
		t.Fatalf("want blocked, got %+v", s)
	}
}

func TestFindingsIterationIsFixDiffOrigin(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/reviewer.md": agentDef("reviewer", "Read", []string{"findings"}),
		"agents/fixer.md":    "---\nname: fixer\ndescription: d\ntools: Read\ninputs: [finding, fix-diff]\noutcomes:\n  done: d\n---\nbody\n",
		"workflows/x.yaml": "version: 1\nstart: review\nnodes:\n" +
			"  review: {type: agent, role: agents/reviewer, next: fix}\n" +
			"  fix: {type: foreach, over: findings, body: workflows/fix, next: end}\n",
		"workflows/fix.yaml": "version: 1\ninputs: [finding]\nstart: f\nnodes:\n" +
			"  f: {type: agent, role: agents/fixer, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r := &e5Runner{fakeRunner: fakeRunner{data: map[string][]byte{}, outputs: map[string][]byte{"findings": []byte(`[]`)}}}
	r.items = []Item{{Key: "f1", Input: "finding", Content: []byte(`{"file":"a.go"}`)}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", nil)
	s, _ := e.Advance(ctx, "r")
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusAgent || s.Task.Agent.Name != "fixer" {
		t.Fatalf("want fixer, got %+v", s)
	}
	fid := s.Task.Inputs["finding"].Occurrence
	if len(r.diffs) != 1 || r.diffs[0] != SnapshotRef("snap-"+fid) || s.Task.Inputs["fix-diff"].Name != "fix-diff" {
		t.Fatalf("fix-diff must start from the iteration's snapshot %s: diffs=%v inputs=%+v", fid, r.diffs, s.Task.Inputs)
	}
}
