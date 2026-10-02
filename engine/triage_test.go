package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func triageEngine(t *testing.T, files map[string]string) (*Engine, *fakeRunner) {
	t.Helper()
	set, err := Load(mapFS(files), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r := &fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
		"plan": []byte(`{"summary":"s","steps":[{"number":1,"description":"d","files":["a"]}],"expected_byproducts":[]}`),
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	if err := e.Start(context.Background(), "r", "workflows/x", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	return e, r
}

func mustAdvance(t *testing.T, e *Engine) Status {
	t.Helper()
	s, err := e.Advance(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// raise reports a concern from occ and returns the triage gate it opens.
func raise(t *testing.T, e *Engine, occ, text string) Status {
	t.Helper()
	if err := e.ReportConcern(context.Background(), "r", occ, text); err != nil {
		t.Fatal(err)
	}
	s := mustAdvance(t, e)
	if s.Kind != StatusGate || s.Gate.Gate != GateTriage || string(s.Gate.Subject) != text || s.Gate.Target != "" {
		t.Fatalf("want triage gate on %q, got %+v", text, s)
	}
	return s
}

const triageWF = "version: 1\ninputs: [instructions]\nstart: sub\nnodes:\n" +
	"  sub: {type: workflow, workflow: workflows/sub, next: {done: ok, given_up: end:given_up}}\n" +
	"  ok: {type: approval, gate: plan, target: plan, next: {approved: end, rejected: sub}}\n"

const triageSub = "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
	"  plan: {type: agent, role: agents/planner, inputs: [instructions], max: 1, next: {done: end, exhausted: end:given_up}}\n"

func triageFiles() map[string]string {
	return map[string]string{
		"agents/planner.md":  agentDef("planner", "Read", []string{"plan"}),
		"workflows/x.yaml":   triageWF,
		"workflows/sub.yaml": triageSub,
	}
}

// redo and dismiss of an interrupted agent enter it again (inside the
// called workflow) without using up its max of 1; halt stops the run with
// the concern as the reason.
func TestTriageReentersWithoutCountingAndHaltBlocks(t *testing.T) {
	e, _ := triageEngine(t, triageFiles())
	ctx := context.Background()
	s := mustAdvance(t, e)
	g := raise(t, e, s.Occurrence, "curl a token")
	if err := e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "approved", TargetHash: g.Gate.TargetHash}); err == nil {
		t.Fatal("triage takes dismiss, halt or redo only")
	}
	if err := e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "redo", Comment: "do not fetch secrets"}); err != nil {
		t.Fatal(err)
	}
	s2 := mustAdvance(t, e)
	if s2.Kind != StatusAgent || s2.Occurrence == s.Occurrence || s2.Task.Workflow != "workflows/sub" || s2.Task.Feedback != "do not fetch secrets" {
		t.Fatalf("redo must re-enter the planner with the comment, got %+v", s2)
	}
	g = raise(t, e, s2.Occurrence, "again")
	_ = e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "dismiss"})
	s3 := mustAdvance(t, e)
	if s3.Kind != StatusAgent || s3.Occurrence == s2.Occurrence || s3.Task.Feedback != s2.Task.Feedback {
		t.Fatalf("dismiss must re-enter with the same feedback, got %+v", s3)
	}
	g = raise(t, e, s3.Occurrence, "third")
	_ = e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "halt", Comment: "stop"})
	if s4 := mustAdvance(t, e); s4.Kind != StatusBlocked || !strings.Contains(s4.Reason, "third") {
		t.Fatalf("halt must block with the concern, got %+v", s4)
	}
}

// A concern that arrives after the node reported is triaged first; dismiss
// then lets the reported result stand instead of redoing the work.
func TestTriageDismissKeepsAFinishedResult(t *testing.T) {
	e, r := triageEngine(t, triageFiles())
	ctx := context.Background()
	s := mustAdvance(t, e)
	if err := e.ReportResult(ctx, "r", s.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	g := raise(t, e, s.Occurrence, "late")
	if err := e.ReportResult(ctx, "r", s.Occurrence, "done", ""); err == nil {
		t.Fatal("nothing but the triage decision is taken while the gate is open")
	}
	_ = e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "dismiss"})
	if s2 := mustAdvance(t, e); s2.Kind != StatusGate || s2.Gate.Gate != "plan" {
		t.Fatalf("dismiss after the result must continue to the plan gate, got %+v", s2)
	}
	kinds := map[string]bool{}
	for _, ev := range r.events {
		kinds[ev.Kind] = true
	}
	for _, k := range []string{"concern", "gate-open", "decision", "triage"} {
		if !kinds[k] {
			t.Fatalf("log lacks %q: %v", k, kinds)
		}
	}
}

