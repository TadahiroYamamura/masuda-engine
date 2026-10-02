package engine

import (
	"context"
	"testing"
)

type execRunner struct {
	fakeRunner
	cmds      []CommandResult
	runs      int
	questions int
}

func (r *execRunner) RunCommand(context.Context, CommandTask) (CommandResult, error) {
	r.runs++
	if len(r.cmds) == 0 {
		return CommandResult{}, nil
	}
	c := r.cmds[0]
	r.cmds = r.cmds[1:]
	return c, nil
}
func (r *execRunner) OpenQuestion(context.Context, QuestionRequest) error { r.questions++; return nil }

const execTestWF = `version: 1
start: fetch
nodes:
  fetch:
    type: exec
    command: ["/bin/true"]
    outputs: [answers]
    max: 2
    next: {done: ask, failed: end:broken, exhausted: end:bad}
  ask:
    type: question
    questions:
      - {id: scope, text: "?", options: [yes, split]}
    outputs: [selected]
    next: {answered: end}
`

func newExecEngine(t *testing.T, r *execRunner) *Engine {
	t.Helper()
	set, err := Load(mapFS(map[string]string{"workflows/x.yaml": execTestWF}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r.data = map[string][]byte{}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	if err := e.Start(context.Background(), "r", "workflows/x", nil); err != nil {
		t.Fatal(err)
	}
	return e
}

// An exec output that fails its schema re-enters the node and counts toward
// max, like an agent's invalid report.
func TestExecInvalidOutputCountsTowardMax(t *testing.T) {
	bad := CommandResult{Outputs: map[string][]byte{"answers": []byte(`[1]`)}}
	r := &execRunner{cmds: []CommandResult{bad, bad}}
	e := newExecEngine(t, r)
	s, err := e.Advance(context.Background(), "r")
	if err != nil || s.Kind != StatusDone || s.Outcome != "bad" || r.runs != 2 {
		t.Fatalf("want end:bad after 2 runs, got %+v %v runs=%d", s, err, r.runs)
	}
}

func TestAnswerIsCheckedAndStatusIsPendingUntilAdvance(t *testing.T) {
	r := &execRunner{cmds: []CommandResult{{Outputs: map[string][]byte{"answers": []byte(`{"a":"b"}`)}}}}
	e := newExecEngine(t, r)
	ctx := context.Background()
	s, err := e.Advance(ctx, "r")
	if err != nil || s.Kind != StatusQuestion {
		t.Fatalf("want question, got %+v %v", s, err)
	}
	if s2, _ := e.Advance(ctx, "r"); s2.Occurrence != s.Occurrence || r.questions != 1 {
		t.Fatalf("question must open once: questions=%d", r.questions)
	}
	if err := e.Answer(ctx, "r", s.Occurrence, Answer{Answers: map[string]string{"scope": "maybe"}}); err == nil {
		t.Fatal("an answer outside the options must be refused")
	}
	if err := e.Answer(ctx, "r", s.Occurrence, Answer{Answers: map[string]string{"scope": "yes", "x": "y"}}); err == nil {
		t.Fatal("an answer to a question not asked must be refused")
	}
	if err := e.Answer(ctx, "r", s.Occurrence, Answer{Answers: map[string]string{"scope": "yes"}}); err != nil {
		t.Fatal(err)
	}
	if st, err := e.Status(ctx, "r"); err != nil || st.Kind != StatusPending {
		t.Fatalf("want pending after Answer, got %+v %v", st, err)
	}
	if string(r.data[s.Occurrence+"/selected"]) != `{"scope":"yes"}` {
		t.Fatalf("answers stored as %q", r.data[s.Occurrence+"/selected"])
	}
	if s, err = e.Advance(ctx, "r"); err != nil || s.Kind != StatusDone || s.Outcome != "done" {
		t.Fatalf("want done, got %+v %v", s, err)
	}
}
