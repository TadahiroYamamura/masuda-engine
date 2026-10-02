package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMergeAccumulated(t *testing.T) {
	got, err := mergeAccumulated([]byte(`[{"id":"a","v":1},{"v":2}]`), []byte(`[{"id":"a","v":3},{"v":2},{"id":"b"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[{"id":"a","v":3},{"v":2},{"v":2},{"id":"b"}]` {
		t.Fatalf("got %s", got)
	}
	got, err = mergeAccumulated([]byte(`[{"id":"a"},{"id":"b"}]`), []byte(`[{"id":"a","withdrawn":true},{"id":"c","withdrawn":true},{"id":"d","withdrawn":false}]`))
	if err != nil || string(got) != `[{"id":"b"},{"id":"d","withdrawn":false}]` {
		t.Fatalf("withdrawn elements are dropped: %s %v", got, err)
	}
	if got, _ := mergeAccumulated([]byte(`[]`), []byte(`[{"id":"a","withdrawn":true}]`)); string(got) != `[]` {
		t.Fatalf("withdrawing everything leaves []: %s", got)
	}
	if _, err := mergeAccumulated([]byte(`[]`), []byte(`{"id":"a"}`)); err == nil {
		t.Fatal("a non-array write must be refused")
	}
}

func TestFieldMatches(t *testing.T) {
	el := json.RawMessage(`{"autofix":true,"n":2,"s":"x"}`)
	for _, c := range []struct {
		field, value string
		want         bool
	}{
		{"autofix", "true", true}, {"autofix", "false", false}, {"n", "2", true}, {"n", "2.0", true},
		{"n", "3", false}, {"s", "x", true}, {"s", `"x"`, true}, {"s", "y", false}, {"missing", "x", false},
	} {
		if got := fieldMatches(el, c.field, c.value); got != c.want {
			t.Errorf("%s=%s: got %v", c.field, c.value, got)
		}
	}
}

type accRunner struct {
	fakeRunner
	itemsCalled []string
}

func (r *accRunner) Items(_ context.Context, _ RunID, over string, _ DataRef) ([]Item, error) {
	r.itemsCalled = append(r.itemsCalled, over)
	return nil, nil
}
func (r *accRunner) Diff(_ context.Context, _ RunID, _ DiffKind, _ SnapshotRef, into DataRef) error {
	r.data[into.Occurrence+"/"+into.Name] = []byte("diff")
	return nil
}

// A later write that withdraws a finding by its id removes it from what a
// data foreach iterates and from what a reader receives.
func TestWithdrawnFindingLeavesForeachAndReaders(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/reviewer.md": agentDef("reviewer", "Read", []string{"findings"}),
		"agents/fixer.md":    "---\nname: fixer\ndescription: d\ntools: Read\ninputs: [finding]\noutcomes:\n  done: d\n---\nbody\n",
		"agents/reader.md":   "---\nname: reader\ndescription: d\ntools: Read\ninputs: [findings]\noutcomes:\n  done: d\n---\nbody\n",
		"workflows/x.yaml": "version: 1\nstart: review1\nnodes:\n" +
			"  review1: {type: agent, role: agents/reviewer, next: review2}\n" +
			"  review2: {type: agent, role: agents/reviewer, next: fix}\n" +
			"  fix: {type: foreach, over: findings, body: workflows/fix, next: read}\n" +
			"  read: {type: agent, role: agents/reader, next: end}\n",
		"workflows/fix.yaml": "version: 1\ninputs: [finding]\nstart: f\nnodes:\n" +
			"  f: {type: agent, role: agents/fixer, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	r := &accRunner{fakeRunner: fakeRunner{data: map[string][]byte{}, outputs: map[string][]byte{}}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", nil)
	agent := func(name string) *AgentTask {
		t.Helper()
		s, err := e.Advance(ctx, "r")
		if err != nil || s.Kind != StatusAgent || s.Task.Agent.Name != name {
			t.Fatalf("want %s, got %+v (%v)", name, s, err)
		}
		return s.Task
	}
	data := func(ref DataRef) string { return string(r.data[ref.Occurrence+"/"+ref.Name]) }

	r.outputs["findings"] = []byte("[" + finding("a", true) + "," + finding("b", true) + "]")
	_ = e.ReportResult(ctx, "r", agent("reviewer").Occurrence, "done", "")
	gone := strings.Replace(finding("b", true), `"autofix"`, `"withdrawn":true,"autofix"`, 1)
	r.outputs["findings"] = []byte("[" + gone + "]")
	_ = e.ReportResult(ctx, "r", agent("reviewer").Occurrence, "done", "")

	f := agent("fixer")
	if !strings.Contains(data(f.Inputs["finding"]), `"id":"a"`) {
		t.Fatalf("the foreach takes a: %s", data(f.Inputs["finding"]))
	}
	_ = e.ReportResult(ctx, "r", f.Occurrence, "done", "")
	got := data(agent("reader").Inputs["findings"])
	if strings.Contains(got, `"id":"b"`) || !strings.Contains(got, `"id":"a"`) {
		t.Fatalf("the reader sees a only: %s", got)
	}
}

func finding(id string, autofix bool) string {
	b, _ := json.Marshal(map[string]any{"id": id, "file": "a.go", "line": 1, "severity": "中", "autofix": autofix, "message": id})
	return string(b)
}

// Two reviews write findings, each followed by a foreach over the autofix
// ones, then a reader takes them all: the second foreach skips what the
// first finished, and the reader sees every write, rewritten ids replaced.
func TestFindingsAccumulateAndForeachSkipsDone(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/reviewer.md": agentDef("reviewer", "Read", []string{"findings"}),
		"agents/fixer.md":    "---\nname: fixer\ndescription: d\ntools: Read\ninputs: [finding]\noutcomes:\n  done: d\n---\nbody\n",
		"agents/reader.md":   "---\nname: reader\ndescription: d\ntools: Read\ninputs: [findings]\noutcomes:\n  done: d\n---\nbody\n",
		"workflows/x.yaml": "version: 1\nstart: read-early\nnodes:\n" +
			"  read-early: {type: agent, role: agents/reader, next: review1}\n" +
			"  review1: {type: agent, role: agents/reviewer, next: fix1}\n" +
			"  fix1: {type: foreach, over: \"findings[autofix=true]\", body: workflows/fix, next: review2}\n" +
			"  review2: {type: agent, role: agents/reviewer, next: fix2}\n" +
			"  fix2: {type: foreach, over: findings, body: workflows/fix, next: read}\n" +
			"  read: {type: agent, role: agents/reader, next: end}\n",
		"workflows/fix.yaml": "version: 1\ninputs: [finding]\nstart: f\nnodes:\n" +
			"  f: {type: agent, role: agents/fixer, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("accumulated data is always available: %+v", p)
	}
	r := &accRunner{fakeRunner: fakeRunner{data: map[string][]byte{}, outputs: map[string][]byte{}}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", nil)
	agent := func(name string) *AgentTask {
		t.Helper()
		s, err := e.Advance(ctx, "r")
		if err != nil || s.Kind != StatusAgent || s.Task.Agent.Name != name {
			t.Fatalf("want %s, got %+v (%v)", name, s, err)
		}
		return s.Task
	}
	data := func(ref DataRef) string { return string(r.data[ref.Occurrence+"/"+ref.Name]) }

	early := agent("reader")
	if got := data(early.Inputs["findings"]); got != "[]" {
		t.Fatalf("unwritten accumulated data reads []: %q", got)
	}
	_ = e.ReportResult(ctx, "r", early.Occurrence, "done", "")

	r.outputs["findings"] = []byte(`{"id":"x"}`)
	rv := agent("reviewer")
	_ = e.ReportResult(ctx, "r", rv.Occurrence, "done", "")
	if s, _ := e.Advance(ctx, "r"); s.Kind != StatusAgent || s.Task.Agent.Name != "reviewer" || !strings.Contains(s.Task.Feedback, "配列") {
		t.Fatalf("a non-array write is invalid: %+v", s)
	}
	r.outputs["findings"] = []byte("[" + finding("a", true) + "," + finding("b", false) + "]")
	_ = e.ReportResult(ctx, "r", agent("reviewer").Occurrence, "done", "")
	f := agent("fixer")
	if !strings.Contains(data(f.Inputs["finding"]), `"id":"a"`) {
		t.Fatalf("fix1 iterates the autofix finding a: %s", data(f.Inputs["finding"]))
	}
	_ = e.ReportResult(ctx, "r", f.Occurrence, "done", "")

	r.outputs["findings"] = []byte("[" + finding("a", true) + "," + finding("c", true) + "]")
	_ = e.ReportResult(ctx, "r", agent("reviewer").Occurrence, "done", "")
	var fixed []string
	for {
		s, _ := e.Advance(ctx, "r")
		if s.Task.Agent.Name != "fixer" {
			break
		}
		var el struct{ ID string }
		_ = json.Unmarshal([]byte(data(s.Task.Inputs["finding"])), &el)
		fixed = append(fixed, el.ID)
		_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	}
	if !slices.Equal(fixed, []string{"b", "c"}) {
		t.Fatalf("fix2 skips a (done in fix1) and takes b and c: %v", fixed)
	}
	var all []struct{ ID string }
	if err := json.Unmarshal([]byte(data(agent("reader").Inputs["findings"])), &all); err != nil || len(all) != 3 {
		t.Fatalf("the reader sees a, b, c: %v %v", all, err)
	}
	if len(r.itemsCalled) != 0 {
		t.Fatalf("data foreaches do not ask Runner.Items: %v", r.itemsCalled)
	}
}

func TestDeviationApprovalWithoutFilesAddsNothing(t *testing.T) {
	e, r := e5Engine(t, "continue")
	ctx := context.Background()
	s, _ := e.Advance(ctx, "r")
	r.changed = []string{"b.go", "x.go", "y.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusGate || s.Gate.Gate != GateDeviation {
		t.Fatalf("want deviation gate, got %+v", s)
	}
	if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash, ApprovedFiles: []string{"y.go"}}); err != nil {
		t.Fatal(err)
	}
	_, _ = e.Advance(ctx, "r")
	if len(r.commits) != 1 {
		t.Fatalf("want one commit, got %+v", r.commits)
	}
	c := r.commits[0]
	if !slices.Equal(c.Allowed, []string{"b.go", "y.go"}) || !slices.Contains(c.Byproducts, "x.go") {
		t.Fatalf("x.go was not approved: it stays out of Allowed and goes to Byproducts: %+v", c)
	}
	s, _ = e.Advance(ctx, "r")
	r.changed = []string{"c.go", "x.go", "z.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusGate || s.Gate.Gate != GateDeviation {
		t.Fatalf("want deviation gate for step 3, got %+v", s)
	}
	_ = e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	_, _ = e.Advance(ctx, "r")
	c = r.commits[len(r.commits)-1]
	if slices.Contains(c.Allowed, "z.go") || slices.Contains(c.Allowed, "x.go") || !slices.Contains(c.Byproducts, "z.go") {
		t.Fatalf("an empty ApprovedFiles adds nothing: %+v", c)
	}
}

func TestLoadOverForms(t *testing.T) {
	for over, ok := range map[string]bool{
		"steps": true, "perspectives": true, "perspectives(from=pick)": true, "findings": true,
		"items[]": true, "findings[autofix=true]": true, "items[n=3]": true, `items[s="a"]`: true,
		"items": false, "items[=x]": false, "items[a=]": false, "Items[]": false,
	} {
		if validOver(over) != ok {
			t.Errorf("validOver(%q) = %v", over, !ok)
		}
	}
	if d, _ := parseDataOver("findings"); d.Data != "findings" || d.Filter {
		t.Fatalf("findings is findings[]: %+v", d)
	}
	if itemInput("findings[autofix=true]") != "finding" || overSource("findings[autofix=true]") != "findings" {
		t.Fatal("a filtered over keeps the data's item name and source")
	}
}