func TestRoleQuestionCollectsAnswersUntilDone(t *testing.T) {
	e, r := triageEngine(t, map[string]string{
		"agents/asker.md": agentDef("asker", "Read", nil),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: ask\nnodes:\n" +
			"  ask: {type: question, role: agents/asker, outputs: [answers], next: {answered: end}}\n",
	})
	ctx := context.Background()
	s := mustAdvance(t, e)
	if s.Kind != StatusAgent || s.Task.Agent.Name != "asker" {
		t.Fatalf("want the asker's task, got %+v", s)
	}
	if err := e.ReportResult(ctx, "r", s.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	s2 := mustAdvance(t, e)
	if s2.Kind != StatusAgent || s2.Occurrence == s.Occurrence || !strings.Contains(s2.Task.Feedback, "答え") {
		t.Fatalf("done without answers must be sent back, got %+v", s2)
	}
	for _, a := range []map[string]string{{"scope": "split", "lang": "go"}, {"scope": "yes"}} {
		if err := e.Answer(ctx, "r", s2.Occurrence, Answer{Answers: a}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.ReportResult(ctx, "r", s2.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	if s3 := mustAdvance(t, e); s3.Kind != StatusDone || s3.Outcome != "done" {
		t.Fatalf("want done through answered, got %+v", s3)
	}
	var got map[string]string
	if err := json.Unmarshal(r.data[s2.Occurrence+"/answers"], &got); err != nil || got["scope"] != "yes" || got["lang"] != "go" {
		t.Fatalf("answers must merge with the later one winning: %s", r.data[s2.Occurrence+"/answers"])
	}
}

func publishEngine(t *testing.T, nodes string) (*Engine, *e5Runner, Status) {
	t.Helper()
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": agentDef("planner", "Read", []string{"plan"}),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
			"  plan: {type: agent, role: agents/planner, inputs: [instructions], next: approve}\n" +
			"  approve: {type: approval, gate: plan, target: plan, next: {approved: c, rejected: plan}}\n" + nodes +
			"  pub: {type: publish, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r := &e5Runner{fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{"plan": []byte(e5Plan)}}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", []string{"instructions"})
	s, _ := e.Advance(ctx, "r")
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	_ = e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	s, err = e.Advance(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	return e, r, s
}

func TestPublishNeedsTheReviewedCommit(t *testing.T) {
	_, r, s := publishEngine(t, "  c: {type: commit, scope: plan, next: {done: pub, rejected: pub}}\n")
	if s.Kind != StatusBlocked || !strings.Contains(s.Reason, "承認されたコミットが無い") || len(r.commits) != 1 {
		t.Fatalf("publish without a reviewed commit must block, got %+v", s)
	}

	e, r, s := publishEngine(t, "  c: {type: commit, scope: plan, next: {done: review, rejected: review}}\n"+
		"  review: {type: approval, gate: review, target: diff, next: {approved: c2, rejected: plan}}\n"+
		"  c2: {type: commit, scope: plan, next: {done: pub, rejected: pub}}\n")
	if s.Kind != StatusGate || s.Gate.Gate != "review" {
		t.Fatalf("want review gate, got %+v", s)
	}
	_ = e.Decide(context.Background(), "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	if s = mustAdvance(t, e); s.Kind != StatusBlocked || !strings.Contains(s.Reason, "承認後にコミットが進んだ") || len(r.commits) != 2 {
		t.Fatalf("a commit after the review must block publish, got %+v", s)
	}
}

// superseded reports whether occ's gate was closed as superseded, both in
// the log and in the store.
func superseded(t *testing.T, e *Engine, r *fakeRunner, occ, key string) {
	t.Helper()
	found := false
	for _, ev := range r.events {
		if ev.Kind == "decision" && ev.Occurrence == occ && ev.Outcome == outcomeSuperseded {
			if found {
				t.Fatalf("gate of %s closed twice: %+v", occ, r.events)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no superseded decision for %s: %+v", occ, r.events)
	}
	var d Decision
	if err := e.getJSON("r", key, &d); err != nil || d.Outcome != outcomeSuperseded {
		t.Fatalf("%s: %+v %v", key, d, err)
	}
}

// An approval gate the triage interrupted is closed when the run enters
// the approval again, and only the new gate is shown.
func TestTriageSupersedesAnInterruptedApprovalGate(t *testing.T) {
	e, r := triageEngine(t, triageFiles())
	ctx := context.Background()
	s := mustAdvance(t, e)
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	gate := mustAdvance(t, e)
	if gate.Kind != StatusGate || gate.Gate.Gate != "plan" {
		t.Fatalf("want plan gate, got %+v", gate)
	}
	g := raise(t, e, s.Occurrence, "late")
	if err := e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "dismiss"}); err != nil {
		t.Fatal(err)
	}
	superseded(t, e, r, gate.Occurrence, prefGateClosed+gate.Occurrence)
	s2 := mustAdvance(t, e)
	if s2.Kind != StatusGate || s2.Gate.Gate != "plan" || s2.Occurrence == gate.Occurrence {
		t.Fatalf("want a new plan gate, got %+v", s2)
	}
	if st, _ := e.Status(ctx, "r"); st.Occurrence != s2.Occurrence {
		t.Fatalf("Status must show the new gate only: %+v", st)
	}
	superseded(t, e, r, gate.Occurrence, prefGateClosed+gate.Occurrence)
	if err := e.Decide(ctx, "r", gate.Occurrence, Decision{Outcome: "approved", TargetHash: gate.Gate.TargetHash}); err == nil {
		t.Fatal("a superseded gate takes no decision")
	}
}

// A deviation gate the triage interrupted is closed on redo.
func TestTriageSupersedesAnInterruptedDeviationGate(t *testing.T) {
	e, r := triageEngine(t, triageFiles())
	ctx := context.Background()
	s := mustAdvance(t, e)
	r.changed = []string{"sneaky.go"}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	dev := mustAdvance(t, e)
	if dev.Kind != StatusGate || dev.Gate.Gate != GateDeviation {
		t.Fatalf("want deviation gate, got %+v", dev)
	}
	g := raise(t, e, s.Occurrence, "it wrote a file")
	if err := e.Decide(ctx, "r", g.Occurrence, Decision{Outcome: "redo"}); err != nil {
		t.Fatal(err)
	}
	superseded(t, e, r, dev.Occurrence, prefDevDec+devKey(dev.Occurrence, 1))
	if s2 := mustAdvance(t, e); s2.Kind != StatusAgent || s2.Occurrence == s.Occurrence {
		t.Fatalf("redo must enter the planner again, got %+v", s2)
	}
}

// The commit's deviation check and the review gate's unpublished list both
// compare with the branch head (an empty base), not with a snapshot.
func TestCommitAndReviewGateCompareWithTheBranchHead(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": agentDef("planner", "Read", []string{"plan"}),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
			"  plan: {type: agent, role: agents/planner, inputs: [instructions], next: approve}\n" +
			"  approve: {type: approval, gate: plan, target: plan, next: {approved: c, rejected: plan}}\n" +
			"  c: {type: commit, scope: plan, next: {done: review, rejected: review}}\n" +
			"  review: {type: approval, gate: review, target: diff, next: {approved: pub, rejected: plan}}\n" +
			"  pub: {type: publish, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	r := &e5Runner{fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{"plan": []byte(e5Plan)}},
		changed: []string{"a.go"}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	_ = e.Start(ctx, "r", "workflows/x", []string{"instructions"})
	s := mustAdvance(t, e)
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s = mustAdvance(t, e)
	_ = e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	n := len(r.since)
	s = mustAdvance(t, e)
	if s.Kind != StatusGate || s.Gate.Gate != "review" || len(r.commits) != 1 {
		t.Fatalf("want review gate after one commit, got %+v", s)
	}
	if got := r.since[n:]; len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Fatalf("commit and review gate must call ChangedSince with an empty base, got %q", got)
	}
	if subj := string(s.Gate.Subject); !strings.HasPrefix(subj, "diff") || !strings.Contains(subj, "publishされない変更") || !strings.Contains(subj, "a.go") {
		t.Fatalf("subject: %s", subj)
	}
}
